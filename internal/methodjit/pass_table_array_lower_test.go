package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTableArrayLower_LoadElimSharesHeaderLenData(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_cse"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	k1 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	k2 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	g1 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), k1.Value()}, Block: b}
	g2 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), k2.Value()}, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{g1.Value(), g2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, k1, k2, g1, g2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = LoadEliminationPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	counts := countOps(fn)
	if counts[OpTableArrayHeader] != 1 || counts[OpTableArrayLen] != 1 || counts[OpTableArrayData] != 1 {
		t.Fatalf("expected shared header/len/data after CSE, counts=%v\n%s", counts, Print(fn))
	}
	if counts[OpTableArrayLoad] != 2 {
		t.Fatalf("expected two loads, got %d\n%s", counts[OpTableArrayLoad], Print(fn))
	}
}

func TestTableArrayLower_SetsScalarElementTypeFromKind(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_scalar_type"}, NumRegs: 2}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny, Aux2: int64(vm.FBKindFloat),
		Args: []*Value{tbl.Value(), key.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{get.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, key, get, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	out, err := TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	var load *Instr
	for _, instr := range out.Entry.Instrs {
		if instr.Op == OpTableArrayLoad {
			load = instr
			break
		}
	}
	if load == nil {
		t.Fatalf("expected lowered TableArrayLoad:\n%s", Print(out))
	}
	if load.Type != TypeFloat {
		t.Fatalf("lowered float-array load Type=%s, want float:\n%s", load.Type, Print(out))
	}
}

func TestTableArrayLower_PreservesMixedArrayFeedbackResultType(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_mixed_result_type"}, NumRegs: 2}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeTable, Aux2: int64(vm.FBKindMixed),
		Args: []*Value{tbl.Value(), key.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{get.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, key, get, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	out, err := TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	var load *Instr
	for _, instr := range out.Entry.Instrs {
		if instr.Op == OpTableArrayLoad {
			load = instr
			break
		}
	}
	if load == nil {
		t.Fatalf("expected lowered TableArrayLoad:\n%s", Print(out))
	}
	if load.Type != TypeTable {
		t.Fatalf("mixed-array row load Type=%s, want preserved table:\n%s", load.Type, Print(out))
	}
}

func TestTableArrayLower_LICMHoistsHeaderLenData(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_licm"}, NumRegs: 2}
	entry, header, body, exit := buildSimpleLoop(fn)

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	j0 := &Instr{ID: fn.newValueID(), Op: OpJump, Block: entry}
	entry.Instrs = []*Instr{tbl, seed, j0}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: header}
	br := &Instr{ID: fn.newValueID(), Op: OpBranch, Args: []*Value{cond.Value()}, Block: header}
	header.Instrs = []*Instr{phi, cond, br}

	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), phi.Value()}, Block: body}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: body}
	next := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{phi.Value(), one.Value()}, Block: body}
	jb := &Instr{ID: fn.newValueID(), Op: OpJump, Block: body}
	body.Instrs = []*Instr{get, one, next, jb}
	phi.Args = []*Value{seed.Value(), next.Value()}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{get.Value()}, Block: exit}
	exit.Instrs = []*Instr{ret}
	assertValidates(t, fn, "input")

	var err error
	fn, err = TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = LoadEliminationPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = LICMPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	if blockHasOp(body, OpTableArrayHeader) || blockHasOp(body, OpTableArrayLen) || blockHasOp(body, OpTableArrayData) {
		t.Fatalf("header/len/data should have been hoisted out of loop body:\n%s", Print(fn))
	}
	if !blockHasOp(header.Preds[0], OpTableArrayHeader) ||
		!blockHasOp(header.Preds[0], OpTableArrayLen) ||
		!blockHasOp(header.Preds[0], OpTableArrayData) {
		t.Fatalf("preheader should contain hoisted table array facts:\n%s", Print(fn))
	}
}

