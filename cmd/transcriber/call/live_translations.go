package call

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/call/utils"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/rtcd/client"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

const sendMTU = 1200

func (t *Transcriber) translateTrack(c *client.Client, tctx *trackCtx, targetLang string, stopCh <-chan struct{}) error {
	pktsCh := make(chan *rtp.Packet, 1)
	go func() {
		defer close(pktsCh)
		for {
			select {
			case pkt, ok := <-tctx.pktsCh:
				if !ok {
					return
				}
				select {
				case pktsCh <- pkt:
				default:
					slog.Warn("failed to send packet on pktsCh, dropping packet", slog.String("trackID", tctx.track.ID()))
				}
			case <-stopCh:
				return
			}
		}
	}()

	decodedCh, err := utils.DecodeTrackPkts(pktsCh)
	if err != nil {
		return fmt.Errorf("failed to decode track: %w", err)
	}

	t.cfg.TranscribeAPIOptions["AZURE_SPEECH_OUTPUT_LANGUAGE"] = targetLang
	if _, ok := t.cfg.TranscribeAPIOptions["AZURE_SPEECH_INPUT_LANGUAGE"]; !ok {
		t.cfg.TranscribeAPIOptions["AZURE_SPEECH_INPUT_LANGUAGE"] = ""
	}

	translatedCh, err := utils.TranslateAudio(decodedCh, stopCh, t.cfg.TranscribeAPIOptions, t.dataPath)
	if err != nil {
		return fmt.Errorf("failed to translate audio: %w", err)
	}

	encodedCh, err := utils.EncodeAudio(translatedCh)
	if err != nil {
		return fmt.Errorf("failed to encode audio: %w", err)
	}

	if err := c.Unmute(tctx.outTrack); err != nil {
		return fmt.Errorf("failed to unmute output track: %w", err)
	}

	err = utils.TransmitAudio(encodedCh, tctx.outTrack, tctx.packetizer)
	if err != nil {
		return fmt.Errorf("failed to transmit audio: %w", err)
	}

	<-stopCh

	if err := c.Mute(); err != nil {
		return fmt.Errorf("failed to mute output track: %w", err)
	}

	return nil
}

type translationState struct {
	sessionID  string
	targetLang string
	stopCh     chan struct{}
}

type trackCtx struct {
	// input
	track  *webrtc.TrackRemote
	pktsCh <-chan *rtp.Packet

	// output
	outTrack   *webrtc.TrackLocalStaticRTP
	packetizer rtp.Packetizer
}

