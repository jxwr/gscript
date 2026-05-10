//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTableShapeID_InterpreterAndNative(t *testing.T) {
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(1))
	tbl.RawSetString("y", runtime.IntValue(2))
	want := int64(tbl.ShapeID())

	proto := &vm.FuncProto{Name: "shape_id", NumParams: 1, MaxStack: 4}
	fn := &Function{Proto: proto, NumRegs: 1, nextID: 3}
	block := &Block{ID: 0}
	fn.Entry = block
	fn.Blocks = []*Block{block}
	load := &Instr{ID: 0, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: block}
	shape := &Instr{ID: 1, Op: OpTableShapeID, Type: TypeInt, Args: []*Value{load.Value()}, Block: block}
	ret := &Instr{ID: 2, Op: OpReturn, Args: []*Value{shape.Value()}, Block: block}
	block.Instrs = []*Instr{load, shape, ret}

	args := []runtime.Value{runtime.TableValue(tbl)}
	interp, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(interp) != 1 || !interp[0].IsInt() || interp[0].Int() != want {
		t.Fatalf("Interpret result=%v want shape %d", interp, want)
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	native, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(native) != 1 || !native[0].IsInt() || native[0].Int() != want {
		t.Fatalf("native result=%v want shape %d", native, want)
	}
}
