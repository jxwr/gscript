//go:build darwin && arm64

// emit_reg.go implements register-resident value resolution for the Method JIT.
// This is the #1 performance optimization: values allocated to physical registers
// (X20-X23 via regalloc.go) stay in those registers, avoiding memory load/store
// on every instruction.
//
// Key principle: values in allocated GPRs are NaN-boxed (same representation as
// in the VM register file). This keeps type dispatch working for generic ops
// (OpAdd, OpDiv) while eliminating memory traffic.
//
// For type-specialized int ops (OpAddInt, OpSubInt, etc.), the emit path unboxes
// from the register (1 SBFX instruction) instead of loading from memory (1 LDR),
// then reboxes the result back into the destination register (2 instructions:
// UBFX + ORR) instead of storing to memory (1 STR). The net win is eliminating
// memory latency on every operation.
//
// Register convention:
//   X20-X23, X28: allocatable GPRs (callee-saved, hold NaN-boxed values)
//   X0-X3:   scratch registers for values without allocation
//   X19:     ExecContext pointer (pinned)
//   X24:     NaN-boxing int tag (pinned)
//   X25:     NaN-boxing bool tag (pinned)
//   X26:     VM register base (pinned)
//   X27:     constants pointer (pinned)

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
)

// computeCrossBlockLive returns a set of value IDs that are used in a different
// block from where they're defined. These values need write-through to memory
// so they can be loaded by other blocks. Values used only within their defining
// block don't need memory writes.
func computeCrossBlockLive(fn *Function) map[int]bool {
	// First, find which block each value is defined in.
	defBlock := make(map[int]int) // valueID -> blockID
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defBlock[instr.ID] = block.ID
			}
		}
	}

	// Find values used in a different block than their definition.
	crossBlock := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				db, ok := defBlock[arg.ID]
				if ok && db != block.ID {
					crossBlock[arg.ID] = true
				}
			}
		}
	}

	// Also mark phi sources as cross-block (they're read from predecessor blocks).
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				break
			}
			for _, arg := range instr.Args {
				crossBlock[arg.ID] = true
			}
		}
	}

	return crossBlock
}

// hasReg returns true if the given value ID has a physical GPR register allocation
// AND the register is currently active (the value was defined in the current block
// or loaded via phi resolution). Values from predecessor blocks that weren't
// carried forward via phi moves must be loaded from memory.
func (ec *emitContext) hasReg(valueID int) bool {
	pr, ok := ec.alloc.ValueRegs[valueID]
	if !ok || pr.IsFloat {
		return false
	}
	// Check if this value's register is active in the current block.
	return ec.activeRegs[valueID]
}

// physReg returns the jit.Reg for a value's physical register allocation.
// Only call if hasReg returns true.
func (ec *emitContext) physReg(valueID int) jit.Reg {
	pr := ec.alloc.ValueRegs[valueID]
	return jit.Reg(pr.Reg)
}

// resolveValueNB ensures the NaN-boxed value identified by valueID is available
// in a GPR. If the value has an allocated register, returns that register directly
// (the register holds the NaN-boxed value). Otherwise, loads from memory into
// scratchReg and returns scratchReg.
//
// The returned register holds a NaN-BOXED value.
func (ec *emitContext) resolveValueNB(valueID int, scratchReg jit.Reg) jit.Reg {
	if ec.hasReg(valueID) {
		return ec.physReg(valueID)
	}
	// Fallback: load NaN-boxed from memory slot
	ec.loadValue(scratchReg, valueID)
	return scratchReg
}

// resolveValueUnboxedInt ensures the value identified by valueID is available
// as a raw (unboxed) int64 in a GPR. If the value has an allocated register,
// unboxes into scratchReg. Otherwise, loads from memory and unboxes into scratchReg.
//
// The returned register holds a RAW int64 (not NaN-boxed).
// Always returns scratchReg (caller can use it freely).
func (ec *emitContext) resolveValueUnboxedInt(valueID int, scratchReg jit.Reg) jit.Reg {
	src := ec.resolveValueNB(valueID, scratchReg)
	jit.EmitUnboxInt(ec.asm, scratchReg, src)
	return scratchReg
}

// storeResultNB stores a NaN-boxed result. If the value has a register allocation,
// stores to the register. If the value is also used in other blocks (cross-block
// live), writes through to memory too. For block-local values, the memory write
// is skipped entirely -- this is the key optimization for inner loops.
func (ec *emitContext) storeResultNB(srcReg jit.Reg, valueID int) {
	pr, ok := ec.alloc.ValueRegs[valueID]
	if ok && !pr.IsFloat {
		// Invalidate any other value that was previously in this register.
		ec.invalidateReg(pr.Reg, valueID)
		// Store to allocated register and activate it.
		ec.activeRegs[valueID] = true
		dstReg := jit.Reg(pr.Reg)
		if srcReg != dstReg {
			ec.asm.MOVreg(dstReg, srcReg)
		}
		// Only write-through to memory if the value is used cross-block.
		if ec.crossBlockLive[valueID] {
			ec.storeValue(dstReg, valueID)
		}
		return
	}
	// No register allocation: store to memory only.
	ec.storeValue(srcReg, valueID)
}