func (t *Transcriber) startLiveTranslations(stopCh <-chan struct{}) {
	defer t.liveTranslationsWg.Done()
	var mut sync.Mutex
	translations := make(map[string]*translationState)
	ctxs := make(map[string]*trackCtx)

	c, err := client.New(client.Config{
		SiteURL:   t.cfg.SiteURL,
		AuthToken: t.cfg.AuthToken,
		ChannelID: t.cfg.CallID,
		JobID:     t.cfg.TranscriptionID,
	})
	if err != nil {
		slog.Error("failed to create calls clients", slog.String("err", err.Error()))
		return
	}

	translateTrack := func(tctx *trackCtx, tr *translationState) {
		slog.Debug("starting translation for track", slog.String("sessionID", tr.sessionID), slog.String("targetLang", tr.targetLang))
		if err := t.translateTrack(c, tctx, tr.targetLang, tr.stopCh); err != nil {
			slog.Error("failed to translate track", slog.String("err", err.Error()))
			mut.Lock()
			delete(translations, tr.sessionID)
			mut.Unlock()
		}
	}

	err = t.client.On(client.WSStartLiveTranslationEvent, func(ctx any) error {
		m, ok := ctx.(map[string]string)
		if !ok {
			return fmt.Errorf("unexpected context type for live translation start")
		}

		sessionID := m["target_session_id"]
		targetLang := m["target_language"]

		if sessionID == "" || targetLang == "" {
			return fmt.Errorf("missing session ID or target language in live translation start event")
		}

		mut.Lock()
		defer mut.Unlock()

		tr := translations[sessionID]
		if tr != nil && tr.targetLang == targetLang {
			slog.Debug("translation already started for session", slog.String("sessionID", sessionID), slog.String("targetLang", targetLang))
			return nil
		}

		if tr != nil {
			slog.Debug("stopping existing translation for session", slog.String("sessionID", sessionID), slog.String("targetLang", tr.targetLang))
			close(tr.stopCh)
		}

		tr = &translationState{
			sessionID:  sessionID,
			targetLang: targetLang,
			stopCh:     make(chan struct{}),
		}

		translations[sessionID] = tr

		tctx := ctxs[sessionID]
		if tctx != nil {
			go translateTrack(tctx, tr)
		}

		return nil
	})
	if err != nil {
		slog.Error("failed to subscribe to live translation start event", slog.String("err", err.Error()))
		return
	}

	err = t.client.On(client.WSStopLiveTranslationEvent, func(ctx any) error {
		m, ok := ctx.(map[string]string)
		if !ok {
			return fmt.Errorf("unexpected context type for live translation start")
		}

		sessionID := m["target_session_id"]
		if sessionID == "" {
			return fmt.Errorf("missing session ID in live translation stop event")
		}

		mut.Lock()
		defer mut.Unlock()

		tr := translations[sessionID]
		if tr == nil {
			slog.Debug("no translation found for session", slog.String("sessionID", sessionID))
			return nil
		}

		slog.Debug("stopping translation for session", slog.String("sessionID", sessionID), slog.String("targetLang", tr.targetLang))
		close(tr.stopCh)
		delete(translations, sessionID)

		return nil
	})
	if err != nil {
		slog.Error("failed to subscribe to live translation stop event", slog.String("err", err.Error()))
		return
	}

	if err := c.On(client.RTCConnectEvent, func(_ any) error {
		slog.Debug("client connected")
		return nil
	}); err != nil {
		slog.Error("failed to subscribe", slog.String("err", err.Error()))
		return
	}

	if err := c.On(client.CloseEvent, func(_ any) error {
		slog.Info("client close")
		return nil
	}); err != nil {
		slog.Error("failed to subscribe", slog.String("err", err.Error()))
		return
	}

	if err := c.On(client.RTCTrackEvent, func(ctx any) error {
		m, ok := ctx.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected context type")
		}

		track, ok := m["track"].(*webrtc.TrackRemote)
		if !ok {
			return fmt.Errorf("unexpected track type")
		}

		receiver, ok := m["receiver"].(*webrtc.RTPReceiver)
		if !ok {
			return fmt.Errorf("unexpected receiver type")
		}

		trackType, sessionID, err := client.ParseTrackID(track.ID())
		if err != nil {
			slog.Error("failed to parse track ID", slog.String("err", err.Error()))
			return receiver.Stop()
		}

		if trackType != client.TrackTypeVoice {
			slog.Debug("ignoring non voice track", slog.String("trackID", track.ID()))
			return receiver.Stop()
		}

		if track.Codec().MimeType != webrtc.MimeTypeOpus {
			slog.Warn("ignoring unsupported mimetype for track", slog.String("mimeType", track.Codec().MimeType), slog.String("trackID", track.ID()))
			return receiver.Stop()
		}

		speakingUser, err := t.getUserForSession(sessionID)
		if err != nil {
			slog.Error("failed to get user for session", slog.String("sessionID", sessionID), slog.String("err", err.Error()))
			return receiver.Stop()
		}

		if speakingUser.Username == "calls" {
			slog.Debug("skipping our own track", slog.String("trackID", track.ID()))
			return receiver.Stop()
		}

		outTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
			MimeType:     "audio/opus",
			ClockRate:    48000,
			Channels:     2,
			SDPFmtpLine:  "minptime=10;useinbandfec=1",
			RTCPFeedback: nil,
		}, "audio", "voice_"+model.NewId())
		if err != nil {
			return fmt.Errorf("failed to create output track: %w", err)
		}

		packetizer := rtp.NewPacketizer(
			sendMTU,
			0,
			0,
			&codecs.OpusPayloader{},
			rtp.NewRandomSequencer(),
			48000,
		)

		mut.Lock()
		defer mut.Unlock()

		tracksPktsCh := utils.ReadTrack(track)
		tctx := &trackCtx{
			track:      track,
			pktsCh:     tracksPktsCh,
			outTrack:   outTrack,
			packetizer: packetizer,
		}
		ctxs[sessionID] = tctx

		tr := translations[sessionID]
		if tr == nil {
			return nil
		}

		go translateTrack(tctx, tr)

		return nil
	}); err != nil {
		slog.Error("failed to subscribe", slog.String("err", err.Error()))
		return
	}

	if err := c.Connect(); err != nil {
		slog.Error("failed to connect", slog.String("err", err.Error()))
		return
	}

	<-stopCh

	if err := c.Close(); err != nil {
		slog.Error("failed to close client on stop", slog.String("err", err.Error()))
	}
}