func TestTableArrayLower_PostLICMCSESharesCrossBlockHoistedFacts(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_post_licm_cse"}, NumRegs: 3}
	entry := newBlock(0)
	header := newBlock(1)
	bodyA := newBlock(2)
	bodyB := newBlock(3)
	exit := newBlock(4)
	fn.Entry = entry
	fn.Blocks = []*Block{entry, header, bodyA, bodyB, exit}

	entry.Succs = []*Block{header}
	header.Preds = []*Block{entry, bodyB}
	header.Succs = []*Block{bodyA, exit}
	bodyA.Preds = []*Block{header}
	bodyA.Succs = []*Block{bodyB}
	bodyB.Preds = []*Block{bodyA}
	bodyB.Succs = []*Block{header}
	exit.Preds = []*Block{header}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	seedI := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	seedAcc := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	entryJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: entry, Aux: int64(header.ID)}
	entry.Instrs = []*Instr{tbl, seedI, seedAcc, entryJump}

	iPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	accPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: header}
	headerBranch := &Instr{ID: fn.newValueID(), Op: OpBranch, Args: []*Value{cond.Value()}, Block: header, Aux: int64(bodyA.ID), Aux2: int64(exit.ID)}
	header.Instrs = []*Instr{iPhi, accPhi, cond, headerBranch}

	getA := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), iPhi.Value()}, Block: bodyA}
	bodyAJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: bodyA, Aux: int64(bodyB.ID)}
	bodyA.Instrs = []*Instr{getA, bodyAJump}

	getB := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), iPhi.Value()}, Block: bodyB}
	pair := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{getA.Value(), getB.Value()}, Block: bodyB}
	accNext := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{accPhi.Value(), pair.Value()}, Block: bodyB}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: bodyB}
	iNext := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{iPhi.Value(), one.Value()}, Block: bodyB}
	bodyBJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: bodyB, Aux: int64(header.ID)}
	bodyB.Instrs = []*Instr{getB, pair, accNext, one, iNext, bodyBJump}
	iPhi.Args = []*Value{seedI.Value(), iNext.Value()}
	accPhi.Args = []*Value{seedAcc.Value(), accNext.Value()}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{accPhi.Value()}, Block: exit}
	exit.Instrs = []*Instr{ret}
	assertValidates(t, fn, "input")

	var err error
	fn, err = TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = LoadEliminationPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = LICMPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	preheader := header.Preds[0]
	if got := countBlockOps(preheader, OpTableArrayHeader); got != 2 {
		t.Fatalf("expected LICM to co-locate two cross-block headers before post-LICM CSE, got %d:\n%s", got, Print(fn))
	}

	fn, err = LoadEliminationPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	counts := countOps(fn)
	if counts[OpTableArrayHeader] != 1 || counts[OpTableArrayLen] != 1 || counts[OpTableArrayData] != 1 {
		t.Fatalf("expected post-LICM CSE to share hoisted header/len/data, counts=%v\n%s", counts, Print(fn))
	}
	if counts[OpTableArrayLoad] != 2 {
		t.Fatalf("expected two table array element loads to remain, got %d\n%s", counts[OpTableArrayLoad], Print(fn))
	}
	assertValidates(t, fn, "after post-LICM CSE")
}

func TestTableArrayLower_LoadElimInvalidatesFactsAcrossTableMutation(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_mutation_invalidates_cse"}, NumRegs: 4}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	k1 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	k2 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 42, Block: b}
	g1 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), k1.Value()}, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Args: []*Value{tbl.Value(), k1.Value(), val.Value()}, Block: b}
	g2 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), k2.Value()}, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{g1.Value(), g2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, k1, k2, val, g1, set, g2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = LoadEliminationPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	counts := countOps(fn)
	if counts[OpTableArrayHeader] != 2 || counts[OpTableArrayLen] != 2 || counts[OpTableArrayData] != 2 {
		t.Fatalf("table mutation should invalidate earlier typed array facts, counts=%v\n%s", counts, Print(fn))
	}
}

func TestTableArrayLower_SetTableBeforeSameTableReadStillLowers(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_set_before_read"}, NumRegs: 4}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	writeKey := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	readKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), writeKey.Value(), val.Value()}, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), readKey.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{get.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, writeKey, readKey, val, set, get, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	out, err := TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	counts := countOps(out)
	if counts[OpTableArrayLoad] != 1 || counts[OpGetTable] != 0 {
		t.Fatalf("SetTable-before-read should still use typed TableArrayLoad, counts=%v\n%s", counts, Print(out))
	}
	if counts[OpSetTable] != 1 {
		t.Fatalf("SetTable should remain, counts=%v\n%s", counts, Print(out))
	}
}

