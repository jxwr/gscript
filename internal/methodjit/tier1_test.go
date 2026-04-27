//go:build darwin && arm64

// tier1_test.go tests the Tier 1 baseline compiler.
// Each test compiles a GScript program, runs it via both the VM interpreter
// and the baseline JIT engine, and compares the results.

package methodjit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runVMFull executes a full program via the VM and returns globals.
func runVMFull(t *testing.T, src string) map[string]runtime.Value {
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
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	_, err = v.Execute(proto)
	if err != nil {
		t.Fatalf("VM runtime error: %v", err)
	}
	return v.Globals()
}

// runVMFullWithJIT executes a full program with the baseline JIT enabled.
func runVMFullWithJIT(t *testing.T, src string) map[string]runtime.Value {
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
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)
	_, err = v.Execute(proto)
	if err != nil {
		t.Fatalf("JIT runtime error: %v", err)
	}
	return v.Globals()
}

// assertValueEq checks that two values are equal.
func assertValueEq(t *testing.T, name string, got, want runtime.Value) {
	t.Helper()
	if uint64(got) == uint64(want) {
		return
	}
	// For floats, compare numerically
	if got.IsFloat() && want.IsFloat() {
		if got.Float() == want.Float() {
			return
		}
	}
	// For strings, compare string content
	if got.IsString() && want.IsString() {
		if got.Str() == want.Str() {
			return
		}
	}
	t.Errorf("%s: got %v (%s), want %v (%s)", name, got, got.TypeName(), want, want.TypeName())
}

// compareVMvsJIT runs a program with both VM and baseline JIT and compares
// the named global variable.
func compareVMvsJIT(t *testing.T, src, globalName string) {
	t.Helper()
	vmGlobals := runVMFull(t, src)
	jitGlobals := runVMFullWithJIT(t, src)

	vmResult, vmOk := vmGlobals[globalName]
	jitResult, jitOk := jitGlobals[globalName]

	if !vmOk {
		t.Fatalf("VM did not produce global %q", globalName)
	}
	if !jitOk {
		t.Fatalf("JIT did not produce global %q", globalName)
	}

	assertValueEq(t, globalName, jitResult, vmResult)
}

func runTier1ProgramForTest(t *testing.T, src string) (*vm.VM, *vm.FuncProto) {
	t.Helper()
	proto := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)
	if _, err := v.Execute(proto); err != nil {
		v.Close()
		t.Fatalf("JIT runtime error: %v", err)
	}
	return v, proto
}

// ---------------------------------------------------------------------------
// 1. Constants: LOADINT, LOADK, LOADBOOL, LOADNIL
// ---------------------------------------------------------------------------

func TestTier1_LoadInt(t *testing.T) {
	compareVMvsJIT(t, `
func f() { return 42 }
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_LoadBool(t *testing.T) {
	compareVMvsJIT(t, `
func f() { return true }
result := false
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_LoadNil(t *testing.T) {
	compareVMvsJIT(t, `
func f() { a := nil; return a }
result := 42
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// 2. Arithmetic: ADD, SUB, MUL, DIV, MOD, UNM, NOT
// ---------------------------------------------------------------------------

func TestTier1_Add(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a + b }
result := 0
for i := 1; i <= 200; i++ { result = f(3, 4) }
`, "result")
}

func TestTier1_Sub(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a - b }
result := 0
for i := 1; i <= 200; i++ { result = f(10, 3) }
`, "result")
}

func TestTier1_Mul(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a * b }
result := 0
for i := 1; i <= 200; i++ { result = f(6, 7) }
`, "result")
}

func TestTier1_Div(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a / b }
result := 0
for i := 1; i <= 200; i++ { result = f(10, 3) }
`, "result")
}

func TestTier1_Mod(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a % b }
result := 0
for i := 1; i <= 200; i++ { result = f(10, 3) }
`, "result")
}

func TestTier1_UnaryMinus(t *testing.T) {
	compareVMvsJIT(t, `
func f(a) { return -a }
result := 0
for i := 1; i <= 200; i++ { result = f(42) }
`, "result")
}

func TestTier1_Not(t *testing.T) {
	compareVMvsJIT(t, `
func f(a) { return !a }
result := true
for i := 1; i <= 200; i++ { result = f(true) }
`, "result")
}

