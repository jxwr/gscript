package methodjit

import (
	"math"
	"testing"
)

func TestFloatScaleReuse_RewritesDoubleScale(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	one := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: int64(math.Float64bits(1.0)), Block: b}
	two := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: int64(math.Float64bits(2.0)), Block: b}
	unit := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Args: []*Value{one.Value(), x.Value()}, Block: b}
	double := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Args: []*Value{two.Value(), x.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{double.Value()}, Block: b}
	b.Instrs = []*Instr{x, one, two, unit, double, ret}

	if _, err := FloatScaleReusePass(fn); err != nil {
		t.Fatalf("FloatScaleReusePass: %v", err)
	}
	if double.Op != OpAddFloat {
		t.Fatalf("double scale op=%s, want AddFloat", double.Op)
	}
	if len(double.Args) != 2 || double.Args[0].ID != unit.ID || double.Args[1].ID != unit.ID {
		t.Fatalf("double scale args=%v, want unit value twice", double.Args)
	}
}

func TestFloatScaleReuse_NoRewriteWithoutUnitScale(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	two := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: int64(math.Float64bits(2.0)), Block: b}
	double := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Args: []*Value{two.Value(), x.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{double.Value()}, Block: b}
	b.Instrs = []*Instr{x, two, double, ret}

	if _, err := FloatScaleReusePass(fn); err != nil {
		t.Fatalf("FloatScaleReusePass: %v", err)
	}
	if double.Op != OpMulFloat {
		t.Fatalf("double scale op=%s, want MulFloat", double.Op)
	}
}
