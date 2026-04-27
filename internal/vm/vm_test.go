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

func TestTableLiteralNilFieldsDoNotInflateHashHint(t *testing.T) {
	tokens, err := lexer.New(`t := {left: nil, right: nil, value: 1}`).Tokenize()
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
	newTables := 0
	setFields := 0
	for _, inst := range proto.Code {
		switch DecodeOp(inst) {
		case OP_NEWTABLE:
			newTables++
			if got := DecodeC(inst); got != 1 {
				t.Fatalf("NEWTABLE hash hint = %d, want 1", got)
			}
		case OP_SETFIELD:
			setFields++
		}
	}
	if newTables != 1 {
		t.Fatalf("NEWTABLE count = %d, want 1", newTables)
	}
	if setFields != 1 {
		t.Fatalf("SETFIELD count = %d, want 1", setFields)
	}
}

func TestTableLiteralNilStringFieldsAreOmitted(t *testing.T) {
	src := `t := {left: nil, right: nil}`
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

	newTables := 0
	setFields := 0
	for _, inst := range proto.Code {
		switch DecodeOp(inst) {
		case OP_NEWTABLE:
			newTables++
			if DecodeC(inst) != 0 {
				t.Fatalf("nil-only keyed literal hash hint = %d, want 0", DecodeC(inst))
			}
		case OP_SETFIELD:
			setFields++
		}
	}
	if newTables != 1 {
		t.Fatalf("NEWTABLE count = %d, want 1", newTables)
	}
	if setFields != 0 {
		t.Fatalf("SETFIELD count = %d, want 0", setFields)
	}

	g := compileAndRun(t, src)
	tbl := g["t"].Table()
	if tbl.SkeysLen() != 0 {
		t.Fatalf("nil-only keyed literal stored %d string fields, want 0", tbl.SkeysLen())
	}
	if !tbl.RawGetString("left").IsNil() || !tbl.RawGetString("right").IsNil() {
		t.Fatalf("omitted nil fields should still read back as nil")
	}
}

func TestTableLiteralComputedNilKeyStillEvaluatesKey(t *testing.T) {
	g := compileAndRun(t, `
		called := 0
		func key() {
			called = called + 1
			return "gone"
		}
		t := {[key()]: nil}
	`)
	expectGlobalInt(t, g, "called", 1)
	tbl := g["t"].Table()
	if tbl.SkeysLen() != 0 {
		t.Fatalf("computed nil field stored %d string fields, want 0", tbl.SkeysLen())
	}
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

// compileAndRunExpectError compiles and runs, expecting a runtime error.
func compileAndRunExpectError(t *testing.T, src string) error {
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
	return err
}

// ============================================================================
// VM Coroutine Tests
// ============================================================================

// Test 1: Basic yield/resume cycle
func TestVMCoroutineBasic(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {
	coroutine.yield(1)
	coroutine.yield(2)
	coroutine.yield(3)
})
ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co)
ok3, v3 := coroutine.resume(co)
ok4 := coroutine.resume(co)
`)
	expectGlobalBool(t, g, "ok1", true)
	expectGlobalInt(t, g, "v1", 1)
	expectGlobalBool(t, g, "ok2", true)
	expectGlobalInt(t, g, "v2", 2)
	expectGlobalBool(t, g, "ok3", true)
	expectGlobalInt(t, g, "v3", 3)
	expectGlobalBool(t, g, "ok4", true)
}

// Test 2: Values passed to yield and from resume
func TestVMCoroutinePassValues(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func(a) {
	b := coroutine.yield(a * 2)
	return b + 1
})
ok1, v1 := coroutine.resume(co, 5)
ok2, v2 := coroutine.resume(co, 20)
`)
	expectGlobalBool(t, g, "ok1", true)
	expectGlobalInt(t, g, "v1", 10)
	expectGlobalBool(t, g, "ok2", true)
	expectGlobalInt(t, g, "v2", 21)
}

// Test 3: coroutine.wrap creates an iterator function
func TestVMCoroutineWrap(t *testing.T) {
	g := compileAndRun(t, `
gen := coroutine.wrap(func() {
	coroutine.yield(1)
	coroutine.yield(2)
	coroutine.yield(3)
})
r1 := gen()
r2 := gen()
r3 := gen()
`)
	expectGlobalInt(t, g, "r1", 1)
	expectGlobalInt(t, g, "r2", 2)
	expectGlobalInt(t, g, "r3", 3)
}

