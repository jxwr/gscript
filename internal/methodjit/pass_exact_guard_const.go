package methodjit

// ExactGuardConstPass materializes exact integer range guards as constants for
// downstream uses while keeping the guard itself as the runtime deopt check.
func ExactGuardConstPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for i := 0; i < len(block.Instrs); i++ {
			guard := block.Instrs[i]
			if guard == nil || guard.Op != OpGuardIntRange || guard.Aux != guard.Aux2 {
				continue
			}
			c := &Instr{
				ID:    fn.newValueID(),
				Op:    OpConstInt,
				Type:  TypeInt,
				Aux:   guard.Aux,
				Aux2:  1,
				Block: block,
			}
			block.Instrs = append(block.Instrs, nil)
			copy(block.Instrs[i+2:], block.Instrs[i+1:])
			block.Instrs[i+1] = c
			replaceUsesAfterGuard(fn, guard.ID, c, c.ID)
			functionRemarks(fn).Add("ExactGuardConst", "changed", block.ID, c.ID, c.Op,
				"materialized exact guarded int as constant")
			i++
		}
	}
	return fn, nil
}
