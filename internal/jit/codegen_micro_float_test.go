//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2, AType: runtime.TypeInt},
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
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2, AType: runtime.TypeInt},
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
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2, AType: runtime.TypeInt},
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
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2, AType: runtime.TypeInt},
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
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3, AType: runtime.TypeInt},
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
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3, AType: runtime.TypeInt},
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
