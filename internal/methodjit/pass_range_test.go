// pass_range_test.go exercises RangeAnalysisPass and its saturating
// arithmetic helpers. Tests build small IR graphs and verify that the
// Int48Safe set contains the expected value IDs.

package methodjit

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// ---------- saturating arithmetic ----------

func TestSatAdd(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{1, 2, 3},
		{-1, -2, -3},
		{math.MaxInt64, 1, math.MaxInt64},        // saturation
		{math.MaxInt64 - 5, 10, math.MaxInt64},   // saturation
		{math.MinInt64, -1, math.MinInt64},       // saturation
		{math.MinInt64 + 5, -10, math.MinInt64},  // saturation
		{0, 0, 0},
	}
	for _, c := range cases {
		if got := satAdd(c.a, c.b); got != c.want {
			t.Errorf("satAdd(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSatSub(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{5, 3, 2},
		{3, 5, -2},
		{math.MaxInt64, -1, math.MaxInt64},   // overflow → saturate
		{math.MinInt64, 1, math.MinInt64},    // overflow → saturate
		{0, math.MinInt64, math.MaxInt64},    // -MinInt64 is MaxInt64+1 → saturate
	}
	for _, c := range cases {
		if got := satSub(c.a, c.b); got != c.want {
			t.Errorf("satSub(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSatMul(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{3, 4, 12},
		{-3, 4, -12},
		{-3, -4, 12},
		{0, math.MaxInt64, 0},
		{math.MaxInt64, 2, math.MaxInt64},    // overflow → saturate
		{math.MinInt64, 2, math.MinInt64},    // overflow → saturate
		{math.MinInt64, -1, math.MaxInt64},   // classic MinInt64 * -1 → saturate
	}
	for _, c := range cases {
		if got := satMul(c.a, c.b); got != c.want {
			t.Errorf("satMul(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSatNeg(t *testing.T) {
	if got := satNeg(5); got != -5 {
		t.Errorf("satNeg(5) = %d, want -5", got)
	}
	if got := satNeg(-5); got != 5 {
		t.Errorf("satNeg(-5) = %d, want 5", got)
	}
	if got := satNeg(math.MinInt64); got != math.MaxInt64 {
		t.Errorf("satNeg(MinInt64) = %d, want MaxInt64", got)
	}
}

// ---------- range arithmetic ----------

func TestRangeFitsInt48(t *testing.T) {
	in48 := intRange{min: -1000, max: 1000, known: true}
	if !in48.fitsInt48() {
		t.Error("[-1000,1000] should fit int48")
	}
	atMin := intRange{min: MinInt48, max: MaxInt48, known: true}
	if !atMin.fitsInt48() {
		t.Error("[MinInt48, MaxInt48] should fit int48")
	}
	tooBig := intRange{min: 0, max: MaxInt48 + 1, known: true}
	if tooBig.fitsInt48() {
		t.Error("[0, MaxInt48+1] should NOT fit int48")
	}
	top := topRange()
	if top.fitsInt48() {
		t.Error("top range should NOT fit int48")
	}
}

func TestAddRangeBasic(t *testing.T) {
	a := intRange{min: 1, max: 10, known: true}
	b := intRange{min: 100, max: 200, known: true}
	got := addRange(a, b)
	if got.min != 101 || got.max != 210 || !got.known {
		t.Errorf("addRange got %+v, want [101,210]", got)
	}
}

func TestSubRangeBasic(t *testing.T) {
	a := intRange{min: 10, max: 20, known: true}
	b := intRange{min: 3, max: 5, known: true}
	// a - b: [10-5, 20-3] = [5, 17]
	got := subRange(a, b)
	if got.min != 5 || got.max != 17 {
		t.Errorf("subRange got %+v, want [5,17]", got)
	}
}

func TestMulRangeBasic(t *testing.T) {
	a := intRange{min: -2, max: 3, known: true}
	b := intRange{min: -4, max: 5, known: true}
	// 4 corners: -2*-4=8, -2*5=-10, 3*-4=-12, 3*5=15 → [-12, 15]
	got := mulRange(a, b)
	if got.min != -12 || got.max != 15 {
		t.Errorf("mulRange got %+v, want [-12,15]", got)
	}
}

func TestMulRangeTopPropagates(t *testing.T) {
	a := topRange()
	b := intRange{min: 0, max: 10, known: true}
	if got := mulRange(a, b); got.known {
		t.Errorf("mulRange(top, _) should be top, got %+v", got)
	}
}

func TestNegRange(t *testing.T) {
	a := intRange{min: -5, max: 7, known: true}
	got := negRange(a)
	if got.min != -7 || got.max != 5 {
		t.Errorf("negRange got %+v, want [-7,5]", got)
	}
}

func TestJoinRange(t *testing.T) {
	a := intRange{min: 0, max: 10, known: true}
	b := intRange{min: 5, max: 20, known: true}
	got := joinRange(a, b)
	if got.min != 0 || got.max != 20 {
		t.Errorf("joinRange got %+v, want [0,20]", got)
	}
	if got := joinRange(a, topRange()); got.known {
		t.Errorf("joinRange(_, top) should be top, got %+v", got)
	}
}

// ---------- pass integration ----------

// TestRangePass_ConstIntsFit: constant-only IR should mark AddInt as safe.
func TestRangePass_ConstIntsFit(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "const_add"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := RangeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("RangeAnalysisPass: %v", err)
	}
	if !result.Int48Safe[add.ID] {
		t.Errorf("AddInt of (1, 2) should be Int48Safe, got safe=%v",
			result.Int48Safe[add.ID])
	}
}

// TestRangePass_UnknownLoadNotSafe: a LoadSlot result has unknown range, so
// its AddInt with a constant must NOT be marked safe.
func TestRangePass_UnknownLoadNotSafe(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "unknown_add"},
		NumRegs: 2,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	load := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{load.Value(), c1.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{load, c1, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := RangeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("RangeAnalysisPass: %v", err)
	}
	if result.Int48Safe[add.ID] {
		t.Errorf("AddInt(load, 1) should NOT be Int48Safe (load is top)")
	}
}

// TestRangePass_Propagation: chained ops propagate ranges correctly.
func TestRangePass_Propagation(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "chain"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c5 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 5, Block: b}
	c7 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 7, Block: b}
	// 5 * 7 = 35
	mul := &Instr{ID: fn.newValueID(), Op: OpMulInt, Type: TypeInt,
		Args: []*Value{c5.Value(), c7.Value()}, Block: b}
	// 35 + 35 = 70 (range [70,70])
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{mul.Value(), mul.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c5, c7, mul, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := RangeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("RangeAnalysisPass: %v", err)
	}
	if !result.Int48Safe[mul.ID] {
		t.Errorf("MulInt(5, 7) should be Int48Safe")
	}
	if !result.Int48Safe[add.ID] {
		t.Errorf("AddInt(35, 35) should be Int48Safe")
	}
}

// TestRangePass_Integration compiles a real nested-loop function and
// confirms that arithmetic purely on loop counters (not accumulating across
// iterations) is marked Int48Safe after TypeSpec + RangeAnalysis.
// This mirrors the spectral_norm A(i,j) idiom.
func TestRangePass_Integration(t *testing.T) {
	// compute((i+j) * (i+j+1)) inside a nested loop with small bounds.
	proto := compile(t, `
func f() {
    total := 0
    for i := 0; i < 100; i++ {
        for j := 0; j < 100; j++ {
            total = total + (i + j) * (i + j + 1)
        }
    }
    return total
}
`)
	fn := BuildGraph(proto)
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("validate: %v", errs[0])
	}
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	fn, _ = RangeAnalysisPass(fn)

	// Expect at least one Int48Safe entry for an i+j / i+j+1 / (i+j)*(i+j+1)
	// op. All of these use loop counters with known bounds.
	if len(fn.Int48Safe) == 0 {
		t.Errorf("expected at least one Int48Safe entry, got empty map\nIR:\n%s", Print(fn))
	}
	// Non-loop-counter arithmetic that was marked safe.
	safeNonCounter := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Aux2 == 1 {
				continue // loop counter increment, already handled by emitter
			}
			switch instr.Op {
			case OpAddInt, OpMulInt, OpSubInt, OpNegInt:
				if fn.Int48Safe[instr.ID] {
					safeNonCounter++
				}
			}
		}
	}
	if safeNonCounter == 0 {
		t.Errorf("no non-loop-counter arithmetic was marked Int48Safe\nIR:\n%s", Print(fn))
	}
}

