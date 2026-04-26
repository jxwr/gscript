package methodjit

// OverflowBoxingPass backs unsafe integer arithmetic out of the raw-int
// representation after RangeAnalysis has identified which ops are int48-safe.
//
// Raw-int loop phis are fast, but they cannot represent the VM's int-overflow
// semantics: when an int result leaves the signed int48 range, the VM promotes
// it to float and subsequent loop iterations carry a boxed float. For such
// values, staying in raw-int form would either deopt every call or corrupt a
// loop-carried phi. This pass converts the affected arithmetic SCC back to
// generic boxed numeric ops, where codegen can promote overflow to float in
// place and continue.
func OverflowBoxingPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}

	loopCarriedDeps := collectPhiArithmeticDeps(fn)
	boxed := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if loopCarriedDeps[instr.ID] && isUnsafeIntArithmetic(fn, instr) {
				boxed[instr.ID] = true
			}
		}
	}
	if len(boxed) == 0 {
		return fn, nil
	}

	changed := true
	for changed {
		changed = false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if boxed[instr.ID] {
					continue
				}
				switch instr.Op {
				case OpPhi:
					if anyArgBoxed(instr, boxed) {
						boxed[instr.ID] = true
						changed = true
					}
				case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt:
					if anyArgBoxed(instr, boxed) ||
						(loopCarriedDeps[instr.ID] && isUnsafeIntArithmetic(fn, instr)) {
						boxed[instr.ID] = true
						changed = true
					}
				}
			}
		}
	}

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if boxed[instr.ID] {
				switch instr.Op {
				case OpPhi:
					instr.Type = TypeUnknown
				case OpAddInt:
					instr.Op = OpAdd
					instr.Type = TypeUnknown
				case OpSubInt:
					instr.Op = OpSub
					instr.Type = TypeUnknown
				case OpMulInt:
					instr.Op = OpMul
					instr.Type = TypeUnknown
				case OpModInt:
					instr.Op = OpMod
					instr.Type = TypeUnknown
				case OpDivIntExact:
					instr.Op = OpDiv
					instr.Type = TypeUnknown
				case OpNegInt:
					instr.Op = OpUnm
					instr.Type = TypeUnknown
				}
			}
			if anyArgBoxed(instr, boxed) {
				switch instr.Op {
				case OpEqInt:
					instr.Op = OpEq
				case OpLtInt:
					instr.Op = OpLt
				case OpLeInt:
					instr.Op = OpLe
				}
			}
		}
	}

	return fn, nil
}

func collectPhiArithmeticDeps(fn *Function) map[int]bool {
	defs := make(map[int]*Instr)
	var worklist []int
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defs[instr.ID] = instr
			}
			if instr.Op == OpPhi {
				for _, arg := range instr.Args {
					if arg != nil {
						worklist = append(worklist, arg.ID)
					}
				}
			}
		}
	}

	deps := make(map[int]bool)
	for len(worklist) > 0 {
		id := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		if deps[id] {
			continue
		}
		instr := defs[id]
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpPhi, OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt:
			deps[id] = true
			for _, arg := range instr.Args {
				if arg != nil {
					worklist = append(worklist, arg.ID)
				}
			}
		}
	}
	return deps
}

func isUnsafeIntArithmetic(fn *Function, instr *Instr) bool {
	if instr == nil {
		return false
	}
	switch instr.Op {
	case OpAddInt, OpSubInt, OpMulInt, OpNegInt:
		if instr.Aux2 != 0 {
			return false
		}
		return fn.Int48Safe == nil || !fn.Int48Safe[instr.ID]
	case OpDivIntExact:
		return fn.Int48Safe == nil || !fn.Int48Safe[instr.ID]
	default:
		return false
	}
}

func anyArgBoxed(instr *Instr, boxed map[int]bool) bool {
	for _, arg := range instr.Args {
		if arg != nil && boxed[arg.ID] {
			return true
		}
	}
	return false
}
