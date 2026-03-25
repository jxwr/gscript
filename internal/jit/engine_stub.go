//go:build darwin && arm64

package jit

import (
	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Engine is a no-op JIT engine stub. All compilation is handled by the
// TraceRecorder (trace-level JIT). This stub satisfies vm.JITEngine.
type Engine struct{}

func NewEngine() *Engine                                                             { return &Engine{} }
func (e *Engine) SetThreshold(n int)                                                 {}
func (e *Engine) SetGlobals(g map[string]rt.Value)                                   {}
func (e *Engine) SetCallHandler(h func(rt.Value, []rt.Value) ([]rt.Value, error))    {}
func (e *Engine) SetGlobalsAccessor(a interface{})                                   {}
func (e *Engine) Free()                                                              {}

func (e *Engine) TryExecute(proto *vm.FuncProto, regs []rt.Value, base int, callCount int) ([]rt.Value, int, bool) {
	return nil, 0, false
}
