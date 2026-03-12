package vm

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/ast"
	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

// compileAndRun compiles GScript source to bytecode and runs it in the VM.
// Returns the VM's globals map for inspection.
func compileAndRun(t *testing.T, src string) map[string]runtime.Value {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	globals := runtime.NewInterpreterGlobals()
	vm := New(globals)
	_, err = vm.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals
}

// compileAndRunWithOutput captures print output.
func compileAndRunWithOutput(t *testing.T, src string) (map[string]runtime.Value, string) {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	var buf strings.Builder
	globals := runtime.NewInterpreterGlobals()
	// Override print to capture output
	globals["print"] = runtime.FunctionValue(&runtime.GoFunction{
		Name: "print",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = a.String()
			}
			buf.WriteString(strings.Join(parts, "\t"))
			buf.WriteString("\n")
			return nil, nil
		},
	})

	vm := New(globals)
	_, err = vm.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals, buf.String()
}

func expectGlobalInt(t *testing.T, globals map[string]runtime.Value, name string, expected int64) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	if !v.IsInt() {
		t.Fatalf("global %q: expected int, got %s (%v)", name, v.TypeName(), v)
	}
	if v.Int() != expected {
		t.Errorf("global %q: got %d, want %d", name, v.Int(), expected)
	}
}

func expectGlobalFloat(t *testing.T, globals map[string]runtime.Value, name string, expected float64) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	if !v.IsNumber() {
		t.Fatalf("global %q: expected number, got %s (%v)", name, v.TypeName(), v)
	}
	if v.Number() != expected {
		t.Errorf("global %q: got %v, want %v", name, v.Number(), expected)
	}
}

func expectGlobalString(t *testing.T, globals map[string]runtime.Value, name string, expected string) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	if !v.IsString() {
		t.Fatalf("global %q: expected string, got %s (%v)", name, v.TypeName(), v)
	}
	if v.Str() != expected {
		t.Errorf("global %q: got %q, want %q", name, v.Str(), expected)
	}
}

func expectGlobalBool(t *testing.T, globals map[string]runtime.Value, name string, expected bool) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	if !v.IsBool() {
		t.Fatalf("global %q: expected bool, got %s (%v)", name, v.TypeName(), v)
	}
	if v.Bool() != expected {
		t.Errorf("global %q: got %v, want %v", name, v.Bool(), expected)
	}
}

func expectGlobalNil(t *testing.T, globals map[string]runtime.Value, name string) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		// Not in globals means nil
		return
	}
	if !v.IsNil() {
		t.Errorf("global %q: expected nil, got %s (%v)", name, v.TypeName(), v)
	}
}

// ============================================================================
// Phase 1: Constants & Basic Arithmetic
// ============================================================================

func TestIntLiteral(t *testing.T) {
	g := compileAndRun(t, `x := 42`)
	expectGlobalInt(t, g, "x", 42)
}

func TestFloatLiteral(t *testing.T) {
	g := compileAndRun(t, `x := 3.14`)
	expectGlobalFloat(t, g, "x", 3.14)
}

func TestBoolLiteral(t *testing.T) {
	g := compileAndRun(t, `x := true; y := false`)
	expectGlobalBool(t, g, "x", true)
	expectGlobalBool(t, g, "y", false)
}

func TestNilLiteral(t *testing.T) {
	g := compileAndRun(t, `x := nil`)
	expectGlobalNil(t, g, "x")
}

func TestStringLiteral(t *testing.T) {
	g := compileAndRun(t, `x := "hello"`)
	expectGlobalString(t, g, "x", "hello")
}

func TestArithmeticInt(t *testing.T) {
	g := compileAndRun(t, `
		a := 10 + 3
		b := 10 - 3
		c := 10 * 3
		d := 10 / 3
		e := 10 % 3
	`)
	expectGlobalInt(t, g, "a", 13)
	expectGlobalInt(t, g, "b", 7)
	expectGlobalInt(t, g, "c", 30)
	// 10/3 in integer arithmetic
	v := g["d"]
	if v.IsInt() {
		if v.Int() != 3 {
			t.Errorf("d: got %d, want 3", v.Int())
		}
	} else {
		expectGlobalFloat(t, g, "d", 10.0/3.0)
	}
	expectGlobalInt(t, g, "e", 1)
}

