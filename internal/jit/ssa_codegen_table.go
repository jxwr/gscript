//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// emitSSALoadGlobal emits code for SSA_LOAD_GLOBAL: loads a full Value from the constant pool.
func emitSSALoadGlobal(asm *Assembler, inst *SSAInst) {
	constIdx := int(inst.AuxInt)
	dstSlot := int(inst.Slot)
	if dstSlot >= 0 && constIdx >= 0 {
		constOff := constIdx * ValueSize
		dstOff := dstSlot * ValueSize
		// Copy ValueSize bytes (ValueSize/8 words) from constants to registers
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X0, regConsts, constOff+w*8)
			asm.STR(X0, regRegs, dstOff+w*8)
		}
	}
}

// emitSSALoadArray emits code for SSA_LOAD_ARRAY: R(A) = table[key].
// Type-specialized fast path: if arrayKind == ArrayInt or ArrayFloat,
// load directly from intArray/floatArray (8 bytes) instead of the
// generic []Value array (24 bytes per element + type check).
func emitSSALoadArray(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	tableSlot := sm.getSlotForRef(inst.Arg1)
	asm.LoadImm64(X9, int64(inst.PC))
	dstSlot := int(inst.Slot)
	// Load key
	keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
	// In-loop table type guard + extract pointer
	if tableSlot >= 0 {
		asm.LDR(X0, regRegs, tableSlot*ValueSize)
		EmitCheckIsTableFull(asm, X0, X1, X3, "side_exit")
		EmitExtractPtr(asm, X0, X0)
	}
	asm.CBZ(X0, "side_exit")
	// Check metatable == nil
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit")

	// --- Type-specialized int array fast path ---
	if inst.Type == SSATypeInt && dstSlot >= 0 {
		doneLabel := fmt.Sprintf("load_array_done_%d", ref)
		boolLabel := fmt.Sprintf("load_array_bool_%d", ref)
		mixedLabel := fmt.Sprintf("load_array_mixed_%d", ref)

		// Check arrayKind == ArrayInt
		asm.LDRB(X1, X0, TableOffArrayKind)
		asm.CMPimmW(X1, AKInt)
		asm.BCond(CondNE, boolLabel)

		// Int array fast path: bounds check against intArray.len
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffIntArray+8) // intArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		// Load intArray[key]: ptr[key] with LSL #3 (8 bytes per element)
		asm.LDR(X3, X0, TableOffIntArray) // intArray.ptr
		asm.LDRreg(X0, X3, keyReg)        // X0 = *(X3 + keyReg*8)

		// Store result (raw int from intArray → NaN-box it)
		if r, ok := regMap.IntReg(dstSlot); ok {
			asm.MOVreg(r, X0)
		} else {
			EmitBoxIntFast(asm, X5, X0, regTagInt)
			asm.STR(X5, regRegs, dstSlot*ValueSize)
		}
		asm.B(doneLabel)

		// --- Bool array fast path ---
		// Sentinel encoding: 0=nil, 1=false, 2=true
		// Result: data = b >> 1 (0 for false, 1 for true); nil → side-exit
		asm.Label(boolLabel)
		asm.CMPimmW(X1, AKBool)
		asm.BCond(CondNE, mixedLabel)

		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffBoolArray+8) // boolArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffBoolArray) // boolArray.ptr
		asm.LDRBreg(X0, X3, keyReg)        // X0 = boolArray[key] (byte)
		asm.CBZ(X0, "side_exit")            // 0 = nil → side-exit
		asm.LSRimm(X0, X0, 1)              // 1→0 (false), 2→1 (true)

		if r, ok := regMap.IntReg(dstSlot); ok {
			asm.MOVreg(r, X0)
		} else {
			// Store as NaN-boxed int (bool treated as int in SSA)
			EmitBoxIntFast(asm, X5, X0, regTagInt)
			asm.STR(X5, regRegs, dstSlot*ValueSize)
		}
		asm.B(doneLabel)

		// Mixed fallback path
		asm.Label(mixedLabel)
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8) // array.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray) // array.ptr
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)

		// NaN-boxing: load full value, check int tag
		asm.LDR(X0, X3, 0) // load NaN-boxed value from array
		asm.LSRimm(X4, X0, 48)
		asm.MOVimm16(X5, NB_TagIntShr48)
		asm.CMPreg(X4, X5)
		typeGuardLabel := fmt.Sprintf("load_array_int_bool_%d", ref)
		asm.BCond(CondEQ, typeGuardLabel)
		asm.MOVimm16(X5, NB_TagBoolShr48)
		asm.CMPreg(X4, X5)
		asm.BCond(CondNE, "side_exit")
		asm.Label(typeGuardLabel)

		// Unbox int (works for both int and bool payloads)
		EmitUnboxInt(asm, X0, X0)
		if r, ok := regMap.IntReg(dstSlot); ok {
			asm.MOVreg(r, X0)
		} else {
			EmitBoxIntFast(asm, X5, X0, regTagInt)
			asm.STR(X5, regRegs, dstSlot*ValueSize)
		}
		asm.Label(doneLabel)

	} else if inst.Type == SSATypeFloat && dstSlot >= 0 {
		// --- Type-specialized float array fast path ---
		doneLabel := fmt.Sprintf("load_array_done_%d", ref)
		mixedLabel := fmt.Sprintf("load_array_mixed_%d", ref)

		// Check arrayKind == ArrayFloat
		asm.LDRB(X1, X0, TableOffArrayKind)
		asm.CMPimmW(X1, AKFloat)
		asm.BCond(CondNE, mixedLabel)

		// Float array fast path: bounds check against floatArray.len
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffFloatArray+8) // floatArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		// Load floatArray[key]: ptr[key] with LSL #3 (8 bytes per element)
		asm.LDR(X3, X0, TableOffFloatArray) // floatArray.ptr
		asm.LDRreg(X0, X3, keyReg)          // X0 = *(X3 + keyReg*8) = float64 bits

		// Store result (raw float64 bits from floatArray)
		// With NaN-boxing, raw float64 bits ARE the NaN-boxed value
		if fr, ok := regMap.FloatReg(dstSlot); ok {
			asm.FMOVtoFP(fr, X0)
		} else {
			asm.STR(X0, regRegs, dstSlot*ValueSize)
		}
		asm.B(doneLabel)

		// Mixed fallback path
		asm.Label(mixedLabel)
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8) // array.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray) // array.ptr
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)

		// NaN-boxing: load full value, check it's a float (not tagged)
		asm.LDR(X0, X3, 0) // load NaN-boxed value
		// Float check: bits 50-62 NOT all set
		EmitIsTagged(asm, X0, X4)
		asm.BCond(CondEQ, "side_exit") // tagged (not float) → side-exit

		if fr, ok := regMap.FloatReg(dstSlot); ok {
			asm.FMOVtoFP(fr, X0) // raw bits → FP reg
		} else {
			asm.STR(X0, regRegs, dstSlot*ValueSize) // float IS the NaN-boxed form
		}
		asm.Label(doneLabel)

	} else if dstSlot >= 0 {
		// Unspecialized fallback: use []Value array, copy full Value
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8)
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray)
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X0, X3, w*8)
			asm.STR(X0, regRegs, dstSlot*ValueSize+w*8)
		}
	}
}

