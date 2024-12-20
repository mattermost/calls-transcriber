package call

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/stretchr/testify/require"
)

func TestSanitizeFilename(t *testing.T) {
	tcs := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "empty string",
		},
		{
			name:     "spaces",
			input:    "some file name with spaces.mp4",
			expected: "some_file_name_with_spaces.mp4",
		},
		{
			name:     "special chars",
			input:    "somefile*with??special/\\chars.mp4",
			expected: "somefile_with__special__chars.mp4",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, sanitizeFilename(tc.input))
		})
	}
}

func TestPublishTranscriptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	middlewares := []middleware{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, mw := range middlewares {
			if mw(w, r) {
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := config.CallTranscriberConfig{
		SiteURL:         ts.URL,
		CallID:          "8w8jorhr7j83uqr6y1st894hqe",
		PostID:          "udzdsg7dwidbzcidx5khrf8nee",
		TranscriptionID: "67t5u6cmtfbb7jug739d43xa9e",
		AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
		NumThreads:      1,
		ModelSize:       config.ModelSizeTiny,
	}
	cfg.SetDefaults()
	tr, err := NewTranscriber(cfg, GetDataDir(""))
	require.NoError(t, err)
	require.NotNil(t, tr)

	t.Run("", func(t *testing.T) {
		err := tr.publishTranscription(transcribe.Transcription{})
		require.EqualError(t, err, "failed to get filename for call: failed to get filename: AppErrorFromJSON: model.utils.decode_json.app_error, body: 404 page not found\n, json: cannot unmarshal number into Go value of type model.AppError")
	})

	t.Run("missing file", func(t *testing.T) {
		middlewares = []middleware{
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/filename" && r.Method == http.MethodGet {
					w.WriteHeader(200)
					fmt.Fprintln(w, `{"filename": "Call_Test"}`)
					return true
				}

				return false
			},
		}

		err := tr.publishTranscription(transcribe.Transcription{})
		require.EqualError(t, err, fmt.Sprintf("failed to open output file: open %s: no such file or directory", filepath.Join(tr.dataPath, "Call_Test.vtt")))
	})

	vttFile, err := os.CreateTemp("", "Call_Test.vtt")
	require.NoError(t, err)
	defer os.Remove(vttFile.Name())

	_, err = vttFile.Write([]byte(`
WEBVTT

00:00:04.675 --> 00:00:11.395
<v Claudio Costa>(Claudio Costa) All right, we should be recording. Welcome everyone, developers meeting for December 13th.
`))
	require.NoError(t, err)

	txtFile, err := os.CreateTemp("", "Call_Test.txt")
	require.NoError(t, err)
	defer os.Remove(txtFile.Name())

	_, err = vttFile.Write([]byte(`
00:00:05 -> 00:00:21
Claudio Costa
All right, we should be recording. Welcome everyone, developers meeting for December 13th.
`))
	require.NoError(t, err)

	dataDir := os.Getenv("DATA_DIR")
	os.Setenv("DATA_DIR", filepath.Dir(vttFile.Name()))
	defer os.Setenv("DATA_DIR", dataDir)

	maxAPIRetryAttempts = 2

	t.Run("upload session creation failure", func(t *testing.T) {
		middlewares = []middleware{
			middlewares[0],
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads" && r.Method == http.MethodPost {
					w.WriteHeader(400)
					fmt.Fprintln(w, `{"message": "upload session error"}`)
					return true
				}

				return false
			},
		}

		err := tr.publishTranscription(transcribe.Transcription{})
		require.EqualError(t, err, "maximum attempts reached : upload session error")
	})

	t.Run("upload failure", func(t *testing.T) {
		middlewares = []middleware{
			middlewares[0],
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads" && r.Method == http.MethodPost {
					var us model.UploadSession

					err := json.NewDecoder(r.Body).Decode(&us)
					require.NoError(t, err)

					us.Id = "jpanyqdipffrpmxxst3kzdjaah"

					w.WriteHeader(200)
					err = json.NewEncoder(w).Encode(&us)
					require.NoError(t, err)

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads/jpanyqdipffrpmxxst3kzdjaah" && r.Method == http.MethodPost {
					w.WriteHeader(400)
					fmt.Fprintln(w, `{"message": "upload error"}`)
					return true
				}

				return false
			},
		}

		err := tr.publishTranscription(transcribe.Transcription{})
		require.EqualError(t, err, "maximum attempts reached : upload error")
	})

	t.Run("success after failure", func(t *testing.T) {
		var failures int
		middlewares = []middleware{
			middlewares[0],
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads" && r.Method == http.MethodPost {
					var us model.UploadSession

					err := json.NewDecoder(r.Body).Decode(&us)
					require.NoError(t, err)

					us.Id = "jpanyqdipffrpmxxst3kzdjaah"

					w.WriteHeader(200)
					err = json.NewEncoder(w).Encode(&us)
					require.NoError(t, err)

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads/jpanyqdipffrpmxxst3kzdjaah" && r.Method == http.MethodPost {
					if failures > 0 {
						var fi model.FileInfo
						w.WriteHeader(200)
						err = json.NewEncoder(w).Encode(&fi)
						require.NoError(t, err)
					} else {
						w.WriteHeader(400)
						fmt.Fprintln(w, `{"message": "upload error"}`)
						failures++
					}

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/transcriptions" && r.Method == http.MethodPost {
					w.WriteHeader(200)
					return true
				}

				return false
			},
		}

		err := tr.publishTranscription(transcribe.Transcription{})
		require.NoError(t, err)
	})

	t.Run("success at first attempt", func(t *testing.T) {
		middlewares = []middleware{
			middlewares[0],
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads" && r.Method == http.MethodPost {
					var us model.UploadSession

					err := json.NewDecoder(r.Body).Decode(&us)
					require.NoError(t, err)

					us.Id = "jpanyqdipffrpmxxst3kzdjaah"

					w.WriteHeader(200)
					err = json.NewEncoder(w).Encode(&us)
					require.NoError(t, err)

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads/jpanyqdipffrpmxxst3kzdjaah" && r.Method == http.MethodPost {
					var fi model.FileInfo
					w.WriteHeader(200)
					err = json.NewEncoder(w).Encode(&fi)
					require.NoError(t, err)

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/transcriptions" && r.Method == http.MethodPost {
					w.WriteHeader(200)
					return true
				}

				return false
			},
		}

		err := tr.publishTranscription(transcribe.Transcription{})
		require.NoError(t, err)
	})

	t.Run("should re-attempt in case of failure to get filename", func(t *testing.T) {
		var failures int
		middlewares = []middleware{
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/filename" && r.Method == http.MethodGet {
					if failures == 0 {
						w.WriteHeader(400)
						failures++
					} else {
						w.WriteHeader(200)
						fmt.Fprintln(w, `{"filename": "Call_Test"}`)
					}

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads" && r.Method == http.MethodPost {
					var us model.UploadSession

					err := json.NewDecoder(r.Body).Decode(&us)
					require.NoError(t, err)

					us.Id = "jpanyqdipffrpmxxst3kzdjaah"

					w.WriteHeader(200)
					err = json.NewEncoder(w).Encode(&us)
					require.NoError(t, err)

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/uploads/jpanyqdipffrpmxxst3kzdjaah" && r.Method == http.MethodPost {
					var fi model.FileInfo
					w.WriteHeader(200)
					err = json.NewEncoder(w).Encode(&fi)
					require.NoError(t, err)

					return true
				}

				return false
			},
			func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/plugins/com.mattermost.calls/bot/calls/8w8jorhr7j83uqr6y1st894hqe/transcriptions" && r.Method == http.MethodPost {
					w.WriteHeader(200)
					return true
				}

				return false
			},
		}

		err := tr.publishTranscription(transcribe.Transcription{})
		require.NoError(t, err)
	})
}