// ---------------------------------------------------------------------------
// 3. Comparison: EQ, LT, LE + TEST
// ---------------------------------------------------------------------------

func TestTier1_LT(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { if a < b { return 1 } else { return 0 } }
result := 0
for i := 1; i <= 200; i++ { result = f(3, 5) }
`, "result")
}

func TestTier1_LE(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { if a <= b { return 1 } else { return 0 } }
result := 0
for i := 1; i <= 200; i++ { result = f(5, 5) }
`, "result")
}

func TestTier1_EQ(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { if a == b { return 1 } else { return 0 } }
result := 0
for i := 1; i <= 200; i++ { result = f(3, 3) }
`, "result")
}

func TestTier1_Test(t *testing.T) {
	compareVMvsJIT(t, `
func f(a) { if a { return 1 } else { return 0 } }
result := 0
for i := 1; i <= 200; i++ { result = f(true) }
`, "result")
}

// ---------------------------------------------------------------------------
// 4. Control flow: JMP, FORPREP, FORLOOP, RETURN
// ---------------------------------------------------------------------------

func TestTier1_ForLoop(t *testing.T) {
	compareVMvsJIT(t, `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := 0
for i := 1; i <= 200; i++ { result = sum(10) }
`, "result")
}

func TestTier1_Sum100(t *testing.T) {
	compareVMvsJIT(t, `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := 0
for i := 1; i <= 200; i++ { result = sum(100) }
`, "result")
}

// ---------------------------------------------------------------------------
// 5. Variables: MOVE, GETGLOBAL, SETGLOBAL
// ---------------------------------------------------------------------------

func TestTier1_Move(t *testing.T) {
	compareVMvsJIT(t, `
func f(a) { b := a; return b }
result := 0
for i := 1; i <= 200; i++ { result = f(99) }
`, "result")
}

func TestTier1_GetGlobal(t *testing.T) {
	compareVMvsJIT(t, `
multiplier := 10
func scale(x) {
    return x * multiplier
}
result := 0
for i := 1; i <= 200; i++ { result = scale(5) }
`, "result")
}

func TestTier1_SetGlobal(t *testing.T) {
	compareVMvsJIT(t, `
counter := 0
func inc() {
    counter = counter + 1
}
for i := 1; i <= 200; i++ { inc() }
result := counter
`, "result")
}

// ---------------------------------------------------------------------------
// 6. Functions: CALL, RETURN
// ---------------------------------------------------------------------------

func TestTier1_Call(t *testing.T) {
	compareVMvsJIT(t, `
func double(x) { return x * 2 }
func apply(n) { return double(n) }
result := 0
for i := 1; i <= 200; i++ { result = apply(i) }
`, "result")
}

func TestTier1_Fib5(t *testing.T) {
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(5)
`, "result")
}

func TestTier1_Fib10(t *testing.T) {
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(10)
`, "result")
}

// ---------------------------------------------------------------------------
// 7. Tables: NEWTABLE, GETFIELD, SETFIELD, GETTABLE, SETTABLE
// ---------------------------------------------------------------------------

func TestTier1_NewTable(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {}
    t.x = 10
    return t.x
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_TableFieldAccess(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {}
    t.a = 1
    t.b = 2
    return t.a + t.b
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_TableArrayAccess(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {}
    t[1] = 10
    t[2] = 20
    return t[1] + t[2]
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_Append(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {1, 2, 3}
    return t[1] + t[2] + t[3]
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// 8. Strings: CONCAT
// ---------------------------------------------------------------------------

func TestTier1_Concat(t *testing.T) {
	compareVMvsJIT(t, `
func f(a, b) { return a .. b }
result := ""
for i := 1; i <= 200; i++ { result = f("hello", " world") }
`, "result")
}

// ---------------------------------------------------------------------------
// 9. Upvalues: CLOSURE
// ---------------------------------------------------------------------------

func TestTier1_Closure(t *testing.T) {
	compareVMvsJIT(t, `
func make_adder(x) {
    func adder(y) { return x + y }
    return adder
}
add5 := make_adder(5)
result := 0
for i := 1; i <= 200; i++ { result = add5(10) }
`, "result")
}

// ---------------------------------------------------------------------------
// 10. End-to-end programs
// ---------------------------------------------------------------------------

func TestTier1_FibIterative(t *testing.T) {
	compareVMvsJIT(t, `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 1; i <= n; i++ {
        c := a + b
        a = b
        b = c
    }
    return a
}
result := 0
for i := 1; i <= 200; i++ { result = fib_iter(20) }
`, "result")
}

func TestTier1_SumPrimes(t *testing.T) {
	compareVMvsJIT(t, `
