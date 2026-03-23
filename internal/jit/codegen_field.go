//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// emitGetField compiles OP_GETFIELD R(A) = R(B).Constants[C] natively.
// Fast path: R(B) is TypeTable, no metatable, key found in flat skeys.
// Slow path: falls through to call-exit for non-table, metatable, or smap cases.
func (cg *Codegen) emitGetField(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	asm := cg.asm

	// Label for call-exit fallback
	fallbackLabel := fmt.Sprintf("getfield_fallback_%d", pc)


	// --- Step 1: Type check R(B) is a Table (NaN-boxed pointer with ptrSubTable) ---
	bOff := b * ValueSize
	if bOff <= 32760 {
		asm.LDR(X0, regRegs, bOff) // X0 = NaN-boxed Value
	} else {
		asm.LoadImm64(X1, int64(bOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, fallbackLabel)

	// --- Step 2: Extract *Table pointer from NaN-boxed value ---
	EmitExtractPtr(asm, X0, X0) // X0 = *Table (44-bit address)
	asm.CBZ(X0, fallbackLabel) // nil table check

	// --- Step 3: Check metatable == nil ---
	asm.LDR(X1, X0, TableOffMetatable) // X1 = table.metatable
	asm.CBNZ(X1, fallbackLabel)        // has metatable → fallback

	// --- Step 4: Load skeys slice (ptr, len) ---
	asm.LDR(X1, X0, TableOffSkeys)    // X1 = skeys base pointer
	asm.LDR(X2, X0, TableOffSkeysLen) // X2 = skeys.len
	asm.CBZ(X2, fallbackLabel)         // no skeys → fallback (might be in smap)

	// Save table pointer for later svals access
	asm.MOVreg(X9, X0) // X9 = *Table (preserved)

	// --- Step 5: Load constant key string (NaN-boxed string pointer) ---
	// Constants[C] is a NaN-boxed StringValue. Extract the pointer, then read the string header.
	cOff := c * ValueSize
	if cOff <= 32760 {
		asm.LDR(X3, regConsts, cOff) // X3 = NaN-boxed string value
	} else {
		asm.LoadImm64(X4, int64(cOff))
		asm.ADDreg(X4, regConsts, X4)
		asm.LDR(X3, X4, 0)
	}
	EmitExtractPtr(asm, X3, X3) // X3 = pointer to string header (*string)
	asm.LDR(X4, X3, 0) // X4 = key string data ptr
	asm.LDR(X5, X3, 8) // X5 = key string len

	// --- Step 6: Linear scan of skeys ---
	loopLabel := fmt.Sprintf("getfield_scan_%d", pc)
	nextLabel := fmt.Sprintf("getfield_next_%d", pc)
	foundLabel := fmt.Sprintf("getfield_found_%d", pc)
	cmpLoopLabel := fmt.Sprintf("getfield_cmp_%d", pc)

	asm.LoadImm64(X6, 0) // X6 = i = 0

	asm.Label(loopLabel)
	asm.CMPreg(X6, X2) // i >= skeys.len?
	asm.BCond(CondGE, fallbackLabel)

	// Load skeys[i]: string at X1 + i*16
	asm.LSLimm(X7, X6, 4)   // X7 = i * 16
	asm.ADDreg(X7, X1, X7)  // X7 = &skeys[i]
	asm.LDR(X10, X7, 0)     // X10 = skeys[i].ptr
	asm.LDR(X11, X7, 8)     // X11 = skeys[i].len

	// Compare lengths first (fast reject)
	asm.CMPreg(X11, X5) // skeys[i].len == key.len?
	asm.BCond(CondNE, nextLabel)

	// Compare data pointers (fast accept for interned strings)
	asm.CMPreg(X10, X4) // same pointer?
	asm.BCond(CondEQ, foundLabel)

	// Byte-by-byte comparison for non-interned strings
	asm.LoadImm64(X12, 0) // j = 0
	asm.Label(cmpLoopLabel)
	asm.CMPreg(X12, X5) // j >= len?
	asm.BCond(CondGE, foundLabel)
	asm.LDRBreg(X13, X10, X12) // skeys[i].ptr[j]
	asm.LDRBreg(X14, X4, X12)  // key.ptr[j]
	asm.CMPreg(X13, X14)
	asm.BCond(CondNE, nextLabel)
	asm.ADDimm(X12, X12, 1)
	asm.B(cmpLoopLabel)

	asm.Label(nextLabel)
	asm.ADDimm(X6, X6, 1) // i++
	asm.B(loopLabel)

	// --- Step 7: Found - load svals[i] into R(A) ---
	asm.Label(foundLabel)
	// svals base is at Table + TableOffSvals
	asm.LDR(X7, X9, TableOffSvals) // X7 = svals base pointer
	// svals[i] is at X7 + i * ValueSize
	EmitMulValueSize(asm, X8, X6, X5) // X8 = i * ValueSize
	asm.ADDreg(X7, X7, X8)            // X7 = &svals[i]

	// Copy Value (ValueSize bytes) from svals[i] to R(A)
	aOff := a * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, X7, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	// Fallback deferred to cold section.
	capturedPinnedGF := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedGF[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedGF {
			cg.spillPinnedRegNB(vmReg, armReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit)
		cg.asm.B("epilogue")
	})

	return nil
}

// emitSetField compiles OP_SETFIELD R(A)[Constants[B]] = RK(C) natively.
// Fast path: R(A) is TypeTable, no metatable, key found in flat skeys.
// Slow path: falls through to call-exit for non-table, metatable, or smap cases.
func (cg *Codegen) emitSetField(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst) // constant index for field name
	cidx := vm.DecodeC(inst) // RK(C) = value to write
	asm := cg.asm

	// Label for call-exit fallback
	fallbackLabel := fmt.Sprintf("setfield_fallback_%d", pc)


	// --- Step 1: Type check R(A) is a Table (NaN-boxed) ---
	aOff := a * ValueSize
	if aOff <= 32760 {
		asm.LDR(X0, regRegs, aOff)
	} else {
		asm.LoadImm64(X1, int64(aOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, fallbackLabel)

	// --- Step 2: Extract *Table pointer from NaN-boxed value ---
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, fallbackLabel) // nil table check

	// --- Step 3: Check metatable == nil (has __newindex → fallback) ---
	asm.LDR(X1, X0, TableOffMetatable) // X1 = table.metatable
	asm.CBNZ(X1, fallbackLabel)        // has metatable → fallback

	// --- Step 4: Load skeys slice (ptr, len) ---
	asm.LDR(X1, X0, TableOffSkeys)    // X1 = skeys base pointer
	asm.LDR(X2, X0, TableOffSkeysLen) // X2 = skeys.len
	asm.CBZ(X2, fallbackLabel)         // no skeys → fallback (might be in smap)

	// Save table pointer for later svals access
	asm.MOVreg(X9, X0) // X9 = *Table (preserved)

	// --- Step 5: Load constant key string (NaN-boxed string pointer) ---
	bOff := b * ValueSize
	if bOff <= 32760 {
		asm.LDR(X3, regConsts, bOff) // X3 = NaN-boxed string value
	} else {
		asm.LoadImm64(X4, int64(bOff))
		asm.ADDreg(X4, regConsts, X4)
		asm.LDR(X3, X4, 0)
	}
	EmitExtractPtr(asm, X3, X3) // X3 = pointer to *string
	asm.LDR(X4, X3, 0) // X4 = key string data ptr
	asm.LDR(X5, X3, 8) // X5 = key string len

	// --- Step 6: Linear scan of skeys to find matching field ---
	loopLabel := fmt.Sprintf("setfield_scan_%d", pc)
	nextLabel := fmt.Sprintf("setfield_next_%d", pc)
	foundLabel := fmt.Sprintf("setfield_found_%d", pc)
	cmpLoopLabel := fmt.Sprintf("setfield_cmp_%d", pc)

	asm.LoadImm64(X6, 0) // X6 = i = 0

	asm.Label(loopLabel)
	asm.CMPreg(X6, X2) // i >= skeys.len?
	asm.BCond(CondGE, fallbackLabel)

	// Load skeys[i]: string at X1 + i*16
	asm.LSLimm(X7, X6, 4)   // X7 = i * 16
	asm.ADDreg(X7, X1, X7)  // X7 = &skeys[i]
	asm.LDR(X10, X7, 0)     // X10 = skeys[i].ptr
	asm.LDR(X11, X7, 8)     // X11 = skeys[i].len

	// Compare lengths first (fast reject)
	asm.CMPreg(X11, X5) // skeys[i].len == key.len?
	asm.BCond(CondNE, nextLabel)

	// Compare data pointers (fast accept for interned strings)
	asm.CMPreg(X10, X4) // same pointer?
	asm.BCond(CondEQ, foundLabel)

	// Byte-by-byte comparison for non-interned strings
	asm.LoadImm64(X12, 0) // j = 0
	asm.Label(cmpLoopLabel)
	asm.CMPreg(X12, X5) // j >= len?
	asm.BCond(CondGE, foundLabel)
	asm.LDRBreg(X13, X10, X12) // skeys[i].ptr[j]
	asm.LDRBreg(X14, X4, X12)  // key.ptr[j]
	asm.CMPreg(X13, X14)
	asm.BCond(CondNE, nextLabel)
	asm.ADDimm(X12, X12, 1)
	asm.B(cmpLoopLabel)

	asm.Label(nextLabel)
	asm.ADDimm(X6, X6, 1) // i++
	asm.B(loopLabel)

	// --- Step 7: Found - write RK(C) value to svals[i] ---
	asm.Label(foundLabel)
	// svals base is at Table + TableOffSvals
	asm.LDR(X7, X9, TableOffSvals) // X7 = svals base pointer
	// svals[i] is at X7 + i * ValueSize
	EmitMulValueSize(asm, X8, X6, X5) // X8 = i * ValueSize
	asm.ADDreg(X7, X7, X8)            // X7 = &svals[i]

	// Copy Value (ValueSize bytes) from RK(C) to svals[i]
	if cidx >= vm.RKBit {
		// Value comes from constants
		constIdx := vm.RKToConstIdx(cidx)
		valOff := constIdx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regConsts, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regConsts, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X7, w*8)
		}
	} else {
		// Value comes from register
		valOff := cidx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regRegs, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regRegs, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X7, w*8)
		}
	}
	// Fallback deferred to cold section.
	capturedPinnedSF := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedSF[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedSF {
			cg.spillPinnedRegNB(vmReg, armReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit)
		cg.asm.B("epilogue")
	})

	return nil
}
