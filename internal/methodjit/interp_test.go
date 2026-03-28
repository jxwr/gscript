// interp_test.go tests the IR interpreter against the VM bytecode interpreter.
// For every test case, both interpreters must produce identical results.
// This is the correctness oracle for the entire Method JIT pipeline.

package methodjit

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// compileFunction compiles GScript source and returns the FuncProto for the
// first declared function. Same as compile() but named for clarity in interp tests.
func compileFunction(t *testing.T, src string) *vm.FuncProto {
	t.Helper()
	return compile(t, src)
}

// compileTop compiles full GScript source and returns the top-level (main) proto.
func compileTop(t *testing.T, src string) *vm.FuncProto {
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

// runVM executes a FuncProto via the VM interpreter with the given arguments.
// This is the ground truth that the IR interpreter must match.
func runVM(t *testing.T, src string, args []runtime.Value) []runtime.Value {
	t.Helper()
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	// Compile and execute the full source (registers function in globals).
	proto := compileTop(t, src)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	// Find the function name from the first inner proto.
	var fnName string
	for _, p := range proto.Protos {
		if p.Name != "" {
			fnName = p.Name
			break
		}
	}
	if fnName == "" {
		t.Fatal("no named function found in proto")
	}

	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		t.Fatalf("function %q not found in globals", fnName)
	}

	results, err := v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("VM call error: %v", err)
	}
	return results
}

// assertValuesEqual checks that two runtime.Value are identical.
func assertValuesEqual(t *testing.T, label string, got, want runtime.Value) {
	t.Helper()
	if got == want {
		return
	}
	// For float comparison, handle NaN and exact bit equality.
	if got.IsFloat() && want.IsFloat() {
		gf, wf := got.Float(), want.Float()
		if math.IsNaN(gf) && math.IsNaN(wf) {
			return
		}
		if gf == wf {
			return
		}
	}
	// Cross-type number equality (int == float with same numeric value).
	if got.IsNumber() && want.IsNumber() {
		if got.Number() == want.Number() {
			return
		}
	}
	t.Errorf("%s: IR=%v (type=%s), VM=%v (type=%s)",
		label, got, got.TypeName(), want, want.TypeName())
}

func TestInterp_Add(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	irResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, args)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "f(3,4)", irResult[0], vmResult[0])
	if !irResult[0].IsInt() || irResult[0].Int() != 7 {
		t.Errorf("expected 7, got %v", irResult[0])
	}
}

func TestInterp_Sub(t *testing.T) {
	src := `func f(a, b) { return a - b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}
	irResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, args)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "f(10,3)", irResult[0], vmResult[0])
	if !irResult[0].IsInt() || irResult[0].Int() != 7 {
		t.Errorf("expected 7, got %v", irResult[0])
	}
}

func TestInterp_Mul(t *testing.T) {
	src := `func f(a, b) { return a * b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	args := []runtime.Value{runtime.IntValue(6), runtime.IntValue(7)}
	irResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, args)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "f(6,7)", irResult[0], vmResult[0])
	if !irResult[0].IsInt() || irResult[0].Int() != 42 {
		t.Errorf("expected 42, got %v", irResult[0])
	}
}

func TestInterp_Div(t *testing.T) {
	src := `func f(a, b) { return a / b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}
	irResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, args)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "f(10,3)", irResult[0], vmResult[0])
	// Div always returns float.
	if !irResult[0].IsFloat() {
		t.Errorf("expected float result, got %s", irResult[0].TypeName())
	}
	expected := 10.0 / 3.0
	if math.Abs(irResult[0].Float()-expected) > 1e-10 {
		t.Errorf("expected %v, got %v", expected, irResult[0].Float())
	}
}

func TestInterp_IfElse(t *testing.T) {
	src := `func f(n) { if n < 2 { return n } else { return n * 2 } }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	// f(1) = 1 (true branch)
	args1 := []runtime.Value{runtime.IntValue(1)}
	irResult1, err := Interpret(fn, args1)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	vmResult1 := runVM(t, src, args1)
	if len(irResult1) == 0 || len(vmResult1) == 0 {
		t.Fatalf("empty result for f(1): IR=%v, VM=%v", irResult1, vmResult1)
	}
	assertValuesEqual(t, "f(1)", irResult1[0], vmResult1[0])

	// f(5) = 10 (false branch)
	args5 := []runtime.Value{runtime.IntValue(5)}
	irResult5, err := Interpret(fn, args5)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	vmResult5 := runVM(t, src, args5)
	if len(irResult5) == 0 || len(vmResult5) == 0 {
		t.Fatalf("empty result for f(5): IR=%v, VM=%v", irResult5, vmResult5)
	}
	assertValuesEqual(t, "f(5)", irResult5[0], vmResult5[0])
}

func TestInterp_ForLoop(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	args := []runtime.Value{runtime.IntValue(10)}
	irResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, args)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "f(10)", irResult[0], vmResult[0])
	if !irResult[0].IsInt() || irResult[0].Int() != 55 {
		t.Errorf("expected 55, got %v", irResult[0])
	}
}