// emitSSAStoreArray emits code for SSA_STORE_ARRAY: table[key] = value.
// Type-specialized fast path: if arrayKind matches the value type,
// store directly to intArray/floatArray (single 8-byte STR) instead
// of the generic []Value array (3x 8-byte STR for 24-byte Value).
func emitSSAStoreArray(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	tableSlot := sm.getSlotForRef(inst.Arg1)
	asm.LoadImm64(X9, int64(inst.PC))
	keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
	valRef := SSARef(inst.AuxInt)
	valSlot := sm.getSlotForRef(valRef)

	// Determine value type from register allocation
	valIsInt := false
	valIsFloat := false
	valIsBool := false
	if valSlot >= 0 {
		if _, ok := regMap.IntReg(valSlot); ok {
			valIsInt = true
		} else if _, ok := regMap.FloatReg(valSlot); ok {
			valIsFloat = true
		}
	}
	// Also check SSA type of the value ref
	if !valIsInt && !valIsFloat && int(valRef) < len(f.Insts) {
		valInst := f.Insts[valRef]
		if valInst.Type == SSATypeInt {
			valIsInt = true
		} else if valInst.Type == SSATypeFloat {
			valIsFloat = true
		} else if valInst.Type == SSATypeBool {
			valIsBool = true
		}
	}

	// For the mixed fallback, we need the value spilled to memory as NaN-boxed.
	// Int registers: box and store. Float D registers: FSTRd (raw bits ARE
	// the NaN-boxed form). Without this spill, the mixed fallback reads stale
	// memory when the live value is only in a D register.
	if valSlot >= 0 {
		if r, ok := regMap.IntReg(valSlot); ok {
			EmitBoxInt(asm, X5, r, X6)
			asm.STR(X5, regRegs, valSlot*ValueSize)
		} else if fr, ok := regMap.FloatReg(valSlot); ok {
			asm.FSTRd(fr, regRegs, valSlot*ValueSize)
		} else if fr, ok := regMap.FloatRefReg(valRef); ok {
			asm.FSTRd(fr, regRegs, valSlot*ValueSize)
		}
	}
	// In-loop table type guard + extract pointer
	if tableSlot >= 0 {
		asm.LDR(X0, regRegs, tableSlot*ValueSize)
		EmitCheckIsTableFull(asm, X0, X1, X3, "side_exit")
		EmitExtractPtr(asm, X0, X0)
	}
	asm.CBZ(X0, "side_exit")
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit")

	if valIsInt && valSlot >= 0 {
		// --- Int array fast path for STORE ---
		doneLabel := fmt.Sprintf("store_array_done_%d", ref)
		boolLabel := fmt.Sprintf("store_array_bool_%d", ref)
		mixedLabel := fmt.Sprintf("store_array_mixed_%d", ref)

		asm.LDRB(X1, X0, TableOffArrayKind)
		asm.CMPimmW(X1, AKInt)
		asm.BCond(CondNE, boolLabel)

		// Bounds check against intArray.len
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffIntArray+8) // intArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")

		// Store intArray[key] = value
		asm.LDR(X3, X0, TableOffIntArray) // intArray.ptr
		if r, ok := regMap.IntReg(valSlot); ok {
			asm.STRreg(r, X3, keyReg) // *(X3 + keyReg*8) = r
		} else {
			// NaN-boxed in memory: load and unbox to raw int
			asm.LDR(X4, regRegs, valSlot*ValueSize)
			EmitUnboxInt(asm, X4, X4)
			asm.STRreg(X4, X3, keyReg)
		}
		asm.B(doneLabel)

		// --- Bool array fallback for int store ---
		// When TypeBool is mapped to SSATypeInt, the data is 0/1.
		// Sentinel encoding: data + 1 (0→1=false, 1→2=true)
		asm.Label(boolLabel)
		asm.CMPimmW(X1, AKBool)
		asm.BCond(CondNE, mixedLabel)

		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffBoolArray+8) // boolArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")

		asm.LDR(X3, X0, TableOffBoolArray) // boolArray.ptr
		if r, ok := regMap.IntReg(valSlot); ok {
			asm.ADDimm(X4, r, 1) // sentinel = data + 1
		} else {
			// NaN-boxed in memory: load and unbox to raw int
			asm.LDR(X4, regRegs, valSlot*ValueSize)
			EmitUnboxInt(asm, X4, X4)
			asm.ADDimm(X4, X4, 1)
		}
		asm.STRBreg(X4, X3, keyReg)
		asm.MOVimm16(X4, 1)
		asm.STRB(X4, X0, TableOffKeysDirty)
		asm.B(doneLabel)

		// Mixed fallback: copy full NaN-boxed value from VM regs to array
		asm.Label(mixedLabel)
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8)
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray)
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X4, regRegs, valSlot*ValueSize+w*8)
			asm.STR(X4, X3, w*8)
		}
		asm.Label(doneLabel)

	} else if valIsFloat && valSlot >= 0 {
		// --- Float array fast path for STORE ---
		doneLabel := fmt.Sprintf("store_array_done_%d", ref)
		mixedLabel := fmt.Sprintf("store_array_mixed_%d", ref)

		asm.LDRB(X1, X0, TableOffArrayKind)
		asm.CMPimmW(X1, AKFloat)
		asm.BCond(CondNE, mixedLabel)

		// Bounds check against floatArray.len
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffFloatArray+8) // floatArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")

		// Store floatArray[key] = value (float64 bits)
		asm.LDR(X3, X0, TableOffFloatArray) // floatArray.ptr
		if fr, ok := regMap.FloatReg(valSlot); ok {
			// Float reg → need to FMOV to GP reg first, then STRreg
			asm.FMOVtoGP(X4, fr)
			asm.STRreg(X4, X3, keyReg)
		} else {
			asm.LDR(X4, regRegs, valSlot*ValueSize+OffsetData)
			asm.STRreg(X4, X3, keyReg)
		}
		asm.B(doneLabel)

		// Mixed fallback
		asm.Label(mixedLabel)
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8)
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray)
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)
		if valSlot >= 0 {
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X4, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X4, X3, w*8)
			}
		}
		asm.Label(doneLabel)

	} else if valIsBool {
		// --- Bool array fast path for STORE ---
		// Value is SSA_CONST_BOOL: data=0 (false) or data=1 (true)
		// Sentinel encoding: 0=nil, 1=false, 2=true → store data+1
		doneLabel := fmt.Sprintf("store_array_done_%d", ref)
		mixedLabel := fmt.Sprintf("store_array_mixed_%d", ref)

		asm.LDRB(X1, X0, TableOffArrayKind)
		asm.CMPimmW(X1, AKBool)
		asm.BCond(CondNE, mixedLabel)

		// Bounds check against boolArray.len
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffBoolArray+8) // boolArray.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")

		// Store boolArray[key] = data + 1 (sentinel encoding)
		asm.LDR(X3, X0, TableOffBoolArray) // boolArray.ptr
		if valSlot >= 0 {
			// NaN-boxed bool: extract payload (bottom bit) then sentinel-encode
			asm.LDR(X4, regRegs, valSlot*ValueSize)
			asm.LoadImm64(X5, 1)
			asm.ANDreg(X4, X4, X5) // extract bool payload (0 or 1)
			asm.ADDimm(X4, X4, 1)  // 0→1 (false), 1→2 (true)
		} else {
			asm.MOVimm16(X4, 1) // default: false sentinel
		}
		asm.STRBreg(X4, X3, keyReg)

		// Set keysDirty = true
		asm.MOVimm16(X4, 1)
		asm.STRB(X4, X0, TableOffKeysDirty)
		asm.B(doneLabel)

		// Mixed fallback
		asm.Label(mixedLabel)
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8)
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray)
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)
		if valSlot >= 0 {
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X0, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X0, X3, w*8)
			}
		}
		asm.Label(doneLabel)

	} else {
		// Untyped fallback: use existing []Value array path
		asm.CMPimm(keyReg, 0)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8)
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray)
		EmitMulValueSize(asm, X4, keyReg, X5)
		asm.ADDreg(X3, X3, X4)
		if valSlot >= 0 {
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X0, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X0, X3, w*8)
			}
		}
	}
}

