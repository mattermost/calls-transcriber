package call

import (
	"errors"
	"fmt"
	"github.com/mattermost/mattermost-plugin-calls/server/public"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/azure"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/whisper.cpp"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/ogg"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"

	"github.com/streamer45/silero-vad-go/speech"

	"github.com/pion/webrtc/v3"
)

const (
	trackInAudioRate          = 48000                                            // Default sample rate for Opus
	trackAudioChannels        = 1                                                // Only mono supported for now
	trackOutAudioRate         = 16000                                            // 16KHz is what Whisper requires
	trackInAudioSamplesPerMs  = trackInAudioRate / 1000                          // Number of audio samples per ms
	trackOutAudioSamplesPerMs = trackOutAudioRate / 1000                         // Number of audio samples per ms
	trackAudioFrameSizeMs     = 20                                               // 20ms is the default Opus frame size for WebRTC
	trackInFrameSize          = trackAudioFrameSizeMs * trackInAudioRate / 1000  // The input frame size in samples
	trackOutFrameSize         = trackAudioFrameSizeMs * trackOutAudioRate / 1000 // The output frame size in samples
	audioGapThreshold         = time.Second                                      // The amount of time after which we detect a gap in the audio track.
	rtpTSWrapAroundThreshold  = trackInAudioRate                                 // The threshold to detect if the RTP timestamp has wrapped around (one second worth of samples).

	dataDir   = "/data"
	modelsDir = "/models"
)

type trackContext struct {
	trackID   string
	sessionID string
	filename  string
	startTS   int64
	user      *model.User
}

