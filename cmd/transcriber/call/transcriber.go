package call

import (
	"context"
	"encoding/json"
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
	maxTracksContexes = 256
)

type APIClient interface {
	DoAPIRequest(ctx context.Context, method, url, data, etag string) (*http.Response, error)
	DoAPIRequestBytes(ctx context.Context, method, url string, data []byte, etag string) (*http.Response, error)
	DoAPIRequestReader(ctx context.Context, method, url string, data io.Reader, headers map[string]string) (*http.Response, error)
}

type Transcriber struct {
	cfg config.CallTranscriberConfig

	dataPath string

	// room is the LiveKit room the bot subscribes to in order to receive audio.
	// It is atomic because it is assigned late in Start while other goroutines
	// (the LiveKit callbacks) may already call done().
	room      atomic.Pointer[lksdk.Room]
	apiClient APIClient
	apiURL    string

	startedCh chan struct{}
	startOnce sync.Once
	// transcriptionSeen records that the transcription job has been observed in
	// the room metadata. Its later absence means the job has ended.
	transcriptionSeen atomic.Bool

	errCh        chan error
	doneCh       chan struct{}
	doneOnce     sync.Once
	stopReason   string
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

	t.startedCh = make(chan struct{})
	t.errCh = make(chan error, 1)
	t.doneCh = make(chan struct{})
	t.trackCtxs = make(chan trackContext, maxTracksContexes)
	t.captionsPoolQueueCh = make(chan captionPackage, transcriberQueueChBuffer)
	t.captionsPoolDoneCh = make(chan struct{})

	return
}

func (t *Transcriber) Start(ctx context.Context) (retErr error) {
	if t.cfg.LiveCaptionsOn {
		// TODO(MM-71164 phase 2): deliver captions over LiveKit data messages.
		slog.Warn("LiveCaptionsOn is set but live captions are not supported without the websocket yet; skipping")
		t.cfg.LiveCaptionsOn = false
	}

	// 1. Join the call over HTTP. This creates the bot's call session and
	//    returns a subscribe-only LiveKit token. It is rejected if the job was
	//    already stopped or started.
	session, err := t.createLiveKitSession(ctx)
	if err != nil {
		return err
	}
	slog.Debug("created livekit session", slog.String("sessionID", session.SessionID))

	// Tear down the LiveKit room if any later startup step fails. (main also
	// calls Stop on failure; done is idempotent.) Synchronous: Start returns only
	// after resources are cleaned up on error, so the caller never observes a
	// half-started transcriber.
	defer func() {
		if retErr != nil {
			t.done("")
		}
	}()

	// 2. Connect to the LiveKit room. Call signalling (job state, stop) arrives
	//    through room metadata and participant removal.
	room, err := lksdk.ConnectToRoomWithToken(session.URL, session.Token, &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: t.handleTrack,
		},
		OnRoomMetadataChanged: t.handleRoomMetadata,
		OnDisconnectedWithReason: func(reason lksdk.DisconnectionReason) {
			slog.Debug("disconnected from livekit room", slog.String("reason", string(reason)))
			switch reason {
			case lksdk.RoomClosed, lksdk.LeaveRequested, lksdk.ParticipantRemoved:
				go t.done("")
			default:
				go t.done("livekit room disconnected unexpectedly: " + string(reason))
			}
		},
	}, lksdk.WithAutoSubscribe(true))
	if err != nil {
		return fmt.Errorf("failed to connect to livekit room: %w", err)
	}
	t.room.Store(room)
	slog.Debug("connected to livekit room")

	// The initial state doesn't go through the metadata callback. The call state
	// from the join response is the freshest view; the room metadata may not
	// have been published yet.
	t.handleJobs(session.CallState)
	t.handleRoomMetadata(room.Metadata())

	// 3. We are coupling transcribing with recording: we don't start processing
	//    audio until the recording job state carries a start time, which keeps
	//    the two jobs in sync.
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

// handleRoomMetadata processes a LiveKit room metadata update.
func (t *Transcriber) handleRoomMetadata(metadata string) {
	if metadata == "" {
		return
	}
	var jobs liveKitJobs
	if err := json.Unmarshal([]byte(metadata), &jobs); err != nil {
		slog.Warn("failed to parse room metadata", slog.String("err", err.Error()))
		return
	}
	t.handleJobs(jobs)
}

func (t *Transcriber) handleJobs(jobs liveKitJobs) {
	// Room metadata only carries active jobs, so an ended transcription job
	// disappears. The first publish can lag behind job creation, so absence only
	// counts as a stop signal once the job has been seen. This is a backstop;
	// the primary stop signal is the bot being removed from the room.
	if jobs.Transcription != nil {
		t.transcriptionSeen.Store(true)
	} else if t.transcriptionSeen.Load() {
		slog.Info("transcription job no longer in room metadata, exiting")
		go t.done("")
		return
	}

	// Note: start_at is the absolute timestamp of when the recording started
	// to process but could come from a different instance and potentially
	// suffer from clock skew. Using time.Now() may be more precise but it
	// requires us to guarantee that the transcribing job starts before the
	// recording does.
	if jobs.Recording == nil || jobs.Recording.StartAt <= 0 {
		return
	}

	t.startOnce.Do(func() {
		// We are coupling transcribing with recording. This means that we
		// won't start unless a recording is ongoing.
		slog.Debug("updating startAt to be in sync with recording", slog.Int64("startAt", jobs.Recording.StartAt))
		t.startTime.Store(newTimeP(time.UnixMilli(jobs.Recording.StartAt)))
		close(t.startedCh)
	})
}

func (t *Transcriber) Stop(ctx context.Context) error {
	go t.done("")

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

func (t *Transcriber) StopReason() string {
	return t.stopReason
}

func (t *Transcriber) Err() error {
	select {
	case err := <-t.errCh:
		return err
	default:
		return nil
	}
}

func (t *Transcriber) done(reason string) {
	t.doneOnce.Do(func() {
		t.stopReason = reason
		// Disconnecting from the room makes the per-track ReadRTP loops return,
		// which lets handleClose's wait on liveTracksWg complete.
		if room := t.room.Load(); room != nil {
			room.Disconnect()
		}
		close(t.captionsPoolDoneCh)
		t.errCh <- t.handleClose()
		close(t.doneCh)
	})
}
