//go:build darwin && arm64

package main

import (
	"github.com/gscript/gscript/internal/jit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM) {
	// Method JIT: function-level compilation
	engine := jit.NewEngine()
	engine.SetThreshold(1)
	engine.SetGlobals(bvm.Globals())
	engine.SetCallHandler(bvm.CallValue)
	engine.SetGlobalsAccessor(bvm)
	bvm.SetJIT(engine)
	// Set JIT factory so goroutine child VMs also get JIT
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
