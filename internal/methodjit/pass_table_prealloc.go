package methodjit

import "github.com/gscript/gscript/internal/vm"

const tier2FeedbackArrayHint = 1024

// TablePreallocHintPass annotates empty table allocations that feed observed
// integer-key stores. The hint is consumed by the existing NewTable exit path,
// so allocation remains in Go while Tier 2 can use mixed-array append stores
// until capacity is exhausted.
func TablePreallocHintPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	candidates := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetTable || len(instr.Args) == 0 {
				continue
			}
			if instr.Aux2 == 0 || instr.Aux2 == int64(vm.FBKindPolymorphic) {
				continue
			}
			tbl := instr.Args[0]
			if tbl == nil || tbl.Def == nil || tbl.Def.Op != OpNewTable {
				continue
			}
			candidates[tbl.Def.ID] = true
		}
	}
	if len(candidates) == 0 {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == OpNewTable && instr.Aux == 0 && candidates[instr.ID] {
				instr.Aux = tier2FeedbackArrayHint
			}
		}
	}
	return fn, nil
}