func is_prime(n) {
    if n < 2 { return false }
    i := 2
    for i * i <= n {
        if n % i == 0 { return false }
        i = i + 1
    }
    return true
}
func sum_primes(limit) {
    s := 0
    for i := 2; i <= limit; i++ {
        if is_prime(i) { s = s + i }
    }
    return s
}
result := sum_primes(100)
`, "result")
}

func TestTier1_MathIntensive(t *testing.T) {
	compareVMvsJIT(t, `
func math_test(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i * i
    }
    return s
}
result := 0
for i := 1; i <= 200; i++ { result = math_test(100) }
`, "result")
}

func TestTier1_AllBenchmarks(t *testing.T) {
	benchmarks := map[string]string{
		"fib_recursive": `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(5)
`,
		"fibonacci_iterative": `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 1; i <= n; i++ {
        c := a + b
        a = b
        b = c
    }
    return a
}
result := fib_iter(20)
`,
		"sum_loop": `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ { s = s + i }
    return s
}
result := 0
for i := 1; i <= 200; i++ { result = sum(100) }
`,
		"nested_call": `
func double(x) { return x * 2 }
func quad(x) { return double(double(x)) }
result := 0
for i := 1; i <= 200; i++ { result = quad(i) }
`,
		"if_else": `
func abs(x) { if x < 0 { return -x } else { return x } }
result := 0
for i := 1; i <= 200; i++ { result = abs(-42) }
`,
		"for_with_mod": `
func count_div3(n) {
    c := 0
    for i := 1; i <= n; i++ {
        if i % 3 == 0 { c = c + 1 }
    }
    return c
}
result := 0
for i := 1; i <= 200; i++ { result = count_div3(100) }
`,
		"table_basic": `
func f() {
    t := {}
    t.x = 42
    return t.x
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`,
		"global_read": `
factor := 7
func scale(x) { return x * factor }
result := 0
for i := 1; i <= 200; i++ { result = scale(6) }
`,
		"closure_adder": `
func make_adder(x) {
    func add(y) { return x + y }
    return add
}
adder := make_adder(100)
result := 0
for i := 1; i <= 200; i++ { result = adder(i) }
`,
		"mutual_recursion_small": `
func is_even(n) { if n == 0 { return true }; return is_odd(n-1) }
func is_odd(n) { if n == 0 { return false }; return is_even(n-1) }
result := is_even(10)
`,
	}

	for name, src := range benchmarks {
		t.Run(name, func(t *testing.T) {
			compareVMvsJIT(t, src, "result")
		})
	}
}

// TestTier1_BenchmarkFiles runs actual .gs benchmark files.
func TestTier1_BenchmarkFiles(t *testing.T) {
	benchDir := filepath.Join("..", "..", "benchmarks", "suite")
	files := []struct {
		name   string
		global string
	}{
		// Add benchmark files as they become compatible.
	}

	for _, f := range files {
		t.Run(f.name, func(t *testing.T) {
			path := filepath.Join(benchDir, f.name+".gs")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("benchmark file not found: %s", path)
				return
			}
			src := string(data)
			vmGlobals := runVMFull(t, src)
			jitGlobals := runVMFullWithJIT(t, src)

			vmResult := vmGlobals[f.global]
			jitResult := jitGlobals[f.global]
			assertValueEq(t, fmt.Sprintf("%s.%s", f.name, f.global), jitResult, vmResult)
		})
	}
}

// ---------------------------------------------------------------------------
// 11. Fast call path tests: JIT-to-JIT direct calls
// ---------------------------------------------------------------------------

// TestTier1_FastCall_Simple verifies simple function calls use the fast path.
func TestTier1_FastCall_Simple(t *testing.T) {
	compareVMvsJIT(t, `
func double(x) { return x * 2 }
result := 0
for i := 1; i <= 200; i++ { result = double(i) }
`, "result")
}

// TestTier1_FastCall_MultiArg verifies multi-argument calls.
func TestTier1_FastCall_MultiArg(t *testing.T) {
	compareVMvsJIT(t, `
func add3(a, b, c) { return a + b + c }
result := 0
for i := 1; i <= 200; i++ { result = add3(i, i+1, i+2) }
`, "result")
}

// TestTier1_FastCall_FibRecursive verifies recursive calls via the fast path.
func TestTier1_FastCall_FibRecursive(t *testing.T) {
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(15)
`, "result")
}

