package transcribe

import (
	"fmt"
	"sort"
	"strings"
)

type namedSegment struct {
	Segment
	Speaker string
}

// vttTS converts ts milliseconds in the 00:00:00.000 format.
func vttTS(ts int64) string {
	sMs := int64(1000)
	mMs := 60 * sMs
	hMs := 60 * mMs

	h := ts / hMs
	m := (ts - (h * hMs)) / mMs
	s := ((ts - (h * hMs)) - m*mMs) / sMs
	ms := ((ts - (h * hMs)) - m*mMs) - s*sMs

	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
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

func (t Transcription) WebVTT() string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	ns := t.interleave()
	for _, s := range ns {
		fmt.Fprintf(&b, "%s --> %s\n", vttTS(s.StartTS), vttTS(s.EndTS))
		fmt.Fprintf(&b, "<v %s>%s\n\n", s.Speaker, s.Text)
	}
	return b.String()
}
