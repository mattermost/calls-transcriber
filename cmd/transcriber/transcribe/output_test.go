package transcribe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVTTTS(t *testing.T) {
	require.Equal(t, "00:00:00.000", vttTS(0, true))
	require.Equal(t, "00:00:00", vttTS(0, false))

	require.Equal(t, "00:01:10.000", vttTS(70000, true))
	require.Equal(t, "00:01:10", vttTS(70000, false))

	require.Equal(t, "00:00:00.999", vttTS(999, true))
	require.Equal(t, "00:00:01", vttTS(999, false))

	require.Equal(t, "00:00:01.000", vttTS(1000, true))
	require.Equal(t, "00:00:01", vttTS(1000, false))

	require.Equal(t, "00:00:01.100", vttTS(1100, true))
	require.Equal(t, "00:00:01", vttTS(1100, false))

	require.Equal(t, "00:01:02.200", vttTS(62200, true))
	require.Equal(t, "00:01:02", vttTS(62200, false))

	require.Equal(t, "01:00:00.000", vttTS(3600000, true))
	require.Equal(t, "01:00:00", vttTS(3600000, false))

	require.Equal(t, "01:45:45.045", vttTS(6345045, true))
	require.Equal(t, "01:45:45", vttTS(6345045, false))
}

func TestInterleave(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var tr Transcription
		var ns []namedSegment
		require.Equal(t, ns, tr.interleave())
	})

	t.Run("ordered", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1,
						Text:    "A1",
					},
					{
						StartTS: 2,
						EndTS:   3,
						Text:    "A2",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerB",
				Segments: []Segment{
					{
						StartTS: 4,
						EndTS:   5,
						Text:    "B1",
					},
					{
						StartTS: 5,
						EndTS:   6,
						Text:    "B2",
					},
				},
			},
		}
		ns := []namedSegment{
			{
				Speaker: "SpeakerA",
				Segment: Segment{
					StartTS: 0,
					EndTS:   1,
					Text:    "A1",
				},
			},
			{
				Speaker: "SpeakerA",
				Segment: Segment{
					StartTS: 2,
					EndTS:   3,
					Text:    "A2",
				},
			},
			{
				Speaker: "SpeakerB",
				Segment: Segment{
					StartTS: 4,
					EndTS:   5,
					Text:    "B1",
				},
			},
			{
				Speaker: "SpeakerB",
				Segment: Segment{
					StartTS: 5,
					EndTS:   6,
					Text:    "B2",
				},
			},
		}
		require.Equal(t, ns, tr.interleave())
	})

	t.Run("unordered", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1,
						Text:    "A1",
					},
					{
						StartTS: 2,
						EndTS:   3,
						Text:    "A2",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 4,
						EndTS:   5,
						Text:    "A3",
					},
					{
						StartTS: 5,
						EndTS:   6,
						Text:    "A4",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerB",
				Segments: []Segment{
					{
						StartTS: 3,
						EndTS:   4,
						Text:    "B1",
					},
					{
						StartTS: 6,
						EndTS:   7,
						Text:    "B2",
					},
				},
			},
		}
		ns := []namedSegment{
			{
				Speaker: "SpeakerA",
				Segment: Segment{
					StartTS: 0,
					EndTS:   1,
					Text:    "A1",
				},
			},
			{
				Speaker: "SpeakerA",
				Segment: Segment{
					StartTS: 2,
					EndTS:   3,
					Text:    "A2",
				},
			},
			{
				Speaker: "SpeakerB",
				Segment: Segment{
					StartTS: 3,
					EndTS:   4,
					Text:    "B1",
				},
			},
			{
				Speaker: "SpeakerA",
				Segment: Segment{
					StartTS: 4,
					EndTS:   5,
					Text:    "A3",
				},
			},
			{
				Speaker: "SpeakerA",
				Segment: Segment{
					StartTS: 5,
					EndTS:   6,
					Text:    "A4",
				},
			},
			{
				Speaker: "SpeakerB",
				Segment: Segment{
					StartTS: 6,
					EndTS:   7,
					Text:    "B2",
				},
			},
		}
		require.Equal(t, ns, tr.interleave())
	})
}

