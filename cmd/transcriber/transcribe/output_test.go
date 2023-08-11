package transcribe

import (
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
