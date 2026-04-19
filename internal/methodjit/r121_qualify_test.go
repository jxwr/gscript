//go:build darwin && arm64

// r121_qualify_test.go verifies the numeric-qualification predicate
// introduced by R121. Scaffolding; R122+ wires it into compile.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestR121_QualifyForNumeric_Fib(t *testing.T) {
	// fib: 1 param, no upvals, no nested protos. Should qualify.
	src := `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	proto := compileByName(t, src, "fib")
	ok, np := qualifyForNumeric(proto)
	if !ok {
		t.Fatalf("fib should qualify for numeric; got ok=false")
	}
	if np != 1 {
		t.Errorf("fib: expected numParams=1, got %d", np)
	}
}

func TestR121_QualifyForNumeric_Ack(t *testing.T) {
	// ackermann: 2 params, no upvals, no nested protos.
	src := `func ack(m, n) { if m == 0 { return n + 1 }; if n == 0 { return ack(m-1, 1) }; return ack(m-1, ack(m, n-1)) }`
	proto := compileByName(t, src, "ack")
	ok, np := qualifyForNumeric(proto)
	if !ok {
		t.Fatalf("ack should qualify; got ok=false")
	}
	if np != 2 {
		t.Errorf("ack: expected numParams=2, got %d", np)
	}
}

func TestR121_QualifyForNumeric_ZeroParams(t *testing.T) {
	// 0-param function: does NOT qualify (R121 minimum = 1 param).
	src := `func f() { return 42 }`
	proto := compileByName(t, src, "f")
	ok, _ := qualifyForNumeric(proto)
	if ok {
		t.Errorf("0-param function should NOT qualify for numeric entry")
	}
}

func TestR121_QualifyForNumeric_FivePlusParams(t *testing.T) {
	// >4 param function: does NOT qualify (X0-X3 limit).
	src := `func f(a, b, c, d, e) { return a + b + c + d + e }`
	proto := compileByName(t, src, "f")
	ok, _ := qualifyForNumeric(proto)
	if ok {
		t.Errorf("5-param function should NOT qualify (AAPCS limit 4 ints)")
	}
}

func TestR121_QualifyForNumeric_NilProto(t *testing.T) {
	ok, _ := qualifyForNumeric(nil)
	if ok {
		t.Errorf("nil proto should not qualify")
	}
}

func TestR121_QualifyForNumeric_NestedProto(t *testing.T) {
	// Function containing a nested closure → HasNested, should NOT qualify.
	src := `func outer(n) { inner := func() { return n }; return inner() }`
	proto := compileByName(t, src, "outer")
	ok, _ := qualifyForNumeric(proto)
	if ok {
		t.Errorf("function with nested proto should NOT qualify")
	}
}

// CompiledFunction.NumericTwin / NumericParamCount default to nil/0.
func TestR121_CompiledFunctionHasNumericFields(t *testing.T) {
	var cf CompiledFunction
	if cf.NumericTwin != nil {
		t.Errorf("zero-value NumericTwin should be nil")
	}
	if cf.NumericParamCount != 0 {
		t.Errorf("zero-value NumericParamCount should be 0")
	}
}

// Unused-var trick to keep vm import referenced if other tests are pruned.
var _ = vm.OP_CALL
