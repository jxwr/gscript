//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// findAccumulators finds registers in the loop body that are both source and
// destination of arithmetic operations (e.g., s = s + i → R(s) is an accumulator).
// Also detects indirect accumulators: ADD Rtemp, Raccum, Rx; MOVE Raccum, Rtemp
// (where the compiler uses a temporary for s = s + i).
// Excludes for-loop control registers (aReg..aReg+3).
// Safety: excludes registers that are also written by non-integer-producing
// instructions (MOVE, LOADK with non-int constant, call-exit ops like GETTABLE,
// GETFIELD, etc.), because pinning such registers would corrupt non-integer values.
func (cg *Codegen) findAccumulators(bodyStart, bodyEnd, aReg int) []int {
	counts := make(map[int]int)
	code := cg.proto.Code
	for pc := bodyStart; pc < bodyEnd; pc++ {
		inst := code[pc]
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL:
			a := vm.DecodeA(inst)
			b := vm.DecodeB(inst)
			c := vm.DecodeC(inst)
			// Skip loop control registers
			if a >= aReg && a <= aReg+3 {
				continue
			}
			// Direct accumulator: s = s + i (R(A) is both source and dest)
			if (!vm.IsRK(b) && b == a) || (!vm.IsRK(c) && c == a) {
				counts[a]++
				continue
			}
			// Indirect accumulator: ADD Rtemp, Raccum, Rx; MOVE Raccum, Rtemp
			if pc+1 < bodyEnd && vm.DecodeOp(code[pc+1]) == vm.OP_MOVE {
				moveA := vm.DecodeA(code[pc+1])
				moveB := vm.DecodeB(code[pc+1])
				if moveB == a { // MOVE copies the ADD result
					// Check if the accumulator (moveA) is one of the ADD sources
					isAccum := (!vm.IsRK(b) && b == moveA) || (!vm.IsRK(c) && c == moveA)
					if isAccum && !(moveA >= aReg && moveA <= aReg+3) {
						counts[moveA]++ // pin the accumulator
						counts[a]++     // pin the temporary too
					}
				}
			}

		case vm.OP_CALL:
			// Detect inlined call accumulator pattern:
			//   MOVE R(fnReg+1) R(accum)  -- arg setup (copies accumulator to arg slot)
			//   ... (other arg setups)
			//   CALL R(fnReg) B=n C=2     -- inlined: result = f(args)
			//   MOVE R(accum) R(fnReg)    -- copy result back to accumulator
			//
			// This is an indirect accumulator through the inlined function.
			if cg.inlineCandidates == nil {
				continue
			}
			candidate, isInline := cg.inlineCandidates[pc]
			if !isInline || candidate.isSelfCall {
				continue
			}
			fnReg := candidate.fnReg
			// Skip loop control registers
			if fnReg >= aReg && fnReg <= aReg+3 {
				continue
			}
			// Check if the next instruction is MOVE R(accum) R(fnReg)
			if pc+1 >= bodyEnd || vm.DecodeOp(code[pc+1]) != vm.OP_MOVE {
				continue
			}
			moveA := vm.DecodeA(code[pc+1])
			moveB := vm.DecodeB(code[pc+1])
			if moveB != fnReg {
				continue
			}
			// moveA is the accumulator candidate. Check that it's used as an argument.
			// Scan backward from the CALL to find MOVE R(fnReg+k) R(moveA).
			isAccum := false
			for scanPC := pc - 1; scanPC >= bodyStart && scanPC >= pc-10; scanPC-- {
				si := code[scanPC]
				if vm.DecodeOp(si) == vm.OP_MOVE {
					srcReg := vm.DecodeB(si)
					dstReg := vm.DecodeA(si)
					if srcReg == moveA && dstReg > fnReg && dstReg <= fnReg+candidate.nArgs {
						isAccum = true
						break
					}
				}
			}
			if isAccum && !(moveA >= aReg && moveA <= aReg+3) {
				counts[moveA]++
				counts[fnReg]++ // pin the result register too
			}
		}
	}

	// Safety check: exclude registers that are written by non-integer-producing
	// instructions anywhere in the loop body. Pinning such registers would corrupt
	// non-integer values (tables, strings) during spill/reload cycles.
	unsafe := make(map[int]bool)
	for pc := bodyStart; pc < bodyEnd; pc++ {
		inst := code[pc]
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_MOVE:
			// MOVE R(A) = R(B) — safe if source is known-int at this PC.
			b := vm.DecodeB(inst)
			if cg.knownInt != nil && pc < len(cg.knownInt) && regSetHas(cg.knownInt[pc], b) {
				// Source is known-int, so this MOVE produces an int value — safe for pinning.
			} else {
				unsafe[a] = true
			}
		case vm.OP_LOADK:
			// LOADK with non-int constant writes a non-integer value
			bx := vm.DecodeBx(inst)
			if bx < len(cg.proto.Constants) && !cg.proto.Constants[bx].IsInt() {
				unsafe[a] = true
			}
		case vm.OP_LOADNIL, vm.OP_LOADBOOL:
			unsafe[a] = true
		case vm.OP_GETTABLE, vm.OP_GETFIELD, vm.OP_GETGLOBAL, vm.OP_GETUPVAL:
			unsafe[a] = true
		case vm.OP_CALL:
			// Inlined calls produce int results — safe for pinning.
			if cg.inlineCandidates != nil {
				if _, inlined := cg.inlineCandidates[pc]; inlined {
					break // safe: inlined call always produces int
				}
			}
			unsafe[a] = true
		case vm.OP_NEWTABLE:
			unsafe[a] = true
		case vm.OP_LEN, vm.OP_CONCAT:
			unsafe[a] = true
		case vm.OP_SELF:
			unsafe[a] = true
			unsafe[a+1] = true
		case vm.OP_TESTSET:
			unsafe[a] = true
		}
	}

	// Return accumulators sorted by frequency (up to 3), excluding unsafe ones.
	var result []int
	for reg := range counts {
		if unsafe[reg] {
			continue
		}
		result = append(result, reg)
	}
	// Simple sort by count (descending)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if counts[result[j]] > counts[result[i]] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	if len(result) > 3 {
		result = result[:3]
	}
	return result
}

