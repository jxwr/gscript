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

func instrIndex(block *Block, target *Instr) int {
	for i, instr := range block.Instrs {
		if instr == target {
			return i
		}
	}
	return -1
}
