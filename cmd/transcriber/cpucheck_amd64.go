//go:build amd64

package main

import (
	"fmt"
	"strings"

	"github.com/klauspost/cpuid/v2"
)

// requiredCPUFeatures must match the instruction set whisper.cpp is built for
// in build/prepare_deps.sh (-DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON, with
// BMI2 and AVX-512 off). Keep the two in sync.
var requiredCPUFeatures = []cpuid.FeatureID{cpuid.AVX2, cpuid.FMA3, cpuid.F16C}

// checkCPUFeatures verifies the host supports the instruction set whisper.cpp
// was built for. A SIGILL raised inside cgo can't be recovered, so we fail
// early with a clear error instead.
func checkCPUFeatures() error {
	var missing []string
	for _, f := range requiredCPUFeatures {
		if !cpuid.CPU.Has(f) {
			missing = append(missing, f.String())
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("unsupported CPU: missing required instruction set extensions [%s] (AVX2, FMA3 and F16C are required; check that the host CPU or VM exposes them)", strings.Join(missing, " "))
	}
	return nil
}
