package runtime

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

// helper: parse and execute source code, return the interpreter.
func runProgram(t *testing.T, src string) *Interpreter {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	interp := New()
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	return interp
}

// helper: run and expect an error.
func runProgramExpectError(t *testing.T, src string) error {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return err
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		return err
	}
	interp := New()
	return interp.Exec(prog)
}

// helper: get a global value from a run.
func getGlobal(t *testing.T, src string, name string) Value {
	t.Helper()
	interp := runProgram(t, src)
	return interp.GetGlobal(name)
}

// ==================================================================
// 1. Basic arithmetic
// ==================================================================

func TestArithmeticIntAdd(t *testing.T) {
	v := getGlobal(t, `result := 1 + 2`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected int 3, got %v", v)
	}
}

func TestArithmeticIntSub(t *testing.T) {
	v := getGlobal(t, `result := 10 - 3`, "result")
	if !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected int 7, got %v", v)
	}
}

func TestArithmeticIntMul(t *testing.T) {
	v := getGlobal(t, `result := 6 * 7`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestArithmeticIntDivExact(t *testing.T) {
	v := getGlobal(t, `result := 10 / 2`, "result")
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected int 5, got %v", v)
	}
}

func TestArithmeticIntDivFloat(t *testing.T) {
	v := getGlobal(t, `result := 10 / 3`, "result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %v (type %s)", v, v.TypeName())
	}
	expected := 10.0 / 3.0
	if v.Number() != expected {
		t.Errorf("expected %v, got %v", expected, v.Number())
	}
}

func TestArithmeticMod(t *testing.T) {
	v := getGlobal(t, `result := 10 % 3`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected int 1, got %v", v)
	}
}

func TestArithmeticPow(t *testing.T) {
	v := getGlobal(t, `result := 2 ** 10`, "result")
	if !v.IsInt() || v.Int() != 1024 {
		t.Errorf("expected int 1024, got %v", v)
	}
}

func TestArithmeticPrecedence(t *testing.T) {
	v := getGlobal(t, `result := 2 + 3 * 4`, "result")
	if !v.IsInt() || v.Int() != 14 {
		t.Errorf("expected int 14, got %v", v)
	}
}

func TestArithmeticFloatAdd(t *testing.T) {
	v := getGlobal(t, `result := 1.5 + 2.5`, "result")
	if !v.IsFloat() || v.Float() != 4.0 {
		t.Errorf("expected float 4.0, got %v", v)
	}
}

func TestArithmeticMixedIntFloat(t *testing.T) {
	v := getGlobal(t, `result := 1 + 2.5`, "result")
	if !v.IsFloat() || v.Float() != 3.5 {
		t.Errorf("expected float 3.5, got %v", v)
	}
}

func TestUnaryMinus(t *testing.T) {
	v := getGlobal(t, `result := -42`, "result")
	if !v.IsInt() || v.Int() != -42 {
		t.Errorf("expected int -42, got %v", v)
	}
}

func TestUnaryNot(t *testing.T) {
	v := getGlobal(t, `result := !true`, "result")
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false, got %v", v)
	}
}

// ==================================================================
// 2. Variable declaration and assignment
// ==================================================================

func TestDeclareAndAssign(t *testing.T) {
	v := getGlobal(t, `
		x := 10
		x = x + 5
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

func TestMultiDeclare(t *testing.T) {
	interp := runProgram(t, `a, b := 1, 2`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if !a.IsInt() || a.Int() != 1 {
		t.Errorf("expected a=1, got %v", a)
	}
	if !b.IsInt() || b.Int() != 2 {
		t.Errorf("expected b=2, got %v", b)
	}
}

func TestCompoundAssign(t *testing.T) {
	v := getGlobal(t, `
		x := 10
		x += 5
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

func TestIncDec(t *testing.T) {
	interp := runProgram(t, `
		a := 10
		a++
		b := 5
		b--
	`)
	if a := interp.GetGlobal("a"); !a.IsInt() || a.Int() != 11 {
		t.Errorf("expected a=11, got %v", a)
	}
	if b := interp.GetGlobal("b"); !b.IsInt() || b.Int() != 4 {
		t.Errorf("expected b=4, got %v", b)
	}
}

// ==================================================================
// 3. String operations
// ==================================================================

func TestStringConcat(t *testing.T) {
	v := getGlobal(t, `result := "hello" .. " " .. "world"`, "result")
	if !v.IsString() || v.Str() != "hello world" {
		t.Errorf("expected 'hello world', got %v", v)
	}
}

func TestStringLength(t *testing.T) {
	v := getGlobal(t, `result := #"hello"`, "result")
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected int 5, got %v", v)
	}
}

func TestStringNumberConcat(t *testing.T) {
	v := getGlobal(t, `result := "count: " .. 42`, "result")
	if !v.IsString() || v.Str() != "count: 42" {
		t.Errorf("expected 'count: 42', got %v", v)
	}
}

// ==================================================================
// 4. If/elseif/else
// ==================================================================

func TestIfTrue(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if true {
			result = 1
		}
	`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected int 1, got %v", v)
	}
}

func TestIfFalse(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if false {
			result = 1
		}
	`, "result")
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected int 0, got %v", v)
	}
}

