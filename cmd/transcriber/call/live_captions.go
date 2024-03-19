package call

import (
	"errors"
	"fmt"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/whisper.cpp"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"
	"github.com/mattermost/mattermost-plugin-calls/server/public"
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
	vadWindowSizeInSamples  = 512
	vadThreshold            = 0.5
	vadMinSilenceDurationMs = 350
	vadSpeechPadMs          = 200
	minSpeechLengthSamples  = 1000 * trackOutAudioSamplesPerMs // 1 second of speech
)

type captionPackage struct {
	pcm   []float32
	retCh chan string
}

func (t *Transcriber) processLiveCaptionsForTrack(ctx trackContext, pktPayloads <-chan []byte, doneCh <-chan struct{}) {
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

		// set WindowSize to 512 to get as fine-grained detection as possible (for when
		// the number of samples don't cleanly divide into the WindowSize
		WindowSize:           vadWindowSizeInSamples,
		Threshold:            vadThreshold,
		MinSilenceDurationMs: vadMinSilenceDurationMs,
		SpeechPadMs:          vadSpeechPadMs,
	})
	if err != nil {
		slog.Error("processLiveCaptionsForTrack: failed to create speech detector",
			slog.String("err", err.Error()))
	}
	defer func() {
		if err := sd.Destroy(); err != nil {
			slog.Error("processLiveCaptionsForTrack: failed to destroy speech detector", slog.String("err", err.Error()))
		}
		slog.Debug("processLiveCaptionsForTrack: finished processing live captions",
			slog.String("trackID", ctx.trackID))
	}()

	windowPressureLimitSamples := windowPressureLimitSec * 1000 * trackOutAudioSamplesPerMs
	window := make([]float32, 0, windowPressureLimitSamples)
	pcmBuf := make([]float32, trackOutFrameSize)

	// readTrackPktPayloads drains the pktPayload channel (data from the track) and converts it to PCM.
	readTrackPktPayloads := func() error {
		for {
			select {
			case payload, ok := <-pktPayloads:
				if !ok {
					// Exit on channel close
					return errors.New("closed")
				}
				n, err := opusDec.Decode(payload, pcmBuf)
				if err != nil {
					slog.Error("failed to decode audio data for live captions",
						slog.String("err", err.Error()),
						slog.String("trackID", ctx.trackID))
				}
				window = append(window, pcmBuf[:n]...)
			default:
				// Done draining
				return nil
			}
		}
	}

	prevTranscribedPos := 0
	prevWindowLen := 0
	var prevAudioAt time.Time

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
	// - Send the transcription to the plugin to be redistributed to clients.
	// - finish and wait for next `tick`

	for {
		select {
		case <-doneCh:
			return
		case <-ticker.C:
			// empty the waiting pktPayloads
			if err := readTrackPktPayloads(); err != nil {
				// exit on close
				return
			}
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
				if err := t.client.SendWs(wsEvMetric, public.MetricMsg{
					SessionID:  ctx.sessionID,
					MetricName: public.MetricLiveCaptionsWindowDropped,
				}, false); err != nil {
					slog.Error("processLiveCaptionsForTrack: error sending wsEvMetric MetricLiveCaptionsWindowDropped",
						slog.String("err", err.Error()),
						slog.String("trackID", ctx.trackID))
				}
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
			removeShortSpeeches(segments)
			cleaned := cleanAudio(window, segments)

			// Before sending off data to be transcribed, check if new data is silence.
			// If it is silence, don't send it off.
			newDataIsSilence, windowFinished := checkSilence(segments, prevTranscribedPos)
			if windowFinished {
				window = window[:0]
				prevTranscribedPos = 0
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
			case t.transcriberQueueCh <- pkg:
				break
			default:
				if err := t.client.SendWs(wsEvMetric, public.MetricMsg{
					SessionID:  ctx.sessionID,
					MetricName: public.MetricLiveCaptionsTranscriberBufFull,
				}, false); err != nil {
					slog.Error("processLiveCaptionsForTrack: error sending wsEvMetric MetricTranscriberBufFull",
						slog.String("err", err.Error()),
						slog.String("trackID", ctx.trackID))
				}
				close(transcribedCh)
			}

			// While audio is being transcribed, we need to cut down the window if it's > maxWindowSize.
			window, prevTranscribedPos = cutWindowToSize(ctx.trackID, window, segments, prevTranscribedPos)
			prevWindowLen = len(window)

			// Use a for loop and a select so that we can drop ticks waiting for the transcriber.
		waitForTranscription:
			for {
				select {
				case <-ticker.C:
					slog.Debug("processLiveCaptionsForTrack: dropped a tick waiting for the transcriber",
						slog.String("trackID", ctx.trackID))
				case text := <-transcribedCh:
					if len(text) == 0 {
						// Either transcribedCh was closed above (captionQueueCh full), or audio transcription failed.
						// Note: this appears to happen when the transcriber fails to decode a block of audio.
						// Usually the probability returned for the language is very low, which makes sense.
						slog.Debug("processLiveCaptionsForTrack: received empty text, ignoring.")
						break waitForTranscription
					}
					if err := t.client.SendWs(wsEvCaption, public.CaptionMsg{
						SessionID:     ctx.sessionID,
						UserID:        ctx.user.Id,
						Text:          text,
						NewAudioLenMs: float64(newAudioLenMs),
					}, false); err != nil {
						slog.Error("processLiveCaptionsForTrack: error sending ws captions",
							slog.String("err", err.Error()),
							slog.String("trackID", ctx.trackID))
					}

					break waitForTranscription
				}
			}
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
func removeShortSpeeches(segments []segmentSamples) {
	for i, seg := range segments {
		if !seg.Silence && (seg.End-seg.Start) < minSpeechLengthSamples {
			segments[i].Silence = true
		}
	}
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
		} else {
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
	}
	return window, prevTranscribedPos
}

func (t *Transcriber) startTranscriberPool() {
	for i := 0; i < t.cfg.LiveCaptionsNumTranscribers; i++ {
		t.transcriberWg.Add(1)
		go t.handleTranscriptionRequests(i)
	}
}

func (t *Transcriber) handleTranscriptionRequests(num int) {
	slog.Debug(fmt.Sprintf("live captions, handleTranscriptionRequests: starting transcriber #%d", num))

	transcriber, err := t.newLiveCaptionsTranscriber()
	if err != nil {
		slog.Error("live captions, handleTranscriptionRequests: failed to create transcriber",
			slog.String("err", err.Error()))
		return
	}
	defer func() {
		err := transcriber.Destroy()
		if err != nil {
			slog.Error("live captions, handleTranscriptionRequests: failed to destroy transcriber",
				slog.String("err", err.Error()))
		}
		t.transcriberWg.Done()
	}()

	for {
		select {
		case <-t.transcriberDoneCh:
			slog.Debug(fmt.Sprintf("live captions, handleTranscriptionRequests: closing transcriber #%d", num))
			return
		case packet := <-t.transcriberQueueCh:
			transcribed, _, err := transcriber.Transcribe(packet.pcm)
			if err != nil {
				slog.Error("live captions, handleTranscriptionRequests: failed to transcribe audio samples",
					slog.String("err", err.Error()))
				packet.retCh <- ""
				return
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
	case config.TranscribeAPIWhisperCPP:
		return whisper.NewContext(whisper.Config{
			ModelFile:     filepath.Join(getModelsDir(), fmt.Sprintf("ggml-%s.bin", string(t.cfg.LiveCaptionsModelSize))),
			NumThreads:    t.cfg.LiveCaptionsNumThreadsPerTranscriber,
			NoContext:     true, // do not use previous translations as context for next translation: https://github.com/ggerganov/whisper.cpp/pull/141#issuecomment-1321225563
			AudioContext:  512,  // a bit more than 10seconds: https://github.com/ggerganov/whisper.cpp/pull/141#issuecomment-1321230379
			PrintProgress: false,
			Language:      "en",
			SingleSegment: true,
		})
	default:
		return nil, fmt.Errorf("transcribe API %q not implemented", t.cfg.TranscribeAPI)
	}
}
