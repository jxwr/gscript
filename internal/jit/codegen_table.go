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

// emitGetTable compiles OP_GETTABLE R(A) = R(B)[RK(C)] natively.
// Fast path: R(B) is TypeTable, no metatable, RK(C) is TypeInt, key in array range.
// Slow path: call-exit for non-table, metatable, non-int keys, or imap.
func (cg *Codegen) emitGetTable(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)
	asm := cg.asm

	fallbackLabel := fmt.Sprintf("gettable_fallback_%d", pc)


	// --- Step 1: Type check R(B) is a Table (NaN-boxing) ---
	bOff := b * ValueSize
	if bOff <= 32760 {
		asm.LDR(X0, regRegs, bOff)
	} else {
		asm.LoadImm64(X1, int64(bOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, fallbackLabel)

	// --- Step 2: Extract *Table pointer from NaN-boxed value ---
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, fallbackLabel)

	// --- Step 3: Check metatable == nil ---
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// --- Step 4: Load key from RK(C), check TypeInt (NaN-boxing) ---
	var keyOff int
	var keyBase Reg
	if cidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(cidx)
		keyOff = constIdx * ValueSize
		keyBase = regConsts
	} else {
		keyOff = cidx * ValueSize
		keyBase = regRegs
	}
	if keyOff <= 32760 {
		asm.LDR(X2, keyBase, keyOff) // X2 = full NaN-boxed key
	} else {
		asm.LoadImm64(X3, int64(keyOff))
		asm.ADDreg(X3, keyBase, X3)
		asm.LDR(X2, X3, 0)
	}
	asm.LSRimm(X3, X2, 48)
	asm.MOVimm16(X4, NB_TagIntShr48)
	asm.CMPreg(X3, X4)
	asm.BCond(CondNE, fallbackLabel)
	EmitUnboxInt(asm, X2, X2) // X2 = key int value

	// --- Step 5: Array bounds check ---
	// Check: key >= 0 (0-indexed array)
	asm.CMPimm(X2, 0) // key >= 0?
	asm.BCond(CondLT, fallbackLabel)

	// Check arrayKind for type-specialized paths
	boolArrayLabel := fmt.Sprintf("gettable_bool_%d", pc)
	intArrayLabel := fmt.Sprintf("gettable_int_%d", pc)
	floatArrayLabel := fmt.Sprintf("gettable_float_%d", pc)
	doneGetLabel := fmt.Sprintf("gettable_done_%d", pc)
	asm.LDRB(X6, X0, TableOffArrayKind)
	asm.CMPimmW(X6, AKBool)
	asm.BCond(CondEQ, boolArrayLabel)
	asm.CMPimmW(X6, AKInt)
	asm.BCond(CondEQ, intArrayLabel)
	asm.CMPimmW(X6, AKFloat)
	asm.BCond(CondEQ, floatArrayLabel)

	// For unknown typed arrays, fall back
	asm.CBNZ(X6, fallbackLabel)

	// --- ArrayMixed path: check key < array.len ---
	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key < array.len?
	asm.BCond(CondGE, fallbackLabel)

	// Load array[key] (Value at array.ptr + key * ValueSize)
	asm.LDR(X3, X0, TableOffArray) // X3 = array.ptr
	EmitMulValueSize(asm, X4, X2, X5) // X4 = key * ValueSize
	asm.ADDreg(X3, X3, X4)            // X3 = &array[key]

	aOff := a * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, X3, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	asm.B(doneGetLabel)

	// --- ArrayInt path: LDR from intArray, store as NaN-boxed IntValue ---
	asm.Label(intArrayLabel)
	asm.LDR(X3, X0, TableOffIntArray+8) // X3 = intArray.len
	asm.CMPreg(X2, X3)                  // key < intArray.len?
	asm.BCond(CondGE, fallbackLabel)
	asm.LDR(X3, X0, TableOffIntArray)   // X3 = intArray.ptr
	asm.LSLimm(X4, X2, 3)               // X4 = key * 8 (byte offset)
	asm.LDRreg(X4, X3, X4)              // X4 = *(ptr + key*8) = intArray[key]
	EmitBoxIntFast(asm, X5, X4, regTagInt)   // X5 = NaN-boxed int
	asm.STR(X5, regRegs, a*ValueSize)
	asm.B(doneGetLabel)

	// --- ArrayFloat path: LDR from floatArray, store as NaN-boxed FloatValue ---
	asm.Label(floatArrayLabel)
	asm.LDR(X3, X0, TableOffFloatArray+8) // X3 = floatArray.len
	asm.CMPreg(X2, X3)                    // key < floatArray.len?
	asm.BCond(CondGE, fallbackLabel)
	asm.LDR(X3, X0, TableOffFloatArray)   // X3 = floatArray.ptr
	asm.LSLimm(X4, X2, 3)                 // X4 = key * 8 (byte offset)
	asm.LDRreg(X4, X3, X4)                // X4 = raw float64 bits = NaN-boxed float
	asm.STR(X4, regRegs, a*ValueSize)      // floats ARE their NaN-boxed form
	asm.B(doneGetLabel)

	// --- ArrayBool path: LDRB from boolArray, sentinel decode ---
	asm.Label(boolArrayLabel)
	asm.LDR(X3, X0, TableOffBoolArray+8) // X3 = boolArray.len
	asm.CMPreg(X2, X3)                   // key < boolArray.len?
	asm.BCond(CondGE, fallbackLabel)
	asm.LDR(X3, X0, TableOffBoolArray)   // X3 = boolArray.ptr
	asm.LDRBreg(X4, X3, X2)             // X4 = boolArray[key] (byte)
	// Sentinel: 0=nil, 1=false, 2=true
	asm.CMPimm(X4, 0)
	asm.BCond(CondEQ, fallbackLabel) // nil sentinel → fallback (might need metamethods)
	asm.SUBimm(X4, X4, 1)           // 1→0 (false), 2→1 (true)
	// Store as NaN-boxed BoolValue
	EmitBoxBool(asm, X5, X4, X6)
	asm.STR(X5, regRegs, a*ValueSize)
	asm.Label(doneGetLabel)
	// Fallback deferred to cold section.
	capturedPinnedGT := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedGT[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedGT {
			cg.spillPinnedRegNB(vmReg, armReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2)
		cg.asm.B("epilogue")
	})

	return nil
}

