package call

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/ogg"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

func setupTranscriberForTest(t *testing.T) *Transcriber {
	t.Helper()

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

	return tr
}

func TestTranscribeTrack(t *testing.T) {
	tr := setupTranscriberForTest(t)

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
		require.Equal(t, " With a gap in speech of a couple of seconds.", trackTr.Segments[1].Text)
		require.Equal(t, 4700*time.Millisecond, d)
	})
}

type trackRemoteMock struct {
	id      string
	readRTP func() (*rtp.Packet, interceptor.Attributes, error)
}

func (t *trackRemoteMock) ID() string {
	return t.id
}

func (t *trackRemoteMock) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	return t.readRTP()
}

func TestProcessLiveTrack(t *testing.T) {
	t.Run("synchronization", func(t *testing.T) {
		t.Run("empty payloads", func(t *testing.T) {
			tr := setupTranscriberForTest(t)

			track := &trackRemoteMock{
				id: "trackID",
			}

			pkts := []*rtp.Packet{
				{
					Header: rtp.Header{
						Timestamp: 1000,
					},
					Payload: []byte{0x45, 0x45, 0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 2000,
					},
					Payload: []byte{0x45, 0x45, 0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 3000,
					},
					Payload: []byte{0x45, 0x45, 0x45},
				},
				// Empty packet
				{
					Header: rtp.Header{
						Timestamp: 4000,
					},
					Payload: []byte{},
				},
				{
					Header: rtp.Header{
						Timestamp: 5000,
					},
					Payload: []byte{0x45, 0x45, 0x45},
				},
			}

			var i int
			track.readRTP = func() (*rtp.Packet, interceptor.Attributes, error) {
				if i >= len(pkts) {
					return nil, nil, io.EOF
				}

				defer func() { i++ }()

				if i == 3 {
					time.Sleep(2 * time.Second)
				}

				return pkts[i], nil, nil
			}

			sessionID := "sessionID"
			user := &model.User{Id: "userID", Username: "testuser"}

			dataDir := os.Getenv("DATA_DIR")
			os.Setenv("DATA_DIR", os.TempDir())
			defer os.Setenv("DATA_DIR", dataDir)

			tr.liveTracksWg.Add(1)
			tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
			tr.processLiveTrack(track, sessionID, user)
			close(tr.trackCtxs)
			require.Len(t, tr.trackCtxs, 1)

			trackFile, err := os.Open(filepath.Join(getDataDir(), fmt.Sprintf("%s_%s.ogg", user.Id, track.id)))
			defer trackFile.Close()
			require.NoError(t, err)

			oggReader, _, err := ogg.NewReaderWith(trackFile)
			require.NoError(t, err)

			// Metadata
			_, hdr, err := oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(0), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(1), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(1001), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(2001), hdr.GranulePosition)

			// Check that empty packets don't affect synchronization.
			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.GreaterOrEqual(t, hdr.GranulePosition, uint64(97001))

			_, _, err = oggReader.ParseNextPage()
			require.Equal(t, io.EOF, err)
		})

		t.Run("out of order packets", func(t *testing.T) {
			tr := setupTranscriberForTest(t)

			track := &trackRemoteMock{
				id: "trackID",
			}

			pkts := []*rtp.Packet{
				{
					Header: rtp.Header{
						Timestamp: 1000,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 3000,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 2000,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 4000,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 5000,
					},
					Payload: []byte{0x45},
				},
			}

			var i int
			track.readRTP = func() (*rtp.Packet, interceptor.Attributes, error) {
				if i >= len(pkts) {
					return nil, nil, io.EOF
				}
				defer func() { i++ }()
				return pkts[i], nil, nil
			}

			sessionID := "sessionID"
			user := &model.User{Id: "userID", Username: "testuser"}

			dataDir := os.Getenv("DATA_DIR")
			os.Setenv("DATA_DIR", os.TempDir())
			defer os.Setenv("DATA_DIR", dataDir)

			tr.liveTracksWg.Add(1)
			tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
			tr.processLiveTrack(track, sessionID, user)
			close(tr.trackCtxs)
			require.Len(t, tr.trackCtxs, 1)

			trackFile, err := os.Open(filepath.Join(getDataDir(), fmt.Sprintf("%s_%s.ogg", user.Id, track.id)))
			defer trackFile.Close()
			require.NoError(t, err)

			oggReader, _, err := ogg.NewReaderWith(trackFile)
			require.NoError(t, err)

			// Metadata
			_, hdr, err := oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(0), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(1), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(2001), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(3001), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(4001), hdr.GranulePosition)

			_, _, err = oggReader.ParseNextPage()
			require.Equal(t, io.EOF, err)
		})

		t.Run("timestamp wrap around", func(t *testing.T) {
			tr := setupTranscriberForTest(t)

			track := &trackRemoteMock{
				id: "trackID",
			}

			pkts := []*rtp.Packet{
				{
					Header: rtp.Header{
						Timestamp: 4294966000,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 4294967000,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 704,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 1704,
					},
					Payload: []byte{0x45},
				},
				{
					Header: rtp.Header{
						Timestamp: 2704,
					},
					Payload: []byte{0x45},
				},
			}

			var i int
			track.readRTP = func() (*rtp.Packet, interceptor.Attributes, error) {
				if i >= len(pkts) {
					return nil, nil, io.EOF
				}
				defer func() { i++ }()
				return pkts[i], nil, nil
			}

			sessionID := "sessionID"
			user := &model.User{Id: "userID", Username: "testuser"}

			dataDir := os.Getenv("DATA_DIR")
			os.Setenv("DATA_DIR", os.TempDir())
			defer os.Setenv("DATA_DIR", dataDir)

			tr.liveTracksWg.Add(1)
			tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
			tr.processLiveTrack(track, sessionID, user)
			close(tr.trackCtxs)
			require.Len(t, tr.trackCtxs, 1)

			trackFile, err := os.Open(filepath.Join(getDataDir(), fmt.Sprintf("%s_%s.ogg", user.Id, track.id)))
			defer trackFile.Close()
			require.NoError(t, err)

			oggReader, _, err := ogg.NewReaderWith(trackFile)
			require.NoError(t, err)

			// Metadata
			_, hdr, err := oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(0), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(1), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(1001), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(2001), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(3001), hdr.GranulePosition)

			_, hdr, err = oggReader.ParseNextPage()
			require.NoError(t, err)
			require.Equal(t, uint64(4001), hdr.GranulePosition)

			_, _, err = oggReader.ParseNextPage()
			require.Equal(t, io.EOF, err)
		})
	})
}
