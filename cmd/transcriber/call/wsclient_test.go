package call

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsTestServer runs a minimal Mattermost-protocol WebSocket server for testing.
// Each accepted connection calls handler(conn, dialCount) so tests can control
// per-connection behaviour (send hello, drop, accept reconnect, etc.).
type wsTestServer struct {
	ts      *httptest.Server
	handler func(conn *websocket.Conn, dialCount int)
	dials   atomic.Int32
}

func newWSTestServer(t *testing.T, handler func(conn *websocket.Conn, dialCount int)) *wsTestServer {
	t.Helper()
	s := &wsTestServer{handler: handler}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		n := int(s.dials.Add(1))
		s.handler(conn, n)
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *wsTestServer) siteURL() string {
	return s.ts.URL
}

// sendHello writes a Mattermost hello event with the given connection ID.
func sendHello(t *testing.T, conn *websocket.Conn, connID string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"event": "hello",
		"data":  map[string]any{"connection_id": connID},
		"seq":   1,
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, b))
}

// drainConn reads and discards all messages until the connection closes.
func drainConn(conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func newTestClient(t *testing.T, siteURL string) *callsWSClient {
	t.Helper()
	return newCallsWSClient(siteURL, "test-token", "test-channel", "test-job")
}

func TestWSClientConnect(t *testing.T) {
	srv := newWSTestServer(t, func(conn *websocket.Conn, _ int) {
		sendHello(t, conn, "conn-1")
		drainConn(conn)
	})

	c := newTestClient(t, srv.siteURL())
	connID, err := c.Connect(context.Background())
	require.NoError(t, err)
	require.Equal(t, "conn-1", connID)
	c.Close()
}

func TestWSClientConnectTwiceFails(t *testing.T) {
	srv := newWSTestServer(t, func(conn *websocket.Conn, _ int) {
		sendHello(t, conn, "conn-1")
		drainConn(conn)
	})

	c := newTestClient(t, srv.siteURL())
	_, err := c.Connect(context.Background())
	require.NoError(t, err)

	_, err = c.Connect(context.Background())
	require.EqualError(t, err, "already connected")
	c.Close()
}

func TestWSClientReconnect(t *testing.T) {
	wsReconnectWindow = 2 * time.Second
	wsMinReconnectInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		wsReconnectWindow = 30 * time.Second
		wsMinReconnectInterval = time.Second
	})

	// dial 1: send hello then close to trigger reconnect
	// dial 2: send hello for reconnected session
	srv := newWSTestServer(t, func(conn *websocket.Conn, dialCount int) {
		switch dialCount {
		case 1:
			sendHello(t, conn, "conn-1")
			// drain the join message then close to trigger reconnect
			conn.ReadMessage()
			conn.Close()
		case 2:
			sendHello(t, conn, "conn-2")
			drainConn(conn)
		}
	})

	c := newTestClient(t, srv.siteURL())
	connID, err := c.Connect(context.Background())
	require.NoError(t, err)
	require.Equal(t, "conn-1", connID)

	// wait for reconnect and verify we receive an event on the second connection
	// by sending one from the test server after reconnect
	// give reconnect time to complete
	time.Sleep(200 * time.Millisecond)

	require.Equal(t, int32(2), srv.dials.Load())
	require.False(t, c.IsClosed())
	c.Close()
}

func TestWSClientReconnectWindowExhausted(t *testing.T) {
	wsReconnectWindow = 100 * time.Millisecond
	wsMinReconnectInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		wsReconnectWindow = 30 * time.Second
		wsMinReconnectInterval = time.Second
	})

	// Always close immediately after hello — reconnect will never succeed.
	srv := newWSTestServer(t, func(conn *websocket.Conn, dialCount int) {
		if dialCount == 1 {
			sendHello(t, conn, "conn-1")
			conn.ReadMessage() // drain join
		}
		conn.Close()
	})

	c := newTestClient(t, srv.siteURL())
	_, err := c.Connect(context.Background())
	require.NoError(t, err)

	// events channel should close once the reconnect window is exhausted
	select {
	case _, ok := <-c.Events():
		require.False(t, ok, "events channel should be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for events channel to close")
	}

	// IsClosed returns false — the client gave up, we didn't call Close()
	require.False(t, c.IsClosed())
}

func TestWSClientCloseStopsReconnect(t *testing.T) {
	wsReconnectWindow = 5 * time.Second
	wsMinReconnectInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		wsReconnectWindow = 30 * time.Second
		wsMinReconnectInterval = time.Second
	})

	// Always fail reconnect attempts so the client loops until Close() is called.
	srv := newWSTestServer(t, func(conn *websocket.Conn, dialCount int) {
		if dialCount == 1 {
			sendHello(t, conn, "conn-1")
			conn.ReadMessage() // drain join
		}
		conn.Close()
	})

	c := newTestClient(t, srv.siteURL())
	_, err := c.Connect(context.Background())
	require.NoError(t, err)

	// close the client while it's in the reconnect loop
	go func() {
		time.Sleep(50 * time.Millisecond)
		c.Close()
	}()

	select {
	case _, ok := <-c.Events():
		require.False(t, ok, "events channel should be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for events channel to close")
	}

	// IsClosed returns true — we called Close()
	require.True(t, c.IsClosed())
}

func TestWSClientSend(t *testing.T) {
	received := make(chan map[string]any, 1)

	srv := newWSTestServer(t, func(conn *websocket.Conn, _ int) {
		sendHello(t, conn, "conn-1")
		// read join, then read the test message
		conn.ReadMessage()
		_, msg, err := conn.ReadMessage()
		if err == nil {
			var m map[string]any
			if json.Unmarshal(msg, &m) == nil {
				received <- m
			}
		}
		drainConn(conn)
	})

	c := newTestClient(t, srv.siteURL())
	_, err := c.Connect(context.Background())
	require.NoError(t, err)

	err = c.Send("test_action", map[string]any{"key": "value"})
	require.NoError(t, err)

	select {
	case msg := <-received:
		require.Equal(t, "test_action", msg["action"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sent message")
	}

	c.Close()
}

func TestWSClientConnectContextCancelled(t *testing.T) {
	// Server never sends hello — connect should respect context cancellation.
	srv := newWSTestServer(t, func(conn *websocket.Conn, _ int) {
		drainConn(conn)
	})

	c := newTestClient(t, srv.siteURL())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Connect(ctx)
	require.Error(t, err)
}