// emitSSALoadField emits code for SSA_LOAD_FIELD: R(A) = table.field at known skeys index.
func emitSSALoadField(asm *Assembler, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	fieldIdx := int(inst.AuxInt)
	tableSlot := sm.getSlotForRef(inst.Arg1)
	dstSlot := int(inst.Slot)
	asm.LoadImm64(X9, int64(inst.PC)) // side-exit PC

	if fieldIdx < 0 || tableSlot < 0 {
		// Unknown field index → side-exit (can't compile)
		asm.B("side_exit")
		return
	}

	// In-loop table type guard: slot may have been reused by arithmetic
	// in a previous iteration (slot reuse across iterations).
	asm.LDR(X0, regRegs, tableSlot*ValueSize)
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit")

	// Extract *Table pointer
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit") // nil table

	// Guard: no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit")

	// Guard: skeys length > fieldIdx (shape hasn't shrunk)
	asm.LDR(X1, X0, TableOffSkeysLen)
	asm.CMPimm(X1, uint16(fieldIdx+1))
	asm.BCond(CondLT, "side_exit")

	// Load svals[fieldIdx]: svals base + fieldIdx * ValueSize
	asm.LDR(X1, X0, TableOffSvals) // X1 = svals base pointer
	svalsOff := fieldIdx * ValueSize
	// Copy entire Value from svals[fieldIdx] to R(A)
	if dstSlot >= 0 {
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X2, X1, svalsOff+w*8)
			asm.STR(X2, regRegs, dstSlot*ValueSize+w*8)
		}
	}
}

