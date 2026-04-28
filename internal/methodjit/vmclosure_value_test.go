package methodjit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestVMClosureFromValueRecognizesVMClosureValue(t *testing.T) {
	cl := &vm.Closure{Proto: &vm.FuncProto{Name: "jit-fast"}}
	v := runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)

	got, ok := vmClosureFromValue(v)
	if !ok {
		t.Fatal("vmClosureFromValue did not recognize VM closure value")
	}
	if got != cl {
		t.Fatalf("vmClosureFromValue = %p, want %p", got, cl)
	}
}

func TestVMClosureFromValueFallsBackToInterfaceValue(t *testing.T) {
	cl := &vm.Closure{Proto: &vm.FuncProto{Name: "jit-legacy"}}
	v := runtime.FunctionValue(cl)

	got, ok := vmClosureFromValue(v)
	if !ok {
		t.Fatal("vmClosureFromValue did not recognize interface-backed closure value")
	}
	if got != cl {
		t.Fatalf("vmClosureFromValue fallback = %p, want %p", got, cl)
	}
}