// handleTrack gets called whenever a new WebRTC track is received (e.g. someone unmuted
// for the first time). As soon as this happens we start processing the track.
func (t *Transcriber) handleTrack(ctx any) error {
	m, ok := ctx.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected context type")
	}

	track, ok := m["track"].(*webrtc.TrackRemote)
	if !ok {
		return fmt.Errorf("unexpected track type")
	}

	trackID := track.ID()

	receiver, ok := m["receiver"].(*webrtc.RTPReceiver)
	if !ok {
		return fmt.Errorf("unexpected receiver type")
	}
	defer func() {
		if err := receiver.Stop(); err != nil {
			slog.Error("failed to stop receiver for track",
				slog.String("trackID", trackID), slog.String("err", err.Error()))
		}
	}()

	trackType, sessionID, err := client.ParseTrackID(trackID)
	if err != nil {
		return fmt.Errorf("failed to parse track ID: %w", err)
	}
	if trackType != client.TrackTypeVoice {
		slog.Debug("ignoring non voice track", slog.String("trackID", trackID))
		return nil
	}
	if mt := track.Codec().MimeType; mt != webrtc.MimeTypeOpus {
		slog.Warn("ignoring unsupported mimetype for track", slog.String("mimeType", mt), slog.String("trackID", trackID))
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

// processLiveTrack saves the content of a voice track to a file for later processing.
// This involves muxing the raw Opus packets into a OGG file with the
// timings adjusted to account for any potential gaps due to mute/unmute sequences.
func (t *Transcriber) processLiveTrack(track trackRemote, sessionID string, user *model.User) {
	ctx := trackContext{
		trackID:   track.ID(),
		sessionID: sessionID,
		user:      user,
		filename:  filepath.Join(getDataDir(), fmt.Sprintf("%s_%s.ogg", user.Id, track.ID())),
	}

	slog.Debug("processing voice track",
		slog.String("username", user.Username),
		slog.String("sessionID", sessionID),
		slog.String("trackID", ctx.trackID))
	slog.Debug("start reading loop for track", slog.String("trackID", ctx.trackID))
	defer func() {
		slog.Debug("exiting reading loop for track", slog.String("trackID", ctx.trackID))
		select {
		case t.trackCtxs <- ctx:
		default:
			slog.Error("failed to enqueue track context", slog.Any("ctx", ctx))
		}
		t.liveTracksWg.Done()
	}()

	oggWriter, err := ogg.NewWriter(ctx.filename, trackInAudioRate, trackAudioChannels)
	if err != nil {
		slog.Error("failed to created ogg writer", slog.String("err", err.Error()), slog.String("trackID", ctx.trackID))
		return
	}
	defer oggWriter.Close()

	// Live captioning:
	// pktPayloadCh is used to send the rtp audio data to the processLiveCaptionsForTrack goroutine
	var pktPayloadCh chan []byte
	if t.cfg.LiveCaptionsOn {
		pktPayloadCh = make(chan []byte, pktPayloadChBuffer)
		defer func() {
			close(pktPayloadCh)
		}()

		go t.processLiveCaptionsForTrack(ctx, pktPayloadCh)
	}

	// Read track audio:
	var prevArrivalTime time.Time
	var prevRTPTimestamp uint32
	for {
		pkt, _, readErr := track.ReadRTP()
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				slog.Error("failed to read RTP packet for track",
					slog.String("err", readErr.Error()),
					slog.String("trackID", ctx.trackID))
			}
			return
		}

		// We start processing audio samples only when the recording process has successfully started.
		if t.startTime.Load() == nil {
			continue
		}

		// Ignore empty packets. This is important to avoid synchronization issues
		// since empty packets are not written in the output OGG file (MM-56186) so
		// they would cause the relative offset value (gap) to be lost.
		if len(pkt.Payload) == 0 {
			continue
		}

		// We ignore out of order packets as they would cause synchronization
		// issues. In the future we may want to reorder them but that requires us to keep
		// buffers and complicate the whole process.
		if pkt.Timestamp < prevRTPTimestamp {
			slog.Debug("out of order packet",
				slog.Int("diff", int(pkt.Timestamp)-int(prevRTPTimestamp)),
				slog.String("trackID", ctx.trackID))

			// Check that timestamp hasn't wrapped around. Fairly unlikely but it's
			// a possibility since the starting timestamp is generated randomly so
			// it could be close to the end of the uint32 range.
			// If it hasn't wrapped around then it's an out of order packet which we want
			// to skip.
			if hasWrappedAround := math.MaxUint32-prevRTPTimestamp < rtpTSWrapAroundThreshold; !hasWrappedAround {
				continue
			}

			// If we detect wraparound we can then go ahead and write the packet
			// as the increment in timestamp will handled automatically (and
			// correctly) by the uint conversion that happens in oggWriter.WriteRTP().
			// Example: uint32(704-4294967000) = 1000
			slog.Debug("ts wrap around detected", slog.String("trackID", ctx.trackID))
		}

		var gap uint64
		if ctx.startTS == 0 {
			ctx.startTS = time.Since(*t.startTime.Load()).Milliseconds()
			slog.Debug("start offset for track",
				slog.Duration("offset", time.Duration(ctx.startTS)*time.Millisecond),
				slog.String("trackID", ctx.trackID))
		} else if receiveGap := time.Since(prevArrivalTime); receiveGap > audioGapThreshold {
			// If the last received audio packet was more than a audioGapThreshold
			// ago we may need to fix the RTP timestamp as some clients (e.g. Firefox) will
			// simply resume from where they left.

			// TODO: check whether it may be easier to rely on sender reports to
			// potentially achieve more accurate synchronization.
			rtpGap := time.Duration((pkt.Timestamp-prevRTPTimestamp)/trackInAudioSamplesPerMs) * time.Millisecond

			slog.Debug("receive gap detected",
				slog.Duration("receiveGap", receiveGap), slog.Duration("rtpGap", rtpGap),
				slog.Uint64("currTS", uint64(pkt.Timestamp)), slog.Uint64("prevTS", uint64(prevRTPTimestamp)),
				slog.String("trackID", ctx.trackID))

			if (rtpGap - receiveGap).Abs() > audioGapThreshold {
				// If the difference between the timestamps reported in RTP packets and
				// the measured time since the last received packet is greater than
				// audioGapThreshold we need to fix it by adding the relative gap in time of
				// arrival. This is to create "time holes" in the OGG file in such a way
				// that we can easily keep track of separate voice sequences (e.g. caused by
				// muting/unmuting).
				gap = uint64((receiveGap.Milliseconds() / trackAudioFrameSizeMs) * trackInFrameSize)
				slog.Debug("fixing audio timestamp", slog.Uint64("gap", gap), slog.String("trackID", ctx.trackID))
			}
		}

		prevArrivalTime = time.Now()
		prevRTPTimestamp = pkt.Timestamp

		if err := oggWriter.WriteRTP(pkt, gap); err != nil {
			slog.Error("failed to write RTP packet",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
		}

		if t.cfg.LiveCaptionsOn {
			select {
			case pktPayloadCh <- pkt.Payload:
			default:
				if err := t.client.SendWS(wsEvMetric, public.MetricMsg{
					SessionID:  ctx.sessionID,
					MetricName: public.MetricLiveCaptionsPktPayloadChBufFull,
				}, false); err != nil {
					slog.Error("processLiveTrack: error sending wsEvMetric MetricLiveCaptionsPktPayloadChBufFull",
						slog.String("err", err.Error()),
						slog.String("trackID", ctx.trackID))
				}
			}
		}
	}

}