// emitSSAStoreField emits code for SSA_STORE_FIELD: table.field = value at known skeys index.
func emitSSAStoreField(asm *Assembler, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	fieldIdx := int(inst.AuxInt)
	tableSlot := sm.getSlotForRef(inst.Arg1)
	valSlot := sm.getSlotForRef(inst.Arg2)
	asm.LoadImm64(X9, int64(inst.PC))

	if fieldIdx < 0 || tableSlot < 0 {
		asm.B("side_exit")
		return
	}

	// In-loop table type guard
	asm.LDR(X0, regRegs, tableSlot*ValueSize)
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit")

	// Extract *Table pointer
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit")

	// Guard: no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit")

	// Guard: skeys length > fieldIdx
	asm.LDR(X1, X0, TableOffSkeysLen)
	asm.CMPimm(X1, uint16(fieldIdx+1))
	asm.BCond(CondLT, "side_exit")

	// Store value to svals[fieldIdx]
	asm.LDR(X1, X0, TableOffSvals)
	svalsOff := fieldIdx * ValueSize
	if valSlot >= 0 {
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X2, regRegs, valSlot*ValueSize+w*8)
			asm.STR(X2, X1, svalsOff+w*8)
		}
	}
}

// === Side-exit continuation for inner loop escape ===

// sideExitContinuation holds analysis results for the inner loop escape optimization.
// When a float guard inside the inner loop fails (e.g., zr²+zi² > 4.0 in mandelbrot),
// instead of side-exiting to the interpreter, we skip the post-inner-loop epilogue
// (GUARD_TRUTHY + count++) and jump directly to the outer FORLOOP increment.
//
// Additionally, when GUARD_TRUTHY fails (non-escaping pixel), instead of side-exiting,
// we execute count++ inline and continue the outer FORLOOP.
type sideExitContinuation struct {
	innerLoopStartIdx   int // index of SSA_INNER_LOOP
	innerLoopEndIdx     int // index of SSA_LE_INT(AuxInt=1)
	innerLoopSlot       int // VM slot of inner loop index (for spilling)
	outerForLoopAddIdx  int // index of the outer FORLOOP's ADD_INT (skip_count target)

	// GUARD_TRUTHY continuation: when escaped=false, execute count++ inline
	guardTruthyIdx int // index of GUARD_TRUTHY in SSA (for redirecting)
	countSlot      int // VM slot of count variable (-1 if unknown)
	countStepSlot  int // VM slot or constant for count increment (-1 if unknown)
	countIsRK      bool // true if countStepSlot is RK (constant)
}

