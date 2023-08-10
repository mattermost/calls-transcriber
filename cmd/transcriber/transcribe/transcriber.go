package transcribe

type Transcriber interface {
	Transcribe(samples []float32) (Transcription, error)
	Destroy() error
}

type Transcription struct {
}

func (t Transcription) WebVTT() string {
	return ""
}
