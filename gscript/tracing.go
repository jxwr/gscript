package gscript

import (
	"github.com/gscript/gscript/internal/jit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

// enableTracing sets up the tracing JIT for hot loop compilation.
func enableTracing(bvm *bytecodevm.VM) {
	recorder := jit.NewTraceRecorder()
	recorder.SetCompile(true)
	recorder.SetUseSSA(true) // SSA codegen handles int, float, table ops, intrinsics
	bvm.SetTraceRecorder(recorder)
}
