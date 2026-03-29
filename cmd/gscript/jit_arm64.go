//go:build darwin && arm64

package main

import (
	"github.com/gscript/gscript/internal/methodjit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM) {
	// Method JIT: V8-style whole-function compilation
	mjit := methodjit.NewMethodJITEngine()
	bvm.SetMethodJIT(mjit)
}
