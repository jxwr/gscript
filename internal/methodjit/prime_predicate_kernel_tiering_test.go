package methodjit

import "testing"

func TestPrimePredicateSumLoopUsesWholeLoopKernelInsteadOfMainTier2(t *testing.T) {
	proto := compileProto(t, `
func check(n) {
    if n < 2 { return false }
    if n < 4 { return true }
    if n % 2 == 0 { return false }
    if n % 3 == 0 { return false }
    i := 5
    for i * i <= n {
        if n % i == 0 { return false }
        if n % (i + 2) == 0 { return false }
        i = i + 6
    }
    return true
}

limit := 2000
total := 0
hits := 0
for candidate := 2; candidate <= limit; candidate++ {
    if check(candidate) {
        total = total + candidate
        hits = hits + 1
    }
}
`)
	tm := NewTieringManager()
	if !tm.hasPrimePredicateSumDriverLoop(proto) {
		t.Fatal("prime predicate sum driver loop was not recognized")
	}
	proto.CallCount = BaselineCompileThreshold
	if got := tm.TryCompile(proto); got != nil {
		t.Fatalf("TryCompile returned %T, want nil whole-loop kernel routing", got)
	}
	if !proto.JITDisabled {
		t.Fatal("<main> was not marked JITDisabled for whole-loop kernel routing")
	}
}
