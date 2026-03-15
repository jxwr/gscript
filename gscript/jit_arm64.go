//go:build darwin && arm64

package gscript

import (
	"github.com/gscript/gscript/internal/jit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func enableJIT(bvm *bytecodevm.VM) {
	engine := jit.NewEngine()
	engine.SetThreshold(1) // compile on first call for maximum benefit
	engine.SetGlobals(bvm.Globals())
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
}