// instructionReadsPinned returns true if the instruction reads from a pinned
// register (R(0) or R(1)) as a source operand. Used to determine if X19/X22
// must be saved across self-recursive calls.
func (cg *Codegen) instructionReadsPinned(inst uint32, op vm.Opcode) bool {
	numParams := cg.proto.NumParams
	switch op {
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_POW:
		// Reads B and C (which may be RK references)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		if !vm.IsRK(b) && b < numParams {
			return true
		}
		if !vm.IsRK(c) && c < numParams {
			return true
		}
	case vm.OP_MOVE:
		// Reads B
		b := vm.DecodeB(inst)
		if b < numParams {
			return true
		}
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
		// Reads B and C
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		if !vm.IsRK(b) && b < numParams {
			return true
		}
		if !vm.IsRK(c) && c < numParams {
			return true
		}
	case vm.OP_UNM, vm.OP_NOT, vm.OP_LEN:
		// Reads B
		b := vm.DecodeB(inst)
		if b < numParams {
			return true
		}
	case vm.OP_TEST:
		// Reads A
		a := vm.DecodeA(inst)
		if a < numParams {
			return true
		}
	case vm.OP_LOADINT, vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_LOADK, vm.OP_JMP:
		// These don't read from registers
		return false
	default:
		// Conservative: assume it reads pinned registers
		return true
	}
	return false
}

// emitForPrep: R(A) -= R(A+2); PC += sBx
// Integer specialization: guards that init/limit/step are all TypeInt.
func (cg *Codegen) emitForPrep(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	exitLabel := fmt.Sprintf("forprep_exit_%d", pc)

	// Type guard: R(A), R(A+1), R(A+2) must all be TypeInt
	cg.loadRegTyp(X0, aReg)
	cg.emitCmpTag(X0, NB_TagIntShr48)
	cg.asm.BCond(CondNE, exitLabel)

	cg.loadRegTyp(X0, aReg+1)
	cg.emitCmpTag(X0, NB_TagIntShr48)
	cg.asm.BCond(CondNE, exitLabel)

	cg.loadRegTyp(X0, aReg+2)
	cg.emitCmpTag(X0, NB_TagIntShr48)
	cg.asm.BCond(CondNE, exitLabel)

	// Guard failure deferred to cold section (no pinning active at FORPREP).
	cg.deferCold(exitLabel, func() {
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
	})

	// Set up register pinning if this loop was analyzed and is innermost.
	desc := cg.forLoops[pc]
	if desc != nil && desc.canPin {
		cg.setupLoopPinning(desc)

		// Pre-set R(A+3).typ = TypeInt in memory (once, before loop).
		cg.asm.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg+3)

		// Also set typ for body accumulators (they're pinned, typ won't be written in body).
		for _, vmReg := range desc.bodyAccumulators {
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}

		// R(A) -= R(A+2) using pinned registers.
		idxReg := cg.pinnedRegs[aReg]
		stepReg := cg.pinnedRegs[aReg+2]
		cg.asm.SUBreg(idxReg, idxReg, stepReg)
	} else {
		// Fallback: no pinning.
		cg.loadRegIval(X0, aReg)
		cg.loadRegIval(X1, aReg+2)
		cg.asm.SUBreg(X0, X0, X1)
		cg.storeRegIval(X0, aReg)
	}

	// Jump to FORLOOP (pc + 1 + sbx)
	target := pc + 1 + sbx
	cg.asm.B(pcLabel(target))
	return nil
}

