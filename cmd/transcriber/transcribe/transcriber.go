package transcribe

const DefaultLanguage = "en"

type Transcriber interface {
	Transcribe(samples []float32) ([]Segment, string, error)
	Destroy() error
}

type Segment struct {
	Text    string
	StartTS int64
	EndTS   int64
}

type TrackTranscription struct {
	Speaker  string
	Language string
	Segments []Segment
}

type Transcription []TrackTranscription

func (tr Transcription) Language() string {
	// Here we make a reasonable assumption. That the language of the
	// transcription is equal to the first detected language. We default to
	// English if none is found.
	for _, t := range tr {
		if t.Language != "" {
			return t.Language
		}
	}
	return DefaultLanguage
}
