//go:build darwin && arm64

package tests_test

import (
	"fmt"
	"strings"
	"testing"

	gs "github.com/gscript/gscript/gscript"
)

// captureOutput creates a VM with the given options and a print capture function.
// Returns the VM and a pointer to the captured output slice.
func captureOutput(opts ...gs.Option) (*gs.VM, *[]string) {
	var output []string
	allOpts := append([]gs.Option{gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	})}, opts...)
	vm := gs.New(allOpts...)
	return vm, &output
}

// runAndGet executes source on a VM with the given options and returns a named global.
func runAndGet(t *testing.T, src, varName string, opts ...gs.Option) interface{} {
	t.Helper()
	vm := gs.New(opts...)
	if err := vm.Exec(src); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	val, err := vm.Get(varName)
	if err != nil {
		t.Fatalf("get %q error: %v", varName, err)
	}
	return val
}

// TestJIT_FibRecursive verifies that JIT and VM produce the same correct result
// for recursive Fibonacci (n=15).
func TestJIT_FibRecursive(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(15)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(610)
	if vmResult != expected {
		t.Errorf("VM fib(15): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT fib(15): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestJIT_HeavyLoop verifies sum 1..10000 produces the exact correct result.
func TestJIT_HeavyLoop(t *testing.T) {
	src := `
func sumN(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := sumN(10000)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(50005000)
	if vmResult != expected {
		t.Errorf("VM sumN(10000): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT sumN(10000): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestJIT_FunctionCallsVaryingArgs tests functions called with different argument counts.
func TestJIT_FunctionCallsVaryingArgs(t *testing.T) {
	src := `
func add0() { return 0 }
func add1(a) { return a }
func add2(a, b) { return a + b }
func add3(a, b, c) { return a + b + c }
func add4(a, b, c, d) { return a + b + c + d }

r0 := add0()
r1 := add1(10)
r2 := add2(10, 20)
r3 := add3(10, 20, 30)
r4 := add4(10, 20, 30, 40)
`
	tests := []struct {
		name     string
		varName  string
		expected int64
	}{
		{"zero args", "r0", 0},
		{"one arg", "r1", 10},
		{"two args", "r2", 30},
		{"three args", "r3", 60},
		{"four args", "r4", 100},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vmResult := runAndGet(t, src, tc.varName, gs.WithVM())
			jitResult := runAndGet(t, src, tc.varName, gs.WithJIT())

			if vmResult != tc.expected {
				t.Errorf("VM %s: got %v (%T), want %d", tc.varName, vmResult, vmResult, tc.expected)
			}
			if jitResult != tc.expected {
				t.Errorf("JIT %s: got %v (%T), want %d", tc.varName, jitResult, jitResult, tc.expected)
			}
		})
	}
}

// TestJIT_NestedForLoops verifies nested loop computation correctness.
func TestJIT_NestedForLoops(t *testing.T) {
	src := `
func nestedSum(n) {
    total := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= n; j++ {
            total = total + i * j
        }
    }
    return total
}
result := nestedSum(50)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum(1..50) = 1275, nestedSum = 1275 * 1275 = 1625625
	expected := int64(1625625)
	if vmResult != expected {
		t.Errorf("VM nestedSum(50): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT nestedSum(50): got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestJIT_MixedArithmetic tests int and float mixed arithmetic.
func TestJIT_MixedArithmetic(t *testing.T) {
	src := `
func compute() {
    a := 10
    b := 3.5
    c := a + b
    d := c * 2
    e := d - 1
    f := e / 3
    return f
}
result := compute()
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// a=10, b=3.5, c=13.5, d=27.0, e=26.0, f=26.0/3 = 8.666...
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v (%T), JIT=%v (%T)", vmResult, vmResult, jitResult, jitResult)
	}
	// Check approximate value
	var fResult float64
	switch v := jitResult.(type) {
	case float64:
		fResult = v
	case int64:
		fResult = float64(v)
	default:
		t.Fatalf("unexpected type %T for result", jitResult)
	}
	expected := 26.0 / 3.0
	if diff := fResult - expected; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("JIT compute(): got %v, want ~%v", fResult, expected)
	}
}

// TestJIT_IntArithmeticOps tests various integer arithmetic operations.
func TestJIT_IntArithmeticOps(t *testing.T) {
	src := `
func intOps() {
    a := 100
    b := 7
    sum := a + b
    diff := a - b
    prod := a * b
    quot := a / b
    rem := a % b
    return sum + diff + prod + quot + rem
}
result := intOps()
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum=107, diff=93, prod=700, quot=14 (int div), rem=2
	// total = 107 + 93 + 700 + 14 + 2 = 916
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v (%T), JIT=%v (%T)", vmResult, vmResult, jitResult, jitResult)
	}
}

// TestJIT_SideExit_TableOps tests that table operations cause JIT side-exits
// and still produce correct results.
func TestJIT_SideExit_TableOps(t *testing.T) {
	src := `
t := {}
for i := 1; i <= 100; i++ {
    t[i] = i * i
}
sum := 0
for i := 1; i <= 100; i++ {
    sum = sum + t[i]
}
result := sum
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum of squares 1..100 = 100*101*201/6 = 338350
	expected := int64(338350)
	if vmResult != expected {
		t.Errorf("VM table ops: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT table ops: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_SideExit_Closures tests that closures cause JIT side-exits
// and still produce correct results.
func TestJIT_SideExit_Closures(t *testing.T) {
	src := `
func makeAdder(x) {
    return func(y) { return x + y }
}

sum := 0
for i := 1; i <= 50; i++ {
    adder := makeAdder(i)
    sum = sum + adder(i)
}
result := sum
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// sum of 2*i for i=1..50 = 2 * (50*51/2) = 2550
	expected := int64(2550)
	if vmResult != expected {
		t.Errorf("VM closures: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT closures: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_SideExit_StringOps tests string operations that cause JIT side-exits.
func TestJIT_SideExit_StringOps(t *testing.T) {
	src := `
s := ""
for i := 0; i < 50; i++ {
    s = s .. "x"
}
result := #s
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(50)
	if vmResult != expected {
		t.Errorf("VM string ops: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT string ops: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_HighlyRecursive tests deeper recursion to stress the JIT.
func TestJIT_HighlyRecursive(t *testing.T) {
	src := `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
result := ack(3, 4)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// ack(3,4) = 125
	expected := int64(125)
	if vmResult != expected {
		t.Errorf("VM ack(3,4): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT ack(3,4): got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_ConditionalBranching tests various conditional patterns.
func TestJIT_ConditionalBranching(t *testing.T) {
	src := `
func classify(n) {
    count := 0
    for i := 1; i <= n; i++ {
        if i % 15 == 0 {
            count = count + 3
        } elseif i % 5 == 0 {
            count = count + 2
        } elseif i % 3 == 0 {
            count = count + 1
        }
    }
    return count
}
result := classify(100)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
	// Verify: multiples of 15 in 1..100: 6 (add 3 each = 18)
	// multiples of 5 but not 15: 14 (add 2 each = 28)
	// multiples of 3 but not 15: 27 (add 1 each = 27)
	// total = 18 + 28 + 27 = 73
	expected := int64(73)
	if jitResult != expected {
		t.Errorf("JIT classify(100): got %v, want %d", jitResult, expected)
	}
}

// TestJIT_WhileLoop tests while-style loops (for without init/post).
func TestJIT_WhileLoop(t *testing.T) {
	src := `
func collatzSteps(n) {
    steps := 0
    for n != 1 {
        if n % 2 == 0 {
            n = n / 2
        } else {
            n = n * 3 + 1
        }
        steps = steps + 1
    }
    return steps
}
result := collatzSteps(27)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// Collatz sequence for 27 takes 111 steps
	expected := int64(111)
	if vmResult != expected {
		t.Errorf("VM collatzSteps(27): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT collatzSteps(27): got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_FibIterative tests iterative fibonacci to verify loop-heavy JIT paths.
func TestJIT_FibIterative(t *testing.T) {
	src := `
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
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(832040)
	if vmResult != expected {
		t.Errorf("VM fib(30): got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT fib(30): got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_MixedTableAndArithmetic tests a program that mixes table ops with
// arithmetic in the same loop, causing JIT side-exits mid-trace.
func TestJIT_MixedTableAndArithmetic(t *testing.T) {
	src := `
func matmul() {
    a := {}
    b := {}
    n := 10
    for i := 1; i <= n; i++ {
        a[i] = {}
        b[i] = {}
        for j := 1; j <= n; j++ {
            a[i][j] = i + j
            b[i][j] = i * j
        }
    }
    // Compute c[1][1] = dot product of a's row 1 and b's col 1
    sum := 0
    for k := 1; k <= n; k++ {
        sum = sum + a[1][k] * b[k][1]
    }
    return sum
}
result := matmul()
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	// a[1][k] = 1+k, b[k][1] = k*1 = k
	// sum = sum of (1+k)*k for k=1..10 = sum of k+k^2 = 55 + 385 = 440
	expected := int64(440)
	if vmResult != expected {
		t.Errorf("VM matmul: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT matmul: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_ColdCodeSplitting_FnCalls verifies that inline call loops produce
// correct results after cold code splitting moves guard failures out of the hot path.
func TestJIT_ColdCodeSplitting_FnCalls(t *testing.T) {
	src := `
func add(a, b) { return a + b }
func callMany() {
    x := 0
    for i := 0; i < 10000; i++ { x = add(x, 1) }
    return x
}
for i := 1; i <= 15; i++ { callMany() }
result := callMany()
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(10000)
	if vmResult != expected {
		t.Errorf("VM callMany: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT callMany: got %v (%T), want %d", jitResult, jitResult, expected)
	}
	if vmResult != jitResult {
		t.Errorf("VM and JIT results differ: VM=%v, JIT=%v", vmResult, jitResult)
	}
}

// TestJIT_ColdCodeSplitting_HeavyLoop verifies that for-loop register spill
// produces correct results after the spill code is moved to the cold section.
func TestJIT_ColdCodeSplitting_HeavyLoop(t *testing.T) {
	src := `
func sumN(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
for i := 1; i <= 15; i++ { sumN(10) }
result := sumN(10000)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(50005000)
	if vmResult != expected {
		t.Errorf("VM sumN: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT sumN: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_ColdCodeSplitting_FibRecursive verifies self-recursive calls
// work correctly after cold code splitting.
func TestJIT_ColdCodeSplitting_FibRecursive(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(15)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(610)
	if vmResult != expected {
		t.Errorf("VM fib: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT fib: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_ColdCodeSplitting_Ackermann verifies the ackermann function
// (two-parameter self-call) works correctly after cold code splitting.
func TestJIT_ColdCodeSplitting_Ackermann(t *testing.T) {
	src := `
func ackermann(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ackermann(m - 1, 1) }
    return ackermann(m - 1, ackermann(m, n - 1))
}
result := ackermann(3, 4)
`
	vmResult := runAndGet(t, src, "result", gs.WithVM())
	jitResult := runAndGet(t, src, "result", gs.WithJIT())

	expected := int64(125)
	if vmResult != expected {
		t.Errorf("VM ackermann: got %v (%T), want %d", vmResult, vmResult, expected)
	}
	if jitResult != expected {
		t.Errorf("JIT ackermann: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}

// TestJIT_ColdCodeSplitting_ArithGuards verifies that arithmetic type guard
// side exits work correctly when the guard failure code is in the cold section.
func TestJIT_ColdCodeSplitting_ArithGuards(t *testing.T) {
	// This test uses a function where the first call uses ints (JIT compiles it),
	// then a subsequent call uses a non-int to trigger the guard failure.
	src := `
func addOne(x) {
    return x + 1
}
// Warm up JIT
for i := 1; i <= 15; i++ { addOne(10) }
result := addOne(42)
`
	jitResult := runAndGet(t, src, "result", gs.WithJIT())
	expected := int64(43)
	if jitResult != expected {
		t.Errorf("JIT addOne: got %v (%T), want %d", jitResult, jitResult, expected)
	}
}
