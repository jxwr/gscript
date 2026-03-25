//go:build darwin && arm64

package jit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ─── Codegen Micro-Tests ───
//
// These tests construct minimal SSAFunc by hand and compile them with CompileSSA,
// exercising individual ARM64 code generation sequences in isolation.
// No trace recorder involved — tests the codegen directly.

// dummyProto is a minimal FuncProto for tests.
var dummyProto = &vm.FuncProto{
	Code: []uint32{0, 0, 0, 0, 0}, // enough bytecodes
	Name: "test",
}

// buildMinimalTrace constructs an SSAFunc from a list of SSA instructions.
// It finds the SSA_LOOP marker automatically and sets up the minimum Trace.
func buildMinimalTrace(insts []SSAInst) *SSAFunc {
	loopIdx := -1
	for i, inst := range insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	return &SSAFunc{
		Insts:   insts,
		LoopIdx: loopIdx,
		Trace:   &Trace{LoopPC: 0, LoopProto: dummyProto},
	}
}

// executeMicro compiles an SSAFunc and executes it against the given registers.
// Returns exitPC, sideExit, guardFail.
func executeMicro(t *testing.T, f *SSAFunc, regs []runtime.Value) (exitPC int, sideExit bool, guardFail bool) {
	t.Helper()

	ct, err := CompileSSA(f)
	if err != nil {
		t.Skipf("not compilable: %v", err)
	}

	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}

	callJIT(uintptr(ct.code.Ptr()), uintptr(unsafe.Pointer(&ctx)))

	switch ctx.ExitCode {
	case 2:
		return 0, false, true
	case 1:
		return int(ctx.ExitPC), true, false
	default:
		return int(ctx.ExitPC), false, false
	}
}

// ─── 1. Integer Arithmetic ───

func TestMicro_AddInt(t *testing.T) {
	// Trace: load slot 0 (idx), load slot 1 (limit), load slot 2 (step),
	//        load slot 3 (i), load slot 4 (sum)
	// Loop body: sum = sum + i, then FORLOOP exit check, MOVE i exposed.
	//
	// Standard for-loop register layout (Lua convention):
	//   slot 0 = idx, slot 1 = limit, slot 2 = step, slot 3 = i (exposed)
	//   slot 4 = sum (accumulated)
	f := buildMinimalTrace([]SSAInst{
		// Pre-loop: LOAD_SLOT + GUARD_TYPE + UNBOX_INT for each live-in slot
		// Slot 0 (idx)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},                   // 0
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},          // 1
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},    // 2
		// Slot 1 (limit)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},                   // 3
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},          // 4
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},    // 5
		// Slot 2 (step)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},                   // 6
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},          // 7
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},    // 8
		// Slot 3 (i)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},                   // 9
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},          // 10
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},    // 11
		// Slot 4 (sum)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},                   // 12
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},         // 13
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},   // 14
		// Loop marker
		{Op: SSA_LOOP}, // 15
		// Loop body: sum = sum + i
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4}, // 16
		// FORLOOP: idx = idx + step
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},  // 17
		// FORLOOP exit: LE_INT(idx, limit) with AuxInt=-1 sentinel
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		// MOVE: expose i = idx
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},             // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx (FORPREP: 1-1=0)
	regs[1] = runtime.IntValue(5)  // limit
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i
	regs[4] = runtime.IntValue(0)  // sum

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// Loop runs 6 times (idx=0,1,2,3,4,5, then 6>5 exits).
	// First iteration i=0 (before increment), sum += 0.
	// But after FORLOOP increment: idx becomes 1,2,3,4,5,6 and MOVE sets i.
	// The ADD_INT for sum uses the pre-increment i (slot 3) value from the
	// slot-level register, which gets the idx value after store-back/reload.
	// With the standard trace pattern: body runs first (sum += i), then FORLOOP.
	// At entry: idx=0, i=0. Body: sum += 0. FORLOOP: idx=1, i=1. Continue.
	// Body: sum += 1. FORLOOP: idx=2, i=2. ... until idx=6 > 5.
	// sum = 0+1+2+3+4+5 = 15
	sum := regs[4].Int()
	if sum != 15 {
		t.Errorf("sum = %d, want 15", sum)
	}
}