func TestInterp_Fib(t *testing.T) {
	src := `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	args := []runtime.Value{runtime.IntValue(10)}
	irResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, args)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "fib(10)", irResult[0], vmResult[0])
	if !irResult[0].IsInt() || irResult[0].Int() != 55 {
		t.Errorf("expected 55, got %v", irResult[0])
	}
}

func TestInterp_TableField(t *testing.T) {
	src := `func f() { t := {x: 1, y: 2}; return t.x + t.y }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	irResult, err := Interpret(fn, nil)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, nil)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	assertValuesEqual(t, "f()", irResult[0], vmResult[0])
	if !irResult[0].IsInt() || irResult[0].Int() != 3 {
		t.Errorf("expected 3, got %v", irResult[0])
	}
}

func TestInterp_String(t *testing.T) {
	src := `func f() { return "hello" }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	irResult, err := Interpret(fn, nil)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	vmResult := runVM(t, src, nil)

	if len(irResult) == 0 || len(vmResult) == 0 {
		t.Fatalf("empty result: IR=%v, VM=%v", irResult, vmResult)
	}
	if !irResult[0].IsString() || irResult[0].Str() != "hello" {
		t.Errorf("expected \"hello\", got %v", irResult[0])
	}
	// String values won't be bit-identical (different *string pointers), compare content.
	if irResult[0].Str() != vmResult[0].Str() {
		t.Errorf("string mismatch: IR=%q, VM=%q", irResult[0].Str(), vmResult[0].Str())
	}
}

func TestInterp_Boolean(t *testing.T) {
	// Use if/else to return bool — avoids the LOADBOOL skip pattern
	// which has a known graph builder limitation with missing phi nodes.
	src := `func f(x) { if x > 0 { return true } else { return false } }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	// f(5) = true
	args5 := []runtime.Value{runtime.IntValue(5)}
	irResult5, err := Interpret(fn, args5)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	vmResult5 := runVM(t, src, args5)
	if len(irResult5) == 0 || len(vmResult5) == 0 {
		t.Fatalf("empty result for f(5)")
	}
	assertValuesEqual(t, "f(5)", irResult5[0], vmResult5[0])

	// f(-1) = false
	argsNeg := []runtime.Value{runtime.IntValue(-1)}
	irResultNeg, err := Interpret(fn, argsNeg)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	vmResultNeg := runVM(t, src, argsNeg)
	if len(irResultNeg) == 0 || len(vmResultNeg) == 0 {
		t.Fatalf("empty result for f(-1)")
	}
	assertValuesEqual(t, "f(-1)", irResultNeg[0], vmResultNeg[0])
}

// TestInterp_MatchesVM runs all test programs through both interpreters
// and asserts identical results.
func TestInterp_MatchesVM(t *testing.T) {
	tests := []struct {
		name string
		src  string
		args []runtime.Value
	}{
		{"add", `func f(a, b) { return a + b }`, []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}},
		{"sub", `func f(a, b) { return a - b }`, []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}},
		{"mul", `func f(a, b) { return a * b }`, []runtime.Value{runtime.IntValue(6), runtime.IntValue(7)}},
		{"div", `func f(a, b) { return a / b }`, []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}},
		{"if_true", `func f(n) { if n < 2 { return n } else { return n * 2 } }`, []runtime.Value{runtime.IntValue(1)}},
		{"if_false", `func f(n) { if n < 2 { return n } else { return n * 2 } }`, []runtime.Value{runtime.IntValue(5)}},
		{"for_loop", `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, []runtime.Value{runtime.IntValue(10)}},
		{"fib_0", `func f(n) { if n < 2 { return n }; return f(n-1) + f(n-2) }`, []runtime.Value{runtime.IntValue(0)}},
		{"fib_1", `func f(n) { if n < 2 { return n }; return f(n-1) + f(n-2) }`, []runtime.Value{runtime.IntValue(1)}},
		{"fib_10", `func f(n) { if n < 2 { return n }; return f(n-1) + f(n-2) }`, []runtime.Value{runtime.IntValue(10)}},
		{"string", `func f() { return "hello" }`, nil},
		{"float_add", `func f(a, b) { return a + b }`, []runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)}},
		{"int_float_add", `func f(a, b) { return a + b }`, []runtime.Value{runtime.IntValue(1), runtime.FloatValue(2.5)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := compileFunction(t, tt.src)
			fn := BuildGraph(proto)

			irResult, err := Interpret(fn, tt.args)
			if err != nil {
				t.Fatalf("IR interpreter error: %v", err)
			}

			vmResult := runVM(t, tt.src, tt.args)

			if len(irResult) != len(vmResult) {
				t.Fatalf("result count mismatch: IR=%d, VM=%d", len(irResult), len(vmResult))
			}
			for i := range irResult {
				if irResult[i].IsString() && vmResult[i].IsString() {
					if irResult[i].Str() != vmResult[i].Str() {
						t.Errorf("%s result[%d]: IR=%q, VM=%q",
							tt.name, i, irResult[i].Str(), vmResult[i].Str())
					}
				} else {
					assertValuesEqual(t, tt.name, irResult[i], vmResult[i])
				}
			}
		})
	}
}
