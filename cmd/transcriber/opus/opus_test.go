package opus

import (
	"io"
	"os"
	"testing"

	"github.com/pion/webrtc/v3/pkg/media/oggreader"
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