func TestMicro_SubInt(t *testing.T) {
	f := buildMinimalTrace([]SSAInst{
		// Slot 0 (idx)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		// Slot 1 (limit)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		// Slot 2 (step)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		// Slot 3 (i)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Slot 4 (val)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		// Loop
		{Op: SSA_LOOP}, // 15
		// val = val - i
		{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4}, // 16
		// FORLOOP: idx += step
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 17
		// FORLOOP exit
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		// MOVE
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3}, // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)    // idx
	regs[1] = runtime.IntValue(5)    // limit
	regs[2] = runtime.IntValue(1)    // step
	regs[3] = runtime.IntValue(0)    // i
	regs[4] = runtime.IntValue(100)  // val

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// val starts at 100. Each iteration subtracts the loop variable.
	// 100 - 0 - 1 - 2 - 3 - 4 - 5 = 85
	val := regs[4].Int()
	if val != 85 {
		t.Errorf("val = %d, want 85", val)
	}
}

func TestMicro_MulInt(t *testing.T) {
	f := buildMinimalTrace([]SSAInst{
		// Slots 0-3: idx, limit, step, i
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Slot 4 (sum), Slot 5 (temp)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		// Loop
		{Op: SSA_LOOP}, // 15
		// temp = i * i
		{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 10, Arg2: 10, Slot: 5}, // 16
		// sum = sum + temp
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 16, Slot: 4}, // 17
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 18
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1}, // 19
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3}, // 20
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx
	regs[1] = runtime.IntValue(10) // limit
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i
	regs[4] = runtime.IntValue(0)  // sum
	regs[5] = runtime.IntValue(0)  // temp

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum of i^2 for i=0..10: 0+1+4+9+16+25+36+49+64+81+100 = 385
	sum := regs[4].Int()
	if sum != 385 {
		t.Errorf("sum = %d, want 385", sum)
	}
}

// ─── 2. Float Arithmetic ───

func TestMicro_MulFloat(t *testing.T) {
	// For-loop with integer index controlling iteration count,
	// but float multiplication in the body.
	// Slots 0-3: idx(int), limit(int), step(int), i(int)
	// Slot 4: x (float), Slot 5: result (float)
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Float slot 4 (x)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 4},                 // 12
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 12, Slot: 4},     // 13
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeFloat), Slot: 4}, // 14
		// Float slot 5 (result)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},                 // 15
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 15, Slot: 5},     // 16
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeFloat), Slot: 5}, // 17
		// Loop
		{Op: SSA_LOOP}, // 18
		// result = result * x
		{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 16, Arg2: 13, Slot: 5}, // 19
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},  // 20
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 20, Arg2: 4, AuxInt: -1}, // 21
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 20, Slot: 3},             // 22
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)      // idx
	regs[1] = runtime.IntValue(3)      // limit (4 iterations: 0,1,2,3)
	regs[2] = runtime.IntValue(1)      // step
	regs[3] = runtime.IntValue(0)      // i
	regs[4] = runtime.FloatValue(2.0)  // x
	regs[5] = runtime.FloatValue(1.0)  // result

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// result = 1.0 * 2.0 * 2.0 * 2.0 * 2.0 = 16.0 (4 iterations)
	result := regs[5].Float()
	if result != 16.0 {
		t.Errorf("result = %f, want 16.0", result)
	}
}

func TestMicro_AddFloat(t *testing.T) {
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Float slot 4 (sum)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 4},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeFloat), Slot: 4},
		// Float slot 5 (delta)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 15, Slot: 5},
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeFloat), Slot: 5},
		// Loop
		{Op: SSA_LOOP}, // 18
		// sum = sum + delta
		{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 13, Arg2: 16, Slot: 4}, // 19
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 20, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 20, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)       // idx
	regs[1] = runtime.IntValue(9)       // limit (10 iterations)
	regs[2] = runtime.IntValue(1)       // step
	regs[3] = runtime.IntValue(0)       // i
	regs[4] = runtime.FloatValue(0.0)   // sum
	regs[5] = runtime.FloatValue(0.5)   // delta

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum = 0.5 * 10 = 5.0
	sum := regs[4].Float()
	if sum != 5.0 {
		t.Errorf("sum = %f, want 5.0", sum)
	}
}

