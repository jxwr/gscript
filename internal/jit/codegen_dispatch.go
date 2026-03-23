//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// setupLoopPinning configures register pinning for a for-loop and emits
// code to load VM registers into ARM registers. Returns true if pinning was set up.
func (cg *Codegen) setupLoopPinning(desc *forLoopDesc) bool {
	// Check we have enough pinning registers.
	needed := 4 + len(desc.bodyAccumulators) // loop control (4) + body accumulators
	if needed > len(pinPool) {
		needed = len(pinPool)
	}

	a := desc.aReg
	poolIdx := 0

	// Pin loop control registers: R(A)=idx, R(A+1)=limit, R(A+2)=step, R(A+3)=loopvar
	for i := 0; i < 4 && poolIdx < len(pinPool); i++ {
		vmReg := a + i
		if i == 3 && desc.aliasLoopVar {
			// Alias R(A+3) to R(A) — no separate ARM register needed.
			cg.pinnedRegs[vmReg] = cg.pinnedRegs[a]
			// Don't add to pinnedVars (spill only through R(A)).
			continue
		}
		armReg := pinPool[poolIdx]
		poolIdx++
		cg.pinnedRegs[vmReg] = armReg
		cg.pinnedVars = append(cg.pinnedVars, vmReg)
	}

	// Pin body accumulators.
	for _, vmReg := range desc.bodyAccumulators {
		if poolIdx >= len(pinPool) {
			break
		}
		armReg := pinPool[poolIdx]
		poolIdx++
		cg.pinnedRegs[vmReg] = armReg
		cg.pinnedVars = append(cg.pinnedVars, vmReg)
	}

	// Load pinned registers from memory (NaN-boxed → unbox int).
	for _, vmReg := range cg.pinnedVars {
		armReg := cg.pinnedRegs[vmReg]
		cg.asm.LDR(armReg, regRegs, regValOffset(vmReg))
		EmitUnboxInt(cg.asm, armReg, armReg)
	}

	return true
}

// spillPinnedRegs stores all pinned ARM registers back to VM register memory.
// With NaN-boxing, boxes the raw int and writes a single 8-byte NaN-boxed Value.
func (cg *Codegen) spillPinnedRegs() {
	for vmReg, armReg := range cg.pinnedRegs {
		cg.spillPinnedRegNB(vmReg, armReg)
	}
}

// reloadPinnedRegs loads all pinned ARM registers from VM register memory.
// With NaN-boxing, loads the NaN-boxed value and unboxes the 48-bit int.
func (cg *Codegen) reloadPinnedRegs() {
	for _, vmReg := range cg.pinnedVars {
		armReg := cg.pinnedRegs[vmReg]
		off := regValOffset(vmReg)
		cg.asm.LDR(armReg, regRegs, off)
		EmitUnboxInt(cg.asm, armReg, armReg)
	}
}

// clearPinning removes all register pinning.
func (cg *Codegen) clearPinning() {
	cg.pinnedRegs = make(map[int]Reg)
	cg.pinnedVars = nil
}

// resumeLabel returns the label name for the resume point after a call-exit at pc.
func resumeLabel(pc int) string {
	return fmt.Sprintf("resume_after_%d", pc)
}

// ──────────────────────────────────────────────────────────────────────────────
// Body compilation
// ──────────────────────────────────────────────────────────────────────────────

