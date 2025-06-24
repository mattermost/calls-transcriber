package azure

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/common"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
)

type SpeechTranslatorConfig struct {
	SpeechKey      string
	SpeechRegion   string
	InputLanguage  string
	OutputLanguage string
	DataDir        string
}

var recognizedTime atomic.Pointer[time.Time]

func (c SpeechTranslatorConfig) IsValid() error {
	if c.SpeechKey == "" {
		return fmt.Errorf("invalid SpeechKey: should not be empty")
	}

	if c.SpeechRegion == "" {
		return fmt.Errorf("invalid SpeechRegion: should not be empty")
	}

	if c.DataDir == "" {
		return fmt.Errorf("invalid DataDir: should not be empty")
	}

	// InputLanguage can be empty, in which case it will be autodetected.

	if c.OutputLanguage == "" {
		return fmt.Errorf("invalid OutputLanguage: should not be empty")
	}

	return nil
}

type SpeechTranslator struct {
	cfg SpeechTranslatorConfig

	config      *speech.SpeechTranslationConfig
	recognizer  *speech.TranslationRecognizer
	audioStream *audio.PushAudioInputStream
	audioConfig *audio.AudioConfig
	langConfig  *speech.AutoDetectSourceLanguageConfig

	RecognizedCh chan string
}

func initSpeechTranslator(config *speech.SpeechTranslationConfig, autoDetectLang bool) (*speech.TranslationRecognizer, *speech.AutoDetectSourceLanguageConfig, *audio.AudioConfig, *audio.PushAudioInputStream, error) {
	audioStream, err := audio.CreatePushAudioInputStream()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to create audio stream: %w", err)
	}

	audioConfig, err := audio.NewAudioConfigFromStreamInput(audioStream)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to create audio config: %w", err)
	}

	var langConfig *speech.AutoDetectSourceLanguageConfig
	var recognizer *speech.TranslationRecognizer
	if autoDetectLang {
		langConfig, err = speech.NewAutoDetectSourceLanguageConfigFromOpenRange()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to create auto detect source language config: %w", err)
		}

		recognizer, err = speech.NewTranslationRecognizerFromAutoDetectSourceLangConfig(config, langConfig, audioConfig)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to create speech recognizer: %w", err)
		}
	} else {
		recognizer, err = speech.NewTranslationRecognizerFromConfig(config, audioConfig)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to create speech recognizer: %w", err)
		}
	}

	recognizer.SessionStarted(func(event speech.SessionEventArgs) {
		defer event.Close()
		slog.Debug("recognizer: session started", slog.String("sessionID", event.SessionID))
	})
	recognizer.SessionStopped(func(event speech.SessionEventArgs) {
		defer event.Close()
		slog.Debug("recognizer: session stopped", slog.String("sessionID", event.SessionID))
	})
	recognizer.Canceled(func(event speech.TranslationRecognitionCanceledEventArgs) {
		defer event.Close()
		slog.Info("recognizer: transcription canceled", slog.String("details", event.ErrorDetails))
	})
	recognizer.Recognizing(func(event speech.TranslationRecognitionEventArgs) {
		defer event.Close()
		slog.Info("recognizer: recognizing", slog.Any("result", event.Result))
	})

	return recognizer, langConfig, audioConfig, audioStream, nil
}

