//go:build darwin && arm64

package methodjit

// RedundantGuardEliminationPass removes GuardType instructions whose input is
// already statically known to have the guarded type.
func RedundantGuardEliminationPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGuardType || len(instr.Args) == 0 {
				continue
			}
			arg := instr.Args[0]
			if arg == nil || arg.Def == nil {
				continue
			}
			guardType := Type(instr.Aux)
			if guardType == TypeUnknown || guardType == TypeAny {
				continue
			}
			if arg.Def.Type != guardType {
				continue
			}
			replaceAllUses(fn, instr.ID, arg.Def)
			instr.Op = OpNop
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			instr.Type = TypeUnknown
		}
	}
	return fn, nil
}
