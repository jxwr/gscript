package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFieldPolyLenPhi_ReplacesJoinWithShapeControlledPhi(t *testing.T) {
	fn := &Function{
		Proto:               &vm.FuncProto{Name: "field_poly_len_phi", Constants: []runtime.Value{runtime.StringValue("kind")}},
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{},
	}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	left := &Block{ID: 1, defs: make(map[int]*Value)}
	right := &Block{ID: 2, defs: make(map[int]*Value)}
	join := &Block{ID: 3, defs: make(map[int]*Value)}
	entry.Succs = []*Block{left, right}
	left.Preds = []*Block{entry}
	right.Preds = []*Block{entry}
	left.Succs = []*Block{join}
	right.Succs = []*Block{join}
	join.Preds = []*Block{left, right}
	fn.Entry = entry
	fn.Blocks = []*Block{entry, left, right, join}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	br := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Args: []*Value{tbl.Value()}, Block: entry}
	entry.Instrs = []*Instr{tbl, br}
	g0 := &Instr{ID: fn.newValueID(), Op: OpGuardFieldCalleeProto, Type: TypeAny, Args: []*Value{tbl.Value()}, Aux: 1, Aux2: int64(10) << 32, Block: left}
	left.Instrs = []*Instr{g0, &Instr{ID: fn.newValueID(), Op: OpJump, Block: left}}
	g1 := &Instr{ID: fn.newValueID(), Op: OpGuardFieldCalleeProto, Type: TypeAny, Args: []*Value{tbl.Value()}, Aux: 1, Aux2: int64(11) << 32, Block: right}
	right.Instrs = []*Instr{g1, &Instr{ID: fn.newValueID(), Op: OpJump, Block: right}}
	ln := &Instr{ID: fn.newValueID(), Op: OpFieldPolyLen, Type: TypeInt, Args: []*Value{tbl.Value()}, Aux: 0, Block: join}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{ln.Value()}, Block: join}
	join.Instrs = []*Instr{ln, ret}
	fn.FieldPolyShapeFacts[ln.ID] = []FieldPolyShapeCase{
		{ShapeID: 10, ReceiverFact: FixedShapeTableFact{FieldLenRanges: map[string]intRange{"kind": pointRange(2)}}},
		{ShapeID: 11, ReceiverFact: FixedShapeTableFact{FieldLenRanges: map[string]intRange{"kind": pointRange(5)}}},
	}

	out, err := FieldPolyLenPhiPass(fn)
	if err != nil {
		t.Fatalf("FieldPolyLenPhiPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after pass: %v\n%s", errs, Print(out))
	}
	if ln.Op != OpNop {
		t.Fatalf("field poly len was not removed:\n%s", Print(out))
	}
	if ret.Args[0].Def == nil || ret.Args[0].Def.Op != OpPhi {
		t.Fatalf("return did not use generated phi:\n%s", Print(out))
	}
}
