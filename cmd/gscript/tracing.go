package main

import (
	"github.com/gscript/gscript/internal/jit"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func cliEnableTracing(bvm *bytecodevm.VM) {
	recorder := jit.NewTraceRecorder()
	recorder.SetCompile(true)
	recorder.SetCallHandler(bvm.CallValue)
	bvm.SetTraceRecorder(recorder)
}
