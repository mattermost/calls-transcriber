package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigIsValid(t *testing.T) {
	tcs := []struct {
		name          string
		cfg           TranscriberConfig
		expectedError string
	}{
		{
			name:          "empty config",
			cfg:           TranscriberConfig{},
			expectedError: "config cannot be empty",
		},
		{
			name: "invalid SiteURL schema",
			cfg: TranscriberConfig{
				SiteURL: "invalid://localhost",
			},
			expectedError: "SiteURL parsing failed: invalid scheme \"invalid\"",
		},
		{
			name: "missing CallID",
			cfg: TranscriberConfig{
				SiteURL: "http://localhost:8065",
			},
			expectedError: "CallID cannot be empty",
		},
		{
			name: "missing ThreadID",
			cfg: TranscriberConfig{
				SiteURL:   "http://localhost:8065",
				CallID:    "8w8jorhr7j83uqr6y1st894hqe",
				AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			},
			expectedError: "ThreadID cannot be empty",
		},
		{
			name: "missing AuthToken",
			cfg: TranscriberConfig{
				SiteURL:  "http://localhost:8065",
				CallID:   "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID: "udzdsg7dwidbzcidx5khrf8nee",
			},
			expectedError: "AuthToken cannot be empty",
		},
		{
			name: "invalid video preset",
			cfg: TranscriberConfig{
				SiteURL:      "http://localhost:8065",
				CallID:       "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:     "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:    "qj75unbsef83ik9p7ueypb6iyw",
				OutputFormat: OutputFormatVTT,
			},
			expectedError: "ModelSize value is not valid",
		},
		{
			name: "invalid format",
			cfg: TranscriberConfig{
				SiteURL:   "http://localhost:8065",
				CallID:    "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:  "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			},
			expectedError: "OutputFormat value is not valid",
		},
		{
			name: "valid config",
			cfg: TranscriberConfig{
				SiteURL:      "http://localhost:8065",
				CallID:       "8w8jorhr7j83uqr6y1st894hqe",
				ThreadID:     "udzdsg7dwidbzcidx5khrf8nee",
				AuthToken:    "qj75unbsef83ik9p7ueypb6iyw",
				ModelSize:    ModelSizeMedium,
				OutputFormat: OutputFormatVTT,
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
		var cfg TranscriberConfig
		cfg.SetDefaults()
		require.Equal(t, TranscriberConfig{
			ModelSize:    ModelSizeDefault,
			OutputFormat: OutputFormatDefault,
		}, cfg)
	})

	t.Run("no overrides", func(t *testing.T) {
		cfg := TranscriberConfig{
			ModelSize: ModelSizeMedium,
		}
		cfg.SetDefaults()
		require.Equal(t, TranscriberConfig{
			ModelSize:    ModelSizeMedium,
			OutputFormat: OutputFormatDefault,
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
		os.Setenv("MODEL_SIZE", "medium")
		defer os.Unsetenv("MODEL_SIZE")
		cfg, err := LoadFromEnv()
		require.NoError(t, err)
		require.NotEmpty(t, cfg)
		require.Equal(t, TranscriberConfig{
			SiteURL:   "http://localhost:8065",
			CallID:    "8w8jorhr7j83uqr6y1st894hqe",
			ThreadID:  "udzdsg7dwidbzcidx5khrf8nee",
			AuthToken: "qj75unbsef83ik9p7ueypb6iyw",
			ModelSize: ModelSizeMedium,
		}, cfg)
	})
}

func TestTranscriberConfigToEnv(t *testing.T) {
	var cfg TranscriberConfig
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
		"MODEL_SIZE=base",
		"OUTPUT_FORMAT=vtt",
	}, cfg.ToEnv())
}

func TestTranscriberConfigMap(t *testing.T) {
	var cfg TranscriberConfig
	cfg.SiteURL = "http://localhost:8065"
	cfg.CallID = "8w8jorhr7j83uqr6y1st894hqe"
	cfg.AuthToken = "qj75unbsef83ik9p7ueypb6iyw"
	cfg.ThreadID = "udzdsg7dwidbzcidx5khrf8nee"
	cfg.SetDefaults()

	t.Run("default config", func(t *testing.T) {
		var c TranscriberConfig
		err := c.FromMap(cfg.ToMap()).IsValid()
		require.NoError(t, err)
	})
}
