package call

import (
	"errors"
	"fmt"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/whisper.cpp"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"
	"github.com/streamer45/silero-vad-go/speech"
	"log/slog"
	"path/filepath"
	"time"
)

const (
	transcriberQueueChBuffer = 1
	tickRate                 = 2 * time.Second
	maxWindowSize            = 8 * time.Second
	windowPressureLimitSec   = 12                                                           // at this point cut the audio down to prevent a death spiral
	pktPayloadChBuffer       = trackInAudioRate / trackInFrameSize * windowPressureLimitSec // hard drop after windowPressureLimitSec seconds of audio backing up
	removeWindowAfterSilence = 3 * time.Second

	// VAD settings
	vadWindowSizeInSamples  = 512 // 30 ms
	vadThreshold            = 0.5
	vadMinSilenceDurationMs = 150
	vadSpeechPadMs          = 60
	minSpeechLengthSamples  = 330 * trackOutAudioSamplesPerMs // padding (120) + 210 of detected speech
)

type captionPackage struct {
	pcm   []float32
	retCh chan string
}

// livePacket is an audio packet sent to the live captions of a track.
type livePacket struct {
	payload []byte
	ts      uint32 // RTP timestamp
}

// liveGapThreshold is the RTP timestamp delta at which a gap ends the utterance.
// It matches removeWindowAfterSilence, when the window would be dropped anyway.
const liveGapThreshold = uint32(removeWindowAfterSilence / time.Millisecond * trackInAudioSamplesPerMs)

// liveGapFiller keeps the live window aligned with real time using RTP timestamps,
// since the sender stops sending packets during silence (Opus DTX). It builds on
// gapFiller by unwrapping the 32-bit timestamps into an offset from the first packet.
type liveGapFiller struct {
	filler  gapFiller
	started bool
	prevTS  uint32
	offset  uint64
	fed     int // samples decoded and padded since the first packet
}

// next returns how many zero samples to append before the decoded PCM of the packet
// with timestamp ts. If reset is true the gap was long enough to end the utterance:
// the caller must drop the window, and the packet starts a new one. If ok is false the
// packet is older than the previous one and the caller must skip it.
func (l *liveGapFiller) next(ts uint32) (pad int, reset, ok bool) {
	if l.started {
		// uint32 arithmetic makes the delta wrap-safe.
		delta := ts - l.prevTS
		switch {
		case delta >= 1<<31:
			// Older than the previous packet, nothing sensible to pad.
			return 0, false, false
		case delta >= liveGapThreshold:
			reset = true
		default:
			l.prevTS = ts
			l.offset += uint64(delta)
			pad = l.filler.padSamples(l.offset, l.fed)
			l.fed += pad
			return pad, false, true
		}
	}

	l.reset()
	l.started, l.prevTS = true, ts
	l.filler.padSamples(0, 0) // marks the start
	return 0, reset, true
}

// clampPad limits the padding for a packet so the window, with the frame about to be
// decoded, stays within limit. A sender can claim any timestamps, so without this a few
// packets with large gaps would grow the window far beyond the pressure limit.
func clampPad(pad, windowLen, limit int) int {
	return max(0, min(pad, limit-windowLen-trackOutFrameSize))
}

// decoded accounts for n samples of decoded PCM.
func (l *liveGapFiller) decoded(n int) {
	l.fed += n
}

// reset must be called whenever the window is dropped, so the next packet starts a new one.
func (l *liveGapFiller) reset() {
	*l = liveGapFiller{}
}

