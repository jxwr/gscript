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
}
