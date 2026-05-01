package methodjit

import "github.com/gscript/gscript/internal/vm"

// TableArraySwapFusionPass fuses same-block typed-array exchange patterns:
//
//	a = load(data, len, k1)
//	b = load(data, len, k2)
//	store(table, data, len, k1, b)
//	store(table, data, len, k2, a)
//
// into one side-effecting swap op. The pass is intentionally narrow: both
// loaded values must be single-use, stores must target the same lowered table
// data/len fact, and only pure integer address arithmetic may sit between the
// four operations.
func TableArraySwapFusionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	uses := computeUseCounts(fn)
	for _, block := range fn.Blocks {
		fuseTableArraySwapsInBlock(fn, block, uses)
	}
	return fn, nil
}

func fuseTableArraySwapsInBlock(fn *Function, block *Block, uses map[int]int) {
	if block == nil {
		return
	}
	for i, loadA := range block.Instrs {
		if !tableArraySwapLoadCandidate(loadA) || uses[loadA.ID] != 1 {
			continue
		}
		table, ok := tableArrayLoadTableValue(loadA)
		if !ok || table == nil {
			continue
		}
		loadBIdx, storeAIdx, storeBIdx := findTableArraySwapTail(block, i, loadA, table, uses)
		if loadBIdx < 0 {
			continue
		}

		loadB := block.Instrs[loadBIdx]
		loadB.Op = OpTableArraySwap
		loadB.Type = TypeUnknown
		loadB.Args = []*Value{table, loadA.Args[0], loadA.Args[1], loadA.Args[2], loadB.Args[2]}
		loadB.Aux2 = 0
		loadB.copySourceFrom(loadA)

		nopTableArraySwapMember(loadA)
		nopTableArraySwapMember(block.Instrs[storeAIdx])
		nopTableArraySwapMember(block.Instrs[storeBIdx])
		functionRemarks(fn).Add("TableArraySwapFusion", "changed", block.ID, loadB.ID, loadB.Op,
			"fused typed-array load/load/store/store exchange")
	}
}

func nopTableArraySwapMember(instr *Instr) {
	if instr == nil {
		return
	}
	instr.Op = OpNop
	instr.Args = nil
	instr.Type = TypeUnknown
	instr.Aux = 0
	instr.Aux2 = 0
}

func tableArraySwapLoadCandidate(instr *Instr) bool {
	if instr == nil || instr.Op != OpTableArrayLoad || len(instr.Args) < 3 {
		return false
	}
	switch instr.Aux {
	case int64(vm.FBKindInt):
		return instr.Type == TypeInt
	case int64(vm.FBKindFloat):
		return instr.Type == TypeFloat
	default:
		return false
	}
}

func findTableArraySwapTail(block *Block, loadAIdx int, loadA *Instr, table *Value, uses map[int]int) (int, int, int) {
	loadBIdx := -1
	var loadB *Instr
	for j := loadAIdx + 1; j < len(block.Instrs); j++ {
		instr := block.Instrs[j]
		if instr == nil {
			continue
		}
		if tableArraySwapSecondLoad(loadA, instr, uses) {
			loadBIdx = j
			loadB = instr
			break
		}
		if !tableArraySwapPureBetween(instr) {
			return -1, -1, -1
		}
	}
	if loadB == nil {
		return -1, -1, -1
	}

	storeAIdx, storeBIdx := -1, -1
	for j := loadBIdx + 1; j < len(block.Instrs); j++ {
		instr := block.Instrs[j]
		if instr == nil {
			continue
		}
		if storeAIdx < 0 && tableArraySwapStoreMatches(instr, table, loadA, loadB, loadA.Args[2], loadB.ID) {
			storeAIdx = j
			continue
		}
		if storeBIdx < 0 && tableArraySwapStoreMatches(instr, table, loadA, loadB, loadB.Args[2], loadA.ID) {
			storeBIdx = j
			continue
		}
		if storeAIdx >= 0 && storeBIdx >= 0 {
			break
		}
		if !tableArraySwapPureBetween(instr) {
			return -1, -1, -1
		}
	}
	if storeAIdx < 0 || storeBIdx < 0 {
		return -1, -1, -1
	}
	return loadBIdx, storeAIdx, storeBIdx
}

func tableArraySwapSecondLoad(loadA, loadB *Instr, uses map[int]int) bool {
	if !tableArraySwapLoadCandidate(loadB) || uses[loadB.ID] != 1 {
		return false
	}
	return loadA.Aux == loadB.Aux &&
		loadA.Type == loadB.Type &&
		len(loadA.Args) >= 2 && len(loadB.Args) >= 2 &&
		loadA.Args[0] != nil && loadB.Args[0] != nil &&
		loadA.Args[1] != nil && loadB.Args[1] != nil &&
		loadA.Args[0].ID == loadB.Args[0].ID &&
		loadA.Args[1].ID == loadB.Args[1].ID
}

func tableArraySwapStoreMatches(store *Instr, table *Value, loadA, loadB *Instr, key *Value, valueID int) bool {
	if store == nil || store.Op != OpTableArrayStore || len(store.Args) < 5 ||
		table == nil || key == nil || loadA == nil || loadB == nil {
		return false
	}
	return store.Aux == loadA.Aux &&
		store.Aux2 == 0 &&
		store.Args[0] != nil && store.Args[0].ID == table.ID &&
		store.Args[1] != nil && store.Args[1].ID == loadA.Args[0].ID &&
		store.Args[2] != nil && store.Args[2].ID == loadA.Args[1].ID &&
		store.Args[3] != nil && equivalentIntValue(store.Args[3], key, 4) &&
		store.Args[4] != nil && store.Args[4].ID == valueID
}

func tableArraySwapPureBetween(instr *Instr) bool {
	if instr == nil {
		return true
	}
	if hasSideEffect(instr) {
		return false
	}
	switch instr.Op {
	case OpConstInt, OpConstFloat, OpConstBool, OpConstNil,
		OpAddInt, OpSubInt, OpMulInt, OpNegInt,
		OpBoxInt, OpUnboxInt,
		OpNop:
		return true
	default:
		return false
	}
}

func equivalentIntValue(a, b *Value, depth int) bool {
	if a == nil || b == nil {
		return false
	}
	if a.ID == b.ID {
		return true
	}
	if depth <= 0 || a.Def == nil || b.Def == nil {
		return false
	}
	if a.Def.Op != b.Def.Op {
		return false
	}
	switch a.Def.Op {
	case OpConstInt:
		return a.Def.Aux == b.Def.Aux
	case OpAddInt, OpSubInt:
		if len(a.Def.Args) < 2 || len(b.Def.Args) < 2 {
			return false
		}
		return equivalentIntValue(a.Def.Args[0], b.Def.Args[0], depth-1) &&
			equivalentIntValue(a.Def.Args[1], b.Def.Args[1], depth-1)
	default:
		return false
	}
}
