package methodjit

import (
	"math"
	"testing"
)

func TestFloatStrengthReduction_DivByIntPowerOfTwo(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	two := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	div := &Instr{ID: fn.newValueID(), Op: OpDivFloat, Type: TypeFloat, Args: []*Value{x.Value(), two.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{div.Value()}, Block: b}
	b.Instrs = []*Instr{x, two, div, ret}

	out, err := FloatStrengthReductionPass(fn)
	if err != nil {
		t.Fatalf("FloatStrengthReductionPass: %v", err)
	}
	if out != fn {
		t.Fatalf("pass returned a different function")
	}
	if div.Op != OpMulFloat {
		t.Fatalf("division was not rewritten:\n%s", Print(fn))
	}
	if len(div.Args) != 2 || div.Args[0].ID != x.ID {
		t.Fatalf("rewritten multiply has wrong args: %#v", div.Args)
	}
	recip := div.Args[1].Def
	if recip == nil || recip.Op != OpConstFloat {
		t.Fatalf("expected reciprocal ConstFloat, got %#v", recip)
	}
	if got := math.Float64frombits(uint64(recip.Aux)); got != 0.5 {
		t.Fatalf("reciprocal = %v, want 0.5", got)
	}
	if idxConst, idxMul := instrIndex(b, recip), instrIndex(b, div); idxConst < 0 || idxMul < 0 || idxConst >= idxMul {
		t.Fatalf("reciprocal const must be inserted before mul, indexes const=%d mul=%d", idxConst, idxMul)
	}
}

func TestFloatStrengthReduction_DoesNotRewriteNonPowerOfTwo(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	three := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}
	div := &Instr{ID: fn.newValueID(), Op: OpDivFloat, Type: TypeFloat, Args: []*Value{x.Value(), three.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{div.Value()}, Block: b}
	b.Instrs = []*Instr{x, three, div, ret}

	if _, err := FloatStrengthReductionPass(fn); err != nil {
		t.Fatalf("FloatStrengthReductionPass: %v", err)
	}
	if div.Op != OpDivFloat {
		t.Fatalf("non-power-of-two divisor should remain DivFloat, got %s", div.Op)
	}
}

func TestFloatStrengthReduction_ExposesFMA(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	y := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Aux: 1, Block: b}
	two := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	div := &Instr{ID: fn.newValueID(), Op: OpDivFloat, Type: TypeFloat, Args: []*Value{x.Value(), two.Value()}, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Args: []*Value{div.Value(), y.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{x, y, two, div, add, ret}

	if _, err := FloatStrengthReductionPass(fn); err != nil {
		t.Fatalf("FloatStrengthReductionPass: %v", err)
	}
	if div.Op != OpMulFloat {
		t.Fatalf("division was not rewritten:\n%s", Print(fn))
	}
	if _, err := FMAFusionPass(fn); err != nil {
		t.Fatalf("FMAFusionPass: %v", err)
	}
	if add.Op != OpFMA {
		t.Fatalf("post-strength-reduction add was not fused:\n%s", Print(fn))
	}
	if len(add.Args) != 3 || add.Args[0].ID != x.ID || add.Args[2].ID != y.ID {
		t.Fatalf("fused FMA has wrong args: %#v", add.Args)
	}
}

func TestFMAFusion_SubFloatMinusSingleUseMulBecomesFMSUB(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	acc := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Aux: 0, Block: b}
	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Aux: 1, Block: b}
	y := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Aux: 2, Block: b}
	mul := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Args: []*Value{x.Value(), y.Value()}, Block: b}
	sub := &Instr{ID: fn.newValueID(), Op: OpSubFloat, Type: TypeFloat, Args: []*Value{acc.Value(), mul.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{sub.Value()}, Block: b}
	b.Instrs = []*Instr{acc, x, y, mul, sub, ret}

	if _, err := FMAFusionPass(fn); err != nil {
		t.Fatalf("FMAFusionPass: %v", err)
	}
	if sub.Op != OpFMSUB {
		t.Fatalf("sub-minus-mul was not fused:\n%s", Print(fn))
	}
	if len(sub.Args) != 3 || sub.Args[0].ID != x.ID || sub.Args[1].ID != y.ID || sub.Args[2].ID != acc.ID {
		t.Fatalf("fused FMSUB has wrong args: %#v", sub.Args)
	}
	if mul.Op != OpNop {
		t.Fatalf("single-use mul should be nopped after fusion, got %s", mul.Op)
	}
}

func instrIndex(block *Block, target *Instr) int {
	for i, instr := range block.Instrs {
		if instr == target {
			return i
		}
	}
	return -1
}
