package methodjit

import "testing"

func runOverflowBoxingPipelineForTest(t *testing.T, src string) *Function {
	t.Helper()
	proto := compile(t, src)
	fn := BuildGraph(proto)
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("validate: %v", errs[0])
	}

	var err error
	passes := []struct {
		name string
		fn   PassFunc
	}{
		{"SimplifyPhis", SimplifyPhisPass},
		{"TypeSpecialize", TypeSpecializePass},
		{"ConstProp", ConstPropPass},
		{"DCE", DCEPass},
		{"RangeAnalysis", RangeAnalysisPass},
		{"OverflowBoxing", OverflowBoxingPass},
	}
	for _, pass := range passes {
		fn, err = pass.fn(fn)
		if err != nil {
			t.Fatalf("%s: %v", pass.name, err)
		}
	}
	return fn
}

func TestOverflowBoxing_KeepsBoundedLinearInductionRaw(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func f(n) {
    i := 2
    hits := 0
    for i * i <= n {
        j := i * i
        for j <= n {
            hits = hits + 1
            j = j + i
        }
        i = i + 1
    }
    return hits
}
`)

	foundGuardedPhi := false
	foundRawUpdate := false
	foundRawInitMul := false
	for _, block := range fn.Blocks {
		cond := loopHeaderBranchCond(block)
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				break
			}
			if !guardBoundsPhi(cond, instr) {
				continue
			}
			foundGuardedPhi = true
			for _, arg := range instr.Args {
				if arg == nil || arg.Def == nil {
					continue
				}
				switch arg.Def.Op {
				case OpAddInt:
					foundRawUpdate = true
				case OpMulInt:
					foundRawInitMul = true
				case OpAdd, OpMul:
					t.Fatalf("bounded linear induction arg was boxed: %s\nIR:\n%s", arg.Def.Op, Print(fn))
				}
			}
		}
	}
	if !foundGuardedPhi || !foundRawUpdate || !foundRawInitMul {
		t.Fatalf("expected guarded linear induction with raw init/update (phi=%v update=%v initMul=%v)\nIR:\n%s",
			foundGuardedPhi, foundRawUpdate, foundRawInitMul, Print(fn))
	}
}

func TestOverflowBoxing_BoxesMultiplicativeModuloRecurrence(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func f(seed) {
    x := seed
    for i := 1; i <= 10; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
    }
    return x
}
`)

	hasGenericMul := false
	hasGenericAdd := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpMul:
				hasGenericMul = true
			case OpAdd:
				hasGenericAdd = true
			}
		}
	}
	if !hasGenericMul || !hasGenericAdd {
		t.Fatalf("expected overflow-prone multiplicative recurrence to stay boxed (mul=%v add=%v)\nIR:\n%s",
			hasGenericMul, hasGenericAdd, Print(fn))
	}
}

func TestOverflowBoxing_BoxesDecrementingInductionUnderUpperGuard(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func f(n) {
    i := n
    for i <= n {
        i = i - 1
        if i < 0 { return i }
    }
    return i
}
`)

	hasGenericSub := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpSub {
				hasGenericSub = true
			}
			if instr.Op == OpSubInt {
				t.Fatalf("decrementing induction under upper guard should not stay raw: %s\nIR:\n%s", instr.Op, Print(fn))
			}
		}
	}
	if !hasGenericSub {
		t.Fatalf("expected boxed Sub for decrementing induction\nIR:\n%s", Print(fn))
	}
}