func TestTableArrayLower_TableArrayLoadKeepsNonNegativeKeyFact(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_nonneg_key"}, NumRegs: 2}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeInt, Aux2: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), key.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{get.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, key, get, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = RangeAnalysisPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if !fn.IntNonNegative[key.ID] {
		t.Fatalf("constant table key should be marked non-negative before lowering")
	}
	fn, err = TableArrayLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	var load *Instr
	for _, instr := range b.Instrs {
		if instr.Op == OpTableArrayLoad {
			load = instr
			break
		}
	}
	if load == nil {
		t.Fatalf("expected lowered TableArrayLoad:\n%s", Print(fn))
	}
	if len(load.Args) < 3 || load.Args[2].ID != key.ID {
		t.Fatalf("lowered TableArrayLoad should retain original key argument:\n%s", Print(fn))
	}
	if !fn.IntNonNegative[load.Args[2].ID] {
		t.Fatalf("lowered TableArrayLoad key should retain non-negative fact")
	}
}

func TestTableArrayNestedLoad_FusesSameBlockMixedRowFloat(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_nested_load"}, NumRegs: 4}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	rows := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	outerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	innerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	outerHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{rows.Value()}, Block: b}
	outerLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerHeader.Value()}, Block: b}
	outerData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerHeader.Value()}, Block: b}
	row := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeTable, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerData.Value(), outerLen.Value(), outerKey.Value()}, Block: b}
	rowHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{row.Value()}, Block: b}
	rowLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowHeader.Value()}, Block: b}
	rowData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowHeader.Value()}, Block: b}
	load := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeFloat, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowData.Value(), rowLen.Value(), innerKey.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load.Value()}, Block: b}
	b.Instrs = []*Instr{rows, outerKey, innerKey, outerHeader, outerLen, outerData, row, rowHeader, rowLen, rowData, load, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = TableArrayNestedLoadPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = DCEPass(fn)
	if err != nil {
		t.Fatal(err)
	}

	counts := countOps(fn)
	if counts[OpTableArrayNestedLoad] != 1 {
		t.Fatalf("expected one nested load, counts=%v\n%s", counts, Print(fn))
	}
	var nested *Instr
	for _, instr := range fn.Blocks[0].Instrs {
		if instr.Op == OpTableArrayNestedLoad {
			nested = instr
			break
		}
	}
	if nested == nil || len(nested.Args) != 5 {
		t.Fatalf("nested load should carry outer table, row data, row len, outer key, inner key:\n%s", Print(fn))
	}
	if nested.Args[0].ID != rows.ID || nested.Args[1].ID != outerData.ID || nested.Args[2].ID != outerLen.ID ||
		nested.Args[3].ID != outerKey.ID || nested.Args[4].ID != innerKey.ID {
		t.Fatalf("unexpected nested load args: %#v\n%s", nested.Args, Print(fn))
	}
	if counts[OpTableArrayLoad] != 0 {
		t.Fatalf("same-block row load chain should be removed, counts=%v\n%s", counts, Print(fn))
	}
	if counts[OpTableArrayHeader] != 1 || counts[OpTableArrayLen] != 1 || counts[OpTableArrayData] != 1 {
		t.Fatalf("outer table facts should remain and row facts should be fused, counts=%v\n%s", counts, Print(fn))
	}
}

func TestDenseMatrixNestedLoadLowerUsesDenseFeedback(t *testing.T) {
	fn := &Function{
		Proto: &vm.FuncProto{
			Name:             "dense_matrix_nested_load_lower",
			Code:             make([]uint32, 4),
			TableKeyFeedback: vm.NewTableKeyFeedbackVector(4),
		},
		NumRegs: 4,
	}
	fn.Proto.TableKeyFeedback[2].DenseMatrix = vm.FBDenseMatrixYes
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	rows := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	outerData := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	outerLen := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	outerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 3, Block: b}
	innerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 4, Block: b}
	load := &Instr{
		ID:        fn.newValueID(),
		Op:        OpTableArrayNestedLoad,
		Type:      TypeFloat,
		Aux:       int64(vm.FBKindFloat),
		Args:      []*Value{rows.Value(), outerData.Value(), outerLen.Value(), outerKey.Value(), innerKey.Value()},
		Block:     b,
		HasSource: true,
		SourcePC:  2,
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load.Value()}, Block: b}
	b.Instrs = []*Instr{rows, outerData, outerLen, outerKey, innerKey, load, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = DenseMatrixNestedLoadLowerPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	counts := countOps(fn)
	if counts[OpMatrixFlat] != 1 || counts[OpMatrixStride] != 1 || counts[OpMatrixLoadFAt] != 1 {
		t.Fatalf("expected dense nested load lowering, counts=%v\n%s", counts, Print(fn))
	}
	if counts[OpTableArrayNestedLoad] != 0 {
		t.Fatalf("nested load should be replaced, counts=%v\n%s", counts, Print(fn))
	}
}

