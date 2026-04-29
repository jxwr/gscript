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

func TestTablePreallocHintPassFollowsSingleGlobalNewTable(t *testing.T) {
	const globalSlot int64 = 8
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_global_float"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	storeGlobal := &Instr{ID: fn.newValueID(), Op: OpSetGlobal, Type: TypeUnknown,
		Args: []*Value{tbl.Value()}, Aux: globalSlot, Block: b}
	globalTbl := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Aux: globalSlot, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 0, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{globalTbl.Value(), key.Value(), val.Value()}, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{globalTbl.Value(), key.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, storeGlobal, globalTbl, key, val, set, get}
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
	if kind != runtime.ArrayFloat {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayFloat)
	}
	if set.Aux2 != int64(vm.FBKindFloat) {
		t.Fatalf("set Aux2 = %d, want FBKindFloat", set.Aux2)
	}
	if get.Aux2 != int64(vm.FBKindFloat) {
		t.Fatalf("get Aux2 = %d, want FBKindFloat", get.Aux2)
	}
}

func TestTablePreallocHintPassKeepsGlobalLoopDefaultSmallWithoutFeedback(t *testing.T) {
	const globalSlot int64 = 8
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_global_loop_float"}, NumRegs: 3}
	entry, header, body, exit := buildSimpleLoop(fn)
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: entry}
	storeGlobal := &Instr{ID: fn.newValueID(), Op: OpSetGlobal, Type: TypeUnknown,
		Args: []*Value{tbl.Value()}, Aux: globalSlot, Block: entry}
	entryJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: entry, Aux: int64(header.ID)}
	entry.Instrs = []*Instr{tbl, storeGlobal, entryJump}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: header}
	headerBranch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown,
		Args: []*Value{cond.Value()}, Block: header, Aux: int64(body.ID), Aux2: int64(exit.ID)}
	header.Instrs = []*Instr{phi, cond, headerBranch}

	globalTbl := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Aux: globalSlot, Block: body}
	val := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 0, Block: body}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{globalTbl.Value(), phi.Value(), val.Value()}, Block: body}
	bodyJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: body, Aux: int64(header.ID)}
	body.Instrs = []*Instr{globalTbl, val, set, bodyJump}
	exit.Instrs = []*Instr{{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: exit}}
	phi.Args = []*Value{val.Value(), val.Value()}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	newTable := got.Entry.Instrs[0]
	if newTable.Aux != tier2FeedbackArrayHint {
		t.Fatalf("array hint = %d, want small default %d", newTable.Aux, tier2FeedbackArrayHint)
	}
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayFloat {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayFloat)
	}
	if set.Aux2 != int64(vm.FBKindFloat) {
		t.Fatalf("set Aux2 = %d, want FBKindFloat", set.Aux2)
	}
}

func TestTablePreallocHintPassDoesNotFollowAmbiguousGlobalNewTable(t *testing.T) {
	const globalSlot int64 = 8
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_global_ambiguous"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	first := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	storeFirst := &Instr{ID: fn.newValueID(), Op: OpSetGlobal, Type: TypeUnknown,
		Args: []*Value{first.Value()}, Aux: globalSlot, Block: b}
	second := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	storeSecond := &Instr{ID: fn.newValueID(), Op: OpSetGlobal, Type: TypeUnknown,
		Args: []*Value{second.Value()}, Aux: globalSlot, Block: b}
	globalTbl := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Aux: globalSlot, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 0, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{globalTbl.Value(), key.Value(), val.Value()}, Block: b}
	b.Instrs = []*Instr{first, storeFirst, second, storeSecond, globalTbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	if got.Entry.Instrs[0].Aux != 0 {
		t.Fatalf("first table array hint = %d, want 0", got.Entry.Instrs[0].Aux)
	}
	if got.Entry.Instrs[2].Aux != 0 {
		t.Fatalf("second table array hint = %d, want 0", got.Entry.Instrs[2].Aux)
	}
	if set.Aux2 != 0 {
		t.Fatalf("set Aux2 = %d, want 0", set.Aux2)
	}
}

