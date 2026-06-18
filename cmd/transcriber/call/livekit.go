package call

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	// userIDSessionIDSeparator must match composeLivekitIdentity in the calls
	// plugin (server/livekit_admin.go), which encodes a participant's LiveKit
	// identity as "<userID>___<sessionID>".
	userIDSessionIDSeparator = "___"

	livekitTokenRetryAttempts = 5
	livekitTokenRetryWaitTime = time.Second
)

// parseLivekitIdentity splits a LiveKit participant identity produced by the
// calls plugin's composeLivekitIdentity back into its userID and sessionID
// parts. This is the LiveKit equivalent of the rtcd client's ParseTrackID:
// with LiveKit the session/user is carried by the participant identity rather
// than the track ID.
func parseLivekitIdentity(identity string) (userID, sessionID string, err error) {
	parts := strings.SplitN(identity, userIDSessionIDSeparator, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("unexpected identity format: %q", identity)
	}
	return parts[0], parts[1], nil
}

// fetchLiveKitToken requests a subscribe-only LiveKit access token (and the
// bot-specific LiveKit URL) for the bot's call session from the plugin. The
// bot's authorization is established by the JobID-gated join performed before
// this call, which registers the call session referenced by sessionID.
//
// The join is handled asynchronously server-side, so the token endpoint may
// briefly fail to find the session; we retry a handful of times.
func (t *Transcriber) fetchLiveKitToken(ctx context.Context, sessionID string) (lkURL, token string, err error) {
	reqURL := fmt.Sprintf("%s/plugins/%s/livekit-token?channel_id=%s&session_id=%s",
		t.cfg.SiteURL, pluginID, t.cfg.CallID, sessionID)

	for attempt := 0; attempt < livekitTokenRetryAttempts; attempt++ {
		if attempt > 0 {
			slog.Warn("fetchLiveKitToken failed, retrying",
				slog.Int("attempt", attempt),
				slog.String("err", err.Error()))
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(livekitTokenRetryWaitTime):
			}
		}

		lkURL, token, err = t.doFetchLiveKitToken(ctx, reqURL)
		if err == nil {
			return lkURL, token, nil
		}
	}

	return "", "", fmt.Errorf("failed to fetch livekit token: %w", err)
}

func (t *Transcriber) doFetchLiveKitToken(ctx context.Context, reqURL string) (string, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, httpRequestTimeout)
	defer cancel()

	resp, err := t.apiClient.DoAPIRequest(reqCtx, http.MethodGet, reqURL, "", "")
	if err != nil {
		return "", "", fmt.Errorf("failed to request token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("failed to decode token response: %w", err)
	}
	if result.URL == "" || result.Token == "" {
		return "", "", fmt.Errorf("invalid token response: empty url or token")
	}

	return result.URL, result.Token, nil
}