// Test 4: coroutine.status returns correct status strings
func TestVMCoroutineStatus(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {
	coroutine.yield()
})
s1 := coroutine.status(co)
coroutine.resume(co)
s2 := coroutine.status(co)
coroutine.resume(co)
s3 := coroutine.status(co)
`)
	expectGlobalString(t, g, "s1", "suspended")
	expectGlobalString(t, g, "s2", "suspended")
	expectGlobalString(t, g, "s3", "dead")
}

// Test 5: Producer pattern with coroutine
func TestVMCoroutineProducer(t *testing.T) {
	g := compileAndRun(t, `
func producer() {
	coroutine.yield(10)
	coroutine.yield(20)
	coroutine.yield(30)
	coroutine.yield(40)
	coroutine.yield(50)
}

co := coroutine.create(producer)
total := 0
for {
	ok, v := coroutine.resume(co)
	if !ok || v == nil {
		break
	}
	total = total + v
}
`)
	expectGlobalInt(t, g, "total", 150)
}

// Test 6: Generator pattern with wrap
func TestVMCoroutineGenerator(t *testing.T) {
	g := compileAndRun(t, `
func range_gen(n) {
	return coroutine.wrap(func() {
		for i := 1; i <= n; i++ {
			coroutine.yield(i)
		}
	})
}

sum := 0
gen := range_gen(5)
for {
	v := gen()
	if v == nil {
		break
	}
	sum = sum + v
}
`)
	expectGlobalInt(t, g, "sum", 15)
}

// Test 7: Dead coroutine returns false with error message
func TestVMCoroutineDeadError(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() { return 42 })
coroutine.resume(co)
ok, msg := coroutine.resume(co)
`)
	expectGlobalBool(t, g, "ok", false)
	v := g["msg"]
	if !strings.Contains(v.String(), "dead") {
		t.Errorf("expected msg to contain 'dead', got %v", v)
	}
}

// Test 8: Nested coroutines (outer resumes inner)
func TestVMCoroutineNested(t *testing.T) {
	g := compileAndRun(t, `
inner := coroutine.create(func() {
	coroutine.yield(1)
	coroutine.yield(2)
})

outer := coroutine.create(func() {
	ok, v := coroutine.resume(inner)
	coroutine.yield(v * 10)
	ok, v = coroutine.resume(inner)
	coroutine.yield(v * 10)
})

_, r1 := coroutine.resume(outer)
_, r2 := coroutine.resume(outer)
`)
	expectGlobalInt(t, g, "r1", 10)
	expectGlobalInt(t, g, "r2", 20)
}

// Test 9: coroutine.isyieldable
func TestVMCoroutineIsYieldable(t *testing.T) {
	g := compileAndRun(t, `
outside := coroutine.isyieldable()
inside := false
co := coroutine.create(func() {
	inside = coroutine.isyieldable()
	coroutine.yield()
})
coroutine.resume(co)
`)
	expectGlobalBool(t, g, "outside", false)
	expectGlobalBool(t, g, "inside", true)
}

// Test 10: Coroutine with multiple yield values
func TestVMCoroutineMultipleYieldValues(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {
	coroutine.yield(1, 2, 3)
})
ok, a, b, c := coroutine.resume(co)
`)
	expectGlobalBool(t, g, "ok", true)
	expectGlobalInt(t, g, "a", 1)
	expectGlobalInt(t, g, "b", 2)
	expectGlobalInt(t, g, "c", 3)
}

// Test 11: Coroutine with return value
func TestVMCoroutineReturnValue(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {
	coroutine.yield(1)
	return 99
})
ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co)
s := coroutine.status(co)
`)
	expectGlobalBool(t, g, "ok1", true)
	expectGlobalInt(t, g, "v1", 1)
	expectGlobalBool(t, g, "ok2", true)
	expectGlobalInt(t, g, "v2", 99)
	expectGlobalString(t, g, "s", "dead")
}

// Test 12: wrap with error propagation on dead coroutine
func TestVMCoroutineWrapDead(t *testing.T) {
	err := compileAndRunExpectError(t, `
gen := coroutine.wrap(func() {
	coroutine.yield(1)
})
gen()
gen()
gen()
`)
	if err == nil {
		t.Fatal("expected an error when calling wrapped dead coroutine")
	}
	if !strings.Contains(err.Error(), "dead") {
		t.Errorf("expected error to contain 'dead', got %v", err)
	}
}

