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
		if !strings.Contains(err.Error(), "known-float OpMod inside loop") {
			t.Fatalf("CompileTier2 rejected for unexpected reason: %v", err)
		}
	} else {
		t.Fatal("CompileTier2(collatz_total) unexpectedly succeeded")
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
	if reason := jitTM.tier2FailReason[collatz]; !strings.Contains(reason, "known-float OpMod inside loop") {
		t.Fatalf("collatz_total Tier2 fail reason=%q, want known-float OpMod gate", reason)
	}
}
