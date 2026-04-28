package vm

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

func TestClosureFromValueRecognizesVMClosureValue(t *testing.T) {
	cl := &Closure{Proto: &FuncProto{Name: "fast"}}
	v := runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)

	got, ok := closureFromValue(v)
	if !ok {
		t.Fatal("closureFromValue did not recognize VM closure value")
	}
	if got != cl {
		t.Fatalf("closureFromValue = %p, want %p", got, cl)
	}
}

func TestClosureFromValueFallsBackToInterfaceValue(t *testing.T) {
	cl := &Closure{Proto: &FuncProto{Name: "legacy"}}
	v := runtime.FunctionValue(cl)

	got, ok := closureFromValue(v)
	if !ok {
		t.Fatal("closureFromValue did not recognize interface-backed closure value")
	}
	if got != cl {
		t.Fatalf("closureFromValue fallback = %p, want %p", got, cl)
	}
}

func TestClosureFromValueRejectsGoFunction(t *testing.T) {
	if got, ok := closureFromValue(runtime.FunctionValue(&runtime.GoFunction{Name: "go"})); ok {
		t.Fatalf("closureFromValue accepted GoFunction: %p", got)
	}
}
