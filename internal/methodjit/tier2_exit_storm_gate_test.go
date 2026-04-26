//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"
)

func TestTier2ExitStormGateBlocksNoFilterRecursiveTableMutation(t *testing.T) {
	t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	top := compileTop(t, `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}
`)
	qs := findProtoByName(top, "quicksort")
	if qs == nil {
		t.Fatal("quicksort proto not found")
	}

	tm := NewTieringManager()
	err := tm.CompileTier2(qs)
	if err == nil {
		t.Fatal("CompileTier2(quicksort) succeeded; want exit-storm gate failure")
	}
	if !strings.Contains(err.Error(), "self-recursive loop has residual table mutation") {
		t.Fatalf("CompileTier2(quicksort) error = %q, want self-recursive table mutation gate", err)
	}
	if qs.Tier2Promoted || qs.DirectEntryPtr != 0 {
		t.Fatalf("quicksort promoted despite hard gate: promoted=%v direct=%#x", qs.Tier2Promoted, qs.DirectEntryPtr)
	}
}

func TestTier2CompilesNoFilterKnownFloatModLoop(t *testing.T) {
	t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	top := compileTop(t, collatzTotalSrc+`
result := 0
for iter := 1; iter <= 3; iter++ {
    result = collatz_total(100)
}
`)
	collatz := findProtoByName(top, "collatz_total")
	if collatz == nil {
		t.Fatal("collatz_total proto not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(collatz); err != nil {
		t.Fatalf("CompileTier2(collatz_total): %v", err)
	}
}

func TestTier2DefaultGateBlocksGenericModLoop(t *testing.T) {
	top := compileTop(t, `
func lcg(n, seed) {
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
    }
    return x
}
`)
	lcg := findProtoByName(top, "lcg")
	if lcg == nil {
		t.Fatal("lcg proto not found")
	}

	tm := NewTieringManager()
	err := tm.CompileTier2(lcg)
	if err == nil {
		t.Fatal("CompileTier2(lcg) succeeded; want generic Mod performance gate")
	}
	if !strings.Contains(err.Error(), "generic OpMod inside loop") {
		t.Fatalf("CompileTier2(lcg) error = %q, want generic Mod gate", err)
	}
}

func TestTier2CompilesNoFilterGenericModLoop(t *testing.T) {
	t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	top := compileTop(t, `
func lcg(n, seed) {
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
    }
    return x
}
`)
	lcg := findProtoByName(top, "lcg")
	if lcg == nil {
		t.Fatal("lcg proto not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(lcg); err != nil {
		t.Fatalf("CompileTier2(lcg): %v", err)
	}
}
