package call

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/ogg"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/pion/rtp"

	"github.com/stretchr/testify/require"
)

const (
	testFrameGP      = trackInFrameSize // one 20ms frame in granule position units
	testFrameSamples = trackOutFrameSize
)

func TestGapFillerPadSamples(t *testing.T) {
	// feed pads and then "decodes" a 20ms packet for the page at gp.
	feed := func(f *gapFiller, pcmLen *int, gp uint64) int {
		pad := f.padSamples(gp, *pcmLen)
		*pcmLen += pad + testFrameSamples
		return pad
	}

	t.Run("first page of a chunk is not padded", func(t *testing.T) {
		var f gapFiller
		var pcmLen int
		require.Zero(t, feed(&f, &pcmLen, 1))
		require.Equal(t, testFrameSamples, pcmLen)
	})

	t.Run("contiguous pages", func(t *testing.T) {
		var f gapFiller
		var pcmLen int
		for i := 0; i < 100; i++ {
			require.Zero(t, feed(&f, &pcmLen, 1+uint64(i)*testFrameGP))
		}
		require.Equal(t, 100*testFrameSamples, pcmLen)
	})

	// The gap is the granule delta between the start of two consecutive pages. The
	// previous packet already covers one frame of it.
	tcs := []struct {
		name  string
		delta uint64
		want  int
	}{
		{"20ms", 20 * trackInAudioSamplesPerMs, 0},
		{"DTX 400ms", 400 * trackInAudioSamplesPerMs, 380 * trackOutAudioSamplesPerMs},
		{"just below the threshold", 999 * trackInAudioSamplesPerMs, 979 * trackOutAudioSamplesPerMs},
		{"at the threshold", 1000 * trackInAudioSamplesPerMs, 980 * trackOutAudioSamplesPerMs},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			var f gapFiller
			var pcmLen int
			require.Zero(t, feed(&f, &pcmLen, 1))
			require.Equal(t, tc.want, feed(&f, &pcmLen, 1+tc.delta))
			// The padded PCM ends exactly where the next packet starts.
			require.Equal(t, int(tc.delta/3)+testFrameSamples, pcmLen)
		})
	}

	t.Run("no drift across many small gaps", func(t *testing.T) {
		var f gapFiller
		var pcmLen int
		require.Zero(t, feed(&f, &pcmLen, 1))

		// 19201 isn't a multiple of 3, so each gap truncates when converted to 16kHz
		// samples. Per-gap rounding would lose ~1000 samples over this run.
		gp := uint64(1)
		for i := 0; i < 1000; i++ {
			gp += 19201
			pcmLen += f.padSamples(gp, pcmLen)
			require.Equal(t, int((gp-1)/3), pcmLen)
			pcmLen += testFrameSamples
		}
	})

	t.Run("first page after a split is not padded", func(t *testing.T) {
		var f gapFiller
		var pcmLen int
		require.Zero(t, feed(&f, &pcmLen, 1))

		f.reset()
		pcmLen = 0
		require.Zero(t, feed(&f, &pcmLen, 1+10*1000*trackInAudioSamplesPerMs))
		require.Equal(t, testFrameSamples, pcmLen)
		require.Equal(t, 380*trackOutAudioSamplesPerMs, feed(&f, &pcmLen, 1+10*1000*trackInAudioSamplesPerMs+400*trackInAudioSamplesPerMs))
	})

	t.Run("granule position going backwards", func(t *testing.T) {
		var f gapFiller
		require.Zero(t, f.padSamples(5000, 0))
		require.Zero(t, f.padSamples(1000, 320))
	})
}

