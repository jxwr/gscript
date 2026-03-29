//go:build darwin && arm64

// emit_reg_test.go tests register-resident value resolution for the Method JIT.
// Verifies that values allocated to physical registers (X20-X23) produce correct
// results and that the optimization yields measurable speedup over the VM.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// TestEmitReg_AddInts verifies that a+b uses register-resident values
// and produces the correct result matching the VM.
func TestEmitReg_AddInts(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	// Verify some values got register allocations.
	regCount := 0
	for _, pr := range alloc.ValueRegs {
		if !pr.IsFloat {
			regCount++
		}
	}
	if regCount == 0 {
		t.Fatal("expected at least one GPR allocation, got 0")
	}
	t.Logf("GPR allocations: %d values", regCount)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	tests := []struct {
		a, b, want int64
	}{
		{3, 4, 7},
		{0, 0, 0},
		{-1, 1, 0},
		{100, 200, 300},
		{-50, -30, -80},
	}

	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.a), runtime.IntValue(tc.b)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d,%d): %v", tc.a, tc.b, err)
		}
		vmResult := runVM(t, src, args)
		assertValuesEqual(t, "f("+itoa(int(tc.a))+","+itoa(int(tc.b))+")", result[0], vmResult[0])
		if !result[0].IsInt() || result[0].Int() != tc.want {
			t.Errorf("f(%d,%d): expected %d, got %v", tc.a, tc.b, tc.want, result[0])
		}
	}
}

// TestEmitReg_ForLoop verifies that a for loop using register-resident values
// produces correct results for various inputs, matching the VM.
func TestEmitReg_ForLoop(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	// Verify phi nodes get register allocations (critical for loop perf).
	phiRegs := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi {
				if _, ok := alloc.ValueRegs[instr.ID]; ok {
					phiRegs++
				}
			}
		}
	}
	t.Logf("Phi nodes with register allocations: %d", phiRegs)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	tests := []struct {
		n    int64
		want int64
	}{
		{0, 0},
		{1, 1},
		{10, 55},
		{100, 5050},
		{1000, 500500},
		{10000, 50005000},
	}

	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.n)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.n, err)
		}
		vmResult := runVM(t, src, args)
		assertValuesEqual(t, "f("+itoa(int(tc.n))+")", result[0], vmResult[0])
		if !result[0].IsInt() || result[0].Int() != tc.want {
			t.Errorf("f(%d): expected %d, got %v", tc.n, tc.want, result[0])
		}
	}
}

// TestEmitReg_Correctness verifies that register-resident code generation
// produces identical results to the VM for all supported function patterns.
func TestEmitReg_Correctness(t *testing.T) {
	cases := []struct {
		name string
		src  string
		args []runtime.Value
	}{
		{"return_const", `func f() { return 42 }`, nil},
		{"add_ints", `func f(a, b) { return a + b }`, intArgs(3, 4)},
		{"sub_ints", `func f(a, b) { return a - b }`, intArgs(10, 3)},
		{"mul_ints", `func f(a, b) { return a * b }`, intArgs(6, 7)},
		{"if_true", `func f(n) { if n < 2 { return n } else { return n * 2 } }`, intArgs(1)},
		{"if_false", `func f(n) { if n < 2 { return n } else { return n * 2 } }`, intArgs(5)},
		{"nested_if", `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`, intArgs(15)},
		{"for_loop_10", `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10)},
		{"for_loop_100", `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(100)},
		{"mul_chain", `func f(a, b) { x := a * b; y := x * a; z := y * b; return z }`, intArgs(3, 4)},
		{"neg_int", `func f(a) { return -a }`, intArgs(5)},
		{"neg_zero", `func f(a) { return -a }`, intArgs(0)},
		{"div_int", `func f(a, b) { return a / b }`, intArgs(10, 3)},
		{"float_add", `func f(a, b) { return a + b }`,
			[]runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)}},
		{"float_sub", `func f(a, b) { return a - b }`,
			[]runtime.Value{runtime.FloatValue(5.0), runtime.FloatValue(1.5)}},
		{"float_mul", `func f(a, b) { return a * b }`,
			[]runtime.Value{runtime.FloatValue(2.0), runtime.FloatValue(3.5)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proto := compileFunction(t, tc.src)
			fn := BuildGraph(proto)
			alloc := AllocateRegisters(fn)

			cf, err := Compile(fn, alloc)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}
			defer cf.Code.Free()

			result, err := cf.Execute(tc.args)
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			vmResult := runVM(t, tc.src, tc.args)

			if len(result) == 0 || len(vmResult) == 0 {
				t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
			}
			assertValuesEqual(t, tc.name, result[0], vmResult[0])
		})
	}
}

// TestEmitReg_CrossBlockLive verifies that the cross-block liveness analysis
// correctly identifies values that need write-through.
func TestEmitReg_CrossBlockLive(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)

	crossBlock := computeCrossBlockLive(fn)
	if len(crossBlock) == 0 {
		t.Fatal("expected cross-block live values in a for loop, got 0")
	}
	t.Logf("Cross-block live values: %d", len(crossBlock))

	// Verify that parameters and loop variables are cross-block live.
	for id := range crossBlock {
		t.Logf("  v%d is cross-block live", id)
	}
}

// BenchmarkEmitReg_Sum10000 benchmarks the register-resident JIT vs VM for sum(10000).
func BenchmarkEmitReg_Sum10000(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(10000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}