// TestTier1_FastCall_MutualRecursion verifies mutual recursion across functions.
func TestTier1_FastCall_MutualRecursion(t *testing.T) {
	compareVMvsJIT(t, `
func is_even(n) {
    if n == 0 { return true }
    return is_odd(n - 1)
}
func is_odd(n) {
    if n == 0 { return false }
    return is_even(n - 1)
}
result := is_even(20)
`, "result")
}

// TestTier1_FastCall_ClosureCall verifies that calling closures with upvalues
// works correctly through the fast path.
func TestTier1_FastCall_ClosureCall(t *testing.T) {
	compareVMvsJIT(t, `
func make_adder(x) {
    func adder(y) { return x + y }
    return adder
}
add10 := make_adder(10)
result := 0
for i := 1; i <= 200; i++ { result = add10(i) }
`, "result")
}

// TestTier1_FastCall_GoFunction verifies that GoFunction calls fall back to
// the generic path correctly (e.g., math.sqrt, print).
func TestTier1_FastCall_GoFunction(t *testing.T) {
	compareVMvsJIT(t, `
func f(x) { return type(x) }
result := ""
for i := 1; i <= 200; i++ { result = f(42) }
`, "result")
}

// TestTier1_FastCall_NestedCalls verifies deeply nested function calls.
func TestTier1_FastCall_NestedCalls(t *testing.T) {
	compareVMvsJIT(t, `
func inc(x) { return x + 1 }
func double(x) { return x * 2 }
func apply(x) { return double(inc(x)) }
result := 0
for i := 1; i <= 200; i++ { result = apply(i) }
`, "result")
}

// TestTier1_FastCall_Ackermann verifies the Ackermann function (stress test
// for deeply recursive fast calls).
func TestTier1_FastCall_Ackermann(t *testing.T) {
	compareVMvsJIT(t, `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
result := ack(3, 4)
`, "result")
}

// TestTier1_FastCall_NoArgs verifies calling a zero-argument function.
func TestTier1_FastCall_NoArgs(t *testing.T) {
	compareVMvsJIT(t, `
func fortytwo() { return 42 }
result := 0
for i := 1; i <= 200; i++ { result = fortytwo() }
`, "result")
}

// TestTier1_FastCall_MultipleResults verifies functions that return values
// used in subsequent computations.
func TestTier1_FastCall_MultipleResults(t *testing.T) {
	compareVMvsJIT(t, `
func compute(x) { return x * x + x }
result := 0
for i := 1; i <= 200; i++ {
    a := compute(i)
    b := compute(i + 1)
    result = a + b
}
`, "result")
}

// TestTier1_FastCall_SumPrimes verifies isPrime called in a loop (benchmark-
// representative workload).
func TestTier1_FastCall_SumPrimes(t *testing.T) {
	compareVMvsJIT(t, `
func is_prime(n) {
    if n < 2 { return false }
    i := 2
    for i * i <= n {
        if n % i == 0 { return false }
        i = i + 1
    }
    return true
}
func sum_primes(limit) {
    s := 0
    for i := 2; i <= limit; i++ {
        if is_prime(i) { s = s + i }
    }
    return s
}
result := sum_primes(200)
`, "result")
}

// ---------------------------------------------------------------------------
// Regression: top-level while loop with baseline JIT
// The top-level proto gets compiled by baseline JIT (threshold=1, called once).
// While loops must not reset local variables between iterations.
// ---------------------------------------------------------------------------

func TestTier1_TopLevelWhileLoop(t *testing.T) {
	// Regression: top-level while loop with globals used as loop variables.
	// The global value cache used a single CachedGlobalGen for all PCs.
	// When SETGLOBAL bumped globalCacheGen and a subsequent GETGLOBAL at
	// a different PC updated CachedGlobalGen, other PCs' stale cached values
	// appeared valid. Fix: clear all cache entries on generation mismatch.
	src := `
depth := 4
count := 0
for depth <= 10 {
    count = count + 1
    depth = depth + 2
}
result := count
`
	compareVMvsJIT(t, src, "result")
}

