package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestModularCallFloorReducePass_ReducesAdditiveModuloLeaf(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "modular_floor_reduce"}}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	tick := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Block: b}
	acc := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{acc.Value(), call.Value()}, Block: b}
	div := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1000000007, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt, Args: []*Value{add.Value(), div.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{mod.Value()}, Block: b}
	b.Instrs = []*Instr{recv, tick, call, acc, add, div, mod, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	out, err := ModularCallFloorReducePass(fn)
	if err != nil {
		t.Fatalf("ModularCallFloorReducePass: %v", err)
	}
	if len(out.Blocks[0].Instrs) < 4 || out.Blocks[0].Instrs[3].Op != OpModInt {
		t.Fatalf("missing inserted ModInt after floor call:\n%s", Print(out))
	}
	reduced := out.Blocks[0].Instrs[3]
	if reduced.Args[0].ID != call.ID || reduced.Args[1].ID != div.ID {
		t.Fatalf("inserted mod args mismatch:\n%s", Print(out))
	}
	if add.Args[1].ID != reduced.ID {
		t.Fatalf("add arg was not rewritten to reduced floor result:\n%s", Print(out))
	}
}

func TestCallResultRangeGuardPass_SkipsModuloReducedFloorCall(t *testing.T) {
	proto := &vm.FuncProto{
		Name:             "modular_floor_guard_skip",
		CallSiteFeedback: vm.NewCallSiteFeedbackVector(1),
	}
	fn := &Function{Proto: proto}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	recv := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	tick := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpFieldCallFloor, Type: TypeInt, Args: []*Value{recv.Value(), tick.Value()}, Block: b, HasSource: true, SourcePC: 0}
	div := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1000000007, Block: b}
	reduced := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt, Args: []*Value{call.Value(), div.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{reduced.Value()}, Block: b}
	b.Instrs = []*Instr{recv, tick, call, div, reduced, ret}
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
		t.Fatalf("modulo-reduced floor call should not get int range guard:\n%s", Print(out))
	}
}
