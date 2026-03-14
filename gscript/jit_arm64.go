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
}
