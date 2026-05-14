package methodjit

import (
	"math"
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
	if len(fn.Blocks) != beforeBlocks+6 {
		t.Fatalf("block count = %d, want %d after unroll tail blocks\nIR:\n%s", len(fn.Blocks), beforeBlocks+6, Print(fn))
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

func TestUnrollAndJam_DivFloatReductionUsesSplitAccumulator(t *testing.T) {
	src := `func f(n) {
		s := 0.0
		for i := 0; i < n; i++ {
			x := i + 1.0
			s = s + 1.0 / (x * x + 1.0)
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

	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		t.Fatalf("UnrollAndJamPass: %v", err)
	}
	assertValidates(t, fn, "after split-accumulator UnrollAndJam")
	ir := Print(fn)
	if !strings.Contains(ir, "split-accumulator") && strings.Count(ir, "Phi") < 3 {
		t.Fatalf("expected extra accumulator phi after split unroll, IR:\n%s", ir)
	}
	if strings.Count(ir, "DivFloat") < 2 {
		t.Fatalf("expected cloned high-latency division body, IR:\n%s", ir)
	}

	for _, n := range []int64{0, 1, 2, 3, 7, 8, 17} {
		got, err := Interpret(fn, []runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Interpret f(%d): %v\nIR:\n%s", n, err, ir)
		}
		want := runVM(t, src, []runtime.Value{runtime.IntValue(n)})
		if len(got) == 0 || len(want) == 0 || !got[0].IsFloat() || !want[0].IsFloat() {
			t.Fatalf("f(%d) result types got=%v want=%v", n, got, want)
		}
		if math.Abs(got[0].Float()-want[0].Float()) > 1e-12 {
			t.Fatalf("f(%d)=%0.17g want %0.17g\nIR:\n%s", n, got[0].Float(), want[0].Float(), ir)
		}
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

func TestUnrollAndJam_SqrtCompanionTailCarriesRecurrence(t *testing.T) {
	src := `func f(n) {
		sum := 0.0
		x := 0.0
		y := 0.0
		for i := 1; i <= n; i++ {
			dx := 1.0 * i - x
			dy := 2.0 * i - y
			sum = sum + math.sqrt(dx * dx + dy * dy)
			x = (x + 0.1) * 0.999
			y = (y + 0.2) * 0.999
		}
		return sum
	}`

	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	var err error
	fn, err = TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass: %v", err)
	}
	fn, _ = IntrinsicPass(fn)
	fn, err = TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass post-intrinsic: %v", err)
	}
	fn, err = FMAFusionPass(fn)
	if err != nil {
		t.Fatalf("FMAFusionPass: %v", err)
	}
	fn, err = ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass: %v", err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}

	fn, err = UnrollAndJamPass(fn)
	if err != nil {
		t.Fatalf("UnrollAndJamPass: %v", err)
	}
	assertValidates(t, fn, "after sqrt companion UnrollAndJam")
	ir := Print(fn)
	if strings.Count(ir, "Sqrt") < 8 {
		t.Fatalf("expected latency-wide unroll for sqrt recurrence loop, IR:\n%s", ir)
	}

	for _, n := range []int64{0, 1, 2, 3, 7, 8, 9, 15, 17} {
		got, err := Interpret(fn, []runtime.Value{runtime.IntValue(n)})
		if err != nil {
			t.Fatalf("Interpret f(%d): %v\nIR:\n%s", n, err, ir)
		}
		if len(got) == 0 || !got[0].IsFloat() {
			t.Fatalf("f(%d) result type got=%v", n, got)
		}
		want := sqrtCompanionTailExpected(n)
		if math.Abs(got[0].Float()-want) > 1e-10 {
			t.Fatalf("f(%d)=%0.17g want %0.17g\nIR:\n%s", n, got[0].Float(), want, ir)
		}
	}
}

func sqrtCompanionTailExpected(n int64) float64 {
	sum, x, y := 0.0, 0.0, 0.0
	for i := int64(1); i <= n; i++ {
		dx := 1.0*float64(i) - x
		dy := 2.0*float64(i) - y
		sum += math.Sqrt(dx*dx + dy*dy)
		x = (x + 0.1) * 0.999
		y = (y + 0.2) * 0.999
	}
	return sum
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
	if got, want := len(fn.Blocks), beforeBlocks+12; got != want {
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
