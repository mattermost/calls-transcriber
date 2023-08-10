package call

import (
	"fmt"
	"sync"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"
)

const (
	pluginID          = "com.mattermost.calls"
	maxTracksContexes = 256
)

type Transcriber struct {
	cfg config.CallTranscriberConfig

	client    *client.Client
	apiClient *model.Client4

	doneCh       chan struct{}
	liveTracksWg sync.WaitGroup
	trackCtxs    chan trackContext
}

func NewTranscriber(cfg config.CallTranscriberConfig) (*Transcriber, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	client, err := client.New(client.Config{
		SiteURL:   cfg.SiteURL,
		AuthToken: cfg.AuthToken,
		ChannelID: cfg.CallID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create calls client: %w", err)
	}

	apiClient := model.NewAPIv4Client(cfg.SiteURL)
	apiClient.SetToken(cfg.AuthToken)

	return &Transcriber{
		cfg:       cfg,
		client:    client,
		apiClient: apiClient,
		doneCh:    make(chan struct{}),
		trackCtxs: make(chan trackContext, maxTracksContexes),
	}, nil
}

func (t *Transcriber) Start() error {
	t.client.On(client.RTCTrackEvent, t.handleTrack)
	t.client.On(client.CloseEvent, t.handleClose)

	if err := t.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	return nil
}

func (t *Transcriber) Stop() error {
	err := t.client.Close()
	<-t.doneCh
	return err
}

func (t *Transcriber) Done() <-chan struct{} {
	return t.doneCh
}
