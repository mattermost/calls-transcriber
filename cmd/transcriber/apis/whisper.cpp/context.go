package whisper

// #cgo linux LDFLAGS: -l:libwhisper.a -lm -lstdc++
// #cgo darwin LDFLAGS: -lwhisper -lstdc++ -framework Accelerate
// #include <whisper.h>
// #include <stdlib.h>
import "C"

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"unsafe"

	"github.com/mattermost/calls-transcriber/cmd/transcriber/transcribe"
)

type Config struct {
	// The path to the GGML model file to use.
	ModelFile string
	// The number of system threads to use to perform the transcription.
	NumThreads int
	// Whether or not past transcription should be used as prompt.
	NoContext bool
	// 512 = a bit more than 10s. Use multiples of 64. Results in a speedup of 3x at 512, b/c whisper was tuned for 30s chunks. See: https://github.com/ggerganov/whisper.cpp/pull/141
	// TODO: tests, validation
	AudioContext int
	// Whether or not to print progress to stdout (default false).
	PrintProgress bool
	// Language to use (defaults to autodetection).
	Language string
	// Whether or not to generate a single segment (default false).
	SingleSegment bool
}

func (c Config) IsValid() error {
	if c == (Config{}) {
		return fmt.Errorf("invalid empty config")
	}

	if c.ModelFile == "" {
		return fmt.Errorf("invalid ModelFile: should not be empty")
	}

	if _, err := os.Stat(c.ModelFile); err != nil {
		return fmt.Errorf("invalid ModelFile: failed to stat model file: %w", err)
	}

	if numCPU := runtime.NumCPU(); c.NumThreads == 0 || c.NumThreads > numCPU {
		return fmt.Errorf("invalid NumThreads: should be in the range [1, %d]", numCPU)
	}

	return nil
}

type Context struct {
	cfg     Config
	ctx     *C.struct_whisper_context
	cparams C.struct_whisper_context_params
	params  C.struct_whisper_full_params
}

func NewContext(cfg Config) (*Context, error) {
	var c Context

	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}
	c.cfg = cfg

	slog.Debug("creating transcription context", slog.Any("cfg", cfg))

	// TODO: verify whether there's any potential optimizations
	// that could be made by using lower level initialization methods
	// such as whisper_init or whisper_init_from_buffer.
	path := C.CString(cfg.ModelFile)
	defer C.free(unsafe.Pointer(path))

	c.cparams = C.whisper_context_default_params()
	c.ctx = C.whisper_init_from_file_with_params(path, c.cparams)
	if c.ctx == nil {
		return nil, fmt.Errorf("failed to load model file")
	}

	c.params = C.whisper_full_default_params(C.WHISPER_SAMPLING_GREEDY)
	c.params.no_context = C.bool(c.cfg.NoContext)
	c.params.audio_ctx = C.int(c.cfg.AudioContext)
	c.params.n_threads = C.int(c.cfg.NumThreads)
	if c.cfg.Language == "" {
		c.cfg.Language = "auto"
	}
	c.params.language = C.CString(c.cfg.Language)
	c.params.single_segment = C.bool(c.cfg.SingleSegment)
	c.params.print_progress = C.bool(c.cfg.PrintProgress)

	return &c, nil
}

func (c *Context) Destroy() error {
	if c.ctx == nil {
		return fmt.Errorf("context is not initialized")
	}
	C.whisper_free(c.ctx)
	C.free(unsafe.Pointer(c.params.language))
	c.ctx = nil
	return nil
}

func (c *Context) Transcribe(samples []float32) ([]transcribe.Segment, string, error) {
	if len(samples) == 0 {
		return nil, "", fmt.Errorf("samples should not be empty")
	}

	ret := C.whisper_full(c.ctx, c.params, (*C.float)(&samples[0]), C.int(len(samples)))
	if ret != 0 {
		return nil, "", fmt.Errorf("whisper_full failed with code %d", ret)
	}

	lang := C.GoString(C.whisper_lang_str(C.whisper_full_lang_id(c.ctx)))

	n := int(C.whisper_full_n_segments(c.ctx))
	segments := make([]transcribe.Segment, n)
	for i := 0; i < n; i++ {
		segments[i].Text = C.GoString(C.whisper_full_get_segment_text(c.ctx, C.int(i)))
		segments[i].StartTS = int64(C.whisper_full_get_segment_t0(c.ctx, C.int(i))) * 10
		segments[i].EndTS = int64(C.whisper_full_get_segment_t1(c.ctx, C.int(i))) * 10
	}

	return segments, lang, nil
}
