package utils

import (
	"fmt"
	"log"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/azure"
)

const (
	transcribedChSize = 10
)

func TranscribeAudio(decodedCh <-chan []float32, opts map[string]any) (chan string, error) {
	speechKey, _ := opts["AZURE_SPEECH_KEY"].(string)
	speechRegion, _ := opts["AZURE_SPEECH_REGION"].(string)

	tr, err := azure.NewSpeechRecognizer(azure.SpeechRecognizerConfig{
		SpeechKey:    speechKey,
		SpeechRegion: speechRegion,
		Language:     "en",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create speech recognizer: %w", err)
	}

	segmentsCh, err := tr.TranscribeAsync(decodedCh)
	if err != nil {
		return nil, fmt.Errorf("failed to transcribe: %w", err)
	}

	transcribedCh := make(chan string, transcribedChSize)
	go func() {
		defer func() {
			close(transcribedCh)
			if err := tr.Destroy(); err != nil {
				log.Printf("failed to destroy transcriber: %s", err.Error())
			}
		}()
		for segment := range segmentsCh {
			select {
			case transcribedCh <- segment.Text:
			default:
				log.Printf("failed to send on transcribedCh")
			}
		}
	}()

	return transcribedCh, nil
}
