//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/vm"
)

// ──────────────────────────────────────────────────────────────────────────────
// Value access helpers (NaN-boxing: 8-byte values)
// ──────────────────────────────────────────────────────────────────────────────

// regValOffset returns the byte offset of R(i) from regRegs (each Value = 8 bytes).
func regValOffset(i int) int {
	return i * ValueSize
}

// loadRegTyp loads the NaN-boxed Value of R(reg) into dst, then extracts the
// tag into dst via LSR #48. After return, dst holds the top 16 bits of the value.
// Callers compare with NB_TagXxxShr48 constants.
// NOTE: the full value is lost; if you need both tag and value, load separately.
func (cg *Codegen) loadRegTyp(dst Reg, reg int) {
	off := regValOffset(reg)
	if off <= 32760 {
		cg.asm.LDR(dst, regRegs, off)
	} else {
		cg.asm.LoadImm64(X10, int64(off))
		cg.asm.ADDreg(X10, regRegs, X10)
		cg.asm.LDR(dst, X10, 0)
	}
	cg.asm.LSRimm(dst, dst, 48)
}

// storeRegTyp is a no-op placeholder for NaN-boxing.
// With NaN-boxing, the type is encoded in the value itself -- there is no
// separate type byte to store. Callers that used this must box values instead.
func (cg *Codegen) storeRegTyp(src Reg, reg int) {
	// No-op: NaN-boxing encodes the type in the value itself.
}

// loadRegIval loads the unboxed int from R(reg) into dst.
// For pinned registers, uses register-to-register MOV.
// For memory, loads the NaN-boxed value and sign-extends the 48-bit payload.
func (cg *Codegen) loadRegIval(dst Reg, reg int) {
	if armReg, ok := cg.pinnedRegs[reg]; ok {
		if dst != armReg {
			cg.asm.MOVreg(dst, armReg)
		}
		return
	}
	off := regValOffset(reg)
	cg.asm.LDR(dst, regRegs, off)
	EmitUnboxInt(cg.asm, dst, dst)
}

