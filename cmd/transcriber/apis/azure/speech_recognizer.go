package azure

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/common"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
)

const (
	audioSampleRate = 16000
	audioBitDepth   = 16
	audioChannels   = 1
)

type SpeechRecognizerConfig struct {
	SpeechKey    string
	SpeechRegion string
	Language     string
}

func (c SpeechRecognizerConfig) IsValid() error {
	if c.SpeechKey == "" {
		return fmt.Errorf("invalid SpeechKey: should not be empty")
	}

	if c.SpeechRegion == "" {
		return fmt.Errorf("invalid SpeechRegion: should not be empty")
	}

	return nil
}

type SpeechRecognizer struct {
	cfg SpeechRecognizerConfig
}

func NewSpeechRecognizer(cfg SpeechRecognizerConfig) (*SpeechRecognizer, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return &SpeechRecognizer{
		cfg: cfg,
	}, nil
}

func (s *SpeechRecognizer) Transcribe(samples []float32) ([]transcribe.Segment, string, error) {
	// TODO: we should likely re-use the same session throughout a track transcription to optimize
	// resources a bit.

	cfg, err := speech.NewSpeechConfigFromSubscription(s.cfg.SpeechKey, s.cfg.SpeechRegion)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create speech config: %w", err)
	}
	defer cfg.Close()

	stream, err := audio.CreatePushAudioInputStream()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create audio stream: %w", err)
	}
	defer stream.Close()

	audioConfig, err := audio.NewAudioConfigFromStreamInput(stream)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create audio config: %w", err)
	}

	speechRecognizer, err := speech.NewSpeechRecognizerFromConfig(cfg, audioConfig)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create speech recognizer: %w", err)
	}
	defer speechRecognizer.Close()

	speechRecognizer.SessionStarted(func(event speech.SessionEventArgs) {
		defer event.Close()
		slog.Debug("session started", slog.String("sessionID", event.SessionID))
	})
	speechRecognizer.SessionStopped(func(event speech.SessionEventArgs) {
		defer event.Close()
		slog.Debug("session stopped", slog.String("sessionID", event.SessionID))
	})

	speechRecognizer.Canceled(func(event speech.SpeechRecognitionCanceledEventArgs) {
		defer event.Close()
		slog.Info("transcription canceled", slog.String("details", event.ErrorDetails))
	})

	if err := stream.Write(f32PCMToWAV(samples)); err != nil {
		return nil, "", fmt.Errorf("failed to write audio data: %w", err)
	}

	speechRecognizer.Recognizing(func(event speech.SpeechRecognitionEventArgs) {
		defer event.Close()
		slog.Info("recognizing", slog.Any("result", event.Result))
	})

	segmentsCh := make(chan []transcribe.Segment, 1)
	errCh := make(chan error, 1)

	speechRecognizer.Recognized(func(event speech.SpeechRecognitionEventArgs) {
		defer event.Close()

		if event.Result.Reason == common.NoMatch {
			errCh <- fmt.Errorf("no match")
			return
		}

		if event.Result.Reason == common.Canceled {
			errCh <- fmt.Errorf("canceled")
			return
		}

		slog.Info("transcription completed", slog.Any("result", event.Result), slog.Any("inputLen", float32(len(samples))/float32(audioSampleRate)))

		segmentsCh <- []transcribe.Segment{
			{
				Text:    event.Result.Text,
				StartTS: int64(event.Result.Offset.Seconds() * 1000),
				EndTS:   int64(event.Result.Offset.Seconds()*1000 + event.Result.Duration.Seconds()*1000),
			},
		}
	})

	err = <-speechRecognizer.StartContinuousRecognitionAsync()
	if err != nil {
		return nil, "", fmt.Errorf("failed to start recognizer: %w", err)
	}
	defer func() {
		err := <-speechRecognizer.StopContinuousRecognitionAsync()
		if err != nil {
			slog.Error("failed to stop recognizer", slog.String("err", err.Error()))
		}
	}()

	// This is important as it flushes out any remaining audio data.
	stream.CloseStream()

	select {
	case segments := <-segmentsCh:
		slog.Info("returning segments")
		return segments, "", nil
	case <-time.After(10 * time.Second):
		return nil, "", fmt.Errorf("timed out waiting for transcription")
	case <-errCh:
		return nil, "", fmt.Errorf("transcription failed: %w", err)
	}
}

func (s *SpeechRecognizer) Destroy() error {
	return nil
}
