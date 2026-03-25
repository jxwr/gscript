//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TraceRecorder stub — placeholder for the new trace JIT pipeline.
// TODO: Implement full trace recording + SSA compilation.
type TraceRecorder struct{}

func NewTraceRecorder() *TraceRecorder { return &TraceRecorder{} }

func (r *TraceRecorder) OnInstruction(pc int, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base int) bool {
	return false
}

func (r *TraceRecorder) OnLoopBackEdge(pc int, proto *vm.FuncProto) bool {
	return false
}

func (r *TraceRecorder) TryExecuteCompiled(pc int, proto *vm.FuncProto) bool {
	return false
}

func (r *TraceRecorder) IsRecording() bool { return false }

func (r *TraceRecorder) PendingTrace() vm.TraceExecutor { return nil }

func (r *TraceRecorder) ShouldSkipJIT() bool { return false }

func (r *TraceRecorder) SetCompile(v bool) {}

func (r *TraceRecorder) SetDebug(v bool) {}

func (r *TraceRecorder) SetCallHandler(h func(runtime.Value, []runtime.Value) ([]runtime.Value, error)) {
}
