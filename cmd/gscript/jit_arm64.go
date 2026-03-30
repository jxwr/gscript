//go:build darwin && arm64

package main

import (
	"github.com/gscript/gscript/internal/methodjit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM) {
	// Tier 1 Baseline JIT: compiles every function on first call.
	// Tier 2 (MethodJITEngine) is disabled until recursive call handling is fixed.
	bjit := methodjit.NewBaselineJITEngine()
	bvm.SetMethodJIT(bjit)
}
