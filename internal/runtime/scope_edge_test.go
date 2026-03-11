package runtime

import (
	"testing"
)

// ==================================================================
// Scope edge cases (beyond what scope_test.go covers)
// ==================================================================

// --- Variable shadowing in deep nesting ---

func TestScopeDeepNesting(t *testing.T) {
	v := getGlobal(t, `
		x := 1
		if true {
			x := 2
			if true {
				x := 3
				if true {
					x := 4
				}
			}
		}
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

func TestScopeDeepAssignment(t *testing.T) {
	v := getGlobal(t, `
		x := 0
		if true {
			if true {
				if true {
					x = 42
				}
			}
		}
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// --- Varargs tests ---

func TestVarArgsPassing(t *testing.T) {
	v := getGlobal(t, `
		func sum(...) {
			s := 0
			for i := 1; i <= #...; i++ {
				s += ...[i]
			}
			return s
		}
		result := sum(10, 20, 30)
	`, "result")
	if !v.IsInt() || v.Int() != 60 {
		t.Errorf("expected 60, got %v", v)
	}
}

func TestVarArgsEmpty(t *testing.T) {
	v := getGlobal(t, `
		func count(...) {
			return #...
		}
		result := count()
	`, "result")
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

func TestVarArgsMixedWithNamed(t *testing.T) {
	v := getGlobal(t, `
		func f(a, b, ...) {
			return a + b + #...
		}
		result := f(10, 20, 30, 40, 50)
	`, "result")
	// a=10, b=20, ...={30,40,50}, #...=3, result=33
	if !v.IsInt() || v.Int() != 33 {
		t.Errorf("expected 33, got %v", v)
	}
}

// --- Multiple return capture ---

func TestMultiReturnSwap(t *testing.T) {
	interp := runProgram(t, `
		func swap(a, b) {
			return b, a
		}
		x, y := swap(10, 20)
	`)
	x := interp.GetGlobal("x")
	y := interp.GetGlobal("y")
	if x.Int() != 20 || y.Int() != 10 {
		t.Errorf("expected x=20,y=10, got x=%v,y=%v", x, y)
	}
}

func TestMultiReturnThreeValues(t *testing.T) {
	interp := runProgram(t, `
		func triple() {
			return 1, 2, 3
		}
		a, b, c := triple()
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != 1 || b.Int() != 2 || c.Int() != 3 {
		t.Errorf("expected 1,2,3 got %v,%v,%v", a, b, c)
	}
}

// --- Function scope interaction with closures ---

func TestClosureScopeIsolation(t *testing.T) {
	interp := runProgram(t, `
		func make(n) {
			return func() { return n }
		}
		f1 := make(10)
		f2 := make(20)
		r1 := f1()
		r2 := f2()
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if r1.Int() != 10 || r2.Int() != 20 {
		t.Errorf("expected r1=10,r2=20, got %v,%v", r1, r2)
	}
}

// --- Scope in for-while loops ---

func TestForWhileVarScope(t *testing.T) {
	v := getGlobal(t, `
		x := 0
		i := 0
		for i < 3 {
			x := i * 10
			i++
		}
		result := x
	`, "result")
	// x := inside for creates a new x each iteration, outer x stays 0
	// Actually for-while doesn't create new scope for condition vars
	// but the body is a block, so x := creates local
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected 0 (inner x doesn't affect outer), got %v", v)
	}
}

// --- Scope with multiple assignment swap ---

func TestScopeSwapViaMultiAssign(t *testing.T) {
	interp := runProgram(t, `
		a, b := 1, 2
		a, b = b, a
		c, d := a, b
	`)
	c := interp.GetGlobal("c")
	d := interp.GetGlobal("d")
	if c.Int() != 2 || d.Int() != 1 {
		t.Errorf("expected c=2,d=1, got %v,%v", c, d)
	}
}

func TestScopeThreeWaySwap(t *testing.T) {
	interp := runProgram(t, `
		a, b, c := 1, 2, 3
		a, b, c = c, a, b
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != 3 || b.Int() != 1 || c.Int() != 2 {
		t.Errorf("expected a=3,b=1,c=2, got %v,%v,%v", a, b, c)
	}
}

// --- Function returning function scope ---

func TestFuncReturningFunc(t *testing.T) {
	v := getGlobal(t, `
		func outer() {
			x := 10
			return func() {
				x = x + 1
				return x
			}
		}
		f := outer()
		f()
		f()
		result := f()
	`, "result")
	if !v.IsInt() || v.Int() != 13 {
		t.Errorf("expected 13, got %v", v)
	}
}

// --- Scope with nested for loops ---

func TestNestedForLoopScope(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				sum += 1
			}
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 9 {
		t.Errorf("expected 9, got %v", v)
	}
}

// --- Assignment in function to outer variable ---

func TestFuncAssignToOuterVar(t *testing.T) {
	// Assigning to a variable declared in outer scope modifies it
	interp := runProgram(t, `
		globalVar := 0
		func setGlobal() {
			globalVar = 42
		}
		setGlobal()
	`)
	v := interp.GetGlobal("globalVar")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestFuncUndeclaredVarIsLocal(t *testing.T) {
	// Assigning to undeclared var inside function creates a local in function env
	interp := runProgram(t, `
		func setLocal() {
			localVar = 42
		}
		setLocal()
	`)
	v := interp.GetGlobal("localVar")
	// localVar is not accessible at global scope
	if !v.IsNil() {
		t.Errorf("expected nil (local not visible), got %v", v)
	}
}

// --- Scope with for range and closures ---

func TestForRangeClosureCapture(t *testing.T) {
	interp := runProgram(t, `
		t := {100, 200, 300}
		getters := {}
		for k, v := range t {
			getters[k] = func() { return v }
		}
		r1 := getters[1]()
		r2 := getters[2]()
		r3 := getters[3]()
	`)
	if interp.GetGlobal("r1").Int() != 100 {
		t.Errorf("expected r1=100, got %v", interp.GetGlobal("r1"))
	}
	if interp.GetGlobal("r2").Int() != 200 {
		t.Errorf("expected r2=200, got %v", interp.GetGlobal("r2"))
	}
	if interp.GetGlobal("r3").Int() != 300 {
		t.Errorf("expected r3=300, got %v", interp.GetGlobal("r3"))
	}
}

// --- Declare same name in different blocks ---

func TestScopeSameNameDifferentBlocks(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		if true {
			x := 10
			result = result + x
		}
		if true {
			x := 20
			result = result + x
		}
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected 30, got %v", v)
	}
}