func TestTier1_TopLevelWhileLoopDepth(t *testing.T) {
	src := `
depth := 4
count := 0
for depth <= 10 {
    count = count + 1
    depth = depth + 2
}
result := depth
`
	compareVMvsJIT(t, src, "result")
}

// ---------------------------------------------------------------------------
// Native BLR call tests: verify ARM64 native call path
// ---------------------------------------------------------------------------

// TestTier1_NativeCall_CalleeOpExit verifies that when a callee does an op-exit
// (like NEWTABLE) during a native BLR call, the ExitNativeCallExit handler
// correctly finishes the callee and returns the result.
func TestTier1_NativeCall_CalleeOpExit(t *testing.T) {
	compareVMvsJIT(t, `
func make_pair(a, b) {
    t := {}
    t[1] = a
    t[2] = b
    return t[1] + t[2]
}
result := 0
for i := 1; i <= 200; i++ { result = make_pair(i, i * 2) }
`, "result")
}

func TestTier1_NativeCall_BLRReplaySideEffectBeforeNewTable(t *testing.T) {
	src := `
func bump_array_then_newtable(t) {
    t[0] = t[0] + 1
    tmp := {}
    return t[0]
}
bag := {}
bag[0] = 0
for i := 1; i <= 200; i++ {
    result = bump_array_then_newtable(bag)
}
result = bag[0]
`
	vmGlobals := runVMFull(t, src)
	want := vmGlobals["result"]
	if !want.IsInt() || want.Int() != 200 {
		t.Fatalf("VM sanity result = %v (%s), want int 200", want, want.TypeName())
	}

	v, proto := runTier1ProgramForTest(t, src)
	defer v.Close()
	got := v.GetGlobal("result")
	assertValueEq(t, "result", got, want)

	callee := findProtoByName(proto, "bump_array_then_newtable")
	if callee == nil {
		t.Fatal("bump_array_then_newtable proto not found")
	}
	if callee.CompiledCodePtr == 0 {
		t.Fatal("unsafe callee was not compiled for normal Tier1 entry")
	}
	if callee.DirectEntryPtr != 0 {
		t.Fatalf("unsafe callee DirectEntryPtr=%#x, want 0", callee.DirectEntryPtr)
	}
}

func TestTier1_NativeCall_BLRReplayFieldSideEffectBeforeNewTable(t *testing.T) {
	src := `
func bump_field_then_newtable(t) {
    t.count = t.count + 1
    tmp := {}
    return t.count
}
bag := {count: 0}
for i := 1; i <= 200; i++ {
    result = bump_field_then_newtable(bag)
}
result = bag.count
`
	vmGlobals := runVMFull(t, src)
	want := vmGlobals["result"]
	if !want.IsInt() || want.Int() != 200 {
		t.Fatalf("VM sanity result = %v (%s), want int 200", want, want.TypeName())
	}

	v, proto := runTier1ProgramForTest(t, src)
	defer v.Close()
	got := v.GetGlobal("result")
	assertValueEq(t, "result", got, want)

	callee := findProtoByName(proto, "bump_field_then_newtable")
	if callee == nil {
		t.Fatal("bump_field_then_newtable proto not found")
	}
	if callee.CompiledCodePtr == 0 {
		t.Fatal("unsafe field callee was not compiled for normal Tier1 entry")
	}
	if callee.DirectEntryPtr != 0 {
		t.Fatalf("unsafe field callee DirectEntryPtr=%#x, want 0", callee.DirectEntryPtr)
	}
}

func TestTier1_NativeCall_PureCalleeKeepsDirectBLR(t *testing.T) {
	src := `
func pure_sum(a, b, c) {
    return a + b + c
}
result := 0
for i := 1; i <= 200; i++ {
    result = pure_sum(i, i * 2, 3)
}
`
	vmGlobals := runVMFull(t, src)
	v, proto := runTier1ProgramForTest(t, src)
	defer v.Close()
	assertValueEq(t, "result", v.GetGlobal("result"), vmGlobals["result"])

	callee := findProtoByName(proto, "pure_sum")
	if callee == nil {
		t.Fatal("pure_sum proto not found")
	}
	if callee.CompiledCodePtr == 0 {
		t.Fatal("pure callee was not compiled")
	}
	if callee.DirectEntryPtr == 0 {
		t.Fatal("pure callee DirectEntryPtr is 0; direct BLR was disabled too broadly")
	}
}

