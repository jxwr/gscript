package methodjit

// CallReturnProjectionPass turns a side-effecting call followed by a pure
// projection of its single result into one explicit call-projection op. This
// gives codegen one protocol owner for fast path, fallback, and callee-exit
// recovery instead of trying to fuse two independent instructions ad hoc.
func CallReturnProjectionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	uses := computeUseCounts(fn)
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr == nil || instr.Op != OpCall || uses[instr.ID] != 1 || i+1 >= len(block.Instrs) {
				continue
			}
			if fn.CallABIs == nil {
				continue
			}
			if _, ok := fn.CallABIs[instr.ID]; !ok {
				continue
			}
			next := block.Instrs[i+1]
			if next == nil || next.Op != OpFloor || len(next.Args) != 1 ||
				next.Args[0] == nil || next.Args[0].ID != instr.ID {
				continue
			}
			instr.Op = OpCallFloor
			instr.Type = TypeInt
			replaceValueUses(fn, next.ID, instr.Value(), instr.ID)
			next.Op = OpNop
			next.Type = TypeUnknown
			next.Args = nil
			next.Aux = 0
			next.Aux2 = 0
			functionRemarks(fn).Add("CallReturnProjection", "changed", block.ID, instr.ID, instr.Op,
				"folded single-use call result into floor projection")
		}
	}
	return fn, nil
}
