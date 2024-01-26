package call

import (
	"fmt"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/whisper.cpp"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"
	"github.com/streamer45/silero-vad-go/speech"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// TODO: these need to be in cfg and env var settable, and in proper style.
	parallelTranscribersPool = 4
	threadsPerTranscriber    = 1

	chunkSizeInMs            = 1000
	maxWindowSizeInMs        = 10000
	audioDataChannelBuffer   = 30 // so we don't block while processing the window's vad
	removeWindowAfterSilence = 3 * time.Second

	// VAD settings
	vadWindowSizeInSamples  = 512
	vadThreshold            = 0.5
	vadMinSilenceDurationMs = 300
	vadMinSpeechDurationMs  = 200
	vadSilencePadMs         = 32
)

// TODO: belongs in calls-common?
type CaptionMsg struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	Text      string `json:"text"`
}

type captionPackage struct {
	pcm []float32
	ret chan string
}

func (t *Transcriber) processLiveCaptionsForTrack(ctx trackContext, incomingAudio <-chan []float32, done <-chan struct{}) {
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
		slog.Error("live captions: failed to create speech detector",
			slog.String("err", err.Error()))
	}
	defer func() {
		if err := sd.Destroy(); err != nil {
			slog.Error("live captions: failed to destroy speech detector", slog.String("err", err.Error()))
		}
		slog.Debug("live captions: finished processing live captions",
			slog.String("trackID", ctx.trackID))
	}()

	// set capacity to our expected window size (because we gather window + 1 tick
	// before discarding the oldest segment, and ticks can vary a little bit, so be safe)
	windowCap := (maxWindowSizeInMs + 2*chunkSizeInMs) * trackOutAudioRate / 1000
	window := make([]float32, 0, windowCap)
	windowMut := sync.RWMutex{}
	windowGoalSize := maxWindowSizeInMs * trackOutAudioRate / 1000
	removeWindowSilenceInSamples := removeWindowAfterSilence.Milliseconds() * trackOutAudioRate / 1000

	prevWindowLen := 0
	var prevAudioAt time.Time
	prevTranscribedPos := 0

	readTrackPCM := func() {
		for pcm := range incomingAudio {
			windowMut.Lock()
			window = append(window, pcm...)
			windowMut.Unlock()
		}
	}
	go readTrackPCM()

	ticker := time.NewTicker(chunkSizeInMs * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			func() {
				// Note: intentional; the lock's purpose is to block the readTrackPCM goroutine
				// from adding new audio.
				// If we don't want to hold onto the lock during translation, we're going to have to
				// manually unlock on every exit, so that we can unlock manually after we're done modifying
				// window (we're done just before we send off `cleaned` to the transcriber pool).
				windowMut.Lock()
				defer windowMut.Unlock()

				// If we don't have enough samples, ignore the window.
				if len(window) < vadWindowSizeInSamples {
					return
				}

				// If there hasn't been any new pcm added, don't re-transcribe.
				if len(window) == prevWindowLen {
					// And clear the window if we haven't had new data (window is stale, don't re-transcribe)
					if time.Since(prevAudioAt) > removeWindowAfterSilence {
						window = window[:0]
					}
					return
				}
				prevAudioAt = time.Now()
				prevWindowLen = len(window)

				// Algorithm summary:
				// - get a cleaned version of the voice (with zeroes where no voice is detected)
				// - and a list of segments of contiguous speech or silence
				// - if window goes over its limit, we drop the oldest segments until it's below the limit
				// - transcribe the whole window
				// - Don't transcribe if data hasn't increased.
				// - Don't transcribe if new (un-transcribed) data is silence.
				// - Send the transcription to the server.

				cleaned, segments, err := sd.DetectRealtime(window)
				if err != nil {
					slog.Error("live captions: vad failed", slog.String("err", err.Error()))
					// TODO: error out properly?
					return
				}

				fmt.Printf("<><> cleaned len: %d, num segments: %d ", len(cleaned), len(segments))
				for i, s := range segments {
					fmt.Printf("\n%d: Start: \t%d,\tEnd: %d,\tSilent?: \t%v", i, s.Start, s.End, s.Silence)
				}
				fmt.Printf("\tprevTranscribePos: %d\n", prevTranscribedPos)

				// Only transcribe up to windowGoalSize length.
				// This is a bit complicated, because we don't want to cut old speech in the middle
				// of a word -- that will cause trouble for whisper. So cut by segment (oldest first).
				// Depending on how wide you make the silence gaps, this might cut then entire window
				// if the speaker doesn't take breaths between words...
				// So consider guarding against that. Maybe fallback to cutting in the middle, to prevent the run-on sentence from disappearing completely .
				for len(cleaned) > windowGoalSize {
					if len(segments) == 0 {
						slog.Error("live captions: we have zero segments in the window. Should not be possible.",
							slog.String("trackID", ctx.trackID))
					} else {
						var oldestSegment speech.RealtimeSegment
						oldestSegment, segments = segments[0], segments[1:]
						var cutUpTo int
						if len(segments) == 0 {
							// We don't have a complete next segment yet: cut from end of oldest segment.
							fmt.Printf("<><> cut from oldest.End  Start: %d End: %d\n", oldestSegment.Start, oldestSegment.End)
							cutUpTo = oldestSegment.End
						} else {
							// Cut up to start of segment we're keeping.
							fmt.Printf("<><> cut from start of segment we're keeping. Start: %d End: %d\n", segments[0].Start, segments[0].End)
							cutUpTo = segments[0].Start
						}
						if cutUpTo > len(cleaned) {
							fmt.Printf("<><> cutUpTo: %d > len(cleaned) %d", cutUpTo, len(cleaned))
							cutUpTo = len(cleaned)
						}
						if cutUpTo > len(window) {
							fmt.Printf("<><> cutUpTo: %d > len(window) %d", cutUpTo, len(window))
							cutUpTo = len(window)
						}
						cleaned = cleaned[cutUpTo:]
						window = window[cutUpTo:]

						// Adjust our marker for where we've transcribed.
						// e.g., prevTranscribedPos was 10, we've cut 6, new pos is 10 - 6 = 4.
						prevTranscribedPos -= cutUpTo
						prevTranscribedPos = max(prevTranscribedPos, 0) // defensive; shouldn't happen
					}
				}

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
						if silenceLength >= int(removeWindowSilenceInSamples) {
							// 1. untranscribed data is all silence, and there's been enough silence to end this window.
							fmt.Printf("segLen: %d, remove after: %d, clearing window!\n", silenceLength, removeWindowSilenceInSamples)
							window = window[:0]
							prevTranscribedPos = 0
							return
						}
						// 2. all new (untranscribed) data is silence, so don't send to transcriber.
						fmt.Printf("-- all untranscribed data is silence ** not sending to transcriber\n")
						return
					}
				}

				// Track our new position and send off data for transcription
				prevTranscribedPos = len(cleaned)
				transcribed := make(chan string)
				t.captionQueue <- captionPackage{
					pcm: cleaned,
					ret: transcribed,
				}

				for {
					select {
					case <-ticker.C:
						// TODO: add metrics for this.
						slog.Debug("live captions: dropped a tick waiting for the transcriber",
							slog.String("trackID", ctx.trackID))
					case text := <-transcribed:
						if err := t.client.Send(wsEvPrefix+"caption", CaptionMsg{
							SessionID: ctx.sessionID,
							UserID:    ctx.user.Id,
							Text:      text,
						}, false); err != nil {
							slog.Error("live captions: error sending ws captions",
								slog.String("err", err.Error()),
								slog.String("trackID", ctx.trackID))
						}

						return
					}
				}
			}()
		}
	}
}

