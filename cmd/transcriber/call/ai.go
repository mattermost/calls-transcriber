package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/call/utils"

	"github.com/mattermost/rtcd/client"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/pion/webrtc/v3"
)

var aiActivationKeywords = []string{
	"copilot",
	"co-pilot",
	"pilot",
	"cortana", // Hilarious, Azure AI is clearly tuned for this word, so we add it.
}

var aiDeactivationKeywords = []string{
	"please mute",
	"mute please",
	"stop please",
	"please stop",
}

func containsString(input string, list []string) bool {
	for _, str := range list {
		if strings.Contains(input, str) {
			return true
		}
	}
	return false
}

func (t *Transcriber) summonAI(authToken string, stopCh <-chan struct{}) {
	c, err := client.New(client.Config{
		SiteURL:   t.cfg.SiteURL,
		AuthToken: authToken,
		ChannelID: t.cfg.CallID,
	})
	if err != nil {
		slog.Error("failed to create calls clients", slog.String("err", err.Error()))
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

	outTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:     "audio/opus",
		ClockRate:    48000,
		Channels:     2,
		SDPFmtpLine:  "minptime=10;useinbandfec=1",
		RTCPFeedback: nil,
	}, "audio", "voice_"+model.NewId())
	if err != nil {
		slog.Error("failed to create out track", slog.String("err", err.Error()))
		return
	}

	postToAI := func(post *model.Post) (*model.Post, error) {
		apiURL := fmt.Sprintf("%s/plugins/%s/bot/calls/%s/post-ai", t.apiClient.URL, pluginID, t.cfg.CallID)
		payload, err := json.Marshal(post)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal: %w", err)
		}

		ctx, cancelCtx := context.WithTimeout(context.Background(), httpRequestTimeout)
		defer cancelCtx()
		resp, err := t.apiClient.DoAPIRequestBytes(ctx, http.MethodPost, apiURL, payload, "")
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		var newPost *model.Post
		if err := json.NewDecoder(resp.Body).Decode(&newPost); err != nil {
			return nil, fmt.Errorf("failed to unmarshal: %w", err)
		}

		return newPost, nil
	}

	aiPost, err := postToAI(&model.Post{Message: "This is the start of the in-call conversation"})
	if err != nil {
		slog.Error("failed to post to AI", slog.String("err", err.Error()))
		return
	}

	slog.Info("AI post created", slog.String("postID", aiPost.Id))

	speakCh := make(chan string, 10)

	var prevMsg string

	if err := t.client.On(client.WSAIPostUpdateEvent, func(ctx any) error {
		data, ok := ctx.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected context type")
		}

		msg, _ := data["next"].(string)
		postID, _ := data["post_id"].(string)

		slog.Info("ai post update!", slog.String("message", msg), slog.String("postID", postID))

		if prevMsg != "" && msg == "" {
			select {
			case speakCh <- prevMsg:
				slog.Debug("msg sent!", slog.String("msg", prevMsg))
			default:
				slog.Error("failed to write on textCh")
			}
			prevMsg = ""
		} else if msg != "" {
			prevMsg = msg
		}

		return nil
	}); err != nil {
		slog.Error("failed to subscribe", slog.String("err", err.Error()))
		return
	}

	var active atomic.Bool
	active.Store(false)

	if err := c.On(client.RTCTrackEvent, func(ctx any) error {
		m, ok := ctx.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected context type")
		}

		track, ok := m["track"].(*webrtc.TrackRemote)
		if !ok {
			return fmt.Errorf("unexpected track type")
		}

		if track.Codec().MimeType != webrtc.MimeTypeOpus {
			slog.Debug("ignoring non voice track", slog.String("trackID", track.ID()))
			receiver, ok := m["receiver"].(*webrtc.RTPReceiver)
			if !ok {
				return fmt.Errorf("unexpected receiver type")
			}
			return receiver.Stop()
		}

		_, sessionID, err := client.ParseTrackID(track.ID())
		if err != nil {
			return fmt.Errorf("failed to parse track ID: %w", err)
		}

		speakingUser, err := t.getUserForSession(sessionID)
		if err != nil {
			return fmt.Errorf("failed to get user for session: %w", err)
		}

		if speakingUser.Username == "ai" {
			slog.Debug("skipping our own track", slog.String("trackID", track.ID()))
			return nil
		}

		decodedCh, err := utils.DecodeTrack(track)
		if err != nil {
			return fmt.Errorf("failed to decode track: %w", err)
		}

		transcribedCh, err := utils.TranscribeAudio(decodedCh, t.cfg.TranscribeAPIOptions)
		if err != nil {
			return fmt.Errorf("failed to transcribe audio: %w", err)
		}

		synthesizedCh, err := utils.SynthesizeText(speakCh, stopCh, t.cfg.TranscribeAPIOptions)
		if err != nil {
			return fmt.Errorf("failed to synthesize text: %w", err)
		}

		encodedCh, err := utils.EncodeAudio(synthesizedCh)
		if err != nil {
			return fmt.Errorf("failed to encode audio: %w", err)
		}

		err = utils.TransmitAudio(c, encodedCh, outTrack, &active)
		if err != nil {
			return fmt.Errorf("failed to transmit audio: %w", err)
		}

		for text := range transcribedCh {
			slog.Debug("transcribed: " + text)

			if !active.Load() && containsString(strings.ToLower(text), aiActivationKeywords) {
				slog.Debug("activation keyword triggered")
				active.Store(true)
			}

			if active.Load() && containsString(strings.ToLower(text), aiDeactivationKeywords) {
				slog.Debug("deactivation keyword triggered")
				active.Store(false)
				if err := c.Mute(); err != nil {
					slog.Error("failed to mute", slog.String("err", err.Error()))
				}
			}

			if active.Load() {
				msg := text
				msg += " Please keep it brief as if you were speaking in a call. Also please don't output emojis."
				post := &model.Post{Message: msg, RootId: aiPost.Id, UserId: speakingUser.Id}
				post.AddProp("activate_ai", true)
				if _, err := postToAI(post); err != nil {
					slog.Error("failed to post to AI", slog.String("err", err.Error()))
				}
			}
		}

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
