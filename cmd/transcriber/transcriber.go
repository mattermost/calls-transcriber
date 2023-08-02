package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

const (
	pluginID = "com.mattermost.calls"
)

type Transcriber struct {
	cfg config.TranscriberConfig

	client    *client.Client
	apiClient *model.Client4
}

func NewTranscriber(cfg config.TranscriberConfig) (*Transcriber, error) {
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
	}, nil
}

func (t *Transcriber) Start() error {
	t.client.On(client.RTCTrackEvent, t.handleTrack)

	if err := t.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	return nil
}

func (t *Transcriber) Stop() error {
	return t.client.Close()
}

func (t *Transcriber) handleTrack(ctx any) error {
	track, ok := ctx.(*webrtc.TrackRemote)
	if !ok {
		return fmt.Errorf("failed to convert track")
	}

	trackID := track.ID()

	trackType, sessionID, err := client.ParseTrackID(trackID)
	if err != nil {
		return fmt.Errorf("failed to parse track ID: %w", err)
	}
	if trackType != client.TrackTypeVoice {
		log.Printf("ignoring non voice track")
		return nil
	}
	if mt := track.Codec().MimeType; mt != webrtc.MimeTypeOpus {
		log.Printf("ignoring unsupported mimetype %s for track %s", mt, trackID)
		return nil
	}

	user, err := t.getUserForSession(sessionID)
	if err != nil {
		return fmt.Errorf("failed to get user for session: %w", err)
	}

	go t.processTrack(track, sessionID, user)

	return nil
}

func (t *Transcriber) processTrack(track *webrtc.TrackRemote, sessionID string, user *model.User) {
	trackID := track.ID()

	log.Printf("processing voice track of %s", user.Username)
	log.Printf("start reading loop for track %s", trackID)
	defer log.Printf("exiting reading loop for track %s", trackID)

	filename := fmt.Sprintf("%s_%s.ogg", user.Id, sessionID)

	trackFile, err := os.OpenFile(filepath.Join("tracks", filename), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		log.Printf("failed to open track file for writing: %s", err)
		return
	}
	defer trackFile.Close()

	oggWriter, err := oggwriter.NewWith(trackFile, 48000, 1)
	if err != nil {
		log.Printf("failed to created ogg writer: %s", err)
		return
	}
	defer oggWriter.Close()

	for {
		pkt, _, readErr := track.ReadRTP()
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				log.Printf("failed to read RTP packet for track %s", trackID)
			}
			return
		}

		if err := oggWriter.WriteRTP(pkt); err != nil {
			log.Printf("failed to write RTP packet: %s", err)
		}
	}
}
