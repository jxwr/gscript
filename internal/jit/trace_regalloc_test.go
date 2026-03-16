package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestRegAlloc_BasicAllocation(t *testing.T) {
	// A trace with registers 0,1,2,3 used with different frequencies
	trace := &Trace{
		IR: []TraceIR{
			// R0 and R1 used heavily (ADD in loop body)
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1},
			// R3 used once
			{Op: vm.OP_MOVE, A: 3, B: 0, C: 0},
		},
	}

	ra := NewRegAlloc(trace)

	// R0 should be allocated (used 8 times: 4 as A, 4 as B)
	if !ra.IsAllocated(0) {
		t.Error("R0 should be allocated (high frequency)")
	}

	// R1 should be allocated (used 4 times as C)
	if !ra.IsAllocated(1) {
		t.Error("R1 should be allocated (high frequency)")
	}

	// R3 used only once — should NOT be allocated
	if ra.IsAllocated(3) {
		t.Error("R3 should NOT be allocated (low frequency)")
	}
}

func TestRegAlloc_MaxRegisters(t *testing.T) {
	// Create a trace using 8 different registers, all heavily
	ir := make([]TraceIR, 0)
	for i := 0; i < 20; i++ {
		ir = append(ir, TraceIR{Op: vm.OP_ADD, A: i % 8, B: (i + 1) % 8, C: (i + 2) % 8})
	}
	trace := &Trace{IR: ir}

	ra := NewRegAlloc(trace)

	// Should allocate at most 5 registers
	if ra.Count() > maxAllocRegs {
		t.Errorf("allocated %d registers, max is %d", ra.Count(), maxAllocRegs)
	}
}

func TestRegAlloc_TracedLoop(t *testing.T) {
	// Test that register allocation works with an actual compiled trace
	g := runWithTracingJIT(t, `
		sum := 0
		x := 10
		for i := 1; i <= 1000; i++ {
			sum = sum + x + i
		}
		result := sum
	`)
	// sum = 10*1000 + (1+2+...+1000) = 10000 + 500500 = 510500
	if v := g["result"]; v.Int() != 510500 {
		t.Errorf("result = %d, want 510500", v.Int())
	}
}

// Ensure unused import
var _ = runtime.TypeInt
