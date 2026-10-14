package call

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/stretchr/testify/require"
)

const (
	testCallID  = "8w8jorhr7j83uqr6y1st894hqe"
	testJobID   = "67t5u6cmtfbb7jug739d43xa9e"
	testToken   = "qj75unbsef83ik9p7ueypb6iyw"
	joinPath    = "/plugins/com.mattermost.calls/livekit-token"
	statusPath  = "/plugins/com.mattermost.calls/bot/calls/" + testCallID + "/jobs/" + testJobID + "/status"
	joinOKBody  = `{"session_id":"sid","token":"lk-token","url":"ws://livekit.test"}`
	startedBody = `{"session_id":"sid","token":"lk-token","url":"ws://livekit.test","call_state":{"recording":{"start_at":1700000000000}}}`
)

// pluginServer fakes the plugin endpoints the transcriber calls when starting.
type pluginServer struct {
	joinStatus   int
	joinBody     string
	statusStatus int

	mu          sync.Mutex
	joinReqs    []map[string]string
	joinAuth    string
	statusCalls int
}

func (p *pluginServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case joinPath:
		var req map[string]string
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		p.mu.Lock()
		if r.Method == http.MethodPost {
			p.joinReqs = append(p.joinReqs, req)
		}
		p.joinAuth = r.Header.Get("Authorization")
		status, respBody := p.joinStatus, p.joinBody
		p.mu.Unlock()

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(status)
		fmt.Fprint(w, respBody)
	case statusPath:
		p.mu.Lock()
		p.statusCalls++
		status := p.statusStatus
		p.mu.Unlock()
		w.WriteHeader(status)
		fmt.Fprint(w, `{"message": "status error"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *pluginServer) statusCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusCalls
}

func newPluginServer(t *testing.T, joinStatus int, joinBody string) (*pluginServer, *Transcriber) {
	t.Helper()

	ps := &pluginServer{joinStatus: joinStatus, joinBody: joinBody, statusStatus: http.StatusOK}
	ts := httptest.NewServer(ps)
	t.Cleanup(ts.Close)

	cfg := config.CallTranscriberConfig{
		SiteURL:         ts.URL,
		CallID:          testCallID,
		PostID:          "udzdsg7dwidbzcidx5khrf8nee",
		TranscriptionID: testJobID,
		AuthToken:       testToken,
		NumThreads:      1,
		ModelSize:       config.ModelSizeTiny,
	}
	cfg.SetDefaults()

	tr, err := NewTranscriber(cfg, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, tr)

	return ps, tr
}

func TestCreateLiveKitSessionAPI(t *testing.T) {
	t.Run("sends the bot token, channel and job ID", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, joinOKBody)

		session, err := tr.createLiveKitSession(context.Background())
		require.NoError(t, err)
		require.Equal(t, "lk-token", session.Token)

		ps.mu.Lock()
		defer ps.mu.Unlock()
		require.Equal(t, []map[string]string{{"channel_id": testCallID, "job_id": testJobID}}, ps.joinReqs)
		require.Contains(t, ps.joinAuth, testToken)
	})

	tcs := []struct {
		name   string
		status int
		body   string
		errMsg string
	}{
		{
			name:   "job not in progress",
			status: http.StatusForbidden,
			body:   `{"message": "job not in progress or already started"}`,
			errMsg: "failed to create livekit session: job not in progress or already started",
		},
		{
			name:   "unauthorized",
			status: http.StatusUnauthorized,
			body:   `{"message": "unauthorized"}`,
			errMsg: "failed to create livekit session: unauthorized",
		},
		{
			name:   "server error",
			status: http.StatusInternalServerError,
			body:   `{"message": "Internal server error"}`,
			errMsg: "failed to create livekit session: Internal server error",
		},
		{
			name:   "malformed response",
			status: http.StatusOK,
			body:   `{not json`,
			errMsg: "failed to decode livekit session response",
		},
		{
			name:   "empty response",
			status: http.StatusOK,
			body:   `{}`,
			errMsg: "invalid livekit session response: empty url or token",
		},
		{
			name:   "missing url",
			status: http.StatusOK,
			body:   `{"session_id":"sid","token":"lk-token"}`,
			errMsg: "invalid livekit session response: empty url or token",
		},
		{
			name:   "missing token",
			status: http.StatusOK,
			body:   `{"session_id":"sid","url":"ws://livekit.test"}`,
			errMsg: "invalid livekit session response: empty url or token",
		},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			_, tr := newPluginServer(t, tc.status, tc.body)

			session, err := tr.createLiveKitSession(context.Background())
			require.Nil(t, session)
			require.ErrorContains(t, err, tc.errMsg)
		})
	}

	t.Run("cancelled context", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusOK, joinOKBody)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		session, err := tr.createLiveKitSession(ctx)
		require.Nil(t, session)
		require.ErrorContains(t, err, "failed to create livekit session")
	})
}

// fakeConnector replaces connecting to a LiveKit room and records how it was
// called.
type fakeConnector struct {
	room *fakeRoom
	err  error

	mu    sync.Mutex
	calls int
	url   string
	token string
	cb    *lksdk.RoomCallback
}

func (f *fakeConnector) connect(url, token string, cb *lksdk.RoomCallback) (roomConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.url, f.token, f.cb = url, token, cb
	if f.err != nil {
		return nil, f.err
	}
	return f.room, nil
}

func (f *fakeConnector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeConnector) callback() *lksdk.RoomCallback {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cb
}

func runStart(ctx context.Context, tr *Transcriber) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- tr.Start(ctx)
	}()
	return ch
}

func requireStartResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Start to return")
		return nil
	}
}

func isDone(tr *Transcriber) bool {
	select {
	case <-tr.doneCh:
		return true
	default:
		return false
	}
}

func TestStart(t *testing.T) {
	t.Run("fails without connecting or finishing when the join request fails", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusForbidden, `{"message": "job not in progress or already started"}`)
		fc := &fakeConnector{room: &fakeRoom{}}
		tr.connectToRoom = fc.connect

		err := tr.Start(context.Background())
		require.ErrorContains(t, err, "job not in progress or already started")
		require.Zero(t, fc.callCount())
		require.False(t, isDone(tr), "done should not have been called")
	})

	t.Run("finishes when connecting to the room fails", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, joinOKBody)
		fc := &fakeConnector{err: fmt.Errorf("boom")}
		tr.connectToRoom = fc.connect

		err := tr.Start(context.Background())
		require.EqualError(t, err, "failed to connect to livekit room: boom")
		require.Equal(t, 1, fc.callCount())
		require.True(t, isDone(tr), "done should have been called")
		require.Zero(t, ps.statusCallCount())
	})

	t.Run("connects with the token and url from the join response", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusOK, startedBody)
		fc := &fakeConnector{room: &fakeRoom{}}
		tr.connectToRoom = fc.connect

		require.NoError(t, tr.Start(context.Background()))
		require.Equal(t, "ws://livekit.test", fc.url)
		require.Equal(t, "lk-token", fc.token)
	})

	t.Run("starts when the recording is already running in the join response", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, startedBody)
		room := &fakeRoom{}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		require.NoError(t, tr.Start(context.Background()))
		require.Equal(t, time.UnixMilli(1700000000000), *tr.startTime.Load())
		require.Equal(t, 1, ps.statusCallCount(), "started should be reported once")
		require.Zero(t, room.disconnects.Load())
		require.False(t, isDone(tr))
	})

	t.Run("starts when the recording is running in the room metadata", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, joinOKBody)
		room := &fakeRoom{metadata: `{"recording":{"start_at":1700000000000}}`}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		require.NoError(t, tr.Start(context.Background()))
		require.Equal(t, time.UnixMilli(1700000000000), *tr.startTime.Load())
		require.Equal(t, 1, ps.statusCallCount())
	})

	t.Run("waits for the recording to start", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, joinOKBody)
		room := &fakeRoom{metadata: `{"host_id":"h"}`}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		ch := runStart(context.Background(), tr)
		require.Eventually(t, func() bool { return fc.callback() != nil }, 2*time.Second, 10*time.Millisecond)

		select {
		case err := <-ch:
			t.Fatalf("Start returned before the recording started: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		require.Zero(t, ps.statusCallCount())

		fc.callback().OnRoomMetadataChanged(`{"recording":{"start_at":1700000000000}}`)

		require.NoError(t, requireStartResult(t, ch))
		require.Equal(t, time.UnixMilli(1700000000000), *tr.startTime.Load())
		require.Equal(t, 1, ps.statusCallCount())
	})

	t.Run("publishes captions on the caption topic when live captions are on", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusOK, startedBody)
		room := &fakeRoom{}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect
		tr.cfg.LiveCaptionsOn = true
		tr.cfg.LiveCaptionsNumTranscribers = 0 // don't start the transcriber pool

		require.NoError(t, tr.Start(context.Background()))

		tr.roomMu.Lock()
		publish := tr.publishData
		tr.roomMu.Unlock()
		require.NotNil(t, publish)

		require.NoError(t, publish([]byte(`{"session_id":"sid","text":"hello"}`)))
		require.Equal(t, []publishedData{{
			payload:  []byte(`{"session_id":"sid","text":"hello"}`),
			topic:    "caption",
			reliable: false,
		}}, room.publishedData())
	})

	t.Run("does not set up caption publishing when live captions are off", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusOK, startedBody)
		fc := &fakeConnector{room: &fakeRoom{}}
		tr.connectToRoom = fc.connect

		require.NoError(t, tr.Start(context.Background()))

		tr.roomMu.Lock()
		defer tr.roomMu.Unlock()
		require.Nil(t, tr.publishData)
	})

	t.Run("disconnects the room and finishes when reporting started fails", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, startedBody)
		ps.statusStatus = http.StatusInternalServerError

		// The status request is retried; don't wait for the real delay.
		origDelay := postJobStatusRetryDelay
		postJobStatusRetryDelay = time.Millisecond
		t.Cleanup(func() { postJobStatusRetryDelay = origDelay })

		room := &fakeRoom{}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		err := tr.Start(context.Background())
		require.ErrorContains(t, err, "failed to report job started status")
		require.NotErrorIs(t, err, ErrStoppedBeforeStart)
		require.Equal(t, 1+postJobStatusMaxRetries, ps.statusCallCount())
		require.Equal(t, int32(1), room.disconnects.Load())
		require.True(t, isDone(tr))
	})

	t.Run("disconnects the room and finishes when the context is cancelled", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusOK, joinOKBody)
		room := &fakeRoom{}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := runStart(ctx, tr)
		require.Eventually(t, func() bool { return fc.callback() != nil }, 2*time.Second, 10*time.Millisecond)
		cancel()

		err := requireStartResult(t, ch)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrStoppedBeforeStart)
		require.Equal(t, int32(1), room.disconnects.Load())
		require.True(t, isDone(tr))
	})

	t.Run("finishes cleanly when the bot is removed before the recording starts", func(t *testing.T) {
		ps, tr := newPluginServer(t, http.StatusOK, joinOKBody)
		room := &fakeRoom{}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		ch := runStart(context.Background(), tr)
		require.Eventually(t, func() bool { return fc.callback() != nil }, 2*time.Second, 10*time.Millisecond)
		fc.callback().OnDisconnectedWithReason(lksdk.ParticipantRemoved)

		require.ErrorIs(t, requireStartResult(t, ch), ErrStoppedBeforeStart)
		require.Equal(t, int32(1), room.disconnects.Load())
		require.Equal(t, "", tr.StopReason())
		require.Zero(t, ps.statusCallCount())
	})

	t.Run("records a failure when the room disconnects unexpectedly before the recording starts", func(t *testing.T) {
		_, tr := newPluginServer(t, http.StatusOK, joinOKBody)
		room := &fakeRoom{}
		fc := &fakeConnector{room: room}
		tr.connectToRoom = fc.connect

		ch := runStart(context.Background(), tr)
		require.Eventually(t, func() bool { return fc.callback() != nil }, 2*time.Second, 10*time.Millisecond)
		fc.callback().OnDisconnectedWithReason(lksdk.Failed)

		err := requireStartResult(t, ch)
		require.EqualError(t, err, "transcriber stopped before starting: livekit room disconnected unexpectedly: connection to room failed")
		require.NotErrorIs(t, err, ErrStoppedBeforeStart, "an unexpected stop is a failure")
		require.Equal(t, int32(1), room.disconnects.Load())
		require.Equal(t, "livekit room disconnected unexpectedly: connection to room failed", tr.StopReason())
	})
}

// Ensure the real connector satisfies the function type.
var _ connectToRoomFunc = connectToLiveKitRoom