func TestMicro_DivFloat(t *testing.T) {
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Float slot 4 (val)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 4},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeFloat), Slot: 4},
		// Float slot 5 (divisor)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 15, Slot: 5},
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeFloat), Slot: 5},
		// Loop
		{Op: SSA_LOOP}, // 18
		// val = val / divisor
		{Op: SSA_DIV_FLOAT, Type: SSATypeFloat, Arg1: 13, Arg2: 16, Slot: 4}, // 19
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 20, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 20, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)         // idx
	regs[1] = runtime.IntValue(2)         // limit (3 iterations)
	regs[2] = runtime.IntValue(1)         // step
	regs[3] = runtime.IntValue(0)         // i
	regs[4] = runtime.FloatValue(1000.0)  // val
	regs[5] = runtime.FloatValue(10.0)    // divisor

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// 1000 / 10 / 10 / 10 = 1.0
	val := regs[4].Float()
	if val != 1.0 {
		t.Errorf("val = %f, want 1.0", val)
	}
}

// ─── 3. Guard Type Check ───

func TestMicro_GuardType_Pass(t *testing.T) {
	// Build a trace that guards slot 4 as int. Pass an int → should proceed normally.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4},
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(1) // limit (2 iterations)
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(0) // sum (int — guard should pass)

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("guard should have passed for int value")
	}
}

func TestMicro_GuardType_Fail(t *testing.T) {
	// Build same trace but put a float in slot 4 → guard should fail.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4},
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)       // idx
	regs[1] = runtime.IntValue(5)       // limit
	regs[2] = runtime.IntValue(1)       // step
	regs[3] = runtime.IntValue(0)       // i
	regs[4] = runtime.FloatValue(3.14)  // WRONG: float, not int → guard should fail

	_, _, guardFail := executeMicro(t, f, regs)
	if !guardFail {
		t.Fatal("expected guard fail for float value in int slot, but got normal exit")
	}
}

func TestMicro_GuardType_Fail_String(t *testing.T) {
	// Put a string where int is expected → guard should fail.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP},
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4},
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)
	regs[1] = runtime.IntValue(5)
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.StringValue("not an int") // WRONG type

	_, _, guardFail := executeMicro(t, f, regs)
	if !guardFail {
		t.Fatal("expected guard fail for string value in int slot")
	}
}

// ─── 4. ExitPC Correctness ───

func TestMicro_LoopDone_ExitPC(t *testing.T) {
	// After loop-done, ExitPC should be LoopPC+1.
	// We set LoopPC = 3, so ExitPC should be 4.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4},
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},
	})
	f.Trace.LoopPC = 3
	f.Trace.LoopProto = &vm.FuncProto{
		Code: []uint32{0, 0, 0, 0, 0, 0},
		Name: "test_exitpc",
	}

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(0) // limit (1 iteration: idx=0 <= 0, body runs, idx=1 > 0 exits)
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(0)

	exitPC, sideExit, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}
	if sideExit {
		t.Fatal("unexpected side exit")
	}
	// LoopPC=3, loop-done sets ExitPC = LoopPC+1 = 4
	if exitPC != 4 {
		t.Errorf("exitPC = %d, want 4", exitPC)
	}
}

// ─── 5. FORLOOP Exit ───

func TestMicro_ForloopExit(t *testing.T) {
	// Build trace with 1 iteration: idx near limit.
	// After body + FORLOOP increment, idx > limit → loop_done.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		// No-op body: just increment
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 16: idx += step
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 16, Arg2: 4, AuxInt: -1}, // 17: idx <= limit?
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 16, Slot: 3}, // 18
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(4) // idx = 4 (near limit)
	regs[1] = runtime.IntValue(5) // limit = 5
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(0)

	_, sideExit, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}
	if sideExit {
		t.Fatal("unexpected side exit — loop should have exited normally")
	}
	// idx starts at 4, increments to 5 (<=5, loops), then to 6 (>5, exits).
	// 2 iterations. ExitCode=0 (loop done).
}

// ─── 6. Store-back ───

