package azure

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
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
	DataDir      string
}

func (c SpeechRecognizerConfig) IsValid() error {
	if c.SpeechKey == "" {
		return fmt.Errorf("invalid SpeechKey: should not be empty")
	}

	if c.SpeechRegion == "" {
		return fmt.Errorf("invalid SpeechRegion: should not be empty")
	}

	if c.DataDir == "" {
		return fmt.Errorf("invalid DataDir: should not be empty")
	}

	return nil
}

type SpeechRecognizer struct {
	cfg SpeechRecognizerConfig

	speechConfig     *speech.SpeechConfig
	speechRecognizer *speech.SpeechRecognizer
	audioStream      *audio.PushAudioInputStream
	audioConfig      *audio.AudioConfig
}

func initSpeechRecognizer(speechConfig *speech.SpeechConfig) (*speech.SpeechRecognizer, *audio.AudioConfig, *audio.PushAudioInputStream, error) {
	audioStream, err := audio.CreatePushAudioInputStream()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create audio stream: %w", err)
	}

	audioConfig, err := audio.NewAudioConfigFromStreamInput(audioStream)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create audio config: %w", err)
	}

	speechRecognizer, err := speech.NewSpeechRecognizerFromConfig(speechConfig, audioConfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create speech recognizer: %w", err)
	}

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
	speechRecognizer.Recognizing(func(event speech.SpeechRecognitionEventArgs) {
		defer event.Close()
		slog.Info("recognizing", slog.Any("result", event.Result))
	})

	return speechRecognizer, audioConfig, audioStream, nil
}

func NewSpeechRecognizer(cfg SpeechRecognizerConfig) (*SpeechRecognizer, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	speechConfig, err := speech.NewSpeechConfigFromSubscription(cfg.SpeechKey, cfg.SpeechRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech config: %w", err)
	}
	if err := speechConfig.SetProperty(common.SpeechLogFilename, filepath.Join(cfg.DataDir, "azure.log")); err != nil {
		return nil, fmt.Errorf("failed to set log property: %w", err)
	}

	speechRecognizer, audioConfig, audioStream, err := initSpeechRecognizer(speechConfig)
	if err != nil {
		return nil, err
	}

	sr := &SpeechRecognizer{
		cfg:              cfg,
		speechConfig:     speechConfig,
		speechRecognizer: speechRecognizer,
		audioConfig:      audioConfig,
		audioStream:      audioStream,
	}

	return sr, nil
}

func (s *SpeechRecognizer) TranscribeAsync(samplesCh <-chan []float32) (<-chan transcribe.Segment, error) {
	segmentsCh := make(chan transcribe.Segment, 1)
	s.speechRecognizer.Recognized(func(event speech.SpeechRecognitionEventArgs) {
		defer event.Close()

		if event.Result.Reason == common.NoMatch {
			slog.Error("no match")
			return
		}

		if event.Result.Reason == common.Canceled {
			slog.Error("canceled")
			return
		}

		if len(event.Result.Text) == 0 {
			slog.Error("empty result")
			return
		}

		segmentsCh <- transcribe.Segment{
			Text:    event.Result.Text,
			StartTS: int64(event.Result.Offset.Seconds() * 1000),
			EndTS:   int64(event.Result.Offset.Seconds()*1000 + event.Result.Duration.Seconds()*1000),
		}
	})

	err := <-s.speechRecognizer.StartContinuousRecognitionAsync()
	if err != nil {
		return nil, fmt.Errorf("failed to start recognizer: %w", err)
	}

	go func() {
		defer func() {
			err := <-s.speechRecognizer.StopContinuousRecognitionAsync()
			if err != nil {
				slog.Error("failed to stop recognizer", slog.String("err", err.Error()))
			}
			defer close(segmentsCh)
		}()

		for samples := range samplesCh {
			if err := s.audioStream.Write(f32PCMToWAV(samples)); err != nil {
				slog.Error("failed to write audio data", slog.String("err", err.Error()))
				break
			}
		}
	}()

	return segmentsCh, nil
}

