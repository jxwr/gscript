package vm

import (
	"strings"
	"testing"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
)

func TestCoroutineResumeBoundaryFromSlotsExtractsTargetAndArgs(t *testing.T) {
	co := NewVMCoroutine(nil)
	vm := &VM{regs: rt.MakeNilSlice(4)}
	vm.regs[1] = rt.VMCoroutineValue(unsafe.Pointer(co), co)
	vm.regs[2] = rt.IntValue(10)
	vm.regs[3] = rt.IntValue(20)

	gotCo, args, err := vm.coroutineResumeBoundaryFromSlots(0, 3)
	if err != nil {
		t.Fatalf("coroutineResumeBoundaryFromSlots returned error: %v", err)
	}
	if gotCo != co {
		t.Fatal("coroutineResumeBoundaryFromSlots returned the wrong coroutine")
	}
	if len(args) != 2 || args[0].Int() != 10 || args[1].Int() != 20 {
		t.Fatalf("args = %v, want [10 20]", args)
	}
}

func TestCoroutineResumeBoundaryFromSlotsRejectsInvalidBoundary(t *testing.T) {
	vm := &VM{regs: rt.MakeNilSlice(1)}
	_, _, err := vm.coroutineResumeBoundaryFromSlots(0, 1)
	if err == nil || !strings.Contains(err.Error(), "expects a coroutine") {
		t.Fatalf("err = %v, want coroutine boundary error", err)
	}
}
