package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestLoopBoundRangeGuard_EnablesParamBoundSpectralArithmetic(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n) {
    total := 0
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            x := (i + j) * (i + j + 1) / 2 + i + 1
            total = total + x
        }
    }
    return total
}
`)

	var err error
	fn, err = LoopBoundRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("LoopBoundRangeGuardPass: %v", err)
	}
	fn, err = RangeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("RangeAnalysisPass: %v", err)
	}

	guardCount := 0
	safeNonCounter := 0
	safeMul := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGuardIntRange {
				guardCount++
				if instr.Aux != 0 || instr.Aux2 != nestedLoopParamRangeMax {
					t.Fatalf("unexpected GuardIntRange bounds: [%d,%d]", instr.Aux, instr.Aux2)
				}
				if r := fn.IntRanges[instr.ID]; !r.known || r.min != 0 || r.max != nestedLoopParamRangeMax {
					t.Fatalf("GuardIntRange range not propagated: %+v", r)
				}
			}
			if instr.Aux2 == 1 {
				continue
			}
			switch instr.Op {
			case OpAddInt, OpSubInt, OpMulInt, OpDivIntExact, OpNegInt:
				if fn.Int48Safe[instr.ID] {
					safeNonCounter++
					if instr.Op == OpMulInt {
						safeMul++
					}
				}
			}
		}
	}
	if guardCount == 0 {
		t.Fatalf("expected GuardIntRange for dynamic nested-loop bound\nIR:\n%s", Print(fn))
	}
	if safeNonCounter == 0 {
		t.Fatalf("expected parameter-bound loop arithmetic to be Int48Safe\nIR:\n%s", Print(fn))
	}
	if safeMul == 0 {
		t.Fatalf("expected triangular MulInt to be Int48Safe\nIR:\n%s", Print(fn))
	}
}

func TestLoopBoundRangeGuard_GuardsSingleLoopBound(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n) {
    total := 0
    for i := 0; i < n; i++ {
        total = total + i
    }
    return total
}
`)
	out, err := LoopBoundRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("LoopBoundRangeGuardPass: %v", err)
	}
	found := false
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGuardIntRange {
				found = true
				if instr.Aux2 != singleLoopParamRangeMax {
					t.Fatalf("single-loop guard should use wide max %d, got %d", singleLoopParamRangeMax, instr.Aux2)
				}
			}
		}
	}
	if !found {
		t.Fatalf("single-loop bound param should receive GuardIntRange\nIR:\n%s", Print(out))
	}
}

func TestLoopBoundRangeGuard_SkipsObservedTooWideParam(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n) {
    total := 0
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            total = total + i + j
        }
    }
    return total
}
`)
	fn.Proto.ArgIntRangeFeedback = []vm.IntRangeFeedback{{Count: callResultRangeGuardMinCount, Min: nestedLoopParamRangeMax + 1, Max: nestedLoopParamRangeMax + 1}}
	out, err := LoopBoundRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("LoopBoundRangeGuardPass: %v", err)
	}
	if countOps(out)[OpGuardIntRange] != 0 {
		t.Fatalf("wide observed argument should skip loop-bound range guard\nIR:\n%s", Print(out))
	}
}

func TestLoopBoundRangeGuard_UsesNarrowObservedParamMax(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n) {
    total := 0
    for i := 0; i < n; i++ {
        total = total + i
    }
    return total
}
`)
	fn.Proto.ArgIntRangeFeedback = []vm.IntRangeFeedback{{Count: callResultRangeGuardMinCount, Min: 45, Max: 45}}
	out, err := LoopBoundRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("LoopBoundRangeGuardPass: %v", err)
	}

	found := false
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGuardIntRange {
				continue
			}
			found = true
			if instr.Aux != 0 || instr.Aux2 != 45 {
				t.Fatalf("observed narrow max should tighten guard to [0,45], got [%d,%d]\nIR:\n%s",
					instr.Aux, instr.Aux2, Print(out))
			}
		}
	}
	if !found {
		t.Fatalf("expected GuardIntRange for observed loop-bound param\nIR:\n%s", Print(out))
	}
}

