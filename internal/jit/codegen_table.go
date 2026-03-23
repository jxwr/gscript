//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

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

	doneLabel := fmt.Sprintf("settable_done_%d", pc)
	boolSetLabel := fmt.Sprintf("settable_bool_%d", pc)
	intSetLabel := fmt.Sprintf("settable_int_%d", pc)
	floatSetLabel := fmt.Sprintf("settable_float_%d", pc)

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

	// Dispatch to per-kind store paths.
	// State: X0 = *Table, X2 = key (int, >= 0).
	cg.emitSetTableMixed(pc, cidx, fallbackLabel, doneLabel)

	cg.emitSetTableInt(pc, cidx, fallbackLabel, doneLabel, intSetLabel)

	cg.emitSetTableFloat(pc, cidx, fallbackLabel, doneLabel, floatSetLabel)

	cg.emitSetTableBool(pc, cidx, fallbackLabel, doneLabel, boolSetLabel)

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

// emitSetTableMixed emits the mixed-array store path for SETTABLE.
// Precondition: X0 = *Table pointer, X2 = integer key (>= 0).
func (cg *Codegen) emitSetTableMixed(pc, cidx int, fallbackLabel, doneLabel string) {
	asm := cg.asm
	inBoundsLabel := fmt.Sprintf("settable_inbounds_%d", pc)

	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key vs array.len
	asm.BCond(CondLT, inBoundsLabel)  // key < len: normal in-bounds write
	asm.BCond(CondNE, fallbackLabel)  // key > len: sparse write, fallback

	// key == len: append fast path (mixed array)
	// Only when there is spare capacity (cap > len); otherwise Go must realloc.
	cg.emitSetTableAppend(TableOffArray, inBoundsLabel, fallbackLabel)

	// --- Compute &array[key] and copy value (mixed array) ---
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
}

// emitSetTableInt emits the int-array store path for SETTABLE.
// Precondition: X0 = *Table pointer, X2 = integer key (>= 0).
func (cg *Codegen) emitSetTableInt(pc, cidx int, fallbackLabel, doneLabel, entryLabel string) {
	asm := cg.asm
	inBoundsLabel := fmt.Sprintf("settable_int_inbounds_%d", pc)

	asm.Label(entryLabel)
	asm.LDR(X3, X0, TableOffIntArray+8)  // X3 = intArray.len
	asm.CMPreg(X2, X3)                   // key vs intArray.len
	asm.BCond(CondLT, inBoundsLabel)  // key < len: in-bounds
	asm.BCond(CondNE, fallbackLabel)     // key > len: sparse, fallback

	// key == len: append fast path for intArray
	cg.emitSetTableAppend(TableOffIntArray, inBoundsLabel, fallbackLabel)

	// Load NaN-boxed value from RK(C), unbox int, store to intArray[key]
	asm.LDR(X3, X0, TableOffIntArray) // X3 = intArray.ptr
	asm.LSLimm(X5, X2, 3)            // X5 = key * 8 (byte offset)
	cg.emitLoadRKWord(X4, cidx, 0)
	EmitUnboxInt(asm, X4, X4) // extract raw int from NaN-boxed value
	asm.STRreg(X4, X3, X5) // intArray[key] = raw int (ptr + key*8)
	asm.B(doneLabel)
}

// emitSetTableFloat emits the float-array store path for SETTABLE.
// Precondition: X0 = *Table pointer, X2 = integer key (>= 0).
func (cg *Codegen) emitSetTableFloat(pc, cidx int, fallbackLabel, doneLabel, entryLabel string) {
	asm := cg.asm
	floatInBoundsLabel := fmt.Sprintf("settable_float_inbounds_%d", pc)

	asm.Label(entryLabel)
	asm.LDR(X3, X0, TableOffFloatArray+8)  // X3 = floatArray.len
	asm.CMPreg(X2, X3)                     // key vs floatArray.len
	asm.BCond(CondLT, floatInBoundsLabel)  // key < len: in-bounds
	asm.BCond(CondNE, fallbackLabel)       // key > len: sparse, fallback

	// key == len: append fast path for floatArray
	cg.emitSetTableAppend(TableOffFloatArray, floatInBoundsLabel, fallbackLabel)

	// Load value data from RK(C), store 8 bytes to floatArray[key]
	asm.LDR(X3, X0, TableOffFloatArray) // X3 = floatArray.ptr
	asm.LSLimm(X5, X2, 3)              // X5 = key * 8 (byte offset)
	cg.emitLoadRKWord(X4, cidx, OffsetData)
	asm.STRreg(X4, X3, X5) // floatArray[key] = data (ptr + key*8)
	asm.B(doneLabel)
}

