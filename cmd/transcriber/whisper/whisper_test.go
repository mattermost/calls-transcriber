package whisper

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigIsValid(t *testing.T) {
	tcs := []struct {
		name string
		cfg  Config
		err  string
	}{
		{
			name: "empty config",
			err:  "invalid empty config",
		},
		{
			name: "non existent model file",
			err:  "invalid ModelFile: failed to stat model file: stat /tmp/invalid.ggml: no such file or directory",
			cfg: Config{
				ModelFile: "/tmp/invalid.ggml",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.IsValid()
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewContext(t *testing.T) {
	t.Run("missing model file", func(t *testing.T) {
		ctx, err := NewContext(Config{})
		require.Error(t, err)
		require.Nil(t, ctx)
	})

	t.Run("success", func(t *testing.T) {
		ctx, err := NewContext(Config{
			ModelFile: "../../../models/ggml-tiny.en.bin",
		})
		require.NoError(t, err)
		require.NotNil(t, ctx)

		err = ctx.Destroy()
		require.NoError(t, err)
	})

	t.Run("destroy", func(t *testing.T) {
		ctx, err := NewContext(Config{
			ModelFile: "../../../models/ggml-tiny.en.bin",
		})
		require.NoError(t, err)
		require.NotNil(t, ctx)

		err = ctx.Destroy()
		require.NoError(t, err)

		err = ctx.Destroy()
		require.EqualError(t, err, "context is not initialized")
	})
}