func (t *Transcriber) processLiveCaptionsForTrack(ctx trackContext, pktPayloadsCh <-chan livePacket) {
	opusDec, err := opus.NewDecoder(trackOutAudioRate, trackAudioChannels)
	if err != nil {
		slog.Error("processLiveCaptionsForTrack: failed to create opus decoder for live captions",
			slog.String("err", err.Error()), slog.String("trackID", ctx.trackID))
		return
	}
	defer func() {
		if err := opusDec.Destroy(); err != nil {
			slog.Error("processLiveCaptionsForTrack: failed to destroy decoder", slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
		}
	}()

	// Setup the VAD
	sd, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:  filepath.Join(getModelsDir(), "silero_vad.onnx"),
		SampleRate: trackOutAudioRate,

		Threshold:            vadThreshold,
		MinSilenceDurationMs: vadMinSilenceDurationMs,
		SpeechPadMs:          vadSpeechPadMs,
	})
	if err != nil {
		slog.Error("processLiveCaptionsForTrack: failed to create speech detector",
			slog.String("err", err.Error()))
		return
	}
	defer func() {
		if err := sd.Destroy(); err != nil {
			slog.Error("processLiveCaptionsForTrack: failed to destroy speech detector", slog.String("err", err.Error()))
		}
		slog.Debug("processLiveCaptionsForTrack: finished processing live captions",
			slog.String("trackID", ctx.trackID))
	}()

	pcmBuf := make([]float32, trackOutFrameSize)

	windowPressureLimitSamples := windowPressureLimitSec * 1000 * trackOutAudioSamplesPerMs
	window := make([]float32, 0, windowPressureLimitSamples)
	prevTranscribedPos := 0
	prevWindowLen := 0
	var prevAudioAt time.Time

	var filler liveGapFiller
	var paddedSamples int // padded since the last tick
	var windowReset bool  // the window was reset by a gap since the last tick

	// readTrackPktPayloads drains the pktPayloadsCh (audio data from the track) and converts it to PCM.
	readTrackPktPayloads := func(window []float32) ([]float32, error) {
		for {
			select {
			case pkt, ok := <-pktPayloadsCh:
				if !ok {
					// Exit on channel close
					return nil, errors.New("closed")
				}
				pad, reset, ok := filler.next(pkt.ts)
				if !ok {
					continue
				}
				if reset {
					// Same as the stale window reset below.
					window = window[:0]
					prevWindowLen = 0
					prevTranscribedPos = 0
					windowReset = true
				}
				pad = clampPad(pad, len(window), windowPressureLimitSamples)
				if pad > 0 {
					window = append(window, make([]float32, pad)...)
					paddedSamples += pad
				}
				n, err := opusDec.Decode(pkt.payload, pcmBuf)
				if err != nil {
					slog.Error("failed to decode audio data for live captions",
						slog.String("err", err.Error()),
						slog.String("trackID", ctx.trackID))
				}
				window = append(window, pcmBuf[:n]...)
				filler.decoded(n)
			default:
				// Done draining
				return window, nil
			}
		}
	}

	ticker := time.NewTicker(tickRate)
	defer ticker.Stop()

	// Algorithm summary:
	// - Get a cleaned version of the voice (with zeroes where no voice is detected)
	// - And a list of segments of contiguous speech or silence
	// - Don't transcribe if data hasn't increased.
	// - Don't transcribe if new (un-transcribed) data is silence.
	// - Send the cleaned data (the whole window) to the transcriber pool
	// - If window goes over its limit, we drop the oldest segments until it's below the limit
	// - Wait for the transcription (let `tick`s pass so that we're only
	//   transcribing a particular track once at a time)
	// - Publish the transcription to the call room as a LiveKit data message.
	// - finish and wait for next `tick`

	for range ticker.C {
		// empty the waiting pktPayloadsCh
		window, err = readTrackPktPayloads(window)
		if err != nil {
			// exit on close
			return
		}

		if len(window) > 0 || paddedSamples > 0 || windowReset {
			slog.Debug("processLiveCaptionsForTrack: tick",
				slog.Int("windowMs", len(window)/trackOutAudioSamplesPerMs),
				slog.Int("paddedMs", paddedSamples/trackOutAudioSamplesPerMs),
				slog.Bool("windowReset", windowReset),
				slog.String("trackID", ctx.trackID),
				slog.String("username", ctx.user.Username))
		}
		paddedSamples, windowReset = 0, false

		// track how long we were waiting until consuming the next batch of audio data, as a measure
		// of the pressure on the transcription process
		newAudioLenMs := (len(window) - prevWindowLen) / trackOutAudioSamplesPerMs

		// If we don't have enough samples, ignore the window.
		if len(window) < vadWindowSizeInSamples {
			continue
		}

		// If there hasn't been any new pcm added, don't re-transcribe.
		if len(window) == prevWindowLen {
			// And clear the window if we haven't had new data (window is stale, don't re-transcribe)
			if time.Since(prevAudioAt) > removeWindowAfterSilence {
				window = window[:0]
				prevWindowLen = 0
				prevTranscribedPos = 0
				filler.reset()
			}
			continue
		}

		// Pressure valve:
		// If the transcriber machine is (even briefly) overloaded, you can get into a kind of death spiral
		// where too much audio has been buffered in toBeTranscribed, and there's no way the transcriber
		// can finish it all in time, and it will never be able to recover. This happens especially when
		// number of calls * threads per call > numCPUs. We need to be able to relieve the pressure.
		if len(window) >= windowPressureLimitSamples {
			window = window[:0]
			prevWindowLen = 0
			prevTranscribedPos = 0
			filler.reset()
			// TODO: report a window-dropped metric once metrics have a non-websocket path.
			slog.Warn("processLiveCaptionsForTrack: window dropped",
				slog.String("trackID", ctx.trackID))
			continue
		}

		prevAudioAt = time.Now()
		prevWindowLen = len(window)

		vadSegments, err := sd.Detect(window)
		if err != nil {
			slog.Error("processLiveCaptionsForTrack: vad failed", slog.String("err", err.Error()))
			continue
		}
		if err := sd.Reset(); err != nil {
			slog.Error("failed to reset speech detector",
				slog.String("err", err.Error()),
				slog.String("trackID", ctx.trackID))
		}

		if len(vadSegments) == 0 {
			continue
		}

		// Prepare the vad segments and the audio for transcription.
		segments := convertToSegmentSamples(vadSegments, len(window))
		segments = removeShortSpeeches(segments)
		cleaned := cleanAudio(window, segments)

		// Before sending off data to be transcribed, check if new data is silence.
		// If it is silence, don't send it off.
		newDataIsSilence, windowFinished := checkSilence(segments, prevTranscribedPos)
		if windowFinished {
			window = window[:0]
			prevTranscribedPos = 0
			filler.reset()
			prevWindowLen = 0
			continue
		}
		if newDataIsSilence {
			continue
		}

		// Track our new position and send off data for transcription.
		prevTranscribedPos = len(cleaned)
		transcribedCh := make(chan string)
		pkg := captionPackage{
			pcm:   cleaned,
			retCh: transcribedCh,
		}
		select {
		case t.captionsPoolQueueCh <- pkg:
			break
		default:
			// TODO: report a buffer-full metric once metrics have a non-websocket path.
			slog.Warn("processLiveCaptionsForTrack: transcriber queue full",
				slog.String("trackID", ctx.trackID))
			close(transcribedCh)
		}

		// While audio is being transcribed, we need to cut down the window if it's > maxWindowSize.
		window, prevTranscribedPos = cutWindowToSize(ctx.trackID, window, segments, prevTranscribedPos)
		prevWindowLen = len(window)

		// Use a for loop and a select so that we can drop ticks waiting for the transcriber.
		for {
			select {
			case <-ticker.C:
				slog.Debug("processLiveCaptionsForTrack: dropped a tick waiting for the transcriber",
					slog.String("trackID", ctx.trackID))
				continue
			case text := <-transcribedCh:
				if len(text) == 0 {
					// Either transcribedCh was closed above (captionQueueCh full), or audio transcription failed.
					// Note: this appears to happen when the transcriber fails to decode a block of audio.
					// Usually the probability returned for the language is very low, which makes sense.
					slog.Debug("processLiveCaptionsForTrack: received empty text, ignoring.")
					break
				}
				t.sendCaption(ctx.sessionID, text)
				slog.Debug("processLiveCaptionsForTrack: caption processed",
					slog.String("trackID", ctx.trackID),
					slog.Float64("newAudioLenMs", float64(newAudioLenMs)))
			}

			// We've processed text, so we're finished.
			break
		}
	}
}

