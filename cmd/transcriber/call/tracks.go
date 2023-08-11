package call

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/whisper.cpp"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

const (
	trackInAudioRate    = 48000 // Default sample rate for Opus
	trackAudioChannels  = 1     // Only mono supported for now
	trackOutAudioRate   = 16000 // 16KHz is what Whisper requires
	trackAudioFrameSize = 20    // 20ms is the default Opus frame size for WebRTC
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

	oggWriter, err := oggwriter.NewWith(trackFile, trackInAudioRate, trackAudioChannels)
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

	var tr transcribe.Transcription
	for ctx := range t.trackCtxs {
		log.Printf("post processing track %s", ctx.trackID)

		trackTr, err := t.transcribeTrack(ctx)
		if err != nil {
			log.Printf("failed to transcribe track %q: %s", ctx.trackID, err)
			continue
		}

		tr = append(tr, trackTr)
	}

	log.Printf("transcription process completed for all tracks")

	log.Printf(tr.WebVTT())

	close(t.doneCh)
	return nil
}

type trackTimedSamples struct {
	pcm     []float32
	startTS int64
}

func (ctx trackContext) decodeAudio() ([]trackTimedSamples, error) {
	trackFile, err := os.Open(ctx.filename)
	defer trackFile.Close()

	if err != nil {
		return nil, fmt.Errorf("failed to open track file: %w", err)
	}

	oggReader, _, err := oggreader.NewWith(trackFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create new ogg reader: %w", err)
	}

	opusDec, err := opus.NewDecoder(trackOutAudioRate, trackAudioChannels)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus decoder: %w", err)
	}
	defer func() {
		if err := opusDec.Destroy(); err != nil {
			log.Printf("failed to destroy decoder: %s", err)
		}
	}()

	log.Printf("decoding track %s", ctx.trackID)

	inFrameSize := trackAudioFrameSize * trackInAudioRate / 1000
	outFrameSize := trackAudioFrameSize * trackOutAudioRate / 1000
	pcmBuf := make([]float32, outFrameSize)

	// TODO: consider pre-calculating track duration to minimize memory waste.
	samples := make([]trackTimedSamples, 1)

	var prevGP uint64
	for {
		data, hdr, err := oggReader.ParseNextPage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			log.Printf("failed to parse off page: %s", err)
			continue
		}

		// Ignoring first page which only contains metadata.
		if hdr.GranulePosition == 0 {
			continue
		}

		if hdr.GranulePosition > prevGP+uint64(inFrameSize) {
			log.Printf("gap in audio samples")
			samples = append(samples, trackTimedSamples{
				startTS: int64(hdr.GranulePosition) / (trackInAudioRate / 1000),
			})
		}
		prevGP = hdr.GranulePosition

		n, err := opusDec.Decode(data, pcmBuf)
		if err != nil {
			log.Printf("failed to decode audio data: %s", err)
		}

		samples[len(samples)-1].pcm = append(samples[len(samples)-1].pcm, pcmBuf[:n]...)
	}

	return samples, nil
}

func (t *Transcriber) transcribeTrack(ctx trackContext) (transcribe.TrackTranscription, error) {
	trackTr := transcribe.TrackTranscription{
		Speaker: ctx.user.GetDisplayName(model.ShowFullName),
	}

	samples, err := ctx.decodeAudio()
	if err != nil {
		return trackTr, fmt.Errorf("failed to decode audio samples: %w", err)
	}

	for _, ts := range samples {
		log.Printf("decoded %d samples starting at %v successfully for %s",
			len(ts.pcm), time.Duration(ts.startTS)*time.Millisecond, ctx.trackID)
	}

	transcriber, err := t.newTrackTranscriber()
	if err != nil {
		return trackTr, fmt.Errorf("failed to create track transcriber: %w", err)
	}

	for _, ts := range samples {
		start := time.Now()
		segments, err := transcriber.Transcribe(ts.pcm)
		if err != nil {
			log.Printf("failed to transcribe audio samples: %s", err)
			continue
		}
		log.Printf("transcribed %v worth of audio in %v", float64(len(ts.pcm))/float64(trackOutAudioRate), time.Since(start))

		for _, s := range segments {
			s.StartTS += ts.startTS
			s.EndTS += ts.startTS
			trackTr.Segments = append(trackTr.Segments, s)
		}
	}

	if err := transcriber.Destroy(); err != nil {
		return trackTr, fmt.Errorf("failed to destroy track transcriber: %w", err)
	}

	return trackTr, nil
}

func (t *Transcriber) newTrackTranscriber() (transcribe.Transcriber, error) {
	switch t.cfg.TranscribeAPI {
	case config.TranscribeAPIWhisperCPP:
		return whisper.NewContext(whisper.Config{
			ModelFile:  filepath.Join("./models", fmt.Sprintf("ggml-%s.en.bin", string(t.cfg.ModelSize))),
			NumThreads: 1,
		})
	default:
		return nil, fmt.Errorf("transcribe API %q not implemented", t.cfg.TranscribeAPI)
	}
}