func TestWebVTT(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var tr Transcription
		var b strings.Builder
		err := tr.WebVTT(&b, WebVTTOptions{})
		require.NoError(t, err)
		require.Equal(t, "WEBVTT\n", b.String())
	})

	t.Run("full", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1000,
						Text:    "A1",
					},
					{
						StartTS: 2000,
						EndTS:   3000,
						Text:    "A2",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 4000,
						EndTS:   5000,
						Text:    "A3",
					},
					{
						StartTS: 5000,
						EndTS:   6000,
						Text:    "A4",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerB",
				Segments: []Segment{
					{
						StartTS: 3000,
						EndTS:   4000,
						Text:    "B1",
					},
					{
						StartTS: 6000,
						EndTS:   7000,
						Text:    "B2",
					},
				},
			},
		}

		var b strings.Builder
		expected := `WEBVTT

00:00:00.000 --> 00:00:01.000
<v SpeakerA>(SpeakerA) A1

00:00:02.000 --> 00:00:03.000
<v SpeakerA>(SpeakerA) A2

00:00:03.000 --> 00:00:04.000
<v SpeakerB>(SpeakerB) B1

00:00:04.000 --> 00:00:05.000
<v SpeakerA>(SpeakerA) A3

00:00:05.000 --> 00:00:06.000
<v SpeakerA>(SpeakerA) A4

00:00:06.000 --> 00:00:07.000
<v SpeakerB>(SpeakerB) B2
`
		err := tr.WebVTT(&b, WebVTTOptions{
			OmitSpeaker: false,
		})
		require.NoError(t, err)
		require.Equal(t, expected, b.String())
	})

	t.Run("omit speaker", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1000,
						Text:    "A1",
					},
					{
						StartTS: 2000,
						EndTS:   3000,
						Text:    "A2",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 4000,
						EndTS:   5000,
						Text:    "A3",
					},
					{
						StartTS: 5000,
						EndTS:   6000,
						Text:    "A4",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerB",
				Segments: []Segment{
					{
						StartTS: 3000,
						EndTS:   4000,
						Text:    "B1",
					},
					{
						StartTS: 6000,
						EndTS:   7000,
						Text:    "B2",
					},
				},
			},
		}

		var b strings.Builder
		expected := `WEBVTT

00:00:00.000 --> 00:00:01.000
A1

00:00:02.000 --> 00:00:03.000
A2

00:00:03.000 --> 00:00:04.000
B1

00:00:04.000 --> 00:00:05.000
A3

00:00:05.000 --> 00:00:06.000
A4

00:00:06.000 --> 00:00:07.000
B2
`
		err := tr.WebVTT(&b, WebVTTOptions{
			OmitSpeaker: true,
		})
		require.NoError(t, err)
		require.Equal(t, expected, b.String())
	})

	t.Run("html escaping", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "<SpeakerA>",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1000,
						Text:    "Some \"text\" to 'escape'",
					},
				},
			},
		}

		var b strings.Builder
		expected := `WEBVTT

00:00:00.000 --> 00:00:01.000
<v SpeakerA>(SpeakerA) Some &#34;text&#34; to &#39;escape&#39;
`
		err := tr.WebVTT(&b, WebVTTOptions{
			OmitSpeaker: false,
		})
		require.NoError(t, err)
		require.Equal(t, expected, b.String())
	})
}

