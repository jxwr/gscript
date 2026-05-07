package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestStaticTableLenPass_FoldsSetListLiteralLen(t *testing.T) {
	fn, tbl, ln := staticTableLenTestFunction(staticTableLenTestOpts{})

	out, err := StaticTableLenPass(fn)
	if err != nil {
		t.Fatalf("StaticTableLenPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("Validate: %v", errs)
	}
	if ln.Op != OpConstInt || ln.Type != TypeInt || ln.Aux != 3 || len(ln.Args) != 0 {
		t.Fatalf("expected Len(%d) to fold to ConstInt(3), got %s aux=%d args=%d", tbl.ID, ln.Op, ln.Aux, len(ln.Args))
	}
}

func TestStaticTableLenPass_DoesNotFoldMutatedTable(t *testing.T) {
	fn, _, ln := staticTableLenTestFunction(staticTableLenTestOpts{mutate: true})

	if _, err := StaticTableLenPass(fn); err != nil {
		t.Fatalf("StaticTableLenPass: %v", err)
	}
	if ln.Op != OpLen {
		t.Fatalf("mutated table length should remain Len, got %s", ln.Op)
	}
}

func TestStaticTableLenPass_DoesNotFoldEscapedTable(t *testing.T) {
	fn, _, ln := staticTableLenTestFunction(staticTableLenTestOpts{escapeToCall: true})

	if _, err := StaticTableLenPass(fn); err != nil {
		t.Fatalf("StaticTableLenPass: %v", err)
	}
	if ln.Op != OpLen {
		t.Fatalf("escaped table length should remain Len, got %s", ln.Op)
	}
}

func TestStaticTableLenPass_DoesNotFoldNonInitialSetListChunk(t *testing.T) {
	fn, _, ln := staticTableLenTestFunction(staticTableLenTestOpts{setListAux: 51})

	if _, err := StaticTableLenPass(fn); err != nil {
		t.Fatalf("StaticTableLenPass: %v", err)
	}
	if ln.Op != OpLen {
		t.Fatalf("non-initial SetList chunk length should remain Len, got %s", ln.Op)
	}
}

type staticTableLenTestOpts struct {
	mutate       bool
	escapeToCall bool
	setListAux   int64
	stringValues bool
}

func staticTableLenTestFunction(opts staticTableLenTestOpts) (*Function, *Instr, *Instr) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "static_len"},
		NumRegs: 4,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	tbl := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	valueOp, valueType := OpConstInt, TypeInt
	if opts.stringValues {
		valueOp, valueType = OpConstString, TypeString
	}
	a := &Instr{ID: fn.newValueID(), Op: valueOp, Type: valueType, Aux: 11, Block: b}
	c := &Instr{ID: fn.newValueID(), Op: valueOp, Type: valueType, Aux: 22, Block: b}
	d := &Instr{ID: fn.newValueID(), Op: valueOp, Type: valueType, Aux: 33, Block: b}
	setListAux := opts.setListAux
	if setListAux == 0 {
		setListAux = 1
	}
	setList := &Instr{ID: fn.newValueID(), Op: OpSetList, Args: []*Value{tbl.Value(), a.Value(), c.Value(), d.Value()}, Aux: setListAux, Block: b}
	ln := &Instr{ID: fn.newValueID(), Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{ln.Value()}, Block: b}

	b.Instrs = []*Instr{tbl, a, c, d, setList}
	if opts.mutate {
		key := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 4, Block: b}
		val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 44, Block: b}
		setTable := &Instr{ID: fn.newValueID(), Op: OpSetTable, Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Block: b}
		b.Instrs = append(b.Instrs, key, val, setTable)
	}
	if opts.escapeToCall {
		callee := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
		call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny, Args: []*Value{callee.Value(), tbl.Value()}, Block: b}
		b.Instrs = append(b.Instrs, callee, call)
	}
	b.Instrs = append(b.Instrs, ln, ret)
	fn.Entry = b
	fn.Blocks = []*Block{b}
	return fn, tbl, ln
}
