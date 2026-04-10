//go:build darwin && arm64

// tier1_int_spec_test.go — integration tests for the Tier 1 int-specialized
// ADD/SUB/MUL/EQ/LT/LE templates. Uses the full VM→JIT pipeline via
// compareVMvsJIT so correctness is validated end-to-end.

package methodjit

import (
	"testing"
)

// TestTier1IntSpec_Ackermann: ackermann is the primary target of int-spec.
// This test runs ack(2, 3) = 9 and ack(3, 3) = 61 through the baseline JIT
// and compares against the interpreter.
func TestTier1IntSpec_Ackermann(t *testing.T) {
	compareVMvsJIT(t, `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
result := ack(3, 3)
`, "result")
}

// TestTier1IntSpec_AckermannDeep validates the same function with deeper
// recursion where the int-spec hot path runs millions of times.
func TestTier1IntSpec_AckermannDeep(t *testing.T) {
	if testing.Short() {
		t.Skip("skip ackermann(3,6) in -short")
	}
	compareVMvsJIT(t, `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
result := ack(3, 6)
`, "result")
}

// TestTier1IntSpec_Fib exercises int-spec ADD/SUB/LT on fib's recursive body.
func TestTier1IntSpec_Fib(t *testing.T) {
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}
result := fib(15)
`, "result")
}

// TestTier1IntSpec_NoRegressionOnFloat: a function that uses float constants
// must remain correct. The analyzer's eligibility gate rejects protos with
// non-int LOADK, so the generic polymorphic templates run and behavior is
// unchanged.
func TestTier1IntSpec_NoRegressionOnFloat(t *testing.T) {
	compareVMvsJIT(t, `
func f(x, y) { return x * 1.5 + y * 0.7 }
result := 0.0
for i := 1; i <= 100; i++ { result = f(i, i) }
`, "result")
}

// TestTier1IntSpec_MixedParams: pass a float in where an int is expected at
// the first call, then an int in subsequent calls. The int-spec guard should
// fire once, deopt the proto, and re-execute generic — the final result must
// match the interpreter.
func TestTier1IntSpec_MixedParams(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a + b }
result := 0
// First call with a float triggers the param-int guard → deopt → generic.
x := f(1.5, 2.5)
// Subsequent int calls run on the recompiled generic template.
for i := 1; i <= 50; i++ { result = f(i, i + 1) }
// Expose x so the test exercises the float path too.
result = result + x
`, "result")
}

// TestTier1IntSpec_MutualRecursion covers mutual_recursion's hot body.
func TestTier1IntSpec_MutualRecursion(t *testing.T) {
	compareVMvsJIT(t, `
func is_even(n) {
    if n == 0 { return true }
    return is_odd(n - 1)
}
func is_odd(n) {
    if n == 0 { return false }
    return is_even(n - 1)
}
result := is_even(20)
`, "result")
}

// TestTier1IntSpec_AnalyzerDisabled: a proto containing OP_POW/DIV is
// rejected by the eligibility gate (OP_POW, OP_DIV are blacklisted), so
// intSpecEnabled is false and the generic polymorphic path runs.
func TestTier1IntSpec_AnalyzerDisabled(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a / b }
result := 0
for i := 1; i <= 50; i++ { result = f(i * 2, 3) }
`, "result")
}

// TestTier1IntSpec_AnalyzerLOADKFloat: a proto that loads a non-int constant
// is rejected, ensuring float constants never trigger spec.
func TestTier1IntSpec_AnalyzerLOADKFloat(t *testing.T) {
	compareVMvsJIT(t, `
func f(a) { return a + 1.25 }
result := 0.0
for i := 1; i <= 50; i++ { result = f(i) }
`, "result")
}
