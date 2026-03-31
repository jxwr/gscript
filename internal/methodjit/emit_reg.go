//go:build darwin && arm64

// emit_reg.go implements register-resident value resolution for the Method JIT.
// This is the #1 performance optimization: values allocated to physical registers
// (X20-X23 via regalloc.go) stay in those registers, avoiding memory load/store
// on every instruction.
//
// Raw int mode: type-specialized int operations (OpAddInt, OpSubInt, etc.) keep
// values as raw int64 in registers, with NO NaN-boxing. A register-resident value
// is either NaN-boxed (for generic ops) or raw int (tracked by rawIntRegs).
//
// Raw int sources:
//   - OpConstInt with TypeInt: loads raw int64 directly (no boxing)
//   - OpLoadSlot with TypeInt: loads NaN-boxed from memory, unboxes immediately
//   - OpAddInt/OpSubInt/OpMulInt/OpModInt/OpNegInt: produce raw int results
//   - Loop header phis with TypeInt: delivered as raw ints by emitPhiMoveRawInt
//
// Transitions:
//   - Raw int -> NaN-boxed: resolveValueNB auto-boxes when a generic op needs it
//   - NaN-boxed -> raw int: resolveRawInt auto-unboxes when a specialized op needs it
//   - Cross-block raw ints: storeRawInt boxes to memory for other blocks to load
//
// Register convention:
//   X20-X23, X28: allocatable GPRs (callee-saved, hold raw int or NaN-boxed)
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
// in a GPR. If the value has an allocated register holding a NaN-boxed value,
// returns that register directly. If the register holds a raw int (from
// type-specialized ops), boxes it into scratchReg first. Otherwise, loads from
// memory into scratchReg and returns scratchReg.
//
// The returned register holds a NaN-BOXED value.
func (ec *emitContext) resolveValueNB(valueID int, scratchReg jit.Reg) jit.Reg {
	if ec.hasReg(valueID) {
		if ec.rawIntRegs[valueID] {
			// Raw int in register: box into scratch before returning.
			reg := ec.physReg(valueID)
			jit.EmitBoxIntFast(ec.asm, scratchReg, reg, mRegTagInt)
			return scratchReg
		}
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

// emitLoadSlotToReg emits code to load a VM register slot's value into the
// value's allocated physical register. For TypeInt values, unboxes the NaN-boxed
// int to a raw int64 and marks the register as raw. For other types, loads
// the NaN-boxed value directly.
func (ec *emitContext) emitLoadSlotToReg(instr *Instr) {
	pr, ok := ec.alloc.ValueRegs[instr.ID]
	if !ok || pr.IsFloat {
		return
	}
	reg := jit.Reg(pr.Reg)
	slot := int(instr.Aux)
	ec.asm.LDR(reg, mRegRegs, slotOffset(slot))
	if instr.Type == TypeInt {
		// Unbox NaN-boxed int to raw int64 at load time.
		// This avoids unboxing at every use site.
		jit.EmitUnboxInt(ec.asm, reg, reg)
		ec.activeRegs[instr.ID] = true
		ec.rawIntRegs[instr.ID] = true
	} else {
		ec.activeRegs[instr.ID] = true
	}
}
