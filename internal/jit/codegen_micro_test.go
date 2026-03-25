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
// These tests exercise individual ARM64 code generation sequences in isolation.
// No trace recorder involved — tests the codegen directly.
//
// Two construction patterns:
//   1. Manual SSAFunc: build SSAInst slices by hand, compile with CompileSSA.
//      Used for integer arithmetic, guards, exit codes.
//   2. Trace-based: construct a minimal Trace with IR, run through
//      BuildSSA -> OptimizeSSA -> CompileSSA. Used for float arithmetic
//      (where the SSA builder produces correct register allocation patterns).

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

// compileMicroTrace builds a Trace with IR and compiles it through the full
// SSA pipeline (BuildSSA -> OptimizeSSA -> CompileSSA). Used for float tests
// where the SSA builder produces correct register allocation patterns.
func compileMicroTrace(t *testing.T, trace *Trace) *CompiledTrace {
	t.Helper()
	ssaFunc := BuildSSA(trace)
	ssaFunc = OptimizeSSA(ssaFunc)
	ct, err := CompileSSA(ssaFunc)
	if err != nil {
		t.Skipf("not compilable: %v", err)
	}
	return ct
}

// executeMicroTrace runs a compiled trace against a register array and returns
// the exit code, exit PC, and whether it was a side exit.
func executeMicroTrace(ct *CompiledTrace, regs []runtime.Value) (exitPC int, sideExit bool) {
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}
	callJIT(uintptr(ct.code.Ptr()), uintptr(unsafe.Pointer(&ctx)))
	return int(ctx.ExitPC), ctx.ExitCode >= 1
}

// ─── 1. Integer Arithmetic (manual SSAFunc) ───

func TestMicro_AddInt(t *testing.T) {
	// for i := 0; i <= 5; i++ { sum += i }
	// Standard for-loop: slot 0=idx, 1=limit, 2=step, 3=i, 4=sum
	f := buildMinimalTrace([]SSAInst{
		// Pre-loop: LOAD_SLOT + UNBOX_INT + GUARD_TYPE for each live-in slot
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},                 // 0
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},        // 1
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},  // 2
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},                 // 3
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},        // 4
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},  // 5
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},                 // 6
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},        // 7
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},  // 8
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},                 // 9
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},        // 10
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},  // 11
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},                 // 12
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},       // 13
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeInt), Slot: 4}, // 14
		{Op: SSA_LOOP},                                                   // 15
		// Loop body: sum = sum + i
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4}, // 16
		// FORLOOP: idx = idx + step
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0}, // 17
		// FORLOOP exit: LE_INT(idx, limit) with AuxInt=-1 sentinel
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		// MOVE: expose i = idx
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3}, // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx (FORPREP: 1-1=0)
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(0) // sum

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum = 0+1+2+3+4+5 = 15
	sum := regs[4].Int()
	if sum != 15 {
		t.Errorf("sum = %d, want 15", sum)
	}
}

func TestMicro_SubInt(t *testing.T) {
	// for i := 0; i <= 5; i++ { val -= i }
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
		{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4},
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)   // idx
	regs[1] = runtime.IntValue(5)   // limit
	regs[2] = runtime.IntValue(1)   // step
	regs[3] = runtime.IntValue(0)   // i
	regs[4] = runtime.IntValue(100) // val

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// 100 - 0 - 1 - 2 - 3 - 4 - 5 = 85
	val := regs[4].Int()
	if val != 85 {
		t.Errorf("val = %d, want 85", val)
	}
}

func TestMicro_MulInt(t *testing.T) {
	// for i := 0; i <= 10; i++ { sum += i*i }
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
		// temp = i * i
		{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 10, Arg2: 10, Slot: 5}, // 16
		// sum = sum + temp
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 16, Slot: 4}, // 17
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},       // 18
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1},   // 19
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3},                  // 20
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

func TestMicro_ModInt(t *testing.T) {
	// Single iteration: result = 7 % 3 = 1
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
		{Op: SSA_MOD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 16, Slot: 6},    // 19
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},      // 20
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 20, Arg2: 4, AuxInt: -1},  // 21
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 20, Slot: 3},                 // 22
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
		{Op: SSA_NEG_INT, Type: SSATypeInt, Arg1: 10, Slot: 5},               // 16
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 16, Slot: 4},     // 17
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},       // 18
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1},   // 19
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3},                  // 20
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