// emitSetTableBool emits the bool-array store path for SETTABLE.
// Precondition: X0 = *Table pointer, X2 = integer key (>= 0).
func (cg *Codegen) emitSetTableBool(pc, cidx int, fallbackLabel, doneLabel, entryLabel string) {
	asm := cg.asm
	boolInBoundsLabel := fmt.Sprintf("settable_bool_inbounds_%d", pc)

	// Value RK(C) must be a bool. Load its data field (0=false, 1=true),
	// convert to sentinel (data+1: 1=false, 2=true), STRB to boolArray[key].
	asm.Label(entryLabel)
	asm.LDR(X3, X0, TableOffBoolArray+8) // X3 = boolArray.len
	asm.CMPreg(X2, X3)                   // key vs boolArray.len
	asm.BCond(CondLT, boolInBoundsLabel) // key < len: in-bounds
	asm.BCond(CondNE, fallbackLabel)     // key > len: sparse, fallback

	// key == len: append fast path for boolArray
	cg.emitSetTableAppend(TableOffBoolArray, boolInBoundsLabel, fallbackLabel)

	// Load NaN-boxed value from RK(C), extract bool payload, convert to sentinel, store byte
	asm.LDR(X3, X0, TableOffBoolArray) // X3 = boolArray.ptr
	cg.emitLoadRKWord(X4, cidx, 0)
	// Extract bool payload from NaN-boxed value (bit 0)
	asm.LoadImm64(X6, nb_i64(NB_PayloadMask))
	asm.ANDreg(X4, X4, X6) // X4 = payload (0 for false, 1 for true)
	asm.ADDimm(X4, X4, 1) // sentinel: 0→1 (false), 1→2 (true)
	asm.STRBreg(X4, X3, X2)
}

// emitSetTableAppend emits the append-fast-path for a typed array in SETTABLE.
// Called when key == array.len. Checks capacity, bumps len, sets keysDirty,
// then falls through to the inBoundsLabel.
// Precondition: X0 = *Table pointer, X2 = key (== current len).
// arrayOff is the struct offset of the slice header (e.g. TableOffArray, TableOffIntArray).
func (cg *Codegen) emitSetTableAppend(arrayOff int, inBoundsLabel, fallbackLabel string) {
	asm := cg.asm

	asm.LDR(X4, X0, arrayOff+16)      // X4 = array.cap
	asm.CMPreg(X2, X4)                // key < cap? (room to append)
	asm.BCond(CondGE, fallbackLabel)   // no capacity → fallback (Go reallocs)

	// Update array.len = key + 1
	asm.ADDimm(X5, X2, 1)
	asm.STR(X5, X0, arrayOff+8)

	// Set keysDirty = true (new key added)
	asm.MOVimm16(X5, 1)
	asm.STRB(X5, X0, TableOffKeysDirty)

	asm.Label(inBoundsLabel)
}

// emitLoadRKWord loads 8 bytes from RK(idx) + extraOff into dst.
// extraOff is added to the base offset of the value (0 for the NaN-boxed word,
// OffsetData for the data field in multi-word values).
// Uses X4 as scratch when the offset exceeds LDR immediate range.
func (cg *Codegen) emitLoadRKWord(dst Reg, idx, extraOff int) {
	asm := cg.asm
	if idx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx*ValueSize + extraOff
		if off <= 32760 {
			asm.LDR(dst, regConsts, off)
		} else {
			asm.LoadImm64(dst, int64(off))
			asm.ADDreg(dst, regConsts, dst)
			asm.LDR(dst, dst, 0)
		}
	} else {
		off := idx*ValueSize + extraOff
		if off <= 32760 {
			asm.LDR(dst, regRegs, off)
		} else {
			asm.LoadImm64(dst, int64(off))
			asm.ADDreg(dst, regRegs, dst)
			asm.LDR(dst, dst, 0)
		}
	}
}
