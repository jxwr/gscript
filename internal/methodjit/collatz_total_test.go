//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const collatzTotalSrc = `
func collatz_total(limit) {
    total_steps := 0
    for n := 2; n <= limit; n++ {
        x := n
        steps := 0
        for x != 1 {
            if x % 2 == 0 {
                x = x / 2
            } else {
                x = 3 * x + 1
            }
            steps = steps + 1
        }
        total_steps = total_steps + steps
    }
    return total_steps
}`

func TestTier2CollatzTotalKnownFloatModGatedBeforeRuntimeDeopt(t *testing.T) {
	src := collatzTotalSrc + `
result := 0
for iter := 1; iter <= 3; iter++ {
    result = collatz_total(100)
}`
	proto := compileProto(t, src)
	collatz := findProtoByName(proto, "collatz_total")
	if collatz == nil {
		t.Fatal("collatz_total proto not found")
	}

	tm := NewTieringManager()
	err := tm.CompileTier2(collatz)
	if err != nil {
		t.Fatalf("CompileTier2(collatz_total): %v", err)
	}

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	jitTM := NewTieringManager()
	v.SetMethodJIT(jitTM)

	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT execute: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 3142 {
		t.Fatalf("collatz_total(100)=%v, want int(3142)", result)
	}
	if reason := jitTM.tier2FailReason[collatz]; reason != "" {
		t.Fatalf("collatz_total Tier2 fail reason=%q, want successful native float mod", reason)
	}
}

func TestTier2CollatzTotalNarrowsExactDivRecurrenceToInt(t *testing.T) {
	proto := compileProto(t, collatzTotalSrc)
	collatz := findProtoByName(proto, "collatz_total")
	if collatz == nil {
		t.Fatal("collatz_total proto not found")
	}
	fn := BuildGraph(collatz)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline(collatz_total): %v", err)
	}

	ir := Print(fn)
	if !strings.Contains(ir, "DivIntExact") {
		t.Fatalf("expected exact integer division narrowing, IR:\n%s", ir)
	}
	if !strings.Contains(ir, "ModZeroInt") {
		t.Fatalf("expected modulo-zero comparison rewrite, IR:\n%s", ir)
	}
	for _, op := range []Op{OpDivFloat, OpAddFloat, OpMulFloat, OpMod, OpModInt} {
		if countOpHelper(fn, op) != 0 {
			t.Fatalf("expected collatz recurrence to avoid %s after narrowing, IR:\n%s", op, ir)
		}
	}
}

func TestTier2ExactDivDoesNotNarrowUnguardedIntDivision(t *testing.T) {
	src := `
func half(x) {
    return x / 2
}
result := half(7)`
	proto := compileProto(t, src)
	half := findProtoByName(proto, "half")
	if half == nil {
		t.Fatal("half proto not found")
	}
	fn := BuildGraph(half)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline(half): %v", err)
	}
	if countOpHelper(fn, OpDivIntExact) != 0 {
		t.Fatalf("unguarded x / 2 must remain float division, IR:\n%s", Print(fn))
	}
}

func TestTier2ExactDivDoesNotNarrowObservableGuardedDivision(t *testing.T) {
	src := `
func half_even(x) {
    if x % 2 == 0 {
        return x / 2
    }
    return 0
}
result := half_even(8)`
	proto := compileProto(t, src)
	halfEven := findProtoByName(proto, "half_even")
	if halfEven == nil {
		t.Fatal("half_even proto not found")
	}
	fn := BuildGraph(halfEven)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline(half_even): %v", err)
	}
	if countOpHelper(fn, OpDivIntExact) != 0 {
		t.Fatalf("guarded but observable x / 2 must remain float division, IR:\n%s", Print(fn))
	}

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	v.SetMethodJIT(NewTieringManager())
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT execute: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsFloat() || result.Float() != 4.0 {
		t.Fatalf("half_even(8) result=%v, want float(4.0)", result)
	}
}
