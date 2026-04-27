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

func TestTablePreallocHintPassInfersLocalTypedArrayWithoutFeedback(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_local_int"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), key.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, key, val, set, get}
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
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayInt {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayInt)
	}
	if set.Aux2 != int64(vm.FBKindInt) {
		t.Fatalf("set Aux2 = %d, want FBKindInt", set.Aux2)
	}
	if get.Aux2 != int64(vm.FBKindInt) {
		t.Fatalf("get Aux2 = %d, want FBKindInt", get.Aux2)
	}
}

func TestTablePreallocHintPassUsesObservedMaxIntKeyAndCarriesKind(t *testing.T) {
	proto := &vm.FuncProto{
		Name:             "prealloc_bool_range",
		Code:             make([]uint32, 4),
		TableKeyFeedback: vm.NewTableKeyFeedbackVector(4),
	}
	proto.TableKeyFeedback[2].ObserveIntKey(runtime.IntValue(4096))
	fn := &Function{Proto: proto, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Aux2: 3, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeBool, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Aux2: int64(vm.FBKindBool),
		Block: b, HasSource: true, SourcePC: 2}
	b.Instrs = []*Instr{tbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	newTable := got.Entry.Instrs[0]
	if newTable.Aux != 4097 {
		t.Fatalf("array hint = %d, want 4097", newTable.Aux)
	}
	hashHint, kind := unpackNewTableAux2(newTable.Aux2)
	if hashHint != 3 {
		t.Fatalf("hash hint = %d, want 3", hashHint)
	}
	if kind != runtime.ArrayBool {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayBool)
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

func TestTablePreallocHintPassOuterLoopFillGetsLargeTypedHint(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "outer_loop_fill"}, NumRegs: 1}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	header := &Block{ID: 1, defs: make(map[int]*Value)}
	body := &Block{ID: 2, defs: make(map[int]*Value)}
	exit := &Block{ID: 3, defs: make(map[int]*Value)}
	fn.Entry = entry
	fn.Blocks = []*Block{entry, header, body, exit}

	entry.Succs = []*Block{header}
	header.Preds = []*Block{entry, body}
	header.Succs = []*Block{body, exit}
	body.Preds = []*Block{header}
	body.Succs = []*Block{header}
	exit.Preds = []*Block{header}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: entry}
	entry.Instrs = []*Instr{
		tbl,
		{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(header.ID), Block: entry},
	}
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: header}
	header.Instrs = []*Instr{
		phi,
		cond,
		{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Args: []*Value{cond.Value()}, Aux: int64(body.ID), Aux2: int64(exit.ID), Block: header},
	}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 7, Block: body}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), phi.Value(), val.Value()}, Aux2: int64(vm.FBKindInt), Block: body}
	body.Instrs = []*Instr{
		val,
		set,
		{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(header.ID), Block: body},
	}
	exit.Instrs = []*Instr{{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: exit}}
	phi.Args = []*Value{val.Value(), val.Value()}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}
	newTable := got.Entry.Instrs[0]
	if newTable.Aux != tier2FeedbackOuterLoopArrayHint {
		t.Fatalf("outer loop fill hint = %d, want %d", newTable.Aux, tier2FeedbackOuterLoopArrayHint)
	}
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayInt {
		t.Fatalf("outer loop typed kind = %d, want %d", kind, runtime.ArrayInt)
	}
}

func TestTablePreallocHintPassLoopLocalTableKeepsSmallHint(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "loop_local_table"}, NumRegs: 1}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	header := &Block{ID: 1, defs: make(map[int]*Value)}
	body := &Block{ID: 2, defs: make(map[int]*Value)}
	exit := &Block{ID: 3, defs: make(map[int]*Value)}
	fn.Entry = entry
	fn.Blocks = []*Block{entry, header, body, exit}

	entry.Succs = []*Block{header}
	header.Preds = []*Block{entry, body}
	header.Succs = []*Block{body, exit}
	body.Preds = []*Block{header}
	body.Succs = []*Block{header}
	exit.Preds = []*Block{header}

	entry.Instrs = []*Instr{{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(header.ID), Block: entry}}
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: header}
	header.Instrs = []*Instr{
		phi,
		cond,
		{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Args: []*Value{cond.Value()}, Aux: int64(body.ID), Aux2: int64(exit.ID), Block: header},
	}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: body}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 7, Block: body}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), phi.Value(), val.Value()}, Aux2: int64(vm.FBKindInt), Block: body}
	body.Instrs = []*Instr{
		tbl,
		val,
		set,
		{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(header.ID), Block: body},
	}
	exit.Instrs = []*Instr{{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: exit}}
	phi.Args = []*Value{val.Value(), val.Value()}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}
	newTable := got.Blocks[2].Instrs[0]
	if newTable.Aux != tier2FeedbackArrayHint {
		t.Fatalf("loop-local table hint = %d, want %d", newTable.Aux, tier2FeedbackArrayHint)
	}
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayInt {
		t.Fatalf("loop-local typed kind = %d, want %d", kind, runtime.ArrayInt)
	}
}
