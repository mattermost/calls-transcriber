package utils

import (
	"log"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
)

const (
	sendMTU                = 1200
	audioLevelExtensionURI = "urn:ietf:params:rtp-hdrext:ssrc-audio-level"
)

func TransmitAudio(pktsCh <-chan []byte, outTrack *webrtc.TrackLocalStaticRTP,
	getSender func() *webrtc.RTPSender, isActive func() bool, setActive func(val bool)) error {
	go func() {
		packetizer := rtp.NewPacketizer(
			sendMTU,
			0,
			0,
			&codecs.OpusPayloader{},
			rtp.NewRandomSequencer(),
			48000,
		)

		frameDuration := time.Millisecond * 20
		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()
		var lastPktTime time.Time
		var audioLevelExtensionID *int

		var audioLevel rtp.AudioLevelExtension
		audioLevelBase := 10
		audioLevelDev := 60

		var pkts uint64

		sendPkt := func(pkt []byte) {
			packets := packetizer.Packetize(pkt, uint32(48000*frameDuration.Seconds()))
			audioLevelData, err := audioLevel.Marshal()
			if err != nil {
				log.Printf("failed to marshal audio level: %s", err.Error())
			}
			for _, p := range packets {
				audioLevel.Level = uint8(audioLevelBase)
				if pkts%2 == 0 {
					audioLevel.Level = uint8(audioLevelBase + audioLevelDev)
				}

				if err := p.SetExtension(uint8(*audioLevelExtensionID), audioLevelData); err != nil {
					log.Printf("failed to set audio level extension: %s", err.Error())
				}

				if err := outTrack.WriteRTP(p); err != nil {
					log.Printf("failed to write rtp pkt: %s", err.Error())
				}
				pkts++
			}
		}

		var resetVAD bool

		for range ticker.C {
			select {
			case pkt, ok := <-pktsCh:
				if !ok {
					log.Printf("transmit done")
					return
				}

				if isActive() {
					if audioLevelExtensionID == nil {
						for _, ext := range getSender().GetParameters().HeaderExtensions {
							if ext.URI == audioLevelExtensionURI {
								log.Printf("found audioLevelExtensionURI")
								audioLevelExtensionID = new(int)
								*audioLevelExtensionID = ext.ID
								break
							}
						}
					}
					sendPkt(pkt)
					resetVAD = true

					// we keep it active if the bot has more to speak
					setActive(true)
				} else {
					log.Printf("inactive: skipping packet")
				}

				lastPktTime = time.Now()
			default:
				// This is just to trick our VAD algorithm to trigger.
				if resetVAD && time.Since(lastPktTime) > 500*time.Millisecond {
					audioLevelDev = 0
					lastPktTime = time.Now()
					resetVAD = false
					for i := 0; i < 60; i++ {
						sendPkt([]byte{0x00})
					}
					audioLevelDev = 60
				}
			}
		}
	}()

	return nil
}
