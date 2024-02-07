package call

import (
	"fmt"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/whisper.cpp"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"
	"github.com/streamer45/silero-vad-go/speech"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	chunkSizeInMs            = 1000
	maxWindowSizeInMs        = 10000
	pktPayloadChBuffer       = 30
	removeWindowAfterSilence = 3 * time.Second

	// VAD settings
	vadWindowSizeInSamples  = 512
	vadThreshold            = 0.5
	vadMinSilenceDurationMs = 300
	vadMinSpeechDurationMs  = 200
	vadSilencePadMs         = 32
)

type CaptionMsg struct {
	SessionID     string  `json:"session_id"`
	UserID        string  `json:"user_id"`
	Text          string  `json:"text"`
	NewAudioLenMs float64 `json:"new_audio_len_ms"`
}

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
		MinSpeechDurationMs:  vadMinSpeechDurationMs,
		SilencePadMs:         vadSilencePadMs,
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

	// readTrackPktPayloads reads incoming pktPayload data from the track and converts it to PCM.
	// toBeTranscribed stores pcm data until it can be added to the window. The capacity is just an
	// guess of the outside amount of time we may be waiting between calls to the transcribing pool.
	// If it's not big enough, we may get a small hiccup while it resizes, but no big deal: it will only
	// affect the readTrackPktPayloads goroutine, and the channel it's reading from has a healthy buffer.
	toBeTranscribed := make([]float32, 0, 3*chunkSizeInMs*trackOutAudioSamplesPerMs)
	toBeTranslatedMut := sync.RWMutex{}
	pcmBuf := make([]float32, trackOutFrameSize)
	readTrackPktPayloads := func() {
		for payload := range pktPayloads {
			n, err := opusDec.Decode(payload, pcmBuf)
			if err != nil {
				slog.Error("failed to decode audio data for live captions",
					slog.String("err", err.Error()),
					slog.String("trackID", ctx.trackID))
			}

			toBeTranslatedMut.Lock()
			toBeTranscribed = append(toBeTranscribed, pcmBuf[:n]...)
			toBeTranslatedMut.Unlock()
		}
	}
	go readTrackPktPayloads()

	// set capacity to our expected window size (+2 chunks, because we gather window + 1 tick
	// before discarding the oldest segment, and ticks can vary a little bit, so be safe)
	windowCap := (maxWindowSizeInMs + 2*chunkSizeInMs) * trackOutAudioSamplesPerMs
	window := make([]float32, 0, windowCap)
	windowGoalSize := maxWindowSizeInMs * trackOutAudioSamplesPerMs
	removeWindowAfterSilenceSamples := removeWindowAfterSilence.Milliseconds() * trackOutAudioSamplesPerMs

	prevWindowLen := 0
	var prevAudioAt time.Time
	prevTranscribedPos := 0

	ticker := time.NewTicker(chunkSizeInMs * time.Millisecond)
	defer ticker.Stop()

	// Algorithm summary:
	// - Get a cleaned version of the voice (with zeroes where no voice is detected)
	// - And a list of segments of contiguous speech or silence
	// - If window goes over its limit, we drop the oldest segments until it's below the limit
	// - Don't transcribe if data hasn't increased.
	// - Don't transcribe if new (un-transcribed) data is silence.
	// - Send the cleaned data (the whole window) to the transcriber pool
	// - Wait for the transcription (let `tick`s pass so that we're only
	//   transcribing a particular track once at a time)
	// - Send the transcription to the plugin to be redistributed to clients.
	// - finish and wait for next `tick`

	for {
		select {
		case <-doneCh:
			return
		case <-ticker.C:
			toBeTranslatedMut.Lock()
			window = append(window, toBeTranscribed...)
			// track how long we were waiting until consuming the next batch of audio data, as a measure
			// of the pressure on the transcription process
			newAudioLenMs := len(toBeTranscribed) / trackOutAudioSamplesPerMs
			toBeTranscribed = toBeTranscribed[:0]
			toBeTranslatedMut.Unlock()

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
				}
				continue
			}
			prevAudioAt = time.Now()
			prevWindowLen = len(window)

			cleaned, segments, err := sd.DetectRealtime(window)
			if err != nil {
				slog.Error("live captions, processLiveCaptionsForTrack: vad failed", slog.String("err", err.Error()))
				continue
			}

			fmt.Printf("<><> cleaned len: %d, window lent: %d, num segments: %d, prevTranscribedPos: %d\n", len(cleaned), len(window), len(segments), prevTranscribedPos)
			// More detailed debugging:
			//for i, s := range segments {
			//	fmt.Printf("\n%d: Start: \t%d,\tEnd: %d,\tSilent?: \t%v", i, s.Start, s.End, s.Silence)
			//}

			// Before sending off data to be transcribed, check if new data is silence.
			// If it is, don't send it off.
			//
			// This is a little complicated because we might miss a tick (if the transcriber
			// takes > 1 tick to transcribe). That is why we are keeping prevTranscribedPos.
			// The goals are:
			// 1. Clear the window if new (untranscribed) data is silence,
			//    and silence > removeWindowAfterSilence.
			// 2. Do not send the window to the transcriber if all new (untranscribed) data is silence.

			prevtranscribedSeg := -1
			for i, seg := range segments {
				if prevTranscribedPos >= seg.Start && prevTranscribedPos < seg.End {
					prevtranscribedSeg = i
					break
				}
			}

			if prevtranscribedSeg >= 0 {
				allSilence := true
				for i := prevtranscribedSeg; i < len(segments); i++ {
					if !segments[i].Silence {
						allSilence = false
						break
					}
				}
				if allSilence {
					silenceLength := segments[len(segments)-1].End - segments[prevtranscribedSeg].Start
					if silenceLength >= int(removeWindowAfterSilenceSamples) {
						// 1. untranscribed data is all silence, and there's been enough silence to end this window.
						fmt.Printf("<><> all untranscribed data is silence, and segLen: %d > removeWindowAfterSilenceSamples: %d, therefore clearing window.\n", silenceLength, removeWindowAfterSilenceSamples)
						window = window[:0]
						prevTranscribedPos = 0
						prevWindowLen = 0
						continue
					}
					// 2. all new (untranscribed) data is silence, so don't send to the transcriber.
					fmt.Printf("<><> all untranscribed data is silence ** not sending to transcriber\n")
					continue
				}
			}

			// Track our new position and send off data for transcription
			// Note: if we were delayed (by a slow transcriber), this may cause us to translate a
			// longer-than expected block of voice. If we see this happening due to slowness,
			// adjust the windowGoalSize lower.
			prevTranscribedPos = len(cleaned)
			transcribedCh := make(chan string)
			t.captionQueue <- captionPackage{
				pcm:   cleaned,
				retCh: transcribedCh,
			}

			// While audio is being transcribed, we need to cut down the window if it's > windowGoalSize.
			// This is a bit complicated, because we don't want to cut old speech in the middle
			// of a word -- that will cause trouble for whisper. So cut by segment (oldest first).
			// Depending on how wide you make the silence gaps, this might cut then entire window
			// if the speaker doesn't take breaths between words...
			// So consider guarding against that. Maybe fallback to cutting in the middle,
			// to prevent starting the next part of a run-on sentence from zero.
			for len(cleaned) > windowGoalSize {
				if len(segments) == 0 {
					// Should not be possible, but instead of panic-ing, log an error.
					slog.Error("live captions, processLiveCaptionsForTrack: we have zero segments in the window. Should not be possible.",
						slog.String("trackID", ctx.trackID))
				} else {
					var oldestSegment speech.RealtimeSegment
					oldestSegment, segments = segments[0], segments[1:]
					var cutUpTo int
					if len(segments) == 0 {
						// We don't have a complete next segment yet: cut to end of oldest segment.
						fmt.Printf("<><> we don't have complete next segment yet, cutUpTo oldest.End: %d\n", oldestSegment.End)
						cutUpTo = oldestSegment.End
					} else {
						// Cut up to start of segment we're keeping.
						fmt.Printf("<><> cutUpTo start of segment we're keeping. Start: %d\n", segments[0].Start)
						cutUpTo = segments[0].Start
					}
					if cutUpTo > len(cleaned) {
						fmt.Printf("<><> ** cutUpTo: %d > len(cleaned) %d", cutUpTo, len(cleaned))
						cutUpTo = len(cleaned)
					}
					if cutUpTo > len(window) {
						fmt.Printf("<><> ** cutUpTo: %d > len(window) %d", cutUpTo, len(window))
						cutUpTo = len(window)
					}
					cleaned = cleaned[cutUpTo:]
					window = window[cutUpTo:]
					prevWindowLen = len(window)

					// Adjust our marker for where we've transcribed.
					// e.g., prevTranscribedPos was 10, we've cut 6, new pos is 10 - 6 = 4.
					prevTranscribedPos -= cutUpTo
				}
			}

		waitForTranscription:
			for {
				select {
				case <-ticker.C:
					slog.Debug("live captions, processLiveCaptionsForTrack: dropped a tick waiting for the transcriber",
						slog.String("trackID", ctx.trackID))
				case text := <-transcribedCh:
					if err := t.client.SendWs(wsEvPrefix+"caption", CaptionMsg{
						SessionID:     ctx.sessionID,
						UserID:        ctx.user.Id,
						Text:          text,
						NewAudioLenMs: float64(newAudioLenMs),
					}, false); err != nil {
						slog.Error("live captions, processLiveCaptionsForTrack: error sending ws captions",
							slog.String("err", err.Error()),
							slog.String("trackID", ctx.trackID))
					}

					break waitForTranscription
				}
			}
		}
	}
}

func (t *Transcriber) startTranscriberPool() {
	for i := 0; i < t.cfg.LiveCaptionsNumTranscribers; i++ {
		t.captionWg.Add(1)
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
		t.captionWg.Done()
	}()

	for {
		select {
		case <-t.captionDoneCh:
			slog.Debug(fmt.Sprintf("live captions, handleTranscriptionRequests: closing transcriber #%d", num))
			return
		case packet := <-t.captionQueue:
			transcribed, _, err := transcriber.Transcribe(packet.pcm)
			if err != nil {
				slog.Error("live captions, handleTranscriptionRequests: failed to transcribe audio samples",
					slog.String("err", err.Error()))
			}

			var text []string
			for _, s := range transcribed {
				text = append(text, s.Text)
			}
			packet.retCh <- strings.Join(text, " ")
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
		})
	default:
		return nil, fmt.Errorf("transcribe API %q not implemented", t.cfg.TranscribeAPI)
	}
}
