package whisper

// #cgo LDFLAGS: -l:libwhisper.a -lm -lstdc++
// #include <whisper.h>
// #include <stdlib.h>
import "C"

import (
	"fmt"
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
}

func (c Config) IsValid() error {
	if c == (Config{}) {
		return fmt.Errorf("invalid empty config")
	}

	if c.ModelFile == "" {
		return fmt.Errorf("invalid ModelFile: should not be empty")
	}

	if numCPU := runtime.NumCPU(); c.NumThreads == 0 || c.NumThreads > numCPU {
		return fmt.Errorf("invalid NumThreads: should be in the range [1, %d]", numCPU)
	}

	if _, err := os.Stat(c.ModelFile); err != nil {
		return fmt.Errorf("invalid ModelFile: failed to stat model file: %w", err)
	}

	return nil
}

type Context struct {
	cfg Config
	ctx *C.struct_whisper_context
}

func NewContext(cfg Config) (*Context, error) {
	var c Context

	if err := cfg.IsValid(); err != nil {
		return nil, fmt.Errorf("failed to validate config: %w", err)
	}
	c.cfg = cfg

	// TODO: verify whether there's any potential optimizations
	// that could be made by using lower level initialization methods
	// such as whisper_init or whisper_init_from_buffer.
	path := C.CString(cfg.ModelFile)
	defer C.free(unsafe.Pointer(path))
	c.ctx = C.whisper_init_from_file(path)
	if c.ctx == nil {
		return nil, fmt.Errorf("failed to load model file")
	}

	return &c, nil
}

func (c *Context) Destroy() error {
	if c.ctx == nil {
		return fmt.Errorf("context is not initialized")
	}
	C.whisper_free(c.ctx)
	c.ctx = nil
	return nil
}

func (c *Context) Transcribe(samples []float32) ([]transcribe.Segment, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("samples should not be empty")
	}

	params := C.whisper_full_default_params(C.WHISPER_SAMPLING_GREEDY)
	params.no_context = C.bool(false)
	params.n_threads = C.int(c.cfg.NumThreads)
	params.max_len = C.int(8)
	params.split_on_word = C.bool(true)

	ret := C.whisper_full(c.ctx, params, (*C.float)(&samples[0]), C.int(len(samples)))
	if ret != 0 {
		return nil, fmt.Errorf("whisper_full failed with code %d", ret)
	}

	n := int(C.whisper_full_n_segments(c.ctx))
	segments := make([]transcribe.Segment, n)
	for i := 0; i < n; i++ {
		segments[i].Text = C.GoString(C.whisper_full_get_segment_text(c.ctx, C.int(i)))
		segments[i].StartTS = int64(C.whisper_full_get_segment_t0(c.ctx, C.int(i))) * 10
		segments[i].EndTS = int64(C.whisper_full_get_segment_t1(c.ctx, C.int(i))) * 10
	}

	return segments, nil
}
