package methodjit

import "github.com/gscript/gscript/internal/vm"

// TableArrayNestedLoadPass fuses the hot table-of-float-arrays shape:
//
//	row    = TableArrayLoad(outerData, outerLen, k) : table
//	hdr    = TableArrayHeader(row)                  : float-array
//	len    = TableArrayLen(hdr)
//	data   = TableArrayData(hdr)
//	value  = TableArrayLoad(data, len, j)           : float
//
// into one guarded nested load that keeps the row table pointer in scratch
// registers. The transform is deliberately same-block and single-use only so
// it does not undo source-level row residency such as matmul's ai := a[i].
func TableArrayNestedLoadPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	uses := computeUseCounts(fn)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpTableArrayLoad || len(instr.Args) < 3 {
				continue
			}
			if !tableArrayNestedLoadSupported(instr.Aux, instr.Type) {
				continue
			}
			data := instr.Args[0].Def
			length := instr.Args[1].Def
			if data == nil || length == nil ||
				data.Op != OpTableArrayData || length.Op != OpTableArrayLen ||
				data.Block != block || length.Block != block ||
				len(data.Args) < 1 || len(length.Args) < 1 ||
				data.Args[0] == nil || length.Args[0] == nil ||
				data.Args[0].ID != length.Args[0].ID {
				continue
			}
			header := data.Args[0].Def
			if header == nil || header.Op != OpTableArrayHeader || header.Block != block ||
				len(header.Args) < 1 || header.Args[0] == nil {
				continue
			}
			rowLoad := header.Args[0].Def
			if rowLoad == nil || rowLoad.Op != OpTableArrayLoad || rowLoad.Block != block ||
				rowLoad.Type != TypeTable || rowLoad.Aux != int64(vm.FBKindMixed) ||
				len(rowLoad.Args) < 3 {
				continue
			}
			if !tableArrayNestedLoadSafeSpan(block, rowLoad, instr) {
				continue
			}
			if uses[rowLoad.ID] != 1 || uses[header.ID] != 2 ||
				uses[data.ID] != 1 || uses[length.ID] != 1 {
				continue
			}

			instr.Op = OpTableArrayNestedLoad
			instr.Args = []*Value{rowLoad.Args[0], rowLoad.Args[1], rowLoad.Args[2], instr.Args[2]}
			instr.Aux2 = 0
			instr.copySourceFrom(rowLoad)
			functionRemarks(fn).Add("TableArrayNestedLoad", "changed", block.ID, instr.ID, instr.Op,
				"fused same-block mixed row table load with float row element load")
		}
	}
	return fn, nil
}

func tableArrayNestedLoadSupported(kind int64, typ Type) bool {
	return kind == int64(vm.FBKindFloat) && typ == TypeFloat
}

func tableArrayNestedLoadSafeSpan(block *Block, rowLoad, finalLoad *Instr) bool {
	if block == nil || rowLoad == nil || finalLoad == nil {
		return false
	}
	rowIdx, finalIdx := -1, -1
	for i, instr := range block.Instrs {
		if instr == rowLoad {
			rowIdx = i
		}
		if instr == finalLoad {
			finalIdx = i
		}
	}
	if rowIdx < 0 || finalIdx < 0 || rowIdx >= finalIdx {
		return false
	}
	for _, instr := range block.Instrs[rowIdx+1 : finalIdx] {
		if hasSideEffect(instr) {
			return false
		}
	}
	return true
}
