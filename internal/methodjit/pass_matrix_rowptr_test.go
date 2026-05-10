package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestMatrixRowPtrFactoringRewritesRepeatedRowAccess(t *testing.T) {
	fn, b := newMatrixRowPtrTestFunction(t)
	flat, stride, row := b.Instrs[0], b.Instrs[1], b.Instrs[2]
	col0, col1 := b.Instrs[3], b.Instrs[4]
	load0 := &Instr{ID: fn.newValueID(), Op: OpMatrixLoadFAt, Type: TypeFloat,
		Args: []*Value{flat.Value(), stride.Value(), row.Value(), col0.Value()}, Block: b}
	load1 := &Instr{ID: fn.newValueID(), Op: OpMatrixLoadFAt, Type: TypeFloat,
		Args: []*Value{flat.Value(), stride.Value(), row.Value(), col1.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load1.Value()}, Block: b}
	b.Instrs = append(b.Instrs, load0, load1, ret)

	got, err := MatrixRowPtrFactoringPass(fn)
	if err != nil {
		t.Fatalf("MatrixRowPtrFactoringPass: %v", err)
	}
	if countOpHelper(got, OpMatrixRowPtr) != 1 {
		t.Fatalf("MatrixRowPtr count mismatch:\n%s", Print(got))
	}
	if countOpHelper(got, OpMatrixLoadFRowConst) != 2 || countOpHelper(got, OpMatrixLoadFRow) != 0 || countOpHelper(got, OpMatrixLoadFAt) != 0 {
		t.Fatalf("load lowering mismatch:\n%s", Print(got))
	}
	if errs := Validate(got); len(errs) > 0 {
		t.Fatalf("invalid IR after factoring: %v\n%s", errs[0], Print(got))
	}
}

func TestMatrixRowPtrFactoringKeepsSingleUseAtForm(t *testing.T) {
	fn, b := newMatrixRowPtrTestFunction(t)
	flat, stride, row := b.Instrs[0], b.Instrs[1], b.Instrs[2]
	col0 := b.Instrs[3]
	load := &Instr{ID: fn.newValueID(), Op: OpMatrixLoadFAt, Type: TypeFloat,
		Args: []*Value{flat.Value(), stride.Value(), row.Value(), col0.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load.Value()}, Block: b}
	b.Instrs = append(b.Instrs, load, ret)

	got, err := MatrixRowPtrFactoringPass(fn)
	if err != nil {
		t.Fatalf("MatrixRowPtrFactoringPass: %v", err)
	}
	if countOpHelper(got, OpMatrixRowPtr) != 0 || countOpHelper(got, OpMatrixLoadFAt) != 1 {
		t.Fatalf("single-use row should remain in MatrixLoadFAt form:\n%s", Print(got))
	}
}

func newMatrixRowPtrTestFunction(t *testing.T) (*Function, *Block) {
	t.Helper()
	fn := &Function{Proto: &vm.FuncProto{Name: "matrix_rowptr_test"}, NumRegs: 3}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	flat := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 0, Block: b}
	stride := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	row := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	col0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: b}
	col1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	b.Instrs = []*Instr{flat, stride, row, col0, col1}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	return fn, b
}
