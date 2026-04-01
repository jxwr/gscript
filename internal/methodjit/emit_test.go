//go:build darwin && arm64

// emit_test.go tests ARM64 code generation from CFG SSA IR.
// Tests compile GScript functions to native code via the full pipeline
// (BuildGraph -> RegAlloc -> Compile), execute them, and compare results
// with the VM interpreter.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// TestEmit_ReturnConst: func f() { return 42 } — compile, execute, verify returns 42.
func TestEmit_ReturnConst(t *testing.T) {
	src := `func f() { return 42 }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	result, err := cf.Execute(nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Compare with VM
	vmResult := runVM(t, src, nil)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	if len(result) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f() return const", result[0], vmResult[0])
	if !result[0].IsInt() || result[0].Int() != 42 {
		t.Errorf("expected 42 (int), got %v (type=%s)", result[0], result[0].TypeName())
	}
}

// TestEmit_AddInts: func f(a, b) { return a + b } — verify 3+4=7.
func TestEmit_AddInts(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(3,4)", result[0], vmResult[0])
	if !result[0].IsInt() || result[0].Int() != 7 {
		t.Errorf("expected 7, got %v", result[0])
	}
}

// TestEmit_SubInts: func f(a, b) { return a - b } — verify 10-3=7.
func TestEmit_SubInts(t *testing.T) {
	src := `func f(a, b) { return a - b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(10,3)", result[0], vmResult[0])
	if !result[0].IsInt() || result[0].Int() != 7 {
		t.Errorf("expected 7, got %v", result[0])
	}
}

// TestEmit_MulInts: func f(a, b) { return a * b } — verify 6*7=42.
func TestEmit_MulInts(t *testing.T) {
	src := `func f(a, b) { return a * b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(6), runtime.IntValue(7)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(6,7)", result[0], vmResult[0])
	if !result[0].IsInt() || result[0].Int() != 42 {
		t.Errorf("expected 42, got %v", result[0])
	}
}

// TestEmit_IfElse: func f(n) { if n < 2 { return n } else { return n * 2 } }
func TestEmit_IfElse(t *testing.T) {
	src := `func f(n) { if n < 2 { return n } else { return n * 2 } }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	tests := []struct {
		input int64
		want  int64
	}{
		{1, 1},
		{0, 0},
		{5, 10},
		{10, 20},
	}
	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.input, err)
		}

		vmResult := runVM(t, src, args)
		if len(vmResult) == 0 || len(result) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}

// TestEmit_ForLoop: func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }
func TestEmit_ForLoop(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	tests := []struct {
		input int64
		want  int64
	}{
		{0, 0},
		{1, 1},
		{10, 55},
		{100, 5050},
	}
	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.input, err)
		}

		vmResult := runVM(t, src, args)
		if len(vmResult) == 0 || len(result) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}

// TestEmit_NestedLoopCount tests that count propagation works across 2 nested loops.
// This is the minimal reproducer for the mandelbrot bug where count stays 0.
func TestEmit_NestedLoopCount(t *testing.T) {
	src := `func f(n) {
		count := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				count = count + 1
			}
		}
		return count
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	for _, n := range []int64{1, 2, 3, 5, 10} {
		result, err := cf.Execute([]runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Execute error for n=%d: %v", n, err)
		}
		vmResult := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		expected := n * n
		if result[0].Int() != expected {
			t.Errorf("n=%d: Tier2=%d, VM=%d, expected %d", n, result[0].Int(), vmResult[0].Int(), expected)
		}
	}
}

// TestEmit_NestedLoopCountConditional tests count propagation with conditional increment.
func TestEmit_NestedLoopCountConditional(t *testing.T) {
	src := `func f(n) {
		count := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if j > 2 { count = count + 1 }
			}
		}
		return count
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	for _, n := range []int64{3, 5, 10} {
		result, err := cf.Execute([]runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Execute error for n=%d: %v", n, err)
		}
		vmResult := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: Tier2=%v vs VM=%v MISMATCH", n, result[0], vmResult[0])
		}
	}
}

// TestEmit_ThreeNestedLoops tests 3-level nested loop count propagation.
func TestEmit_ThreeNestedLoops(t *testing.T) {
	src := `func f(n) {
		count := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				for k := 0; k < n; k++ {
					count = count + 1
				}
			}
		}
		return count
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	for _, n := range []int64{1, 2, 3, 4} {
		result, err := cf.Execute([]runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Execute error for n=%d: %v", n, err)
		}
		vmResult := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		expected := n * n * n
		if result[0].Int() != expected {
			t.Errorf("n=%d: Tier2=%d, VM=%d, expected %d", n, result[0].Int(), vmResult[0].Int(), expected)
		}
	}
}

// TestEmit_NestedLoopBreak tests nested loops with break + conditional
// count — the mandelbrot pattern.
func TestEmit_NestedLoopBreak(t *testing.T) {
	src := `func f(n) {
		count := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				escaped := false
				for k := 0; k < 10; k++ {
					if k > 3 {
						escaped = true
						break
					}
				}
				if !escaped { count = count + 1 }
			}
		}
		return count
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	for _, n := range []int64{1, 2, 3, 5} {
		result, err := cf.Execute([]runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Execute error for n=%d: %v", n, err)
		}
		vmResult := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: Tier2=%v vs VM=%v MISMATCH", n, result[0], vmResult[0])
		}
	}
}

// itoa for test labels (no import strconv needed).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
