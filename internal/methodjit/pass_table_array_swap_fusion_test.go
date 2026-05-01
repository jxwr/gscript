package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTableArraySwapFusion_FusesSameBlockExchange(t *testing.T) {
	fn := tableArraySwapFusionFixture(t)

	out, err := TableArraySwapFusionPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatal(err)
	}
	assertValidates(t, out, "table array swap fused")

	counts := countOps(out)
	if counts[OpTableArraySwap] != 1 {
		t.Fatalf("expected one fused swap, counts=%v\n%s", counts, Print(out))
	}
	if counts[OpTableArrayLoad] != 0 || counts[OpTableArrayStore] != 0 {
		t.Fatalf("expected exchange loads/stores to be removed, counts=%v\n%s", counts, Print(out))
	}
}

func TestTableArraySwapFusion_ProductionPipelineCoversIntExchange(t *testing.T) {
	proto := compileProto(t, `
func swap_pair(a, i) {
    tmp := a[i]
    a[i] = a[i + 1]
    a[i + 1] = tmp
}
arr := {1, 2, 3}
swap_pair(arr, 1)
result := arr[1] * 10 + arr[2]
	`)
	fnProto := findProtoByName(proto, "swap_pair")
	seedSwapFusionIntTableFeedback(fnProto)
	art, err := NewTieringManager().CompileForDiagnostics(fnProto)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.IRAfter, "TableArraySwap") {
		t.Fatalf("expected production pipeline to fuse table-array exchange:\n%s", art.IRAfter)
	}
}

func tableArraySwapFusionFixture(t *testing.T) *Function {
	t.Helper()

	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_swap_fusion"}, NumRegs: 3}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	header := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindInt), Args: []*Value{tbl.Value()}, Block: entry}
	length := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindInt), Args: []*Value{header.Value()}, Block: entry}
	data := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindInt), Args: []*Value{header.Value()}, Block: entry}
	keyA := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: entry}
	oneA := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: entry}
	keyBLoad := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{keyA.Value(), oneA.Value()}, Block: entry}
	loadA := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{data.Value(), length.Value(), keyA.Value()}, Block: entry}
	loadB := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{data.Value(), length.Value(), keyBLoad.Value()}, Block: entry}
	storeA := &Instr{ID: fn.newValueID(), Op: OpTableArrayStore, Type: TypeUnknown, Aux: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), data.Value(), length.Value(), keyA.Value(), loadB.Value()}, Block: entry}
	oneB := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: entry}
	keyBStore := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{keyA.Value(), oneB.Value()}, Block: entry}
	storeB := &Instr{ID: fn.newValueID(), Op: OpTableArrayStore, Type: TypeUnknown, Aux: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), data.Value(), length.Value(), keyBStore.Value(), loadA.Value()}, Block: entry}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{tbl.Value()}, Block: entry}
	entry.Instrs = []*Instr{tbl, header, length, data, keyA, oneA, keyBLoad, loadA, loadB, storeA, oneB, keyBStore, storeB, ret}

	assertValidates(t, fn, "table array swap fusion fixture")
	return fn
}

func seedSwapFusionIntTableFeedback(proto *vm.FuncProto) {
	fb := proto.EnsureFeedback()
	for pc, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETTABLE:
			fb[pc].Left = vm.FBTable
			fb[pc].Right = vm.FBInt
			fb[pc].Result = vm.FBInt
			fb[pc].Kind = vm.FBKindInt
		case vm.OP_SETTABLE:
			fb[pc].Left = vm.FBTable
			fb[pc].Right = vm.FBInt
			fb[pc].Result = vm.FBInt
			fb[pc].Kind = vm.FBKindInt
		}
	}
}