// analyzeSideExitContinuation scans the SSA to detect the inner loop structure
// for the side-exit continuation optimization. Returns nil if no inner loop is found
// or the pattern doesn't match.
func analyzeSideExitContinuation(f *SSAFunc, loopIdx int) *sideExitContinuation {
	info := &sideExitContinuation{
		innerLoopStartIdx:  -1,
		innerLoopEndIdx:    -1,
		innerLoopSlot:      -1,
		outerForLoopAddIdx: -1,
		guardTruthyIdx:     -1,
		countSlot:          -1,
		countStepSlot:      -1,
	}

	// Find SSA_INNER_LOOP and SSA_LE_INT(AuxInt=1) after the main LOOP
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_INNER_LOOP {
			info.innerLoopStartIdx = i
		}
		if inst.Op == SSA_LE_INT && inst.AuxInt == 1 {
			info.innerLoopEndIdx = i
		}
	}

	if info.innerLoopStartIdx < 0 || info.innerLoopEndIdx < 0 {
		return nil // no inner loop
	}

	// Check that there are float guards inside the inner loop
	hasFloatGuard := false
	for i := info.innerLoopStartIdx; i < info.innerLoopEndIdx; i++ {
		if isFloatGuard(f.Insts[i].Op) {
			hasFloatGuard = true
			break
		}
	}
	if !hasFloatGuard {
		return nil // no float guards to optimize
	}

	// Find the inner loop's slot from LE_INT(AuxInt=1)'s Arg1
	leInst := &f.Insts[info.innerLoopEndIdx]
	arg1Ref := leInst.Arg1
	if int(arg1Ref) < len(f.Insts) {
		argInst := &f.Insts[arg1Ref]
		if argInst.Slot >= 0 {
			info.innerLoopSlot = int(argInst.Slot)
		}
	}

	// Find GUARD_TRUTHY between inner_loop_done and the outer FORLOOP
	for i := info.innerLoopEndIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_GUARD_TRUTHY {
			info.guardTruthyIdx = i
			break
		}
		// Stop scanning if we hit the outer exit check
		if (inst.Op == SSA_LE_INT && inst.AuxInt == 0) || inst.Op == SSA_LT_INT {
			break
		}
	}

	// Find the outer FORLOOP's ADD_INT: it's the Arg1 of LE_INT(AuxInt=0)
	for i := info.innerLoopEndIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_LE_INT && inst.AuxInt == 0 {
			// The outer FORLOOP exit check. Its Arg1 is the ADD_INT (idx += step).
			addRef := inst.Arg1
			if int(addRef) >= 0 && int(addRef) < len(f.Insts) {
				info.outerForLoopAddIdx = int(addRef)
			}
			break
		}
		if inst.Op == SSA_LT_INT {
			// While-loop style outer exit check
			addRef := inst.Arg1
			if int(addRef) >= 0 && int(addRef) < len(f.Insts) {
				info.outerForLoopAddIdx = int(addRef)
			}
			break
		}
	}

	if info.outerForLoopAddIdx < 0 {
		return nil // can't find outer FORLOOP increment
	}

	// Analyze count++ from bytecodes: look at the bytecodes between
	// the GUARD_TRUTHY's TEST PC and the outer FORLOOP PC.
	// Pattern: LOADINT Rtemp 1 → ADD Rtemp Rcount Rtemp → MOVE Rcount Rtemp
	// The real count slot is the source B of ADD (= destination A of MOVE).
	if info.guardTruthyIdx >= 0 && f.Trace != nil && f.Trace.LoopProto != nil {
		proto := f.Trace.LoopProto
		guardInst := &f.Insts[info.guardTruthyIdx]
		testPC := guardInst.PC // PC of the TEST instruction

		// The JMP after TEST tells us where count++ is.
		// TEST at testPC, JMP at testPC+1.
		if testPC+1 < len(proto.Code) {
			jmpInst := proto.Code[testPC+1]
			jmpOp := vm.DecodeOp(jmpInst)
			if jmpOp == vm.OP_JMP {
				jmpSBX := vm.DecodesBx(jmpInst)
				jmpTarget := testPC + 1 + jmpSBX + 1
				// Scan the skipped instructions for the ADD+MOVE pattern
				for pc := testPC + 2; pc < jmpTarget && pc < len(proto.Code); pc++ {
					inst := proto.Code[pc]
					op := vm.DecodeOp(inst)
					if op == vm.OP_ADD {
						addB := vm.DecodeB(inst) // source: count slot
						// Look for a MOVE after the ADD that copies result to the count slot
						if pc+1 < jmpTarget && pc+1 < len(proto.Code) {
							moveInst := proto.Code[pc+1]
							moveOp := vm.DecodeOp(moveInst)
							if moveOp == vm.OP_MOVE {
								moveA := vm.DecodeA(moveInst) // destination
								if moveA == addB {
									// Confirmed: count is at addB, and the pattern is
									// ADD Rtemp Rcount Rstep → MOVE Rcount Rtemp
									info.countSlot = addB
								}
							}
						}
						if info.countSlot < 0 {
							// No MOVE after ADD → direct count++: ADD Rcount Rcount Rstep
							info.countSlot = vm.DecodeA(inst)
						}
						break
					}
				}
			}
		}
	}

	return info
}