// emitForLoop: R(A) += R(A+2); if in range: R(A+3) = R(A), PC += sBx.
// Optimized with register pinning and step-sign specialization.

func (cg *Codegen) emitForLoop(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	desc := cg.forLoops[pc]
	loopBody := pcLabel(pc + 1 + sbx)
	exitFor := fmt.Sprintf("forloop_exit_%d", pc)

	if desc != nil && len(cg.pinnedRegs) > 0 {
		idxReg := cg.pinnedRegs[aReg]
		limitReg := cg.pinnedRegs[aReg+1]
		stepReg := cg.pinnedRegs[aReg+2]
		loopVarReg := cg.pinnedRegs[aReg+3]

		if desc.stepKnown && desc.stepValue > 0 {
			// Optimized: known positive step — bottom-tested loop.
			if desc.stepValue == 1 {
				cg.asm.ADDimm(idxReg, idxReg, 1)
			} else {
				cg.asm.ADDreg(idxReg, idxReg, stepReg)
			}
			cg.asm.CMPreg(idxReg, limitReg)

			if loopVarReg != idxReg {
				cg.asm.MOVreg(loopVarReg, idxReg)
			}
			// Conditional back-edge: continue if idx <= limit.
			cg.asm.BCond(CondLE, loopBody)
			// Fall-through: loop done → jump to cold exit stub.
			cg.asm.B(exitFor)
		} else if desc.stepKnown && desc.stepValue < 0 {
			// Optimized: known negative step — bottom-tested loop.
			if desc.stepValue == -1 {
				cg.asm.SUBimm(idxReg, idxReg, 1)
			} else {
				cg.asm.ADDreg(idxReg, idxReg, stepReg)
			}
			cg.asm.CMPreg(idxReg, limitReg)

			if loopVarReg != idxReg {
				cg.asm.MOVreg(loopVarReg, idxReg)
			}
			// Conditional back-edge: continue if idx >= limit.
			cg.asm.BCond(CondGE, loopBody)
			// Fall-through: loop done → jump to cold exit stub.
			cg.asm.B(exitFor)
		} else {
			// Unknown step sign: general path with pinned registers.
			cg.asm.ADDreg(idxReg, idxReg, stepReg)

			cg.asm.CMPimm(stepReg, 0)
			negStep := fmt.Sprintf("forloop_neg_%d", pc)
			cg.asm.BCond(CondLT, negStep)

			cg.asm.CMPreg(idxReg, limitReg)
			cg.asm.BCond(CondGT, exitFor)
			cg.asm.B(fmt.Sprintf("forloop_cont_%d", pc))

			cg.asm.Label(negStep)
			cg.asm.CMPreg(idxReg, limitReg)
			cg.asm.BCond(CondLT, exitFor)

			cg.asm.Label(fmt.Sprintf("forloop_cont_%d", pc))
			if loopVarReg != idxReg {
				cg.asm.MOVreg(loopVarReg, idxReg)
			}
			cg.asm.B(loopBody)
		}

		// Loop exit: spill code deferred to cold section.
		// Capture pinning state before clearing (spillPinnedRegs uses pinnedRegs).
		capturedPinnedRegs := make(map[int]Reg, len(cg.pinnedRegs))
		for k, v := range cg.pinnedRegs {
			capturedPinnedRegs[k] = v
		}
		nextPC := pcLabel(pc + 1)
		cg.deferCold(exitFor, func() {
			for vmReg, armReg := range capturedPinnedRegs {
				cg.spillPinnedRegNB(vmReg, armReg)
			}
			cg.asm.B(nextPC) // jump back to hot path after loop
		})
		cg.clearPinning()
	} else {
		// Fallback: no pinning (original code).
		cg.loadRegIval(X0, aReg)
		cg.loadRegIval(X1, aReg+2)
		cg.asm.ADDreg(X0, X0, X1)
		cg.storeRegIval(X0, aReg)

		cg.loadRegIval(X2, aReg+1)

		cg.asm.CMPimm(X1, 0)
		negStep := fmt.Sprintf("forloop_neg_%d", pc)
		cg.asm.BCond(CondLT, negStep)

		cg.asm.CMPreg(X0, X2)
		cg.asm.BCond(CondGT, exitFor)
		cg.asm.B(fmt.Sprintf("forloop_cont_%d", pc))

		cg.asm.Label(negStep)
		cg.asm.CMPreg(X0, X2)
		cg.asm.BCond(CondLT, exitFor)

		cg.asm.Label(fmt.Sprintf("forloop_cont_%d", pc))
		cg.storeIntValue(aReg+3, X0)
		cg.asm.B(loopBody)

		cg.asm.Label(exitFor)
	}
	return nil
}
