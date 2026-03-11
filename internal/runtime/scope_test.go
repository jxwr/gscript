package runtime

import (
	"testing"
)

// ==================================================================
// Scope and variable tests
// ==================================================================

// --- Local vs global scope ---

func TestScopeLocalShadowsGlobal(t *testing.T) {
	v := getGlobal(t, `
		x := 1
		if true {
			x := 2
		}
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1 (inner := should not affect outer), got %v", v)
	}
}

func TestScopeAssignModifiesOuter(t *testing.T) {
	v := getGlobal(t, `
		x := 1
		if true {
			x = 10
		}
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected 10 (assignment should modify outer), got %v", v)
	}
}

func TestScopeNestedShadow(t *testing.T) {
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
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

// --- Multiple assignment ---

func TestMultiAssignBasic(t *testing.T) {
	interp := runProgram(t, `a, b, c := 1, 2, 3`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != 1 || b.Int() != 2 || c.Int() != 3 {
		t.Errorf("expected 1,2,3 got %v,%v,%v", a, b, c)
	}
}

func TestMultiAssignFewerValues(t *testing.T) {
	interp := runProgram(t, `a, b := 1`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if !a.IsInt() || a.Int() != 1 {
		t.Errorf("expected a=1, got %v", a)
	}
	if !b.IsNil() {
		t.Errorf("expected b=nil, got %v", b)
	}
}

func TestMultiAssignMoreValues(t *testing.T) {
	interp := runProgram(t, `a := 1, 2, 3`)
	a := interp.GetGlobal("a")
	if !a.IsInt() || a.Int() != 1 {
		t.Errorf("expected a=1, got %v", a)
	}
}

func TestMultiAssignSwap(t *testing.T) {
	interp := runProgram(t, `
		a, b := 1, 2
		a, b = b, a
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if a.Int() != 2 || b.Int() != 1 {
		t.Errorf("expected a=2,b=1 got %v,%v", a, b)
	}
}

// --- Function params shadow outer ---

func TestFuncParamShadow(t *testing.T) {
	v := getGlobal(t, `
		x := 100
		func f(x) {
			return x
		}
		result := f(42)
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestFuncParamDoesNotAffectOuter(t *testing.T) {
	interp := runProgram(t, `
		x := 100
		func f(x) {
			x = 999
		}
		f(42)
	`)
	x := interp.GetGlobal("x")
	if !x.IsInt() || x.Int() != 100 {
		t.Errorf("expected x=100 (unchanged), got %v", x)
	}
}

// --- For loop variable scope ---

func TestForLoopVarScope(t *testing.T) {
	// The loop variable i should be scoped to the for loop
	interp := runProgram(t, `
		result := 0
		for i := 0; i < 5; i++ {
			result = result + i
		}
	`)
	result := interp.GetGlobal("result")
	if result.Int() != 10 {
		t.Errorf("expected 10, got %v", result)
	}
}

// --- Declare in if branch scope ---

func TestIfBranchScope(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if true {
			x := 42
			result = x
		}
		result2 := result
	`, "result2")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// --- Nested function scopes ---

func TestNestedFuncScopes(t *testing.T) {
	v := getGlobal(t, `
		x := 10
		func outer() {
			y := 20
			func inner() {
				return x + y
			}
			return inner()
		}
		result := outer()
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected 30, got %v", v)
	}
}

func TestScopeChainedAssignment(t *testing.T) {
	v := getGlobal(t, `
		x := 1
		func f() {
			x = x + 1
		}
		f()
		f()
		f()
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 4 {
		t.Errorf("expected 4, got %v", v)
	}
}

// --- For range creates new scope per iteration ---

func TestForRangeNewScope(t *testing.T) {
	interp := runProgram(t, `
		funcs := {}
		t := {10, 20, 30}
		for k, v := range t {
			funcs[k] = func() { return v }
		}
		r1 := funcs[1]()
		r2 := funcs[2]()
		r3 := funcs[3]()
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	r3 := interp.GetGlobal("r3")
	if r1.Int() != 10 {
		t.Errorf("expected r1=10, got %v", r1)
	}
	if r2.Int() != 20 {
		t.Errorf("expected r2=20, got %v", r2)
	}
	if r3.Int() != 30 {
		t.Errorf("expected r3=30, got %v", r3)
	}
}
