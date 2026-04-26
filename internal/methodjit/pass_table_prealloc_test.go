package methodjit

import "testing"

func TestTablePreallocHintPassUsesTypedIntegerKeyEvidence(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass error: %v", err)
	}

	if got.Entry.Instrs[0].Aux != tier2FeedbackArrayHint {
		t.Fatalf("NewTable Aux = %d, want prealloc hint %d", got.Entry.Instrs[0].Aux, tier2FeedbackArrayHint)
	}
}

func TestTablePreallocHintPassSkipsNonIntegerKeyEvidence(t *testing.T) {
	fn := &Function{}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass error: %v", err)
	}

	if got.Entry.Instrs[0].Aux != 0 {
		t.Fatalf("NewTable Aux = %d, want no prealloc hint", got.Entry.Instrs[0].Aux)
	}
}