// writeTestTrack writes the first len(deltas) Opus packets of src to a new track
// file. deltas[i] is the RTP timestamp increment before packet i (deltas[0] is
// ignored), which lets a test simulate the gaps Opus DTX leaves in a track.
func writeTestTrack(t *testing.T, src string, deltas []uint32) string {
	t.Helper()

	f, err := os.Open(src)
	require.NoError(t, err)
	defer f.Close()

	reader, _, err := ogg.NewReaderWith(f)
	require.NoError(t, err)

	var payloads [][]byte
	for len(payloads) < len(deltas) {
		data, hdr, err := reader.ParseNextPage()
		require.False(t, errors.Is(err, io.EOF), "%s has less than %d packets", src, len(deltas))
		require.NoError(t, err)
		if hdr.GranulePosition == 0 {
			continue
		}
		payloads = append(payloads, data)
	}

	filename := filepath.Join(t.TempDir(), "track.ogg")
	w, err := ogg.NewWriter(filename, trackInAudioRate, trackAudioChannels)
	require.NoError(t, err)

	ts := uint32(1000)
	for i, payload := range payloads {
		if i > 0 {
			ts += deltas[i]
		}
		require.NoError(t, w.WriteRTP(&rtp.Packet{Header: rtp.Header{Timestamp: ts}, Payload: payload}, 0))
	}
	require.NoError(t, w.Close())

	return filename
}

func TestDecodeAudioGapFill(t *testing.T) {
	src := "../../../testfiles/speech_contiguous.opus"
	frame := uint32(testFrameGP)
	dtx := uint32(400 * trackInAudioSamplesPerMs)

	repeat := func(inc uint32, n int) []uint32 {
		out := make([]uint32, n)
		for i := range out {
			out[i] = inc
		}
		return out
	}
	join := func(parts ...[]uint32) []uint32 {
		var out []uint32
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	newCtx := func(filename string) trackContext {
		return trackContext{
			trackID:  "trackID",
			filename: filename,
			user:     &model.User{Username: "testuser"},
		}
	}

	// sumDeltas adds up the increments after the first packet, which is the offset
	// between the start of the first and the start of the last packet.
	sumDeltas := func(incs []uint32) uint64 {
		var total uint64
		for _, inc := range incs[1:] {
			total += uint64(inc)
		}
		return total
	}
	// coveredSamples is the same offset in 16kHz samples.
	coveredSamples := func(incs []uint32) int {
		return int(sumDeltas(incs) * trackOutAudioSamplesPerMs / trackInAudioSamplesPerMs)
	}

	t.Run("contiguous audio is unchanged", func(t *testing.T) {
		incs := repeat(frame, 50)
		samples, err := newCtx(writeTestTrack(t, src, incs)).decodeAudio()
		require.NoError(t, err)
		require.Len(t, samples, 1)
		require.InDelta(t, coveredSamples(incs), len(samples[0].pcm), float64(testFrameSamples))
	})

	t.Run("DTX gaps keep their duration", func(t *testing.T) {
		// A few contiguous packets, then one packet every 400ms as DTX sends.
		incs := join(repeat(frame, 10), repeat(dtx, 40))
		samples, err := newCtx(writeTestTrack(t, src, incs)).decodeAudio()
		require.NoError(t, err)
		require.Len(t, samples, 1)

		// Without the fix this would be 50 frames, ~1s instead of ~16s.
		require.InDelta(t, coveredSamples(incs), len(samples[0].pcm), float64(testFrameSamples))
		require.Greater(t, len(samples[0].pcm), 15*trackOutAudioRate)
	})

	t.Run("gaps over the threshold still split", func(t *testing.T) {
		long := uint32(5 * trackInAudioRate)
		first := join(repeat(frame, 5), repeat(dtx, 5))
		second := join([]uint32{long}, repeat(dtx, 5))
		incs := join(first, second)

		samples, err := newCtx(writeTestTrack(t, src, incs)).decodeAudio()
		require.NoError(t, err)
		require.Len(t, samples, 2)

		// Each chunk covers its own span and the long gap is not padded. The offset of
		// the second chunk is carried by its startTS.
		require.InDelta(t, coveredSamples(first), len(samples[0].pcm), float64(testFrameSamples))
		require.InDelta(t, coveredSamples(second), len(samples[1].pcm), float64(testFrameSamples))
		require.Equal(t, int64((1+sumDeltas(first)+uint64(long))/trackInAudioSamplesPerMs), samples[1].startTS)
	})
}
