package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/call"
	"github.com/mattermost/calls-transcriber/cmd/transcriber/config"
)

const (
	startTimeout = 30 * time.Second
)

func slogReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.SourceKey {
		source := a.Value.Any().(*slog.Source)
		if source.File == "" {
			// Log from a dependency (e.g. rtcd client).
			if pc, file, line, ok := runtime.Caller(7); ok {
				if f := runtime.FuncForPC(pc); f != nil {
					source.File = filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file)
					source.Line = line
				}
			}
		} else {
			source.File = filepath.Base(source.File)
		}
	}
	return a
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource:   true,
		Level:       slog.LevelDebug,
		ReplaceAttr: slogReplaceAttr,
	}))
	slog.SetDefault(logger)

	pid := os.Getpid()
	if err := os.WriteFile("/tmp/transcriber.pid", []byte(fmt.Sprintf("%d", pid)), 0666); err != nil {
		slog.Error("failed to write pid file", slog.String("err", err.Error()))
		os.Exit(1)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("failed to load config", slog.String("err", err.Error()))
		os.Exit(1)
	}
	cfg.SetDefaults()

	transcriber, err := call.NewTranscriber(cfg)
	if err != nil {
		slog.Error("failed to create call transcriber", slog.String("err", err.Error()))
		os.Exit(1)
	}

	slog.Info("starting transcriber")

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	if err := transcriber.Start(ctx); err != nil {
		if err := transcriber.ReportJobFailure(err.Error()); err != nil {
			slog.Error("failed to report job failure", slog.String("err", err.Error()))
		}

		slog.Error("failed to start transcriber", slog.String("err", err.Error()))
		os.Exit(1)
	}

	slog.Info("transcriber has started")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-transcriber.Done():
		if err := transcriber.Err(); err != nil {
			slog.Error("transcriber failed", slog.String("err", err.Error()))
			os.Exit(1)
		}
	case <-sig:
		slog.Info("received SIGTERM, stopping transcriber")
		if err := transcriber.Stop(context.Background()); err != nil {
			slog.Error("failed to stop transcriber", slog.String("err", err.Error()))
			os.Exit(1)
		}
	}

	slog.Info("transcriber has finished, exiting")
}
