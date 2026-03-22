package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestSSA_BuildSimpleLoop(t *testing.T) {
	// Build SSA from a trace of: for i := 1; i <= 100; i++ { sum = sum + i }
	// The trace should produce:
	//   GUARD_TYPE sum, Int
	//   GUARD_TYPE i, Int
	//   PHI sum_phi (entry sum, loop sum_next)
	//   PHI i_phi (entry i, loop i_next)
	//   ADD_INT sum_next, sum_phi, i_phi
	//   ADD_INT i_next, i_phi, step
	//   CMP_LE i_next, limit → loop or exit

	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ssa := BuildSSA(trace)
	if ssa == nil {
		t.Fatal("BuildSSA returned nil")
	}
	if len(ssa.Insts) == 0 {
		t.Fatal("SSA has no instructions")
	}

	// Check that we have at least some typed instructions
	hasAddInt := false
	for _, inst := range ssa.Insts {
		if inst.Op == SSA_ADD_INT {
			hasAddInt = true
			if inst.Type != SSATypeInt {
				t.Errorf("ADD_INT type = %d, want SSATypeInt", inst.Type)
			}
		}
	}
	if !hasAddInt {
		t.Error("SSA missing ADD_INT instruction")
	}
}

func TestSSA_TypePropagation(t *testing.T) {
	// Verify type inference: ADD of two known-int operands produces int
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 2, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 3, SBX: -3},
		},
	}

	ssa := BuildSSA(trace)
	if ssa == nil {
		t.Fatal("BuildSSA returned nil")
	}

	// All ADD operations should be typed as Int
	for _, inst := range ssa.Insts {
		if inst.Op == SSA_ADD_INT && inst.Type != SSATypeInt {
			t.Errorf("ADD_INT has wrong type: %d", inst.Type)
		}
	}
}

func TestSSA_GuardHoisting(t *testing.T) {
	// After optimization, type guards should appear BEFORE the LOOP marker
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ssa := BuildSSA(trace)
	ssa = OptimizeSSA(ssa)

	// Guards should be before LOOP, body ops after LOOP
	loopIdx := -1
	for i, inst := range ssa.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}

	if loopIdx < 0 {
		t.Fatal("no LOOP marker in SSA")
	}

	// All GUARD_TYPE should be before loopIdx
	for i, inst := range ssa.Insts {
		if inst.Op == SSA_GUARD_TYPE && i > loopIdx {
			t.Errorf("GUARD_TYPE at position %d is after LOOP at %d", i, loopIdx)
		}
	}
}

func TestSSA_FloatArith(t *testing.T) {
	// Float operations should produce SSA_ADD_FLOAT etc.
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -2},
		},
	}
	ssa := BuildSSA(trace)
	if ssa == nil {
		t.Fatal("BuildSSA returned nil")
	}

	hasFloatAdd := false
	for _, inst := range ssa.Insts {
		if inst.Op == SSA_ADD_FLOAT {
			hasFloatAdd = true
			if inst.Type != SSATypeFloat {
				t.Errorf("ADD_FLOAT type = %d, want SSATypeFloat", inst.Type)
			}
		}
	}
	if !hasFloatAdd {
		t.Error("SSA missing ADD_FLOAT for float operands")
	}
}

func TestSSA_EndToEnd(t *testing.T) {
	// Full pipeline: trace → SSA → optimize → compile → execute
	// Verify the result matches the interpreter
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 1000; i++ {
			sum = sum + i
		}
		result := sum
	`)
	if v := g["result"]; v.Int() != 500500 {
		t.Errorf("result = %d, want 500500", v.Int())
	}
}

func TestComputeLiveIn_PureNumeric_DeadIntSlot(t *testing.T) {
	// Purely numeric trace where LOADINT kills a slot before it's read.
	// The slot should NOT be live-in and should NOT get a guard.
	// Trace: LOADINT A=5; ADD A=4 B=5 C=3; FORLOOP A=0
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_LOADINT, A: 5, SBX: 42, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 4, B: 5, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	liveIn, _, _ := computeLiveIn(trace)

	// Slot 5 is written by LOADINT before ADD reads it → NOT live-in
	if liveIn[5] {
		t.Error("slot 5 should NOT be live-in (LOADINT writes before ADD reads)")
	}

	// Slot 3 is read by ADD and never written → live-in
	if !liveIn[3] {
		t.Error("slot 3 should be live-in (read by ADD, never written)")
	}

	// FORLOOP control slots 0,1,2 → live-in
	if !liveIn[0] || !liveIn[1] || !liveIn[2] {
		t.Error("FORLOOP slots 0,1,2 should be live-in")
	}
}

func TestComputeLiveIn_PureNumeric_FloatSlotAlwaysLive(t *testing.T) {
	// Float slots are always live-in for D register initialization.
	// Even if LOADK writes a float before it's read, it should be live-in.
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -2},
		},
	}

	liveIn, slotType, _ := computeLiveIn(trace)

	// Slot 0 is a float operand, should be live-in
	if !liveIn[0] {
		t.Error("float slot 0 should be live-in (float slots always live for D register init)")
	}
	if slotType[0] != runtime.TypeFloat {
		t.Errorf("slot 0 type = %d, want TypeFloat(%d)", slotType[0], runtime.TypeFloat)
	}
}

func TestComputeLiveIn_NonNumeric_UsesLegacy(t *testing.T) {
	// Trace with GETFIELD falls back to legacy path.
	// This should match the old code's behavior exactly.
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_GETFIELD, A: 4, B: 5, BType: runtime.TypeTable, FieldIndex: 0, ShapeID: 1},
			{Op: vm.OP_ADD, A: 3, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	liveIn, slotType, _ := computeLiveIn(trace)

	// Slot 5 is the table read by GETFIELD → should be live-in as TypeTable
	if !liveIn[5] {
		t.Error("slot 5 (table) should be live-in")
	}
	if slotType[5] != runtime.TypeTable {
		t.Errorf("slot 5 type = %d, want TypeTable(%d)", slotType[5], runtime.TypeTable)
	}

	// Slot 3 is read by ADD before any write → live-in
	if !liveIn[3] {
		t.Error("slot 3 should be live-in (read by ADD)")
	}
}