func TestTablePreallocHintPassRecoversDefsByValueID(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_missing_defs"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 0, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{{ID: tbl.ID}, {ID: key.ID}, {ID: val.ID}}, Block: b}
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
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayFloat {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayFloat)
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

func TestTablePreallocHintPassAddsHeadroomForLargeOuterLoopFeedback(t *testing.T) {
	const sourcePC = 2
	observedMax := int64(tier2FeedbackOuterLoopArrayHint + 8191)
	proto := &vm.FuncProto{
		Name:             "prealloc_large_loop",
		Code:             make([]uint32, 4),
		TableKeyFeedback: vm.NewTableKeyFeedbackVector(4),
	}
	proto.TableKeyFeedback[sourcePC].ObserveIntKey(runtime.IntValue(observedMax))

	fn := &Function{Proto: proto, NumRegs: 3}
	entry, header, body, exit := buildSimpleLoop(fn)
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: entry}
	entryJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: entry, Aux: int64(header.ID)}
	entry.Instrs = []*Instr{tbl, entryJump}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: header, Aux: 1}
	headerBranch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Args: []*Value{cond.Value()}, Block: header, Aux: int64(body.ID), Aux2: int64(exit.ID)}
	header.Instrs = []*Instr{phi, cond, headerBranch}

	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: body}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: body}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Aux2: int64(vm.FBKindInt),
		Block: body, HasSource: true, SourcePC: sourcePC}
	bodyJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: body, Aux: int64(header.ID)}
	body.Instrs = []*Instr{key, val, set, bodyJump}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: exit, Args: []*Value{tbl.Value()}}
	exit.Instrs = []*Instr{ret}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	newTable := got.Entry.Instrs[0]
	want := (observedMax + 1) * 2
	if newTable.Aux != want {
		t.Fatalf("array hint = %d, want %d", newTable.Aux, want)
	}
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayInt {
		t.Fatalf("array kind = %d, want %d", kind, runtime.ArrayInt)
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

func TestTablePreallocHintPassInfersMixedForTableValuesWithoutFeedback(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "prealloc_table_values"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Block: b}
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

func TestTablePreallocHintPassOuterLoopTableValuesGetsLargeMixedHint(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "outer_loop_table_values"}, NumRegs: 1}
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
	val := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: body}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{tbl.Value(), phi.Value(), val.Value()}, Block: body}
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
		t.Fatalf("outer loop table-value hint = %d, want %d", newTable.Aux, tier2FeedbackOuterLoopArrayHint)
	}
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayMixed {
		t.Fatalf("outer loop table-value kind = %d, want %d", kind, runtime.ArrayMixed)
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

func TestTablePreallocHintPassPolymorphicFeedbackForcesMixedPrealloc(t *testing.T) {
	fn := &Function{
		Proto: &vm.FuncProto{
			Name:             "prealloc_poly_kind",
			Feedback:         make([]vm.TypeFeedback, 3),
			TableKeyFeedback: make([]vm.TableKeyFeedback, 3),
		},
		NumRegs: 3,
	}
	fn.Proto.Feedback[2].Kind = vm.FBKindPolymorphic
	fn.Proto.TableKeyFeedback[2] = vm.TableKeyFeedback{HasIntKey: true, MaxIntKey: 49999}

	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	set := &Instr{
		ID:        fn.newValueID(),
		Op:        OpSetTable,
		Type:      TypeUnknown,
		Args:      []*Value{tbl.Value(), key.Value(), val.Value()},
		Block:     b,
		HasSource: true,
		SourcePC:  2,
	}
	b.Instrs = []*Instr{tbl, key, val, set}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	got, err := TablePreallocHintPass(fn)
	if err != nil {
		t.Fatalf("TablePreallocHintPass: %v", err)
	}

	newTable := got.Entry.Instrs[0]
	if newTable.Aux != 50000 {
		t.Fatalf("array hint = %d, want 50000", newTable.Aux)
	}
	_, kind := unpackNewTableAux2(newTable.Aux2)
	if kind != runtime.ArrayMixed {
		t.Fatalf("array kind = %d, want mixed", kind)
	}
	if set.Aux2 != 0 {
		t.Fatalf("set Aux2 = %d, want 0 for polymorphic feedback", set.Aux2)
	}
}
