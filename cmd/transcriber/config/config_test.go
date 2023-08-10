package config

import (
	"os"
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
			name: "missing ThreadID",
			cfg: CallTranscriberConfig{
				SiteURL:   "http://localhost:8065",
				CallID:    "8w8jorhr7j83uqr6y1st894hqe",
				AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			},
			expectedError: "ThreadID cannot be empty",
		},
		{
			name: "missing AuthToken",
			cfg: CallTranscriberConfig{
				SiteURL:  "http://localhost:8065",
				CallID:   "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID: "udzdsg7dwidbzcidx5khrf8nee",
			},
			expectedError: "AuthToken cannot be empty",
		},
		{
			name: "invalid TranscribeAPI",
			cfg: CallTranscriberConfig{
				SiteURL:   "http://localhost:8065",
				CallID:    "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:  "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			},
			expectedError: "TranscribeAPI value is not valid",
		},
		{
			name: "invalid ModelSize",
			cfg: CallTranscriberConfig{
				SiteURL:       "http://localhost:8065",
				CallID:        "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:      "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:     "qj75unbsef83ik9p7ueypb6iyw",
				TranscribeAPI: TranscribeAPIDefault,
				OutputFormat:  OutputFormatVTT,
			},
			expectedError: "ModelSize value is not valid",
		},
		{
			name: "invalid OutputFormat",
			cfg: CallTranscriberConfig{
				SiteURL:       "http://localhost:8065",
				CallID:        "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:      "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:     "qj75unbsef83ik9p7ueypb6iyw",
				TranscribeAPI: TranscribeAPIDefault,
				ModelSize:     ModelSizeMedium,
			},
			expectedError: "OutputFormat value is not valid",
		},
		{
			name: "valid config",
			cfg: CallTranscriberConfig{
				SiteURL:       "http://localhost:8065",
				CallID:        "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:      "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:     "qj75unbsef83ik9p7ueypb6iyw",
				TranscribeAPI: TranscribeAPIDefault,
				ModelSize:     ModelSizeMedium,
				OutputFormat:  OutputFormatVTT,
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
		os.Setenv("THREAD_ID", "udzdsg7dwidbzcidx5khrf8nee")
		defer os.Unsetenv("THREAD_ID")
		os.Setenv("AUTH_TOKEN", "qj75unbsef83ik9p7ueypb6iyw")
		defer os.Unsetenv("AUTH_TOKEN")
		os.Setenv("TRANSCRIBE_API", "whisper.cpp")
		defer os.Unsetenv("TRANSCRIBE_API")
		os.Setenv("MODEL_SIZE", "medium")
		defer os.Unsetenv("MODEL_SIZE")
		cfg, err := LoadFromEnv()
		require.NoError(t, err)
		require.NotEmpty(t, cfg)
		require.Equal(t, CallTranscriberConfig{
			SiteURL:       "http://localhost:8065",
			CallID:        "8w8jorhr7j83uqr6y1st894hqe",
			ThreadID:      "udzdsg7dwidbzcidx5khrf8nee",
			AuthToken:     "qj75unbsef83ik9p7ueypb6iyw",
			TranscribeAPI: TranscribeAPIWhisperCPP,
			ModelSize:     ModelSizeMedium,
		}, cfg)
	})
}

func TestCallTranscriberConfigToEnv(t *testing.T) {
	var cfg CallTranscriberConfig
	cfg.SiteURL = "http://localhost:8065"
	cfg.CallID = "8w8jorhr7j83uqr6y1st894hqe"
	cfg.AuthToken = "qj75unbsef83ik9p7ueypb6iyw"
	cfg.ThreadID = "udzdsg7dwidbzcidx5khrf8nee"
	cfg.SetDefaults()
	require.Equal(t, []string{
		"SITE_URL=http://localhost:8065",
		"CALL_ID=8w8jorhr7j83uqr6y1st894hqe",
		"THREAD_ID=udzdsg7dwidbzcidx5khrf8nee",
		"AUTH_TOKEN=qj75unbsef83ik9p7ueypb6iyw",
		"TRANSCRIBE_API=whisper.cpp",
		"MODEL_SIZE=base",
		"OUTPUT_FORMAT=vtt",
	}, cfg.ToEnv())
}

func TestCallTranscriberConfigMap(t *testing.T) {
	var cfg CallTranscriberConfig
	cfg.SiteURL = "http://localhost:8065"
	cfg.CallID = "8w8jorhr7j83uqr6y1st894hqe"
	cfg.AuthToken = "qj75unbsef83ik9p7ueypb6iyw"
	cfg.ThreadID = "udzdsg7dwidbzcidx5khrf8nee"
	cfg.SetDefaults()

	t.Run("default config", func(t *testing.T) {
		var c CallTranscriberConfig
		err := c.FromMap(cfg.ToMap()).IsValid()
		require.NoError(t, err)
	})
}
