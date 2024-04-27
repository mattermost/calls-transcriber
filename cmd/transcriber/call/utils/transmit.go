package utils

import (
	"log"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mattermost/rtcd/client"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

func TransmitAudio(c *client.Client, pktsCh <-chan []byte, outTrack *webrtc.TrackLocalStaticSample, active *atomic.Bool) error {
	go func() {
		frameDuration := time.Millisecond * 20
		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()
		var lastPktTime time.Time
		muted := true
		for range ticker.C {
			select {
			case pkt, ok := <-pktsCh:
				if !ok {
					log.Printf("transmit done")
					return
				}

				if muted {
					if err := c.Unmute(outTrack); err != nil {
						log.Printf("failed to unmute: %s", err.Error())
					} else {
						muted = false
					}
				}

				if err := outTrack.WriteSample(media.Sample{Data: pkt, Duration: frameDuration}); err != nil {
					log.Printf("failed to write audio sample: %s", err.Error())
				}

				lastPktTime = time.Now()
			default:
				if !muted && time.Since(lastPktTime) > 10*time.Second {
					slog.Debug("deactivating after timeout")
					active.Store(false)
					if err := c.Mute(); err != nil {
						log.Printf("failed to unmute: %s", err.Error())
					} else {
						muted = true
					}
				}
			}
		}
	}()

	return nil
}