// storeRegIval stores a raw int64 as a NaN-boxed IntValue into R(reg).
// For pinned registers, uses register-to-register MOV (pinned regs hold raw ints).
func (cg *Codegen) storeRegIval(src Reg, reg int) {
	if armReg, ok := cg.pinnedRegs[reg]; ok {
		if src != armReg {
			cg.asm.MOVreg(armReg, src)
		}
		return
	}
	// Box the int and store the full NaN-boxed value
	EmitBoxIntFast(cg.asm, X10, src, regTagInt)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// loadRegFval loads R(reg) as a float64 into the ARM64 FP register dst.
// With NaN-boxing, the float IS the raw value bits, so just FLDRd.
func (cg *Codegen) loadRegFval(dst FReg, reg int) {
	off := regValOffset(reg)
	cg.asm.FLDRd(dst, regRegs, off)
}

// storeIntValue stores a complete NaN-boxed IntValue to R(reg).
// valReg holds the raw int64 value. This boxes it and writes.
// For pinned registers, only updates the ARM register (no memory write).
func (cg *Codegen) storeIntValue(reg int, valReg Reg) {
	if armReg, pinned := cg.pinnedRegs[reg]; pinned {
		if !cg.hasSelfCalls {
			// Pinned regs hold raw ints, no boxing needed
		}
		if valReg != armReg {
			cg.asm.MOVreg(armReg, valReg)
		}
		return
	}
	// Box and store
	EmitBoxIntFast(cg.asm, X10, valReg, regTagInt)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// storeNilValue stores NaN-boxed nil in R(reg).
func (cg *Codegen) storeNilValue(reg int) {
	EmitBoxNil(cg.asm, X10)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// storeBoolValue stores a NaN-boxed BoolValue to R(reg).
// valReg should contain 0 (false) or 1 (true).
func (cg *Codegen) storeBoolValue(reg int, valReg Reg) {
	EmitBoxBool(cg.asm, X10, valReg, X11)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// spillPinnedRegNB spills a pinned register as a NaN-boxed IntValue to memory.
// Used before side-exits and returns where the interpreter needs valid Values.
func (cg *Codegen) spillPinnedRegNB(vmReg int, armReg Reg) {
	EmitBoxIntFast(cg.asm, X10, armReg, regTagInt)
	off := regValOffset(vmReg)
	cg.asm.STR(X10, regRegs, off)
}

// emitCmpTag compares the tag value in dst (after LSR #48) with a NaN-boxing tag.
// Uses X10 as scratch for the tag constant.
func (cg *Codegen) emitCmpTag(dst Reg, tagShr48 uint16) {
	cg.asm.MOVimm16(X10, tagShr48)
	cg.asm.CMPreg(dst, X10)
}

// ──────────────────────────────────────────────────────────────────────────────
// RK value loading (register or constant) -- NaN-boxing
// ──────────────────────────────────────────────────────────────────────────────

// loadRKTyp loads the tag of RK(idx) into dst (via LSR #48).
// After return, dst holds the top 16 bits for comparison with NB_TagXxxShr48.
func (cg *Codegen) loadRKTyp(dst Reg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx * ValueSize
		if off <= 32760 {
			cg.asm.LDR(dst, regConsts, off)
		} else {
			cg.asm.LoadImm64(X10, int64(off))
			cg.asm.ADDreg(X10, regConsts, X10)
			cg.asm.LDR(dst, X10, 0)
		}
		cg.asm.LSRimm(dst, dst, 48)
	} else {
		cg.loadRegTyp(dst, idx)
	}
}

// loadRKIval loads the unboxed int from RK(idx) into dst.
// For small integer constants, emits a MOV immediate instead of a memory load.
func (cg *Codegen) loadRKIval(dst Reg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		// Optimize: if the constant is a small integer, use MOV immediate.
		if constIdx < len(cg.proto.Constants) && cg.proto.Constants[constIdx].IsInt() {
			v := cg.proto.Constants[constIdx].Int()
			cg.asm.LoadImm64(dst, v)
			return
		}
		// Load NaN-boxed value and unbox
		off := constIdx * ValueSize
		cg.asm.LDR(dst, regConsts, off)
		EmitUnboxInt(cg.asm, dst, dst)
	} else {
		cg.loadRegIval(dst, idx)
	}
}

// rkSmallIntConst returns the integer value if idx refers to an RK constant that
// is a non-negative integer fitting in 12 bits (0..4095). Returns -1 otherwise.
// Also checks for registers set by the immediately preceding LOADINT instruction,
// enabling immediate-form optimizations in self-call function bodies.
func (cg *Codegen) rkSmallIntConst(idx int) int64 {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		if constIdx >= len(cg.proto.Constants) {
			return -1
		}
		c := cg.proto.Constants[constIdx]
		if !c.IsInt() {
			return -1
		}
		v := c.Int()
		if v >= 0 && v <= 4095 {
			return v
		}
		return -1
	}
	return -1
}


// ──────────────────────────────────────────────────────────────────────────────
// Copy a full Value (8 bytes NaN-boxed) between registers.
// ──────────────────────────────────────────────────────────────────────────────

// copyValue copies the full NaN-boxed Value (8 bytes) from src to dst.
func (cg *Codegen) copyValue(dstReg, srcReg int) {
	srcOff := srcReg * ValueSize
	dstOff := dstReg * ValueSize
	a := cg.asm
	a.LDR(X0, regRegs, srcOff)
	a.STR(X0, regRegs, dstOff)
}

// copyRKValue copies a full NaN-boxed Value from RK(idx) to R(dst).
func (cg *Codegen) copyRKValue(dstReg, rkIdx int) {
	dstOff := dstReg * ValueSize
	a := cg.asm
	if vm.IsRK(rkIdx) {
		constIdx := vm.RKToConstIdx(rkIdx)
		srcOff := constIdx * ValueSize
		a.LDR(X0, regConsts, srcOff)
		a.STR(X0, regRegs, dstOff)
	} else {
		cg.copyValue(dstReg, rkIdx)
	}
}

// emitLoadNil emits code for the LOADNIL instruction.
func (cg *Codegen) emitLoadNil(inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	for i := aReg; i <= aReg+b; i++ {
		cg.storeNilValue(i)
	}
	return nil
}
