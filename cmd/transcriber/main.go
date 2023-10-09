package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/call"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
)

const (
	startTimeout = 30 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	pid := os.Getpid()
	if err := os.WriteFile("/tmp/transcriber.pid", []byte(fmt.Sprintf("%d", pid)), 0666); err != nil {
		log.Fatalf("failed to write pid file: %s", err)
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("failed to load config: %s", err)
	}
	cfg.SetDefaults()

	transcriber, err := call.NewTranscriber(cfg)
	if err != nil {
		log.Fatalf("failed to create call transcriber: %s", err)
	}

	log.Printf("starting transcriber")

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	if err := transcriber.Start(ctx); err != nil {
		if err := transcriber.ReportJobFailure(err.Error()); err != nil {
			log.Printf("failed to report job failure: %s", err)
		}

		log.Fatalf("failed to start transcriber: %s", err)
	}

	log.Printf("transcriber has started")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-transcriber.Done():
		if err := transcriber.Err(); err != nil {
			log.Fatalf("transcriber failed: %s", err)
		}
	case <-sig:
		log.Printf("received SIGTERM, stopping transcriber")
		if err := transcriber.Stop(context.Background()); err != nil {
			log.Fatalf("failed to stop transcriber: %s", err)
		}
	}

	log.Printf("transcriber has finished, exiting")
}
