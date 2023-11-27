package call

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/stretchr/testify/require"
)

func TestTranscribeTrack(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	cfg := config.CallTranscriberConfig{
		SiteURL:         "http://localhost:8065",
		CallID:          "8w8jorhr7j83uqr6y1st894hqe",
		PostID:          "udzdsg7dwidbzcidx5khrf8nee",
		TranscriptionID: "67t5u6cmtfbb7jug739d43xa9e",
		AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
		NumThreads:      1,
		ModelSize:       config.ModelSizeTiny,
	}
	cfg.SetDefaults()
	tr, err := NewTranscriber(cfg)
	require.NoError(t, err)
	require.NotNil(t, tr)

	t.Run("contiguous audio", func(t *testing.T) {
		tctx := trackContext{
			trackID:   "trackID",
			sessionID: "sessionID",
			filename:  "../../../testfiles/speech_contiguous.opus",
			startTS:   0,
			user: &model.User{
				Username: "testuser",
			},
		}

		trackTr, d, err := tr.transcribeTrack(tctx)
		require.NoError(t, err)
		require.Len(t, trackTr.Segments, 1)
		require.Equal(t, " This is a test transcription sample.", trackTr.Segments[0].Text)
		require.Equal(t, 2888*time.Millisecond, d)
	})

	t.Run("gaps in audio", func(t *testing.T) {
		tctx := trackContext{
			trackID:   "trackID",
			sessionID: "sessionID",
			filename:  "../../../testfiles/speech_gap.opus",
			startTS:   0,
			user: &model.User{
				Username: "testuser",
			},
		}

		trackTr, d, err := tr.transcribeTrack(tctx)
		require.NoError(t, err)
		require.Len(t, trackTr.Segments, 2)
		require.Equal(t, " This is a test transcription sample.", trackTr.Segments[0].Text)
		require.Equal(t, " with a gap in speech of a couple of seconds.", trackTr.Segments[1].Text)
		require.Equal(t, 4700*time.Millisecond, d)
	})
}