func TestObservedParamRangeGuard_GuardsStableNonLoopParam(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n, reps) {
    total := 0
    for i := 0; i < reps; i++ {
        total = total + n - (i % 8)
    }
    return total
}
`)
	fn.Proto.ArgIntRangeFeedback = []vm.IntRangeFeedback{
		{Count: callResultRangeGuardMinCount, Min: 45, Max: 45},
		{Count: callResultRangeGuardMinCount, Min: 1000, Max: 1000},
	}
	out, err := LoopBoundRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("LoopBoundRangeGuardPass: %v", err)
	}
	out, err = ObservedParamRangeGuardPass(out)
	if err != nil {
		t.Fatalf("ObservedParamRangeGuardPass: %v", err)
	}

	guards := map[int][2]int64{}
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGuardIntRange {
				continue
			}
			slot, ok := guardedParamSlot(instr)
			if !ok {
				continue
			}
			guards[slot] = [2]int64{instr.Aux, instr.Aux2}
		}
	}
	if got := guards[0]; got != [2]int64{45, 45} {
		t.Fatalf("non-loop param guard = %v, want [45 45]\nIR:\n%s", got, Print(out))
	}
	if got := guards[1]; got != [2]int64{1000, 1000} {
		t.Fatalf("loop param guard should be tightened to observed range, got %v\nIR:\n%s", got, Print(out))
	}
}

func TestObservedParamRangeGuard_SkipsSingleWarmupObservation(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n) {
    return n + 1
}
`)
	fn.Proto.ArgIntRangeFeedback = []vm.IntRangeFeedback{{Count: 1, Min: 45, Max: 45}}
	out, err := ObservedParamRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("ObservedParamRangeGuardPass: %v", err)
	}
	if countOps(out)[OpGuardIntRange] != 0 {
		t.Fatalf("single warmup observation should not emit parameter range guard\nIR:\n%s", Print(out))
	}
}

func TestObservedParamRangeGuard_UsesTwoStableObservations(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n) {
    return n + 1
}
`)
	fn.Proto.ArgIntRangeFeedback = []vm.IntRangeFeedback{{Count: observedParamRangeGuardMinCount, Min: 45, Max: 45}}
	out, err := ObservedParamRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("ObservedParamRangeGuardPass: %v", err)
	}
	if countOps(out)[OpGuardIntRange] != 1 {
		t.Fatalf("two stable observations should emit parameter range guard\nIR:\n%s", Print(out))
	}
}

func TestLoopBoundRangeGuard_GuardsOnlyLoopBoundParams(t *testing.T) {
	fn := buildForLoopBoundRangeGuardTest(t, `
func f(n, offset) {
    total := 0
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            total = total + i + j + offset
        }
    }
    return total
}
`)
	out, err := LoopBoundRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("LoopBoundRangeGuardPass: %v", err)
	}

	var guardedSlots []int
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGuardIntRange {
				continue
			}
			slot, ok := guardedParamSlot(instr)
			if !ok {
				t.Fatalf("range guard did not wrap a parameter type guard: %v\nIR:\n%s", instr, Print(out))
			}
			guardedSlots = append(guardedSlots, slot)
		}
	}
	if len(guardedSlots) != 1 || guardedSlots[0] != 0 {
		t.Fatalf("expected only loop-bound param n(slot 0) to be guarded, got %v\nIR:\n%s",
			guardedSlots, Print(out))
	}
}

func guardedParamSlot(instr *Instr) (int, bool) {
	if instr == nil || instr.Op != OpGuardIntRange || len(instr.Args) == 0 {
		return 0, false
	}
	typeGuard := instr.Args[0].Def
	if typeGuard == nil || typeGuard.Op != OpGuardType || len(typeGuard.Args) == 0 {
		return 0, false
	}
	load := typeGuard.Args[0].Def
	if load == nil || load.Op != OpLoadSlot {
		return 0, false
	}
	return int(load.Aux), true
}

func buildForLoopBoundRangeGuardTest(t *testing.T, src string) *Function {
	t.Helper()
	proto := compile(t, src)
	fn := BuildGraph(proto)
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("validate: %v", errs[0])
	}
	var err error
	fn, err = SimplifyPhisPass(fn)
	if err != nil {
		t.Fatalf("SimplifyPhisPass: %v", err)
	}
	fn, err = TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass: %v", err)
	}
	fn, err = ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass: %v", err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}
	return fn
}
