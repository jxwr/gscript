package methodjit

// JumpOnlyThreadingPass removes single-predecessor blocks that contain only an
// unconditional jump. It preserves successor phi semantics by transferring the
// old jump-block incoming value to the predecessor edge.
func JumpOnlyThreadingPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, block := range append([]*Block(nil), fn.Blocks...) {
		if threadJumpOnlyBlock(fn, block) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func threadJumpOnlyBlock(fn *Function, block *Block) bool {
	if fn == nil || block == nil || block == fn.Entry || len(block.Preds) != 1 || len(block.Succs) != 1 || len(block.Instrs) != 1 {
		return false
	}
	if block.Instrs[0] == nil || block.Instrs[0].Op != OpJump {
		return false
	}
	pred := block.Preds[0]
	succ := block.Succs[0]
	if pred == nil || succ == nil || pred == succ || containsBlock(succ.Preds, pred) {
		return false
	}
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
	for i, s := range pred.Succs {
		if s == block {
			pred.Succs[i] = succ
		}
	}
	succ.Preds = append(succ.Preds, pred)
	block.Preds = nil
	functionRemarks(fn).Add("JumpOnlyThreading", "changed", block.ID, block.Instrs[0].ID, OpJump,
		"removed single-predecessor jump-only block")
	return true
}
