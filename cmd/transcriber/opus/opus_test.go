package opus

import (
	"io"
	"os"
	"testing"

	"github.com/pion/webrtc/v4/pkg/media/oggreader"
	"github.com/stretchr/testify/require"
)

func TestOpusDecode(t *testing.T) {
	f, err := os.Open("../../../testfiles/sample.opus")
	require.NoError(t, err)
	defer f.Close()

	ogg, _, err := oggreader.NewWith(f)
	require.NoError(t, err)

	rate := 16000
	frameSize := 20 * rate / 1000
	samples := make([]float32, frameSize)

	dec, err := NewDecoder(rate, 1)
	require.NoError(t, err)
	require.NotNil(t, dec)

	for {
		data, hdr, err := ogg.ParseNextPage()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.NotEmpty(t, data)

		if hdr.GranulePosition == 0 {
			continue
		}

		n, err := dec.Decode(data, samples)
		require.NoError(t, err)
		require.Equal(t, frameSize, n)
	}

	err = dec.Destroy()
	require.NoError(t, err)
}

func BenchmarkOpusDecode(b *testing.B) {
	f, err := os.Open("../../../testfiles/sample.opus")
	require.NoError(b, err)
	defer f.Close()

	ogg, _, err := oggreader.NewWith(f)
	require.NoError(b, err)

	samples := make([]float32, 320)

	dec, err := NewDecoder(16000, 1)
	require.NoError(b, err)
	require.NotNil(b, dec)

	b.StopTimer()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, hdr, err := ogg.ParseNextPage()
		if err == io.EOF {
			ogg.ResetReader(func(_ int64) io.Reader {
				_, _ = f.Seek(0, 0)
				return f
			})
			data, hdr, err = ogg.ParseNextPage()
			require.NoError(b, err)
		}
		if hdr.GranulePosition == 0 {
			continue
		}
		b.StartTimer()
		n, err := dec.Decode(data, samples)
		b.StopTimer()
		require.NoError(b, err)
		require.Equal(b, 320, n)
	}

	err = dec.Destroy()
	require.NoError(b, err)
}
