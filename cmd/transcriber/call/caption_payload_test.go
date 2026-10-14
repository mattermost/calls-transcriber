package call

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTruncateToBytes(t *testing.T) {
	t.Run("shorter than limit", func(t *testing.T) {
		require.Equal(t, "hello", truncateToBytes("hello", 10))
	})

	t.Run("exactly the limit", func(t *testing.T) {
		require.Equal(t, "hello", truncateToBytes("hello", 5))
	})

	t.Run("ascii over the limit", func(t *testing.T) {
		require.Equal(t, "hel", truncateToBytes("hello", 3))
	})

	t.Run("zero limit", func(t *testing.T) {
		require.Equal(t, "", truncateToBytes("hello", 0))
	})

	t.Run("empty", func(t *testing.T) {
		require.Equal(t, "", truncateToBytes("", 5))
	})

	t.Run("does not split a multibyte rune", func(t *testing.T) {
		// "é" is 2 bytes, "世" is 3 bytes, "😀" is 4 bytes.
		s := "aé世😀"
		require.Equal(t, 10, len(s))

		tcs := []struct {
			maxBytes int
			expected string
		}{
			{10, "aé世😀"},
			{9, "aé世"},
			{6, "aé世"},
			{5, "aé"},
			{4, "aé"},
			{3, "aé"},
			{2, "a"},
			{1, "a"},
			{0, ""},
		}
		for _, tc := range tcs {
			got := truncateToBytes(s, tc.maxBytes)
			require.Equal(t, tc.expected, got, "maxBytes=%d", tc.maxBytes)
			require.True(t, utf8.ValidString(got))
		}
	})
}

func TestBuildCaptionPayload(t *testing.T) {
	t.Run("shape", func(t *testing.T) {
		data, err := buildCaptionPayload("sess1", "hello world")
		require.NoError(t, err)
		require.JSONEq(t, `{"session_id":"sess1","text":"hello world"}`, string(data))
	})

	t.Run("round trips special characters", func(t *testing.T) {
		text := "she said \"hi\" <b>\n世界 😀"
		data, err := buildCaptionPayload("sess1", text)
		require.NoError(t, err)

		var p captionPayload
		require.NoError(t, json.Unmarshal(data, &p))
		require.Equal(t, "sess1", p.SessionID)
		require.Equal(t, text, p.Text)
	})

	t.Run("text at the limit is kept", func(t *testing.T) {
		text := strings.Repeat("a", maxCaptionTextBytes)
		data, err := buildCaptionPayload("sess1", text)
		require.NoError(t, err)

		var p captionPayload
		require.NoError(t, json.Unmarshal(data, &p))
		require.Equal(t, text, p.Text)
	})

	t.Run("long ascii text is truncated", func(t *testing.T) {
		data, err := buildCaptionPayload("sess1", strings.Repeat("a", maxCaptionTextBytes*3))
		require.NoError(t, err)

		var p captionPayload
		require.NoError(t, json.Unmarshal(data, &p))
		require.Len(t, p.Text, maxCaptionTextBytes)
	})

	t.Run("long multibyte text is truncated on a rune boundary", func(t *testing.T) {
		// 3-byte runes: 1024 is not a multiple of 3, so the cut lands mid-rune.
		data, err := buildCaptionPayload("sess1", strings.Repeat("世", maxCaptionTextBytes))
		require.NoError(t, err)

		var p captionPayload
		require.NoError(t, json.Unmarshal(data, &p))
		require.True(t, utf8.ValidString(p.Text))
		require.NotContains(t, p.Text, string(utf8.RuneError))
		require.Equal(t, maxCaptionTextBytes/3, utf8.RuneCountInString(p.Text))
		require.LessOrEqual(t, len(p.Text), maxCaptionTextBytes)
	})
}

func TestSendCaption(t *testing.T) {
	t.Run("publishes the payload", func(t *testing.T) {
		var tr Transcriber
		var got [][]byte
		tr.setDataPublisher(func(payload []byte) error {
			got = append(got, payload)
			return nil
		})

		tr.sendCaption("sess1", "hello")

		require.Len(t, got, 1)
		require.JSONEq(t, `{"session_id":"sess1","text":"hello"}`, string(got[0]))
	})

	t.Run("no publisher drops the caption", func(t *testing.T) {
		var tr Transcriber
		logs := captureLogs(t)
		require.NotPanics(t, func() { tr.sendCaption("sess1", "hello") })
		require.NotContains(t, logs.String(), "level=ERROR")
	})

	t.Run("publisher error is logged and does not panic", func(t *testing.T) {
		var tr Transcriber
		calls := 0
		tr.setDataPublisher(func([]byte) error {
			calls++
			return errors.New("boom")
		})
		logs := captureLogs(t)

		require.NotPanics(t, func() { tr.sendCaption("sess1", "hello") })

		require.Equal(t, 1, calls)
		require.Contains(t, logs.String(), "level=ERROR")
		require.Contains(t, logs.String(), "failed to publish caption")
		require.Contains(t, logs.String(), "boom")
	})
}

// captureLogs redirects the default slog logger to a buffer for the duration of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
