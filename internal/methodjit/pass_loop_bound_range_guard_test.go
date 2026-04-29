package methodjit

import "testing"

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

func TestLoopBoundRangeGuard_SkipsSingleLoop(t *testing.T) {
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
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGuardIntRange {
				t.Fatalf("single-loop function should not receive GuardIntRange\nIR:\n%s", Print(out))
			}
		}
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
