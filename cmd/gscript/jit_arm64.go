//go:build darwin && arm64

package main

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/methodjit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM) {
	// Method JIT: V8-style whole-function compilation (new)
	mjit := methodjit.NewMethodJITEngine()
	bvm.SetMethodJIT(mjit)

	// Legacy Method JIT: function-level compilation (old, for goroutine support)
	engine := jit.NewEngine()
	engine.SetThreshold(1)
	bvm.SetJIT(engine)
	bvm.SetJITFactory(func(child *bytecodevm.VM) bytecodevm.JITEngine {
		e := jit.NewEngine()
		e.SetThreshold(1)
		e.SetGlobals(child.Globals())
		e.SetCallHandler(child.CallValue)
		e.SetGlobalsAccessor(child)
		return e
	})

	// Trace JIT: loop-level SSA compilation
	cliEnableTracing(bvm)
}
