package whisper

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func getModelPath() string {
	modelsDir := os.Getenv("MODELS_DIR")
	if modelsDir == "" {
		modelsDir = "../../../../models"
	}
	return filepath.Join(modelsDir, "ggml-tiny.bin")
}

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
		{
			name: "valid",
			cfg: Config{
				ModelFile:  getModelPath(),
				NumThreads: 1,
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
			NumThreads: 1,
			ModelFile:  getModelPath(),
		})
		require.NoError(t, err)
		require.NotNil(t, ctx)

		err = ctx.Destroy()
		require.NoError(t, err)
	})

	t.Run("destroy", func(t *testing.T) {
		ctx, err := NewContext(Config{
			NumThreads: 1,
			ModelFile:  getModelPath(),
		})
		require.NoError(t, err)
		require.NotNil(t, ctx)

		err = ctx.Destroy()
		require.NoError(t, err)

		err = ctx.Destroy()
		require.EqualError(t, err, "context is not initialized")
	})
}

func TestTranscribe(t *testing.T) {
	ctx, err := NewContext(Config{
		NumThreads: 1,
		ModelFile:  getModelPath(),
	})
	require.NoError(t, err)
	require.NotNil(t, ctx)

	data, err := os.ReadFile("../../../../testfiles/sample.pcm")
	require.NoError(t, err)

	samples := make([]float32, 0, len(data)/4)
	for i := 0; i < len(data); i += 4 {
		samples = append(samples, math.Float32frombits(binary.LittleEndian.Uint32(data[i:i+4])))
	}

	segments, err := ctx.Transcribe(samples)
	require.NoError(t, err)
	require.NotEmpty(t, segments)
	require.Equal(t, " This is a test transcription sample.", segments[0].Text)

	err = ctx.Destroy()
	require.NoError(t, err)
}
