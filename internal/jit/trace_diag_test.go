//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestDiag_SubTraceCall_Direct diagnoses the SubTraceCall_Direct failure.
// Original error: sum=0, exitPC=0, sideExit=true (trace does nothing)
func TestDiag_SubTraceCall_Direct(t *testing.T) {
	// Inner trace: sum = sum + 1, FORLOOP j=5..1
	innerTrace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		Constants: []runtime.Value{},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 10, BType: runtime.TypeInt, CType: runtime.TypeInt, PC: 1},
			{Op: vm.OP_FORLOOP, A: 5, SBX: -2, PC: 2},
		},
	}

	regs := make([]runtime.Value, 20)
	regs[0] = runtime.IntValue(0)  // sum
	regs[5] = runtime.IntValue(1)  // idx = 1
	regs[6] = runtime.IntValue(3)  // limit
	regs[7] = runtime.IntValue(1)  // step
	regs[8] = runtime.IntValue(1)  // loop var = 1
	regs[10] = runtime.IntValue(1) // constant 1

	diag := DiagnoseTrace(innerTrace, regs, innerTrace.LoopProto, DiagConfig{
		WatchSlots: []int{0, 5, 6, 7, 8, 10},
	})
	t.Log(diag)

	if diag.GuardFail {
		t.Error("DIAGNOSIS: guard fail — type check failed before loop entry")
	}
	sum := regs[0].Int()
	if sum != 3 {
		t.Errorf("sum = %d, want 3", sum)
	}
}

// TestDiag_CallExit diagnoses the CallExit failure.
// Original error: "unexpected guard fail"
func TestDiag_CallExit(t *testing.T) {
	code := []uint32{
		vm.EncodeABC(vm.OP_ADD, 4, 4, 3),
		vm.EncodeABC(vm.OP_MOVE, 6, 3, 0),
		vm.EncodeABC(vm.OP_CALL, 5, 2, 2),
		vm.EncodeAsBx(vm.OP_FORLOOP, 0, -4),
	}
	proto := &vm.FuncProto{
		Code:      code,
		Constants: []runtime.Value{},
		MaxStack:  10,
	}

	trace := &Trace{
		LoopProto: proto,
		LoopPC:    3,
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, PC: 0, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_MOVE, A: 6, B: 3, PC: 1, BType: runtime.TypeInt},
			{Op: vm.OP_CALL, A: 5, B: 2, C: 2, PC: 2, Intrinsic: IntrinsicNone},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -4, PC: 3, AType: runtime.TypeInt},
		},
	}

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(1) // idx
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(1) // i (loop var)
	regs[4] = runtime.IntValue(0) // sum
	regs[5] = runtime.NilValue()  // fn

	diag := DiagnoseTrace(trace, regs, proto, DiagConfig{
		WatchSlots: []int{0, 1, 2, 3, 4, 5},
		MaxIter:    1,
	})
	t.Log(diag)

	if diag.GuardFail {
		t.Error("DIAGNOSIS: guard fail — a pre-loop type guard failed")
	}
}