func TestText(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var tr Transcription
		var b strings.Builder
		err := tr.Text(&b, TextOptions{})
		require.NoError(t, err)
		require.Empty(t, b.String())
	})

	t.Run("full", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1000,
						Text:    "A1",
					},
					{
						StartTS: 2000,
						EndTS:   3000,
						Text:    "A2",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 4000,
						EndTS:   5000,
						Text:    "A3",
					},
					{
						StartTS: 5000,
						EndTS:   6000,
						Text:    "A4",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerB",
				Segments: []Segment{
					{
						StartTS: 3000,
						EndTS:   4000,
						Text:    "B1",
					},
					{
						StartTS: 6000,
						EndTS:   7000,
						Text:    " B2 ",
					},
				},
			},
		}

		var b strings.Builder
		expected := `00:00:00 -> 00:00:01
SpeakerA
A1

00:00:02 -> 00:00:03
SpeakerA
A2

00:00:03 -> 00:00:04
SpeakerB
B1

00:00:04 -> 00:00:05
SpeakerA
A3

00:00:05 -> 00:00:06
SpeakerA
A4

00:00:06 -> 00:00:07
SpeakerB
B2
`
		err := tr.Text(&b, TextOptions{})
		require.NoError(t, err)
		require.Equal(t, expected, b.String())
	})

	t.Run("compact", func(t *testing.T) {
		tr := Transcription{
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 0,
						EndTS:   1000,
						Text:    "A1",
					},
					{
						StartTS: 2000,
						EndTS:   3000,
						Text:    "A2",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerA",
				Segments: []Segment{
					{
						StartTS: 4000,
						EndTS:   5000,
						Text:    "A3",
					},
					{
						StartTS: 5000,
						EndTS:   6000,
						Text:    "A4",
					},
				},
			},
			TrackTranscription{
				Speaker: "SpeakerB",
				Segments: []Segment{
					{
						StartTS: 3000,
						EndTS:   4000,
						Text:    "B1",
					},
					{
						StartTS: 6000,
						EndTS:   7000,
						Text:    " B2 ",
					},
				},
			},
		}

		var b strings.Builder
		expected := `00:00:00 -> 00:00:03
SpeakerA
A1 A2

00:00:03 -> 00:00:04
SpeakerB
B1

00:00:04 -> 00:00:06
SpeakerA
A3 A4

00:00:06 -> 00:00:07
SpeakerB
B2
`
		err := tr.Text(&b, TextOptions{
			CompactOptions: TextCompactOptions{
				SilenceThresholdMs:   2000,
				MaxSegmentDurationMs: 10000,
			},
		})
		require.NoError(t, err)
		require.Equal(t, expected, b.String())
	})
}

func TestSanitizeSegment(t *testing.T) {
	tcs := []struct {
		name     string
		input    namedSegment
		expected namedSegment
	}{
		{
			name: "empty",
		},
		{
			name: "plaintext",
			input: namedSegment{
				Segment: Segment{
					Text: "some sentence.",
				},
				Speaker: "Firstname Lastname",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "some sentence.",
				},
				Speaker: "Firstname Lastname",
			},
		},
		{
			name: "multiple spaces",
			input: namedSegment{
				Segment: Segment{
					Text: "   some   sentence with  multiple spaces.  ",
				},
				Speaker: "Firstname   Lastname",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "some sentence with multiple spaces.",
				},
				Speaker: "Firstname Lastname",
			},
		},
		{
			name: "new lines",
			input: namedSegment{
				Segment: Segment{
					Text: "sentence with new\r \n\r\n lines\n\n.\n\n\n",
				},
				Speaker: "Firstname\n\nLastname",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "sentence with new lines .",
				},
				Speaker: "Firstname Lastname",
			},
		},
		{
			name: "dots",
			input: namedSegment{
				Segment: Segment{
					Text: "test sentence.",
				},
				Speaker: "Firstname Lastname Jr.",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "test sentence.",
				},
				Speaker: "Firstname Lastname Jr.",
			},
		},
		{
			name: "dashes and underscores",
			input: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "Firstname_Last-name",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "Firstname_Last-name",
			},
		},
		{
			name: "parentheses",
			input: namedSegment{
				Segment: Segment{
					Text: "(test sentence)",
				},
				Speaker: "(Firstname Lastname)",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "(test sentence)",
				},
				Speaker: "Firstname Lastname",
			},
		},
		{
			name: "digits",
			input: namedSegment{
				Segment: Segment{
					Text: "test 45",
				},
				Speaker: "Firstname45 Lastname",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "test 45",
				},
				Speaker: "Firstname45 Lastname",
			},
		},
		{
			name: "unicode",
			input: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "Firstname 🦄 Lastname",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "Firstname Lastname",
			},
		},
		{
			name: "foreign alphabet characters",
			input: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "うずまき ナルト",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "うずまき ナルト",
			},
		},
		{
			name: "foreign alphabet digits",
			input: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "٣ ٢",
			},
			expected: namedSegment{
				Segment: Segment{
					Text: "test sentence",
				},
				Speaker: "٣ ٢",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			tc.input.sanitize()
			require.Equal(t, tc.expected, tc.input)
		})
	}
}

