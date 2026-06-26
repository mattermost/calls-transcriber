package call

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	"github.com/mattermost/mattermost/server/public/model"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

const (
	pluginID          = "com.mattermost.calls"
	wsEvPrefix        = "custom_" + pluginID + "_"
	wsEvCaption       = wsEvPrefix + "caption"
	wsEvMetric        = wsEvPrefix + "metric"
	maxTracksContexes = 256

	// Outgoing WS actions (sent to the plugin).
	wsEventJoin      = wsEvPrefix + "join"
	wsEventReconnect = wsEvPrefix + "reconnect"

	// Incoming WS events (received from the plugin). Plugin-published events are
	// automatically prefixed with custom_<pluginID>_ by the server framework.
	wsEventCallJobState = wsEvPrefix + "call_job_state"
	wsEventJobStop      = wsEvPrefix + "job_stop"
	wsEventCallEnd      = wsEvPrefix + "call_ended"
)

type APIClient interface {
	DoAPIRequest(ctx context.Context, method, url, data, etag string) (*http.Response, error)
	DoAPIRequestBytes(ctx context.Context, method, url string, data []byte, etag string) (*http.Response, error)
	DoAPIRequestReader(ctx context.Context, method, url string, data io.Reader, headers map[string]string) (*http.Response, error)
}

type Transcriber struct {
	cfg config.CallTranscriberConfig

	dataPath string

	// wsClient is the reconnecting Mattermost-calls websocket client used for
	// call signalling (join, job-state/job-stop/call-end events) and for sending
	// captions and metrics back to the plugin. With LiveKit it no longer carries
	// media.
	wsClient *callsWSClient
	// room is the LiveKit room the bot subscribes to in order to receive audio.
	room      *lksdk.Room
	apiClient APIClient
	apiURL    string

	startedCh chan struct{}
	startOnce sync.Once

	errCh        chan error
	doneCh       chan struct{}
	doneOnce     sync.Once
	liveTracksWg sync.WaitGroup
	trackCtxs    chan trackContext
	startTime    atomic.Pointer[time.Time]

	captionsPoolQueueCh chan captionPackage
	captionsPoolWg      sync.WaitGroup
	captionsPoolDoneCh  chan struct{}
}

func NewTranscriber(cfg config.CallTranscriberConfig, dataPath string) (t *Transcriber, retErr error) {
	if err := cfg.IsValidURL(); err != nil {
		return nil, fmt.Errorf("failed to validate URL: %w", err)
	}

	if dataPath == "" {
		return nil, fmt.Errorf("dataPath should not be empty")
	}

	apiClient := model.NewAPIv4Client(cfg.SiteURL)
	apiClient.SetToken(cfg.AuthToken)

	t = &Transcriber{
		cfg:       cfg,
		apiClient: apiClient,
		apiURL:    apiClient.URL,
		dataPath:  dataPath,
	}

	defer func() {
		if retErr != nil && t != nil {
			retErrStr := fmt.Errorf("failed to create Transcriber: %w", retErr)
			if err := t.ReportJobFailure(retErrStr.Error()); err != nil {
				retErr = fmt.Errorf("failed to report job failure: %s, original error: %s", err.Error(), retErrStr)
			}
		}
	}()

	if err := cfg.IsValid(); err != nil {
		return t, err
	}

	t.wsClient = newCallsWSClient(cfg.SiteURL, cfg.AuthToken, cfg.CallID, cfg.TranscriptionID)
	t.startedCh = make(chan struct{})
	t.errCh = make(chan error, 1)
	t.doneCh = make(chan struct{})
	t.trackCtxs = make(chan trackContext, maxTracksContexes)
	t.captionsPoolQueueCh = make(chan captionPackage, transcriberQueueChBuffer)
	t.captionsPoolDoneCh = make(chan struct{})

	return
}

