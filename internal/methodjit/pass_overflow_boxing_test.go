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

func TestOverflowBoxing_DetectsShiftAddOverflowVersion(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
`)

	spec, ok := detectShiftAddOverflowVersion(fn)
	if !ok {
		t.Fatalf("expected shift-add overflow version for fib-style recurrence\nIR:\n%s", Print(fn))
	}
	if spec.add == nil || spec.add.Op != OpAdd {
		t.Fatalf("versioned recurrence should still have boxed Add IR before codegen\nIR:\n%s", Print(fn))
	}
	if spec.leftPhi == nil || spec.rightPhi == nil || spec.leftPhi == spec.rightPhi {
		t.Fatalf("expected distinct shift-add phis, got left=%v right=%v\nIR:\n%s",
			spec.leftPhi, spec.rightPhi, Print(fn))
	}
}

func TestOverflowBoxing_ShiftAddVersionRejectsLCG(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func f(seed) {
    x := seed
    for i := 1; i <= 10; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
    }
    return x
}
`)

	if _, ok := detectShiftAddOverflowVersion(fn); ok {
		t.Fatalf("LCG recurrence must not use shift-add overflow versioning\nIR:\n%s", Print(fn))
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

func TestOverflowBoxing_KeepsConvergingSwapIndicesRaw(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func f(n) {
    lo := 1
    hi := n
    acc := 0
    for lo < hi {
        acc = acc + lo
        lo = lo + 1
        hi = hi - 1
    }
    return lo + hi + acc
}
`)

	foundAdd := false
	foundSub := false
	for _, block := range fn.Blocks {
		cond := loopHeaderBranchCond(block)
		if cond == nil || cond.Op != OpLtInt || len(cond.Args) < 2 {
			continue
		}
		leftPhi := headerPhiValue(cond.Args[0], block)
		rightPhi := headerPhiValue(cond.Args[1], block)
		if leftPhi == nil || rightPhi == nil {
			continue
		}
		for _, phi := range []*Instr{leftPhi, rightPhi} {
			for _, arg := range phi.Args {
				if arg == nil || arg.Def == nil {
					continue
				}
				switch arg.Def.Op {
				case OpAddInt:
					foundAdd = true
				case OpSubInt:
					foundSub = true
				case OpAdd, OpSub:
					t.Fatalf("converging loop index was boxed: %s\nIR:\n%s", arg.Def.Op, Print(fn))
				}
			}
		}
	}
	if !foundAdd || !foundSub {
		t.Fatalf("expected raw AddInt/SubInt converging indices (add=%v sub=%v)\nIR:\n%s",
			foundAdd, foundSub, Print(fn))
	}
}

func TestOverflowBoxing_BoxesWideConvergingSub(t *testing.T) {
	fn := runOverflowBoxingPipelineForTest(t, `
func f(n) {
    lo := 1
    hi := n
    for lo < hi {
        lo = lo + 1
        hi = hi - 2
    }
    return hi
}
`)

	hasGenericSub := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpSub {
				hasGenericSub = true
			}
			if instr.Op == OpSubInt && len(instr.Args) >= 2 && valueIsConstInt(instr.Args[1], 2) {
				t.Fatalf("hi-2 under lo<hi is not generally int48-safe and should be boxed\nIR:\n%s", Print(fn))
			}
		}
	}
	if !hasGenericSub {
		t.Fatalf("expected generic Sub for wide decrement\nIR:\n%s", Print(fn))
	}
}
