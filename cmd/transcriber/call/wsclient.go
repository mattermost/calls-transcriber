package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// Window during which we keep trying to resume a dropped connection before
	// giving up and terminating the job.
	wsReconnectWindow      = 30 * time.Second
	wsMinReconnectInterval = time.Second
	wsReconnectJitter      = 500 * time.Millisecond
	wsHelloTimeout         = 10 * time.Second

	wsOutboundBuffer = 256
)

// callsWSClient is a reconnecting Mattermost-calls WebSocket client built on top
// of model.WebSocketClient. It owns the connect/join/reconnect lifecycle and
// exposes a single stream of plugin events plus a concurrency-safe send.
//
// It is the v2 equivalent of the WebSocket half of rtcd/client (the reconnect +
// resume logic is not media-stack specific), reimplemented on the upstream
// client so we don't depend on rtcd. The bare model.WebSocketClient does not
// reconnect, is not safe for concurrent sends (it mutates an unsynchronized
// sequence counter), and closes its internal write channel on disconnect (so a
// send racing a drop can panic); this type handles all three.
type callsWSClient struct {
	wsURL     string
	authToken string
	channelID string
	jobID     string

	// mu guards ws and serializes SendMessage (which is not concurrency-safe).
	mu sync.Mutex
	ws *model.WebSocketClient

	// Connection bookkeeping for resume. Only touched by the run goroutine after
	// the initial Connect.
	originalConnID string
	currentConnID  string
	lastSeq        int

	events    chan *model.WebSocketEvent
	closeCh   chan struct{}
	closeOnce sync.Once
}

