//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// LOAD_FIELD: table field access
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadField(ref SSARef, inst *SSAInst) {
	fieldIdx := int(int32(inst.AuxInt))

	// Invalid field index (not captured during recording) → skip.
	// This happens when GETFIELD targets a library table (e.g., math.sqrt)
	// whose field index wasn't resolved. The result is typically dead code
	// (the CALL was replaced by SSA_INTRINSIC). Emitting nothing is safe
	// because nothing references this instruction's slot.
	if fieldIdx < 0 {
		return
	}

	// Set ExitPC for any guard failure in this instruction
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the TABLE slot from Arg1 (the SSA ref for the table).
	// inst.Slot is the DESTINATION slot (ir.A), NOT the table slot.
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		ec.asm.B("side_exit_setup")
		return
	}

	// Load table NaN-boxed value. If the table source is a LOAD_GLOBAL,
	// load directly from the trace constant pool (regConsts) to avoid
	// slot conflicts with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		ec.asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		ec.asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	// Check it's a table
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")
	// Extract pointer
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Check no metatable
	ec.asm.LDR(X1, X0, TableOffMetatable)
	ec.asm.CBNZ(X1, "side_exit_setup")

	// Load field value: svals[fieldIdx]
	ec.asm.LDR(X1, X0, TableOffSvals) // X1 = svals slice data pointer
	ec.asm.LDR(X2, X1, fieldIdx*ValueSize) // X2 = svals[fieldIdx] (NaN-boxed)
	if inst.Type == SSATypeFloat {
		if freg, ok := ec.regMap.FloatRefReg(ref); ok {
			ec.asm.FMOVtoFP(freg, X2)
		} else if freg, ok := ec.regMap.FloatReg(int(inst.Slot)); ok {
			ec.asm.FMOVtoFP(freg, X2)
		} else {
			// Store to memory (raw float bits)
			ec.asm.STR(X2, regRegs, int(inst.Slot)*ValueSize)
		}
	} else if inst.Type == SSATypeInt {
		EmitUnboxInt(ec.asm, X2, X2)
		if reg, ok := ec.regMap.IntReg(int(inst.Slot)); ok {
			ec.asm.MOVreg(reg, X2)
		} else {
			// Store to memory (NaN-boxed)
			EmitBoxIntFast(ec.asm, X2, X2, regTagInt)
			ec.asm.STR(X2, regRegs, int(inst.Slot)*ValueSize)
		}
	} else {
		// Unknown type — store raw NaN-boxed value to memory
		ec.asm.STR(X2, regRegs, int(inst.Slot)*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_TABLE_SHAPE: load table shape pointer for guard
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadTableShape(ref SSARef, inst *SSAInst) {
	// Set ExitPC for any guard failure
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the table value (Arg1 is the SSA ref for the table)
	tableSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tableSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tableSlot < 0 {
		// Invalid table slot - should not happen in valid traces
		ec.asm.B("side_exit_setup")
		return
	}

	// Load table NaN-boxed value from slot
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		ec.asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		ec.asm.LDR(X0, regRegs, tableSlot*ValueSize)
	}

	// Check it's a table
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")

	// Extract table pointer
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Load shape pointer from table (at offset TableOffShape)
	// Result is in X0: the shape pointer (or nil if no shape)
	ec.asm.LDR(X0, X0, TableOffShape)

	// Store shape pointer in a scratch register for use by CHECK_SHAPE_ID
	// We use X2 for the shape pointer (X2 is a scratch register)
	ec.asm.MOVreg(X2, X0)
}

// ────────────────────────────────────────────────────────────────────────────
// CHECK_SHAPE_ID: guard that table.shape.ID matches expected
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitCheckShapeId(inst *SSAInst) {
	// Set ExitPC (X9) before any potential guard failure
	// BailoutID is stored in AuxInt
	bailoutID := int(inst.AuxInt)
	ec.asm.LoadImm64(X9, int64(bailoutID))

	// X2 contains the shape pointer from LOAD_TABLE_SHAPE
	// CBZ X2, side_exit_setup - if shape is nil, fail guard
	ec.asm.CBZ(X2, "side_exit_setup")

	// Load shape.ID (first field of Shape struct at offset 0)
	// Shape struct: ID uint32, FieldKeys []string, etc.
	// We only need to check the ID at offset 0
	ec.asm.LDRW(X0, X2, 0) // Load 32-bit shape ID

	// Expected shape ID is in inst.AuxInt (from LOAD_TABLE_SHAPE)
	// But for CHECK_SHAPE_ID, AuxInt contains bailout ID
	// We need to get the expected shape ID from the LOAD_TABLE_SHAPE instruction
	// that produced the shape reference (inst.Arg1)
	expectedShapeID := uint32(0)
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		shapeLoadInst := ec.f.Insts[inst.Arg1]
		if shapeLoadInst.Op == SSA_LOAD_TABLE_SHAPE {
			expectedShapeID = uint32(shapeLoadInst.AuxInt)
		}
	}

	// Compare loaded shape ID with expected
	ec.asm.LoadImm64(X1, int64(expectedShapeID))
	ec.asm.CMPreg(X0, X1)

	// Branch to side-exit if shape IDs don't match
	ec.asm.BCond(CondNE, "side_exit_setup")

	// Guard passed - shape is valid, direct svals[idx] access in LOAD_FIELD is safe
	// No additional code needed here
}