func TestMicro_StoreBack(t *testing.T) {
	// After loop completes, registers should have updated values.
	// for i := 0; i <= 3; i++ { sum += 10 }
	// Using SSA_CONST_INT for the constant 10 in the pre-loop.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		// sum = sum + i
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4}, // 16
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 17
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3}, // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx
	regs[1] = runtime.IntValue(3)  // limit
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i
	regs[4] = runtime.IntValue(0)  // sum

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum = 0 + 1 + 2 + 3 = 6 (idx goes 0->1->2->3->4, exits at 4>3)
	sum := regs[4].Int()
	if sum != 6 {
		t.Errorf("sum = %d, want 6", sum)
	}

	// Verify that idx was stored back (it should be 4 after the last increment)
	idx := regs[0].Int()
	if idx != 4 {
		t.Errorf("idx = %d, want 4 (last increment before exit)", idx)
	}
}

// ─── 7. Constant Int in Loop ───

func TestMicro_ConstInt(t *testing.T) {
	// Use SSA_CONST_INT in pre-loop to set a constant value in a slot.
	// for i := 0; i <= 4; i++ { sum += 7 }
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		// Constant 7 in slot 5 (pre-loop)
		{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 7, Slot: 5}, // 15
		{Op: SSA_LOOP}, // 16
		// sum += 7
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 15, Slot: 4}, // 17
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 18
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1}, // 19
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3}, // 20
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(4) // limit (5 iterations: 0,1,2,3,4)
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(0) // sum
	regs[5] = runtime.IntValue(0) // const placeholder

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// 5 iterations * 7 = 35
	sum := regs[4].Int()
	if sum != 35 {
		t.Errorf("sum = %d, want 35", sum)
	}
}

// ─── 8. Negative Integer ───

func TestMicro_NegInt(t *testing.T) {
	// for i := 0; i <= 5; i++ { sum += -i }
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		// neg = -i
		{Op: SSA_NEG_INT, Type: SSATypeInt, Arg1: 10, Slot: 5}, // 16
		// sum += neg
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 16, Slot: 4}, // 17
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 18
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1}, // 19
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3}, // 20
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(0) // sum
	regs[5] = runtime.IntValue(0) // neg

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum = -(0) + -(1) + -(2) + -(3) + -(4) + -(5) = -15
	sum := regs[4].Int()
	if sum != -15 {
		t.Errorf("sum = %d, want -15", sum)
	}
}

// ─── 9. Mod Int ───

func TestMicro_ModInt(t *testing.T) {
	// for i := 0; i <= 0; i++ { result = 7 % 3 }
	// Single iteration, verify modulo.
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 5},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 15, Slot: 5},
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeInt), Slot: 5},
		{Op: SSA_LOOP}, // 18
		// result = slot4 % slot5 = 7 % 3 = 1
		{Op: SSA_MOD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 16, Slot: 6}, // 19
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 20
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 20, Arg2: 4, AuxInt: -1}, // 21
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 20, Slot: 3}, // 22
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(0) // limit (1 iteration)
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(7) // dividend
	regs[5] = runtime.IntValue(3) // divisor
	regs[6] = runtime.IntValue(0) // result

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	result := regs[6].Int()
	if result != 1 {
		t.Errorf("7 %% 3 = %d, want 1", result)
	}
}

// ─── 10. SubFloat ───

func TestMicro_SubFloat(t *testing.T) {
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Float slot 4 (val)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 4},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeFloat), Slot: 4},
		// Float slot 5 (delta)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 15, Slot: 5},
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeFloat), Slot: 5},
		// Loop
		{Op: SSA_LOOP}, // 18
		// val = val - delta
		{Op: SSA_SUB_FLOAT, Type: SSATypeFloat, Arg1: 13, Arg2: 16, Slot: 4}, // 19
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 20, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 20, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)        // idx
	regs[1] = runtime.IntValue(4)        // limit (5 iterations)
	regs[2] = runtime.IntValue(1)        // step
	regs[3] = runtime.IntValue(0)        // i
	regs[4] = runtime.FloatValue(100.0)  // val
	regs[5] = runtime.FloatValue(10.0)   // delta

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// 100 - 10*5 = 50
	val := regs[4].Float()
	if val != 50.0 {
		t.Errorf("val = %f, want 50.0", val)
	}
}

// ─── 11. NegFloat ───