// resolveRawInt returns a GPR holding the raw (unboxed) int64 for a value.
// If the value has a register with raw int content (from a prior emitRawIntBinOp),
// returns that register directly — zero instructions emitted.
// Otherwise unboxes from NaN-boxed register or loads from memory.
func (ec *emitContext) resolveRawInt(valueID int, scratch jit.Reg) jit.Reg {
	// If the value is in a register AND was produced by a raw-int operation,
	// the register already holds a raw int — return it directly.
	if ec.hasReg(valueID) && ec.rawIntRegs[valueID] {
		return ec.physReg(valueID)
	}
	// Otherwise unbox from NaN-boxed source.
	return ec.resolveValueUnboxedInt(valueID, scratch)
}

// storeRawInt stores a raw int64 result to the allocated register (if any)
// and marks it as containing a raw int (not NaN-boxed).
// For cross-block values, writes NaN-boxed to memory.
// For values that are ONLY used as phi args to loop headers, the write-through
// is skipped since emitPhiMoveRawInt reads from the register directly.
func (ec *emitContext) storeRawInt(srcReg jit.Reg, valueID int) {
	pr, ok := ec.alloc.ValueRegs[valueID]
	if ok && !pr.IsFloat {
		// Invalidate any other value that was previously in this register.
		ec.invalidateReg(pr.Reg, valueID)
		ec.activeRegs[valueID] = true
		ec.rawIntRegs[valueID] = true
		dstReg := jit.Reg(pr.Reg)
		if srcReg != dstReg {
			ec.asm.MOVreg(dstReg, srcReg)
		}
		// Cross-block: write NaN-boxed to memory (box then store).
		// Skip if the value is only used as a phi arg to a loop header
		// (the phi move reads from the register via emitPhiMoveRawInt).
		if ec.crossBlockLive[valueID] && !ec.loopPhiOnlyArgs[valueID] {
			jit.EmitBoxIntFast(ec.asm, jit.X0, dstReg, mRegTagInt)
			ec.storeValue(jit.X0, valueID)
		}
		return
	}
	// No register: box and store to memory
	jit.EmitBoxIntFast(ec.asm, jit.X0, srcReg, mRegTagInt)
	ec.storeValue(jit.X0, valueID)
}

// inLoopBlock returns true if the current block being emitted is inside a loop.
func (ec *emitContext) inLoopBlock() bool {
	return ec.loop != nil && ec.loop.loopBlocks[ec.currentBlockID]
}

// invalidateReg removes any other value that was previously active in the
// given register (reg is the register number, not jit.Reg). This is needed
// when a register is reused for a new value, as the old value's register
// content is now stale. Without this, loop-carried values that share a register
// with a later-defined value would have stale activeRegs/rawIntRegs entries.
func (ec *emitContext) invalidateReg(reg int, newValueID int) {
	for valID := range ec.activeRegs {
		if valID == newValueID {
			continue
		}
		if pr, ok := ec.alloc.ValueRegs[valID]; ok && pr.Reg == reg && !pr.IsFloat {
			delete(ec.activeRegs, valID)
			delete(ec.rawIntRegs, valID)
		}
	}
}

// constIntImm12 checks if a value ID refers to a ConstInt whose value fits
// in a 12-bit unsigned immediate (0-4095). Returns the value and true if so.
// Used to emit ADDimm/SUBimm instead of register-based forms.
func (ec *emitContext) constIntImm12(valueID int) (uint16, bool) {
	v, ok := ec.constInts[valueID]
	if !ok {
		return 0, false
	}
	if v >= 0 && v <= 4095 {
		return uint16(v), true
	}
	return 0, false
}

// emitLoadSlotToReg emits code to load a VM register slot's NaN-boxed value
// into the value's allocated physical register. Marks the value as active.
func (ec *emitContext) emitLoadSlotToReg(instr *Instr) {
	pr, ok := ec.alloc.ValueRegs[instr.ID]
	if !ok || pr.IsFloat {
		return
	}
	reg := jit.Reg(pr.Reg)
	slot := int(instr.Aux)
	ec.asm.LDR(reg, mRegRegs, slotOffset(slot))
	ec.activeRegs[instr.ID] = true
}
