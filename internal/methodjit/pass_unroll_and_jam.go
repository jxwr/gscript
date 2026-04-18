// pass_unroll_and_jam.go — 2-way loop unroll-and-jam for float reductions.
//
// Targets the canonical innermost-loop pattern:
//   acc = Phi(0.0, new_acc)       // loop-carried float accumulator
//   iv  = Phi(init, iv + step)     // integer induction variable
//   ... body using iv ...
//   new_acc = acc + Mul(X, Y)      // reduction update
//
// Transform: clone the body with iv_alt = iv + step, add acc2 = Phi(0.0, new_acc2),
// step the IV by 2×step in the header, combine acc + acc2 at loop exit.
//
// Motivation (R47 + R60 measured): FMA fusion on a Phi accumulator serializes
// the critical path (Phi→Mul→Add→Phi, ~4 cycles latency per iter). Splitting
// the accumulator into two independent chains doubles ILP on M4's dual FMA
// pipes; downstream FMAFusionPass can then fuse each partial sum freely
// because neither chain's Phi is "alone" any more — both have independent
// forward progress.
//
// Scope: R62 ships the scaffold + pattern detection + a dry-run report.
// Later rounds add the actual transform + tail handler + pipeline wiring.

package methodjit

// UnrollAndJamPass detects float-reduction loops suitable for 2-way
// unroll-and-jam and (eventually) transforms them. In R62's initial form,
// it only identifies candidates and annotates them (no IR change) so the
// pipeline can run the pass as a no-op. Transform is added incrementally.
func UnrollAndJamPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}

	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return fn, nil
	}

	// For each loop header, check if it matches the float-reduction pattern.
	for headerID := range li.loopHeaders {
		header := findBlock(fn, headerID)
		if header == nil {
			continue
		}
		cand := detectFloatReductionLoop(fn, li, header)
		if cand == nil {
			continue
		}
		_ = cand // R62: detection only; no transform yet.
	}
	return fn, nil
}

// floatReductionCandidate holds the SSA shape of a candidate loop for
// 2-way unroll-and-jam. All fields refer to the ORIGINAL IR; the transform
// (later rounds) clones from these.
type floatReductionCandidate struct {
	header      *Block   // loop header block
	bodyBlock   *Block   // the single block containing the reduction body
	accPhi      *Instr   // OpPhi for the float accumulator, defined in header
	ivPhi       *Instr   // OpPhi for the integer induction variable
	stepInstr   *Instr   // the AddInt(ivPhi, ConstInt(step)) that advances iv
	step        int64    // the step value (must be a ConstInt)
	updateInstr *Instr   // the AddFloat(accPhi, MulFloat(...)) reduction update
	mulInstr    *Instr   // the MulFloat feeding updateInstr
}

// detectFloatReductionLoop inspects a loop header and returns a candidate
// if the loop matches the 2-way-unroll pattern; else nil.
func detectFloatReductionLoop(fn *Function, li *loopInfo, header *Block) *floatReductionCandidate {
	// 1. Find exactly one Phi of TypeFloat in the header.
	var accPhi, ivPhi *Instr
	floatPhiCount := 0
	intPhiCount := 0
	for _, instr := range header.Instrs {
		if instr.Op != OpPhi {
			continue
		}
		switch instr.Type {
		case TypeFloat:
			accPhi = instr
			floatPhiCount++
		case TypeInt:
			ivPhi = instr
			intPhiCount++
		}
	}
	if floatPhiCount != 1 || intPhiCount < 1 || accPhi == nil || ivPhi == nil {
		return nil
	}

	// 2. The back-edge input of accPhi should be an AddFloat(accPhi, MulFloat(...))
	//    defined in a block inside the loop body.
	updateInstr := findAccumUpdate(accPhi)
	if updateInstr == nil {
		return nil
	}
	// AddFloat(accPhi_ref, mulResult) or AddFloat(mulResult, accPhi_ref)
	var mulArg *Value
	if len(updateInstr.Args) != 2 {
		return nil
	}
	if updateInstr.Args[0].ID == accPhi.ID {
		mulArg = updateInstr.Args[1]
	} else if updateInstr.Args[1].ID == accPhi.ID {
		mulArg = updateInstr.Args[0]
	} else {
		return nil
	}
	if mulArg == nil || mulArg.Def == nil || mulArg.Def.Op != OpMulFloat {
		return nil
	}
	mulInstr := mulArg.Def

	// 3. Must be the only update to accPhi's back-edge (single body block).
	bodyBlock := updateInstr.Block
	if bodyBlock == nil {
		return nil
	}
	if !li.loopBlocks[bodyBlock.ID] {
		return nil
	}

	// 4. IV step detection: find an AddInt(ivPhi, ConstInt) in the loop that
	//    the ivPhi's back-edge input points to.
	stepInstr, stepVal := findIntIVStep(fn, li, ivPhi)
	if stepInstr == nil {
		return nil
	}

	return &floatReductionCandidate{
		header:      header,
		bodyBlock:   bodyBlock,
		accPhi:      accPhi,
		ivPhi:       ivPhi,
		stepInstr:   stepInstr,
		step:        stepVal,
		updateInstr: updateInstr,
		mulInstr:    mulInstr,
	}
}

// findAccumUpdate returns the AddFloat instruction that feeds the back-edge
// input of phi (i.e. the instruction computing phi's next-iteration value).
// Returns nil if not found or if the back-edge value isn't an AddFloat.
func findAccumUpdate(phi *Instr) *Instr {
	// Phi args are (preheader_val, back_edge_val) per the graph-builder
	// convention. The back-edge is whichever arg is defined inside the loop.
	// For simplicity: look for an AddFloat(phi, X) or AddFloat(X, phi) among
	// phi's args.
	for _, arg := range phi.Args {
		if arg == nil || arg.Def == nil {
			continue
		}
		if arg.Def.Op != OpAddFloat {
			continue
		}
		if len(arg.Def.Args) != 2 {
			continue
		}
		if arg.Def.Args[0].ID == phi.ID || arg.Def.Args[1].ID == phi.ID {
			return arg.Def
		}
	}
	return nil
}

// findIntIVStep returns the (AddInt, step_const) pair for a loop's IV phi.
// The step must be a ConstInt; step value is returned.
func findIntIVStep(fn *Function, li *loopInfo, ivPhi *Instr) (*Instr, int64) {
	for _, arg := range ivPhi.Args {
		if arg == nil || arg.Def == nil {
			continue
		}
		def := arg.Def
		if def.Op != OpAddInt {
			continue
		}
		if len(def.Args) != 2 {
			continue
		}
		// One arg should be ivPhi (or a value derived from ivPhi), the other
		// should be a ConstInt.
		var constArg *Instr
		for _, a := range def.Args {
			if a != nil && a.Def != nil && a.Def.Op == OpConstInt {
				constArg = a.Def
			}
		}
		if constArg == nil {
			continue
		}
		// Must be defined inside the loop for this to be the IV step.
		if def.Block == nil || !li.loopBlocks[def.Block.ID] {
			continue
		}
		return def, constArg.Aux
	}
	return nil, 0
}

// findBlock returns the *Block with the given ID, or nil if not found.
func findBlock(fn *Function, id int) *Block {
	for _, b := range fn.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}