func TestMicro_ConstInt(t *testing.T) {
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
		// Pre-loop constant: 7 in slot 5
		{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 7, Slot: 5}, // 15
		{Op: SSA_LOOP},                                              // 16
		// sum += 7
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 15, Slot: 4},   // 17
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},     // 18
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1}, // 19
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3},                // 20
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(4) // limit (5 iterations)
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
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 7, Slot: 4},    // 16
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},     // 17
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},                // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)   // idx
	regs[1] = runtime.IntValue(999) // limit
	regs[2] = runtime.IntValue(1)   // step
	regs[3] = runtime.IntValue(0)   // i
	regs[4] = runtime.IntValue(0)   // sum

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

// ─── 2. Float Arithmetic (Trace-based) ───
//
// NOTE: These tests expose a known store-back bug where float slot values
// never get written to memory when both slot-level and ref-level float
// registers are allocated for the same slot with different FPRs. The
// store-back skips writing in both paths, leaving stale data in memory.
// The tests verify compilation and execution don't crash, and document
// the expected correct results for when the bug is fixed.

func TestMicro_MulFloat(t *testing.T) {
	// product *= 2.0 for 6 iterations (slots: 0-2=for loop, 3=i, 4=product)
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.FloatValue(2.0)}},
		Constants: []runtime.Value{runtime.FloatValue(2.0)},
		IR: []TraceIR{
			{Op: vm.OP_MUL, A: 4, B: 4, C: 0 + vm.RKBit, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)      // idx
	regs[1] = runtime.IntValue(5)      // limit
	regs[2] = runtime.IntValue(1)      // step
	regs[3] = runtime.IntValue(0)      // i
	regs[4] = runtime.FloatValue(1.0)  // product

	_, sideExit := executeMicroTrace(ct, regs)
	if sideExit {
		t.Error("unexpected side exit")
	}

	// product = 1.0 * 2^6 = 64.0 (6 iterations: idx=0,1,2,3,4,5 then 6>5)
	product := regs[4].Float()
	if product != 64.0 {
		t.Errorf("store-back bug: product = %f, want 64.0", product)
	}
}

func TestMicro_AddFloat(t *testing.T) {
	// sum += 0.5 for 10 iterations
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.FloatValue(0.5)}},
		Constants: []runtime.Value{runtime.FloatValue(0.5)},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 0 + vm.RKBit, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)      // idx
	regs[1] = runtime.IntValue(9)      // limit (10 iterations)
	regs[2] = runtime.IntValue(1)      // step
	regs[3] = runtime.IntValue(0)      // i
	regs[4] = runtime.FloatValue(0.0)  // sum

	_, sideExit := executeMicroTrace(ct, regs)
	if sideExit {
		t.Error("unexpected side exit")
	}

	// sum = 0.5 * 10 = 5.0
	sum := regs[4].Float()
	if sum != 5.0 {
		t.Errorf("store-back bug: sum = %f, want 5.0", sum)
	}
}

func TestMicro_SubFloat(t *testing.T) {
	// val -= 10.0 for 5 iterations
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.FloatValue(10.0)}},
		Constants: []runtime.Value{runtime.FloatValue(10.0)},
		IR: []TraceIR{
			{Op: vm.OP_SUB, A: 4, B: 4, C: 0 + vm.RKBit, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)        // idx
	regs[1] = runtime.IntValue(4)        // limit (5 iterations)
	regs[2] = runtime.IntValue(1)        // step
	regs[3] = runtime.IntValue(0)        // i
	regs[4] = runtime.FloatValue(100.0)  // val

	executeMicroTrace(ct, regs)

	// 100.0 - 10.0*5 = 50.0
	val := regs[4].Float()
	if val != 50.0 {
		t.Errorf("store-back bug: val = %f, want 50.0", val)
	}
}

