package jit

import (
	"fmt"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestSieveDiagnosis_WhileLoopTraced verifies that while-loops (OP_JMP backward
// branch) ARE traced by the JIT. Back-edge detection for backward OP_JMP is
// re-enabled so that while-loop style loops (like sieve's inner marking loop)
// can be compiled by the trace JIT.
func TestSieveDiagnosis_WhileLoopTraced(t *testing.T) {
	// This is the sieve's hot inner loop pattern:
	//   j := start
	//   for j <= n { arr[j] = false; j = j + step }
	// It compiles to a while-loop (OP_JMP), NOT FORLOOP.
	// With JMP back-edge detection re-enabled, both loops should be traced.
	src := `
		arr := {}
		for i := 1; i <= 100; i++ { arr[i] = true }
		j := 4
		for j <= 100 {
			arr[j] = false
			j = j + 2
		}
	`

	traces, _ := runWithTracing(t, src)

	// Both the FORLOOP init loop and the while-loop should be traced.
	forLoopTraced := false
	whileLoopTraced := false
	for _, tr := range traces {
		hasSettable := false
		hasForloop := false
		hasJmpBack := false
		for _, ir := range tr.IR {
			if ir.Op == vm.OP_SETTABLE {
				hasSettable = true
			}
			if ir.Op == vm.OP_FORLOOP {
				hasForloop = true
			}
			if ir.Op == vm.OP_JMP && ir.SBX < 0 {
				hasJmpBack = true
			}
		}
		if hasSettable && !hasForloop && hasJmpBack {
			whileLoopTraced = true
		}
		if hasForloop {
			forLoopTraced = true
		}
	}

	if !whileLoopTraced {
		t.Error("While-loop SHOULD be traced (JMP back-edge detection re-enabled)")
	}

	if !forLoopTraced {
		t.Error("Expected the FORLOOP init loop to be traced")
	}

	t.Logf("While-loop tracing enabled: %d traces recorded", len(traces))
	for i, tr := range traces {
		ops := make([]string, 0, len(tr.IR))
		for _, ir := range tr.IR {
			ops = append(ops, fmt.Sprintf("op%d", ir.Op))
		}
		t.Logf("  Trace %d: %v", i, ops)
	}
}

// TestSieveDiagnosis_ForLoopEquivalentIsTraced shows that if the sieve inner
// loop were rewritten as a FORLOOP (for j := start; j <= n; j += step),
// it WOULD be traced. This is the fix: either detect while-loop back-edges
// in the VM, or rewrite the sieve to use numeric for-loops.
func TestSieveDiagnosis_ForLoopEquivalentIsTraced(t *testing.T) {
	// Same logic as the sieve inner loop, but using a numeric for-loop
	src := `
		arr := {}
		for i := 1; i <= 100; i++ { arr[i] = true }
		for j := 4; j <= 100; j += 2 {
			arr[j] = false
		}
	`

	traces, _ := runWithTracing(t, src)

	// Should have traces for BOTH for-loops
	settableInForloop := false
	for _, tr := range traces {
		hasSettable := false
		hasForloop := false
		for _, ir := range tr.IR {
			if ir.Op == vm.OP_SETTABLE {
				hasSettable = true
			}
			if ir.Op == vm.OP_FORLOOP {
				hasForloop = true
			}
		}
		if hasSettable && hasForloop {
			settableInForloop = true
		}
	}

	if !settableInForloop {
		t.Error("Expected SETTABLE inside a FORLOOP trace when using numeric for-loop syntax")
	}

	t.Logf("Confirmed: numeric for-loop equivalent IS traced (%d traces)", len(traces))
}

// TestSieveDiagnosis_CountLoopSideExits shows the second problem: even for
// the count loop that IS traced, the JIT produces a trace that side-exits
// on every composite number (where is_prime[i] is false). Since most numbers
// are composite, the trace side-exits ~80% of the time, adding overhead.
func TestSieveDiagnosis_CountLoopSideExits(t *testing.T) {
	// Simplified version of the sieve count loop
	src := `
		arr := {}
		for i := 1; i <= 50; i++ { arr[i] = true }
		// Mark some as false (like sieve would)
		for i := 4; i <= 50; i += 2 { arr[i] = false }

		count := 0
		for i := 2; i <= 50; i++ {
			if arr[i] { count = count + 1 }
		}
	`

	traces, globals := runWithTracing(t, src)

	// The count should be correct regardless
	if v, ok := globals["count"]; !ok || v.Int() == 0 {
		t.Errorf("count = %v, want non-zero", globals["count"])
	}

	// Find the count loop trace (has GETTABLE + TEST + ADD + FORLOOP)
	for i, tr := range traces {
		hasGettable := false
		hasTest := false
		for _, ir := range tr.IR {
			if ir.Op == vm.OP_GETTABLE {
				hasGettable = true
			}
			if ir.Op == vm.OP_TEST {
				hasTest = true
			}
		}
		if hasGettable && hasTest {
			t.Logf("Count loop trace %d: GETTABLE + TEST present", i)
			t.Log("This trace will side-exit on every arr[i]=false (composite number)")
			t.Log("With ~80%% composites in sieve, the trace adds overhead instead of speed")
		}
	}
}
