package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runWithTracingJIT executes with tracing + compilation, returns globals.
func runWithTracingJIT(t *testing.T, src string) map[string]runtime.Value {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	recorder.SetCompile(true) // enable compilation + execution of traces
	v.SetTraceRecorder(recorder)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals
}

func TestTraceCompile_SimpleAdd(t *testing.T) {
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 1000; i++ {
			sum = sum + i
		}
	`)
	if v := g["sum"]; v.Int() != 500500 {
		t.Errorf("sum = %d, want 500500", v.Int())
	}
}

func TestTraceCompile_ForLoop(t *testing.T) {
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + i * i
		}
	`)
	// sum of i^2 for i=1..100 = 338350
	if v := g["sum"]; v.Int() != 338350 {
		t.Errorf("sum = %d, want 338350", v.Int())
	}
}

func TestTraceCompile_Nested(t *testing.T) {
	t.Skip("TODO: nested loops need trace linking (Phase D)")
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			for j := 1; j <= 50; j++ {
				sum = sum + 1
			}
		}
	`)
	if v := g["sum"]; v.Int() != 2500 {
		t.Errorf("sum = %d, want 2500", v.Int())
	}
}

func TestTraceCompile_Conditional(t *testing.T) {
	t.Skip("TODO: conditionals in traces need side-exit to interpreter")
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			if i % 2 == 0 {
				sum = sum + i
			}
		}
	`)
	// sum of even numbers 2+4+...+100 = 2550
	if v := g["sum"]; v.Int() != 2550 {
		t.Errorf("sum = %d, want 2550", v.Int())
	}
}

func TestTraceCompile_MatchesInterpreter(t *testing.T) {
	src := `
		a := 0
		b := 1
		for i := 0; i < 30; i++ {
			t := a + b
			a = b
			b = t
		}
		result := a
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with tracing JIT
	g2 := runWithTracingJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, tracing=%d", g1["result"].Int(), g2["result"].Int())
	}
}
