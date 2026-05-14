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
		if !tableArraySwapLoadCandidate(loadA) {
			continue
		}
		loadBIdx, storeAIdx, storeBIdx, table := findTableArraySwapTail(fn, block, i, loadA, uses)
		if loadBIdx < 0 {
			continue
		}

		loadB := block.Instrs[loadBIdx]
		loadB.Op = OpTableArraySwap
		loadB.Type = TypeUnknown
		loadB.Args = []*Value{table, loadA.Args[0], loadA.Args[1], loadA.Args[2], loadB.Args[2]}
		loadB.Aux = loadA.Aux
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

func findTableArraySwapTail(fn *Function, block *Block, loadAIdx int, loadA *Instr, uses map[int]int) (int, int, int, *Value) {
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
			return -1, -1, -1, nil
		}
	}
	if loadB == nil {
		return -1, -1, -1, nil
	}

	storeAIdx, storeBIdx := -1, -1
	var table *Value
	for j := loadBIdx + 1; j < len(block.Instrs); j++ {
		instr := block.Instrs[j]
		if instr == nil {
			continue
		}
		matchedStore := false
		if storeAIdx < 0 && tableArraySwapStoreMatches(instr, loadA, loadA.Args[2], loadB.ID) {
			if !tableArraySwapRecordTable(&table, instr.Args[0], instr.Aux) {
				return -1, -1, -1, nil
			}
			storeAIdx = j
			matchedStore = true
		}
		if storeBIdx < 0 && tableArraySwapStoreMatches(instr, loadA, loadB.Args[2], loadA.ID) {
			if !tableArraySwapRecordTable(&table, instr.Args[0], instr.Aux) {
				return -1, -1, -1, nil
			}
			storeBIdx = j
			matchedStore = true
		}
		if storeAIdx >= 0 && storeBIdx >= 0 {
			break
		}
		if matchedStore {
			continue
		}
		if !tableArraySwapPureBetween(instr) {
			return -1, -1, -1, nil
		}
	}
	if storeAIdx < 0 || storeBIdx < 0 || table == nil {
		return -1, -1, -1, nil
	}
	if !tableArraySwapLoadUseOnlyStores(block, loadA.ID, loadB.ID, storeAIdx, storeBIdx) {
		return -1, -1, -1, nil
	}
	return loadBIdx, storeAIdx, storeBIdx, table
}

func tableArraySwapSecondLoad(loadA, loadB *Instr, uses map[int]int) bool {
	if !tableArraySwapLoadCandidate(loadB) {
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

func tableArraySwapStoreMatches(store *Instr, loadA *Instr, key *Value, valueID int) bool {
	if store == nil || store.Op != OpTableArrayStore || len(store.Args) < 5 ||
		key == nil || loadA == nil {
		return false
	}
	return store.Aux == loadA.Aux &&
		store.Aux2 == 0 &&
		store.Args[1] != nil && store.Args[1].ID == loadA.Args[0].ID &&
		store.Args[2] != nil && store.Args[2].ID == loadA.Args[1].ID &&
		store.Args[3] != nil && equivalentIntValue(store.Args[3], key, 4) &&
		store.Args[4] != nil && store.Args[4].ID == valueID
}

func tableArraySwapRecordTable(current **Value, candidate *Value, kind int64) bool {
	if candidate == nil {
		return false
	}
	if *current == nil {
		*current = candidate
		return true
	}
	return tableArraySwapSameTable(candidate, *current, kind)
}

func tableArraySwapLoadUseOnlyStores(block *Block, loadAID, loadBID, storeAIdx, storeBIdx int) bool {
	if block == nil {
		return false
	}
	for i, instr := range block.Instrs {
		if instr == nil {
			continue
		}
		for _, arg := range instr.Args {
			if arg == nil || (arg.ID != loadAID && arg.ID != loadBID) {
				continue
			}
			if i == storeAIdx || i == storeBIdx {
				continue
			}
			return false
		}
	}
	return true
}

func tableArraySwapSameTable(storeTable, loadTable *Value, kind int64) bool {
	if storeTable == nil || loadTable == nil {
		return false
	}
	if storeTable.ID == loadTable.ID {
		return true
	}
	storeBase := tableArrayStoreFactTable(storeTable, kind)
	loadBase := tableArrayStoreFactTable(loadTable, kind)
	return storeBase != nil && loadBase != nil && storeBase.ID == loadBase.ID
}

func tableArraySwapPureBetween(instr *Instr) bool {
	if instr == nil {
		return true
	}
	if instr.Op == OpGuardTableKind {
		return true
	}
	if hasSideEffect(instr) {
		return false
	}
	switch instr.Op {
	case OpConstInt, OpConstFloat, OpConstBool, OpConstNil,
		OpTableArrayHeader, OpTableArrayLen, OpTableArrayData,
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
