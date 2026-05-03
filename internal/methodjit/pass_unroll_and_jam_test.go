package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestUnrollAndJam_FloatReductionUnrollsWithScalarTail(t *testing.T) {
	src := `func f(n) {
		s := 0.0
		for i := 0; i < n; i++ {
			x := i + 1.0
			s = s + x * 0.5
		}
		return s
	}`

	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	var err error
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

	beforeBlocks := len(fn.Blocks)
	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		t.Fatalf("UnrollAndJamPass: %v", err)
	}
	assertValidates(t, fn, "after UnrollAndJam")
	if len(fn.Blocks) != beforeBlocks+2 {
		t.Fatalf("block count = %d, want %d after unroll tail blocks\nIR:\n%s", len(fn.Blocks), beforeBlocks+2, Print(fn))
	}
	ir := Print(fn)
	if !strings.Contains(ir, "Branch") || strings.Count(ir, "MulFloat") < 2 || strings.Count(ir, "AddFloat") < 2 {
		t.Fatalf("expected cloned float body with scalar tail, IR:\n%s", ir)
	}

	for _, n := range []int64{0, 1, 2, 3, 7, 8} {
		got, err := Interpret(fn, []runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Interpret f(%d): %v\nIR:\n%s", n, err, ir)
		}
		want := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		assertValuesEqual(t, "f", got[0], want[0])
	}
}

func TestUnrollAndJam_FloatReductionWithCompanionRecurrence(t *testing.T) {
	src := `func f(n) {
		sum := 0.0
		sign := 1.0
		for i := 0; i < n; i++ {
			sum = sum + sign / (2.0 * i + 1.0)
			sign = -sign
		}
		return sum * 4.0
	}`

	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	var err error
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

	beforeBlocks := len(fn.Blocks)
	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		t.Fatalf("UnrollAndJamPass: %v", err)
	}
	assertValidates(t, fn, "after UnrollAndJam")
	if len(fn.Blocks) != beforeBlocks+2 {
		t.Fatalf("block count = %d, want %d after unroll tail blocks\nIR:\n%s", len(fn.Blocks), beforeBlocks+2, Print(fn))
	}
	ir := Print(fn)
	if strings.Count(ir, "DivFloat") < 2 || strings.Count(ir, "NegFloat") < 2 {
		t.Fatalf("expected cloned expression and companion recurrence, IR:\n%s", ir)
	}

	for _, n := range []int64{0, 1, 2, 3, 8, 9} {
		got, err := Interpret(fn, []runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Interpret f(%d): %v\nIR:\n%s", n, err, ir)
		}
		want := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		assertValuesEqual(t, "f", got[0], want[0])
	}
}

func TestUnrollAndJam_UnrollsMultipleInlinedHelperLoops(t *testing.T) {
	src := `func f(n, which) {
		if which < 1 {
			a := 0.0
			for i := 0; i < n; i++ {
				x := i + 1.0
				a = a + x * 0.5
			}
			return a
		}
		b := 0.0
		for j := 0; j < n; j++ {
			y := j + 2.0
			b = b + y * 0.25
		}
		return b
	}`

	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	var err error
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

	beforeBlocks := len(fn.Blocks)
	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		t.Fatalf("UnrollAndJamPass: %v", err)
	}
	assertValidates(t, fn, "after UnrollAndJam")
	if got, want := len(fn.Blocks), beforeBlocks+4; got != want {
		t.Fatalf("block count = %d, want %d after unrolling two loops\nIR:\n%s", got, want, Print(fn))
	}

	for _, n := range []int64{0, 1, 2, 3, 7, 8} {
		for _, which := range []int64{0, 1} {
			args := []runtime.Value{runtime.IntValue(n), runtime.IntValue(which)}
			got, err := Interpret(fn, args)
			if err != nil {
				t.Fatalf("Interpret f(%d,%d): %v\nIR:\n%s", n, which, err, Print(fn))
			}
			want := runVM(t, src, args)
			assertValuesEqual(t, "f", got[0], want[0])
		}
	}
}

func TestUnrollAndJam_SecondStepKeepsLoopCounterMarker(t *testing.T) {
	src := `func f(n) {
		s := 0.0
		for i := 0; i < n; i++ {
			x := i + 1.0
			s = s + x * 0.5
		}
		return s
	}`

	proto := compileFunction(t, src)
	fn, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	assertValidates(t, fn, "after RunTier2Pipeline")

	var foundSecondStep bool
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpAddInt || len(instr.Args) != 2 || instr.Args[0] == nil {
				continue
			}
			first := instr.Args[0].Def
			if first == nil || first.Op != OpAddInt {
				continue
			}
			foundSecondStep = true
			if instr.Aux2 == 0 {
				t.Fatalf("unrolled second-step counter v%d lost loop-counter marker\nIR:\n%s", instr.ID, Print(fn))
			}
		}
	}
	if !foundSecondStep {
		t.Fatalf("expected unrolled second-step AddInt in optimized IR:\n%s", Print(fn))
	}
}

func TestUnrollAndJam_RejectsLoopBodyStores(t *testing.T) {
	src := `func f(n) {
		t := {}
		s := 0.0
		for i := 0; i < n; i++ {
			x := i + 1.0
			t[i] = x
			s = s + x * 0.5
		}
		return s
	}`

	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	var err error
	fn, err = TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass: %v", err)
	}
	fn, err = ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass: %v", err)
	}
	beforeBlocks := len(fn.Blocks)
	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		t.Fatalf("UnrollAndJamPass: %v", err)
	}
	assertValidates(t, fn, "after rejected UnrollAndJam")
	if len(fn.Blocks) != beforeBlocks {
		t.Fatalf("store loop should not be unrolled; before blocks=%d after=%d\nIR:\n%s", beforeBlocks, len(fn.Blocks), Print(fn))
	}
}
