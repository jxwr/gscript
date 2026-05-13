//go:build darwin && arm64

package methodjit

import "fmt"

// LoopGlobalStoreSinkPass sinks repeated loop stores of a loop-invariant value
// to the loop exit when the loop is proven to execute at least once.
func LoopGlobalStoreSinkPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	if li == nil || !li.hasLoops() {
		return fn, nil
	}
	for headerID := range li.loopHeaders {
		header := findBlock(fn, headerID)
		if header == nil || !loopHeaderExecutesAtLeastOnce(li, header) {
			continue
		}
		exit := singleLoopExit(li, header)
		if exit == nil {
			continue
		}
		storeBlock, store, ok := sinkableLoopSetGlobal(fn, li, header)
		if !ok {
			continue
		}
		removeInstrFromBlock(storeBlock, store)
		store.Block = exit
		insertSideEffectAtBlockStart(exit, store)
		functionRemarks(fn).Add("LoopGlobalStoreSink", "changed", exit.ID, store.ID, store.Op,
			fmt.Sprintf("sank repeated global store globals[%d] from loop B%d to exit B%d", store.Aux, header.ID, exit.ID))
		return fn, nil
	}
	return fn, nil
}

func singleLoopExit(li *loopInfo, header *Block) *Block {
	if li == nil || header == nil {
		return nil
	}
	var exit *Block
	for _, succ := range header.Succs {
		if succ == nil || li.loopBlocks[succ.ID] {
			continue
		}
		if exit != nil {
			return nil
		}
		exit = succ
	}
	return exit
}

func sinkableLoopSetGlobal(fn *Function, li *loopInfo, header *Block) (*Block, *Instr, bool) {
	var storeBlock *Block
	var store *Instr
	var constIdx int64
	for _, block := range fn.Blocks {
		if block == nil || !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpCall, OpCallFloor, OpFieldCallFloor, OpResume, OpYield, OpSelf, OpGo, OpSend, OpRecv:
				return nil, nil, false
			case OpGetGlobal:
				if store != nil && instr.Aux == constIdx {
					return nil, nil, false
				}
			case OpSetGlobal:
				if len(instr.Args) != 1 || instr.Args[0] == nil || valueDefinedInLoop(li, instr.Args[0]) {
					return nil, nil, false
				}
				if store != nil {
					return nil, nil, false
				}
				storeBlock = block
				store = instr
				constIdx = instr.Aux
			}
		}
	}
	if store == nil || storeBlock == nil {
		return nil, nil, false
	}
	for _, block := range fn.Blocks {
		if block == nil || !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == OpGetGlobal && instr.Aux == constIdx {
				return nil, nil, false
			}
		}
	}
	return storeBlock, store, true
}

func valueDefinedInLoop(li *loopInfo, v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Block != nil && li.loopBlocks[v.Def.Block.ID]
}

func loopHeaderExecutesAtLeastOnce(li *loopInfo, header *Block) bool {
	if header == nil || len(header.Instrs) < 2 {
		return false
	}
	term := header.Instrs[len(header.Instrs)-1]
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 || term.Args[0] == nil || term.Args[0].Def == nil {
		return false
	}
	cond := term.Args[0].Def
	if cond.Op != OpLeInt || len(cond.Args) != 2 {
		return false
	}
	left, right := cond.Args[0], cond.Args[1]
	lc, lok := constIntValue(left)
	rc, rok := constIntValue(right)
	if !lok {
		lc, lok = firstLoopCompareValue(li, left, header)
	}
	return lok && rok && lc <= rc
}

func firstLoopCompareValue(li *loopInfo, v *Value, header *Block) (int64, bool) {
	if v == nil || v.Def == nil || v.Def.Op != OpAddInt || len(v.Def.Args) != 2 {
		return 0, false
	}
	var phi *Instr
	var step int64
	var hasStep bool
	for _, arg := range v.Def.Args {
		if arg == nil || arg.Def == nil {
			continue
		}
		if arg.Def.Op == OpPhi {
			phi = arg.Def
			continue
		}
		if c, ok := constIntValue(arg); ok {
			step = c
			hasStep = true
		}
	}
	if phi == nil || !hasStep || phi.Block != header {
		return 0, false
	}
	for i, pred := range header.Preds {
		if pred == nil || li.loopBlocks[pred.ID] {
			continue
		}
		if i >= 0 && i < len(phi.Args) {
			if init, ok := constIntValue(phi.Args[i]); ok {
				return init + step, true
			}
		}
	}
	return 0, false
}

func constIntValue(v *Value) (int64, bool) {
	if v == nil || v.Def == nil || v.Def.Op != OpConstInt {
		return 0, false
	}
	return v.Def.Aux, true
}

func removeInstrFromBlock(block *Block, target *Instr) {
	if block == nil || target == nil {
		return
	}
	for i, instr := range block.Instrs {
		if instr == target {
			block.Instrs = append(block.Instrs[:i], block.Instrs[i+1:]...)
			return
		}
	}
}

func insertSideEffectAtBlockStart(block *Block, instr *Instr) {
	if block == nil || instr == nil {
		return
	}
	idx := 0
	for idx < len(block.Instrs) && block.Instrs[idx] != nil && block.Instrs[idx].Op == OpPhi {
		idx++
	}
	block.Instrs = append(block.Instrs, nil)
	copy(block.Instrs[idx+1:], block.Instrs[idx:])
	block.Instrs[idx] = instr
}
