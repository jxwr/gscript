//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestDebug_SimpleAdd demonstrates using debug tools to diagnose bugs.
// This test FAILS because FORLOOP lacks AType - that's intentional for demonstration.
func TestDebug_SimpleAdd(t *testing.T) {
	t.Skip("intentional bug demonstration, see TestDebug_SimpleAdd_Fixed")
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2}, // NOTE: missing AType!
		},
	}

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(0) // sum

	// Run debug session
	ct, _, _ := DebugSession(t, trace, regs, TraceConfig{
		DebugLabel:     "SimpleAdd (BUGGY - no AType)",
		DumpRegsBefore: true,
		DumpRegsAfter:  true,
		WatchSlots:     []int{0, 3, 4}, // watch idx, i, sum
	})

	sum := regs[4].Int()
	t.Logf("RESULT: sum = %d, want 15", sum)

	// This will show the bug:
	// - SSA IR shows NO UNBOX_INT for slots 0,1,2
	// - RegAlloc shows GPR allocated but never loaded
	// - Result is 0 because register was never loaded

	if sum != 15 {
		t.Errorf("BUG CONFIRMED: sum = %d, want 15", sum)
	}

	_ = ct
}

// TestDebug_SimpleAdd_Fixed shows the same test with AType set correctly.
func TestDebug_SimpleAdd_Fixed(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2, AType: runtime.TypeInt}, // FIXED: AType set
		},
	}

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i
	regs[4] = runtime.IntValue(0) // sum

	ct, _, sideExit := DebugSession(t, trace, regs, TraceConfig{
		DebugLabel:     "SimpleAdd (FIXED - with AType)",
		DumpRegsBefore: true,
		DumpRegsAfter:  true,
		WatchSlots:     []int{0, 3, 4},
	})

	sum := regs[4].Int()
	t.Logf("RESULT: sum = %d, want 15", sum)

	if sideExit {
		t.Error("unexpected side exit")
	}

	if sum != 15 {
		t.Errorf("still broken: sum = %d, want 15", sum)
	}

	_ = ct
}

// TestDebug_IterationTracing tests the iteration-level tracing feature.
// It verifies that MaxIterations correctly stops execution after N iterations.
func TestDebug_IterationTracing(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2, AType: runtime.TypeInt},
		},
	}

	// Build and compile
	ssaFunc := BuildSSA(trace)
	ssaFunc = OptimizeSSA(ssaFunc)
	ct, err := CompileSSA(ssaFunc)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Test 1: Stop after 3 iterations
	// NOTE: The iteration counter is incremented at loop_top BEFORE the body executes.
	// So "3 iterations" means:
	//   - Iter 1: counter=1, body runs (idx=1, sum=0+1=1)
	//   - Iter 2: counter=2, body runs (idx=2, sum=1+2=3)
	//   - Iter 3: counter=3, CHECK: counter >= MaxIterations(3) -> exit BEFORE body
	// Result: sum = 3, idx = 2 (not 3, because iter 3 exited before body)
	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx
	regs[1] = runtime.IntValue(10) // limit (but we'll stop early)
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i
	regs[4] = runtime.IntValue(0)  // sum

	exitCode, iterCount := TraceExecutionIter(ct, regs, TraceConfig{
		DebugLabel:     "IterationTracing (3 iters)",
		DumpRegsBefore: true,
		DumpRegsAfter:  true,
		WatchSlots:     []int{0, 3, 4},
		MaxIterations:  3,
	})

	sum := regs[4].Int()
	t.Logf("After 3 iterations: sum = %d, idx = %d", sum, regs[0].Int())

	if exitCode != 5 {
		t.Errorf("expected ExitCode=5 (max iterations), got %d", exitCode)
	}
	if iterCount != 3 {
		t.Errorf("expected 3 iterations, got %d", iterCount)
	}
	// 2 body executions: sum = 0 + 1 = 1 (i goes 0->1 in first iter, then sum+=1 in second)
	if sum != 1 {
		t.Errorf("expected sum=1 after 3 iterations (2 body executions), got %d", sum)
	}

	// Test 2: Run to completion (MaxIterations = 0 = unlimited)
	// The loop runs 5 times (idx 1-5), then exits.
	// Counter ends at 6 because it's incremented at loop_top before the exit check.
	regs2 := make([]runtime.Value, 10)
	regs2[0] = runtime.IntValue(0) // idx
	regs2[1] = runtime.IntValue(5) // limit
	regs2[2] = runtime.IntValue(1) // step
	regs2[3] = runtime.IntValue(0) // i
	regs2[4] = runtime.IntValue(0) // sum

	exitCode2, iterCount2 := TraceExecutionIter(ct, regs2, TraceConfig{
		DebugLabel:     "IterationTracing (unlimited)",
		DumpRegsBefore: false,
		DumpRegsAfter:  true,
		WatchSlots:     []int{0, 3, 4},
		MaxIterations:  0, // unlimited
	})

	sum2 := regs2[4].Int()
	t.Logf("Full run: sum = %d, iterations = %d", sum2, iterCount2)

	if exitCode2 != 0 {
		t.Errorf("expected ExitCode=0 (loop done), got %d", exitCode2)
	}
	// Counter is 6 because: 5 body executions + 1 increment at loop_top before exit
	if iterCount2 != 6 {
		t.Errorf("expected 6 iterations (5 body + 1 exit increment), got %d", iterCount2)
	}
	if sum2 != 15 {
		t.Errorf("expected sum=15, got %d", sum2)
	}
}