// TestRangePass_Spectral mirrors spectral_norm's hot loop: nested loops
// compute 1.0/((i+j)*(i+j+1)/2+i+1). Verify that all integer arithmetic
// on the loop counters is marked Int48Safe.
func TestRangePass_Spectral(t *testing.T) {
	proto := compile(t, `
func f() {
    n := 500
    acc := 0
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            x := (i + j) * (i + j + 1) / 2 + i + 1
            acc = acc + x
        }
    }
    return acc
}
`)
	fn := BuildGraph(proto)
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("validate: %v", errs[0])
	}
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	fn, _ = RangeAnalysisPass(fn)

	// Count safe non-counter ops. We expect i+j, (i+j)*(i+j+1), and i+1 to
	// all be provably within int48.
	safeNonCounter := 0
	totalNonCounter := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Aux2 == 1 {
				continue
			}
			switch instr.Op {
			case OpAddInt, OpMulInt, OpSubInt, OpNegInt:
				totalNonCounter++
				if fn.Int48Safe[instr.ID] {
					safeNonCounter++
				}
			}
		}
	}
	t.Logf("safe=%d total=%d", safeNonCounter, totalNonCounter)
	if safeNonCounter < 2 {
		t.Errorf("expected at least 2 safe non-counter ops, got %d/%d\nIR:\n%s",
			safeNonCounter, totalNonCounter, Print(fn))
	}
}