// Test 13: Fibonacci generator using coroutines
func TestVMCoroutineFibonacci(t *testing.T) {
	g := compileAndRun(t, `
func fib_gen() {
	a, b := 0, 1
	for {
		coroutine.yield(a)
		a, b = b, a + b
	}
}

co := coroutine.create(fib_gen)
results := {}
for i := 0; i < 10; i++ {
	_, v := coroutine.resume(co)
	results[i + 1] = v
}
`)
	expected := []int64{0, 1, 1, 2, 3, 5, 8, 13, 21, 34}
	results := g["results"].Table()
	for i, exp := range expected {
		v := results.RawGet(runtime.IntValue(int64(i + 1)))
		if !v.IsInt() || v.Int() != exp {
			t.Errorf("fib[%d]: expected %d, got %v", i, exp, v)
		}
	}
}

// Test 14: Resume passes values back into yield
func TestVMCoroutineResumePassesValues(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {
	x := coroutine.yield("first")
	y := coroutine.yield("second")
	return x .. " " .. y
})
ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co, "hello")
ok3, v3 := coroutine.resume(co, "world")
`)
	expectGlobalString(t, g, "v1", "first")
	expectGlobalString(t, g, "v2", "second")
	expectGlobalString(t, g, "v3", "hello world")
}

func TestVMCoroutineYieldInsideNestedCall(t *testing.T) {
	g := compileAndRun(t, `
func helper() {
	x := coroutine.yield(10)
	return x + 1
}

co := coroutine.create(func() {
	y := helper()
	return y * 2
})

ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co, 20)
`)
	expectGlobalBool(t, g, "ok1", true)
	expectGlobalInt(t, g, "v1", 10)
	expectGlobalBool(t, g, "ok2", true)
	expectGlobalInt(t, g, "v2", 42)
}

// Test 15: Type function reports "coroutine"
func TestVMCoroutineType(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {})
tp := type(co)
`)
	expectGlobalString(t, g, "tp", "coroutine")
}

func TestVMCoroutineLeafNoCallReturn(t *testing.T) {
	g := compileAndRun(t, `
outside1 := coroutine.isyieldable()
co := coroutine.create(func(x) {
	return x * 2
})
ok, val := coroutine.resume(co, 21)
s := coroutine.status(co)
ok2, msg := coroutine.resume(co)
outside2 := coroutine.isyieldable()
`)
	expectGlobalBool(t, g, "outside1", false)
	expectGlobalBool(t, g, "ok", true)
	expectGlobalInt(t, g, "val", 42)
	expectGlobalString(t, g, "s", "dead")
	expectGlobalBool(t, g, "ok2", false)
	expectGlobalString(t, g, "msg", "cannot resume dead coroutine")
	expectGlobalBool(t, g, "outside2", false)
}

func TestVMCoroutineYieldableOutsideWhileSuspended(t *testing.T) {
	g := compileAndRun(t, `
co := coroutine.create(func() {
	coroutine.yield(1)
	return 2
})
ok, val := coroutine.resume(co)
outside := coroutine.isyieldable()
`)
	expectGlobalBool(t, g, "ok", true)
	expectGlobalInt(t, g, "val", 1)
	expectGlobalBool(t, g, "outside", false)
}

// Test 16: wrap with for-range using iterator function
func TestVMCoroutineWrapForRange(t *testing.T) {
	g := compileAndRun(t, `
func counter(n) {
	return coroutine.wrap(func() {
		for i := 1; i <= n; i++ {
			coroutine.yield(i)
		}
	})
}

sum := 0
for v := range counter(4) {
	sum = sum + v
}
`)
	expectGlobalInt(t, g, "sum", 10)
}

// ============================================================================
// Goroutine Tests
// ============================================================================

// Test basic go statement with shared globals
func TestGoStmtBasic(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
go func() {
	ch <- 42
}()
result := <-ch
`)
	expectGlobalInt(t, g, "result", 42)
}

// Test go with named function
func TestGoStmtNamedFunc(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
func compute() {
	ch <- 100
}
go compute()
result := <-ch
`)
	expectGlobalInt(t, g, "result", 100)
}

