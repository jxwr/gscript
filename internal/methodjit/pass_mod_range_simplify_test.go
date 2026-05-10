package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestModRangeSimplify_ReplacesKnownBelowDivisor(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "mod_identity"}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	d := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 10, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt, Args: []*Value{x.Value(), d.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{mod.Value()}, Block: b}
	b.Instrs = []*Instr{x, d, mod, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	fn.IntRanges = map[int]intRange{x.ID: {min: 0, max: 9, known: true}}

	out, err := ModRangeSimplifyPass(fn)
	if err != nil {
		t.Fatalf("ModRangeSimplifyPass: %v", err)
	}
	if mod.Op != OpNop {
		t.Fatalf("mod should be removed, got %s\n%s", mod.Op, Print(out))
	}
	if len(ret.Args) != 1 || ret.Args[0].ID != x.ID {
		t.Fatalf("return should use original dividend:\n%s", Print(out))
	}
}

func TestModRangeSimplify_KeepsPossibleWrap(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "mod_wrap"}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	d := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 10, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt, Args: []*Value{x.Value(), d.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{mod.Value()}, Block: b}
	b.Instrs = []*Instr{x, d, mod, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	fn.IntRanges = map[int]intRange{x.ID: {min: 0, max: 10, known: true}}

	out, err := ModRangeSimplifyPass(fn)
	if err != nil {
		t.Fatalf("ModRangeSimplifyPass: %v", err)
	}
	if mod.Op != OpModInt {
		t.Fatalf("mod should remain, got %s\n%s", mod.Op, Print(out))
	}
}

func TestModRangeSimplify_FoldsModuloOne(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "mod_one"}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	x := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	d := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt, Args: []*Value{x.Value(), d.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{mod.Value()}, Block: b}
	b.Instrs = []*Instr{x, d, mod, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	fn.IntRanges = map[int]intRange{x.ID: {min: -100, max: 100, known: true}}

	out, err := ModRangeSimplifyPass(fn)
	if err != nil {
		t.Fatalf("ModRangeSimplifyPass: %v", err)
	}
	if mod.Op != OpConstInt || mod.Aux != 0 {
		t.Fatalf("x %% 1 should fold to ConstInt 0:\n%s", Print(out))
	}
}
