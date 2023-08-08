package whisper

// #cgo LDFLAGS: -l:libwhisper.a -lm -lstdc++
// #include <whisper.h>
// #include <stdlib.h>
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

type Config struct {
	// The path to the GGML model file to use.
	ModelFile string
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
