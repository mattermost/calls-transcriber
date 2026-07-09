package call

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	// userIDSessionIDSeparator must match composeLivekitIdentity in the calls
	// plugin (server/livekit_admin.go), which encodes a participant's LiveKit
	// identity as "<userID>___<sessionID>".
	userIDSessionIDSeparator = "___"
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
func (t *Transcriber) fetchLiveKitToken(ctx context.Context, sessionID string) (string, string, error) {
	reqURL := fmt.Sprintf("%s/plugins/%s/livekit-token?channel_id=%s&session_id=%s",
		t.cfg.SiteURL, pluginID, url.QueryEscape(t.cfg.CallID), url.QueryEscape(sessionID))

	reqCtx, cancel := context.WithTimeout(ctx, httpRequestTimeout)
	defer cancel()

	resp, err := t.apiClient.DoAPIRequest(reqCtx, http.MethodGet, reqURL, "", "")
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch livekit token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("failed to decode livekit token response: %w", err)
	}
	if result.URL == "" || result.Token == "" {
		return "", "", fmt.Errorf("invalid livekit token response: empty url or token")
	}

	return result.URL, result.Token, nil
}
