package call

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	"github.com/mattermost/rtcd/client"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	pluginID          = "com.mattermost.calls"
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

	captionsPoolQueueCh chan captionPackage
	captionsPoolWg      sync.WaitGroup
	closingCh           chan struct{}
}

func NewTranscriber(cfg config.CallTranscriberConfig) (t *Transcriber, retErr error) {
	if err := cfg.IsValidURL(); err != nil {
		return nil, fmt.Errorf("failed to validate URL: %w", err)
	}

	apiClient := model.NewAPIv4Client(cfg.SiteURL)
	apiClient.SetToken(cfg.AuthToken)

	t = &Transcriber{
		cfg:       cfg,
		apiClient: apiClient,
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

	rtcdClient, err := client.New(client.Config{
		SiteURL:   cfg.SiteURL,
		AuthToken: cfg.AuthToken,
		ChannelID: cfg.CallID,
		JobID:     cfg.TranscriptionID,
	})
	if err != nil {
		return t, err
	}

	t = &Transcriber{
		cfg:                 cfg,
		client:              rtcdClient,
		apiClient:           apiClient,
		errCh:               make(chan error, 1),
		doneCh:              make(chan struct{}),
		trackCtxs:           make(chan trackContext, maxTracksContexes),
		captionsPoolQueueCh: make(chan captionPackage, transcriberQueueChBuffer),
		closingCh:           make(chan struct{}),
	}

	return
}

func (t *Transcriber) Start(ctx context.Context) error {
	var connectOnce sync.Once
	connectedCh := make(chan struct{})
	if err := t.client.On(client.RTCConnectEvent, func(_ any) error {
		slog.Debug("transcoder RTC client connected")

		connectOnce.Do(func() {
			close(connectedCh)
		})

		return nil
	}); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	if err := t.client.On(client.RTCTrackEvent, t.handleTrack); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	if err := t.client.On(client.CloseEvent, func(_ any) error {
		go t.done()
		return nil
	}); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	var startOnce sync.Once
	startedCh := make(chan struct{})
	if err := t.client.On(client.WSCallRecordingStateEvent, func(ctx any) error {
		if recState, ok := ctx.(client.CallJobState); ok && recState.StartAt > 0 {
			slog.Debug("received call recording state", slog.Any("jobState", recState))

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
	}); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	if err := t.client.On(client.WSJobStopEvent, func(ctx any) error {
		jobID, _ := ctx.(string)
		if jobID == "" {
			return fmt.Errorf("unexpected empty jobID")
		}

		if jobID == t.cfg.TranscriptionID {
			slog.Info("received job stop event, exiting")
			go t.done()
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	var aiMut sync.Mutex
	var aiConnected bool
	if err := t.client.On(client.WSSummonAIEvent, func(ctx any) error {
		authToken, _ := ctx.(string)
		slog.Info("AI was summoned", slog.String("authToken", authToken))

		aiMut.Lock()
		defer aiMut.Unlock()

		if !aiConnected {
			aiConnected = true
			go func() {
				t.summonAI(authToken, t.closingCh)
				aiMut.Lock()
				aiConnected = false
				aiMut.Unlock()
			}()
		} else {
			slog.Warn("AI already connected")
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	if err := t.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	select {
	case <-connectedCh:
		t.startTime.Store(newTimeP(time.Now()))
		close(startedCh)
	case <-ctx.Done():
		return ctx.Err()
	}

	if t.cfg.LiveCaptionsOn {
		slog.Debug("LiveCaptionsOn is true; startingTranscriberPool starting transcriber pool.",
			slog.String("LiveCaptionsModelSize", string(t.cfg.LiveCaptionsModelSize)),
			slog.Int("LiveCaptionsNumTranscribers", t.cfg.LiveCaptionsNumTranscribers),
			slog.Int("LiveCaptionsNumThreadsPerTranscriber", t.cfg.LiveCaptionsNumThreadsPerTranscriber),
			slog.String("LiveCaptionsLanguage", t.cfg.LiveCaptionsLanguage))
		go t.startTranscriberPool()
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
		close(t.closingCh)
		t.errCh <- t.handleClose()
		close(t.doneCh)
	})
}