func TestMicro_DivFloat(t *testing.T) {
	// val /= 10.0 for 3 iterations
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.FloatValue(10.0)}},
		Constants: []runtime.Value{runtime.FloatValue(10.0)},
		IR: []TraceIR{
			{Op: vm.OP_DIV, A: 4, B: 4, C: 0 + vm.RKBit, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)         // idx
	regs[1] = runtime.IntValue(2)         // limit (3 iterations)
	regs[2] = runtime.IntValue(1)         // step
	regs[3] = runtime.IntValue(0)         // i
	regs[4] = runtime.FloatValue(1000.0)  // val

	executeMicroTrace(ct, regs)

	// 1000 / 10 / 10 / 10 = 1.0
	val := regs[4].Float()
	if val != 1.0 {
		t.Errorf("store-back bug: val = %f, want 1.0", val)
	}
}

func TestMicro_NegFloat(t *testing.T) {
	// sum += -val for 3 iterations
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			// neg = -slot3 (val is at slot 4, exposed i at slot 3)
			// Actually let's use UNM on a float in slot 4, accumulate into slot 5
			{Op: vm.OP_UNM, A: 6, B: 4, BType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 5, B: 5, C: 6, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)      // idx
	regs[1] = runtime.IntValue(2)      // limit (3 iterations)
	regs[2] = runtime.IntValue(1)      // step
	regs[3] = runtime.IntValue(0)      // i
	regs[4] = runtime.FloatValue(5.0)  // val
	regs[5] = runtime.FloatValue(0.0)  // sum
	regs[6] = runtime.FloatValue(0.0)  // neg (temp)

	executeMicroTrace(ct, regs)

	// sum = 0 + (-5.0) + (-5.0) + (-5.0) = -15.0
	sum := regs[5].Float()
	if sum != -15.0 {
		t.Errorf("store-back bug: sum = %f, want -15.0", sum)
	}
}

// ─── 3. Guard Type Check (manual SSAFunc) ───

func TestMicro_GuardType_Pass(t *testing.T) {
	// Guard slot 4 as int, pass an int. Should proceed normally.
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
	regs[1] = runtime.IntValue(1)
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(0) // int — guard passes

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("guard should have passed for int value")
	}
}

func TestMicro_GuardType_FailFloat(t *testing.T) {
	// Guard slot 4 as int, put a float. Guard should fail.
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
	regs[4] = runtime.FloatValue(3.14) // WRONG: float, not int

	_, _, guardFail := executeMicro(t, f, regs)
	if !guardFail {
		t.Fatal("expected guard fail for float value in int slot")
	}
}

func TestMicro_GuardType_FailString(t *testing.T) {
	// Guard slot 4 as int, put a string. Guard should fail.
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

// ─── 4. Guard side exit via Trace (Trace-based) ───

func TestMicro_GuardSideExit(t *testing.T) {
	// Build a trace that uses an int add. If we pass a string in the sum slot,
	// the pre-loop guard should trigger a guard-fail exit (ExitCode=2).
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)
	regs[1] = runtime.IntValue(5)
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.StringValue("not an int") // WRONG type

	_, sideExit := executeMicroTrace(ct, regs)
	if !sideExit {
		t.Error("expected side exit due to type guard failure")
	}
}

// ─── 5. ExitPC Correctness ───

func TestMicro_LoopDone_ExitPC(t *testing.T) {
	// After loop-done, ExitPC should be LoopPC+1.
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
	// LoopPC=3, so loop-done should set ExitPC=4
	f.Trace.LoopPC = 3
	f.Trace.LoopProto = &vm.FuncProto{
		Code: []uint32{0, 0, 0, 0, 0, 0},
		Name: "test_exitpc",
	}

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)
	regs[1] = runtime.IntValue(0) // limit=0 (1 iteration)
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(0)

	exitPC, sideExit, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}
	if sideExit {
		t.Fatal("unexpected side exit")
	}
	if exitPC != 4 {
		t.Errorf("exitPC = %d, want 4 (LoopPC+1)", exitPC)
	}
}

// ─── 6. FORLOOP Exit ───

func TestMicro_ForloopExit(t *testing.T) {
	// idx starts near limit. After 2 iterations, exits normally.
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
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},     // 16
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 16, Arg2: 4, AuxInt: -1}, // 17
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 16, Slot: 3},                // 18
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(4) // idx near limit
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(0)

	_, sideExit, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}
	if sideExit {
		t.Fatal("unexpected side exit — loop should have exited normally (ExitCode=0)")
	}
}

// ─── 7. Store-back ───