func newCallsWSClient(siteURL, authToken, channelID, jobID string) *callsWSClient {
	wsURL := strings.Replace(siteURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	return &callsWSClient{
		wsURL:     wsURL,
		authToken: authToken,
		channelID: channelID,
		jobID:     jobID,
		events:    make(chan *model.WebSocketEvent, 64),
		closeCh:   make(chan struct{}),
	}
}

// Connect dials the websocket, waits for the hello event, and sends the
// job-gated join. It returns the connection ID (which doubles as the bot's call
// session ID) and starts the background run loop. The returned connID is stable
// across reconnects (resume reuses the original connection ID).
func (c *callsWSClient) Connect(ctx context.Context) (string, error) {
	ws, connID, err := c.dial(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.ws = ws
	c.originalConnID = connID
	c.currentConnID = connID
	c.mu.Unlock()

	if err := c.Send(wsEventJoin, map[string]any{
		"channelID": c.channelID,
		"jobID":     c.jobID,
	}); err != nil {
		ws.Close()
		return "", fmt.Errorf("failed to send join: %w", err)
	}

	go c.run()
	return connID, nil
}

// dial opens a websocket and returns it once the hello event is received. If
// originalConnID is set it resumes that connection from lastSeq so the server
// replays missed events; otherwise it opens a fresh connection.
func (c *callsWSClient) dial(ctx context.Context) (*model.WebSocketClient, string, error) {
	var ws *model.WebSocketClient
	var err error
	if c.originalConnID == "" {
		ws, err = model.NewWebSocketClient4WithDialer(websocket.DefaultDialer, c.wsURL, c.authToken)
	} else {
		ws, err = model.NewReliableWebSocketClientWithDialer(websocket.DefaultDialer, c.wsURL, c.authToken, c.originalConnID, c.lastSeq, false)
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to create websocket client: %w", err)
	}
	ws.Listen()

	timeout := time.NewTimer(wsHelloTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			ws.Close()
			return nil, "", ctx.Err()
		case <-c.closeCh:
			ws.Close()
			return nil, "", fmt.Errorf("client closed")
		case <-timeout.C:
			ws.Close()
			return nil, "", fmt.Errorf("timed out waiting for hello")
		case ev, ok := <-ws.EventChannel:
			if !ok {
				return nil, "", fmt.Errorf("websocket closed before hello")
			}
			if s := int(ev.GetSequence()); s > c.lastSeq {
				c.lastSeq = s
			}
			if ev.EventType() == model.WebsocketEventHello {
				connID, _ := ev.GetData()["connection_id"].(string)
				if connID == "" {
					ws.Close()
					return nil, "", fmt.Errorf("hello missing connection_id")
				}
				return ws, connID, nil
			}
		// The other channels must be drained or the client's reader goroutine
		// blocks (see the WebSocketClient doc comment).
		case <-ws.ResponseChannel:
		case <-ws.PingTimeoutChannel:
		}
	}
}

// run consumes the active connection and reconnects on drops until the window
// is exhausted or the client is closed. It closes the events channel on exit.
func (c *callsWSClient) run() {
	defer close(c.events)
	for {
		c.mu.Lock()
		ws := c.ws
		c.mu.Unlock()

		if !c.consume(ws) {
			return // intentional close
		}

		slog.Warn("calls websocket dropped, attempting to reconnect")
		if !c.reconnect() {
			slog.Error("calls websocket reconnect failed, giving up")
			return
		}
		slog.Info("calls websocket reconnected", slog.String("connID", c.currentConnID))
	}
}

// consume reads from the connection until it drops (returns true) or the client
// is closed (returns false).
func (c *callsWSClient) consume(ws *model.WebSocketClient) (dropped bool) {
	for {
		select {
		case <-c.closeCh:
			return false
		case ev, ok := <-ws.EventChannel:
			if !ok {
				return true
			}
			if s := int(ev.GetSequence()); s > c.lastSeq {
				c.lastSeq = s
			}
			// hello is consumed at (re)dial time; don't surface it.
			if ev.EventType() == model.WebsocketEventHello {
				continue
			}
			select {
			case c.events <- ev:
			case <-c.closeCh:
				return false
			}
		case _, ok := <-ws.ResponseChannel:
			if !ok {
				return true
			}
		case <-ws.PingTimeoutChannel:
			slog.Warn("calls websocket ping timeout")
		}
	}
}

// reconnect tries to resume the connection within the reconnect window, sending
// the reconnect message (rather than a fresh join) so the call session is
// resumed instead of duplicated. Returns true once reconnected.
func (c *callsWSClient) reconnect() bool {
	prevConnID := c.currentConnID
	start := time.Now()
	var interval time.Duration
	for time.Since(start) < wsReconnectWindow {
		interval += wsMinReconnectInterval + time.Duration(rand.Int63n(wsReconnectJitter.Milliseconds()))*time.Millisecond
		select {
		case <-c.closeCh:
			return false
		case <-time.After(interval):
		}

		ctx, cancel := context.WithTimeout(context.Background(), wsHelloTimeout)
		ws, connID, err := c.dial(ctx)
		cancel()
		if err != nil {
			slog.Warn("calls websocket reconnect attempt failed", slog.String("err", err.Error()))
			continue
		}

		c.mu.Lock()
		c.ws = ws
		c.currentConnID = connID
		c.mu.Unlock()

		if err := c.Send(wsEventReconnect, map[string]any{
			"channelID":      c.channelID,
			"originalConnID": c.originalConnID,
			"prevConnID":     prevConnID,
		}); err != nil {
			slog.Warn("failed to send reconnect message", slog.String("err", err.Error()))
			continue
		}
		return true
	}
	return false
}

// Send marshals msg to the map[string]any payload SendMessage expects and sends
// it on the current connection. It is safe for concurrent use.
func (c *callsWSClient) Send(action string, msg any) (retErr error) {
	var data map[string]any
	switch m := msg.(type) {
	case nil:
	case map[string]any:
		data = m
	default:
		b, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("failed to marshal ws message (%s): %w", action, err)
		}
		if err := json.Unmarshal(b, &data); err != nil {
			return fmt.Errorf("failed to unmarshal ws message (%s): %w", action, err)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ws == nil {
		return fmt.Errorf("websocket not connected")
	}
	// SendMessage panics if the connection dropped and closed its write channel
	// between our nil check and the send; recover and surface it as an error.
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("failed to send ws message (%s): connection closed (%v)", action, r)
		}
	}()
	c.ws.SendMessage(action, data)
	return nil
}

// Events returns the stream of plugin websocket events. It is closed when the
// client is closed or the connection is permanently lost.
func (c *callsWSClient) Events() <-chan *model.WebSocketEvent {
	return c.events
}

// Close permanently shuts down the client.
func (c *callsWSClient) Close() {
	c.closeOnce.Do(func() {
		close(c.closeCh)
		c.mu.Lock()
		if c.ws != nil {
			c.ws.Close()
		}
		c.mu.Unlock()
	})
}
