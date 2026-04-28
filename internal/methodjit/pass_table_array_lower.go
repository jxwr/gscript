package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TableArrayLowerPass splits monomorphic-kind GetTable loads into a small
// typed-array pipeline:
//
//	hdr  = TableArrayHeader(t)  // table/metatable/kind guard
//	len  = TableArrayLen(hdr)
//	data = TableArrayData(hdr)
//	val  = TableArrayLoad(data, len, key)
//
// Header/len/data are pure SSA values after the guard, so the existing
// LoadElimination and LICM passes can reuse or hoist them in read-only loops.
func TableArrayLowerPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		needsRewrite := false
		for _, instr := range block.Instrs {
			if instr.Op == OpGetTable && tableArrayLowerableKind(instr.Aux2) {
				needsRewrite = true
				break
			}
		}
		if !needsRewrite {
			continue
		}

		newInstrs := make([]*Instr, 0, len(block.Instrs)*2)
		for _, instr := range block.Instrs {
			if instr.Op != OpGetTable || !tableArrayLowerableKind(instr.Aux2) || len(instr.Args) < 2 {
				newInstrs = append(newInstrs, instr)
				continue
			}

			tbl, key := instr.Args[0], instr.Args[1]
			kind := instr.Aux2
			header := emitIRInstr(fn, block, OpTableArrayHeader, TypeInt, []*Value{tbl}, kind, 0)
			length := emitIRInstr(fn, block, OpTableArrayLen, TypeInt, []*Value{header.Value()}, kind, 0)
			data := emitIRInstr(fn, block, OpTableArrayData, TypeInt, []*Value{header.Value()}, kind, 0)
			header.copySourceFrom(instr)
			length.copySourceFrom(instr)
			data.copySourceFrom(instr)

			newInstrs = append(newInstrs, header, length, data)
			instr.Op = OpTableArrayLoad
			instr.Args = []*Value{data.Value(), length.Value(), key}
			instr.Aux = kind
			instr.Aux2 = 0
			if typ, ok := tableArrayKindElementType(kind); ok {
				instr.Type = typ
			}
			newInstrs = append(newInstrs, instr)
			functionRemarks(fn).Add("TableArrayLower", "changed", block.ID, instr.ID, instr.Op,
				"split monomorphic GetTable into typed array header/data/load")
		}
		block.Instrs = newInstrs
	}
	return fn, nil
}

func tableArrayLowerableKind(kind int64) bool {
	switch kind {
	case int64(vm.FBKindMixed), int64(vm.FBKindInt), int64(vm.FBKindFloat), int64(vm.FBKindBool):
		return true
	default:
		return false
	}
}

func tableArrayKindElementType(kind int64) (Type, bool) {
	switch kind {
	case int64(vm.FBKindInt):
		return TypeInt, true
	case int64(vm.FBKindFloat):
		return TypeFloat, true
	case int64(vm.FBKindBool):
		return TypeBool, true
	default:
		return TypeUnknown, false
	}
}

func fbKindToRuntimeArrayKind(kind int64) (runtime.ArrayKind, bool) {
	switch kind {
	case int64(vm.FBKindMixed):
		return runtime.ArrayMixed, true
	case int64(vm.FBKindInt):
		return runtime.ArrayInt, true
	case int64(vm.FBKindFloat):
		return runtime.ArrayFloat, true
	case int64(vm.FBKindBool):
		return runtime.ArrayBool, true
	default:
		return runtime.ArrayMixed, false
	}
}
