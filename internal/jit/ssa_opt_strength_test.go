//go:build darwin && arm64

package jit

import "testing"

func TestStrengthReduce_Mul2ToAdd(t *testing.T) {
	// Build SSA: i * 2 in loop body
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOOP},                                                        // 0
			{Op: SSA_CONST_INT, AuxInt: 2, Slot: -1, Type: SSATypeInt},            // 1
			{Op: SSA_MUL_INT, Arg1: SSARef(0), Arg2: SSARef(1), Type: SSATypeInt, Slot: 3}, // 2
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(1), AuxInt: -1},        // 3 (loop exit)
		},
		LoopIdx: 0,
	}

	f = StrengthReduce(f)

	// MUL_INT(x, 2) should become ADD_INT(x, x)
	inst := &f.Insts[2]
	if inst.Op != SSA_ADD_INT {
		t.Errorf("expected ADD_INT, got %s", ssaOpString(inst.Op))
	}
	if inst.Arg1 != inst.Arg2 {
		t.Errorf("expected Arg1==Arg2 (self-add), got Arg1=%d, Arg2=%d", inst.Arg1, inst.Arg2)
	}
}

func TestStrengthReduce_Mul3NotReduced(t *testing.T) {
	// MUL(x, 3) should NOT be reduced (3 is not a power of 2)
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOOP},
			{Op: SSA_CONST_INT, AuxInt: 3, Slot: -1, Type: SSATypeInt},
			{Op: SSA_MUL_INT, Arg1: SSARef(0), Arg2: SSARef(1), Type: SSATypeInt, Slot: 3},
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(1), AuxInt: -1},
		},
		LoopIdx: 0,
	}

	f = StrengthReduce(f)

	inst := &f.Insts[2]
	if inst.Op != SSA_MUL_INT {
		t.Errorf("expected MUL_INT to stay, got %s", ssaOpString(inst.Op))
	}
}

func TestStrengthReduce_Correctness(t *testing.T) {
	// End-to-end: multiply by 2 in a loop
	src := `func f() { s:=0; for i:=1;i<=100;i++ { s=s+i*2 }; return s }; result = f()`
	vmResult := runVMGetInt(t, src, "result")
	jitResult := runJITGetInt(t, src, "result")
	if vmResult != jitResult {
		t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
	}
	expected := int64(0)
	for i := int64(1); i <= 100; i++ {
		expected += i * 2
	}
	if vmResult != expected {
		t.Errorf("VM result %d != expected %d", vmResult, expected)
	}
}
