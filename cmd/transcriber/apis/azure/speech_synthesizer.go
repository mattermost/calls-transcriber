package azure

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/common"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
)

type SpeechSynthesizerConfig struct {
	SpeechKey    string
	SpeechRegion string
	Language     string
}

func (c SpeechSynthesizerConfig) IsValid() error {
	if c.SpeechKey == "" {
		return fmt.Errorf("invalid SpeechKey: should not be empty")
	}

	if c.SpeechRegion == "" {
		return fmt.Errorf("invalid SpeechRegion: should not be empty")
	}

	return nil
}

type SpeechSynthesizer struct {
	cfg SpeechSynthesizerConfig

	speechConfig      *speech.SpeechConfig
	speechSynthesizer *speech.SpeechSynthesizer
	audioStream       *audio.PullAudioOutputStream
	audioConfig       *audio.AudioConfig
}

func NewSpeechSynthesizer(cfg SpeechSynthesizerConfig) (*SpeechSynthesizer, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	speechConfig, err := speech.NewSpeechConfigFromSubscription(cfg.SpeechKey, cfg.SpeechRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech config: %w", err)
	}

	// TODO: make it configurable
	if err := speechConfig.SetSpeechSynthesisVoiceName("es-ES-TristanMultilingualNeural"); err != nil {
		return nil, fmt.Errorf("failed to set speech voice name: %w", err)
	}

	if err := speechConfig.SetSpeechSynthesisOutputFormat(common.Raw16Khz16BitMonoPcm); err != nil {
		return nil, fmt.Errorf("failed to set speech output format: %w", err)
	}

	audioStream, err := audio.CreatePullAudioOutputStream()
	if err != nil {
		return nil, fmt.Errorf("failed to create audio stream: %w", err)
	}

	audioConfig, err := audio.NewAudioConfigFromStreamOutput(audioStream)
	if err != nil {
		return nil, fmt.Errorf("failed to create audio config: %w", err)
	}

	speechSynthesizer, err := speech.NewSpeechSynthesizerFromConfig(speechConfig, audioConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech synthesize: %w", err)
	}

	ss := &SpeechSynthesizer{
		cfg:               cfg,
		speechConfig:      speechConfig,
		speechSynthesizer: speechSynthesizer,
		audioStream:       audioStream,
		audioConfig:       audioConfig,
	}

	return ss, nil
}

func (s *SpeechSynthesizer) SynthesizeAsync(textCh <-chan string) (chan []int16, error) {
	synthesizedCh := make(chan []int16, 1000)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			buf, err := s.audioStream.Read(1920 * 2)
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				slog.Error("failed to read", slog.String("err", err.Error()))
				return
			}

			took := time.Since(*recognizedTime.Load())
			slog.Debug("recognizer: took to synthesize", slog.Duration("took", took))

			// Convert []byte to []int16 samples
			samples := make([]int16, len(buf)/2)
			for i := 0; i < len(samples); i++ {
				samples[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
			}

			select {
			case synthesizedCh <- samples:
			default:
				slog.Error("failed to send on synthesizedCh")
			}
		}
	}()

	go func() {
		defer close(synthesizedCh)
		for text := range textCh {
			s.speechSynthesizer.SpeakTextAsync(text)
		}
		wg.Wait()
	}()

	return synthesizedCh, nil
}

func (s *SpeechSynthesizer) Destroy() error {
	if s.audioStream != nil {
		s.audioStream.Close()
	}

	if s.audioConfig != nil {
		s.audioConfig.Close()
	}

	if s.speechSynthesizer != nil {
		s.speechSynthesizer.Close()
	}

	if s.speechConfig != nil {
		s.speechConfig.Close()
	}

	return nil
}
