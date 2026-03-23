//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/vm"
)

// regLoadIntConst checks if a VM register holds a known small constant set by
// a LOADINT instruction. Scans backward from currentPC (up to 3 instructions)
// looking for a LOADINT that set the register, verifying no intervening write.
// Returns the constant value (0..4095) or -1 if not found.
func (cg *Codegen) regLoadIntConst(reg, currentPC int) int64 {
	if !cg.hasSelfCalls {
		return -1
	}
	code := cg.proto.Code
	for scanPC := currentPC - 1; scanPC >= 0 && scanPC >= currentPC-3; scanPC-- {
		scanInst := code[scanPC]
		scanOp := vm.DecodeOp(scanInst)
		scanA := vm.DecodeA(scanInst)

		// Check if this is the LOADINT that set our register.
		if scanOp == vm.OP_LOADINT && scanA == reg {
			v := int64(vm.DecodesBx(scanInst))
			if v >= 0 && v <= 4095 {
				return v
			}
			return -1
		}
		// If any intervening instruction writes to our register, give up.
		if scanA == reg {
			switch scanOp {
			case vm.OP_MOVE, vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD,
				vm.OP_UNM, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_GETGLOBAL,
				vm.OP_GETTABLE, vm.OP_GETFIELD, vm.OP_GETUPVAL:
				return -1
			}
		}
		// Skip instructions that are part of inline patterns (not emitted).
		if cg.inlineSkipPCs[scanPC] || cg.inlineArgSkipPCs[scanPC] {
			continue
		}
	}
	return -1
}

func (cg *Codegen) emitLoadBool(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	if b != 0 {
		cg.asm.LoadImm64(X0, 1)
	} else {
		cg.asm.LoadImm64(X0, 0)
	}
	cg.storeBoolValue(aReg, X0)

	if c != 0 {
		// Skip next instruction: jump to pc+2.
		cg.asm.B(pcLabel(pc + 2))
	}
	return nil
}

func (cg *Codegen) emitLoadInt(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	// For self-call functions: skip the Value array write if the constant will
	// be consumed as an immediate by the next instruction (LT, LE, SUB, ADD).
	// The constant value is still available via regLoadIntConst for the consumer.
	if cg.hasSelfCalls && sbx >= 0 && sbx <= 4095 {
		if cg.isLoadIntDeadStore(pc, aReg) {
			return nil
		}
	}

	cg.asm.LoadImm64(X0, int64(sbx))
	cg.storeIntValue(aReg, X0)
	return nil
}

// isLoadIntDeadStore checks if a LOADINT at pc is a dead store whose value
// will only be consumed via immediate form by subsequent instructions.
// Returns true if the LOADINT's Value array write can be safely elided.
func (cg *Codegen) isLoadIntDeadStore(pc, reg int) bool {
	code := cg.proto.Code
	// Scan forward to find all uses of this register before it's overwritten.
	for scanPC := pc + 1; scanPC < len(code); scanPC++ {
		scanInst := code[scanPC]
		scanOp := vm.DecodeOp(scanInst)
		scanA := vm.DecodeA(scanInst)

		// If the register is overwritten (destination = our reg), the store is dead.
		switch scanOp {
		case vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL,
			vm.OP_MOVE, vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD,
			vm.OP_UNM, vm.OP_GETGLOBAL, vm.OP_GETTABLE, vm.OP_GETFIELD, vm.OP_GETUPVAL:
			if scanA == reg {
				return true // register overwritten before any memory read
			}
		case vm.OP_CALL:
			// Self-call writes result to fnReg (=A), check if it overwrites our reg.
			if scanA == reg {
				return true
			}
		}

		// If the register is read as a source operand, check if the consumer
		// will use the immediate form (regLoadIntConst). If not, we need the store.
		switch scanOp {
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			b := vm.DecodeB(scanInst)
			c := vm.DecodeC(scanInst)
			if b == reg || c == reg {
				// The EQ/LT/LE emitter will detect this via regLoadIntConst and use CMPimm.
				// Safe to skip the LOADINT store.
				return true
			}
		case vm.OP_ADD, vm.OP_SUB:
			b := vm.DecodeB(scanInst)
			c := vm.DecodeC(scanInst)
			if b == reg || c == reg {
				// The arithmetic emitter will detect this via regLoadIntConst and use ADDimm/SUBimm.
				return true
			}
		case vm.OP_MOVE:
			b := vm.DecodeB(scanInst)
			if b == reg {
				return false // MOVE reads from Value array — need the store
			}
		case vm.OP_RETURN:
			// RETURN A B reads R(A)..R(A+B-2)
			retA := vm.DecodeA(scanInst)
			retB := vm.DecodeB(scanInst)
			if retB > 0 && reg >= retA && reg < retA+retB-1 {
				return false // returned value — need the store
			}
			if retB == 0 && reg >= retA {
				return false // variable return — need the store
			}
		case vm.OP_JMP:
			// Branch target could loop back and read the register — be conservative.
			return false
		case vm.OP_FORLOOP, vm.OP_FORPREP:
			return false // loop instructions — be conservative
		}

		// Skip instructions that are part of inline patterns.
		if cg.inlineSkipPCs[scanPC] {
			continue
		}
	}
	// Reached end of function without finding a reader or overwriter — store is dead.
	return true
}

