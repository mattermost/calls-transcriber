package utils

import (
	"log/slog"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TransmitAudio(pktsCh <-chan []byte, outTrack *webrtc.TrackLocalStaticRTP, packetizer rtp.Packetizer) error {
	go func() {
		frameDuration := time.Millisecond * 20
		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()

		sendPkt := func(pkt []byte) {
			packets := packetizer.Packetize(pkt, uint32(48000*frameDuration.Seconds()))
			for _, p := range packets {
				if err := outTrack.WriteRTP(p); err != nil {
					slog.Error("failed to write RTP packet", slog.String("trackID", outTrack.ID()), slog.String("error", err.Error()))
				}
			}
		}

		for range ticker.C {
			pkt, ok := <-pktsCh
			if !ok {
				slog.Debug("pktsCh closed, stopping transmission", slog.String("trackID", outTrack.ID()))
				return
			}

			sendPkt(pkt)
		}
	}()

	return nil
}