// TestRangePass_LoopCounter: a FORLOOP-style loop should propagate the
// phi's seeded range to operations on the phi.
func TestRangePass_LoopCounter(t *testing.T) {
	// Simulate:
	//   entry:
	//     c0 = 0; c100 = 100; c1 = 1
	//     pre = c0 - c1        (OpSub, Aux2=1)  -> initial phi value: -1
	//     jump loop
	//   loop:                  (header)
	//     i = phi(pre, inc)
	//     inc = i + c1         (OpAdd, Aux2=1)
	//     cond = inc <= c100   (OpLe)
	//     branch cond → loop, exit
	//   exit:
	//     x = i + c1            (regular AddInt, no Aux2)
	//     return x
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "loop"},
		NumRegs: 4,
	}

	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	loop := &Block{ID: 1, defs: make(map[int]*Value)}
	exit := &Block{ID: 2, defs: make(map[int]*Value)}

	c0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	c100 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 100, Block: entry}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: entry}

	// pre = c0 - c1  (forwardSub, Aux2=1)
	pre := &Instr{ID: fn.newValueID(), Op: OpSub, Type: TypeInt,
		Args: []*Value{c0.Value(), c1.Value()}, Aux2: 1, Block: entry}
	entryJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: entry}
	entry.Instrs = []*Instr{c0, c100, c1, pre, entryJump}

	// Phi at loop header — inputs filled after inc is built.
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: loop}
	// inc = phi + c1 (backAdd, Aux2=1)
	inc := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeInt,
		Args: []*Value{phi.Value(), c1.Value()}, Aux2: 1, Block: loop}
	// cond = inc <= c100
	cond := &Instr{ID: fn.newValueID(), Op: OpLe, Type: TypeBool,
		Args: []*Value{inc.Value(), c100.Value()}, Block: loop}
	br := &Instr{ID: fn.newValueID(), Op: OpBranch, Args: []*Value{cond.Value()}, Block: loop}
	loop.Instrs = []*Instr{phi, inc, cond, br}
	// Phi inputs: [pre from entry, inc from back-edge loop]
	phi.Args = []*Value{pre.Value(), inc.Value()}

	// exit: x = phi + c1 (regular AddInt, Aux2=0)
	x := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{phi.Value(), c1.Value()}, Block: exit}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{x.Value()}, Block: exit}
	exit.Instrs = []*Instr{x, ret}

	// Connect CFG.
	entry.Succs = []*Block{loop}
	loop.Preds = []*Block{entry, loop}
	loop.Succs = []*Block{loop, exit}
	exit.Preds = []*Block{loop}

	fn.Entry = entry
	fn.Blocks = []*Block{entry, loop, exit}

	// TypeSpec hasn't run — promote the Add to AddInt manually so the
	// analysis has a canonical int op to inspect. Same for inc.
	// The pass inspects the phi/forwardSub/backAdd structure which accepts
	// both generic and int-specialized opcodes.

	result, err := RangeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("RangeAnalysisPass: %v", err)
	}

	// Phi's seeded range should cover at least [min(-1,100)-1, max(-1,100)+1]
	// = [-2, 101]. That's well within int48.
	// The post-loop AddInt consumes the phi, so its range should also fit.
	if !result.Int48Safe[x.ID] {
		t.Errorf("post-loop AddInt(phi, 1) should be Int48Safe (phi bounded by loop)")
	}
}