// ────────────────────────────────────────────────────────────────────────────
// STORE_FIELD: table field write
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitStoreField(inst *SSAInst) {
	// inst.Slot = table slot (ir.A), inst.AuxInt = fieldIndex
	// inst.Arg1 = table ref, inst.Arg2 = value ref
	fieldIdx := int(int32(inst.AuxInt))
	tblSlot := int(inst.Slot)

	// Invalid field index → side-exit
	if fieldIdx < 0 {
		ec.emitCallExitInst(inst)
		return
	}

	// Set ExitPC for any guard failure
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the value to store FIRST (before loading table pointer).
	// resolveFloatRef may clobber X0 (for constant loads), so we must
	// do this before we put the table pointer in X0.
	valInst := &ec.f.Insts[inst.Arg2]
	if valInst.Type == SSATypeFloat {
		freg := ec.resolveFloatRef(inst.Arg2, D0)
		ec.asm.FMOVtoGP(X3, freg)
	} else if valInst.Type == SSATypeInt {
		reg := ec.resolveIntRef(inst.Arg2, X3)
		EmitBoxIntFast(ec.asm, X3, reg, regTagInt)
	} else {
		// Load raw value from memory
		valSlot := int(valInst.Slot)
		if valSlot >= 0 {
			ec.asm.LDR(X3, regRegs, valSlot*ValueSize)
		}
	}

	// Load table pointer (X3 holds the value, X0 is free for table).
	// If the table source is a LOAD_GLOBAL, load from the trace constant pool
	// to avoid slot conflicts with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		ec.asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		ec.asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Check no metatable
	ec.asm.LDR(X1, X0, TableOffMetatable)
	ec.asm.CBNZ(X1, "side_exit_setup")

	// Store to svals[fieldIdx]
	ec.asm.LDR(X1, X0, TableOffSvals)
	ec.asm.STR(X3, X1, fieldIdx*ValueSize)
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_ARRAY / STORE_ARRAY
// ────────────────────────────────────────────────────────────────────────────

// emitLoadArray: R(A) = table[key] (integer index, native codegen)
//
// SSA encoding: Arg1=tableRef, Arg2=keyRef, Slot=destination slot
// The table's register slot is found via ec.f.Insts[inst.Arg1].Slot.
//
// Handles all arrayKind variants (Mixed, Int, Float, Bool) with
// runtime dispatch. Side-exits on bounds check failure or nil table.
// Falls back to call-exit for non-scalar result types (table, string, etc.)
// to avoid nested table access issues.
func (ec *emitCtx) emitLoadArray(ref SSARef, inst *SSAInst) {
	// Table-type results: load NaN-boxed value from Mixed array, store to memory slot.
	// LOAD_FIELD/STORE_FIELD will read the table from memory.
	if inst.Type == SSATypeTable {
		ec.emitLoadArrayTable(inst)
		return
	}
	// Fall back to call-exit for other non-scalar result types (string, nil, unknown).
	if inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool {
		ec.emitCallExit(inst)
		return
	}

	asm := ec.asm
	seq := ec.arraySeq
	ec.arraySeq++

	// Unique labels for this instance
	lMixed := "la_mixed_" + itoa(seq)
	lInt := "la_int_" + itoa(seq)
	lFloat := "la_float_" + itoa(seq)
	lBool := "la_bool_" + itoa(seq)
	lDone := "la_done_" + itoa(seq)

	// Set ExitPC for any guard failure
	asm.LoadImm64(X9, int64(inst.PC))

	// 1. Resolve table slot from Arg1
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		// Can't resolve table → side-exit
		asm.B("side_exit_setup")
		return
	}

	// 2. Load table NaN-boxed value. If the table source is a LOAD_GLOBAL,
	// load from the trace constant pool (regConsts) to avoid slot conflicts
	// with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// 3. Check no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// 4. Resolve key (integer index) into X3
	keyReg := ec.resolveIntRef(inst.Arg2, X3)
	if keyReg != X3 {
		asm.MOVreg(X3, keyReg)
	}
	// X3 = integer key (0-indexed)

	// 5. Load arrayKind and dispatch
	asm.LDRB(X4, X0, TableOffArrayKind)

	asm.CMPimm(X4, AKMixed)
	asm.BCond(CondEQ, lMixed)
	asm.CMPimm(X4, AKInt)
	asm.BCond(CondEQ, lInt)
	asm.CMPimm(X4, AKFloat)
	asm.BCond(CondEQ, lFloat)
	asm.CMPimm(X4, AKBool)
	asm.BCond(CondEQ, lBool)
	// Unknown arrayKind → side-exit
	asm.B("side_exit_setup")

	// --- ArrayMixed: array []Value at TableOffArray ---
	asm.Label(lMixed)
	asm.LDR(X5, X0, TableOffArray)   // X5 = array data ptr
	asm.LDR(X6, X0, TableOffArray+8) // X6 = array len
	asm.CMPreg(X3, X6)               // key < len? (unsigned)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3) // X7 = array[key] (8-byte NaN-boxed Value, LSL #3)
	asm.B(lDone)

	// --- ArrayInt: intArray []int64 at TableOffIntArray ---
	asm.Label(lInt)
	asm.LDR(X5, X0, TableOffIntArray)   // X5 = intArray data ptr
	asm.LDR(X6, X0, TableOffIntArray+8) // X6 = intArray len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3) // X7 = intArray[key] (raw int64)
	// Box as NaN-boxed int
	EmitBoxIntFast(asm, X7, X7, regTagInt)
	asm.B(lDone)

	// --- ArrayFloat: floatArray []float64 at TableOffFloatArray ---
	asm.Label(lFloat)
	asm.LDR(X5, X0, TableOffFloatArray)   // X5 = floatArray data ptr
	asm.LDR(X6, X0, TableOffFloatArray+8) // X6 = floatArray len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3) // X7 = floatArray[key] (raw float64 bits = NaN-boxed float)
	// Float64 bits are already NaN-boxed (identity encoding for non-tagged values)
	asm.B(lDone)

	// --- ArrayBool: boolArray []byte at TableOffBoolArray ---
	asm.Label(lBool)
	asm.LDR(X5, X0, TableOffBoolArray)   // X5 = boolArray data ptr
	asm.LDR(X6, X0, TableOffBoolArray+8) // X6 = boolArray len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// Byte load: LDRB with register offset (X5 + X3)
	asm.LDRBreg(X7, X5, X3) // X7 = boolArray[key] (0=nil, 1=false, 2=true)
	// Convert byte encoding to NaN-boxed value:
	//   0 → nil (NB_ValNil)
	//   1 → false (NB_ValFalse = NB_TagBool | 0)
	//   2 → true (NB_TagBool | 1)
	// CMP X7, #2
	asm.CMPimm(X7, 2)
	asm.BCond(CondEQ, "la_bool_true_"+itoa(seq))
	asm.CMPimm(X7, 1)
	asm.BCond(CondEQ, "la_bool_false_"+itoa(seq))
	// 0 = nil
	EmitBoxNil(asm, X7)
	asm.B(lDone)
	asm.Label("la_bool_true_" + itoa(seq))
	asm.LoadImm64(X7, nb_i64(NB_TagBool|1)) // NB_TagBool | 1 = true
	asm.B(lDone)
	asm.Label("la_bool_false_" + itoa(seq))
	asm.LoadImm64(X7, nb_i64(NB_TagBool)) // NB_TagBool | 0 = false
	// Fall through to done

	// --- Done: X7 = NaN-boxed result value ---
	asm.Label(lDone)

	// Store to destination register based on result type
	dstSlot := int(inst.Slot)
	if inst.Type == SSATypeFloat {
		if freg, ok := ec.regMap.FloatRefReg(ref); ok {
			asm.FMOVtoFP(freg, X7)
		} else if freg, ok := ec.regMap.FloatReg(dstSlot); ok {
			asm.FMOVtoFP(freg, X7)
		} else {
			asm.STR(X7, regRegs, dstSlot*ValueSize)
		}
	} else if inst.Type == SSATypeInt {
		EmitUnboxInt(asm, X7, X7)
		if reg, ok := ec.regMap.IntReg(dstSlot); ok {
			asm.MOVreg(reg, X7)
		} else {
			EmitBoxIntFast(asm, X7, X7, regTagInt)
			asm.STR(X7, regRegs, dstSlot*ValueSize)
		}
	} else if inst.Type == SSATypeBool {
		// Bool result: store NaN-boxed value to memory
		// The trace loop will use GUARD_TRUTHY to test it.
		asm.STR(X7, regRegs, dstSlot*ValueSize)
	} else {
		// Unknown type — store raw NaN-boxed value to memory
		asm.STR(X7, regRegs, dstSlot*ValueSize)
	}
}

