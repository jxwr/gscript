//go:build darwin && arm64

// emit_tier2_correctness_test.go reproduces Tier 2 correctness failures found
// in failing benchmarks. Each test runs the same GScript program twice: once
// via the VM interpreter (oracle) and once with TieringManager (Tier 2
// promotion). Results are compared; any mismatch indicates a Tier 2 bug.

package methodjit

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// tier2TestTimeout is the per-test timeout. Tests that hang (indicating a
// Tier 2 bug such as an infinite loop in table exit handling) will be
// reported as failures rather than blocking the entire suite.
const tier2TestTimeout = 10 * time.Second

// executeWithTimeout runs a VM execution in a goroutine with a timeout.
// Returns the result global and any error. If the execution hangs, it
// returns an error after the timeout (the goroutine leaks, but that is
// acceptable for a failing-reproducer test).
type execResult struct {
	val runtime.Value
	err error
}

func executeGetGlobal(v *vm.VM, proto *vm.FuncProto, globalName string) (runtime.Value, error) {
	_, err := v.Execute(proto)
	if err != nil {
		return runtime.NilValue(), err
	}
	return v.GetGlobal(globalName), nil
}

// compareTier2Result runs src twice (VM-only and with TieringManager) and
// compares the global named globalName. Fails the test on mismatch.
// Uses a per-execution timeout to detect hangs from Tier 2 bugs.
func compareTier2Result(t *testing.T, src, globalName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), tier2TestTimeout)
	defer cancel()

	// Run 1: VM only (no JIT) — oracle
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()

	vmCh := make(chan execResult, 1)
	go func() {
		val, err := executeGetGlobal(vVM, protoVM, globalName)
		vmCh <- execResult{val, err}
	}()

	var vmResult runtime.Value
	select {
	case res := <-vmCh:
		if res.err != nil {
			t.Fatalf("VM execute error: %v", res.err)
		}
		vmResult = res.val
	case <-ctx.Done():
		t.Fatalf("VM execution timed out after %v", tier2TestTimeout)
	}

	// Run 2: With TieringManager (Tier 2)
	protoJIT := compileProto(t, src)
	globalsJIT := runtime.NewInterpreterGlobals()
	vJIT := vm.New(globalsJIT)
	defer vJIT.Close()
	tm := NewTieringManager()
	vJIT.SetMethodJIT(tm)

	jitCh := make(chan execResult, 1)
	go func() {
		val, err := executeGetGlobal(vJIT, protoJIT, globalName)
		jitCh <- execResult{val, err}
	}()

	var jitResult runtime.Value
	select {
	case res := <-jitCh:
		if res.err != nil {
			t.Fatalf("JIT execute error: %v", res.err)
		}
		jitResult = res.val
	case <-ctx.Done():
		t.Fatalf("JIT execution timed out after %v (likely infinite loop in Tier 2)", tier2TestTimeout)
	}

	// Compare
	if vmResult.IsInt() && jitResult.IsInt() {
		if vmResult.Int() != jitResult.Int() {
			t.Errorf("int mismatch: VM=%d, JIT=%d", vmResult.Int(), jitResult.Int())
		}
	} else if vmResult.IsFloat() && jitResult.IsFloat() {
		vmF, jitF := vmResult.Float(), jitResult.Float()
		if math.Abs(vmF-jitF) > 1e-6*math.Abs(vmF) {
			t.Errorf("float mismatch: VM=%f, JIT=%f", vmF, jitF)
		}
	} else if vmResult == jitResult {
		// Exact bit match (covers nil, bool, same-typed values)
	} else {
		t.Errorf("type/value mismatch: VM=%v (%s), JIT=%v (%s)",
			vmResult, vmResult.TypeName(), jitResult, jitResult.TypeName())
	}
}

// TestTier2_SieveCorrectness exercises GETTABLE+SETTABLE in nested loops with
// a boolean array. The sieve of Eratosthenes pattern stresses table read/write
// inside while-style and for-style loops.
func TestTier2_SieveCorrectness(t *testing.T) {
	src := `
func sieve(n) {
    is_prime := {}
    for i := 0; i <= n; i++ {
        is_prime[i] = true
    }
    count := 0
    i := 2
    for i * i <= n {
        if is_prime[i] {
            j := i * i
            for j <= n {
                is_prime[j] = false
                j = j + i
            }
        }
        i = i + 1
    }
    for i := 2; i <= n; i++ {
        if is_prime[i] {
            count = count + 1
        }
    }
    return count
}
result := 0
for iter := 1; iter <= 3; iter++ {
    result = sieve(100)
}
`
	compareTier2Result(t, src, "result")

	// Verify expected value: 25 primes up to 100
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()
	vVM.Execute(protoVM)
	vmResult := vVM.GetGlobal("result")
	if vmResult.IsInt() && vmResult.Int() != 25 {
		t.Errorf("sieve(100) VM sanity check: got %d, want 25", vmResult.Int())
	}
}

