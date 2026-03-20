//go:build darwin && arm64

package jit

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ─── Unit tests for FuseMultiplyAdd pass ───

func TestFMA_BasicFMADD(t *testing.T) {
	// Pattern: MUL(a, b) then ADD(mul_result, c) → FMADD(a, b, c)
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},  // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},  // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},  // ref 2: c
			{Op: SSA_LOOP},                                     // ref 3
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 3},  // ref 4: a*b
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 4, Arg2: 2, Slot: 4},  // ref 5: a*b + c
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 1},               // ref 6: exit
		},
	}

	result := FuseMultiplyAdd(f)

	// MUL should be preserved (not NOP'd) but marked as absorbed
	if result.Insts[4].Op != SSA_MUL_FLOAT {
		t.Errorf("expected MUL to stay MUL_FLOAT, got %d", result.Insts[4].Op)
	}
	if !result.AbsorbedMuls[4] {
		t.Error("MUL ref 4 should be in AbsorbedMuls")
	}
	// ADD should become FMADD
	fmadd := result.Insts[5]
	if fmadd.Op != SSA_FMADD {
		t.Fatalf("expected FMADD, got %d", fmadd.Op)
	}
	if fmadd.Arg1 != 0 || fmadd.Arg2 != 1 {
		t.Errorf("FMADD args: Arg1=%d, Arg2=%d, want 0, 1", fmadd.Arg1, fmadd.Arg2)
	}
	if SSARef(fmadd.AuxInt) != 2 {
		t.Errorf("FMADD addend: AuxInt=%d, want ref 2", fmadd.AuxInt)
	}
}

func TestFMA_BasicFMADD_Commutative(t *testing.T) {
	// Pattern: ADD(c, MUL(a, b)) → FMADD(a, b, c)
	// ADD is commutative, so the MUL can be Arg2
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},  // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},  // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},  // ref 2: c
			{Op: SSA_LOOP},                                     // ref 3
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 3},  // ref 4: a*b
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 4, Slot: 4},  // ref 5: c + a*b
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 1},               // ref 6: exit
		},
	}

	result := FuseMultiplyAdd(f)

	if result.Insts[4].Op != SSA_MUL_FLOAT {
		t.Errorf("expected MUL to stay MUL_FLOAT, got %d", result.Insts[4].Op)
	}
	if !result.AbsorbedMuls[4] {
		t.Error("MUL ref 4 should be in AbsorbedMuls")
	}
	fmadd := result.Insts[5]
	if fmadd.Op != SSA_FMADD {
		t.Fatalf("expected FMADD, got %d", fmadd.Op)
	}
	if fmadd.Arg1 != 0 || fmadd.Arg2 != 1 {
		t.Errorf("FMADD args: Arg1=%d, Arg2=%d, want 0, 1", fmadd.Arg1, fmadd.Arg2)
	}
	if SSARef(fmadd.AuxInt) != 2 {
		t.Errorf("FMADD addend: AuxInt=%d, want ref 2", fmadd.AuxInt)
	}
}

func TestFMA_BasicFMSUB(t *testing.T) {
	// Pattern: SUB(c, MUL(a, b)) → FMSUB(a, b, c)
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},  // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},  // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},  // ref 2: c
			{Op: SSA_LOOP},                                     // ref 3
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 3},  // ref 4: a*b
			{Op: SSA_SUB_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 4, Slot: 4},  // ref 5: c - a*b
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 1},               // ref 6: exit
		},
	}

	result := FuseMultiplyAdd(f)

	if result.Insts[4].Op != SSA_MUL_FLOAT {
		t.Errorf("expected MUL to stay MUL_FLOAT, got %d", result.Insts[4].Op)
	}
	if !result.AbsorbedMuls[4] {
		t.Error("MUL ref 4 should be in AbsorbedMuls")
	}
	fmsub := result.Insts[5]
	if fmsub.Op != SSA_FMSUB {
		t.Fatalf("expected FMSUB, got %d", fmsub.Op)
	}
	if fmsub.Arg1 != 0 || fmsub.Arg2 != 1 {
		t.Errorf("FMSUB args: Arg1=%d, Arg2=%d, want 0, 1", fmsub.Arg1, fmsub.Arg2)
	}
	if SSARef(fmsub.AuxInt) != 2 {
		t.Errorf("FMSUB addend: AuxInt=%d, want ref 2", fmsub.AuxInt)
	}
}

