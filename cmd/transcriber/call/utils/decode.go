package utils

import (
	"fmt"
	"log"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/opus"
	"github.com/pion/rtp"
)

const (
	decodedChSize = 10
)

func DecodeTrackPkts(pkts <-chan *rtp.Packet) (<-chan []float32, error) {
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
		for pkt := range pkts {
			n, err := opusDec.Decode(pkt.Payload, pcm)
			if err != nil {
				log.Printf("failed to decode packet: %s", err.Error())
				continue
			}

			samples = append(samples, pcm[:n]...)

			// We buffer up to 500ms
			if len(samples) >= 8000 {
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