// TestTier2_FibonacciIterativeCorrectness tests a pure integer loop with
// accumulator variables. Exercises int arithmetic and loop register state
// preservation across Tier 2 compilation.
func TestTier2_FibonacciIterativeCorrectness(t *testing.T) {
	src := `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 1; i <= n; i++ {
        temp := a + b
        a = b
        b = temp
    }
    return a
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = fib_iter(30)
}
`
	compareTier2Result(t, src, "result")

	// Verify expected value: fib(30) = 832040
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()
	vVM.Execute(protoVM)
	vmResult := vVM.GetGlobal("result")
	if vmResult.IsInt() && vmResult.Int() != 832040 {
		t.Errorf("fib_iter(30) VM sanity check: got %d, want 832040", vmResult.Int())
	}
}

// TestTier2_TableFieldCorrectness tests GETFIELD+SETFIELD inside a loop with
// float arithmetic. Exercises inline field cache correctness and float
// register handling at Tier 2.
func TestTier2_TableFieldCorrectness(t *testing.T) {
	src := `
func step(n) {
    p := {x: 1.0, y: 2.0, vx: 0.1, vy: 0.2}
    for i := 1; i <= n; i++ {
        p.x = p.x + p.vx
        p.y = p.y + p.vy
    }
    return p.x + p.y
}
result := 0.0
for iter := 1; iter <= 5; iter++ {
    result = step(100)
}
`
	compareTier2Result(t, src, "result")
}

// TestTier2_CallInLoopCorrectness tests function calls inside a loop.
// Exercises emitCallNative spill/reload of SSA registers around BLR.
func TestTier2_CallInLoopCorrectness(t *testing.T) {
	src := `
func add(a, b) {
    return a + b
}
func sum_with_calls(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = add(s, i)
    }
    return s
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = sum_with_calls(100)
}
`
	compareTier2Result(t, src, "result")

	// Verify expected value: sum(1..100) = 5050
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()
	vVM.Execute(protoVM)
	vmResult := vVM.GetGlobal("result")
	if vmResult.IsInt() && vmResult.Int() != 5050 {
		t.Errorf("sum_with_calls(100) VM sanity check: got %d, want 5050", vmResult.Int())
	}
}

// TestTier2_NestedLoopTableCorrectness tests nested loops with GETTABLE,
// similar to a matrix multiplication pattern. Stresses table index reads
// inside tight nested loops at Tier 2.
func TestTier2_NestedLoopTableCorrectness(t *testing.T) {
	src := `
func matmul_small() {
    a := {1, 2, 3, 4}
    b := {5, 6, 7, 8}
    sum := 0
    for i := 1; i <= 4; i++ {
        for j := 1; j <= 4; j++ {
            sum = sum + a[i] * b[j]
        }
    }
    return sum
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = matmul_small()
}
`
	compareTier2Result(t, src, "result")

	// Verify expected value: (1+2+3+4)*(5+6+7+8) = 10*26 = 260
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()
	vVM.Execute(protoVM)
	vmResult := vVM.GetGlobal("result")
	if vmResult.IsInt() && vmResult.Int() != 260 {
		t.Errorf("matmul_small() VM sanity check: got %d, want 260", vmResult.Int())
	}
}

// TestTier2_MixedIntFloatCorrectness tests mixed int/float arithmetic in a
// loop. Exercises type specialization correctness when integer loop variable
// is multiplied by a float constant.
func TestTier2_MixedIntFloatCorrectness(t *testing.T) {
	src := `
func mixed(n) {
    sum := 0.0
    for i := 1; i <= n; i++ {
        sum = sum + i * 0.5
    }
    return sum
}
result := 0.0
for iter := 1; iter <= 5; iter++ {
    result = mixed(100)
}
`
	compareTier2Result(t, src, "result")

	// Verify expected value: 0.5 * sum(1..100) = 0.5 * 5050 = 2525.0
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()
	vVM.Execute(protoVM)
	vmResult := vVM.GetGlobal("result")
	if vmResult.IsFloat() && math.Abs(vmResult.Float()-2525.0) > 1e-6 {
		t.Errorf("mixed(100) VM sanity check: got %f, want 2525.0", vmResult.Float())
	}
}