// emitLoadArrayTable handles LOAD_ARRAY when the result type is SSATypeTable.
// The source table always uses ArrayMixed for table-valued elements.
// Loads the NaN-boxed table pointer from the Mixed array and stores it to the
// destination slot in memory (tables are never kept in registers).
func (ec *emitCtx) emitLoadArrayTable(inst *SSAInst) {
	asm := ec.asm

	// Set ExitPC for any guard failure
	asm.LoadImm64(X9, int64(inst.PC))

	// 1. Resolve table slot from Arg1
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		asm.B("side_exit_setup")
		return
	}

	// 2. Load source table NaN-boxed value. If the table source is a
	// LOAD_GLOBAL, load from the trace constant pool (regConsts) to avoid
	// slot conflicts with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// 3. Check no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// 4. Resolve key (integer index) into X3
	keyReg := ec.resolveIntRef(inst.Arg2, X3)
	if keyReg != X3 {
		asm.MOVreg(X3, keyReg)
	}

	// 5. Load from Mixed array (tables-of-tables are always ArrayMixed)
	asm.LDR(X5, X0, TableOffArray)   // X5 = array data ptr
	asm.LDR(X6, X0, TableOffArray+8) // X6 = array len
	asm.CMPreg(X3, X6)               // key < len? (unsigned)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3)           // X7 = array[key] (NaN-boxed Value, LSL #3)

	// 6. Verify the loaded value is a table
	EmitCheckIsTableFull(asm, X7, X1, X2, "side_exit_setup")

	// 7. Store the NaN-boxed table value to the destination slot in memory
	dstSlot := int(inst.Slot)
	asm.STR(X7, regRegs, dstSlot*ValueSize)
	// Clear stale float tracking: this slot now holds a table pointer.
	// Without this, store-back would write a stale FPR value to this slot,
	// corrupting the table pointer.
	delete(ec.floatSlotReg, dstSlot)
	delete(ec.floatWrittenSlots, dstSlot)
}

