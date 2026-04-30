//go:build darwin && arm64

package methodjit

import (
	"os"
	"strings"
	"testing"
)

func TestTier2ExitStormGateAllowsNoFilterReadBackedRecursiveTableMutation(t *testing.T) {
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
	if err := tm.CompileTier2(qs); err != nil {
		t.Fatalf("CompileTier2(quicksort) failed: %v", err)
	}
	if !qs.Tier2Promoted || qs.Tier2DirectEntryPtr == 0 {
		t.Fatalf("quicksort did not install Tier2-only direct entry: promoted=%v tier2Direct=%#x", qs.Tier2Promoted, qs.Tier2DirectEntryPtr)
	}
}

func TestTableMutationRecoveryAdmitsQuicksortReadBackedSwap(t *testing.T) {
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
	if site, ok := summary.firstUnadmitted(); ok {
		t.Fatalf("quicksort swap mutation should be admitted, first unadmitted=%+v", site)
	}
	for _, site := range summary.Sites {
		if site.Op == OpSetTable && site.RecoveryClass == tableMutationRecoverReadBackedOverwrite {
			return
		}
	}
	t.Fatalf("quicksort recovery sites did not include a read-backed SetTable: %+v", summary.Sites)
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

func TestTier2LoopGateAllowsDefaultSelfRecursiveIdempotentTableOverwrite(t *testing.T) {
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
	if hasStaticSelfRecursivePartitionSetTableLoop(touch) {
		t.Fatal("idempotent recursive overwrite should not match partition-style SetTable loop")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(touch); err != nil {
		t.Fatalf("CompileTier2(touch) failed: %v", err)
	}
}

func TestTryCompilePromotesRecursivePartitionTableMutationTier2(t *testing.T) {
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
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}
`)
	qs := findProtoByName(top, "quicksort")
	if qs == nil {
		t.Fatal("quicksort proto not found")
	}
	if !hasStaticSelfRecursivePartitionSetTableLoop(qs) {
		t.Fatal("quicksort should match partition-style SetTable loop prefilter")
	}

	tm := NewTieringManager()
	qs.CallCount = tmDefaultTier2Threshold
	compiled := tm.TryCompile(qs)
	if compiled == nil {
		t.Fatal("TryCompile(quicksort) returned nil; want Tier2 compile")
	}
	if tm.tier2Failed[qs] {
		t.Fatal("quicksort should not be recorded as a Tier2 failure")
	}
	if tm.Tier2Attempted() != 1 {
		t.Fatalf("quicksort should attempt Tier2 once, got %d attempts", tm.Tier2Attempted())
	}
	if !qs.Tier2Promoted || qs.Tier2DirectEntryPtr == 0 {
		t.Fatalf("quicksort did not install Tier2-only direct entry: promoted=%v tier2Direct=%#x", qs.Tier2Promoted, qs.Tier2DirectEntryPtr)
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
