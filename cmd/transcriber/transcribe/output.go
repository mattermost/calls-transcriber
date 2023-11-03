package transcribe

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

type namedSegment struct {
	Segment
	Speaker string
}

// vttTS converts ts milliseconds in the 00:00:00.000 format.
func vttTS(ts int64, withMs bool) string {
	sMs := int64(1000)
	mMs := 60 * sMs
	hMs := 60 * mMs

	h := ts / hMs
	m := (ts - (h * hMs)) / mMs

	if withMs {
		s := ((ts - (h * hMs)) - m*mMs) / sMs
		ms := ((ts - (h * hMs)) - m*mMs) - s*sMs
		return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
	}

	s := int64(math.Round(float64(((ts - (h * hMs)) - m*mMs)) / float64(sMs)))
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func (t Transcription) interleave() []namedSegment {
	var nss []namedSegment

	for _, trackTr := range t {
		for _, s := range trackTr.Segments {
			var ns namedSegment
			ns.Segment = s
			ns.Speaker = trackTr.Speaker
			nss = append(nss, ns)
		}
	}

	sort.Slice(nss, func(i, j int) bool {
		return nss[i].StartTS < nss[j].StartTS
	})

	return nss
}

type WebVTTOptions struct {
	OmitSpeaker bool
}

func (t Transcription) WebVTT(w io.Writer, opts WebVTTOptions) error {
	_, err := fmt.Fprintf(w, "WEBVTT\n")
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	for _, s := range t.interleave() {
		_, err = fmt.Fprintf(w, "\n%s --> %s\n", vttTS(s.StartTS, true), vttTS(s.EndTS, true))
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}
		tmpl := "<v %[1]s>(%[1]s) %[2]s\n"
		if opts.OmitSpeaker {
			tmpl = "%[2]s\n"
		}
		_, err = fmt.Fprintf(w, tmpl, s.Speaker, s.Text)
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}
	}

	return nil
}

func (t Transcription) Text(w io.Writer) error {
	for i, s := range t.interleave() {
		nl := "\n"
		if i == 0 {
			nl = ""
		}
		_, err := fmt.Fprintf(w, "%s%v -> %v\n", nl, vttTS(s.StartTS, false), vttTS(s.EndTS, false))
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}
		_, err = fmt.Fprintf(w, "%s\n%s\n", s.Speaker, strings.TrimSpace(s.Text))
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}
	}

	return nil
}
