package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/ast"
	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// compileProto compiles GScript source to a FuncProto.
func compileProto(t *testing.T, src string) *vm.FuncProto {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return proto
}

// runWithTracing executes a script with tracing enabled, returns recorded traces.
func runWithTracing(t *testing.T, src string) ([]*Trace, map[string]runtime.Value) {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	v.SetTraceRecorder(recorder)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return recorder.Traces(), globals
}

// --- Tests ---

func TestTraceRecorder_SimpleForLoop(t *testing.T) {
	traces, globals := runWithTracing(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + i
		}
	`)

	// Verify the result is correct
	if v, ok := globals["sum"]; !ok || v.Int() != 5050 {
		t.Errorf("sum = %v, want 5050", globals["sum"])
	}

	// Should have recorded at least one trace for the for-loop
	if len(traces) == 0 {
		t.Fatal("expected at least one trace, got none")
	}

	tr := traces[0]
	if len(tr.IR) == 0 {
		t.Fatal("trace has no instructions")
	}

	// Trace should contain ADD and FORLOOP
	hasAdd := false
	hasForloop := false
	for _, ir := range tr.IR {
		if ir.Op == vm.OP_ADD {
			hasAdd = true
		}
		if ir.Op == vm.OP_FORLOOP {
			hasForloop = true
		}
	}
	if !hasAdd {
		t.Error("trace missing OP_ADD")
	}
	if !hasForloop {
		t.Error("trace missing OP_FORLOOP")
	}
}

func TestTraceRecorder_TypeCapture(t *testing.T) {
	traces, _ := runWithTracing(t, `
		sum := 0
		for i := 1; i <= 20; i++ {
			sum = sum + i
		}
	`)

	if len(traces) == 0 {
		t.Fatal("expected at least one trace")
	}

	// All operands in this loop should be TypeInt
	for _, ir := range traces[0].IR {
		if ir.Op == vm.OP_ADD {
			if ir.BType != runtime.TypeInt {
				t.Errorf("ADD operand B type = %d, want TypeInt(%d)", ir.BType, runtime.TypeInt)
			}
			if ir.CType != runtime.TypeInt {
				t.Errorf("ADD operand C type = %d, want TypeInt(%d)", ir.CType, runtime.TypeInt)
			}
		}
	}
}

func TestTraceRecorder_TableGetField(t *testing.T) {
	traces, globals := runWithTracing(t, `
		t := {x: 10}
		sum := 0
		for i := 1; i <= 20; i++ {
			sum = sum + t.x
		}
	`)

	if v := globals["sum"]; v.Int() != 200 {
		t.Errorf("sum = %v, want 200", globals["sum"])
	}

	if len(traces) == 0 {
		t.Fatal("expected at least one trace")
	}

	// Trace should contain GETFIELD
	hasGetField := false
	for _, ir := range traces[0].IR {
		if ir.Op == vm.OP_GETFIELD {
			hasGetField = true
		}
	}
	if !hasGetField {
		t.Error("trace missing OP_GETFIELD")
	}
}

func TestTraceRecorder_NestedCall(t *testing.T) {
	traces, globals := runWithTracing(t, `
		func double(x) { return x * 2 }
		sum := 0
		for i := 1; i <= 20; i++ {
			sum = sum + double(i)
		}
	`)

	// double(1)+double(2)+...+double(20) = 2*(1+2+...+20) = 2*210 = 420
	if v := globals["sum"]; v.Int() != 420 {
		t.Errorf("sum = %v, want 420", globals["sum"])
	}

	if len(traces) == 0 {
		t.Fatal("expected at least one trace")
	}

	// Trace should have inlined the function call:
	// Should contain MUL (from double's body) without a separate CALL
	hasMul := false
	for _, ir := range traces[0].IR {
		if ir.Op == vm.OP_MUL {
			hasMul = true
		}
	}
	if !hasMul {
		t.Error("trace missing OP_MUL (function not inlined?)")
	}
}

func TestTraceRecorder_Correctness(t *testing.T) {
	// Verify that tracing doesn't change program behavior
	src := `
		sum := 0
		for i := 1; i <= 50; i++ {
			if i % 2 == 0 {
				sum = sum + i
			}
		}
	`

	// Run without tracing
	proto := compileProto(t, src)
	globals1 := runtime.NewInterpreterGlobals()
	v1 := vm.New(globals1)
	v1.Execute(proto)

	// Run with tracing
	_, globals2 := runWithTracing(t, src)

	// Results should match
	if globals1["sum"].Int() != globals2["sum"].Int() {
		t.Errorf("tracing changed result: without=%d, with=%d",
			globals1["sum"].Int(), globals2["sum"].Int())
	}
}

// Ensure unused import doesn't cause build failure
var _ ast.Node

func TestTraceRecorder_WhileLoopBackEdge(t *testing.T) {
	// While-loops compile to a backward OP_JMP (not FORLOOP).
	// With back-edge detection re-enabled, these should be traced.
	traces, globals := runWithTracing(t, `
		sum := 0
		i := 1
		for i <= 100 {
			sum = sum + i
			i = i + 1
		}
	`)

	// Verify correctness: sum(1..100) = 5050
	if v, ok := globals["sum"]; !ok || v.Int() != 5050 {
		t.Errorf("sum = %v, want 5050", globals["sum"])
	}

	// Should have recorded at least one trace for the while-loop
	if len(traces) == 0 {
		t.Fatal("expected at least one trace for while-loop, got none")
	}

	// The trace should contain ADD and a backward JMP (not FORLOOP)
	hasAdd := false
	hasJmpBack := false
	hasForloop := false
	for _, tr := range traces {
		for _, ir := range tr.IR {
			if ir.Op == vm.OP_ADD {
				hasAdd = true
			}
			if ir.Op == vm.OP_JMP && ir.SBX < 0 {
				hasJmpBack = true
			}
			if ir.Op == vm.OP_FORLOOP {
				hasForloop = true
			}
		}
	}
	if !hasAdd {
		t.Error("while-loop trace missing OP_ADD")
	}
	if !hasJmpBack {
		t.Error("while-loop trace missing backward OP_JMP")
	}
	if hasForloop {
		t.Error("while-loop trace should not contain OP_FORLOOP")
	}
}

func TestTraceRecorder_WhileLoopInFunction(t *testing.T) {
	// While-loop inside a function — ensures tracing works with function calls.
	// Uses enough iterations (10000) to exceed trace threshold and verify correctness.
	src := `
		func sumWhile(n) {
			s := 0
			i := 1
			for i <= n {
				s = s + i
				i = i + 1
			}
			return s
		}
		result := sumWhile(10000)
	`

	traces, globals := runWithTracing(t, src)

	// Verify correctness: sum(1..10000) = 50005000
	if globals["result"].Int() != 50005000 {
		t.Errorf("result = %d, want 50005000", globals["result"].Int())
	}

	// Should have at least one trace (the while-loop inside sumWhile)
	if len(traces) == 0 {
		t.Fatal("expected at least one trace for while-loop in function, got none")
	}

	t.Logf("Recorded %d trace(s) for while-loop in function", len(traces))
}
