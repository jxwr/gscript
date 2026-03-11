package runtime

import (
	"testing"
)

// ==================================================================
// Multiple return value edge cases
// ==================================================================

func TestMultiReturnSingleAssignment(t *testing.T) {
	// Single value assigned from multi-return: only first value
	v := getGlobal(t, `
		func f() { return 10, 20, 30 }
		a := f()
		result := a
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected 10, got %v", v)
	}
}

func TestMultiReturnInExpression(t *testing.T) {
	// Multi-value in expression: only first value used
	v := getGlobal(t, `
		func f() { return 10, 20 }
		result := f() + 1
	`, "result")
	if !v.IsInt() || v.Int() != 11 {
		t.Errorf("expected 11, got %v", v)
	}
}

func TestMultiReturnExpandAsLastArg(t *testing.T) {
	// Multi-value as last arg: f() expands
	v := getGlobal(t, `
		func pair() { return 10, 20 }
		func add(a, b) { return a + b }
		result := add(pair())
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected 30, got %v", v)
	}
}

func TestMultiReturnTruncatedNotLastArg(t *testing.T) {
	// Multi-value NOT as last arg: f() truncated to 1
	v := getGlobal(t, `
		func pair() { return 10, 20 }
		func f(a, b) { return a + b }
		result := f(pair(), 5)
	`, "result")
	// pair() is not the last arg, so it's truncated to 10
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

func TestMultiReturnExpandInDeclaration(t *testing.T) {
	interp := runProgram(t, `
		func f() { return 1, 2, 3 }
		a, b, c := f()
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != 1 || b.Int() != 2 || c.Int() != 3 {
		t.Errorf("expected 1,2,3 got %v,%v,%v", a, b, c)
	}
}

func TestMultiReturnPartialCapture(t *testing.T) {
	// More returns than variables: extras discarded
	interp := runProgram(t, `
		func f() { return 1, 2, 3 }
		a, b := f()
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if a.Int() != 1 || b.Int() != 2 {
		t.Errorf("expected 1,2 got %v,%v", a, b)
	}
}

func TestMultiReturnFewerReturns(t *testing.T) {
	// Fewer returns than variables: remaining are nil
	interp := runProgram(t, `
		func f() { return 1 }
		a, b := f()
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if a.Int() != 1 {
		t.Errorf("expected a=1, got %v", a)
	}
	if !b.IsNil() {
		t.Errorf("expected b=nil, got %v", b)
	}
}

func TestReturnExpandsAll(t *testing.T) {
	// return f() expands all
	v := getGlobal(t, `
		func inner() { return 10, 20 }
		func outer() { return inner() }
		a, b := outer()
		result := a + b
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected 30, got %v", v)
	}
}

func TestNestedMultiReturnCalls(t *testing.T) {
	// f(g()) where g returns 3 values
	v := getGlobal(t, `
		func g() { return 1, 2, 3 }
		func f(a, b, c) { return a + b + c }
		result := f(g())
	`, "result")
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected 6, got %v", v)
	}
}

func TestMultiReturnInTableConstructor(t *testing.T) {
	// {f()} should expand the return values in the table
	interp := runProgram(t, `
		func f() { return 10, 20, 30 }
		t := {f()}
	`)
	tbl := interp.GetGlobal("t").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected length 3, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Int() != 10 {
		t.Errorf("expected t[1]=10, got %v", tbl.RawGet(IntValue(1)))
	}
	if tbl.RawGet(IntValue(2)).Int() != 20 {
		t.Errorf("expected t[2]=20, got %v", tbl.RawGet(IntValue(2)))
	}
	if tbl.RawGet(IntValue(3)).Int() != 30 {
		t.Errorf("expected t[3]=30, got %v", tbl.RawGet(IntValue(3)))
	}
}

func TestMultiReturnNoReturn(t *testing.T) {
	// Function with no return statement returns nil
	v := getGlobal(t, `
		func f() {}
		result := f()
	`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}
