package transcribe

import (
	"regexp"
	"sort"
	"strings"
)

var (
	segmentSanitizationSpacesRE = regexp.MustCompile(`\s+`)
	// We allow spaces, dots, dashes, underscores, digits and letters in both ASCII and foreign alphabets.
	segmentSanitizationSpecialRE = regexp.MustCompile(`[^\s\d\pL\pN.\-_]`)
)

type namedSegment struct {
	Segment
	Speaker string
}

func (ns *namedSegment) sanitize(escapers ...func(string) string) {
	// Remove unwanted special characters
	ns.Speaker = segmentSanitizationSpecialRE.ReplaceAllString(ns.Speaker, "")

	// Remove any left extra space
	ns.Text = strings.TrimSpace(ns.Text)
	ns.Speaker = strings.TrimSpace(ns.Speaker)
	ns.Text = segmentSanitizationSpacesRE.ReplaceAllString(ns.Text, " ")
	ns.Speaker = segmentSanitizationSpacesRE.ReplaceAllString(ns.Speaker, " ")

	for _, escaper := range escapers {
		ns.Text = escaper(ns.Text)
		ns.Speaker = escaper(ns.Speaker)
	}
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
