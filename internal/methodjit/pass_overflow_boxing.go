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
	overflowCheckedRaw := collectOverflowCheckedLinearInductionDeps(fn)
	boxed := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if loopCarriedDeps[instr.ID] &&
				!overflowCheckedRaw[instr.ID] &&
				isUnsafeIntArithmetic(fn, instr) {
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
						(loopCarriedDeps[instr.ID] &&
							!overflowCheckedRaw[instr.ID] &&
							isUnsafeIntArithmetic(fn, instr)) {
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

// collectOverflowCheckedLinearInductionDeps finds loop-carried values that
// should stay raw even when their full int48 range is not statically proven.
//
// This is deliberately narrower than "all unsafe int arithmetic":
//   - the value must be a header Phi for a linear induction;
//   - the update must be AddInt of the Phi by a loop-invariant,
//     non-negative step;
//   - the header guard must bound the Phi itself (phi <= bound or phi < bound).
//
// In that shape an overflow in the update is handled by the raw AddInt
// overflow check before the next iteration observes the value. Keeping the Phi
// raw avoids boxing hot induction loops like sieve's inner `j += i`, while
// multiplicative/modulo recurrences such as LCGs remain boxed to avoid
// predictable deopt storms.
func collectOverflowCheckedLinearInductionDeps(fn *Function) map[int]bool {
	keep := make(map[int]bool)
	if fn == nil || len(fn.Blocks) == 0 {
		return keep
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return keep
	}

	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		cond := loopHeaderBranchCond(header)
		for _, phi := range header.Instrs {
			if phi.Op != OpPhi {
				break
			}
			if !phi.Type.isIntegerLike() {
				continue
			}
			update, init, ok := linearInductionUpdate(phi, li.headerBlocks[header.ID])
			if !ok {
				continue
			}
			if !guardBoundsLinearInduction(cond, phi, update) {
				continue
			}
			step := linearInductionStepValue(update, phi.ID)
			if !nonNegativeLoopInvariantStep(fn, step, li.headerBlocks[header.ID]) {
				continue
			}
			keep[phi.ID] = true
			keep[update.ID] = true
			markOverflowCheckedArithmeticDeps(init.Def, keep)
		}
	}
	return keep
}

func guardBoundsPhi(cond *Instr, phi *Instr) bool {
	if cond == nil || phi == nil || len(cond.Args) < 2 {
		return false
	}
	switch cond.Op {
	case OpLe, OpLeInt, OpLt, OpLtInt:
	default:
		return false
	}
	return cond.Args[0] != nil && cond.Args[0].ID == phi.ID
}

func guardBoundsLinearInduction(cond *Instr, phi *Instr, update *Instr) bool {
	if guardBoundsPhi(cond, phi) {
		return true
	}
	if cond == nil || update == nil || len(cond.Args) < 2 {
		return false
	}
	switch cond.Op {
	case OpLe, OpLeInt, OpLt, OpLtInt:
	default:
		return false
	}
	return cond.Args[0] != nil && cond.Args[0].ID == update.ID
}

func linearInductionUpdate(phi *Instr, body map[int]bool) (update *Instr, init *Value, ok bool) {
	if phi == nil || body == nil {
		return nil, nil, false
	}
	for predIdx, arg := range phi.Args {
		if arg == nil || arg.Def == nil {
			continue
		}
		fromLoop := false
		if predIdx < len(phi.Block.Preds) {
			fromLoop = body[phi.Block.Preds[predIdx].ID]
		} else if arg.Def.Block != nil {
			fromLoop = body[arg.Def.Block.ID]
		}
		if fromLoop {
			if !isLinearSelfUpdate(arg.Def, phi.ID) || update != nil {
				return nil, nil, false
			}
			update = arg.Def
			continue
		}
		if init != nil {
			return nil, nil, false
		}
		init = arg
	}
	return update, init, update != nil && init != nil
}

func isLinearSelfUpdate(instr *Instr, phiID int) bool {
	if instr == nil || len(instr.Args) < 2 {
		return false
	}
	if instr.Op == OpAddInt {
		return (instr.Args[0] != nil && instr.Args[0].ID == phiID) ||
			(instr.Args[1] != nil && instr.Args[1].ID == phiID)
	}
	return false
}

func linearInductionStepValue(update *Instr, phiID int) *Value {
	if update == nil || len(update.Args) < 2 {
		return nil
	}
	if update.Op == OpAddInt {
		if update.Args[0] != nil && update.Args[0].ID == phiID {
			return update.Args[1]
		}
		if update.Args[1] != nil && update.Args[1].ID == phiID {
			return update.Args[0]
		}
	}
	return nil
}

func nonNegativeLoopInvariantStep(fn *Function, step *Value, body map[int]bool) bool {
	if step == nil || step.Def == nil {
		return false
	}
	if step.Def.Block != nil && body[step.Def.Block.ID] {
		return false
	}
	if c, ok := constIntFromValue(step); ok {
		return c >= 0
	}
	if fn == nil || fn.IntRanges == nil {
		return false
	}
	r, ok := fn.IntRanges[step.ID]
	return ok && r.known && r.min >= 0
}

func markOverflowCheckedArithmeticDeps(instr *Instr, keep map[int]bool) {
	if instr == nil || keep[instr.ID] {
		return
	}
	switch instr.Op {
	case OpAddInt, OpSubInt, OpMulInt, OpNegInt:
		keep[instr.ID] = true
		for _, arg := range instr.Args {
			if arg != nil {
				markOverflowCheckedArithmeticDeps(arg.Def, keep)
			}
		}
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