func TestArithmeticFloat(t *testing.T) {
	g := compileAndRun(t, `
		a := 1.5 + 2.5
		b := 10.0 / 3.0
	`)
	expectGlobalFloat(t, g, "a", 4.0)
	expectGlobalFloat(t, g, "b", 10.0/3.0)
}

func TestPower(t *testing.T) {
	g := compileAndRun(t, `x := 2 ** 10`)
	expectGlobalFloat(t, g, "x", 1024.0)
}

func TestUnaryMinus(t *testing.T) {
	g := compileAndRun(t, `x := -42; y := -3.14`)
	expectGlobalInt(t, g, "x", -42)
	expectGlobalFloat(t, g, "y", -3.14)
}

func TestUnaryNot(t *testing.T) {
	g := compileAndRun(t, `x := !true; y := !false; z := !nil`)
	expectGlobalBool(t, g, "x", false)
	expectGlobalBool(t, g, "y", true)
	expectGlobalBool(t, g, "z", true)
}

func TestConcat(t *testing.T) {
	g := compileAndRun(t, `x := "hello" .. " " .. "world"`)
	expectGlobalString(t, g, "x", "hello world")
}

func TestLength(t *testing.T) {
	g := compileAndRun(t, `x := #"hello"`)
	expectGlobalInt(t, g, "x", 5)
}

// ============================================================================
// Phase 2: Comparison & Logic
// ============================================================================

func TestComparison(t *testing.T) {
	g := compileAndRun(t, `
		a := 1 < 2
		b := 2 < 1
		c := 1 <= 1
		d := 1 > 2
		e := 2 >= 2
		f := 1 == 1
		g := 1 != 2
	`)
	expectGlobalBool(t, g, "a", true)
	expectGlobalBool(t, g, "b", false)
	expectGlobalBool(t, g, "c", true)
	expectGlobalBool(t, g, "d", false)
	expectGlobalBool(t, g, "e", true)
	expectGlobalBool(t, g, "f", true)
	expectGlobalBool(t, g, "g", true)
}

func TestShortCircuitAnd(t *testing.T) {
	g := compileAndRun(t, `
		a := true && 42
		b := false && 42
		c := nil && 42
	`)
	expectGlobalInt(t, g, "a", 42)
	expectGlobalBool(t, g, "b", false)
	expectGlobalNil(t, g, "c")
}

func TestShortCircuitOr(t *testing.T) {
	g := compileAndRun(t, `
		a := false || 42
		b := true || 42
		c := nil || "default"
	`)
	expectGlobalInt(t, g, "a", 42)
	expectGlobalBool(t, g, "b", true)
	expectGlobalString(t, g, "c", "default")
}

// ============================================================================
// Phase 3: Variables & Assignment
// ============================================================================

func TestVariableDeclaration(t *testing.T) {
	g := compileAndRun(t, `
		x := 10
		y := x + 5
	`)
	expectGlobalInt(t, g, "x", 10)
	expectGlobalInt(t, g, "y", 15)
}

func TestVariableAssignment(t *testing.T) {
	g := compileAndRun(t, `
		x := 10
		x = 20
	`)
	expectGlobalInt(t, g, "x", 20)
}

func TestCompoundAssignment(t *testing.T) {
	g := compileAndRun(t, `
		x := 10
		x += 5
		y := 20
		y -= 3
		z := 4
		z *= 5
	`)
	expectGlobalInt(t, g, "x", 15)
	expectGlobalInt(t, g, "y", 17)
	expectGlobalInt(t, g, "z", 20)
}

func TestIncDec(t *testing.T) {
	g := compileAndRun(t, `
		x := 10
		x++
		y := 5
		y--
	`)
	expectGlobalInt(t, g, "x", 11)
	expectGlobalInt(t, g, "y", 4)
}

func TestMultipleDeclaration(t *testing.T) {
	g := compileAndRun(t, `
		a, b := 10, 20
	`)
	expectGlobalInt(t, g, "a", 10)
	expectGlobalInt(t, g, "b", 20)
}

