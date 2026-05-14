package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFieldCallPolyLenFusionPass_RecordsSameBlockFusion(t *testing.T) {
	calleeA := &vm.FuncProto{Name: "step_a", NumParams: 2}
	calleeB := &vm.FuncProto{Name: "step_b", NumParams: 2}
	fn := &Function{Proto: &vm.FuncProto{
		Name:      "caller",
		Constants: []runtime.Value{runtime.StringValue("kind")},
	}}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	tick := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt, Args: []*Value{call.Value(), tick.Value()}, Block: b}
	ln := &Instr{ID: fn.newValueID(), Op: OpFieldPolyLen, Type: TypeInt, Args: []*Value{recv.Value()}, Aux: 0, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{ln.Value()}, Block: b}
	b.Instrs = []*Instr{recv, tick, call, mod, ln, ret}
	fn.FieldPolyShapeFacts = map[int][]FieldPolyShapeCase{
		call.ID: {
			{ShapeID: 101, FieldIdx: 0, VMProto: calleeA},
			{ShapeID: 102, FieldIdx: 0, VMProto: calleeB},
		},
		ln.ID: {
			{ShapeID: 101, ReceiverFact: FixedShapeTableFact{FieldLenRanges: map[string]intRange{"kind": pointRange(2)}}},
			{ShapeID: 102, ReceiverFact: FixedShapeTableFact{FieldLenRanges: map[string]intRange{"kind": pointRange(5)}}},
		},
	}

	out, err := FieldCallPolyLenFusionPass(fn)
	if err != nil {
		t.Fatalf("FieldCallPolyLenFusionPass: %v", err)
	}
	fusions := out.FieldCallPolyLenFusions[call.ID]
	if len(fusions) != 2 {
		t.Fatalf("fusion count=%d want 2", len(fusions))
	}
	if fusions[0].LenValueID != ln.ID || fusions[0].ShapeID != 101 || fusions[0].Len != 2 {
		t.Fatalf("first fusion=%+v", fusions[0])
	}
	if fusions[1].LenValueID != ln.ID || fusions[1].ShapeID != 102 || fusions[1].Len != 5 {
		t.Fatalf("second fusion=%+v", fusions[1])
	}
}

func TestFieldCallPolyLenFusionPass_StopsAtMutationBarrier(t *testing.T) {
	callee := &vm.FuncProto{Name: "step", NumParams: 2}
	fn := &Function{Proto: &vm.FuncProto{
		Name:      "caller",
		Constants: []runtime.Value{runtime.StringValue("kind")},
	}}
	b := &Block{ID: 0}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	tick := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetField, Args: []*Value{recv.Value(), tick.Value()}, Aux: 0, Block: b}
	ln := &Instr{ID: fn.newValueID(), Op: OpFieldPolyLen, Type: TypeInt, Args: []*Value{recv.Value()}, Aux: 0, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{ln.Value()}, Block: b}
	b.Instrs = []*Instr{recv, tick, call, set, ln, ret}
	fn.FieldPolyShapeFacts = map[int][]FieldPolyShapeCase{
		call.ID: {
			{ShapeID: 101, FieldIdx: 0, VMProto: callee},
			{ShapeID: 102, FieldIdx: 0, VMProto: callee},
		},
		ln.ID: {
			{ShapeID: 101, ReceiverFact: FixedShapeTableFact{FieldLenRanges: map[string]intRange{"kind": pointRange(2)}}},
			{ShapeID: 102, ReceiverFact: FixedShapeTableFact{FieldLenRanges: map[string]intRange{"kind": pointRange(5)}}},
		},
	}

	out, err := FieldCallPolyLenFusionPass(fn)
	if err != nil {
		t.Fatalf("FieldCallPolyLenFusionPass: %v", err)
	}
	if got := len(out.FieldCallPolyLenFusions[call.ID]); got != 0 {
		t.Fatalf("fusion count=%d want 0", got)
	}
}
