package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestAnalyzeClosureRecurrence_UpvalueDelta(t *testing.T) {
	proto := &vm.FuncProto{
		Name:      "acc",
		NumParams: 0,
		MaxStack:  5,
		Upvalues: []vm.UpvalDesc{
			{Name: "value", InStack: true, Index: 2},
			{Name: "delta", InStack: true, Index: 1},
		},
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETUPVAL, 1, 0, 0),
			vm.EncodeABC(vm.OP_GETUPVAL, 2, 1, 0),
			vm.EncodeABC(vm.OP_ADD, 0, 1, 2),
			vm.EncodeABC(vm.OP_SETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_GETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}
	fact, ok := analyzeClosureRecurrence(proto)
	if !ok {
		t.Fatal("closure recurrence not recognized")
	}
	if fact.Proto != proto || fact.ValueUpval != 0 || fact.DeltaKind != closureRecurrenceDeltaUpvalue ||
		fact.DeltaUpval != 1 || fact.ReturnReg != 0 {
		t.Fatalf("unexpected fact: %#v", fact)
	}
}

func TestAnalyzeClosureRecurrence_ConstIntDelta(t *testing.T) {
	proto := &vm.FuncProto{
		Name:      "acc",
		NumParams: 0,
		MaxStack:  5,
		Upvalues:  []vm.UpvalDesc{{Name: "value", InStack: true, Index: 0}},
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETUPVAL, 1, 0, 0),
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 3),
			vm.EncodeABC(vm.OP_ADD, 0, 2, 1),
			vm.EncodeABC(vm.OP_SETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_GETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}
	fact, ok := analyzeClosureRecurrence(proto)
	if !ok {
		t.Fatal("const-int closure recurrence not recognized")
	}
	if fact.DeltaKind != closureRecurrenceDeltaConstInt || fact.DeltaInt != 3 || fact.ValueUpval != 0 {
		t.Fatalf("unexpected fact: %#v", fact)
	}
}

func TestAnalyzeClosureRecurrence_RejectsEscapingShape(t *testing.T) {
	proto := &vm.FuncProto{
		Name:      "not_acc",
		NumParams: 1,
		MaxStack:  3,
		Upvalues:  []vm.UpvalDesc{{Name: "value", InStack: true, Index: 0}},
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETUPVAL, 1, 0, 0),
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 1),
			vm.EncodeABC(vm.OP_ADD, 0, 1, 2),
			vm.EncodeABC(vm.OP_SETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_GETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}
	if fact, ok := analyzeClosureRecurrence(proto); ok {
		t.Fatalf("recognized non-zero-arg closure: %#v", fact)
	}
}
