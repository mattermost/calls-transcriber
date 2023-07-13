package main

import (
	"fmt"

	"github.com/streamer45/calls-transcriber/cmd/transcriber/config"
)

type Transcriber struct {
	cfg config.TranscriberConfig
}

func NewTranscriber(cfg config.TranscriberConfig) (*Transcriber, error) {
	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}

	return &Transcriber{
		cfg: cfg,
	}, nil
}

func (t *Transcriber) Start() error {
	return nil
}

func (t *Transcriber) Stop() error {
	return nil
}
