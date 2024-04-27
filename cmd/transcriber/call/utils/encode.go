package utils

import (
	"fmt"
	"log"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
)

const (
	encodedChSize = 1000
)

func EncodeAudio(samplesCh <-chan []int16) (<-chan []byte, error) {
	encoder, err := opus.NewEncoder(48000, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}

	encodedCh := make(chan []byte, encodedChSize)
	go func() {
		defer func() {
			close(encodedCh)
			if err := encoder.Destroy(); err != nil {
				log.Printf("failed to destroy encoder: %s", err.Error())
			}
		}()
		for samples := range samplesCh {
			frameSize := 960

			for i := 0; i < len(samples); i += frameSize {
				data := make([]byte, 1024)
				n, err := encoder.Encode(samples[i:], data, frameSize)
				if err != nil {
					log.Printf("failed to encode samples: %s", err.Error())
				}

				select {
				case encodedCh <- data[:n]:
				default:
					log.Printf("failed to send on encodedCh")
				}
			}
		}
	}()

	return encodedCh, nil
}
