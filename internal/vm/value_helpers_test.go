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

func TestNewClosureUsesInlineStorageForOneUpvalue(t *testing.T) {
	proto := &FuncProto{
		Name:     "one",
		Upvalues: []UpvalDesc{{Name: "x", InStack: true}},
	}
	cl := NewClosure(proto)
	if cl.Proto != proto {
		t.Fatalf("Proto=%p, want %p", cl.Proto, proto)
	}
	if len(cl.Upvalues) != 1 {
		t.Fatalf("len(Upvalues)=%d, want 1", len(cl.Upvalues))
	}
	if &cl.Upvalues[0] != &cl.inlineUpvalue[0] {
		t.Fatal("one-upvalue closure should use inline storage")
	}
}

func TestNewClosureAllocatesSliceForMultipleUpvalues(t *testing.T) {
	proto := &FuncProto{
		Name: "two",
		Upvalues: []UpvalDesc{
			{Name: "x", InStack: true},
			{Name: "y", InStack: true},
		},
	}
	cl := NewClosure(proto)
	if len(cl.Upvalues) != 2 {
		t.Fatalf("len(Upvalues)=%d, want 2", len(cl.Upvalues))
	}
	if &cl.Upvalues[0] == &cl.inlineUpvalue[0] {
		t.Fatal("multi-upvalue closure should not use one-slot inline storage")
	}
}