type segmentSamples struct {
	Start   int
	End     int
	Silence bool
}

// convertToSegmentSamples turns the speech.Segments (in time) into segmentSamples (measured in samples)
func convertToSegmentSamples(segments []speech.Segment, audioLen int) []segmentSamples {
	// close off the last segment (we know segments > 0)
	if segments[len(segments)-1].SpeechEndAt == 0 {
		segments[len(segments)-1].SpeechEndAt = float64(audioLen) / trackOutAudioRate
	}

	var ret []segmentSamples
	lastEndAtIdx := 0
	for _, seg := range segments {
		start := int(seg.SpeechStartAt * trackOutAudioRate)
		end := int(seg.SpeechEndAt * trackOutAudioRate)
		ret = append(ret, segmentSamples{
			Start:   lastEndAtIdx,
			End:     start,
			Silence: true,
		})
		ret = append(ret, segmentSamples{
			Start:   start,
			End:     end,
			Silence: false,
		})
		lastEndAtIdx = end
	}

	if lastEndAtIdx < audioLen {
		ret = append(ret, segmentSamples{
			Start:   lastEndAtIdx,
			End:     audioLen,
			Silence: true,
		})
	}

	return ret
}

// removeShortSpeeches removes small sections of speech because either they are not actual words,
// or the transcriber will have trouble with such a short amount.
func removeShortSpeeches(segments []segmentSamples) []segmentSamples {
	for i, seg := range segments {
		if !seg.Silence && (seg.End-seg.Start) < minSpeechLengthSamples {
			segments[i].Silence = true
		}
	}
	return segments
}

