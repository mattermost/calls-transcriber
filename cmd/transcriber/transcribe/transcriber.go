package transcribe

type Transcriber interface {
	Transcribe(samples []float32) ([]Segment, error)
	Destroy() error
}

type Segment struct {
	Text    string
	StartTS int64
	EndTS   int64
}

type TrackTranscription struct {
	Speaker  string
	Segments []Segment
}

type Transcription []TrackTranscription
