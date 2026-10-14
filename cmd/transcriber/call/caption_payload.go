package call

import (
	"encoding/json"
	"unicode/utf8"
)

const (
	// captionDataTopic is the LiveKit data message topic captions are published on.
	// The payload format has no version field: additions must be backward
	// compatible, a breaking change would use a new topic.
	captionDataTopic = "caption"

	// maxCaptionTextBytes caps the caption text carried in a single data message.
	maxCaptionTextBytes = 1024
)

// captionPayload is the JSON body of a caption data message. It must match what
// the Calls webapp expects to receive.
type captionPayload struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// truncateToBytes returns s cut to at most maxBytes bytes, never splitting a
// UTF-8 sequence.
func truncateToBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// buildCaptionPayload returns the data message payload for a caption spoken in
// the given session, truncating the text to maxCaptionTextBytes.
func buildCaptionPayload(sessionID, text string) ([]byte, error) {
	return json.Marshal(captionPayload{
		SessionID: sessionID,
		Text:      truncateToBytes(text, maxCaptionTextBytes),
	})
}
