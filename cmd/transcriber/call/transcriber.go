package call

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"
)

const (
	pluginID          = "com.mattermost.calls"
	wsEvPrefix        = "custom_" + pluginID + "_"
	wsEvCaption       = "custom_" + pluginID + "_caption"
	wsEvMetric        = "custom_" + pluginID + "_metric"
	maxTracksContexes = 256
)

type Transcriber struct {
	cfg config.CallTranscriberConfig

	client    *client.Client
	apiClient *model.Client4

	errCh        chan error
	doneCh       chan struct{}
	doneOnce     sync.Once
	liveTracksWg sync.WaitGroup
	trackCtxs    chan trackContext
	startTime    atomic.Pointer[time.Time]

	transcriberQueueCh chan captionPackage
	transcriberWg      sync.WaitGroup
	transcriberDoneCh  chan struct{}
}

func NewTranscriber(cfg config.CallTranscriberConfig) (*Transcriber, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	client, err := client.New(client.Config{
		SiteURL:   cfg.SiteURL,
		AuthToken: cfg.AuthToken,
		ChannelID: cfg.CallID,
		JobID:     cfg.TranscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create calls client: %w", err)
	}

	apiClient := model.NewAPIv4Client(cfg.SiteURL)
	apiClient.SetToken(cfg.AuthToken)

	return &Transcriber{
		cfg:                cfg,
		client:             client,
		apiClient:          apiClient,
		errCh:              make(chan error, 1),
		doneCh:             make(chan struct{}),
		trackCtxs:          make(chan trackContext, maxTracksContexes),
		transcriberQueueCh: make(chan captionPackage, captionQueueBuffer),
		transcriberDoneCh:  make(chan struct{}),
	}, nil
}

func (t *Transcriber) Start(ctx context.Context) error {
	var connectOnce sync.Once
	connectedCh := make(chan struct{})
	t.client.On(client.RTCConnectEvent, func(_ any) error {
		slog.Debug("transcoder RTC client connected")

		connectOnce.Do(func() {
			close(connectedCh)
		})

		return nil
	})
	t.client.On(client.RTCTrackEvent, t.handleTrack)
	t.client.On(client.CloseEvent, func(msg any) error {
		go t.done()
		return nil
	})

	var startOnce sync.Once
	startedCh := make(chan struct{})
	t.client.On(client.WSCallRecordingState, func(ctx any) error {
		if recState, ok := ctx.(client.CallJobState); ok && recState.StartAt > 0 {
			slog.Debug("received call recording state", slog.Any("recState", recState))

			// Note: recState.StartAt is the absolute timestamp of when the recording
			//       started to process but could come from a different instance and
			//       potentially suffer from clock skew. Using time.Now() may be more
			//       precise but it requires us to guarantee that the transcribing
			//       job starts before the recording does.
			startOnce.Do(func() {
				// We are coupling transcribing with recording. This means that we
				// won't start unless a recording is on going.
				slog.Debug("updating startAt to be in sync with recording", slog.Int64("startAt", recState.StartAt))
				t.startTime.Store(newTimeP(time.UnixMilli(recState.StartAt)))
				close(startedCh)
			})
		}
		return nil
	})

	t.client.On(client.WSJobStopEvent, func(ctx any) error {
		jobID, _ := ctx.(string)
		if jobID == "" {
			return fmt.Errorf("unexpected empty jobID")
		}

		if jobID == t.cfg.TranscriptionID {
			slog.Info("received job stop event, exiting")
			go t.done()
		}

		return nil
	})

	if err := t.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	select {
	case <-connectedCh:
	case <-ctx.Done():
		return ctx.Err()
	}

	if t.cfg.LiveCaptionsOn {
		slog.Debug("LiveCaptionsOn is true; startingTranscriberPool starting transcriber pool.",
			slog.String("LiveCaptionsModelSize", string(t.cfg.LiveCaptionsModelSize)),
			slog.Int("LiveCaptionsNumTranscribers", t.cfg.LiveCaptionsNumTranscribers),
			slog.Int("LiveCaptionsNumThreadsPerTranscriber", t.cfg.LiveCaptionsNumThreadsPerTranscriber))
		go t.startTranscriberPool()
	} else {
		slog.Info("LiveCaptionsOn is false; not starting the transcriber pool")
	}

	select {
	case <-startedCh:
		if err := t.ReportJobStarted(); err != nil {
			return fmt.Errorf("failed to report job started status: %w", err)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (t *Transcriber) Stop(ctx context.Context) error {
	if err := t.client.Close(); err != nil {
		slog.Error("failed to close client on stop", slog.String("err", err.Error()))
	}

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
		close(t.transcriberDoneCh)
		t.errCh <- t.handleClose()
		close(t.doneCh)
	})
}
