//go:build darwin && arm64

package methodjit

import "testing"

func TestRedundantGuardElimination_StaticType(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	x := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b}
	guard := &Instr{
		ID:    fn.newValueID(),
		Op:    OpGuardType,
		Type:  TypeFloat,
		Args:  []*Value{x.Value()},
		Aux:   int64(TypeFloat),
		Block: b,
	}
	one := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 1, Block: b}
	add := &Instr{
		ID:    fn.newValueID(),
		Op:    OpAddFloat,
		Type:  TypeFloat,
		Args:  []*Value{guard.Value(), one.Value()},
		Block: b,
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{x, guard, one, add, ret}

	out, err := RedundantGuardEliminationPass(fn)
	if err != nil {
		t.Fatalf("RedundantGuardEliminationPass: %v", err)
	}
	if add.Args[0].ID != x.ID {
		t.Fatalf("expected guard use to be replaced with v%d, got v%d", x.ID, add.Args[0].ID)
	}

	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}
	for _, instr := range out.Entry.Instrs {
		if instr.Op == OpGuardType {
			t.Fatalf("redundant GuardType should have been removed, IR:\n%s", Print(out))
		}
	}
}