func TestCompact(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		require.Empty(t, compactSegments(nil, TextCompactOptions{
			SilenceThresholdMs:   1000,
			MaxSegmentDurationMs: 1000,
		}))
		require.Empty(t, compactSegments([]namedSegment{}, TextCompactOptions{
			SilenceThresholdMs:   1000,
			MaxSegmentDurationMs: 1000,
		}))
	})

	t.Run("single segment", func(t *testing.T) {
		segments := []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   100,
					Text:    "test",
				},
			},
		}
		require.Equal(t, segments, compactSegments(segments, TextCompactOptions{
			SilenceThresholdMs:   1000,
			MaxSegmentDurationMs: 1000,
		}))
	})

	t.Run("single speaker", func(t *testing.T) {
		segments := []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   100,
					Text:    "test1",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 100,
					EndTS:   200,
					Text:    "test2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 200,
					EndTS:   300,
					Text:    "test3",
				},
			},
		}
		require.Equal(t, []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   300,
					Text:    "test1 test2 test3",
				},
			},
		}, compactSegments(segments, TextCompactOptions{
			SilenceThresholdMs:   1000,
			MaxSegmentDurationMs: 1000,
		}))
	})

	t.Run("silence threshold", func(t *testing.T) {
		segments := []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   100,
					Text:    "test1",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 100,
					EndTS:   200,
					Text:    "test2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 500,
					EndTS:   600,
					Text:    "test3",
				},
			},
		}
		require.Equal(t, []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   200,
					Text:    "test1 test2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 500,
					EndTS:   600,
					Text:    "test3",
				},
			},
		}, compactSegments(segments, TextCompactOptions{
			SilenceThresholdMs:   100,
			MaxSegmentDurationMs: 200,
		}))
	})

	t.Run("max duration", func(t *testing.T) {
		segments := []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   100,
					Text:    "test1",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 100,
					EndTS:   200,
					Text:    "test2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 200,
					EndTS:   300,
					Text:    "test3",
				},
			},
		}
		require.Equal(t, []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   200,
					Text:    "test1 test2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 200,
					EndTS:   300,
					Text:    "test3",
				},
			},
		}, compactSegments(segments, TextCompactOptions{
			SilenceThresholdMs:   100,
			MaxSegmentDurationMs: 200,
		}))
	})

	t.Run("speaker change", func(t *testing.T) {
		segments := []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   100,
					Text:    "testA1",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 100,
					EndTS:   200,
					Text:    "testA2",
				},
			},
			{
				Speaker: "B",
				Segment: Segment{
					StartTS: 100,
					EndTS:   200,
					Text:    "testB1",
				},
			},
			{
				Speaker: "B",
				Segment: Segment{
					StartTS: 200,
					EndTS:   300,
					Text:    "testB2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 200,
					EndTS:   300,
					Text:    "testA3",
				},
			},
		}
		require.Equal(t, []namedSegment{
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 0,
					EndTS:   200,
					Text:    "testA1 testA2",
				},
			},
			{
				Speaker: "B",
				Segment: Segment{
					StartTS: 100,
					EndTS:   300,
					Text:    "testB1 testB2",
				},
			},
			{
				Speaker: "A",
				Segment: Segment{
					StartTS: 200,
					EndTS:   300,
					Text:    "testA3",
				},
			},
		}, compactSegments(segments, TextCompactOptions{
			SilenceThresholdMs:   1000,
			MaxSegmentDurationMs: 1000,
		}))
	})
}
