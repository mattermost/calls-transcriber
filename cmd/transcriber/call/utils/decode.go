package utils

import (
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"

	"github.com/pion/webrtc/v3"
)

const (
	decodedChSize = 10
)

func DecodeTrack(t *webrtc.TrackRemote) (<-chan []float32, error) {
	opusDec, err := opus.NewDecoder(16000, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create opus decoder: %w", err)
	}

	decodedCh := make(chan []float32, decodedChSize)

	go func() {
		defer func() {
			close(decodedCh)
			if err := opusDec.Destroy(); err != nil {
				log.Printf("failed to destroy encoder: %s", err.Error())
			}
		}()

		var samples []float32
		pcm := make([]float32, 320)
		for {
			pkt, _, err := t.ReadRTP()
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				log.Printf("failed to read track: %s", err.Error())
				continue
			}

			if len(pkt.Payload) == 0 {
				continue
			}

			n, err := opusDec.Decode(pkt.Payload, pcm)
			if err != nil {
				log.Printf("failed to decode packet: %s", err.Error())
				continue
			}

			samples = append(samples, pcm[:n]...)

			// We buffer up to 1 second
			if len(samples) >= 16000 {
				select {
				case decodedCh <- samples:
					samples = []float32{}
				default:
					log.Print("failed to send on decodedCh")
				}
			}
		}
	}()

	return decodedCh, nil
}
