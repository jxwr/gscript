package methodjit

// ConstantPhiBranchThreadingPass bypasses join blocks whose branch condition is
// fully determined by per-predecessor constant phi inputs. The rewrite is a
// guarded-version CFG cleanup: earlier passes may create runtime guards, and
// this pass removes the residual branch fan-in inside that specialized body.
func ConstantPhiBranchThreadingPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, block := range append([]*Block(nil), fn.Blocks...) {
		if threadConstantPhiBranchBlock(fn, block) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func threadConstantPhiBranchBlock(fn *Function, block *Block) bool {
	if fn == nil || block == nil || block == fn.Entry || len(block.Preds) == 0 || len(block.Succs) != 2 || len(block.Instrs) == 0 {
		return false
	}
	term := block.Instrs[len(block.Instrs)-1]
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 {
		return false
	}
	if !constantPhiBranchBlockIsPure(block) {
		return false
	}
	outcomes, ok := constantPhiBranchOutcomes(block, term.Args[0])
	if !ok || len(outcomes) != len(block.Preds) {
		return false
	}
	oldPreds := append([]*Block(nil), block.Preds...)
	for _, pred := range oldPreds {
		if pred == nil || !containsBlock(pred.Succs, block) {
			return false
		}
		outcome, ok := outcomes[pred.ID]
		if !ok {
			return false
		}
		succ := block.Succs[1]
		if outcome {
			succ = block.Succs[0]
		}
		if containsBlock(succ.Preds, pred) {
			return false
		}
	}

	for _, pred := range oldPreds {
		outcome := outcomes[pred.ID]
		succ := block.Succs[1]
		if outcome {
			succ = block.Succs[0]
		}
		if !redirectPredAroundBlock(pred, block, succ) {
			return false
		}
		functionRemarks(fn).Add("ConstantPhiBranchThreading", "changed", block.ID, term.ID, term.Op,
			"threaded constant phi branch predecessor")
	}
	block.Preds = nil
	for _, instr := range block.Instrs {
		if instr != nil && instr.Op == OpPhi {
			instr.Args = nil
		}
	}
	return true
}

func constantPhiBranchBlockIsPure(block *Block) bool {
	for i, instr := range block.Instrs {
		if instr == nil {
			return false
		}
		if i == len(block.Instrs)-1 {
			return instr.Op == OpBranch
		}
		switch instr.Op {
		case OpPhi, OpConstInt, OpConstBool, OpEqInt, OpNot:
		default:
			return false
		}
	}
	return false
}

func constantPhiBranchOutcomes(block *Block, cond *Value) (map[int]bool, bool) {
	if block == nil || cond == nil || cond.Def == nil {
		return nil, false
	}
	switch cond.Def.Op {
	case OpPhi:
		return boolPhiOutcomes(block, cond.Def)
	case OpNot:
		if len(cond.Def.Args) != 1 {
			return nil, false
		}
		out, ok := constantPhiBranchOutcomes(block, cond.Def.Args[0])
		if !ok {
			return nil, false
		}
		for predID, v := range out {
			out[predID] = !v
		}
		return out, true
	case OpEqInt:
		return eqIntPhiOutcomes(block, cond.Def)
	default:
		return nil, false
	}
}

func boolPhiOutcomes(block *Block, phi *Instr) (map[int]bool, bool) {
	if phi == nil || phi.Op != OpPhi || len(phi.Args) != len(block.Preds) {
		return nil, false
	}
	out := make(map[int]bool, len(block.Preds))
	for i, pred := range block.Preds {
		arg := phi.Args[i]
		if pred == nil || arg == nil || arg.Def == nil || arg.Def.Op != OpConstBool {
			return nil, false
		}
		out[pred.ID] = arg.Def.Aux != 0
	}
	return out, true
}

func eqIntPhiOutcomes(block *Block, eq *Instr) (map[int]bool, bool) {
	if eq == nil || eq.Op != OpEqInt || len(eq.Args) != 2 {
		return nil, false
	}
	phi, c, ok := intPhiConstPair(eq.Args[0], eq.Args[1])
	if !ok {
		phi, c, ok = intPhiConstPair(eq.Args[1], eq.Args[0])
	}
	if !ok || len(phi.Args) != len(block.Preds) {
		return nil, false
	}
	out := make(map[int]bool, len(block.Preds))
	for i, pred := range block.Preds {
		arg := phi.Args[i]
		if pred == nil || arg == nil || arg.Def == nil || arg.Def.Op != OpConstInt {
			return nil, false
		}
		out[pred.ID] = arg.Def.Aux == c
	}
	return out, true
}

func intPhiConstPair(a, b *Value) (*Instr, int64, bool) {
	if a == nil || a.Def == nil || a.Def.Op != OpPhi || b == nil || b.Def == nil || b.Def.Op != OpConstInt {
		return nil, 0, false
	}
	return a.Def, b.Def.Aux, true
}

func redirectPredAroundBlock(pred, block, succ *Block) bool {
	oldIdx := predIndex(succ, block)
	if oldIdx < 0 {
		return false
	}
	for _, instr := range succ.Instrs {
		if instr == nil || instr.Op != OpPhi {
			break
		}
		if oldIdx >= len(instr.Args) {
			return false
		}
		instr.Args = append(instr.Args, instr.Args[oldIdx])
	}
	succ.Preds = append(succ.Preds, pred)
	for i, s := range pred.Succs {
		if s == block {
			pred.Succs[i] = succ
		}
	}
	return true
}

func removeUnreachableBlocks(fn *Function) {
	if fn == nil || fn.Entry == nil {
		return
	}
	reachable := make(map[*Block]bool)
	var walk func(*Block)
	walk = func(block *Block) {
		if block == nil || reachable[block] {
			return
		}
		reachable[block] = true
		for _, succ := range block.Succs {
			walk(succ)
		}
	}
	walk(fn.Entry)

	for _, block := range fn.Blocks {
		if reachable[block] {
			continue
		}
		for _, succ := range block.Succs {
			removePredAndPhiArgs(succ, block)
		}
		block.Succs = nil
		block.Preds = nil
	}
	out := fn.Blocks[:0]
	for _, block := range fn.Blocks {
		if reachable[block] {
			out = append(out, block)
		}
	}
	fn.Blocks = out
}

func removePredAndPhiArgs(block, pred *Block) {
	if block == nil || pred == nil {
		return
	}
	for {
		idx := predIndex(block, pred)
		if idx < 0 {
			return
		}
		block.Preds = append(block.Preds[:idx], block.Preds[idx+1:]...)
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpPhi {
				break
			}
			if idx < len(instr.Args) {
				instr.Args = append(instr.Args[:idx], instr.Args[idx+1:]...)
			}
		}
	}
}
