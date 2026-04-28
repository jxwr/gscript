//go:build darwin && arm64

package methodjit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestBuildInlineGlobalsRecognizesVMClosureValue(t *testing.T) {
	calleeProto := &vm.FuncProto{Name: "callee"}
	callee := &vm.Closure{Proto: calleeProto}

	callVM := vm.New(map[string]runtime.Value{
		"callee": runtime.VMClosureFunctionValue(unsafe.Pointer(callee), callee),
		"go":     runtime.FunctionValue(&runtime.GoFunction{Name: "go"}),
	})
	defer callVM.Close()

	tm := &TieringManager{callVM: callVM}
	globals := tm.buildInlineGlobals()

	if got := globals["callee"]; got != calleeProto {
		t.Fatalf("globals[callee] = %p, want %p", got, calleeProto)
	}
	if got := globals["go"]; got != nil {
		t.Fatalf("globals[go] = %p, want nil", got)
	}
}
