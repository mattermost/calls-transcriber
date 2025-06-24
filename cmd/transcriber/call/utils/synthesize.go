package utils

import (
	"fmt"
	"log"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/azure"
)

func SynthesizeText(transcribedCh <-chan string, stopCh <-chan struct{}, opts map[string]any) (<-chan []int16, error) {
	speechKey, _ := opts["AZURE_SPEECH_KEY"].(string)
	speechRegion, _ := opts["AZURE_SPEECH_REGION"].(string)
	ss, err := azure.NewSpeechSynthesizer(azure.SpeechSynthesizerConfig{
		SpeechKey:    speechKey,
		SpeechRegion: speechRegion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create speech synthesizer: %w", err)
	}

	synthesizedCh, err := ss.SynthesizeAsync(transcribedCh)
	if err != nil {
		return nil, fmt.Errorf("failed to synthesize: %w", err)
	}

	go func() {
		<-stopCh
		if err := ss.Destroy(); err != nil {
			log.Printf("failed to destroy synthesizer: %s", err.Error())
		}
	}()

	return synthesizedCh, nil
}
