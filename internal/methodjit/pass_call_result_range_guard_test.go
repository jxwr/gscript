package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestCallResultRangeGuardPass_GuardsProfiledFieldCallResult(t *testing.T) {
	proto := &vm.FuncProto{
		Name:             "call_result_range",
		CallSiteFeedback: vm.NewCallSiteFeedbackVector(1),
	}
	for i := 0; i < int(callResultRangeGuardMinCount); i++ {
		proto.CallSiteFeedback[0].ObserveCall(runtime.NilValue(), nil, 1, 2)
		proto.CallSiteFeedback[0].ObserveResult(runtime.IntValue(int64(i + 3)))
	}
	fn := &Function{Proto: proto}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	arg := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{
		ID:        fn.newValueID(),
		Op:        OpFieldCallFloor,
		Type:      TypeInt,
		Args:      []*Value{recv.Value(), arg.Value()},
		Block:     b,
		HasSource: true,
		SourcePC:  0,
	}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{call.Value(), one.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{recv, arg, call, one, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	out, err := CallResultRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("CallResultRangeGuardPass: %v", err)
	}
	if len(out.Blocks[0].Instrs) < 4 || out.Blocks[0].Instrs[3].Op != OpGuardIntRange {
		t.Fatalf("missing GuardIntRange after call:\n%s", Print(out))
	}
	guard := out.Blocks[0].Instrs[3]
	wantMax := int64(2 + int(callResultRangeGuardMinCount))
	if guard.Aux != 3 || guard.Aux2 != wantMax {
		t.Fatalf("guard range=[%d,%d], want [3,%d]", guard.Aux, guard.Aux2, wantMax)
	}
	if add.Args[0].ID != guard.ID {
		t.Fatalf("AddInt arg not rewritten to guard:\n%s", Print(out))
	}
}

func TestCallResultRangeGuardPass_RangeAnalysisConsumesGuard(t *testing.T) {
	proto := &vm.FuncProto{
		Name:             "call_result_range_safe",
		CallSiteFeedback: vm.NewCallSiteFeedbackVector(1),
	}
	for i := 0; i < int(callResultRangeGuardMinCount); i++ {
		proto.CallSiteFeedback[0].ObserveCall(runtime.NilValue(), nil, 1, 2)
		proto.CallSiteFeedback[0].ObserveResult(runtime.IntValue(40))
	}
	fn := &Function{Proto: proto}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	arg := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), arg.Value()}, Block: b, HasSource: true, SourcePC: 0}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{call.Value(), one.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{recv, arg, call, one, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	var err error
	fn, err = CallResultRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("CallResultRangeGuardPass: %v", err)
	}
	fn, err = RangeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("RangeAnalysisPass: %v", err)
	}
	if !fn.Int48Safe[add.ID] {
		t.Fatalf("AddInt fed by guarded call result should be Int48Safe:\n%s", Print(fn))
	}
}

func TestCallResultRangeGuardPass_SpeculatesStableFieldCallFloor(t *testing.T) {
	proto := &vm.FuncProto{
		Name:             "call_result_spec",
		CallSiteFeedback: vm.NewCallSiteFeedbackVector(1),
	}
	proto.CallSiteFeedback[0].ObserveCall(runtime.NilValue(), nil, 1, 2)
	fn := &Function{Proto: proto}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	arg := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), arg.Value()}, Block: b, HasSource: true, SourcePC: 0}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{call.Value()}, Block: b}
	b.Instrs = []*Instr{recv, arg, call, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	fn.FieldPolyShapeFacts = map[int][]FieldPolyShapeCase{
		call.ID: {{ShapeID: 7, FieldIdx: 1}},
	}

	out, err := CallResultRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("CallResultRangeGuardPass: %v", err)
	}
	if len(out.Blocks[0].Instrs) < 4 || out.Blocks[0].Instrs[3].Op != OpGuardIntRange {
		t.Fatalf("missing speculative GuardIntRange after field call:\n%s", Print(out))
	}
	guard := out.Blocks[0].Instrs[3]
	if guard.Aux != callFloorSpecRangeMin || guard.Aux2 != callFloorSpecRangeMax {
		t.Fatalf("guard range=[%d,%d], want [%d,%d]", guard.Aux, guard.Aux2, callFloorSpecRangeMin, callFloorSpecRangeMax)
	}
	if ret.Args[0].ID != guard.ID {
		t.Fatalf("return arg not rewritten to guard:\n%s", Print(out))
	}
}

func TestCallResultRangeGuardPass_SkipsSuppressedIntRange(t *testing.T) {
	proto := &vm.FuncProto{
		Name:             "call_result_suppressed",
		CallSiteFeedback: vm.NewCallSiteFeedbackVector(1),
	}
	proto.CallSiteFeedback[0].ObserveCall(runtime.NilValue(), nil, 1, 2)
	fn := &Function{
		Proto: proto,
		SuppressedSpecGuardKinds: map[int]map[string]bool{
			0: {"GuardIntRange": true},
		},
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	arg := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), arg.Value()}, Block: b, HasSource: true, SourcePC: 0}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{call.Value()}, Block: b}
	b.Instrs = []*Instr{recv, arg, call, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	fn.FieldPolyShapeFacts = map[int][]FieldPolyShapeCase{
		call.ID: {{ShapeID: 7, FieldIdx: 1}},
	}

	out, err := CallResultRangeGuardPass(fn)
	if err != nil {
		t.Fatalf("CallResultRangeGuardPass: %v", err)
	}
	if countOps(out)[OpGuardIntRange] != 0 {
		t.Fatalf("suppressed GuardIntRange was still emitted:\n%s", Print(out))
	}
	if ret.Args[0].ID != call.ID {
		t.Fatalf("suppressed guard should leave uses unchanged:\n%s", Print(out))
	}
}