// isFloatGuard returns true if the SSA op is a float comparison guard.
func isFloatGuard(op SSAOp) bool {
	switch op {
	case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	}
	return false
}

// emitGuardTruthyWithContinuation emits a GUARD_TRUTHY that branches to the
// given target label instead of "side_exit" on failure. Used for the non-escaping
// pixel continuation: instead of side-exiting, jump to truthy_cont which does count++.
func emitGuardTruthyWithContinuation(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, target string) {
	slot := int(inst.Slot)
	asm.LoadImm64(X9, int64(inst.PC))
	// NaN-boxing: load full 8-byte value and compare against NaN-boxed constants
	asm.LDR(X0, regRegs, slot*ValueSize)
	if inst.AuxInt == 0 {
		// Expect truthy: branch to target if nil or false
		asm.LoadImm64(X1, nb_i64(NB_ValNil))
		asm.CMPreg(X0, X1)
		asm.BCond(CondEQ, target) // nil → falsy → continuation
		asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		asm.CMPreg(X0, X1)
		asm.BCond(CondEQ, target) // false → falsy → continuation
	} else {
		// Expect falsy: branch to target if truthy (not nil and not false)
		doneLabel := fmt.Sprintf("guard_falsy_cont_%d", ref)
		asm.LoadImm64(X1, nb_i64(NB_ValNil))
		asm.CMPreg(X0, X1)
		asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
		asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		asm.CMPreg(X0, X1)
		asm.BCond(CondNE, target) // not nil, not false → truthy → continuation
		asm.Label(doneLabel)
	}
}

// emitFloatGuardWithTarget emits a float comparison guard that branches to the
// given target label instead of "side_exit". Used for inner loop escape optimization.
func emitFloatGuardWithTarget(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder, target string) {
	asm.LoadImm64(X9, int64(inst.PC))
	arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
	arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D1)
	asm.FCMPd(arg1D, arg2D)
	switch inst.Op {
	case SSA_LT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGE, target)
		} else {
			asm.BCond(CondLT, target)
		}
	case SSA_LE_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGT, target)
		} else {
			asm.BCond(CondLE, target)
		}
	case SSA_GT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondLE, target)
		} else {
			asm.BCond(CondGT, target)
		}
	}
}
