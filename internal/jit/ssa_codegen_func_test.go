//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ─── Function trace tests ───
// These tests are disabled because function-entry tracing has been removed.
// It caused crashes (binary_trees, mutual_recursion) and wrong results.
// Will be replaced by proper trace-through-calls.

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
	t.Skip("function-entry tracing removed; will be replaced by trace-through-calls")
}

func TestFuncTrace_FibMedium(t *testing.T) {
	t.Skip("function-entry tracing removed; will be replaced by trace-through-calls")
}

func TestFuncTrace_Ackermann(t *testing.T) {
	t.Skip("function-entry tracing removed; will be replaced by trace-through-calls")
}

func TestFuncTrace_FibLarge(t *testing.T) {
	t.Skip("function-entry tracing removed; will be replaced by trace-through-calls")
}

func TestFuncTrace_Fib35(t *testing.T) {
	t.Skip("function-entry tracing removed; will be replaced by trace-through-calls")
}