func TestIfElse(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if false {
			result = 1
		} else {
			result = 2
		}
	`, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected int 2, got %v", v)
	}
}

func TestIfElseIf(t *testing.T) {
	v := getGlobal(t, `
		x := 15
		result := 0
		if x < 10 {
			result = 1
		} elseif x < 20 {
			result = 2
		} else {
			result = 3
		}
	`, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected int 2, got %v", v)
	}
}

// ==================================================================
// 5. For loops
// ==================================================================

func TestForWhile(t *testing.T) {
	v := getGlobal(t, `
		i := 0
		sum := 0
		for i < 5 {
			sum += i
			i++
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected int 10 (0+1+2+3+4), got %v", v)
	}
}

func TestForNumCStyle(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 0; i < 5; i++ {
			sum += i
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected int 10, got %v", v)
	}
}

func TestForBreak(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 0; i < 100; i++ {
			if i >= 5 {
				break
			}
			sum += i
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected int 10, got %v", v)
	}
}

func TestForContinue(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 0; i < 10; i++ {
			if i % 2 == 0 {
				continue
			}
			sum += i
		}
		result := sum
	`, "result")
	// 1+3+5+7+9 = 25
	if !v.IsInt() || v.Int() != 25 {
		t.Errorf("expected int 25, got %v", v)
	}
}

func TestForRange(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20, 30}
		sum := 0
		for k, v := range t {
			sum += v
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 60 {
		t.Errorf("expected int 60, got %v", v)
	}
}

// ==================================================================
// 6. Functions (basic)
// ==================================================================

func TestFuncDecl(t *testing.T) {
	v := getGlobal(t, `
		func add(a, b) {
			return a + b
		}
		result := add(3, 4)
	`, "result")
	if !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected int 7, got %v", v)
	}
}

func TestFuncLiteral(t *testing.T) {
	v := getGlobal(t, `
		mul := func(a, b) {
			return a * b
		}
		result := mul(6, 7)
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestFuncNoReturn(t *testing.T) {
	v := getGlobal(t, `
		func noop() {
		}
		result := noop()
	`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

// ==================================================================
// 7. Multiple return values
// ==================================================================

func TestMultipleReturn(t *testing.T) {
	interp := runProgram(t, `
		func swap(a, b) {
			return b, a
		}
		x, y := swap(1, 2)
	`)
	x := interp.GetGlobal("x")
	y := interp.GetGlobal("y")
	if !x.IsInt() || x.Int() != 2 {
		t.Errorf("expected x=2, got %v", x)
	}
	if !y.IsInt() || y.Int() != 1 {
		t.Errorf("expected y=1, got %v", y)
	}
}

func TestMultiReturnExpansionAsLastArg(t *testing.T) {
	v := getGlobal(t, `
		func pair() {
			return 10, 20
		}
		func add(a, b) {
			return a + b
		}
		result := add(pair())
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected int 30, got %v", v)
	}
}

func TestMultiReturnTruncated(t *testing.T) {
	v := getGlobal(t, `
		func pair() {
			return 10, 20
		}
		result := pair() + 5
	`, "result")
	// pair() in non-last position => only first value (10)
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

// ==================================================================
// 8. Nested scopes
// ==================================================================

func TestNestedScope(t *testing.T) {
	v := getGlobal(t, `
		x := 1
		if true {
			x := 2
			if true {
				x := 3
			}
		}
		result := x
	`, "result")
	// Inner x := 2 and x := 3 should not affect outer x
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected int 1 (scoping), got %v", v)
	}
}

func TestScopeAssignment(t *testing.T) {
	v := getGlobal(t, `
		x := 1
		if true {
			x = 10
		}
		result := x
	`, "result")
	// Assignment (not declaration) should modify outer x
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected int 10, got %v", v)
	}
}

// ==================================================================
// 9. Recursive functions
// ==================================================================

func TestFibonacci(t *testing.T) {
	v := getGlobal(t, `
		func fib(n) {
			if n <= 1 {
				return n
			}
			return fib(n - 1) + fib(n - 2)
		}
		result := fib(10)
	`, "result")
	if !v.IsInt() || v.Int() != 55 {
		t.Errorf("expected int 55 (fib(10)), got %v", v)
	}
}

func TestFactorial(t *testing.T) {
	v := getGlobal(t, `
		func fact(n) {
			if n <= 1 {
				return 1
			}
			return n * fact(n - 1)
		}
		result := fact(10)
	`, "result")
	if !v.IsInt() || v.Int() != 3628800 {
		t.Errorf("expected int 3628800 (10!), got %v", v)
	}
}

// ==================================================================
// 10. Table creation and access
// ==================================================================

func TestTableLiteral(t *testing.T) {
	interp := runProgram(t, `
		t := {x: 10, y: 20}
		rx := t.x
		ry := t["y"]
	`)
	rx := interp.GetGlobal("rx")
	ry := interp.GetGlobal("ry")
	if !rx.IsInt() || rx.Int() != 10 {
		t.Errorf("expected rx=10, got %v", rx)
	}
	if !ry.IsInt() || ry.Int() != 20 {
		t.Errorf("expected ry=20, got %v", ry)
	}
}

func TestTableFieldAssign(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		t.x = 42
		result := t.x
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestTableIndexAssign(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		t["key"] = "value"
		result := t["key"]
	`, "result")
	if !v.IsString() || v.Str() != "value" {
		t.Errorf("expected 'value', got %v", v)
	}
}

// ==================================================================
// 11. Table with integer keys (array-style)
// ==================================================================

func TestTableArray(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20, 30, 40, 50}
		result := t[3]
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected int 30, got %v", v)
	}
}

func TestTableLength(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20, 30}
		result := #t
	`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected int 3, got %v", v)
	}
}

func TestTableMixed(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, name: "test", 30}
		a := t[1]
		b := t[2]
		c := t[3]
		n := t.name
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	n := interp.GetGlobal("n")
	if !a.IsInt() || a.Int() != 10 {
		t.Errorf("expected a=10, got %v", a)
	}
	if !b.IsInt() || b.Int() != 20 {
		t.Errorf("expected b=20, got %v", b)
	}
	if !c.IsInt() || c.Int() != 30 {
		t.Errorf("expected c=30, got %v", c)
	}
	if !n.IsString() || n.Str() != "test" {
		t.Errorf("expected n='test', got %v", n)
	}
}

// ==================================================================
// 12. Error cases
// ==================================================================

func TestUndefinedVariable(t *testing.T) {
	err := runProgramExpectError(t, `result := x`)
	if err == nil {
		t.Fatal("expected error for undefined variable")
	}
	if !strings.Contains(err.Error(), "undefined") {
		t.Errorf("expected 'undefined' in error, got: %v", err)
	}
}

func TestTypeErrorArithmetic(t *testing.T) {
	err := runProgramExpectError(t, `result := "hello" + 1`)
	if err == nil {
		t.Fatal("expected error for string + int")
	}
}

func TestTypeErrorCallNonFunction(t *testing.T) {
	err := runProgramExpectError(t, `
		x := 42
		x()
	`)
	if err == nil {
		t.Fatal("expected error for calling non-function")
	}
	if !strings.Contains(err.Error(), "call") {
		t.Errorf("expected 'call' in error, got: %v", err)
	}
}

func TestDivisionByZero(t *testing.T) {
	err := runProgramExpectError(t, `result := 1 / 0`)
	if err == nil {
		t.Fatal("expected error for division by zero")
	}
}

func TestIndexNonTable(t *testing.T) {
	err := runProgramExpectError(t, `
		x := 42
		result := x.field
	`)
	if err == nil {
		t.Fatal("expected error for indexing non-table")
	}
}

// ==================================================================
// Additional tests: comparison, logic, print, etc.
// ==================================================================

func TestComparison(t *testing.T) {
	tests := []struct {
		src    string
		expect bool
	}{
		{`result := 1 < 2`, true},
		{`result := 2 < 1`, false},
		{`result := 1 <= 1`, true},
		{`result := 1 > 2`, false},
		{`result := 2 > 1`, true},
		{`result := 2 >= 2`, true},
		{`result := 1 == 1`, true},
		{`result := 1 != 2`, true},
		{`result := "a" < "b"`, true},
	}
	for _, tt := range tests {
		v := getGlobal(t, tt.src, "result")
		if !v.IsBool() || v.Bool() != tt.expect {
			t.Errorf("%s: expected %v, got %v", tt.src, tt.expect, v)
		}
	}
}

func TestLogicShortCircuit(t *testing.T) {
	// && returns first falsy or last truthy
	v := getGlobal(t, `result := true && 42`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}

	v = getGlobal(t, `result := false && 42`, "result")
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false, got %v", v)
	}

	// || returns first truthy or last falsy
	v = getGlobal(t, `result := nil || 42`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}

	v = getGlobal(t, `result := 1 || 42`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

func TestNilTruthiness(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if nil {
			result = 1
		}
	`, "result")
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("nil should be falsy, got %v", v)
	}
}

