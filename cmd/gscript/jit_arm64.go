//go:build darwin && arm64

package main

import (
	"github.com/gscript/gscript/internal/jit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableJIT(bvm *bytecodevm.VM) {
	engine := jit.NewEngine()
	engine.SetThreshold(1)
	bvm.SetJIT(engine)
	// Set JIT factory so goroutine child VMs also get JIT
	bvm.SetJITFactory(func() bytecodevm.JITEngine {
		e := jit.NewEngine()
		e.SetThreshold(1)
		return e
	})
}