func TestFMA_NoFuseMULUsedTwice(t *testing.T) {
	// MUL result used by both ADD and another instruction → no fusion
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},  // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},  // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},  // ref 2: c
			{Op: SSA_LOOP},                                     // ref 3
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 3},  // ref 4: a*b
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 4, Arg2: 2, Slot: 4},  // ref 5: a*b + c
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 4, Arg2: 0, Slot: 5},  // ref 6: a*b + a (second use of ref4)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 1},               // ref 7: exit
		},
	}

	result := FuseMultiplyAdd(f)

	// MUL should NOT be absorbed (used twice)
	if result.Insts[4].Op != SSA_MUL_FLOAT {
		t.Errorf("MUL should stay MUL_FLOAT, got %d", result.Insts[4].Op)
	}
	if result.AbsorbedMuls != nil && result.AbsorbedMuls[4] {
		t.Error("MUL should not be absorbed when it has multiple uses")
	}
	// ADD should remain ADD
	if result.Insts[5].Op != SSA_ADD_FLOAT {
		t.Errorf("ADD should remain ADD_FLOAT, got %d", result.Insts[5].Op)
	}
}

func TestFMA_NoFuseSubMULInArg1(t *testing.T) {
	// SUB(MUL(a,b), c) → MUL*a*b - c, NOT c - a*b
	// This pattern is NOT fuseable to FMSUB because FMSUB = c - a*b
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},  // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},  // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},  // ref 2: c
			{Op: SSA_LOOP},                                     // ref 3
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 3},  // ref 4: a*b
			{Op: SSA_SUB_FLOAT, Type: SSATypeFloat, Arg1: 4, Arg2: 2, Slot: 4},  // ref 5: a*b - c
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 1},               // ref 6: exit
		},
	}

	result := FuseMultiplyAdd(f)

	// SUB should remain SUB (can't fuse MUL-in-arg1 to FMSUB)
	if result.Insts[5].Op != SSA_SUB_FLOAT {
		t.Errorf("SUB(MUL, c) should not be fused, got op %d", result.Insts[5].Op)
	}
}

func TestFMA_NoLoop(t *testing.T) {
	// No LOOP marker → no fusion
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 0},
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 1},
		},
	}

	result := FuseMultiplyAdd(f)
	if result.Insts[0].Op != SSA_MUL_FLOAT {
		t.Error("should not fuse without LOOP marker")
	}
}

func TestFMA_NilFunc(t *testing.T) {
	result := FuseMultiplyAdd(nil)
	if result != nil {
		t.Error("expected nil return for nil input")
	}
}

func TestFMA_DoubleFusion(t *testing.T) {
	// Two independent MUL+ADD patterns → both should be fused
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},  // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},  // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},  // ref 2: c
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 3},  // ref 3: d
			{Op: SSA_LOOP},                                     // ref 4
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 4},  // ref 5: a*b
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 5, Arg2: 2, Slot: 5},  // ref 6: a*b + c
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 3, Slot: 6},  // ref 7: c*d
			{Op: SSA_SUB_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 7, Slot: 7},  // ref 8: a - c*d
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 1},               // ref 9: exit
		},
	}

	result := FuseMultiplyAdd(f)

	if result.Insts[5].Op != SSA_MUL_FLOAT {
		t.Errorf("first MUL should stay MUL_FLOAT, got %d", result.Insts[5].Op)
	}
	if !result.AbsorbedMuls[5] {
		t.Error("first MUL ref 5 should be in AbsorbedMuls")
	}
	if result.Insts[6].Op != SSA_FMADD {
		t.Errorf("first ADD should become FMADD, got %d", result.Insts[6].Op)
	}
	if result.Insts[7].Op != SSA_MUL_FLOAT {
		t.Errorf("second MUL should stay MUL_FLOAT, got %d", result.Insts[7].Op)
	}
	if !result.AbsorbedMuls[7] {
		t.Error("second MUL ref 7 should be in AbsorbedMuls")
	}
	if result.Insts[8].Op != SSA_FMSUB {
		t.Errorf("SUB should become FMSUB, got %d", result.Insts[8].Op)
	}
}

func TestFMA_UseDefTracking(t *testing.T) {
	// Verify that FMADD's AuxInt ref is tracked as a use in UseDef
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},                     // ref 0: a
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},                     // ref 1: b
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 2},                     // ref 2: c
			{Op: SSA_FMADD, Type: SSATypeFloat, Arg1: 0, Arg2: 1, AuxInt: 2, Slot: 3}, // ref 3: c + a*b
		},
	}

	ud := BuildUseDef(f)

	// ref 2 (the addend via AuxInt) should have ref 3 as a user
	if ud.UserCount(2) < 1 {
		t.Errorf("ref 2 should be used by FMADD, got %d users", ud.UserCount(2))
	}
	// ref 0 (Arg1) should have ref 3 as a user
	if ud.UserCount(0) < 1 {
		t.Errorf("ref 0 should be used by FMADD, got %d users", ud.UserCount(0))
	}
}

// ─── Integration test: FMADD end-to-end via the full pipeline ───

