//go:build darwin && arm64

// emit_fused_branch_test.go tests fused compare+branch emission in Tier 2.
// When a comparison has exactly one use and that use is an immediately-following
// Branch in the same block, the emitter should fuse them into CMP/FCMP + B.cc
// instead of CMP + CSET + ORR + TBNZ. These tests verify correctness only
// (the optimization must not change observable behavior).

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// TestTier2Emit_FusedBranch_IntCmp verifies that a loop with an integer
// comparison (i <= n) produces correct results when the comparison and
// branch are fused. The sum(10) = 55 invariant is the correctness oracle.
func TestTier2Emit_FusedBranch_IntCmp(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	fn.CarryPreheaderInvariants = true
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
		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "int_cmp_fused f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}

// TestTier2Emit_FusedBranch_IntLt verifies that a strict less-than integer
// comparison (i < n) is fused correctly with its branch.
func TestTier2Emit_FusedBranch_IntLt(t *testing.T) {
	src := `func f(n) { s := 0; for i := 0; i < n; i++ { s = s + 1 }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	fn.CarryPreheaderInvariants = true
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
		{10, 10},
		{50, 50},
	}
	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.input, err)
		}
		vmResult := runVM(t, src, args)
		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "int_lt_fused f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}

// TestTier2Emit_FusedBranch_FloatCmp verifies that a loop with a float
// comparison (x < 4.0) produces correct results when fused. This exercises
// the FCMP + B.cc path rather than CMP + B.cc.
func TestTier2Emit_FusedBranch_FloatCmp(t *testing.T) {
	src := `func f(n) {
		x := 0.0
		s := 0.0
		for i := 0; i < n; i++ {
			x = x + 0.5
			if x < 4.0 {
				s = s + x
			}
		}
		return s
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	fn.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	tests := []struct {
		input int64
	}{
		{0},
		{1},
		{8},
		{20},
	}
	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.input, err)
		}
		vmResult := runVM(t, src, args)
		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "float_cmp_fused f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}

// TestTier2Emit_FusedBranch_IntEq verifies that an equality comparison
// (n == 0) is fused correctly with its branch.
func TestTier2Emit_FusedBranch_IntEq(t *testing.T) {
	src := `func f(n) { if n == 0 { return 1 } else { return n * 2 } }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	fn.CarryPreheaderInvariants = true
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
		{0, 1},
		{1, 2},
		{5, 10},
	}
	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.input, err)
		}
		vmResult := runVM(t, src, args)
		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "int_eq_fused f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}

// TestTier2Emit_FusedBranch_NestedBranch verifies that two comparisons in
// the same block (only the last one is followed by a branch) both produce
// correct results. The first comparison feeds an if-else that does NOT fuse
// (it's in a different block), while the loop comparison can fuse.
func TestTier2Emit_FusedBranch_NestedBranch(t *testing.T) {
	src := `func f(n) {
		s := 0
		for i := 0; i < n; i++ {
			if i < 5 { s = s + 1 } else { s = s + 2 }
		}
		return s
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	fn.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	tests := []struct {
		input int64
	}{
		{0},
		{3},
		{5},
		{10},
	}
	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result, err := cf.Execute(args)
		if err != nil {
			t.Fatalf("Execute error for f(%d): %v", tc.input, err)
		}
		vmResult := runVM(t, src, args)
		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("empty result for f(%d): JIT=%v, VM=%v", tc.input, result, vmResult)
		}
		assertValuesEqual(t, "nested_branch f("+itoa(int(tc.input))+")", result[0], vmResult[0])
	}
}