func cleanAudio(audio []float32, segments []segmentSamples) []float32 {
	cleaned := append([]float32(nil), audio...)
	for _, seg := range segments {
		if seg.Silence {
			for i := seg.Start; i < seg.End; i++ {
				cleaned[i] = 0
			}
		}
	}

	return cleaned
}

func checkSilence(segments []segmentSamples, prevTranscribedPos int) (newDataIsSilence bool, windowFinished bool) {
	// This is a little complicated because we might miss a tick (if the transcriber
	// takes > 1 tick to transcribe). That is why we are keeping prevTranscribedPos.
	// The goals are:
	// 1. Clear the window if new (untranscribed) data is silence,
	//    and silence > removeWindowAfterSilence.
	// 2. Do not send the window to the transcriber if all new (untranscribed) data is silence.

	removeWindowAfterSilenceSamples := removeWindowAfterSilence.Milliseconds() * trackOutAudioSamplesPerMs
	prevtranscribedSeg := -1
	for i, seg := range segments {
		if prevTranscribedPos >= seg.Start && prevTranscribedPos < seg.End {
			prevtranscribedSeg = i
			break
		}
	}

	if prevtranscribedSeg == -1 {
		return false, false
	}

	for i := prevtranscribedSeg; i < len(segments); i++ {
		if !segments[i].Silence {
			return false, false
		}
	}
	silenceLength := segments[len(segments)-1].End - segments[prevtranscribedSeg].Start
	if silenceLength >= int(removeWindowAfterSilenceSamples) {
		// 1. untranscribed data is all silence, and there's been enough silence to end this window.
		return true, true
	}

	// 2. all new (untranscribed) data is silence, so don't send to the transcriber.
	return true, false
}

func cutWindowToSize(trackID string, window []float32, segments []segmentSamples, prevTranscribedPos int) ([]float32, int) {
	windowGoalSize := int(maxWindowSize.Milliseconds() * trackOutAudioSamplesPerMs)

	for len(window) > windowGoalSize {
		if len(segments) == 0 {
			// Should not be possible, but instead of panic-ing, log an error.
			slog.Error("processLiveCaptionsForTrack: we have zero segments in the window. Should not be possible.",
				slog.String("trackID", trackID))
			break
		}

		var oldestSegment segmentSamples
		oldestSegment, segments = segments[0], segments[1:]
		var cutUpTo int
		if len(segments) == 0 {
			// We don't have a complete next segment yet: cut to end of oldest segment.
			cutUpTo = oldestSegment.End
		} else {
			// Cut up to start of segment we're keeping.
			cutUpTo = segments[0].Start
		}
		if cutUpTo > len(window) {
			// Don't panic, defensive, shouldn't happen.
			cutUpTo = len(window)
		}
		window = window[cutUpTo:]

		// Adjust our marker for where we've transcribed.
		// e.g., prevTranscribedPos was 10, we've cut 6, new pos is 10 - 6 = 4.
		prevTranscribedPos -= cutUpTo
	}

	return window, prevTranscribedPos
}