// emitStoreArray: table[key] = value (integer index, native codegen)
//
// SSA encoding (after builder fix): Arg1=keyRef, Arg2=valRef, Slot=table slot
// The table is loaded directly from Slot (the table's register slot).
//
// Handles all arrayKind variants with runtime dispatch.
func (ec *emitCtx) emitStoreArray(inst *SSAInst) {
	asm := ec.asm
	seq := ec.arraySeq
	ec.arraySeq++

	// Unique labels for this instance
	lMixed := "sa_mixed_" + itoa(seq)
	lInt := "sa_int_" + itoa(seq)
	lFloat := "sa_float_" + itoa(seq)
	lBool := "sa_bool_" + itoa(seq)
	lDone := "sa_done_" + itoa(seq)

	tblSlot := int(inst.Slot)

	// Set ExitPC for any guard failure
	asm.LoadImm64(X9, int64(inst.PC))

	// 1. Load table NaN-boxed value. Check if the table slot was produced
	// by a LOAD_GLOBAL — if so, load from the trace constant pool.
	loadedFromConsts := false
	for j := 0; j < len(ec.f.Insts); j++ {
		si := &ec.f.Insts[j]
		if si.Op == SSA_LOAD_GLOBAL && int(si.Slot) == tblSlot {
			asm.LDR(X0, regConsts, int(si.AuxInt)*ValueSize)
			loadedFromConsts = true
			break
		}
	}
	if !loadedFromConsts {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// 2. Check no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// 3. Resolve key (integer index) into X3
	keyReg := ec.resolveIntRef(inst.Arg1, X3)
	if keyReg != X3 {
		asm.MOVreg(X3, keyReg)
	}
	// X3 = integer key (0-indexed)

	// 4. Resolve value to store into X8
	// The value is in Arg2. We need the NaN-boxed form for ArrayMixed,
	// or the raw form for typed arrays.
	valInst := &ec.f.Insts[inst.Arg2]
	// Prepare NaN-boxed value in X8 for ArrayMixed path
	if valInst.Type == SSATypeFloat {
		freg := ec.resolveFloatRef(inst.Arg2, D0)
		asm.FMOVtoGP(X8, freg)
	} else if valInst.Type == SSATypeInt {
		reg := ec.resolveIntRef(inst.Arg2, X8)
		EmitBoxIntFast(asm, X8, reg, regTagInt)
	} else if valInst.Type == SSATypeBool {
		// For bool constants, always use the compile-time constant.
		// Never read from memory because the slot may have been overwritten
		// by a different trace's store-back (e.g., an int count variable
		// reusing the same slot on a subsequent function call).
		if valInst.Op == SSA_CONST_BOOL {
			if valInst.AuxInt != 0 {
				asm.LoadImm64(X8, nb_i64(NB_TagBool|1)) // true
			} else {
				asm.LoadImm64(X8, nb_i64(NB_TagBool)) // false
			}
		} else {
			// Non-constant bool: load from memory
			valSlot := int(valInst.Slot)
			if valSlot >= 0 {
				asm.LDR(X8, regRegs, valSlot*ValueSize)
			} else {
				asm.LoadImm64(X8, nb_i64(NB_ValNil))
			}
		}
	} else if valInst.Op == SSA_CONST_NIL {
		// Constant nil
		asm.LoadImm64(X8, nb_i64(NB_ValNil))
	} else {
		// Unknown type — load from memory
		valSlot := int(valInst.Slot)
		if valSlot >= 0 {
			asm.LDR(X8, regRegs, valSlot*ValueSize)
		} else {
			asm.LoadImm64(X8, nb_i64(NB_ValNil))
		}
	}
	// X8 = NaN-boxed value to store

	// 5. Load arrayKind and dispatch
	// Need to reload X0 (table ptr) since resolveIntRef/resolveFloatRef may have clobbered it
	asm.LDR(X0, regRegs, tblSlot*ValueSize)
	EmitExtractPtr(asm, X0, X0)
	asm.LDRB(X4, X0, TableOffArrayKind)

	asm.CMPimm(X4, AKMixed)
	asm.BCond(CondEQ, lMixed)
	asm.CMPimm(X4, AKInt)
	asm.BCond(CondEQ, lInt)
	asm.CMPimm(X4, AKFloat)
	asm.BCond(CondEQ, lFloat)
	asm.CMPimm(X4, AKBool)
	asm.BCond(CondEQ, lBool)
	asm.B("side_exit_setup")

	// --- ArrayMixed: array []Value at TableOffArray ---
	asm.Label(lMixed)
	asm.LDR(X5, X0, TableOffArray)   // X5 = array data ptr
	asm.LDR(X6, X0, TableOffArray+8) // X6 = array len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	asm.STRreg(X8, X5, X3) // array[key] = value (8-byte NaN-boxed, LSL #3)
	asm.B(lDone)

	// --- ArrayInt: intArray []int64 at TableOffIntArray ---
	asm.Label(lInt)
	asm.LDR(X5, X0, TableOffIntArray)
	asm.LDR(X6, X0, TableOffIntArray+8)
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// Need raw int64 from NaN-boxed value in X8
	EmitUnboxInt(asm, X7, X8)
	asm.STRreg(X7, X5, X3)
	asm.B(lDone)

	// --- ArrayFloat: floatArray []float64 at TableOffFloatArray ---
	asm.Label(lFloat)
	asm.LDR(X5, X0, TableOffFloatArray)
	asm.LDR(X6, X0, TableOffFloatArray+8)
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// X8 holds NaN-boxed float64 bits = raw IEEE 754 = correct for float64
	asm.STRreg(X8, X5, X3)
	asm.B(lDone)

	// --- ArrayBool: boolArray []byte at TableOffBoolArray ---
	asm.Label(lBool)
	asm.LDR(X5, X0, TableOffBoolArray)
	asm.LDR(X6, X0, TableOffBoolArray+8)
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// Convert NaN-boxed bool to byte encoding:
	//   NB_ValNil → 0, NB_TagBool|0 (false) → 1, NB_TagBool|1 (true) → 2
	// Check if it's a bool by checking tag
	asm.LSRimm(X7, X8, 48)
	asm.MOVimm16(X6, NB_TagBoolShr48)
	asm.CMPreg(X7, X6)
	asm.BCond(CondNE, "sa_bool_nil_"+itoa(seq))
	// It's a bool. Check payload bit 0: 0=false, 1=true
	asm.LoadImm64(X6, 1)
	asm.ANDreg(X7, X8, X6) // X7 = 0 (false) or 1 (true)
	asm.ADDimm(X7, X7, 1)   // X7 = 1 (false) or 2 (true)
	asm.B("sa_bool_store_" + itoa(seq))
	asm.Label("sa_bool_nil_" + itoa(seq))
	asm.MOVimm16(X7, 0) // nil → 0
	asm.Label("sa_bool_store_" + itoa(seq))
	asm.STRBreg(X7, X5, X3) // boolArray[key] = byte
	// Fall through to done

	asm.Label(lDone)
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_GLOBAL: native load from trace constant pool
// ────────────────────────────────────────────────────────────────────────────

// emitLoadGlobal loads a global variable's value from the trace constant pool.
// At recording time, the GETGLOBAL result was captured into trace.Constants[AuxInt].
// At runtime, we load the NaN-boxed value from regConsts (X27) and store it to
// the destination slot. For table-type globals (the common case for nbody/sieve),
// this replaces the expensive call-exit round-trip with a single load+store.
func (ec *emitCtx) emitLoadGlobal(ref SSARef, inst *SSAInst) {
	constIdx := int(inst.AuxInt) // index into trace constant pool
	dstSlot := int(inst.Slot)

	if dstSlot < 0 {
		return
	}

	// Bounds check: ensure constIdx is valid for the trace constant pool.
	nConsts := len(ec.f.Trace.Constants)
	if constIdx < 0 || constIdx >= nConsts {
		// Out of bounds: fall back to call-exit
		ec.emitCallExitInst(inst)
		return
	}

	asm := ec.asm

	// For table-type globals, we do NOT write to the VM register slot.
	// LOAD_FIELD and STORE_FIELD load the table pointer directly from regConsts
	// when the source is a LOAD_GLOBAL. This avoids conflicts where the same
	// slot is used for both a table pointer (LOAD_GLOBAL) and an int/float value
	// (other instructions) in the same loop iteration.
	if inst.Type == SSATypeTable {
		// Write table pointer to memory. LOAD_FIELD/STORE_FIELD read from
		// regConsts when source is LOAD_GLOBAL, but LOAD_ARRAY reads from
		// the VM register slot.
		asm.LDR(X0, regConsts, constIdx*ValueSize)
		asm.STR(X0, regRegs, dstSlot*ValueSize)
		// Clear stale float tracking: this slot now holds a table pointer.
		delete(ec.floatSlotReg, dstSlot)
		delete(ec.floatWrittenSlots, dstSlot)
		return
	}

	// For non-table globals (int, float), load from constant pool and store to
	// the VM register slot + optional register.
	asm.LDR(X0, regConsts, constIdx*ValueSize)
	asm.STR(X0, regRegs, dstSlot*ValueSize)

	if inst.Type == SSATypeFloat {
		// Float globals: load into FPR if allocated
		if freg, ok := ec.regMap.FloatRefReg(ref); ok {
			asm.FMOVtoFP(freg, X0)
			ec.floatSlotReg[dstSlot] = freg
		} else if freg, ok := ec.regMap.FloatReg(dstSlot); ok {
			asm.FMOVtoFP(freg, X0)
			ec.floatSlotReg[dstSlot] = freg
		}
	} else if inst.Type == SSATypeInt {
		// Int globals: unbox and load into GPR if allocated.
		// Clear stale float tracking: this slot now holds an int, not a float.
		// Without this, store-back would write a stale float FPR value to this
		// slot, overwriting the correct int loaded from the constant pool.
		delete(ec.floatSlotReg, dstSlot)
		delete(ec.floatWrittenSlots, dstSlot)
		if reg, ok := ec.regMap.IntReg(dstSlot); ok {
			EmitUnboxInt(asm, reg, X0)
		}
	}
	// Table and other types: value is in memory, no register allocation needed.
}

// ────────────────────────────────────────────────────────────────────────────
// TABLE_LEN
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitTableLen(ref SSARef, inst *SSAInst) {
	dstSlot := int(inst.Slot)
	if dstSlot < 0 {
		ec.emitCallExit(inst)
		return
	}

	asm := ec.asm

	// Set ExitPC for guard failures
	asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the table source. Arg1 is the SSA ref for the table.
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		asm.B("side_exit_setup")
		return
	}

	// Load table NaN-boxed value. If the table source is a LOAD_GLOBAL,
	// load from regConsts to avoid slot conflicts.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}

	// Check it's a table
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	// Extract pointer
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// Guard: no metatable (metatable could have __len metamethod)
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// Load array.len: the []Value slice header is at TableOffArray.
	// Slice layout: (ptr, len, cap) = (8, 8, 8). So len is at TableOffArray + 8.
	asm.LDR(X1, X0, TableOffArray+8) // X1 = array length (int64)

	// Store result to destination slot as NaN-boxed int.
	dst := ec.getIntDst(ref, inst, X1)
	if dst != X1 {
		asm.MOVreg(dst, X1)
	}
	ec.spillInt(ref, inst, dst)
}
