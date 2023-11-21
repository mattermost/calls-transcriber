package transcribe

import (
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"strconv"
)

type WebVTTOptions struct {
	OmitSpeaker bool
}

func (o *WebVTTOptions) IsValid() error {
	return nil
}

func (o *WebVTTOptions) IsEmpty() bool {
	return o == nil || *o == WebVTTOptions{}
}

func (o *WebVTTOptions) SetDefaults() {
	o.OmitSpeaker = false
}

func (o *WebVTTOptions) FromEnv() {
	o.OmitSpeaker, _ = strconv.ParseBool(os.Getenv("WEBVTT_OMIT_SPEAKER"))
}

func (o *WebVTTOptions) ToEnv() []string {
	return []string{
		fmt.Sprintf("WEBVTT_OMIT_SPEAKER=%t", o.OmitSpeaker),
	}
}

func (o *WebVTTOptions) FromMap(m map[string]any) {
	o.OmitSpeaker, _ = m["webvtt_omit_speaker"].(bool)
}

func (o *WebVTTOptions) ToMap() map[string]any {
	return map[string]any{
		"webvtt_omit_speaker": o.OmitSpeaker,
	}
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

func (t Transcription) WebVTT(w io.Writer, opts WebVTTOptions) error {
	_, err := fmt.Fprintf(w, "WEBVTT\n")
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	for _, s := range t.interleave() {
		s.sanitize(html.EscapeString)

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
