//go:build amd64

package main

import (
	"fmt"

	"golang.org/x/sys/cpu"
)

// checkCPUFeatures verifies the host supports the instruction set whisper.cpp
// was built for (see build/prepare_deps.sh). A SIGILL raised inside cgo can't
// be recovered, so we fail early with a clear error instead.
//
// F16C is also required by the build but isn't exposed by x/sys/cpu; every
// CPU with AVX2 and FMA in practice has it.
func checkCPUFeatures() error {
	var missing []string
	if !cpu.X86.HasAVX2 {
		missing = append(missing, "AVX2")
	}
	if !cpu.X86.HasFMA {
		missing = append(missing, "FMA")
	}
	if len(missing) > 0 {
		return fmt.Errorf("unsupported CPU: missing required instruction set extensions %v (AVX2 and FMA are required; check that the host CPU or VM exposes them)", missing)
	}
	return nil
}
