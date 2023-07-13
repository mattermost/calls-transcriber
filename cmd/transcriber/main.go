package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/streamer45/calls-transcriber/cmd/transcriber/config"
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

	transcriber, err := NewTranscriber(cfg)
	if err != nil {
		log.Fatalf("failed to create transcriber: %s", err)
	}

	log.Printf("starting transcriber")

	if err := transcriber.Start(); err != nil {
		log.Fatalf("failed to start transcriber: %s", err)
	}

	log.Printf("transcriber has started")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("received SIGTERM, stopping transcriber")

	if err := transcriber.Stop(); err != nil {
		log.Fatalf("failed to stop transcriber: %s", err)
	}

	log.Printf("transcriber has finished, exiting")
}
