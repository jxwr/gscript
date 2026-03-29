//go:build darwin && arm64

package gscript

import (
	"github.com/gscript/gscript/internal/methodjit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func enableJIT(bvm *bytecodevm.VM) {
	// Method JIT: V8-style whole-function compilation.
	// Functions called 100+ times are compiled to native ARM64 code.
	mjit := methodjit.NewMethodJITEngine()
	bvm.SetMethodJIT(mjit)
}