// emitSetTable compiles OP_SETTABLE R(A)[RK(B)] = RK(C) natively.
// Fast path: R(A) is TypeTable, no metatable, RK(B) is TypeInt, key in array range.
// Slow path: call-exit for non-table, metatable, non-int keys, or out-of-bounds.
func (cg *Codegen) emitSetTable(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)
	asm := cg.asm

	fallbackLabel := fmt.Sprintf("settable_fallback_%d", pc)


	// --- Step 1: Type check R(A) is a Table (NaN-boxing) ---
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
	asm.CBZ(X0, fallbackLabel)

	// --- Step 3: Check metatable == nil ---
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// --- Step 4: Load key from RK(B), check TypeInt (NaN-boxing) ---
	var keyOff int
	var keyBase Reg
	if bidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(bidx)
		keyOff = constIdx * ValueSize
		keyBase = regConsts
	} else {
		keyOff = bidx * ValueSize
		keyBase = regRegs
	}
	if keyOff <= 32760 {
		asm.LDR(X2, keyBase, keyOff) // X2 = full NaN-boxed key
	} else {
		asm.LoadImm64(X3, int64(keyOff))
		asm.ADDreg(X3, keyBase, X3)
		asm.LDR(X2, X3, 0)
	}
	asm.LSRimm(X3, X2, 48)
	asm.MOVimm16(X4, NB_TagIntShr48)
	asm.CMPreg(X3, X4)
	asm.BCond(CondNE, fallbackLabel)
	EmitUnboxInt(asm, X2, X2) // X2 = key int value

	// --- Step 5: Array bounds check (with append fast path) ---
	// Check: key >= 0 (0-indexed array)
	asm.CMPimm(X2, 0) // key >= 0?
	asm.BCond(CondLT, fallbackLabel)

	inBoundsLabel := fmt.Sprintf("settable_inbounds_%d", pc)
	doneLabel := fmt.Sprintf("settable_done_%d", pc)
	boolSetLabel := fmt.Sprintf("settable_bool_%d", pc)
	boolInBoundsLabel := fmt.Sprintf("settable_bool_inbounds_%d", pc)
	intSetLabel := fmt.Sprintf("settable_int_%d", pc)
	intInBoundsLabel := fmt.Sprintf("settable_int_inbounds_%d", pc)
	floatSetLabel := fmt.Sprintf("settable_float_%d", pc)
	floatInBoundsLabel := fmt.Sprintf("settable_float_inbounds_%d", pc)

	// Check arrayKind for type-specialized paths
	asm.LDRB(X6, X0, TableOffArrayKind)
	asm.CMPimmW(X6, AKBool)
	asm.BCond(CondEQ, boolSetLabel)
	asm.CMPimmW(X6, AKInt)
	asm.BCond(CondEQ, intSetLabel)
	asm.CMPimmW(X6, AKFloat)
	asm.BCond(CondEQ, floatSetLabel)

	// For unknown typed arrays, fall back
	asm.CBNZ(X6, fallbackLabel) // arrayKind != 0 (not ArrayMixed) → fallback

	// --- Mixed array path (arrayKind == 0 / ArrayMixed) ---
	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key vs array.len
	asm.BCond(CondLT, inBoundsLabel)  // key < len: normal in-bounds write
	asm.BCond(CondNE, fallbackLabel)  // key > len: sparse write, fallback

	// key == len: append fast path (mixed array)
	// Only when there is spare capacity (cap > len); otherwise Go must realloc.
	asm.LDR(X4, X0, TableOffArray+16) // X4 = array.cap
	asm.CMPreg(X2, X4)                // key < cap? (room to append)
	asm.BCond(CondGE, fallbackLabel)   // no capacity → fallback (Go reallocs)

	// Update array.len = key + 1
	asm.ADDimm(X5, X2, 1)
	asm.STR(X5, X0, TableOffArray+8)

	// Set keysDirty = true (new key added)
	asm.MOVimm16(X5, 1)
	asm.STRB(X5, X0, TableOffKeysDirty)

	// Fall through to in-bounds write (array[key] now within new len)
	asm.Label(inBoundsLabel)

	// --- Step 6: Compute &array[key] and copy value (mixed array) ---
	asm.LDR(X3, X0, TableOffArray)     // X3 = array.ptr
	EmitMulValueSize(asm, X4, X2, X5)  // X4 = key * ValueSize
	asm.ADDreg(X3, X3, X4)             // X3 = &array[key]

	// Load value from RK(C) and store to array[key] (24-byte copy: 3 words)
	if cidx >= vm.RKBit {
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
			asm.STR(X0, X3, w*8)
		}
	} else {
		valOff := cidx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regRegs, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regRegs, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X3, w*8)
		}
	}
	asm.B(doneLabel)

	// --- ArrayInt path: STR to intArray (8-byte store) ---
	asm.Label(intSetLabel)
	asm.LDR(X3, X0, TableOffIntArray+8)  // X3 = intArray.len
	asm.CMPreg(X2, X3)                   // key vs intArray.len
	asm.BCond(CondLT, intInBoundsLabel)  // key < len: in-bounds
	asm.BCond(CondNE, fallbackLabel)     // key > len: sparse, fallback

	// key == len: append fast path for intArray
	asm.LDR(X4, X0, TableOffIntArray+16) // X4 = intArray.cap
	asm.CMPreg(X2, X4)
	asm.BCond(CondGE, fallbackLabel)     // no capacity → fallback

	// Update intArray.len = key + 1
	asm.ADDimm(X5, X2, 1)
	asm.STR(X5, X0, TableOffIntArray+8)

	// Set keysDirty = true
	asm.MOVimm16(X5, 1)
	asm.STRB(X5, X0, TableOffKeysDirty)

	asm.Label(intInBoundsLabel)
	// Load NaN-boxed value from RK(C), unbox int, store to intArray[key]
	asm.LDR(X3, X0, TableOffIntArray) // X3 = intArray.ptr
	asm.LSLimm(X5, X2, 3)            // X5 = key * 8 (byte offset)
	if cidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(cidx)
		valOff := constIdx * ValueSize
		if valOff <= 32760 {
			asm.LDR(X4, regConsts, valOff)
		} else {
			asm.LoadImm64(X4, int64(valOff))
			asm.ADDreg(X4, regConsts, X4)
			asm.LDR(X4, X4, 0)
		}
	} else {
		valOff := cidx * ValueSize
		if valOff <= 32760 {
			asm.LDR(X4, regRegs, valOff)
		} else {
			asm.LoadImm64(X4, int64(valOff))
			asm.ADDreg(X4, regRegs, X4)
			asm.LDR(X4, X4, 0)
		}
	}
	EmitUnboxInt(asm, X4, X4) // extract raw int from NaN-boxed value
	asm.STRreg(X4, X3, X5) // intArray[key] = raw int (ptr + key*8)
	asm.B(doneLabel)

	// --- ArrayFloat path: STR to floatArray (8-byte store) ---
	asm.Label(floatSetLabel)
	asm.LDR(X3, X0, TableOffFloatArray+8)  // X3 = floatArray.len
	asm.CMPreg(X2, X3)                     // key vs floatArray.len
	asm.BCond(CondLT, floatInBoundsLabel)  // key < len: in-bounds
	asm.BCond(CondNE, fallbackLabel)       // key > len: sparse, fallback

	// key == len: append fast path for floatArray
	asm.LDR(X4, X0, TableOffFloatArray+16) // X4 = floatArray.cap
	asm.CMPreg(X2, X4)
	asm.BCond(CondGE, fallbackLabel)       // no capacity → fallback

	// Update floatArray.len = key + 1
	asm.ADDimm(X5, X2, 1)
	asm.STR(X5, X0, TableOffFloatArray+8)

	// Set keysDirty = true
	asm.MOVimm16(X5, 1)
	asm.STRB(X5, X0, TableOffKeysDirty)

	asm.Label(floatInBoundsLabel)
	// Load value data from RK(C), store 8 bytes to floatArray[key]
	asm.LDR(X3, X0, TableOffFloatArray) // X3 = floatArray.ptr
	asm.LSLimm(X5, X2, 3)              // X5 = key * 8 (byte offset)
	if cidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(cidx)
		valDataOff := constIdx*ValueSize + OffsetData
		if valDataOff <= 32760 {
			asm.LDR(X4, regConsts, valDataOff)
		} else {
			asm.LoadImm64(X4, int64(valDataOff))
			asm.ADDreg(X4, regConsts, X4)
			asm.LDR(X4, X4, 0)
		}
	} else {
		valDataOff := cidx*ValueSize + OffsetData
		if valDataOff <= 32760 {
			asm.LDR(X4, regRegs, valDataOff)
		} else {
			asm.LoadImm64(X4, int64(valDataOff))
			asm.ADDreg(X4, regRegs, X4)
			asm.LDR(X4, X4, 0)
		}
	}
	asm.STRreg(X4, X3, X5) // floatArray[key] = data (ptr + key*8)
	asm.B(doneLabel)

	// --- ArrayBool path: STRB to boolArray with sentinel encoding ---
	// Value RK(C) must be a bool. Load its data field (0=false, 1=true),
	// convert to sentinel (data+1: 1=false, 2=true), STRB to boolArray[key].
	asm.Label(boolSetLabel)
	asm.LDR(X3, X0, TableOffBoolArray+8) // X3 = boolArray.len
	asm.CMPreg(X2, X3)                   // key vs boolArray.len
	asm.BCond(CondLT, boolInBoundsLabel) // key < len: in-bounds
	asm.BCond(CondNE, fallbackLabel)     // key > len: sparse, fallback

	// key == len: append fast path for boolArray
	asm.LDR(X4, X0, TableOffBoolArray+16) // X4 = boolArray.cap
	asm.CMPreg(X2, X4)
	asm.BCond(CondGE, fallbackLabel) // no capacity → fallback

	// Update boolArray.len = key + 1
	asm.ADDimm(X5, X2, 1)
	asm.STR(X5, X0, TableOffBoolArray+8)

	// Set keysDirty = true
	asm.MOVimm16(X5, 1)
	asm.STRB(X5, X0, TableOffKeysDirty)

	asm.Label(boolInBoundsLabel)

	// Load NaN-boxed value from RK(C), extract bool payload, convert to sentinel, store byte
	asm.LDR(X3, X0, TableOffBoolArray) // X3 = boolArray.ptr
	if cidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(cidx)
		valOff := constIdx * ValueSize
		if valOff <= 32760 {
			asm.LDR(X4, regConsts, valOff)
		} else {
			asm.LoadImm64(X4, int64(valOff))
			asm.ADDreg(X4, regConsts, X4)
			asm.LDR(X4, X4, 0)
		}
	} else {
		valOff := cidx * ValueSize
		if valOff <= 32760 {
			asm.LDR(X4, regRegs, valOff)
		} else {
			asm.LoadImm64(X4, int64(valOff))
			asm.ADDreg(X4, regRegs, X4)
			asm.LDR(X4, X4, 0)
		}
	}
	// Extract bool payload from NaN-boxed value (bit 0)
	asm.LoadImm64(X6, nb_i64(NB_PayloadMask))
	asm.ANDreg(X4, X4, X6) // X4 = payload (0 for false, 1 for true)
	asm.ADDimm(X4, X4, 1) // sentinel: 0→1 (false), 1→2 (true)
	asm.STRBreg(X4, X3, X2)
	asm.Label(doneLabel)
	// Fallback deferred to cold section.
	capturedPinnedST := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedST[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedST {
			cg.spillPinnedRegNB(vmReg, armReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2)
		cg.asm.B("epilogue")
	})

	return nil
}