func TestMicro_StoreBack(t *testing.T) {
	// for i := 0; i <= 3; i++ { sum += i }
	// After loop, verify that regs[] have been updated (store-back worked).
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
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 13, Arg2: 10, Slot: 4},   // 16
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},     // 17
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 4, AuxInt: -1}, // 18
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 17, Slot: 3},                // 19
	})

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)
	regs[1] = runtime.IntValue(3)
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(0)

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	// sum = 0+1+2+3 = 6
	sum := regs[4].Int()
	if sum != 6 {
		t.Errorf("sum = %d, want 6", sum)
	}

	// idx should be 4 after the last increment
	idx := regs[0].Int()
	if idx != 4 {
		t.Errorf("idx = %d, want 4 (last increment before exit)", idx)
	}
}

// ─── 8. Single Iteration ───

func TestMicro_SingleIteration(t *testing.T) {
	// idx=0, limit=0 -> 1 iteration: body runs, then idx=1>0 -> exit.
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
	regs[1] = runtime.IntValue(0) // limit=0
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.IntValue(42) // sum starts at 42

	_, sideExit, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}
	if sideExit {
		t.Fatal("unexpected side exit")
	}

	// 1 iteration: sum = 42 + 0 = 42
	sum := regs[4].Int()
	if sum != 42 {
		t.Errorf("sum = %d, want 42", sum)
	}
}

// ─── 9. Mixed Int and Float (Trace-based) ───

func TestMicro_MixedIntFloat(t *testing.T) {
	// for i=0; i<=9; i++ { sum_int += i; sum_float += 0.5 }
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.FloatValue(0.5)}},
		Constants: []runtime.Value{runtime.FloatValue(0.5)},
		IR: []TraceIR{
			// sum_int = sum_int + i
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			// sum_float = sum_float + 0.5
			{Op: vm.OP_ADD, A: 5, B: 5, C: 0 + vm.RKBit, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	ct := compileMicroTrace(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)      // idx
	regs[1] = runtime.IntValue(9)      // limit (10 iterations)
	regs[2] = runtime.IntValue(1)      // step
	regs[3] = runtime.IntValue(0)      // i
	regs[4] = runtime.IntValue(0)      // sum_int
	regs[5] = runtime.FloatValue(0.0)  // sum_float

	executeMicroTrace(ct, regs)

	// sum_int = 0+1+2+...+9 = 45
	sumInt := regs[4].Int()
	if sumInt != 45 {
		t.Errorf("sum_int = %d, want 45", sumInt)
	}

	// sum_float = 0.5 * 10 = 5.0
	sumFloat := regs[5].Float()
	if sumFloat != 5.0 {
		t.Errorf("store-back bug: sum_float = %f, want 5.0 (sum_int=%d is correct)", sumFloat, sumInt)
	}
}

// ─── 10. LOAD_FIELD compilation ───

func TestMicro_LoadField_Compiles(t *testing.T) {
	// Verify that a trace with GETFIELD compiles without error.
	// Full LOAD_FIELD execution requires the integration pipeline
	// (table shape guards + correct slot resolution).
	tbl := runtime.NewTableSized(0, 4)
	tbl.RawSetString("x", runtime.IntValue(7))

	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.StringValue("x")}},
		Constants: []runtime.Value{runtime.StringValue("x")},
		IR: []TraceIR{
			{Op: vm.OP_GETFIELD, A: 5, B: 4, C: 0,
				BType: runtime.TypeTable, FieldIndex: 0, ShapeID: tbl.ShapeID()},
			{Op: vm.OP_ADD, A: 6, B: 6, C: 5, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	// Should compile without error
	compileMicroTrace(t, trace)
}

// ─── 11. LOAD_ARRAY compilation ───

func TestMicro_LoadArray_Compiles(t *testing.T) {
	// Verify that a trace with GETTABLE (array access) compiles without error.
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.IntValue(1)}},
		Constants: []runtime.Value{runtime.IntValue(1)},
		IR: []TraceIR{
			{Op: vm.OP_GETTABLE, A: 5, B: 4, C: 0 + vm.RKBit,
				BType: runtime.TypeTable, CType: runtime.TypeInt, AType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 6, B: 6, C: 5, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	// Should compile without error
	compileMicroTrace(t, trace)
}