func (cg *Codegen) emitLoadK(inst uint32) error {
	aReg := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)
	cg.copyRKValue(aReg, vm.ConstToRK(bx))
	return nil
}

func (cg *Codegen) emitMove(inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	srcArm, srcPinned := cg.pinnedRegs[bReg]
	dstArm, dstPinned := cg.pinnedRegs[aReg]

	if srcPinned && dstPinned {
		// Both pinned: register-to-register move.
		if srcArm != dstArm {
			cg.asm.MOVreg(dstArm, srcArm)
		}
	} else if srcPinned {
		// Source pinned, dest in memory: write int value.
		// In hasSelfCalls mode, type tags are known TypeInt and don't need updating.
		// We keep the alignment-equivalent code (NOPs) to avoid branch target shifts
		// that cause performance regression on Apple Silicon.
		if cg.hasSelfCalls {
			cg.asm.NOP()
			cg.asm.NOP()
		}
		// Box the raw int and store as NaN-boxed Value
		EmitBoxIntFast(cg.asm, X10, srcArm, regTagInt)
		cg.asm.STR(X10, regRegs, regValOffset(aReg))
	} else if dstPinned {
		// Dest pinned, source in memory: load NaN-boxed value and unbox int.
		cg.asm.LDR(dstArm, regRegs, regValOffset(bReg))
		EmitUnboxInt(cg.asm, dstArm, dstArm)
	} else {
		cg.copyValue(aReg, bReg)
	}
	return nil
}

func (cg *Codegen) emitReturnOp(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	// For self-call functions, pinned registers (R(0)→X19, optionally R(1)→X22)
	// don't need to be in the Value array for nested returns (the caller restores
	// them from the ARM64 stack). The outermost return in emitSelfCallReturn
	// handles writing type tags for the return register explicitly.
	// Skip spillPinnedRegs to eliminate wasted instructions per nested return.
	if cg.hasSelfCalls {
		return cg.emitSelfCallReturn(pc, aReg, b)
	}

	// Spill pinned registers before returning (return values must be in memory).
	if len(cg.pinnedRegs) > 0 {
		cg.spillPinnedRegs()
	}

	if b == 0 {
		// Return R(A) to top — side exit since we don't track 'top'.
		cg.asm.LoadImm64(X1, int64(-1)) // signal variable return
		cg.asm.STR(X1, regCtx, ctxOffRetBase)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
		return nil
	}
	if b == 1 {
		// Return nothing.
		cg.asm.LoadImm64(X1, int64(aReg))
		cg.asm.STR(X1, regCtx, ctxOffRetBase)
		cg.asm.LoadImm64(X1, 0)
		cg.asm.STR(X1, regCtx, ctxOffRetCount)
		cg.asm.LoadImm64(X0, 0)
		cg.asm.B("epilogue")
		return nil
	}

	nret := b - 1
	cg.asm.LoadImm64(X1, int64(aReg))
	cg.asm.STR(X1, regCtx, ctxOffRetBase)
	cg.asm.LoadImm64(X1, int64(nret))
	cg.asm.STR(X1, regCtx, ctxOffRetCount)
	cg.asm.LoadImm64(X0, 0)
	cg.asm.B("epilogue")
	return nil
}
