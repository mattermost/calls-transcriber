package utils

import (
	"errors"
	"io"
	"log/slog"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func ReadTrack(track *webrtc.TrackRemote) <-chan *rtp.Packet {
	trackPktsCh := make(chan *rtp.Packet, 1)

	go func() {
		defer close(trackPktsCh)

		for {
			pkt, _, err := track.ReadRTP()
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				slog.Error("failed to read track", slog.String("trackID", track.ID()), slog.String("err", err.Error()))
				continue
			}

			if len(pkt.Payload) == 0 {
				continue
			}

			select {
			case trackPktsCh <- pkt:
			default:
				// It's okay to drop packets if there's no reader on trackPktsCh (i.e. translations are off).
			}
		}
	}()

	return trackPktsCh
}
