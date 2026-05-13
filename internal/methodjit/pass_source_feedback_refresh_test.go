package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestSourceFeedbackRefresh_RestoresInlinedGetTableKindAndType(t *testing.T) {
	source := &vm.FuncProto{
		Code:             make([]uint32, 3),
		Feedback:         make([]vm.TypeFeedback, 3),
		TableKeyFeedback: vm.NewTableKeyFeedbackVector(3),
	}
	source.Feedback[1].Kind = vm.FBKindInt
	source.Feedback[1].Result = vm.FBInt

	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	get := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny, Args: []*Value{tbl.Value(), key.Value()}, Block: b}
	get.setSourceFromPC(source, 1)
	b.Instrs = []*Instr{tbl, key, get}

	if _, err := SourceFeedbackRefreshPass(fn); err != nil {
		t.Fatalf("SourceFeedbackRefreshPass: %v", err)
	}
	if get.Aux2 != int64(vm.FBKindInt) {
		t.Fatalf("GetTable Aux2=%d want FBKindInt", get.Aux2)
	}
	if get.Type != TypeInt {
		t.Fatalf("GetTable Type=%s want int", get.Type)
	}
}

func TestSourceFeedbackRefresh_RestoresInlinedSetTableKind(t *testing.T) {
	source := &vm.FuncProto{
		Code:     make([]uint32, 3),
		Feedback: make([]vm.TypeFeedback, 3),
	}
	source.Feedback[1].Kind = vm.FBKindInt

	fn := &Function{}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Block: b}
	set.setSourceFromPC(source, 1)
	b.Instrs = []*Instr{tbl, key, val, set}

	if _, err := SourceFeedbackRefreshPass(fn); err != nil {
		t.Fatalf("SourceFeedbackRefreshPass: %v", err)
	}
	if set.Aux2 != int64(vm.FBKindInt) {
		t.Fatalf("SetTable Aux2=%d want FBKindInt", set.Aux2)
	}
}

func TestEnsureSourceProtoBackfillsInlinedCalleeSource(t *testing.T) {
	source := &vm.FuncProto{Name: "callee", Code: make([]uint32, 2)}
	src := &Instr{ID: 1, Op: OpGetTable, HasSource: true, SourcePC: 1, SourceLine: 12}
	dst := &Instr{ID: 2, Op: OpGetTable}

	dst.copySourceFrom(src)
	dst.ensureSourceProto(source)

	if !dst.HasSource || dst.SourcePC != 1 || dst.SourceLine != 12 {
		t.Fatalf("source metadata not copied: %+v", dst)
	}
	if dst.SourceProto != source {
		t.Fatalf("SourceProto=%p want %p", dst.SourceProto, source)
	}
}
