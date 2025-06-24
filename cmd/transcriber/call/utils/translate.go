package utils

import (
	"fmt"
	"log"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/apis/azure"
)

func TranslateAudio(samplesCh <-chan []float32, stopCh <-chan struct{}, opts map[string]any, dataDir string) (<-chan []int16, error) {
	speechKey, _ := opts["AZURE_SPEECH_KEY"].(string)
	speechRegion, _ := opts["AZURE_SPEECH_REGION"].(string)
	ss, err := azure.NewSpeechTranslator(azure.SpeechTranslatorConfig{
		SpeechKey:      speechKey,
		SpeechRegion:   speechRegion,
		InputLanguage:  opts["AZURE_SPEECH_INPUT_LANGUAGE"].(string),
		OutputLanguage: opts["AZURE_SPEECH_OUTPUT_LANGUAGE"].(string),
		DataDir:        dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create speech translator: %w", err)
	}

	synthesizedCh, err := ss.TranslateAsync(samplesCh)
	if err != nil {
		return nil, fmt.Errorf("failed to translate: %w", err)
	}

	// synthesizedCh, err = SynthesizeText(ss.RecognizedCh, stopCh, opts)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to synthesize: %w", err)
	// }

	go func() {
		<-stopCh
		if err := ss.Destroy(); err != nil {
			log.Printf("failed to destroy translator: %s", err.Error())
		}
	}()

	return synthesizedCh, nil
}