func TestZeroIsTruthy(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if 0 {
			result = 1
		}
	`, "result")
	// Unlike Lua where 0 is truthy, our design follows Lua: only nil and false are falsy
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("0 should be truthy (Lua semantics), got %v", v)
	}
}

func TestPrintCapture(t *testing.T) {
	interp := runProgram(t, `print("hello", "world")`)
	output := interp.Output()
	if len(output) != 1 || output[0] != "hello\tworld" {
		t.Errorf("expected print output 'hello\\tworld', got %v", output)
	}
}

func TestTypeFunction(t *testing.T) {
	v := getGlobal(t, `result := type(42)`, "result")
	if !v.IsString() || v.Str() != "number" {
		t.Errorf("expected 'number', got %v", v)
	}

	v = getGlobal(t, `result := type("hello")`, "result")
	if !v.IsString() || v.Str() != "string" {
		t.Errorf("expected 'string', got %v", v)
	}

	v = getGlobal(t, `result := type(nil)`, "result")
	if !v.IsString() || v.Str() != "nil" {
		t.Errorf("expected 'nil', got %v", v)
	}
}

func TestForRangeKeys(t *testing.T) {
	// Test that for-range captures keys
	v := getGlobal(t, `
		t := {10, 20, 30}
		sum := 0
		for k := range t {
			sum += k
		}
		result := sum
	`, "result")
	// Keys are 1, 2, 3 => sum = 6
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected int 6, got %v", v)
	}
}

func TestVarArgs(t *testing.T) {
	v := getGlobal(t, `
		func sum(...) {
			s := 0
			for i := 1; i <= #...; i++ {
				s += ...[i]
			}
			return s
		}
		result := sum(1, 2, 3, 4, 5)
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

