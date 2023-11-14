package config

import (
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigIsValid(t *testing.T) {
	tcs := []struct {
		name          string
		cfg           CallTranscriberConfig
		expectedError string
	}{
		{
			name:          "empty config",
			cfg:           CallTranscriberConfig{},
			expectedError: "config cannot be empty",
		},
		{
			name: "invalid SiteURL schema",
			cfg: CallTranscriberConfig{
				SiteURL: "invalid://localhost",
			},
			expectedError: "SiteURL parsing failed: invalid scheme \"invalid\"",
		},
		{
			name: "missing CallID",
			cfg: CallTranscriberConfig{
				SiteURL: "http://localhost:8065",
			},
			expectedError: "CallID cannot be empty",
		},
		{
			name: "missing PostID",
			cfg: CallTranscriberConfig{
				SiteURL:   "http://localhost:8065",
				CallID:    "8w8jorhr7j83uqr6y1st894hqe",
				AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			},
			expectedError: "PostID cannot be empty",
		},
		{
			name: "missing AuthToken",
			cfg: CallTranscriberConfig{
				SiteURL: "http://localhost:8065",
				CallID:  "8w8jorhr7j83uqr6y1st894hqe",
				PostID:  "udzdsg7dwidbzcidx5khrf8nee",
			},
			expectedError: "AuthToken cannot be empty",
		},
		{
			name: "missing TranscriptionID",
			cfg: CallTranscriberConfig{
				SiteURL:   "http://localhost:8065",
				CallID:    "8w8jorhr7j83uqr6y1st894hqe",
				PostID:    "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			},
			expectedError: "TranscriptionID cannot be empty",
		},
		{
			name: "invalid TranscribeAPI",
			cfg: CallTranscriberConfig{
				SiteURL:         "http://localhost:8065",
				CallID:          "8w8jorhr7j83uqr6y1st894hqe",
				PostID:          "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
				TranscriptionID: "on5yfih5etn5m8rfdidamc1oxa",
			},
			expectedError: "TranscribeAPI value is not valid",
		},
		{
			name: "invalid ModelSize",
			cfg: CallTranscriberConfig{
				SiteURL:         "http://localhost:8065",
				CallID:          "8w8jorhr7j83uqr6y1st894hqe",
				PostID:          "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
				TranscriptionID: "on5yfih5etn5m8rfdidamc1oxa",
				TranscribeAPI:   TranscribeAPIDefault,
				OutputFormat:    OutputFormatVTT,
			},
			expectedError: "ModelSize value is not valid",
		},
		{
			name: "invalid OutputFormat",
			cfg: CallTranscriberConfig{
				SiteURL:         "http://localhost:8065",
				CallID:          "8w8jorhr7j83uqr6y1st894hqe",
				PostID:          "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
				TranscriptionID: "on5yfih5etn5m8rfdidamc1oxa",
				TranscribeAPI:   TranscribeAPIDefault,
				ModelSize:       ModelSizeMedium,
			},
			expectedError: "OutputFormat value is not valid",
		},
		{
			name: "invalid NumThreads",
			cfg: CallTranscriberConfig{
				SiteURL:         "http://localhost:8065",
				CallID:          "8w8jorhr7j83uqr6y1st894hqe",
				PostID:          "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
				TranscriptionID: "on5yfih5etn5m8rfdidamc1oxa",
				TranscribeAPI:   TranscribeAPIDefault,
				ModelSize:       ModelSizeMedium,
				OutputFormat:    OutputFormatVTT,
			},
			expectedError: fmt.Sprintf("NumThreads should be in the range [1, %d]", runtime.NumCPU()),
		},
		{
			name: "valid config",
			cfg: CallTranscriberConfig{
				SiteURL:         "http://localhost:8065",
				CallID:          "8w8jorhr7j83uqr6y1st894hqe",
				PostID:          "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
				TranscriptionID: "on5yfih5etn5m8rfdidamc1oxa",
				TranscribeAPI:   TranscribeAPIDefault,
				ModelSize:       ModelSizeMedium,
				OutputFormat:    OutputFormatVTT,
				NumThreads:      1,
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.IsValid()
			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestConfigSetDefaults(t *testing.T) {
	t.Run("empty input config", func(t *testing.T) {
		var cfg CallTranscriberConfig
		cfg.SetDefaults()
		require.Equal(t, CallTranscriberConfig{
			TranscribeAPI: TranscribeAPIDefault,
			ModelSize:     ModelSizeDefault,
			OutputFormat:  OutputFormatDefault,
			NumThreads:    max(1, runtime.NumCPU()/2),
		}, cfg)
	})

	t.Run("no overrides", func(t *testing.T) {
		cfg := CallTranscriberConfig{
			ModelSize: ModelSizeMedium,
		}
		cfg.SetDefaults()
		require.Equal(t, CallTranscriberConfig{
			TranscribeAPI: TranscribeAPIDefault,
			ModelSize:     ModelSizeMedium,
			OutputFormat:  OutputFormatDefault,
			NumThreads:    max(1, runtime.NumCPU()/2),
		}, cfg)
	})
}

func TestLoadFromEnv(t *testing.T) {
	t.Run("no env set", func(t *testing.T) {
		cfg, err := LoadFromEnv()
		require.NoError(t, err)
		require.Empty(t, cfg)
	})

	t.Run("valid config", func(t *testing.T) {
		os.Setenv("SITE_URL", "http://localhost:8065/")
		defer os.Unsetenv("SITE_URL")
		os.Setenv("CALL_ID", "8w8jorhr7j83uqr6y1st894hqe")
		defer os.Unsetenv("CALL_ID")
		os.Setenv("POST_ID", "udzdsg7dwidbzcidx5khrf8nee")
		defer os.Unsetenv("POST_ID")
		os.Setenv("AUTH_TOKEN", "qj75unbsef83ik9p7ueypb6iyw")
		defer os.Unsetenv("AUTH_TOKEN")
		os.Setenv("TRANSCRIPTION_ID", "on5yfih5etn5m8rfdidamc1oxa")
		defer os.Unsetenv("TRANSCRIPTION_ID")
		os.Setenv("TRANSCRIBE_API", "whisper.cpp")
		defer os.Unsetenv("TRANSCRIBE_API")
		os.Setenv("MODEL_SIZE", "medium")
		defer os.Unsetenv("MODEL_SIZE")
		os.Setenv("NUM_THREADS", "1")
		defer os.Unsetenv("NUM_THREADS")
		cfg, err := LoadFromEnv()
		require.NoError(t, err)
		require.NotEmpty(t, cfg)
		require.Equal(t, CallTranscriberConfig{
			SiteURL:         "http://localhost:8065",
			CallID:          "8w8jorhr7j83uqr6y1st894hqe",
			PostID:          "udzdsg7dwidbzcidx5khrf8nee",
			AuthToken:       "qj75unbsef83ik9p7ueypb6iyw",
			TranscriptionID: "on5yfih5etn5m8rfdidamc1oxa",
			TranscribeAPI:   TranscribeAPIWhisperCPP,
			ModelSize:       ModelSizeMedium,
			NumThreads:      1,
		}, cfg)
	})
}

func TestCallTranscriberConfigToEnv(t *testing.T) {
	var cfg CallTranscriberConfig
	cfg.SiteURL = "http://localhost:8065"
	cfg.CallID = "8w8jorhr7j83uqr6y1st894hqe"
	cfg.PostID = "udzdsg7dwidbzcidx5khrf8nee"
	cfg.AuthToken = "qj75unbsef83ik9p7ueypb6iyw"
	cfg.TranscriptionID = "on5yfih5etn5m8rfdidamc1oxa"
	cfg.NumThreads = 1
	cfg.SetDefaults()
	require.Equal(t, []string{
		"SITE_URL=http://localhost:8065",
		"CALL_ID=8w8jorhr7j83uqr6y1st894hqe",
		"POST_ID=udzdsg7dwidbzcidx5khrf8nee",
		"AUTH_TOKEN=qj75unbsef83ik9p7ueypb6iyw",
		"TRANSCRIPTION_ID=on5yfih5etn5m8rfdidamc1oxa",
		"TRANSCRIBE_API=whisper.cpp",
		"MODEL_SIZE=base",
		"OUTPUT_FORMAT=vtt",
		"NUM_THREADS=1",
	}, cfg.ToEnv())
}

func TestCallTranscriberConfigMap(t *testing.T) {
	var cfg CallTranscriberConfig
	cfg.SiteURL = "http://localhost:8065"
	cfg.CallID = "8w8jorhr7j83uqr6y1st894hqe"
	cfg.PostID = "udzdsg7dwidbzcidx5khrf8nee"
	cfg.AuthToken = "qj75unbsef83ik9p7ueypb6iyw"
	cfg.TranscriptionID = "on5yfih5etn5m8rfdidamc1oxa"
	cfg.NumThreads = 1
	cfg.SetDefaults()

	t.Run("default config", func(t *testing.T) {
		var c CallTranscriberConfig
		err := c.FromMap(cfg.ToMap()).IsValid()
		require.NoError(t, err)
	})
}