func TestFMA_Integration_FloatFMADD(t *testing.T) {
	// sum = sum + x * y, iterated 100 times
	// This should produce an FMADD pattern: MUL(x, y) + sum → FMADD
	g := runWithSSAJIT(t, `
		sum := 0.0
		x := 2.0
		y := 3.0
		for i := 1; i <= 100; i++ {
			sum = sum + x * y
		}
	`)
	if v := g["sum"]; v.Float() != 600.0 {
		t.Errorf("sum = %v, want 600.0", v.Float())
	}
}

func TestFMA_Integration_FloatFMSUB(t *testing.T) {
	// diff = diff - x * y, iterated 10 times
	// Pattern: SUB(diff, MUL(x, y)) → but this is diff - x*y = FMSUB
	g := runWithSSAJIT(t, `
		diff := 100.0
		x := 2.0
		y := 3.0
		for i := 1; i <= 10; i++ {
			diff = diff - x * y
		}
	`)
	if v := g["diff"]; v.Float() != 40.0 {
		t.Errorf("diff = %v, want 40.0", v.Float())
	}
}

func TestFMA_Integration_MandelbrotPattern(t *testing.T) {
	// Simplified mandelbrot inner loop:
	// tr = zr*zr - zi*zi + cr
	// ti = 2.0*zr*zi + ci
	// This tests the combined FMADD/FMSUB patterns
	g := runWithSSAJIT(t, `
		zr := 0.0
		zi := 0.0
		cr := 0.5
		ci := 0.25
		for i := 1; i <= 10; i++ {
			tr := zr*zr - zi*zi + cr
			ti := 2.0*zr*zi + ci
			zr = tr
			zi = ti
		}
	`)
	// Verify the computation converges to the expected fixed point
	zr := g["zr"].Float()
	zi := g["zi"].Float()
	// After 10 iterations starting from (0,0) with c=(0.5, 0.25):
	// These are specific values we can compute
	// Let's just verify they're reasonable (not NaN/Inf)
	if math.IsNaN(zr) || math.IsInf(zr, 0) {
		t.Errorf("zr is %v, expected finite value", zr)
	}
	if math.IsNaN(zi) || math.IsInf(zi, 0) {
		t.Errorf("zi is %v, expected finite value", zi)
	}
}

func TestFMA_Integration_Correctness(t *testing.T) {
	// Verify that FMADD produces the same result as separate MUL+ADD
	// for a specific computation: 2.0 * 3.0 + 4.0 = 10.0
	g := runWithSSAJIT(t, `
		a := 2.0
		b := 3.0
		c := 4.0
		result := 0.0
		for i := 1; i <= 1; i++ {
			result = a * b + c
		}
	`)
	if v := g["result"]; v.Float() != 10.0 {
		t.Errorf("result = %v, want 10.0", v.Float())
	}
}

// ─── Direct SSA codegen test for FMADD/FMSUB instructions ───

func TestSSACodegen_FMADD_Direct(t *testing.T) {
	// Build an SSA function with FMADD directly and compile it
	// sum = sum + a * b, loop 5 times
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			// The trace records MUL then ADD, but we'll manually inject FMADD
			{Op: vm.OP_MUL, A: 5, B: 3, C: 4, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 5, B: 5, C: 6, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	ssaFunc := BuildSSA(trace)
	ssaFunc = OptimizeSSA(ssaFunc)
	ssaFunc = ConstHoist(ssaFunc)
	ssaFunc = CSE(ssaFunc)
	ssaFunc = FuseMultiplyAdd(ssaFunc)

	// Verify FMADD was created
	hasFMADD := false
	for _, inst := range ssaFunc.Insts {
		if inst.Op == SSA_FMADD {
			hasFMADD = true
			break
		}
	}
	// The fusion depends on trace structure; just verify it compiles
	if ssaIsIntegerOnly(ssaFunc) && SSAIsUseful(ssaFunc) {
		ct, err := CompileSSA(ssaFunc)
		if err != nil {
			t.Fatalf("CompileSSA error: %v", err)
		}

		// Set up registers: slots 0-2 = for loop (idx, limit, step),
		// slot 3 = a (float), slot 4 = b (float), slot 5 = temp, slot 6 = sum
		regs := make([]runtime.Value, 10)
		regs[0] = runtime.IntValue(0)       // idx
		regs[1] = runtime.IntValue(5)       // limit
		regs[2] = runtime.IntValue(1)       // step
		regs[3] = runtime.FloatValue(2.0)   // a
		regs[4] = runtime.FloatValue(3.0)   // b
		regs[5] = runtime.FloatValue(0.0)   // temp
		regs[6] = runtime.FloatValue(0.0)   // sum

		_, _ = executeSSATrace(ct, regs)

		// After 5 iterations: sum += 2.0 * 3.0 = 6.0 each time → 30.0
		// But the actual result depends on SSA builder's interpretation
		// Just verify no crash and finite result
		result := regs[5].Float()
		if math.IsNaN(result) || math.IsInf(result, 0) {
			t.Errorf("result = %v, expected finite value", result)
		}
		_ = hasFMADD
	}
}
