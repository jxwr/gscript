package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestStaticTableLenFold_FoldsDominatingSetListLen(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "static_table_len"}, NumRegs: 1}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	body := &Block{ID: 1, defs: make(map[int]*Value)}
	entry.Succs = []*Block{body}
	body.Preds = []*Block{entry}
	fn.Entry = entry
	fn.Blocks = []*Block{entry, body}
	fn.Proto.Constants = []runtime.Value{runtime.StringValue("a"), runtime.StringValue("b"), runtime.StringValue("c")}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: entry}
	a := &Instr{ID: fn.newValueID(), Op: OpConstString, Type: TypeString, Aux: 0, Block: entry}
	b := &Instr{ID: fn.newValueID(), Op: OpConstString, Type: TypeString, Aux: 1, Block: entry}
	c := &Instr{ID: fn.newValueID(), Op: OpConstString, Type: TypeString, Aux: 2, Block: entry}
	setList := &Instr{ID: fn.newValueID(), Op: OpSetList, Type: TypeUnknown, Aux: 1, Args: []*Value{tbl.Value(), a.Value(), b.Value(), c.Value()}, Block: entry}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(body.ID), Block: entry}
	entry.Instrs = []*Instr{tbl, a, b, c, setList, jump}

	ln := &Instr{ID: fn.newValueID(), Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}, Block: body}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{ln.Value()}, Block: body}
	body.Instrs = []*Instr{ln, ret}

	out, err := StaticTableLenFoldPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if ln.Op != OpConstInt || ln.Aux != 3 {
		t.Fatalf("Len was not folded to 3:\n%s", Print(out))
	}
}

func TestStaticTableLenFold_RejectsDynamicLengthMutation(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "static_table_len_mut"}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	setList := &Instr{ID: fn.newValueID(), Op: OpSetList, Type: TypeUnknown, Aux: 1, Args: []*Value{tbl.Value(), val.Value()}, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), val.Value(), val.Value()}, Block: b}
	ln := &Instr{ID: fn.newValueID(), Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{ln.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, val, setList, set, ln, ret}

	out, err := StaticTableLenFoldPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if ln.Op != OpLen {
		t.Fatalf("Len folded despite SetTable mutation:\n%s", Print(out))
	}
}

func TestStaticTableLenFold_RejectsTablePassedToCall(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "static_table_len_call"}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	setList := &Instr{ID: fn.newValueID(), Op: OpSetList, Type: TypeUnknown, Aux: 1, Args: []*Value{tbl.Value(), val.Value()}, Block: b}
	callee := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFunction, Aux: 0, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny, Args: []*Value{callee.Value(), tbl.Value()}, Block: b}
	ln := &Instr{ID: fn.newValueID(), Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{ln.Value(), call.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, val, setList, callee, call, ln, ret}

	out, err := StaticTableLenFoldPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if ln.Op != OpLen {
		t.Fatalf("Len folded despite table escaping to call:\n%s", Print(out))
	}
}

func TestStaticTableLenFold_RejectsTableStoredAsValue(t *testing.T) {
	fn := &Function{Proto: &vm.FuncProto{Name: "static_table_len_alias"}, NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	container := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	setList := &Instr{ID: fn.newValueID(), Op: OpSetList, Type: TypeUnknown, Aux: 1, Args: []*Value{tbl.Value(), val.Value()}, Block: b}
	aliasStore := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{container.Value(), val.Value(), tbl.Value()}, Block: b}
	ln := &Instr{ID: fn.newValueID(), Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{ln.Value(), aliasStore.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, container, val, setList, aliasStore, ln, ret}

	out, err := StaticTableLenFoldPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if ln.Op != OpLen {
		t.Fatalf("Len folded despite table aliasing through SetTable value:\n%s", Print(out))
	}
}
