package call

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveGapFiller(t *testing.T) {
	// feed returns the padding and reset for a packet at ts, then "decodes" 20ms of it.
	feed := func(f *liveGapFiller, ts uint32) (int, bool) {
		pad, reset, ok := f.next(ts)
		require.True(t, ok)
		if reset {
			f.fed = 0 // the caller drops the window
		}
		f.decoded(testFrameSamples)
		return pad, reset
	}
	const ms = trackInAudioSamplesPerMs

	t.Run("first packet is not padded", func(t *testing.T) {
		var f liveGapFiller
		pad, reset := feed(&f, 123456)
		require.Zero(t, pad)
		require.False(t, reset)
	})

	t.Run("contiguous packets", func(t *testing.T) {
		var f liveGapFiller
		ts := uint32(1000)
		for i := 0; i < 100; i++ {
			pad, reset := feed(&f, ts)
			require.Zero(t, pad)
			require.False(t, reset)
			ts += testFrameGP
		}
		require.Equal(t, 100*testFrameSamples, f.fed)
	})

	tcs := []struct {
		name  string
		delta uint32
		want  int
		reset bool
	}{
		{"20ms", 20 * ms, 0, false},
		{"DTX 400ms", 400 * ms, 380 * trackOutAudioSamplesPerMs, false},
		{"just below the reset threshold", liveGapThreshold - ms, (3000 - 1 - 20) * trackOutAudioSamplesPerMs, false},
		{"at the reset threshold", liveGapThreshold, 0, true},
		{"above the reset threshold", 10 * trackInAudioRate, 0, true},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			var f liveGapFiller
			feed(&f, 5000)
			pad, reset := feed(&f, 5000+tc.delta)
			require.Equal(t, tc.want, pad)
			require.Equal(t, tc.reset, reset)
			if !tc.reset {
				// The window ends exactly where the new packet ends.
				require.Equal(t, int(tc.delta/3)+testFrameSamples, f.fed)
			} else {
				// The new window starts with the new packet only.
				require.Equal(t, testFrameSamples, f.fed)
				pad, reset = feed(&f, 5000+tc.delta+400*ms)
				require.Equal(t, 380*trackOutAudioSamplesPerMs, pad)
				require.False(t, reset)
			}
		})
	}

	t.Run("wraparound", func(t *testing.T) {
		var f liveGapFiller
		start := uint32(math.MaxUint32 - 100*ms)
		feed(&f, start)

		// 400ms later the timestamp has wrapped.
		pad, reset := feed(&f, start+400*ms)
		require.Less(t, start+400*ms, start)
		require.Equal(t, 380*trackOutAudioSamplesPerMs, pad)
		require.False(t, reset)

		// And a gap at the threshold across the wrap still resets.
		_, reset = feed(&f, start+400*ms+liveGapThreshold)
		require.True(t, reset)
	})

	t.Run("no drift across many small gaps", func(t *testing.T) {
		var f liveGapFiller
		ts := uint32(7)
		feed(&f, ts)
		var total uint64
		for i := 0; i < 1000; i++ {
			// 19201 isn't a multiple of 3, so per-gap truncation would lose samples.
			ts += 19201
			total += 19201
			f.next(ts)
			require.Equal(t, int(total/3), f.fed)
			f.decoded(testFrameSamples)
		}
	})

	t.Run("older packet is ignored", func(t *testing.T) {
		var f liveGapFiller
		feed(&f, 10000)
		fed := f.fed
		pad, reset, ok := f.next(9000)
		require.Zero(t, pad)
		require.False(t, reset)
		require.False(t, ok)
		require.Equal(t, fed, f.fed)

		// And the next in-order packet is still measured from the last accepted one.
		pad, _, ok = f.next(10000 + 400*ms)
		require.True(t, ok)
		require.Equal(t, 400*trackOutAudioSamplesPerMs-fed, pad)
	})

	t.Run("reset starts a new window", func(t *testing.T) {
		var f liveGapFiller
		feed(&f, 1)
		f.reset()
		pad, reset := feed(&f, 1+400*ms)
		require.Zero(t, pad)
		require.False(t, reset)
	})
}

func TestClampPad(t *testing.T) {
	const limit = 12 * 1000 * trackOutAudioSamplesPerMs

	tcs := []struct {
		name      string
		pad       int
		windowLen int
		want      int
	}{
		{"no padding", 0, 1000, 0},
		{"plenty of room", 380 * trackOutAudioSamplesPerMs, 1000, 380 * trackOutAudioSamplesPerMs},
		{"exactly fits", limit - 1000 - testFrameSamples, 1000, limit - 1000 - testFrameSamples},
		{"clamped to the room left", limit, 1000, limit - 1000 - testFrameSamples},
		{"window full", 1000, limit - testFrameSamples, 0},
		{"window over the limit", 1000, limit + 5000, 0},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, clampPad(tc.pad, tc.windowLen, limit))
		})
	}

	t.Run("sender claiming large gaps can't grow the window past the limit", func(t *testing.T) {
		var f liveGapFiller
		var windowLen int
		// A packet every 20ms of wall clock time with an RTP delta just below the reset threshold.
		ts := uint32(1)
		for i := 0; i < 600; i++ {
			pad, reset, ok := f.next(ts)
			require.True(t, ok)
			require.False(t, reset)
			pad = clampPad(pad, windowLen, limit)
			windowLen += pad
			if i < 3 {
				require.LessOrEqual(t, windowLen, limit)
			}
			windowLen += testFrameSamples
			f.decoded(testFrameSamples)
			ts += liveGapThreshold - 1
		}
		// Past the limit the window only grows by the real decoded audio.
		require.LessOrEqual(t, windowLen, limit+600*testFrameSamples)
	})
}