func (cg *Codegen) emitBody() error {
	code := cg.proto.Code

	// Build set of call-exit PCs for fast lookup.
	callExitSet := make(map[int]bool, len(cg.callExitPCs))
	for _, pc := range cg.callExitPCs {
		callExitSet[pc] = true
	}

	// Emit code for each instruction.
	for pc := 0; pc < len(code); pc++ {
		cg.asm.Label(pcLabel(pc))
		inst := code[pc]
		op := vm.DecodeOp(inst)

		// Skip GETGLOBAL instructions that are part of an inline candidate or cross-call
		if cg.inlineSkipPCs[pc] || cg.crossCallSkipPCs[pc] {
			continue
		}

		// Skip argument setup instructions (MOVE/LOADINT) that were traced
		// through by inline call optimization. The inline code reads the
		// actual source directly, so these stores are dead.
		if cg.inlineArgSkipPCs[pc] {
			continue
		}

		// Handle inlined CALL and self-recursive CALL instructions
		if candidate, ok := cg.inlineCandidates[pc]; ok {
			if candidate.isSelfCall {
				if err := cg.emitSelfCall(pc, candidate); err != nil {
					return fmt.Errorf("pc %d (self-call): %w", pc, err)
				}
			} else {
				if err := cg.emitInlineCall(pc, candidate); err != nil {
					return fmt.Errorf("pc %d (inline): %w", pc, err)
				}
			}
			continue
		}

		// Handle cross-call CALL instructions (direct BLR to compiled callee)
		if ccInfo, ok := cg.crossCalls[pc]; ok {
			if err := cg.emitCrossCall(pc, ccInfo); err != nil {
				return fmt.Errorf("pc %d (cross-call): %w", pc, err)
			}
			continue
		}

		if !cg.isSupported(op) {
			if callExitSet[pc] {
				// Call-exit: jump to cold stub for spill+exit, then resume inline.
				coldLabel := fmt.Sprintf("cold_callexit_%d", pc)
				capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
				for k, v := range cg.pinnedRegs {
					capturedPinned[k] = v
				}
				cg.asm.B(coldLabel)
				cg.deferCold(coldLabel, func() {
					for vmReg, armReg := range capturedPinned {
						cg.spillPinnedRegNB(vmReg, armReg)
					}
					cg.asm.LoadImm64(X1, int64(pc))
					cg.asm.STR(X1, regCtx, ctxOffExitPC)
					cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit, resumable)
					cg.asm.B("epilogue")
				})

				// Resume label: re-entry point after executor handles the instruction.
				cg.asm.Label(resumeLabel(pc))
				cg.asm.LDR(regRegs, regCtx, ctxOffRegs) // reload in case regs were reallocated
				cg.reloadPinnedRegs()
				continue // next pc label is emitted by the loop
			}

			// Permanent side exit: jump to cold stub.
			coldLabel := fmt.Sprintf("cold_sideexit_%d", pc)
			capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
			for k, v := range cg.pinnedRegs {
				capturedPinned[k] = v
			}
			cg.asm.B(coldLabel)
			cg.deferCold(coldLabel, func() {
				for vmReg, armReg := range capturedPinned {
					cg.spillPinnedRegNB(vmReg, armReg)
				}
				cg.asm.LoadImm64(X1, int64(pc))
				cg.asm.STR(X1, regCtx, ctxOffExitPC)
				cg.asm.LoadImm64(X0, 1)
				cg.asm.B("epilogue")
			})
			continue
		}

		if err := cg.emitInstruction(pc, inst); err != nil {
			return fmt.Errorf("pc %d: %w", pc, err)
		}

		// For ops with native fast path + call-exit fallback,
		// emit the resume label after the native code. The fast path
		// must skip the resume reload (which would corrupt pinned regs).
		if (op == vm.OP_GETFIELD || op == vm.OP_SETFIELD || op == vm.OP_GETTABLE || op == vm.OP_SETTABLE) && callExitSet[pc] {
			skipLabel := fmt.Sprintf("skip_resume_%d", pc)
			cg.asm.B(skipLabel)           // fast path: skip resume reload
			cg.asm.Label(resumeLabel(pc)) // call-exit resume entry
			cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
			cg.reloadPinnedRegs()
			cg.asm.Label(skipLabel)
		}
	}

	return nil
}

// emitInstruction dispatches a single bytecode instruction to the appropriate emitter.
func (cg *Codegen) emitInstruction(pc int, inst uint32) error {
	op := vm.DecodeOp(inst)

	switch op {
	case vm.OP_LOADNIL:
		return cg.emitLoadNil(inst)
	case vm.OP_LOADBOOL:
		return cg.emitLoadBool(pc, inst)
	case vm.OP_LOADINT:
		return cg.emitLoadInt(pc, inst)
	case vm.OP_LOADK:
		return cg.emitLoadK(inst)
	case vm.OP_MOVE:
		return cg.emitMove(inst)
	case vm.OP_ADD:
		return cg.emitArithInt(pc, inst, "ADD")
	case vm.OP_SUB:
		return cg.emitArithInt(pc, inst, "SUB")
	case vm.OP_MUL:
		return cg.emitArithInt(pc, inst, "MUL")
	case vm.OP_UNM:
		return cg.emitUNM(pc, inst)
	case vm.OP_NOT:
		return cg.emitNOT(pc, inst)
	case vm.OP_EQ:
		return cg.emitEQ(pc, inst)
	case vm.OP_LT:
		return cg.emitLT(pc, inst)
	case vm.OP_LE:
		return cg.emitLE(pc, inst)
	case vm.OP_JMP:
		return cg.emitJMP(pc, inst)
	case vm.OP_TEST:
		return cg.emitTest(pc, inst)
	case vm.OP_FORPREP:
		return cg.emitForPrep(pc, inst)
	case vm.OP_FORLOOP:
		return cg.emitForLoop(pc, inst)
	case vm.OP_GETFIELD:
		return cg.emitGetField(pc, inst)
	case vm.OP_SETFIELD:
		return cg.emitSetField(pc, inst)
	case vm.OP_GETTABLE:
		return cg.emitGetTable(pc, inst)
	case vm.OP_SETTABLE:
		return cg.emitSetTable(pc, inst)
	case vm.OP_RETURN:
		return cg.emitReturnOp(pc, inst)
	}
	return fmt.Errorf("unhandled opcode %s", vm.OpName(op))
}
