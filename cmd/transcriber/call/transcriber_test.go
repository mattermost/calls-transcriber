package call

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/ogg"

	mocks "github.com/mattermost/calls-transcriber/cmd/transcriber/mocks/github.com/mattermost/calls-transcriber/cmd/transcriber/call"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"

	"github.com/stretchr/testify/mock"
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

	dir, err := os.MkdirTemp("", "data")
	if err != nil {
		require.NoError(t, err)
	}
	os.Setenv("DATA_DIR", dir)
	t.Cleanup(func() {
		os.Unsetenv("DATA_DIR")
		os.RemoveAll(dir)
	})

	tr, err := NewTranscriber(cfg, GetDataDir(""))
	require.NoError(t, err)
	require.NotNil(t, tr)

	return tr
}

func TestNewTranscriber(t *testing.T) {
	t.Run("invalid siteURL", func(t *testing.T) {
		cfg := config.CallTranscriberConfig{
			SiteURL:         "invalid-url",
			CallID:          "8w8jorhr7j83uqr6y1st894hqe",
			PostID:          "udzdsg7dwidbzcidx5khrf8nee",
			TranscriptionID: "67t5u6cmtfbb7jug739d43xa9e",
			AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
			NumThreads:      1,
			ModelSize:       config.ModelSizeTiny,
		}
		cfg.SetDefaults()

		tr, err := NewTranscriber(cfg, GetDataDir(""))
		require.EqualError(t, err, "failed to validate URL: SiteURL parsing failed: invalid scheme \"\"")
		require.Nil(t, tr)
	})
	t.Run("empty data path", func(t *testing.T) {
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

		tr, err := NewTranscriber(cfg, "")
		require.EqualError(t, err, "dataPath should not be empty")
		require.Nil(t, tr)
	})
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
		require.Equal(t, "this is a test transcription sample.", strings.TrimSpace(strings.ToLower(trackTr.Segments[0].Text)))
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
		require.Equal(t, "this is a test transcription sample.", strings.TrimSpace(strings.ToLower(trackTr.Segments[0].Text)))
		require.Equal(t, "with a gap in speech of a couple of seconds.", strings.TrimSpace(strings.ToLower(trackTr.Segments[1].Text)))
		require.Equal(t, 4668*time.Millisecond, d)
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

			mockClient := &mocks.MockAPIClient{}
			tr.apiClient = mockClient

			defer mockClient.AssertExpectations(t)

			mockClient.On("DoAPIRequest", mock.Anything, http.MethodGet,
				"http://localhost:8065/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/sessions/sessionID/profile", "", "").
				Return(&http.Response{
					Body: io.NopCloser(strings.NewReader(`{"id": "userID", "username": "testuser"}`)),
				}, nil).Once()

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

			tr.liveTracksWg.Add(1)
			tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
			tr.processLiveTrack(track, sessionID)
			close(tr.trackCtxs)
			require.Len(t, tr.trackCtxs, 1)

			trackFile, err := os.Open(filepath.Join(tr.dataPath, fmt.Sprintf("userID_%s.ogg", track.id)))
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

			mockClient := &mocks.MockAPIClient{}
			tr.apiClient = mockClient

			defer mockClient.AssertExpectations(t)

			mockClient.On("DoAPIRequest", mock.Anything, http.MethodGet,
				"http://localhost:8065/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/sessions/sessionID/profile", "", "").
				Return(&http.Response{
					Body: io.NopCloser(strings.NewReader(`{"id": "userID", "username": "testuser"}`)),
				}, nil).Once()

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

			tr.liveTracksWg.Add(1)
			tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
			tr.processLiveTrack(track, sessionID)
			close(tr.trackCtxs)
			require.Len(t, tr.trackCtxs, 1)

			trackFile, err := os.Open(filepath.Join(tr.dataPath, fmt.Sprintf("userID_%s.ogg", track.id)))
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

			mockClient := &mocks.MockAPIClient{}
			tr.apiClient = mockClient

			defer mockClient.AssertExpectations(t)

			mockClient.On("DoAPIRequest", mock.Anything, http.MethodGet,
				"http://localhost:8065/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/sessions/sessionID/profile", "", "").
				Return(&http.Response{
					Body: io.NopCloser(strings.NewReader(`{"id": "userID", "username": "testuser"}`)),
				}, nil).Once()

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

			tr.liveTracksWg.Add(1)
			tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
			tr.processLiveTrack(track, sessionID)
			close(tr.trackCtxs)
			require.Len(t, tr.trackCtxs, 1)

			trackFile, err := os.Open(filepath.Join(tr.dataPath, fmt.Sprintf("userID_%s.ogg", track.id)))
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

	t.Run("should reattempt getUserForSession on failure", func(t *testing.T) {
		tr := setupTranscriberForTest(t)

		mockClient := &mocks.MockAPIClient{}
		tr.apiClient = mockClient

		defer mockClient.AssertExpectations(t)

		mockClient.On("DoAPIRequest", mock.Anything, http.MethodGet,
			"http://localhost:8065/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/sessions/sessionID/profile", "", "").
			Return(nil, fmt.Errorf("failed")).Once()

		mockClient.On("DoAPIRequest", mock.Anything, http.MethodGet,
			"http://localhost:8065/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/sessions/sessionID/profile", "", "").
			Return(&http.Response{
				Body: io.NopCloser(strings.NewReader(`{"id": "userID", "username": "testuser"}`)),
			}, nil).Once()

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
			{
				Header: rtp.Header{
					Timestamp: 4000,
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
			return pkts[i], nil, nil
		}

		tr.liveTracksWg.Add(1)
		tr.startTime.Store(newTimeP(time.Now().Add(-time.Second)))
		tr.processLiveTrack(track, "sessionID")

		close(tr.trackCtxs)
		require.Len(t, tr.trackCtxs, 1)
	})

	t.Run("should not queue contexes with no samples", func(t *testing.T) {
		tr := setupTranscriberForTest(t)

		mockClient := &mocks.MockAPIClient{}
		tr.apiClient = mockClient

		defer mockClient.AssertExpectations(t)

		mockClient.On("DoAPIRequest", mock.Anything, http.MethodGet,
			"http://localhost:8065/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/sessions/sessionID/profile", "", "").
			Return(&http.Response{
				Body: io.NopCloser(strings.NewReader(`{"id": "userID", "username": "testuser"}`)),
			}, nil).Once()

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
			{
				Header: rtp.Header{
					Timestamp: 4000,
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

		tr.liveTracksWg.Add(1)
		tr.processLiveTrack(track, "sessionID")
		close(tr.trackCtxs)
		require.Empty(t, tr.trackCtxs)
	})
}
