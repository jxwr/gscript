//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ─── Function trace tests ───

// runWithFuncTrace executes with tracing + SSA compilation + function tracing,
// returning globals and the trace recorder for inspection.
func runWithFuncTrace(t *testing.T, src string) (map[string]runtime.Value, *TraceRecorder) {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v.SetTraceRecorder(recorder)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals, recorder
}

// runInterpreter executes a script with the interpreter only, no JIT.
func runInterpreter(t *testing.T, src string) map[string]runtime.Value {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals
}

func TestFuncTrace_FibSmall(t *testing.T) {
	// fib(10) = 55, called enough times to trigger function trace recording.
	// fib(10) makes 177 total calls, well above the 50-call threshold.
	src := `
func fib(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
result = fib(10)
`
	// Get interpreter baseline
	interpGlobals := runInterpreter(t, src)
	expected := interpGlobals["result"].Int()
	if expected != 55 {
		t.Fatalf("interpreter fib(10) = %d, want 55", expected)
	}

	// Run with function tracing
	jitGlobals, recorder := runWithFuncTrace(t, src)
	actual := jitGlobals["result"].Int()

	if expected != actual {
		t.Errorf("fib(10): interpreter=%d, funcTrace=%d", expected, actual)
	}
	t.Logf("fib(10) = %d (interpreter=%d)", actual, expected)

	// Check that a function trace was compiled
	traces := recorder.Traces()
	hasFuncTrace := false
	for _, tr := range traces {
		if tr.IsFuncTrace {
			hasFuncTrace = true
			t.Logf("Function trace: proto=%s, nIR=%d, hasSelfCalls=%v",
				tr.LoopProto.Name, len(tr.IR), tr.HasSelfCalls)
		}
	}
	if hasFuncTrace {
		t.Logf("Function trace was recorded and compiled")
	} else {
		t.Logf("No function trace recorded (may still use interpreter fallback)")
	}
}

func TestFuncTrace_FibMedium(t *testing.T) {
	// fib(20) = 6765, many more calls to exercise compiled function trace.
	src := `
func fib(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
result = fib(20)
`
	interpGlobals := runInterpreter(t, src)
	expected := interpGlobals["result"].Int()

	jitGlobals, recorder := runWithFuncTrace(t, src)
	actual := jitGlobals["result"].Int()

	if expected != actual {
		t.Errorf("fib(20): interpreter=%d, funcTrace=%d", expected, actual)
	}
	t.Logf("fib(20) = %d", actual)

	// Check for function traces with self-calls
	hasFuncTraceWithSelfCalls := false
	for _, tr := range recorder.Traces() {
		if tr.IsFuncTrace {
			t.Logf("Function trace: proto=%s, nIR=%d, hasSelfCalls=%v, returnSlot=%d, returnCount=%d",
				tr.LoopProto.Name, len(tr.IR), tr.HasSelfCalls, tr.FuncReturnSlot, tr.FuncReturnCount)
			if tr.HasSelfCalls {
				hasFuncTraceWithSelfCalls = true
			}
		}
	}
	if !hasFuncTraceWithSelfCalls {
		t.Error("expected function trace with self-calls for fib")
	}
}

func TestFuncTrace_Ackermann(t *testing.T) {
	// Ackermann function: two-parameter self-recursion.
	src := `
func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}
r00 = ack(0, 0)
r10 = ack(1, 0)
r20 = ack(2, 0)
r30 = ack(3, 0)
r34 = ack(3, 4)
`
	interpGlobals := runInterpreter(t, src)
	jitGlobals, _ := runWithFuncTrace(t, src)

	for _, tc := range []struct {
		name     string
		expected int64
	}{
		{"r00", 1},
		{"r10", 2},
		{"r20", 3},
		{"r30", 5},
		{"r34", 125},
	} {
		interp := interpGlobals[tc.name].Int()
		jit := jitGlobals[tc.name].Int()
		if interp != tc.expected {
			t.Errorf("interpreter %s = %d, want %d", tc.name, interp, tc.expected)
		}
		if jit != tc.expected {
			t.Errorf("funcTrace %s = %d, want %d", tc.name, jit, tc.expected)
		}
	}
	t.Logf("Ackermann: all values match")
}

func TestFuncTrace_FibLarge(t *testing.T) {
	// fib(30) = 832040. This exercises the compiled function trace with
	// deep native recursion via BL self-calls.
	src := `
func fib(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
result = fib(30)
`
	interpGlobals := runInterpreter(t, src)
	expected := interpGlobals["result"].Int()
	if expected != 832040 {
		t.Fatalf("interpreter fib(30) = %d, want 832040", expected)
	}

	jitGlobals, recorder := runWithFuncTrace(t, src)
	actual := jitGlobals["result"].Int()

	if expected != actual {
		t.Errorf("fib(30): interpreter=%d, funcTrace=%d", expected, actual)
	}

	// Verify function trace was compiled with self-calls
	hasSelfCalls := false
	for _, tr := range recorder.Traces() {
		if tr.IsFuncTrace && tr.HasSelfCalls {
			hasSelfCalls = true
		}
	}
	if !hasSelfCalls {
		t.Error("expected function trace with self-calls for fib(30)")
	}
	t.Logf("fib(30) = %d (correct=%v)", actual, actual == expected)
}

func TestFuncTrace_Fib35(t *testing.T) {
	// fib(35) = 9227465. Correctness test for large recursive function traces.
	src := `
func fib(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
result = fib(35)
`
	jitGlobals, recorder := runWithFuncTrace(t, src)
	actual := jitGlobals["result"].Int()
	if actual != 9227465 {
		t.Errorf("fib(35) = %d, want 9227465", actual)
	}

	// Verify function trace was compiled with self-calls
	hasSelfCalls := false
	for _, tr := range recorder.Traces() {
		if tr.IsFuncTrace && tr.HasSelfCalls {
			hasSelfCalls = true
		}
	}
	if !hasSelfCalls {
		t.Error("expected function trace with self-calls for fib(35)")
	}
	hits, _ := recorder.FuncTraceStats()
	t.Logf("fib(35) = %d, funcTrace hits=%d", actual, hits)
}