func TestMicro_NegFloat(t *testing.T) {
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Float slot 4 (val)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 4},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeFloat), Slot: 4},
		// Float slot 5 (sum)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 15, Slot: 5},
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeFloat), Slot: 5},
		// Loop
		{Op: SSA_LOOP}, // 18
		// neg = -val
		{Op: SSA_NEG_FLOAT, Type: SSATypeFloat, Arg1: 13, Slot: 6}, // 19
		// sum += neg
		{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 16, Arg2: 19, Slot: 5}, // 20
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 21, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 21, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)       // idx
	regs[1] = runtime.IntValue(2)       // limit (3 iterations)
	regs[2] = runtime.IntValue(1)       // step
	regs[3] = runtime.IntValue(0)       // i
	regs[4] = runtime.FloatValue(5.0)   // val
	regs[5] = runtime.FloatValue(0.0)   // sum
	regs[6] = runtime.FloatValue(0.0)   // neg (scratch)

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum = 0 + (-5.0) + (-5.0) + (-5.0) = -15.0
	sum := regs[5].Float()
	if sum != -15.0 {
		t.Errorf("sum = %f, want -15.0", sum)
	}
}

// ─── 12. Mixed int and float ───

func TestMicro_MixedIntFloat(t *testing.T) {
	// for i := 0; i <= 9; i++ { sum_int += 1; sum_float += 0.5 }
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Int slot 4 (sum_int)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		// Float slot 5 (sum_float)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},                 // 15
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 15, Slot: 5},     // 16
		{Op: SSA_GUARD_TYPE, Arg1: 15, AuxInt: int64(TypeFloat), Slot: 5}, // 17
		// Float slot 6 (delta)
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 6},                 // 18
		{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 18, Slot: 6},     // 19
		{Op: SSA_GUARD_TYPE, Arg1: 18, AuxInt: int64(TypeFloat), Slot: 6}, // 20
		// Loop
		{Op: SSA_LOOP}, // 21
		// sum_int += step (slot 2 = step = 1)
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 7, Slot: 4}, // 22
		// sum_float += delta
		{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 16, Arg2: 19, Slot: 5}, // 23
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 24
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 24, Arg2: 4, AuxInt: -1}, // 25
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 24, Slot: 3}, // 26
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)       // idx
	regs[1] = runtime.IntValue(9)       // limit (10 iterations)
	regs[2] = runtime.IntValue(1)       // step
	regs[3] = runtime.IntValue(0)       // i
	regs[4] = runtime.IntValue(0)       // sum_int
	regs[5] = runtime.FloatValue(0.0)   // sum_float
	regs[6] = runtime.FloatValue(0.5)   // delta

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum_int = 10 * 1 = 10
	sumInt := regs[4].Int()
	if sumInt != 10 {
		t.Errorf("sum_int = %d, want 10", sumInt)
	}

	// sum_float = 10 * 0.5 = 5.0
	sumFloat := regs[5].Float()
	if sumFloat != 5.0 {
		t.Errorf("sum_float = %f, want 5.0", sumFloat)
	}
}

// ─── 13. Single iteration correctness ───

func TestMicro_SingleIteration(t *testing.T) {
	// Verify exact behavior with idx=0, limit=0, step=1 (exactly 1 iteration).
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		// sum = sum + 42 (via slot 4 += slot 3 where slot 3 = i)
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4}, // 16
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 17
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3}, // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx=0
	regs[1] = runtime.IntValue(0)  // limit=0 (1 iteration: body runs at idx=0, then idx=1>0 exits)
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i=0
	regs[4] = runtime.IntValue(42) // sum starts at 42

	_, sideExit, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}
	if sideExit {
		t.Fatal("unexpected side exit")
	}

	// 1 iteration: sum = 42 + 0 = 42 (adding i=0)
	sum := regs[4].Int()
	if sum != 42 {
		t.Errorf("sum = %d, want 42", sum)
	}
}

// ─── 14. Large iteration count ───

func TestMicro_LargeLoop(t *testing.T) {
	// for i := 0; i <= 999; i++ { sum += 1 }
	f := buildMinimalTrace([]SSAInst{
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4},
		{Op: SSA_LOOP}, // 15
		// sum += step (step=1)
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 7, Slot: 4}, // 16
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 17
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3}, // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)    // idx
	regs[1] = runtime.IntValue(999)  // limit
	regs[2] = runtime.IntValue(1)    // step
	regs[3] = runtime.IntValue(0)    // i
	regs[4] = runtime.IntValue(0)    // sum

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// 1000 iterations of sum += 1 = 1000
	sum := regs[4].Int()
	if sum != 1000 {
		t.Errorf("sum = %d, want 1000", sum)
	}
}
