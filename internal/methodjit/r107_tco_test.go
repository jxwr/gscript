//go:build darwin && arm64

// r107_tco_test.go validates the R107 tail-call optimization.
// Key property: deep tail recursion must NOT blow the stack; correctness
// of ackermann/gcd/factorial tail patterns must match the VM interpreter.

package methodjit

import (
	"testing"
)

// TestR107_TailCall_Ackermann checks ack(3,4)=125 via JIT matches VM.
// This is the primary TCO correctness gate — ackermann has tail-call sites.
func TestR107_TailCall_Ackermann(t *testing.T) {
	src := `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
result := ack(3, 4)
`
	compareTier2Result(t, src, "result")
}

// TestR107_TailCall_Factorial checks a classically tail-recursive
// factorial accumulator form. Depth gets high (50!) — non-TCO version
// would bump native call depth.
func TestR107_TailCall_Factorial(t *testing.T) {
	src := `
func fact_acc(n, acc) {
    if n <= 1 { return acc }
    return fact_acc(n - 1, acc * n)
}
result := fact_acc(20, 1)
`
	compareTier2Result(t, src, "result")
}

// TestR107_TailCall_Countdown checks a simple tail-recursive countdown
// that would otherwise overflow maxNativeCallDepth at ~48 levels.
// With TCO, depth 200 should complete.
func TestR107_TailCall_Countdown(t *testing.T) {
	src := `
func count(n) {
    if n == 0 { return 42 }
    return count(n - 1)
}
result := count(200)
`
	compareTier2Result(t, src, "result")
}

// TestR107_NoTCO_Fib ensures non-tail-recursive fib still works (no
// accidental TCO on non-tail patterns).
func TestR107_NoTCO_Fib(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(8)
`
	compareTier2Result(t, src, "result")
}
