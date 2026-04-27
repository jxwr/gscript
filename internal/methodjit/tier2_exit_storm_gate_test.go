//go:build darwin && arm64

package methodjit

import (
	"os"
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

func TestTableMutationRecoveryClassifiesQuicksortSwapAsDiagnosticOnly(t *testing.T) {
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
	fn := BuildGraph(qs)
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("pipeline quicksort: %v", err)
	}
	summary := analyzeLoopTableMutationRecovery(fn)
	if len(summary.Sites) == 0 {
		t.Fatal("expected quicksort table mutation recovery sites")
	}
	site, ok := summary.firstUnadmitted()
	if !ok {
		t.Fatal("quicksort swap mutations should remain diagnostic-only, not admitted")
	}
	if site.Op != OpSetTable {
		t.Fatalf("first unadmitted op = %s, want SetTable", site.Op)
	}
	if site.RecoveryClass != tableMutationRecoverReadBackedOverwrite {
		t.Fatalf("quicksort recovery class = %s, want read-backed-overwrite", site.RecoveryClass)
	}
}

func TestTier2ExitStormGateAllowsNoFilterSelfRecursiveIdempotentTableOverwrite(t *testing.T) {
	t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	top := compileTop(t, `
func touch(arr, n) {
    if n <= 0 { return }
    for i := 1; i <= n; i++ {
        arr[i] = arr[i]
    }
    touch(arr, n - 1)
}
`)
	touch := findProtoByName(top, "touch")
	if touch == nil {
		t.Fatal("touch proto not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(touch); err != nil {
		t.Fatalf("CompileTier2(touch) failed: %v", err)
	}
}

func TestTier2ExitStormGateAllowsNoFilterKnownFloatModLoop(t *testing.T) {
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

func TestTier2ExitStormGateAllowsNoFilterNativeNumericGenericModLoop(t *testing.T) {
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
		t.Fatalf("CompileTier2(lcg) failed: %v", err)
	}
}

func TestTier2LoopGateAllowsNativeNumericSetTableLoop(t *testing.T) {
	top := compileTop(t, `
func make_values(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr[n]
}
`)
	makeValues := findProtoByName(top, "make_values")
	if makeValues == nil {
		t.Fatal("make_values proto not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(makeValues); err != nil {
		t.Fatalf("CompileTier2(make_values) failed: %v", err)
	}
}

func TestTier2ExitStormGateBlocksNoFilterUnknownGenericModLoop(t *testing.T) {
	t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	top := compileTop(t, `
func mod_unknown(xs, n) {
    x := xs[1]
    for i := 1; i <= n; i++ {
        x = (x + 1) % 2147483648
    }
    return x
}
`)
	modUnknown := findProtoByName(top, "mod_unknown")
	if modUnknown == nil {
		t.Fatal("mod_unknown proto not found")
	}

	tm := NewTieringManager()
	err := tm.CompileTier2(modUnknown)
	if err == nil {
		t.Fatal("CompileTier2(mod_unknown) succeeded; want generic Mod gate failure")
	}
	if !strings.Contains(err.Error(), "generic OpMod inside loop") {
		t.Fatalf("CompileTier2(mod_unknown) error = %q, want generic Mod gate", err)
	}
}

func TestTier2ExitStormGateAllowsFannkuchSmallConstModulo(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/fannkuch.gs")
	if err != nil {
		t.Fatalf("read fannkuch.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))
	fannkuch := findProtoByName(top, "fannkuch")
	if fannkuch == nil {
		t.Fatal("fannkuch proto not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(fannkuch); err != nil {
		t.Fatalf("CompileTier2(fannkuch) failed: %v", err)
	}
	if !fannkuch.Tier2Promoted || fannkuch.DirectEntryPtr == 0 {
		t.Fatalf("fannkuch did not install Tier2: promoted=%v direct=%#x",
			fannkuch.Tier2Promoted, fannkuch.DirectEntryPtr)
	}
}