func NewSpeechTranslator(cfg SpeechTranslatorConfig) (*SpeechTranslator, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	config, err := speech.NewSpeechTranslationConfigFromSubscription(cfg.SpeechKey, cfg.SpeechRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech translation config: %w", err)
	}

	if err := config.SetProperty(common.SpeechLogFilename, filepath.Join(cfg.DataDir, "azure_translator.log")); err != nil {
		return nil, fmt.Errorf("failed to set log property: %w", err)
	}

	if cfg.InputLanguage != "" {
		slog.Debug("input language is set, setting speech recognition language", slog.String("inputLanguage", cfg.InputLanguage))
		if err := config.SetSpeechRecognitionLanguage(cfg.InputLanguage); err != nil {
			return nil, fmt.Errorf("failed to set speech recognition language: %w", err)
		}
	} else {
		slog.Debug("input language is not set, using auto-detection for speech recognition")
	}

	if err := config.AddTargetLanguage(cfg.OutputLanguage); err != nil {
		return nil, fmt.Errorf("failed to set speech target language: %w", err)
	}

	if err := config.SetVoiceName("en-US-AndrewMultilingualNeural"); err != nil {
		return nil, fmt.Errorf("failed to set speech voice name: %w", err)
	}

	if err := config.SetSpeechSynthesisOutputFormat(common.Raw48Khz16BitMonoPcm); err != nil {
		return nil, fmt.Errorf("failed to set speech output format: %w", err)
	}

	recognizer, langConfig, audioConfig, audioStream, err := initSpeechTranslator(config, cfg.InputLanguage == "")
	if err != nil {
		return nil, err
	}

	recognizedCh := make(chan string, 1)

	recognizer.Recognized(func(event speech.TranslationRecognitionEventArgs) {
		defer event.Close()
		slog.Info("recognizer: recognized", slog.Any("result", event.Result))

		if event.Result == nil {
			return
		}

		translated := event.Result.GetTranslation(cfg.OutputLanguage)
		if translated != "" {
			now := time.Now()
			recognizedTime.Store(&now)
		}

		// This would be needed to do manual synthesis (e.g. outputting to multiple languages simultaneously).
		// 	select {
		// 	case recognizedCh <- translated:
		// 	default:
		// 		slog.Error("recognizer: failed to send recognized text on channel")
		// 	}
		// }
	})

	sr := &SpeechTranslator{
		cfg:          cfg,
		config:       config,
		recognizer:   recognizer,
		audioConfig:  audioConfig,
		audioStream:  audioStream,
		langConfig:   langConfig,
		RecognizedCh: recognizedCh,
	}

	return sr, nil
}

func (s *SpeechTranslator) TranslateAsync(samplesCh <-chan []float32) (<-chan []int16, error) {
	synthesizedCh := make(chan []int16, 100)

	s.recognizer.Synthesizing(func(event speech.TranslationSynthesisEventArgs) {
		defer event.Close()

		if event.Result == nil {
			slog.Debug("recognizer: no result", slog.String("sessionID", event.SessionID))
			return
		}

		buf := event.Result.GetAudioData()

		slog.Debug("recognizer: synthesizing", slog.String("sessionID", event.SessionID), slog.Int("result", len(buf)))

		if len(buf) == 0 {
			// empty audio data.
			return
		}

		took := time.Since(*recognizedTime.Load())
		slog.Debug("recognizer: took to synthesize", slog.Duration("took", took))

		samples, err := wavToPCMInt16(buf)
		if err != nil {
			slog.Error("failed to convert WAV to PCM int16", slog.String("err", err.Error()))
			return
		}

		select {
		case synthesizedCh <- samples:
		default:
			slog.Error("failed to send on synthesizedCh")
		}
	})

	err := <-s.recognizer.StartContinuousRecognitionAsync()
	if err != nil {
		return nil, fmt.Errorf("failed to start recognizer: %w", err)
	}

	go func() {
		defer func() {
			err := <-s.recognizer.StopContinuousRecognitionAsync()
			if err != nil {
				slog.Error("failed to stop recognizer", slog.String("err", err.Error()))
			}
			defer close(synthesizedCh)
			defer close(s.RecognizedCh)
		}()

		for samples := range samplesCh {
			if err := s.audioStream.Write(f32PCMToWAV(samples)); err != nil {
				slog.Error("failed to write audio data", slog.String("err", err.Error()))
				break
			}
		}
	}()

	return synthesizedCh, nil
}

func (s *SpeechTranslator) Destroy() error {
	if s.audioStream != nil {
		s.audioStream.CloseStream()
	}

	if s.audioConfig != nil {
		s.audioConfig.Close()
	}

	if s.recognizer != nil {
		err := <-s.recognizer.StopContinuousRecognitionAsync()
		if err != nil {
			slog.Error("failed to stop recognizer", slog.String("err", err.Error()))
		}
		s.recognizer.Close()
	}

	if s.langConfig != nil {
		s.langConfig.Close()
	}

	if s.config != nil {
		s.config.Close()
	}

	return nil
}