func (s *SpeechRecognizer) Transcribe(samples []float32) ([]transcribe.Segment, string, error) {
	// TODO: we should likely re-use the same session throughout a track transcription to optimize
	// resources a bit.
	//
	// NOTE: the underlying Golang wrapper is currently a bit bugged. Re-using the client is recommended
	// but it doesn't work properly because everything relies on a stream which can't be flushed which can
	// lead to data loss. And if we close the stream then we need to re-initialize everything like we do.
	//
	// A better solution may be to extend the Transcriber interface and pass an audio reader to this method
	// instead of the chunks we create since we are dealing with post-transcript.

	inputDuration := time.Duration(float32(len(samples))/float32(audioSampleRate)) * time.Second

	speechRecognizer, audioConfig, audioStream, err := initSpeechRecognizer(s.speechConfig)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize recognizer: %w", err)
	}

	defer func() {
		audioStream.CloseStream()
		audioConfig.Close()
		speechRecognizer.Close()
	}()

	resultsCh := make(chan speech.SpeechRecognitionResult, 1)
	errCh := make(chan error, 1)
	speechRecognizer.Recognized(func(event speech.SpeechRecognitionEventArgs) {
		defer event.Close()

		if event.Result.Reason == common.NoMatch {
			errCh <- fmt.Errorf("no match")
			return
		}

		if event.Result.Reason == common.Canceled {
			slog.Debug("canceled")
			return
		}

		if len(event.Result.Text) == 0 {
			slog.Warn("empty result")
			return
		}

		slog.Info("transcription completed", slog.Any("result", event.Result), slog.Duration("inputDuration", inputDuration))

		resultsCh <- event.Result
	})

	eosCh := make(chan struct{})
	speechRecognizer.Canceled(func(event speech.SpeechRecognitionCanceledEventArgs) {
		defer event.Close()
		slog.Info("transcription canceled", slog.String("details", event.ErrorDetails), slog.Any("reason", event.Reason), slog.Any("code", event.ErrorCode))
		if event.Reason == common.EndOfStream {
			close(eosCh)
		} else if event.Reason == common.Error {
			errCh <- errors.New(event.ErrorDetails)
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

	if err := audioStream.Write(f32PCMToWAV(samples)); err != nil {
		return nil, "", fmt.Errorf("failed to write audio data: %w", err)
	}

	// This is important as it flushes out any remaining audio data.
	audioStream.CloseStream()

	timeoutCh := time.After(max(inputDuration*2, 10*time.Second))

	var segments []transcribe.Segment
	for {
		select {
		case result := <-resultsCh:
			segment := transcribe.Segment{
				Text:    result.Text,
				StartTS: int64(result.Offset.Seconds() * 1000),
				EndTS:   int64(result.Offset.Seconds()*1000 + result.Duration.Seconds()*1000),
			}
			segments = append(segments, segment)
		case <-timeoutCh:
			return nil, "", fmt.Errorf("timed out waiting for transcription")
		case err := <-errCh:
			return nil, "", fmt.Errorf("transcription failed: %w", err)
		case <-eosCh:
			slog.Info("done transcribing, returning segments", slog.Int("numSegments", len(segments)))
			return segments, "", nil
		}
	}
}

func (s *SpeechRecognizer) Destroy() error {
	if s.audioStream != nil {
		s.audioStream.CloseStream()
	}

	if s.audioConfig != nil {
		s.audioConfig.Close()
	}

	if s.speechRecognizer != nil {
		err := <-s.speechRecognizer.StopContinuousRecognitionAsync()
		if err != nil {
			slog.Error("failed to stop recognizer", slog.String("err", err.Error()))
		}
		s.speechRecognizer.Close()
	}

	if s.speechConfig != nil {
		s.speechConfig.Close()
	}

	return nil
}
