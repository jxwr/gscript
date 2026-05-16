package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestIntAlgebraSimplify_RemovesSafeAddSubPair(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "safe_add_sub"}, NumRegs: 1}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: entry}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: entry}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{x.Value(), one.Value()}, Block: entry}
	sub := &Instr{ID: fn.newValueID(), Op: OpSubInt, Type: TypeInt, Args: []*Value{add.Value(), one.Value()}, Block: entry}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{sub.Value()}, Block: entry}
	entry.Instrs = []*Instr{x, one, add, sub, ret}
	fn.Int48Safe = map[int]bool{add.ID: true, sub.ID: true}

	out, err := IntAlgebraSimplifyPass(fn)
	if err != nil {
		t.Fatalf("IntAlgebraSimplifyPass: %v", err)
	}
	if sub.Op != OpNop {
		t.Fatalf("expected SubInt to be removed:\n%s", Print(out))
	}
	if ret.Args[0].ID != x.ID {
		t.Fatalf("return should use original base value, got v%d:\n%s", ret.Args[0].ID, Print(out))
	}
}

func TestIntAlgebraSimplify_KeepsUnsafeAddSubPair(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "unsafe_add_sub"}, NumRegs: 1}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: entry}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: entry}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{x.Value(), one.Value()}, Block: entry}
	sub := &Instr{ID: fn.newValueID(), Op: OpSubInt, Type: TypeInt, Args: []*Value{add.Value(), one.Value()}, Block: entry}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{sub.Value()}, Block: entry}
	entry.Instrs = []*Instr{x, one, add, sub, ret}
	fn.Int48Safe = map[int]bool{sub.ID: true}

	out, err := IntAlgebraSimplifyPass(fn)
	if err != nil {
		t.Fatalf("IntAlgebraSimplifyPass: %v", err)
	}
	if sub.Op != OpSubInt {
		t.Fatalf("unsafe pair should remain:\n%s", Print(out))
	}
}