// sendCaption publishes a caption for the given speaker session to the call room
// as a lossy LiveKit data message. Captions are best effort: failures are logged
// and the caption is dropped.
func (t *Transcriber) sendCaption(sessionID, text string) {
	t.roomMu.Lock()
	publish := t.publishData
	t.roomMu.Unlock()
	if publish == nil {
		slog.Debug("sendCaption: not connected to the room yet, dropping caption", slog.String("sessionID", sessionID))
		return
	}

	payload, err := buildCaptionPayload(sessionID, text)
	if err != nil {
		slog.Error("sendCaption: failed to build caption payload",
			slog.String("err", err.Error()), slog.String("sessionID", sessionID))
		return
	}

	slog.Debug("sendCaption: sending caption",
		slog.String("sessionID", sessionID),
		slog.Int("textLen", len(text)),
		slog.Int64("sentAt", time.Now().UnixMilli()))
	if err := publish(payload); err != nil {
		slog.Error("sendCaption: failed to publish caption",
			slog.String("err", err.Error()), slog.String("sessionID", sessionID))
	}
}

func (t *Transcriber) startTranscriberPool() {
	for i := 0; i < t.cfg.LiveCaptionsNumTranscribers; i++ {
		t.captionsPoolWg.Add(1)
		go t.handleTranscriptionRequests(i)
	}
}

func (t *Transcriber) handleTranscriptionRequests(num int) {
	slog.Debug(fmt.Sprintf("live captions, handleTranscriptionRequests: starting transcriber #%d", num))

	transcriber, err := t.newLiveCaptionsTranscriber()
	if err != nil {
		slog.Error("live captions, handleTranscriptionRequests: failed to create transcriber",
			slog.String("err", err.Error()))
		t.captionsPoolWg.Done()
		return
	}
	defer func() {
		err := transcriber.Destroy()
		if err != nil {
			slog.Error("live captions, handleTranscriptionRequests: failed to destroy transcriber",
				slog.String("err", err.Error()))
		}
		t.captionsPoolWg.Done()
	}()

	for {
		select {
		case <-t.captionsPoolDoneCh:
			slog.Debug(fmt.Sprintf("live captions, handleTranscriptionRequests: closing transcriber #%d", num))
			return
		case packet := <-t.captionsPoolQueueCh:
			transcribed, _, err := transcriber.Transcribe(packet.pcm)
			if err != nil {
				slog.Error("live captions, handleTranscriptionRequests: failed to transcribe audio samples",
					slog.String("err", err.Error()))
				packet.retCh <- ""
				continue
			}

			if len(transcribed) == 0 {
				packet.retCh <- ""
			} else {
				packet.retCh <- transcribed[0].Text
			}
		}
	}
}

func (t *Transcriber) newLiveCaptionsTranscriber() (transcribe.Transcriber, error) {
	switch t.cfg.TranscribeAPI {
	case config.TranscribeAPIAzure:
		// Only supporting WhisperCPP live captions for the time being.
		fallthrough
	case config.TranscribeAPIWhisperCPP:
		return whisper.NewContext(whisper.Config{
			ModelFile:     filepath.Join(getModelsDir(), fmt.Sprintf("ggml-%s.bin", string(t.cfg.LiveCaptionsModelSize))),
			NumThreads:    t.cfg.LiveCaptionsNumThreadsPerTranscriber,
			NoContext:     true, // do not use previous translations as context for next translation: https://github.com/ggerganov/whisper.cpp/pull/141#issuecomment-1321225563
			AudioContext:  512,  // a bit more than 10seconds: https://github.com/ggerganov/whisper.cpp/pull/141#issuecomment-1321230379
			PrintProgress: false,
			Language:      t.cfg.LiveCaptionsLanguage,
			SingleSegment: true,
		})
	default:
		return nil, fmt.Errorf("transcribe API %q not implemented", t.cfg.TranscribeAPI)
	}
}
