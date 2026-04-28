package runtime

import (
	goruntime "runtime"
	"testing"
	"unsafe"
)

type vmClosurePointerProbe struct {
	marker int
	next   *vmClosurePointerProbe
}

func newVMClosureProbeValue() (Value, uintptr) {
	obj := &vmClosurePointerProbe{
		marker: 42,
		next:   &vmClosurePointerProbe{marker: 7},
	}
	return VMClosureFunctionValue(unsafe.Pointer(obj), obj), uintptr(unsafe.Pointer(obj))
}

func TestVMClosurePointerKeepsOriginalInterfaceAlive(t *testing.T) {
	v, wantAddr := newVMClosureProbeValue()

	for i := 0; i < 3; i++ {
		goruntime.GC()
	}

	p := v.VMClosurePointer()
	if p == nil {
		t.Fatal("VMClosurePointer returned nil for a VM closure value")
	}
	if gotAddr := uintptr(p); gotAddr != wantAddr {
		t.Fatalf("VMClosurePointer = %#x, want %#x", gotAddr, wantAddr)
	}
	got := (*vmClosurePointerProbe)(p)
	if got.marker != 42 || got.next == nil || got.next.marker != 7 {
		t.Fatalf("VMClosurePointer object = %#v", got)
	}

	ptr, ok := v.Ptr().(*vmClosurePointerProbe)
	if !ok || uintptr(unsafe.Pointer(ptr)) != wantAddr {
		t.Fatalf("Ptr() = %T %p, want *vmClosurePointerProbe %#x", v.Ptr(), ptr, wantAddr)
	}
}

func TestVMClosurePointerRejectsOtherValues(t *testing.T) {
	if p := FunctionValue(&GoFunction{Name: "go"}).VMClosurePointer(); p != nil {
		t.Fatalf("GoFunction VMClosurePointer = %p, want nil", p)
	}
	if p := TableValue(NewTable()).VMClosurePointer(); p != nil {
		t.Fatalf("table VMClosurePointer = %p, want nil", p)
	}
	if p := NilValue().VMClosurePointer(); p != nil {
		t.Fatalf("nil VMClosurePointer = %p, want nil", p)
	}
}
