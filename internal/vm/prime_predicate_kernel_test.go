package vm

import "testing"

const trialDivisionPrimePredicateSource = `
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
`

func TestPrimePredicateKernelRecognizesStructuralLoop(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, trialDivisionPrimePredicateSource+`
M := 30
total := 10
hits := 2
for candidate := 2; candidate <= M; candidate++ {
    if check(candidate) {
        total = total + candidate
        hits = hits + 1
    }
}
`)
	defer vm.Close()
	if len(proto.Protos) != 1 {
		t.Fatalf("got %d nested protos, want 1", len(proto.Protos))
	}
	if !IsTrialDivisionPrimePredicateProto(proto.Protos[0]) {
		t.Fatal("trial-division predicate proto was not recognized")
	}
	if !HasPrimePredicateSumLoopKernel(proto, map[string]*FuncProto{"check": proto.Protos[0]}) {
		t.Fatal("prime predicate sum loop was not recognized")
	}
}

func TestPrimePredicateKernelCorrectnessWithNonBenchmarkNames(t *testing.T) {
	globals := compileAndRun(t, trialDivisionPrimePredicateSource+`
M := 30
total := 10
hits := 2
for candidate := 2; candidate <= M; candidate++ {
    if check(candidate) {
        total = total + candidate
        hits = hits + 1
    }
}
`)
	expectGlobalInt(t, globals, "total", 139)
	expectGlobalInt(t, globals, "hits", 12)
}

func TestPrimePredicateKernelFallsBackForNonStructuralPredicate(t *testing.T) {
	globals := compileAndRun(t, `
calls := 0
func check(n) {
    calls = calls + 1
    return n == 2
}
limit := 5
total := 0
hits := 0
for i := 2; i <= limit; i++ {
    if check(i) {
        total = total + i
        hits = hits + 1
    }
}
`)
	expectGlobalInt(t, globals, "calls", 4)
	expectGlobalInt(t, globals, "total", 2)
	expectGlobalInt(t, globals, "hits", 1)
}
