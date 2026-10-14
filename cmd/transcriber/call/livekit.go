package call

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// liveKitJobState mirrors the plugin's JobStateClient, which is used both in
// the room metadata and in the call state returned by POST /livekit-token.
type liveKitJobState struct {
	StartAt int64 `json:"start_at"`
	EndAt   int64 `json:"end_at"`
}

// liveKitJobs holds the job states carried by the room metadata and call state.
// Only active jobs are present: a job that ended or failed is omitted.
type liveKitJobs struct {
	Recording     *liveKitJobState `json:"recording,omitempty"`
	Transcription *liveKitJobState `json:"transcription,omitempty"`
}

type liveKitSession struct {
	SessionID string      `json:"session_id"`
	Token     string      `json:"token"`
	URL       string      `json:"url"`
	CallState liveKitJobs `json:"call_state"`
}

// createLiveKitSession joins the call through the plugin's HTTP API. The
// request is authorized by the bot token and gated by the transcription job ID.
// The plugin creates the bot's call session and returns its ID along with a
// subscribe-only LiveKit token and the bot-specific LiveKit URL.
func (t *Transcriber) createLiveKitSession(ctx context.Context) (*liveKitSession, error) {
	reqURL := fmt.Sprintf("%s/plugins/%s/livekit-token", t.cfg.SiteURL, pluginID)
	body, err := json.Marshal(map[string]string{
		"channel_id": t.cfg.CallID,
		"job_id":     t.cfg.TranscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal livekit session request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpRequestTimeout)
	defer cancel()

	resp, err := t.apiClient.DoAPIRequestBytes(reqCtx, http.MethodPost, reqURL, body, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create livekit session: %w", err)
	}
	defer resp.Body.Close()

	var session liveKitSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to decode livekit session response: %w", err)
	}
	if session.URL == "" || session.Token == "" {
		return nil, fmt.Errorf("invalid livekit session response: empty url or token")
	}

	return &session, nil
}