// handleClose will kick off post-processing of saved voice tracks.
func (t *Transcriber) handleClose() error {
	slog.Debug("handleClose")

	t.liveTracksWg.Wait()
	close(t.trackCtxs)

	t.captionsPoolWg.Wait()

	slog.Debug("live tracks processing done, starting post processing")
	start := time.Now()

	var samplesDur time.Duration
	var tr transcribe.Transcription
	for ctx := range t.trackCtxs {
		slog.Debug("post processing track", slog.String("trackID", ctx.trackID))

		trackTr, dur, err := t.transcribeTrack(ctx)
		if err != nil {
			slog.Error("failed to transcribe track", slog.String("trackID", ctx.trackID), slog.String("err", err.Error()))
			continue
		}

		samplesDur += dur

		if len(trackTr.Segments) > 0 {
			tr = append(tr, trackTr)
		}
	}

	if len(tr) == 0 {
		slog.Warn("nothing to do, empty transcription")
		return nil
	}

	dur := time.Since(start)
	slog.Debug(fmt.Sprintf("transcription process completed for all tracks: transcribed %v of audio in %v, %0.2fx",
		samplesDur, dur, samplesDur.Seconds()/dur.Seconds()))

	if err := t.publishTranscription(tr); err != nil {
		return fmt.Errorf("failed to publish transcription: %w", err)
	}

	slog.Debug("transcription published successfully")

	return nil
}

// trackTimedSamples is used to account for potential gaps in
// voice tracks due to mute/unmute sequences. Each spoken segment
// will have a relative time offset (startTS).
type trackTimedSamples struct {
	pcm     []float32
	startTS int64
}

