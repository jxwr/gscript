//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
)

// findLoopInvariantTableSlots identifies table slots used in SSA_LOAD_ARRAY
// or SSA_STORE_ARRAY within the loop body that are NOT modified by any
// SSA_STORE_SLOT targeting the same slot. For these slots, the table type
// guard, metatable check, and array kind check can be hoisted to pre-loop.
func findLoopInvariantTableSlots(f *SSAFunc, loopIdx int, sm *ssaSlotMapper) map[int]int {
	// Map from table slot → SSAType (int/float) for hoistable guards
	result := make(map[int]int)
	// Set of slots modified in the loop body
	modifiedSlots := make(map[int]bool)

	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_STORE_SLOT {
			modifiedSlots[int(inst.Slot)] = true
		}
	}

	// Identify slots that have a pre-loop GUARD_TYPE for table type.
	// Only these slots are guaranteed to hold tables at loop entry.
	// Slots that are WBR (written before read) don't have pre-loop guards
	// and their pre-loop values may be stale (e.g., int/float from the
	// previous outer iteration). Hoisting table guards for such slots
	// causes false guard failures and trace blacklisting.
	guardedTableSlots := make(map[int]bool)
	for i := 0; i < loopIdx; i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_GUARD_TYPE && inst.AuxInt == int64(runtime.TypeTable) {
			loadRef := inst.Arg1
			if int(loadRef) < len(f.Insts) && f.Insts[loadRef].Op == SSA_LOAD_SLOT {
				guardedTableSlots[int(f.Insts[loadRef].Slot)] = true
			}
		}
	}

	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_LOAD_ARRAY || inst.Op == SSA_STORE_ARRAY {
			tableSlot := sm.getSlotForRef(inst.Arg1)
			if tableSlot >= 0 && !modifiedSlots[tableSlot] && guardedTableSlots[tableSlot] {
				result[tableSlot] = int(inst.Type) // SSATypeInt or SSATypeFloat
			}
		}
	}
	return result
}

// emitSSALoadArray emits code for SSA_LOAD_ARRAY: R(A) = table[key].
// Type-specialized fast path: if arrayKind == ArrayInt or ArrayFloat,
// load directly from intArray/floatArray (8 bytes) instead of the
// generic []Value array (24 bytes per element + type check).
func emitSSALoadArray(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, hoistedTables map[int]int) {
	tableSlot := sm.getSlotForRef(inst.Arg1)
	asm.LoadImm64(X9, int64(inst.PC))
	dstSlot := int(inst.Slot)
	// Load key
	keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)

	_, tableHoisted := hoistedTables[tableSlot]
	if tableHoisted && tableSlot >= 0 {
		// Table was verified in pre-loop guards (is-table, no-meta, array-kind).
		// Just load and extract the pointer — skip type/meta checks.
		asm.LDR(X0, regRegs, tableSlot*ValueSize)
		EmitExtractPtr(asm, X0, X0)
	} else {
		// Full in-loop table type guard + extract pointer
		if tableSlot >= 0 {
			asm.LDR(X0, regRegs, tableSlot*ValueSize)
			EmitCheckIsTableFull(asm, X0, X1, X3, "side_exit")
			EmitExtractPtr(asm, X0, X0)
		}
		asm.CBZ(X0, "side_exit")
		// Check metatable == nil
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")
	}

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
func emitSSAStoreArray(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, hoistedTables map[int]int) {
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
	_, tableHoisted := hoistedTables[tableSlot]
	if tableHoisted && tableSlot >= 0 {
		asm.LDR(X0, regRegs, tableSlot*ValueSize)
		EmitExtractPtr(asm, X0, X0)
	} else {
		if tableSlot >= 0 {
			asm.LDR(X0, regRegs, tableSlot*ValueSize)
			EmitCheckIsTableFull(asm, X0, X1, X3, "side_exit")
			EmitExtractPtr(asm, X0, X0)
		}
		asm.CBZ(X0, "side_exit")
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")
	}

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