func (t *Transcriber) Start(ctx context.Context) error {
	// 1. Connect to the Mattermost websocket and join the call. The websocket
	//    carries call signalling (job-state/job-stop/call-end events) and the
	//    captions/metrics we send back; media flows over LiveKit instead. The
	//    JobID-gated join registers the bot's call session, which authorizes the
	//    LiveKit token request below. connID is the bot's session ID.
	connID, err := t.wsClient.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect websocket: %w", err)
	}
	slog.Debug("transcriber ws client connected", slog.String("connID", connID))

	go t.wsEventLoop()

	// 2. Fetch a subscribe-only LiveKit token and connect to the room.
	lkURL, token, err := t.fetchLiveKitToken(ctx, connID)
	if err != nil {
		return err
	}
	room, err := lksdk.ConnectToRoomWithToken(lkURL, token, &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: t.handleTrack,
		},
		OnDisconnected: func() {
			slog.Debug("disconnected from livekit room")
			go t.done()
		},
	}, lksdk.WithAutoSubscribe(true))
	if err != nil {
		return fmt.Errorf("failed to connect to livekit room: %w", err)
	}
	t.room = room
	slog.Debug("connected to livekit room")

	if t.cfg.LiveCaptionsOn {
		slog.Debug("LiveCaptionsOn is true; starting transcriber pool.",
			slog.String("LiveCaptionsModelSize", string(t.cfg.LiveCaptionsModelSize)),
			slog.Int("LiveCaptionsNumTranscribers", t.cfg.LiveCaptionsNumTranscribers),
			slog.Int("LiveCaptionsNumThreadsPerTranscriber", t.cfg.LiveCaptionsNumThreadsPerTranscriber),
			slog.String("LiveCaptionsLanguage", t.cfg.LiveCaptionsLanguage))
		go t.startTranscriberPool()
	}

	// 3. We are coupling transcribing with recording: we don't start processing
	//    audio until we receive the call job state carrying the (recording)
	//    start time, which keeps the two jobs in sync.
	select {
	case <-t.startedCh:
		if err := t.ReportJobStarted(); err != nil {
			return fmt.Errorf("failed to report job started status: %w", err)
		}
	case <-t.doneCh:
		return fmt.Errorf("transcriber stopped before starting")
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// wsEventLoop dispatches events from the (reconnecting) websocket client. The
// client's Events channel is closed when the connection is permanently lost or
// the client is closed; either way we're then done.
func (t *Transcriber) wsEventLoop() {
	for ev := range t.wsClient.Events() {
		t.handleWSEvent(ev)
	}
	go t.done()
}

func (t *Transcriber) handleWSEvent(ev *model.WebSocketEvent) {
	switch ev.EventType() {
	case wsEventCallJobState:
		callID, _ := ev.GetData()["callID"].(string)
		if callID != t.cfg.CallID {
			// Ignore if the event is not for the current call/channel.
			return
		}

		jobState, ok := ev.GetData()["jobState"].(map[string]any)
		if !ok {
			slog.Warn("received call job state with invalid jobState", slog.Any("data", ev.GetData()))
			return
		}

		// Note: start_at is the absolute timestamp of when the recording started
		// to process but could come from a different instance and potentially
		// suffer from clock skew. Using time.Now() may be more precise but it
		// requires us to guarantee that the transcribing job starts before the
		// recording does.
		startAt, _ := jobState["start_at"].(float64)
		if startAt <= 0 {
			return
		}

		slog.Debug("received call job state", slog.Any("jobState", jobState))

		t.startOnce.Do(func() {
			// We are coupling transcribing with recording. This means that we
			// won't start unless a recording is ongoing.
			slog.Debug("updating startAt to be in sync with recording", slog.Float64("startAt", startAt))
			t.startTime.Store(newTimeP(time.UnixMilli(int64(startAt))))
			close(t.startedCh)
		})
	case wsEventJobStop:
		jobID, _ := ev.GetData()["job_id"].(string)
		if jobID == t.cfg.TranscriptionID {
			slog.Info("received job stop event, exiting")
			go t.done()
		}
	case wsEventCallEnd:
		if b := ev.GetBroadcast(); b != nil && b.ChannelId != "" && b.ChannelId != t.cfg.CallID {
			return
		}
		slog.Info("received call end event, exiting")
		go t.done()
	}
}

// sendWS sends a custom websocket message to the plugin.
func (t *Transcriber) sendWS(ev string, msg any) error {
	return t.wsClient.Send(ev, msg)
}

func (t *Transcriber) Stop(ctx context.Context) error {
	go t.done()

	select {
	case <-t.doneCh:
		return <-t.errCh
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Transcriber) Done() <-chan struct{} {
	return t.doneCh
}

func (t *Transcriber) Err() error {
	select {
	case err := <-t.errCh:
		return err
	default:
		return nil
	}
}

func (t *Transcriber) done() {
	t.doneOnce.Do(func() {
		// Disconnecting from the room makes the per-track ReadRTP loops return,
		// which lets handleClose's wait on liveTracksWg complete.
		if t.room != nil {
			t.room.Disconnect()
		}
		close(t.captionsPoolDoneCh)
		t.errCh <- t.handleClose()
		if t.wsClient != nil {
			t.wsClient.Close()
		}
		close(t.doneCh)
	})
}