// ============================================================================
// Phase 4: Control Flow
// ============================================================================

func TestIfStatement(t *testing.T) {
	g := compileAndRun(t, `
		x := 0
		if true {
			x = 1
		}
	`)
	expectGlobalInt(t, g, "x", 1)
}

func TestIfElse(t *testing.T) {
	g := compileAndRun(t, `
		x := 0
		if false {
			x = 1
		} else {
			x = 2
		}
	`)
	expectGlobalInt(t, g, "x", 2)
}

func TestIfElseIf(t *testing.T) {
	g := compileAndRun(t, `
		x := 0
		if false {
			x = 1
		} elseif true {
			x = 2
		} else {
			x = 3
		}
	`)
	expectGlobalInt(t, g, "x", 2)
}

func TestForNumLoop(t *testing.T) {
	g := compileAndRun(t, `
		sum := 0
		for i := 1; i <= 10; i++ {
			sum += i
		}
	`)
	expectGlobalInt(t, g, "sum", 55)
}

func TestWhileLoop(t *testing.T) {
	g := compileAndRun(t, `
		x := 1
		for x < 100 {
			x = x * 2
		}
	`)
	expectGlobalInt(t, g, "x", 128)
}

func TestBreak(t *testing.T) {
	g := compileAndRun(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			if i > 5 {
				break
			}
			sum += i
		}
	`)
	expectGlobalInt(t, g, "sum", 15)
}

func TestContinue(t *testing.T) {
	g := compileAndRun(t, `
		sum := 0
		for i := 1; i <= 10; i++ {
			if i % 2 == 0 {
				continue
			}
			sum += i
		}
	`)
	expectGlobalInt(t, g, "sum", 25) // 1+3+5+7+9
}

func TestNestedLoops(t *testing.T) {
	g := compileAndRun(t, `
		count := 0
		for i := 0; i < 5; i++ {
			for j := 0; j < 5; j++ {
				count++
			}
		}
	`)
	expectGlobalInt(t, g, "count", 25)
}

// ============================================================================
// Phase 5: Functions
// ============================================================================

func TestFunctionDecl(t *testing.T) {
	g := compileAndRun(t, `
		func add(a, b) {
			return a + b
		}
		result := add(3, 4)
	`)
	expectGlobalInt(t, g, "result", 7)
}

func TestFunctionLiteral(t *testing.T) {
	g := compileAndRun(t, `
		add := func(a, b) { return a + b }
		result := add(10, 20)
	`)
	expectGlobalInt(t, g, "result", 30)
}

func TestRecursion(t *testing.T) {
	g := compileAndRun(t, `
		func fib(n) {
			if n < 2 { return n }
			return fib(n-1) + fib(n-2)
		}
		result := fib(10)
	`)
	expectGlobalInt(t, g, "result", 55)
}

func TestMultipleReturns(t *testing.T) {
	g := compileAndRun(t, `
		func swap(a, b) {
			return b, a
		}
		x, y := swap(1, 2)
	`)
	expectGlobalInt(t, g, "x", 2)
	expectGlobalInt(t, g, "y", 1)
}

func TestFunctionNoReturn(t *testing.T) {
	g := compileAndRun(t, `
		x := 0
		func inc() {
			x = x + 1
		}
		inc()
		inc()
		inc()
	`)
	expectGlobalInt(t, g, "x", 3)
}

// ============================================================================
// Phase 6: Closures & Upvalues
// ============================================================================

func TestSimpleClosure(t *testing.T) {
	g := compileAndRun(t, `
		func make() {
			x := 10
			return func() { return x }
		}
		f := make()
		result := f()
	`)
	expectGlobalInt(t, g, "result", 10)
}

func TestClosureMutation(t *testing.T) {
	g := compileAndRun(t, `
		func counter() {
			n := 0
			return func() {
				n = n + 1
				return n
			}
		}
		c := counter()
		a := c()
		b := c()
		c2 := c()
	`)
	expectGlobalInt(t, g, "a", 1)
	expectGlobalInt(t, g, "b", 2)
	expectGlobalInt(t, g, "c2", 3)
}

func TestNestedClosure(t *testing.T) {
	g := compileAndRun(t, `
		func outer() {
			x := 1
			func middle() {
				x = x + 10
				return func() {
					x = x + 100
					return x
				}
			}
			return middle()
		}
		f := outer()
		result := f()
	`)
	expectGlobalInt(t, g, "result", 111)
}

// ============================================================================
// Phase 7: Tables
// ============================================================================

func TestTableConstruction(t *testing.T) {
	g := compileAndRun(t, `
		t := {1, 2, 3}
		x := #t
	`)
	expectGlobalInt(t, g, "x", 3)
}

func TestTableHashConstruction(t *testing.T) {
	g := compileAndRun(t, `
		t := {x: 10, y: 20}
		result := t.x + t.y
	`)
	expectGlobalInt(t, g, "result", 30)
}

func TestTableFieldAccess(t *testing.T) {
	g := compileAndRun(t, `
		t := {}
		t.name = "hello"
		result := t.name
	`)
	expectGlobalString(t, g, "result", "hello")
}

func TestTableIndexAccess(t *testing.T) {
	g := compileAndRun(t, `
		t := {10, 20, 30}
		result := t[1] + t[2] + t[3]
	`)
	expectGlobalInt(t, g, "result", 60)
}

func TestTableLength(t *testing.T) {
	g := compileAndRun(t, `
		t := {}
		for i := 1; i <= 5; i++ {
			t[i] = i * i
		}
		result := #t
	`)
	expectGlobalInt(t, g, "result", 5)
}

// ============================================================================
// Phase 8: Standard Library Calls
// ============================================================================

func TestPrint(t *testing.T) {
	_, output := compileAndRunWithOutput(t, `print("hello", "world")`)
	expected := "hello\tworld\n"
	if output != expected {
		t.Errorf("output: got %q, want %q", output, expected)
	}
}

func TestType(t *testing.T) {
	g := compileAndRun(t, `
		a := type(42)
		b := type("hello")
		c := type(true)
		d := type(nil)
		e := type({})
	`)
	expectGlobalString(t, g, "a", "number")
	expectGlobalString(t, g, "b", "string")
	expectGlobalString(t, g, "c", "boolean")
	expectGlobalString(t, g, "d", "nil")
	expectGlobalString(t, g, "e", "table")
}

func TestTostring(t *testing.T) {
	g := compileAndRun(t, `
		x := tostring(42)
		y := tostring(true)
	`)
	expectGlobalString(t, g, "x", "42")
	expectGlobalString(t, g, "y", "true")
}

func TestTonumber(t *testing.T) {
	g := compileAndRun(t, `
		x := tonumber("42")
		y := tonumber("3.14")
	`)
	expectGlobalInt(t, g, "x", 42)
	expectGlobalFloat(t, g, "y", 3.14)
}

func TestTableInsert(t *testing.T) {
	g := compileAndRun(t, `
		t := {}
		table.insert(t, "a")
		table.insert(t, "b")
		table.insert(t, "c")
		result := #t
	`)
	expectGlobalInt(t, g, "result", 3)
}

func TestMathFloor(t *testing.T) {
	g := compileAndRun(t, `
		x := math.floor(3.7)
	`)
	expectGlobalInt(t, g, "x", 3)
}

// ============================================================================
// Phase 9: Complex Programs
// ============================================================================

func TestFibonacciBenchmark(t *testing.T) {
	g := compileAndRun(t, `
		func fib(n) {
			if n < 2 { return n }
			return fib(n-1) + fib(n-2)
		}
		result := fib(20)
	`)
	expectGlobalInt(t, g, "result", 6765)
}

func TestIterativeFib(t *testing.T) {
	g := compileAndRun(t, `
		func fib(n) {
			a := 0
			b := 1
			for i := 0; i < n; i++ {
				t := a + b
				a = b
				b = t
			}
			return a
		}
		result := fib(30)
	`)
	expectGlobalInt(t, g, "result", 832040)
}

func TestFunctionCalls10k(t *testing.T) {
	g := compileAndRun(t, `
		func add(a, b) {
			return a + b
		}
		x := 0
		for i := 0; i < 10000; i++ {
			x = add(x, 1)
		}
	`)
	expectGlobalInt(t, g, "x", 10000)
}

func TestTableOperations(t *testing.T) {
	g := compileAndRun(t, `
		t := {}
		for i := 0; i < 100; i++ {
			t[tostring(i)] = i
		}
		sum := 0
		for i := 0; i < 100; i++ {
			sum = sum + t[tostring(i)]
		}
	`)
	expectGlobalInt(t, g, "sum", 4950)
}

func TestClosureCreation(t *testing.T) {
	g := compileAndRun(t, `
		func make(x) {
			return func() { return x }
		}
		sum := 0
		for i := 1; i <= 100; i++ {
			f := make(i)
			sum = sum + f()
		}
	`)
	expectGlobalInt(t, g, "sum", 5050)
}

// ============================================================================
// Phase 10: Edge Cases
// ============================================================================

func TestEmptyReturn(t *testing.T) {
	g := compileAndRun(t, `
		func f() {
			return
		}
		x := f()
	`)
	expectGlobalNil(t, g, "x")
}

func TestVariableScoping(t *testing.T) {
	g := compileAndRun(t, `
		x := 1
		if true {
			x := 2
			y := x
		}
	`)
	expectGlobalInt(t, g, "x", 1)
}

func TestChainedComparison(t *testing.T) {
	g := compileAndRun(t, `
		x := 5
		result := x > 3 && x < 10
	`)
	expectGlobalBool(t, g, "result", true)
}

func TestNegativeForLoop(t *testing.T) {
	g := compileAndRun(t, `
		sum := 0
		for i := 10; i >= 1; i-- {
			sum += i
		}
	`)
	expectGlobalInt(t, g, "sum", 55)
}

// Test that is useful as a canary for the overall system
func TestIntegrationSmoke(t *testing.T) {
	g := compileAndRun(t, `
		func makeAdder(n) {
			return func(x) { return x + n }
		}
		add5 := makeAdder(5)
		add10 := makeAdder(10)
		result := add5(3) + add10(7)
	`)
	expectGlobalInt(t, g, "result", 25) // (3+5) + (7+10)
}

// =========================================================================
// Phase 11: Multi-return value bugs
// =========================================================================

func TestMultiReturnDeclaration(t *testing.T) {
	// Regression: multi-return from function call into local declaration
	// was assigning wrong registers (variables got values from prior expressions)
	g := compileAndRun(t, `
		func getTwo() {
			return 10, 20
		}
		label := "hello"
		a, b := getTwo()
		result := a + b
	`)
	expectGlobalInt(t, g, "result", 30)
}

func TestMultiReturnDeclarationArithmetic(t *testing.T) {
	// The actual chess bug: tw, th := measureTextEx(...) then tx := px - tw / 2
	g := compileAndRun(t, `
		func measure(text, size) {
			return 40.0, 20.0
		}
		label := "test"
		tw, th := measure(label, 26)
		tx := 100 - tw / 2
		ty := 200 - th / 2
		result_tx := tx
		result_th := th
	`)
	expectGlobalFloat(t, g, "result_tx", 80.0)
	expectGlobalFloat(t, g, "result_th", 20.0)
}

func TestMultiReturnDeclarationWithPriorLocals(t *testing.T) {
	// Multiple prior locals should not interfere with multi-return
	g := compileAndRun(t, `
		func triple() {
			return 1, 2, 3
		}
		x := 100
		y := 200
		z := "foo"
		a, b, c := triple()
		result := a * 100 + b * 10 + c
	`)
	expectGlobalInt(t, g, "result", 123)
}

func TestStringNumberCoercion(t *testing.T) {
	// String-to-number coercion in arithmetic (Lua semantics)
	g := compileAndRun(t, `
		a := "10" + 5
		b := 5 + "10"
		c := "3.14" * 2
		d := -"42"
		result := a + b + c
		neg := d
	`)
	expectGlobalFloat(t, g, "result", 36.28) // 15 + 15 + 6.28
	expectGlobalInt(t, g, "neg", -42)
}

// Ensure the test file is valid even if some helpers aren't wired up yet.
// This provides a graceful message.
func init() {
	// Verify Compile function exists (will fail at compile time if not)
	var _ func(*ast.Program) (*FuncProto, error) = Compile
}
