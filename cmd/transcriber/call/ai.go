package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

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

	outTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
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

	ctx, cancelCtx := context.WithTimeout(context.Background(), httpRequestTimeout)
	aiUser, _, err := t.apiClient.GetUserByUsername(ctx, "ai", "")
	if err != nil {
		cancelCtx()
		slog.Error("failed to get AI user", slog.String("err", err.Error()))
		return
	}
	cancelCtx()

	aiPost, err := postToAI(&model.Post{Message: "What follows in this thread is a real time transcription of the current call you are in."})
	if err != nil {
		slog.Error("failed to post to AI", slog.String("err", err.Error()))
		return
	}

	slog.Info("AI post created", slog.String("postID", aiPost.Id))

	speakCh := make(chan string, 10)
	var prevMsg string
	var currentAIPostID string
	if err := t.client.On(client.WSGenericEvent, func(ctx any) error {
		ev, ok := ctx.(*model.WebSocketEvent)
		if !ok {
			return fmt.Errorf("unexpected context type")
		}

		switch ev.EventType() {
		case "posted":
			data := ev.GetData()
			postData, _ := data["post"].(string)

			var post model.Post
			if err := json.Unmarshal([]byte(postData), &post); err != nil {
				slog.Error("failed to unmarshal post", slog.String("err", err.Error()))
				break
			}

			if post.RootId == aiPost.Id && post.UserId == aiUser.Id {
				slog.Debug("setting current AI post id", slog.String("id", post.Id))
				currentAIPostID = post.Id
			}
		case "custom_mattermost-ai_postupdate":
			data := ev.GetData()
			msg, _ := data["next"].(string)
			postID, _ := data["post_id"].(string)

			if postID != currentAIPostID {
				break
			}

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
		default:
			slog.Info(string(ev.EventType()))
			return nil
		}

		return nil
	}); err != nil {
		slog.Error("failed to subscribe", slog.String("err", err.Error()))
		return
	}

	var active atomic.Pointer[time.Time]
	active.Store(&time.Time{})
	isActive := func() bool {
		return !active.Load().IsZero()
	}
	setActive := func(val bool) {
		if val {
			active.Store(newTimeP(time.Now()))
		} else {
			active.Store(&time.Time{})
		}
	}

	go func() {
		ticker := time.NewTicker(time.Second)
		for {
			select {
			case <-ticker.C:
				if isActive() && time.Since(*active.Load()) > 30*time.Second {
					slog.Info("deactivating after timeout")
					setActive(false)
					if err := c.Mute(); err != nil {
						slog.Error("failed to mute", slog.String("err", err.Error()))
					}
				}
			case <-stopCh:
				return
			}
		}
	}()

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
			slog.Debug("ignoring non voice track", slog.String("trackID", track.ID()))
			return receiver.Stop()
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

		var sender *webrtc.RTPSender
		err = utils.TransmitAudio(encodedCh, outTrack, func() *webrtc.RTPSender {
			return sender
		}, isActive, setActive)
		if err != nil {
			return fmt.Errorf("failed to transmit audio: %w", err)
		}

		go func() {
			for text := range transcribedCh {
				slog.Debug("transcribed: " + text)

				// keep it active if more text is transcribed while already active.
				if isActive() {
					setActive(true)
				}

				if !isActive() && containsString(strings.ToLower(text), aiActivationKeywords) {
					slog.Debug("activation keyword triggered")
					sender, err = c.Unmute(outTrack)
					if err != nil {
						slog.Error("failed to unmute", slog.String("err", err.Error()))
					} else {
						setActive(true)
					}
				}

				if containsString(strings.ToLower(text), aiDeactivationKeywords) {
					slog.Debug("deactivation keyword triggered")
					setActive(false)
					if err := c.Mute(); err != nil {
						slog.Error("failed to mute", slog.String("err", err.Error()))
					}
				}

				if isActive() {
					msg := fmt.Sprintf("You are speaking in a call. Please try to keep it brief. Also please don't output emojis or other special characters. %s is speaking to you what follows:\n", speakingUser.GetDisplayName(model.ShowFullName))

					msg += text
					post := &model.Post{Message: msg, RootId: aiPost.Id, UserId: speakingUser.Id}
					post.AddProp("activate_ai", true)
					if _, err := postToAI(post); err != nil {
						slog.Error("failed to post to AI", slog.String("err", err.Error()))
					}
				} else {
					post := &model.Post{Message: text, RootId: aiPost.Id, UserId: speakingUser.Id}
					if _, err := postToAI(post); err != nil {
						slog.Error("failed to post to AI", slog.String("err", err.Error()))
					}
				}
			}
		}()

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
