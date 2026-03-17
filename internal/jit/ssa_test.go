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

func TestSSA_EndToEnd(t *testing.T) {
	// Full pipeline: trace → SSA → optimize → compile → execute
	// Verify the result matches the interpreter
	g := runWithTracingJIT(t, `
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