// decodeAudio reads a track OGG file and decodes its audio into raw PCM samples
// for later processing.
func (ctx trackContext) decodeAudio() ([]trackTimedSamples, error) {
	trackFile, err := os.Open(ctx.filename)
	defer trackFile.Close()

	if err != nil {
		return nil, fmt.Errorf("failed to open track file: %w", err)
	}

	oggReader, _, err := ogg.NewReaderWith(trackFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create new ogg reader: %w", err)
	}

	opusDec, err := opus.NewDecoder(trackOutAudioRate, trackAudioChannels)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus decoder: %w", err)
	}
	defer func() {
		if err := opusDec.Destroy(); err != nil {
			slog.Error("failed to destroy decoder",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
		}
	}()

	slog.Debug("decoding track", slog.String("trackID", ctx.trackID))

	pcmBuf := make([]float32, trackOutFrameSize)
	// TODO: consider pre-calculating track duration to minimize memory waste.
	samples := make([]trackTimedSamples, 1)

	var prevGP uint64
	for {
		data, hdr, err := oggReader.ParseNextPage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			slog.Error("failed to parse ogg page",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
			continue
		}

		// Ignoring first page which only contains metadata.
		if hdr.GranulePosition == 0 {
			continue
		}

		if hdr.GranulePosition > prevGP+trackInFrameSize {
			gap := time.Duration((hdr.GranulePosition-prevGP)/trackInAudioSamplesPerMs) * time.Millisecond
			slog.Debug("gap in audio samples", slog.Duration("gap", gap))
			// If there's enough of a gap in the audio (audioGapThreshold) we split and
			// update the start time accordingly.
			if gap > audioGapThreshold {
				samples = append(samples, trackTimedSamples{
					startTS: int64(hdr.GranulePosition) / trackInAudioSamplesPerMs,
				})
			}
		}
		prevGP = hdr.GranulePosition

		n, err := opusDec.Decode(data, pcmBuf)
		if err != nil {
			slog.Error("failed to decode audio data",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
		}

		samples[len(samples)-1].pcm = append(samples[len(samples)-1].pcm, pcmBuf[:n]...)
	}

	return samples, nil
}

// transcribeTrack feeds track's raw audio samples to a transcription engine (e.g. whisper)
// and outputs a transcription.
func (t *Transcriber) transcribeTrack(ctx trackContext) (transcribe.TrackTranscription, time.Duration, error) {
	trackTr := transcribe.TrackTranscription{
		Speaker: ctx.user.GetDisplayName(model.ShowFullName),
	}

	samples, err := ctx.decodeAudio()
	if err != nil {
		return trackTr, 0, fmt.Errorf("failed to decode audio samples: %w", err)
	}

	slog.Debug("decoding done", slog.Any("samplesLen", len(samples)))

	transcriber, err := t.newTrackTranscriber()
	if err != nil {
		return trackTr, 0, fmt.Errorf("failed to create track transcriber: %w", err)
	}

	sd, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:   filepath.Join(getModelsDir(), "silero_vad.onnx"),
		SampleRate:  trackOutAudioRate,
		WindowSize:  1536,
		Threshold:   0.5,
		SpeechPadMs: 100,

		// 2 seconds of silence is a good threshold that allows us not to split speech portions excessively
		// which in turn will improve the transcribing performance as there will be less overhead.
		MinSilenceDurationMs: 2000,
	})
	if err != nil {
		return trackTr, 0, fmt.Errorf("failed to ceate speech detector: %w", err)
	}
	defer func() {
		if err := sd.Destroy(); err != nil {
			slog.Error("failed to destroy speech detector", slog.String("err", err.Error()), slog.String("trackID", ctx.trackID))
		}
	}()

	// Before transcribing, we feed the samples to a speech detector and adjust
	// the timestamps in accordance to when the speech begins/ends. This is
	// to account for any potential silence that Whisper wouldn't recognize with
	// much accuracy.
	// TODO: consider deprecating this logic if we get accurate word level timestamps
	// (https://github.com/ggerganov/whisper.cpp/issues/375).

	var speechSamples []trackTimedSamples
	for _, ts := range samples {
		// We need to reset the speech detector's state from one chunk of samples
		// to the next.
		if err := sd.Reset(); err != nil {
			slog.Error("failed to reset speech detector",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
		}

		segments, err := sd.Detect(ts.pcm)
		if err != nil {
			slog.Warn("failed to detect speech",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))

			// As a fallback in case of failure, we keep the original samples.
			speechSamples = append(speechSamples, ts)
			continue
		}
		slog.Debug("speech detection done", slog.Any("segments", segments))

		for _, seg := range segments {
			// Both SpeechStartAt and SpeechEndAt are in seconds.
			// We simply multiply by the audio sampling rate to find out
			// the index of the sample where speech starts/ends.
			startSampleOff := int(seg.SpeechStartAt * trackOutAudioRate)
			endSampleOff := int(seg.SpeechEndAt * trackOutAudioRate)

			if startSampleOff >= len(ts.pcm) {
				slog.Error("invalid startSampleOff",
					slog.Int("startSampleOff", startSampleOff),
					slog.String("trackID", ctx.trackID))
				continue
			}

			var speechPCM []float32
			if endSampleOff > startSampleOff {
				speechPCM = ts.pcm[startSampleOff:endSampleOff]
			} else {
				speechPCM = ts.pcm[startSampleOff:]
			}

			speechSamples = append(speechSamples, trackTimedSamples{
				pcm: speechPCM,
				// Multiplying as our timestamps are in milliseconds.
				startTS: ts.startTS + int64(seg.SpeechStartAt*1000),
			})
		}
	}

	slog.Debug("speech detection done", slog.Any("speechSamples", len(speechSamples)))

	var totalDur time.Duration
	for _, ts := range speechSamples {
		segments, lang, err := transcriber.Transcribe(ts.pcm)
		if err != nil {
			slog.Error("failed to transcribe audio samples",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
			continue
		}

		if lang != "" && trackTr.Language == "" {
			trackTr.Language = lang
		}

		samplesDur := time.Duration(len(ts.pcm)/trackOutAudioSamplesPerMs) * time.Millisecond
		totalDur += samplesDur

		for _, s := range segments {
			s.StartTS += ts.startTS + ctx.startTS
			s.EndTS += ts.startTS + ctx.startTS
			trackTr.Segments = append(trackTr.Segments, s)
		}
	}

	if err := transcriber.Destroy(); err != nil {
		return trackTr, 0, fmt.Errorf("failed to destroy track transcriber: %w", err)
	}

	return trackTr, totalDur, nil
}

func (t *Transcriber) newTrackTranscriber() (transcribe.Transcriber, error) {
	switch t.cfg.TranscribeAPI {
	case config.TranscribeAPIWhisperCPP:
		return whisper.NewContext(whisper.Config{
			ModelFile:     filepath.Join(getModelsDir(), fmt.Sprintf("ggml-%s.bin", string(t.cfg.ModelSize))),
			NumThreads:    t.cfg.NumThreads,
			PrintProgress: true,
		})
	case config.TranscribeAPIAzure:
		speechKey, _ := t.cfg.TranscribeAPIOptions["AZURE_SPEECH_KEY"].(string)
		speechRegion, _ := t.cfg.TranscribeAPIOptions["AZURE_SPEECH_REGION"].(string)
		return azure.NewSpeechRecognizer(azure.SpeechRecognizerConfig{
			SpeechKey:    speechKey,
			SpeechRegion: speechRegion,
		})
	default:
		return nil, fmt.Errorf("transcribe API %q not implemented", t.cfg.TranscribeAPI)
	}
}
