package transcribe

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
)

type TextCompactOptions struct {
	SilenceThresholdMs   int
	MaxSegmentDurationMs int
}

func (o *TextCompactOptions) SetDefaults() {
	o.SilenceThresholdMs = 2000
	o.MaxSegmentDurationMs = 10000
}

func (o *TextCompactOptions) IsEmpty() bool {
	return o == nil || *o == TextCompactOptions{}
}

type TextOptions struct {
	CompactOptions TextCompactOptions
}

func (o *TextOptions) SetDefaults() {
	o.CompactOptions.SetDefaults()
}

func (o *TextOptions) IsValid() error {
	if o.CompactOptions.SilenceThresholdMs <= 0 {
		return fmt.Errorf("SilenceThresholdMs should be a positive number")
	}

	if o.CompactOptions.MaxSegmentDurationMs <= 0 {
		return fmt.Errorf("MaxSegmentDurationMs should be a positive number")
	}

	return nil
}

func (o *TextOptions) IsEmpty() bool {
	return o.CompactOptions.IsEmpty()
}

func (o *TextOptions) ToEnv() []string {
	return []string{
		fmt.Sprintf("TEXT_COMPACT_SILENCE_THRESHOLD_MS=%d", o.CompactOptions.SilenceThresholdMs),
		fmt.Sprintf("TEXT_COMPACT_MAX_SEGMENT_DURATION_MS=%d", o.CompactOptions.MaxSegmentDurationMs),
	}
}

func (o *TextOptions) FromEnv() {
	o.CompactOptions.SilenceThresholdMs, _ = strconv.Atoi(os.Getenv("TEXT_COMPACT_SILENCE_THRESHOLD_MS"))
	o.CompactOptions.MaxSegmentDurationMs, _ = strconv.Atoi(os.Getenv("TEXT_COMPACT_MAX_SEGMENT_DURATION_MS"))
}

func (o *TextOptions) ToMap() map[string]any {
	return map[string]any{
		"text_compact_silence_threshold_ms":    o.CompactOptions.SilenceThresholdMs,
		"text_compact_max_segment_duration_ms": o.CompactOptions.MaxSegmentDurationMs,
	}
}

func (o *TextOptions) FromMap(m map[string]any) {
	// These can either be int or float64 dependning whether they have been
	// previously marshaled or not.
	switch m["text_compact_silence_threshold_ms"].(type) {
	case int:
		o.CompactOptions.SilenceThresholdMs = m["text_compact_silence_threshold_ms"].(int)
	case float64:
		o.CompactOptions.SilenceThresholdMs = int(m["text_compact_silence_threshold_ms"].(float64))
	}

	switch m["text_compact_max_segment_duration_ms"].(type) {
	case int:
		o.CompactOptions.MaxSegmentDurationMs = m["text_compact_max_segment_duration_ms"].(int)
	case float64:
		o.CompactOptions.MaxSegmentDurationMs = int(m["text_compact_max_segment_duration_ms"].(float64))
	}
}

func compactSegments(segments []namedSegment, opts TextCompactOptions) []namedSegment {
	if len(segments) < 2 {
		return segments
	}

	out := []namedSegment{segments[0]}

	for i := 1; i < len(segments); i++ {
		currSeg := segments[i]
		prevSeg := segments[i-1]

		// We join the segments if:
		// - The speaker hasn't changed. This is required to guarantee order (e.g. question/answer sequences).
		// - There's less than silenceThresholdMs of pause between the end of a previous text segment and the start of the next one.
		// - The overall (running) duration of the joined segments is less than maxDurationMs seconds.
		if currSeg.Speaker == prevSeg.Speaker &&
			int(currSeg.StartTS-prevSeg.EndTS) < opts.SilenceThresholdMs &&
			int(currSeg.StartTS-out[len(out)-1].StartTS) < opts.MaxSegmentDurationMs {

			slog.Debug(fmt.Sprintf("%d and %d can be joined", i-1, i))
			out[len(out)-1].Text += " " + currSeg.Text
			out[len(out)-1].EndTS = currSeg.EndTS
		} else {
			out = append(out, currSeg)
		}
	}

	slog.Debug("compact done", slog.Int("inLen", len(segments)), slog.Int("outLen", len(out)))

	return out
}

func (t Transcription) Text(w io.Writer, opts TextOptions) error {
	segments := t.interleave()

	if !opts.CompactOptions.IsEmpty() {
		segments = compactSegments(segments, opts.CompactOptions)
	}

	for i, s := range segments {
		s.sanitize()

		nl := "\n"
		if i == 0 {
			nl = ""
		}
		_, err := fmt.Fprintf(w, "%s%v -> %v\n", nl, vttTS(s.StartTS, false), vttTS(s.EndTS, false))
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}
		_, err = fmt.Fprintf(w, "%s\n%s\n", s.Speaker, s.Text)
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}
	}

	return nil
}
