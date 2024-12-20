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
	"github.com/mattermost/rtcd/client"
)

const (
	pluginID          = "com.mattermost.calls"
	wsEvCaption       = "custom_" + pluginID + "_caption"
	wsEvMetric        = "custom_" + pluginID + "_metric"
	maxTracksContexes = 256
)

type APIClient interface {
	DoAPIRequest(ctx context.Context, method, url, data, etag string) (*http.Response, error)
	DoAPIRequestBytes(ctx context.Context, method, url string, data []byte, etag string) (*http.Response, error)
	DoAPIRequestReader(ctx context.Context, method, url string, data io.Reader, headers map[string]string) (*http.Response, error)
}

type Transcriber struct {
	cfg config.CallTranscriberConfig

	client    *client.Client
	apiClient APIClient
	apiURL    string

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

func NewTranscriber(cfg config.CallTranscriberConfig) (t *Transcriber, retErr error) {
	if err := cfg.IsValidURL(); err != nil {
		return nil, fmt.Errorf("failed to validate URL: %w", err)
	}

	apiClient := model.NewAPIv4Client(cfg.SiteURL)
	apiClient.SetToken(cfg.AuthToken)

	t = &Transcriber{
		cfg:       cfg,
		apiClient: apiClient,
		apiURL:    apiClient.URL,
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

	t.client = rtcdClient
	t.errCh = make(chan error, 1)
	t.doneCh = make(chan struct{})
	t.trackCtxs = make(chan trackContext, maxTracksContexes)
	t.captionsPoolQueueCh = make(chan captionPackage, transcriberQueueChBuffer)
	t.captionsPoolDoneCh = make(chan struct{})

	return
}

func (t *Transcriber) Start(ctx context.Context) error {
	var connectOnce sync.Once
	connectedCh := make(chan struct{})
	err := t.client.On(client.RTCConnectEvent, func(_ any) error {
		slog.Debug("transcoder RTC client connected")

		connectOnce.Do(func() {
			close(connectedCh)
		})

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to register RTCConnectEvent: %w", err)
	}
	err = t.client.On(client.RTCTrackEvent, t.handleTrack)
	if err != nil {
		return fmt.Errorf("failed to register RTCTrackEvent: %w", err)
	}
	err = t.client.On(client.CloseEvent, func(_ any) error {
		go t.done()
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to register CloseEvent: %w", err)
	}

	var startOnce sync.Once
	startedCh := make(chan struct{})
	err = t.client.On(client.WSCallJobStateEvent, func(ctx any) error {
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
	})
	if err != nil {
		return fmt.Errorf("failed to register WSCallJobStateEvent: %w", err)
	}

	err = t.client.On(client.WSJobStopEvent, func(ctx any) error {
		jobID, _ := ctx.(string)
		if jobID == "" {
			return fmt.Errorf("unexpected empty jobID")
		}

		if jobID == t.cfg.TranscriptionID {
			slog.Info("received job stop event, exiting")
			go t.client.Close()
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to register WSJobStopEvent: %w", err)
	}

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
		close(t.captionsPoolDoneCh)
		t.errCh <- t.handleClose()
		close(t.doneCh)
	})
}