func (t *Transcriber) startTranscriberPool() {
	slog.Debug("live captions: starting transcriber pool")

	// Setup the transcribers
	for i := 0; i < parallelTranscribersPool; i++ {
		go t.handleTranscriptionRequests(i)
	}
}

func (t *Transcriber) handleTranscriptionRequests(num int) {
	slog.Debug(fmt.Sprintf("live captions: starting transcriber #%d", num))
	t.captionWg.Add(1)

	transcriber, err := t.newLiveCaptionsTranscriber()
	if err != nil {
		slog.Error("live captions: failed to create transcriber",
			slog.String("err", err.Error()))
		return
	}
	defer func() {
		err := transcriber.Destroy()
		if err != nil {
			slog.Error("live captions: failed to destroy transcriber",
				slog.String("err", err.Error()))
		}
		t.captionWg.Done()
	}()

	for {
		select {
		case <-t.captionDoneCh:
			slog.Debug(fmt.Sprintf("live captions: closing transcriber #%d", num))
			return
		case packet := <-t.captionQueue:
			// TODO: a context with expiry for this?
			transcribed, _, err := transcriber.Transcribe(packet.pcm)
			if err != nil {
				slog.Error("live captions: failed to transcribe audio samples",
					slog.String("err", err.Error()))
			}

			var text []string
			for _, s := range transcribed {
				text = append(text, s.Text)
			}
			packet.ret <- strings.Join(text, " ")
		}
	}
}

func (t *Transcriber) newLiveCaptionsTranscriber() (transcribe.Transcriber, error) {
	switch t.cfg.TranscribeAPI {
	case config.TranscribeAPIWhisperCPP:
		return whisper.NewContext(whisper.Config{
			ModelFile:     filepath.Join(getModelsDir(), fmt.Sprintf("ggml-%s.bin", string(t.cfg.ModelSize))),
			NumThreads:    threadsPerTranscriber,
			NoContext:     true, // do not use previous translations as context for next translation: https://github.com/ggerganov/whisper.cpp/pull/141#issuecomment-1321225563
			AudioContext:  512,  // a bit more than 10seconds: https://github.com/ggerganov/whisper.cpp/pull/141#issuecomment-1321230379
			PrintProgress: false,
		})
	default:
		return nil, fmt.Errorf("transcribe API %q not implemented", t.cfg.TranscribeAPI)
	}
}