// TestTier1_NativeCall_DeepRecursionWithOpExit verifies recursive native calls
// where the callee also does op-exits.
func TestTier1_NativeCall_DeepRecursionWithOpExit(t *testing.T) {
	compareVMvsJIT(t, `
func sum_with_table(n) {
    if n <= 0 { return 0 }
    t := {val: n}
    return t.val + sum_with_table(n - 1)
}
result := sum_with_table(10)
`, "result")
}

// TestTier1_NativeCall_Fib20 verifies fib(20) produces correct results via native calls.
func TestTier1_NativeCall_Fib20(t *testing.T) {
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(20)
`, "result")
}

// TestTier1_NativeCall_RecursiveWithTableOps verifies recursive calls combined
// with table access ops work correctly (no corruption from native BLR + op-exit).
func TestTier1_NativeCall_RecursiveWithTableOps(t *testing.T) {
	compareVMvsJIT(t, `
func fill(arr, n) {
    if n <= 0 { return }
    arr[n] = n * 10
    fill(arr, n - 1)
}
arr := {}
fill(arr, 20)
result := arr[1] + arr[10] + arr[20]
`, "result")
}

// TestTier1_NativeCall_TwoRecursiveCalls verifies double recursion with table ops.
func TestTier1_NativeCall_TwoRecursiveCalls(t *testing.T) {
	compareVMvsJIT(t, `
func work(arr, lo, hi) {
    if lo >= hi { return }
    mid := lo + (hi - lo) / 2
    arr[mid] = mid * 10
    work(arr, lo, mid)
    work(arr, mid + 1, hi)
}
arr := {}
for i := 1; i <= 32; i++ { arr[i] = 0 }
work(arr, 1, 32)
result := arr[1] + arr[16] + arr[32]
`, "result")
}

// TestTier1_NativeCall_Quicksort_Small verifies quicksort on a small array.
func TestTier1_NativeCall_Quicksort_Small(t *testing.T) {
	compareVMvsJIT(t, `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            tmp := arr[i]
            arr[i] = arr[j]
            arr[j] = tmp
            i = i + 1
        }
    }
    tmp := arr[i]
    arr[i] = arr[hi]
    arr[hi] = tmp
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

arr := {5, 3, 8, 1, 9, 2, 7, 4, 6, 10}
quicksort(arr, 1, 10)
result := arr[1] * 1000000000 + arr[2] * 100000000 + arr[3] * 10000000 + arr[4] * 1000000 + arr[5] * 100000 + arr[6] * 10000 + arr[7] * 1000 + arr[8] * 100 + arr[9] * 10 + arr[10]
`, "result")
}

// TestTier1_NativeCall_Quicksort verifies quicksort with table ops and deep
// recursion works correctly. This reproduces a corruption crash caused by
// native BLR call + op-exit interaction.
func TestTier1_NativeCall_Quicksort(t *testing.T) {
	compareVMvsJIT(t, `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            tmp := arr[i]
            arr[i] = arr[j]
            arr[j] = tmp
            i = i + 1
        }
    }
    tmp := arr[i]
    arr[i] = arr[hi]
    arr[hi] = tmp
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

N := 500
arr := {}
x := 42
for i := 1; i <= N; i++ {
    x = (x * 1103515245 + 12345) % 2147483648
    arr[i] = x
}
quicksort(arr, 1, N)

sorted := true
for i := 1; i < N; i++ {
    if arr[i] > arr[i + 1] { sorted = false }
}
result := sorted
`, "result")
}

// TestTier1_NativeCall_Ackermann verifies ack(3,4) with variable-return (C=0)
// and variable-arg (B=0) native calls works correctly.
func TestTier1_NativeCall_Ackermann(t *testing.T) {
	compareVMvsJIT(t, `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
result := ack(3, 4)
`, "result")
}

// TestTier1_NativeCall_MutualRecursion verifies mutual recursion with C=0/B=0.
func TestTier1_NativeCall_MutualRecursion(t *testing.T) {
	compareVMvsJIT(t, `
func F(n) {
    if n == 0 { return 1 }
    return n - M(F(n - 1))
}
func M(n) {
    if n == 0 { return 0 }
    return n - F(M(n - 1))
}
result := F(15)
`, "result")
}