func TestTableArrayNestedLoad_DoesNotFuseCrossBlockRowResidency(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_nested_cross_block"}, NumRegs: 4}
	entry := newBlock(0)
	body := newBlock(1)
	fn.Entry = entry
	fn.Blocks = []*Block{entry, body}
	entry.Succs = []*Block{body}
	body.Preds = []*Block{entry}

	rows := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	outerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	outerHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{rows.Value()}, Block: entry}
	outerLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerHeader.Value()}, Block: entry}
	outerData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerHeader.Value()}, Block: entry}
	row := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeTable, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerData.Value(), outerLen.Value(), outerKey.Value()}, Block: entry}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Aux: int64(body.ID), Block: entry}
	entry.Instrs = []*Instr{rows, outerKey, outerHeader, outerLen, outerData, row, jump}

	innerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: body}
	rowHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{row.Value()}, Block: body}
	rowLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowHeader.Value()}, Block: body}
	rowData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowHeader.Value()}, Block: body}
	load := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeFloat, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowData.Value(), rowLen.Value(), innerKey.Value()}, Block: body}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load.Value()}, Block: body}
	body.Instrs = []*Instr{innerKey, rowHeader, rowLen, rowData, load, ret}

	var err error
	fn, err = TableArrayNestedLoadPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	counts := countOps(fn)
	if counts[OpTableArrayNestedLoad] != 0 {
		t.Fatalf("cross-block row load should stay materialized, counts=%v\n%s", counts, Print(fn))
	}
	if counts[OpTableArrayLoad] != 2 {
		t.Fatalf("expected existing row and element loads to remain, counts=%v\n%s", counts, Print(fn))
	}
}

func TestTableArrayNestedLoad_DoesNotFuseAcrossSideEffect(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_nested_side_effect"}, NumRegs: 5}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	rows := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	outerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	innerKey := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	fnVal := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFunction, Aux: 3, Block: b}
	outerHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{rows.Value()}, Block: b}
	outerLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerHeader.Value()}, Block: b}
	outerData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerHeader.Value()}, Block: b}
	row := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeTable, Aux: int64(vm.FBKindMixed),
		Args: []*Value{outerData.Value(), outerLen.Value(), outerKey.Value()}, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeUnknown, Args: []*Value{fnVal.Value()}, Block: b}
	rowHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{row.Value()}, Block: b}
	rowLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowHeader.Value()}, Block: b}
	rowData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowHeader.Value()}, Block: b}
	load := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeFloat, Aux: int64(vm.FBKindFloat),
		Args: []*Value{rowData.Value(), rowLen.Value(), innerKey.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load.Value(), call.Value()}, Block: b}
	b.Instrs = []*Instr{rows, outerKey, innerKey, fnVal, outerHeader, outerLen, outerData, row, call, rowHeader, rowLen, rowData, load, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = TableArrayNestedLoadPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	counts := countOps(fn)
	if counts[OpTableArrayNestedLoad] != 0 {
		t.Fatalf("side-effect span should not fuse, counts=%v\n%s", counts, Print(fn))
	}
	if counts[OpTableArrayLoad] != 2 {
		t.Fatalf("expected existing row and element loads to remain, counts=%v\n%s", counts, Print(fn))
	}
}

func countOps(fn *Function) map[Op]int {
	counts := make(map[Op]int)
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			counts[instr.Op]++
		}
	}
	return counts
}

func blockHasOp(b *Block, op Op) bool {
	if b == nil {
		return false
	}
	for _, instr := range b.Instrs {
		if instr.Op == op {
			return true
		}
	}
	return false
}

func countBlockOps(b *Block, op Op) int {
	if b == nil {
		return 0
	}
	count := 0
	for _, instr := range b.Instrs {
		if instr.Op == op {
			count++
		}
	}
	return count
}
