package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

type trackContext struct {
	trackID   string
	sessionID string
	filename  string
	startTS   int64
	user      *model.User
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

	t.liveTracksWg.Add(1)
	go t.processLiveTrack(track, sessionID, user)

	return nil
}

func (t *Transcriber) processLiveTrack(track *webrtc.TrackRemote, sessionID string, user *model.User) {
	ctx := trackContext{
		trackID:   track.ID(),
		sessionID: sessionID,
		user:      user,
		filename:  filepath.Join("tracks", fmt.Sprintf("%s_%s.ogg", user.Id, sessionID)),
	}

	log.Printf("processing voice track of %s", user.Username)
	log.Printf("start reading loop for track %s", ctx.trackID)
	defer func() {
		log.Printf("exiting reading loop for track %s", ctx.trackID)
		select {
		case t.trackCtxs <- ctx:
		default:
			log.Printf("failed to enqueue track context: %+v", ctx)
		}
		t.liveTracksWg.Done()
	}()

	trackFile, err := os.OpenFile(ctx.filename, os.O_RDWR|os.O_CREATE, 0600)
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
				log.Printf("failed to read RTP packet for track %s", ctx.trackID)
			}
			return
		}

		if ctx.startTS == 0 {
			ctx.startTS = time.Now().UnixMilli()
		}

		if err := oggWriter.WriteRTP(pkt); err != nil {
			log.Printf("failed to write RTP packet: %s", err)
		}
	}
}

func (t *Transcriber) handleClose(_ any) error {
	log.Printf("handleClose")

	t.liveTracksWg.Wait()
	close(t.trackCtxs)

	log.Printf("live tracks processing done, starting post processing")

	for tCtx := range t.trackCtxs {
		log.Printf("post processing track: %+v", tCtx)
	}

	close(t.doneCh)
	return nil
}
