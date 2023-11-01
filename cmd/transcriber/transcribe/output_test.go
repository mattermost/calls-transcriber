package transcribe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVTTTS(t *testing.T) {
	require.Equal(t, "00:00:00.000", vttTS(0))

	require.Equal(t, "00:01:10.000", vttTS(70000))

	require.Equal(t, "00:00:00.999", vttTS(999))

	require.Equal(t, "00:00:01.000", vttTS(1000))

	require.Equal(t, "00:00:01.100", vttTS(1100))

	require.Equal(t, "00:01:02.200", vttTS(62200))

	require.Equal(t, "01:00:00.000", vttTS(3600000))

	require.Equal(t, "01:45:45.045", vttTS(6345045))
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
}
