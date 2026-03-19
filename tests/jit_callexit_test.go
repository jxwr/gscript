//go:build darwin && arm64

package tests_test

import (
	"testing"

	gs "github.com/gscript/gscript/gscript"
)

// TestCallExit_LoopWithExternalCall tests a for loop that calls an external function
// on each iteration. The JIT should call-exit at each CALL, execute it in Go,
// then re-enter JIT for the loop arithmetic.
func TestCallExit_LoopWithExternalCall(t *testing.T) {
	src := `
func double(x) {
    return x * 2
}

func sumDoubles(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + double(i)
    }
    return s
}
result := sumDoubles(100)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum of 2*i for i=1..100 = 2 * 5050 = 10100
	expected := int64(10100)
	if vmResult != expected {
		t.Errorf("VM sumDoubles(100): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT sumDoubles(100): got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestCallExit_MultipleCallsInLoop tests multiple external calls within a single loop iteration.
func TestCallExit_MultipleCallsInLoop(t *testing.T) {
	src := `
func add(a, b) { return a + b }
func mul(a, b) { return a * b }

func compute(n) {
    s := 0
    for i := 1; i <= n; i++ {
        a := add(i, i)
        b := mul(i, 2)
        s = s + a + b
    }
    return s
}
result := compute(50)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// a = 2*i, b = 2*i, sum of 4*i for i=1..50 = 4 * 1275 = 5100
	expected := int64(5100)
	if vmResult != expected {
		t.Errorf("VM compute(50): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT compute(50): got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestCallExit_NestedJIT tests that the called function is also JIT-compiled.
func TestCallExit_NestedJIT(t *testing.T) {
	src := `
func innerLoop(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}

func outer(n) {
    total := 0
    for i := 1; i <= n; i++ {
        total = total + innerLoop(i)
    }
    return total
}
result := outer(50)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
	// sum of sum(1..i) for i=1..50 = sum of i*(i+1)/2 = n*(n+1)*(n+2)/6
	// = 50*51*52/6 = 22100
	expected := int64(22100)
	if jitResult != expected {
		t.Errorf("JIT outer(50): got %v, want %d", jitResult, expected)
	}
}

// TestCallExit_GetGlobal tests that GETGLOBAL call-exit works correctly.
func TestCallExit_GetGlobal(t *testing.T) {
	src := `
myval := 42

func readGlobal() {
    s := 0
    for i := 1; i <= 10; i++ {
        s = s + myval
    }
    return s
}
result := readGlobal()
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(420)
	if vmResult != expected {
		t.Errorf("VM readGlobal: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT readGlobal: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestCallExit_SetGlobal tests that SETGLOBAL call-exit works correctly.
func TestCallExit_SetGlobal(t *testing.T) {
	src := `
counter := 0

func increment(n) {
    for i := 1; i <= n; i++ {
        counter = counter + 1
    }
}
increment(100)
result := counter
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(100)
	if vmResult != expected {
		t.Errorf("VM increment: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT increment: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestCallExit_FunctionWithoutLoop tests that functions with external calls but
// no loops are now compiled (shouldCompile change).
func TestCallExit_FunctionWithoutLoop(t *testing.T) {
	src := `
func square(x) { return x * x }
func sumOfSquares(a, b, c) {
    return square(a) + square(b) + square(c)
}
result := sumOfSquares(3, 4, 5)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(50)
	if vmResult != expected {
		t.Errorf("VM sumOfSquares: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT sumOfSquares: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestCallExit_GoFunction tests calling a Go-implemented function from JIT code.
func TestCallExit_GoFunction(t *testing.T) {
	src := `
func sumAbs(n) {
    s := 0
    for i := -n; i <= n; i++ {
        if i < 0 {
            s = s + (-i)
        } else {
            s = s + i
        }
    }
    return s
}
result := sumAbs(50)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum of |i| for i=-50..50 = 2 * sum(1..50) = 2 * 1275 = 2550
	expected := int64(2550)
	if vmResult != expected {
		t.Errorf("VM sumAbs: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT sumAbs: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestMethodJIT_SettableNative tests SETTABLE native fast path in method JIT.
// The JIT should compile table[int_key] = value natively without call-exit
// for integer keys within the array part.
func TestMethodJIT_SettableNative(t *testing.T) {
	src := `
func fillTable(n) {
    t := {}
    for i := 1; i <= n; i++ {
        t[i] = i * 2
    }
    return t[100]
}
result := fillTable(1000)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(200)
	if vmResult != expected {
		t.Errorf("VM fillTable(1000): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT fillTable(1000): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestMethodJIT_SettableNativeReadBack tests SETTABLE followed by GETTABLE
// to verify the native fast path writes correct values that can be read back.
func TestMethodJIT_SettableNativeReadBack(t *testing.T) {
	src := `
func buildAndSum(n) {
    t := {}
    for i := 1; i <= n; i++ {
        t[i] = i
    }
    s := 0
    for i := 1; i <= n; i++ {
        s = s + t[i]
    }
    return s
}
result := buildAndSum(100)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum 1..100 = 5050
	expected := int64(5050)
	if vmResult != expected {
		t.Errorf("VM buildAndSum(100): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT buildAndSum(100): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestMethodJIT_SettableNativeOverwrite tests that SETTABLE correctly overwrites
// existing array elements.
func TestMethodJIT_SettableNativeOverwrite(t *testing.T) {
	src := `
func overwrite(n) {
    t := {}
    for i := 1; i <= n; i++ {
        t[i] = i
    }
    // Overwrite all values
    for i := 1; i <= n; i++ {
        t[i] = t[i] * 3
    }
    return t[50]
}
result := overwrite(100)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(150) // 50 * 3
	if vmResult != expected {
		t.Errorf("VM overwrite(100): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT overwrite(100): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestMethodJIT_SettableConstKey tests SETTABLE with a constant integer key (RK(B) is constant).
func TestMethodJIT_SettableConstKey(t *testing.T) {
	src := `
func setConst(n) {
    t := {}
    // t[1] uses a constant key
    for i := 1; i <= n; i++ {
        t[1] = i
    }
    return t[1]
}
result := setConst(100)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(100) // last write wins
	if vmResult != expected {
		t.Errorf("VM setConst(100): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT setConst(100): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestCallExit_SelfCallWithExternalCall tests a function that has both
// self-recursive calls and external calls.
func TestCallExit_SelfCallWithExternalCall(t *testing.T) {
	src := `
func helper(x) { return x + 1 }

func recur(n) {
    if n <= 0 { return 0 }
    return helper(n) + recur(n - 1)
}
result := recur(10)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// helper(n) = n+1, so sum of (i+1) for i=1..10 = sum(2..11) = 65
	expected := int64(65)
	if vmResult != expected {
		t.Errorf("VM recur(10): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT recur(10): got %v (%T), want %d", jitResult, jitResult, expected)
	}
}