func TestNestedFunctionCalls(t *testing.T) {
	v := getGlobal(t, `
		func double(x) {
			return x * 2
		}
		func triple(x) {
			return x * 3
		}
		result := double(triple(5))
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected int 30, got %v", v)
	}
}

func TestTableNestedAccess(t *testing.T) {
	v := getGlobal(t, `
		t := {inner: {value: 42}}
		result := t.inner.value
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestFuncAsValue(t *testing.T) {
	v := getGlobal(t, `
		func apply(f, x) {
			return f(x)
		}
		func double(x) {
			return x * 2
		}
		result := apply(double, 21)
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestEqualityNil(t *testing.T) {
	v := getGlobal(t, `result := nil == nil`, "result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}


func TestInfiniteLoopBreak(t *testing.T) {
	v := getGlobal(t, `
		count := 0
		for {
			count++
			if count >= 10 {
				break
			}
		}
		result := count
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected int 10, got %v", v)
	}
}

// ==================================================================
// Closure/Upvalue tests
// ==================================================================

// Test 1: Basic closure captures variable (counter pattern)
func TestClosureCapture(t *testing.T) {
	interp := runProgram(t, `
		func makeCounter() {
			n := 0
			return func() {
				n = n + 1
				return n
			}
		}
		c := makeCounter()
		r1 := c()
		r2 := c()
		r3 := c()
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	r3 := interp.GetGlobal("r3")
	if !r1.IsInt() || r1.Int() != 1 {
		t.Errorf("expected r1=1, got %v", r1)
	}
	if !r2.IsInt() || r2.Int() != 2 {
		t.Errorf("expected r2=2, got %v", r2)
	}
	if !r3.IsInt() || r3.Int() != 3 {
		t.Errorf("expected r3=3, got %v", r3)
	}
}

// Test 2: Shared upvalue between two closures
func TestClosureSharedUpvalue(t *testing.T) {
	v := getGlobal(t, `
		func makePair() {
			x := 0
			inc := func() { x = x + 1 }
			get := func() { return x }
			return inc, get
		}
		inc, get := makePair()
		inc()
		inc()
		result := get()
	`, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected result=2, got %v", v)
	}
}

// Test 3: Closure captures loop variable (each iteration creates new scope)
func TestClosureInLoop(t *testing.T) {
	interp := runProgram(t, `
		funcs := {}
		for i := 1; i <= 3; i++ {
			ii := i
			funcs[i] = func() { return ii }
		}
		r1 := funcs[1]()
		r2 := funcs[2]()
		r3 := funcs[3]()
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	r3 := interp.GetGlobal("r3")
	if !r1.IsInt() || r1.Int() != 1 {
		t.Errorf("expected r1=1, got %v", r1)
	}
	if !r2.IsInt() || r2.Int() != 2 {
		t.Errorf("expected r2=2, got %v", r2)
	}
	if !r3.IsInt() || r3.Int() != 3 {
		t.Errorf("expected r3=3, got %v", r3)
	}
}

// Test 4: Deeply nested closures
func TestClosureNestedDeep(t *testing.T) {
	v := getGlobal(t, `
		func outer() {
			x := 10
			func middle() {
				y := 20
				return func() {
					return x + y
				}
			}
			return middle()
		}
		f := outer()
		result := f()
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected result=30, got %v", v)
	}
}

// Test 5: Two closures from the same scope share upvalue (global scope)
func TestClosureMutualGlobal(t *testing.T) {
	v := getGlobal(t, `
		a := 0
		inc := func() { a = a + 1 }
		get := func() { return a }
		inc()
		inc()
		result := get()
	`, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected result=2, got %v", v)
	}
}

// Test 6: Recursive closure via named function
func TestClosureRecursiveFib(t *testing.T) {
	v := getGlobal(t, `
		func fib(n) {
			if n < 2 {
				return n
			}
			return fib(n-1) + fib(n-2)
		}
		result := fib(10)
	`, "result")
	if !v.IsInt() || v.Int() != 55 {
		t.Errorf("expected result=55, got %v", v)
	}
}

// Test 7: Closure captures variable that changes after closure creation
func TestClosureLateBinding(t *testing.T) {
	v := getGlobal(t, `
		x := 10
		f := func() { return x }
		x = 20
		result := f()
	`, "result")
	// Closure captures reference, not value -- should see updated x
	if !v.IsInt() || v.Int() != 20 {
		t.Errorf("expected result=20, got %v", v)
	}
}

// Test 8: Adder factory (closure over parameter)
func TestClosureOverParam(t *testing.T) {
	v := getGlobal(t, `
		func makeAdder(n) {
			return func(x) { return x + n }
		}
		add5 := makeAdder(5)
		result := add5(10)
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected result=15, got %v", v)
	}
}

// Test 9: Multiple independent counters
func TestClosureIndependentCounters(t *testing.T) {
	interp := runProgram(t, `
		func makeCounter() {
			n := 0
			return func() {
				n = n + 1
				return n
			}
		}
		c1 := makeCounter()
		c2 := makeCounter()
		c1()
		c1()
		c1()
		r1 := c1()
		r2 := c2()
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if !r1.IsInt() || r1.Int() != 4 {
		t.Errorf("expected r1=4, got %v", r1)
	}
	if !r2.IsInt() || r2.Int() != 1 {
		t.Errorf("expected r2=1, got %v", r2)
	}
}

// Test 10: Closure with varargs
func TestClosureSimpleArgs(t *testing.T) {
	v := getGlobal(t, `
		func sum(a, b, c) {
			return a + b + c
		}
		result := sum(1, 2, 3)
	`, "result")
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected result=6, got %v", v)
	}
}
