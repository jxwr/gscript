//go:build darwin && arm64

// tier1_int_spec_deopt_test.go — correctness tests for int-spec deopt
// side-effect replay prevention. The int-spec overflow path must resume
// the interpreter at the guard PC, not restart the function at pc=0.

package methodjit

import (
	"testing"
)

// TestTier1IntSpec_OverflowDeoptNoSideEffectReplay verifies that when an
// int-spec arithmetic overflow fires mid-function, earlier side effects are
// NOT replayed. Before the fix, Execute restarted from pc=0, causing exit-
// resume ops (e.g., SETGLOBAL) to fire twice. After the fix, the interpreter
// resumes at the overflow PC and the side effects run exactly once.
//
// Test structure:
//   counter = counter + 1   ← GETGLOBAL/SETGLOBAL exit-resume (side effect)
//   return a + b            ← int-spec ADD; overflows for large=10^14
//
// VM produces counter=1. JIT without fix: counter=2 (replay). JIT with fix: counter=1.
func TestTier1IntSpec_OverflowDeoptNoSideEffectReplay(t *testing.T) {
	compareVMvsJIT(t, `
counter = 0
func f(a, b) {
    counter = counter + 1
    return a + b
}
large = 100000000000000
r = f(large, large)
`, "counter")
}

// TestTier1IntSpec_OverflowDeoptReturnValue verifies that the return value
// from a function that deopts mid-body is correct. The overflowing ADD is
// handled by the interpreter generically (as float arithmetic).
func TestTier1IntSpec_OverflowDeoptReturnValue(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a + b }
large = 100000000000000
r = f(large, large)
`, "r")
}

// TestTier1IntSpec_OverflowDeoptMultipleSideEffects verifies replay prevention
// when there are multiple side effects before the overflow.
func TestTier1IntSpec_OverflowDeoptMultipleSideEffects(t *testing.T) {
	compareVMvsJIT(t, `
c1 = 0
c2 = 0
func f(a, b) {
    c1 = c1 + 1
    c2 = c2 + 1
    return a + b
}
large = 100000000000000
r = f(large, large)
`, "c1")
}
