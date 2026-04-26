package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTablePreallocHintPassCarriesFloatKind(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_float"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Aux2: int64(vm.FBKindFloat), Block: b}
	b.Instrs = []*Instr{tbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	newTable := got.Entry.Instrs[0]
	if newTable.Aux != tier2FeedbackArrayHint {
		t.Fatalf("array hint = %d, want %d", newTable.Aux, tier2FeedbackArrayHint)
	}
	hashHint, kind := unpackNewTableAux2(newTable.Aux2)
	if hashHint != 0 {
		t.Fatalf("hash hint = %d, want 0", hashHint)
	}
	if kind != runtime.ArrayFloat {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayFloat)
	}
}

func TestTablePreallocHintPassKeepsMixedForTableValues(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_mixed"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Aux2: int64(vm.FBKindMixed), Block: b}
	b.Instrs = []*Instr{tbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	newTable := got.Entry.Instrs[0]
	if newTable.Aux != tier2FeedbackArrayHint {
		t.Fatalf("array hint = %d, want %d", newTable.Aux, tier2FeedbackArrayHint)
	}
	hashHint, kind := unpackNewTableAux2(newTable.Aux2)
	if hashHint != 0 {
		t.Fatalf("hash hint = %d, want 0", hashHint)
	}
	if kind != runtime.ArrayMixed {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayMixed)
	}
}