// Test go with arguments
func TestGoStmtWithArgs(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
func add(a, b) {
	ch <- a + b
}
go add(10, 20)
result := <-ch
`)
	expectGlobalInt(t, g, "result", 30)
}

// Test go with computation
func TestGoStmtComputation(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
go func() {
	sum := 0
	for i := 1; i <= 100; i++ {
		sum = sum + i
	}
	ch <- sum
}()
result := <-ch
`)
	expectGlobalInt(t, g, "result", 5050)
}

// Test multiple goroutines
func TestGoStmtMultiple(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 2)
go func() { ch <- 10 }()
go func() { ch <- 20 }()
r1 := <-ch
r2 := <-ch
total := r1 + r2
`)
	expectGlobalInt(t, g, "total", 30)
}

// Test go writes to shared table
func TestGoStmtSharedTable(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
state := {value: 0}
go func() {
	state.value = 99
	ch <- true
}()
<-ch
result := state.value
`)
	expectGlobalInt(t, g, "result", 99)
}

// ===== CHANNEL TESTS =====

func TestChannelMakeAndType(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan)
result := type(ch)
`)
	expectGlobalString(t, g, "result", "channel")
}

func TestChannelMakeBuffered(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 3)
result := type(ch)
`)
	expectGlobalString(t, g, "result", "channel")
}

func TestChannelSendRecvBuffered(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
ch <- 42
result := <-ch
`)
	expectGlobalInt(t, g, "result", 42)
}

func TestChannelSendRecvString(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
ch <- "hello"
result := <-ch
`)
	expectGlobalString(t, g, "result", "hello")
}

func TestChannelMultipleValues(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 3)
ch <- 10
ch <- 20
ch <- 30
a := <-ch
b := <-ch
c := <-ch
result := a + b + c
`)
	expectGlobalInt(t, g, "result", 60)
}

func TestChannelWithGoroutine(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan)
go func() {
    ch <- 42
}()
result := <-ch
`)
	expectGlobalInt(t, g, "result", 42)
}

func TestChannelGoroutineSync(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan)
go func() {
    ch <- 100
}()
result := <-ch
`)
	expectGlobalInt(t, g, "result", 100)
}

func TestChannelProducerConsumer(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 10)
go func() {
    for i := 1; i <= 10; i++ {
        ch <- i
    }
    close(ch)
}()
sum := 0
for v := range ch {
    sum = sum + v
}
result := sum
`)
	expectGlobalInt(t, g, "result", 55)
}

func TestChannelClose(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 2)
ch <- 1
ch <- 2
close(ch)
a := <-ch
b := <-ch
c := <-ch
result := a + b
closed := c == nil
`)
	expectGlobalInt(t, g, "result", 3)
	expectGlobalBool(t, g, "closed", true)
}

func TestChannelDirectionGoroutines(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan)

go func() {
    ch <- 1
    ch <- 2
    ch <- 3
    close(ch)
}()

sum := 0
for v := range ch {
    sum = sum + v
}
result := sum
`)
	expectGlobalInt(t, g, "result", 6)
}

func TestChannelPingPong(t *testing.T) {
	g := compileAndRun(t, `
ping := make(chan)
pong := make(chan)

go func() {
    for i := 1; i <= 5; i++ {
        v := <-ping
        pong <- v + 1
    }
}()

result := 0
for i := 1; i <= 5; i++ {
    ping <- i
    result = <-pong
}
`)
	expectGlobalInt(t, g, "result", 6)
}

func TestChannelRecvAsExpression(t *testing.T) {
	g := compileAndRun(t, `
ch := make(chan, 1)
ch <- 10
result := (<-ch) + 5
`)
	expectGlobalInt(t, g, "result", 15)
}

func TestChannelPassBetweenGoroutines(t *testing.T) {
	g := compileAndRun(t, `
ch1 := make(chan)
ch2 := make(chan)

go func() {
    v := <-ch1
    ch2 <- v * 2
}()

ch1 <- 21
result := <-ch2
`)
	expectGlobalInt(t, g, "result", 42)
}

// Ensure the test file is valid even if some helpers aren't wired up yet.
// This provides a graceful message.
func init() {
	// Verify Compile function exists (will fail at compile time if not)
	var _ func(*ast.Program) (*FuncProto, error) = Compile
}
