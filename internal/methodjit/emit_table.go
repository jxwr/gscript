//go:build darwin && arm64

// emit_table.go implements ARM64 code generation for table operations in the
// Method JIT. Handles:
//
//   - OpGetField: inline shape-guarded field access with deopt fallback
//   - OpSetField: inline shape-guarded field store with deopt fallback
//   - OpNewTable: call-exit to Go helper (table allocation is complex)
//   - OpGetTable/OpSetTable: call-exit for dynamic key access
//
// The inline field access pattern (OpGetField/OpSetField) uses the table's
// shapeID to validate that the field layout hasn't changed since the field
// cache was populated. When the shapeID matches, the field index is known
// at compile time, enabling direct svals[fieldIndex] access in ~6 ARM64
// instructions instead of a full Go function call.
//
// Shape guard failure falls back to a table-exit (ExitCode=5), which
// performs the operation in Go and resumes the JIT.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// emitGetField emits ARM64 code for OpGetField (table field read).
//
// If field cache info is available (Aux2 != 0), emits inline shape-guarded
// access: extract table pointer, check shapeID, load svals[fieldIndex].
// On shape guard failure, falls back to table-exit.
//
// If no field cache (Aux2 == 0), emits table-exit immediately (the
// interpreter will populate the cache for next compilation).
//
// Instr layout:
//   - Args[0] = table value (NaN-boxed)
//   - Aux = constant pool index for field name
//   - Aux2 = (shapeID << 32) | fieldIndex  (0 if no cache)
func (ec *emitContext) emitGetField(instr *Instr) {
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))

	// No field cache or invalid: use table-exit fallback.
	if shapeID == 0 || instr.Aux2 == 0 {
		ec.emitGetFieldExit(instr)
		return
	}

	asm := ec.asm

	// Load table value (NaN-boxed) into X0.
	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}

	// Deopt label for shape guard failure.
	deoptLabel := ec.uniqueLabel("getfield_deopt")

	// Check it's a table pointer (tag = 0xFFFF, sub = 0).
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)

	// Extract raw *Table pointer (44-bit payload).
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)

	// Shape guard: load table.shapeID (uint32 at TableOffShapeID), compare.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	asm.LoadImm64(jit.X2, int64(shapeID))
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, deoptLabel)

	// Direct field access: svals[fieldIndex].
	// svals is a Go slice: first 8 bytes = data pointer.
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize) // X0 = svals[fieldIndex]

	// Store NaN-boxed result.
	ec.storeResultNB(jit.X0, instr.ID)

	// Skip the deopt fallback.
	doneLabel := ec.uniqueLabel("getfield_done")
	asm.B(doneLabel)

	// Deopt fallback: use table-exit to perform the field access in Go.
	asm.Label(deoptLabel)
	// Save rawIntRegs before deopt path emission (see emitGetTableNative).
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitGetFieldExit(instr)
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
}

// emitSetField emits ARM64 code for OpSetField (table field write).
//
// If field cache info is available (Aux2 != 0), emits inline shape-guarded
// store: extract table pointer, check shapeID, store to svals[fieldIndex].
// On shape guard failure, falls back to table-exit.
//
// Instr layout:
//   - Args[0] = table value (NaN-boxed)
//   - Args[1] = value to store (NaN-boxed)
//   - Aux = constant pool index for field name
//   - Aux2 = (shapeID << 32) | fieldIndex  (0 if no cache)
func (ec *emitContext) emitSetField(instr *Instr) {
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))

	// No field cache or invalid: use table-exit fallback.
	if shapeID == 0 || instr.Aux2 == 0 {
		ec.emitSetFieldExit(instr)
		return
	}

	asm := ec.asm

	// Load value to store into X3 first (before we use X0 for the table).
	valReg := ec.resolveValueNB(instr.Args[1].ID, jit.X3)
	if valReg != jit.X3 {
		asm.MOVreg(jit.X3, valReg)
	}

	// Load table value (NaN-boxed) into X0.
	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}

	// Deopt label for shape guard failure.
	deoptLabel := ec.uniqueLabel("setfield_deopt")

	// Check it's a table pointer.
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)

	// Extract raw *Table pointer.
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)

	// Shape guard: load table.shapeID, compare.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	asm.LoadImm64(jit.X2, int64(shapeID))
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, deoptLabel)

	// Direct field store: svals[fieldIndex] = value.
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	asm.STR(jit.X3, jit.X1, fieldIdx*jit.ValueSize) // svals[fieldIndex] = value

	// Skip the deopt fallback.
	doneLabel := ec.uniqueLabel("setfield_done")
	asm.B(doneLabel)

	// Deopt fallback: use table-exit.
	asm.Label(deoptLabel)
	// Save rawIntRegs before deopt path emission (see emitGetTableNative).
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitSetFieldExit(instr)
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
}

// emitGetFieldExit emits a table-exit for OpGetField when no inline cache
// is available or when the shape guard fails. Stores table and field info
// to ExecContext, exits to Go, and resumes after the operation completes.
func (ec *emitContext) emitGetFieldExit(instr *Instr) {
	asm := ec.asm

	// We need the table value in a register slot so Go can read it.
	// Store the table arg to its home slot (it may only be in a register).
	if len(instr.Args) > 0 {
		tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		tblSlot, hasTblSlot := ec.slotMap[instr.Args[0].ID]
		if hasTblSlot {
			asm.STR(jit.X0, mRegRegs, slotOffset(tblSlot))
		}
	}

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	tblSlot := 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}

	// Store all active register-resident values to memory.
	ec.emitStoreAllActiveRegs()

	// Write table-exit descriptor.
	asm.LoadImm64(jit.X0, int64(TableOpGetField))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(instr.Aux)) // constant pool index
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label: resume entry jumps here.
	continueLabel := fmt.Sprintf("table_continue_%d", instr.ID)
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Load result from register file.
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// emitSetFieldExit emits a table-exit for OpSetField when no inline cache
// is available or when the shape guard fails.
func (ec *emitContext) emitSetFieldExit(instr *Instr) {
	asm := ec.asm

	// Store the table arg to its home slot.
	if len(instr.Args) > 0 {
		tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		tblSlot, hasTblSlot := ec.slotMap[instr.Args[0].ID]
		if hasTblSlot {
			asm.STR(jit.X0, mRegRegs, slotOffset(tblSlot))
		}
	}

	// Store the value arg to a temp slot.
	valSlot := ec.nextSlot
	ec.nextSlot++
	if len(instr.Args) > 1 {
		valReg := ec.resolveValueNB(instr.Args[1].ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(valSlot))
	}

	tblSlot := 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}

	// Store all active register-resident values to memory.
	ec.emitStoreAllActiveRegs()

	// Write table-exit descriptor.
	asm.LoadImm64(jit.X0, int64(TableOpSetField))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(instr.Aux)) // constant pool index
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(valSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label: resume entry jumps here.
	continueLabel := fmt.Sprintf("table_continue_%d", instr.ID)
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// emitNewTableExit emits a table-exit for OpNewTable. Table allocation is
// complex (Go heap, slice allocation), so always exits to Go.
//
// Instr layout:
//   - Aux = array hint
//   - Aux2 = hash hint
func (ec *emitContext) emitNewTableExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	// Store all active register-resident values to memory.
	ec.emitStoreAllActiveRegs()

	// Write table-exit descriptor.
	asm.LoadImm64(jit.X0, int64(TableOpNewTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, instr.Aux) // array hint
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, instr.Aux2) // hash hint
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label.
	continueLabel := fmt.Sprintf("table_continue_%d", instr.ID)
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Load result (the new table NaN-boxed value) from register file.
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// emitGetTableNative emits a native ARM64 fast path for OpGetTable with
// deopt fallback to exit-resume. The fast path handles integer keys with
// bounds-checked access to the table's array part (both Mixed and Int kinds).
// Non-integer keys, tables with metatables, and out-of-bounds access fall
// through to the exit-resume slow path.
//
// Instr layout:
//   - Args[0] = table value (NaN-boxed)
//   - Args[1] = key value (NaN-boxed)
func (ec *emitContext) emitGetTableNative(instr *Instr) {
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("gettable_deopt")
	doneLabel := ec.uniqueLabel("gettable_done")
	intArrayLabel := ec.uniqueLabel("gettable_intarr")
	boolArrayLabel := ec.uniqueLabel("gettable_boolarr")
	floatArrayLabel := ec.uniqueLabel("gettable_floatarr")

	// Load table value (NaN-boxed) into X0.
	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}

	// Check table pointer (tag=0xFFFF, sub=0).
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)

	// Extract raw *Table pointer (44-bit payload).
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)

	// Check metatable is nil (tables with metatables need __index).
	asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X1, deoptLabel)

	// Load key into X1 with type-specialized fast paths.
	keyID := instr.Args[1].ID

	if ec.hasReg(keyID) && ec.rawIntRegs[keyID] {
		// Fast path 1: key is raw int in a register (rawIntRegs).
		reg := ec.physReg(keyID)
		if reg != jit.X1 {
			asm.MOVreg(jit.X1, reg)
		}
		// Key is already a raw int64 — skip boxing, tag check, and unbox.
	} else if ec.irTypes[keyID] == TypeInt {
		// Fast path 2: key is known TypeInt but NaN-boxed — skip tag check, just unbox.
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.SBFX(jit.X1, jit.X1, 0, 48)
	} else {
		// Slow path: full NaN-boxed key with tag check.
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.LSRimm(jit.X2, jit.X1, 48)
		asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		asm.CMPreg(jit.X2, jit.X3)
		asm.BCond(jit.CondNE, deoptLabel)
		asm.SBFX(jit.X1, jit.X1, 0, 48)
	}

	// Check key >= 0 (shared by all paths).
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, deoptLabel)

	// Dispatch on arrayKind: 0=Mixed, 1=Int, 2=Float, 3=Bool, else=slow.
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondEQ, boolArrayLabel)
	asm.CMPimm(jit.X2, jit.AKFloat)
	asm.BCond(jit.CondEQ, floatArrayLabel)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondEQ, intArrayLabel)
	asm.CBNZ(jit.X2, deoptLabel) // not Mixed(0) -> deopt

	// --- ArrayMixed fast path ---
	asm.LDR(jit.X2, jit.X0, jit.TableOffArrayLen) // array.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArray) // array data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)         // value = array[key]
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArrayLen) // intArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray) // intArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)            // raw int64 = intArray[key]
	// NaN-box the int64: UBFX + ORR with pinned tag register.
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	// --- ArrayFloat fast path ---
	asm.Label(floatArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArrayLen) // floatArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // floatArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)              // raw float64 bits = floatArray[key]
	// Float64 bits ARE the NaN-boxed value — no conversion needed!
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	// --- ArrayBool fast path ---
	asm.Label(boolArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArrayLen) // boolArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray) // boolArray data pointer
	asm.LDRBreg(jit.X3, jit.X2, jit.X1)            // byte = boolArray[key]
	// Convert byte to NaN-boxed value: 0=nil, 1=false, 2=true
	nilLabel := ec.uniqueLabel("gettable_bool_nil")
	falseLabel := ec.uniqueLabel("gettable_bool_false")
	asm.CBZ(jit.X3, nilLabel)         // byte == 0 → nil
	asm.CMPimm(jit.X3, 1)
	asm.BCond(jit.CondEQ, falseLabel) // byte == 1 → false
	// byte == 2 → true: NaN-boxed true = 0xFFFD000000000001
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)
	asm.Label(falseLabel)
	// NaN-boxed false = 0xFFFD000000000000
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool))
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)
	asm.Label(nilLabel)
	// NaN-boxed nil = 0xFFFC000000000000
	asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Deopt: fall back to exit-resume.
	asm.Label(deoptLabel)
	// Save rawIntRegs before deopt path emission — emitGetTableExit calls
	// emitReloadAllActiveRegs which clears rawIntRegs entries. We need to
	// restore them AND emit unbox instructions on the slow path so that
	// registers are in raw-int form (matching the fast path) when execution
	// reaches doneLabel.
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitGetTableExit(instr)
	// After the exit-resume reload, registers hold NaN-boxed values.
	// Unbox any that were raw-int so both paths converge with raw ints.
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
}

// emitGetTableExit emits a table-exit for OpGetTable (dynamic key access).
//
// Instr layout:
//   - Args[0] = table value
//   - Args[1] = key value
func (ec *emitContext) emitGetTableExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	// Store table arg to its home slot.
	if len(instr.Args) > 0 {
		tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	// Store key arg to its home slot.
	if len(instr.Args) > 1 {
		keyReg := ec.resolveValueNB(instr.Args[1].ID, jit.X0)
		if keyReg != jit.X0 {
			asm.MOVreg(jit.X0, keyReg)
		}
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot := 0
	keySlot := 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > 1 {
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			keySlot = s
		}
	}

	// Store all active register-resident values to memory.
	ec.emitStoreAllActiveRegs()

	// Write table-exit descriptor.
	asm.LoadImm64(jit.X0, int64(TableOpGetTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(keySlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(resultSlot)) // result slot in Aux
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label.
	continueLabel := fmt.Sprintf("table_continue_%d", instr.ID)
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Load result from register file.
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// emitSetTableNative emits a native ARM64 fast path for OpSetTable with
// deopt fallback to exit-resume. The fast path handles integer keys with
// bounds-checked store to the table's array part (both Mixed and Int kinds).
// Non-integer keys, tables with metatables, and out-of-bounds access fall
// through to the exit-resume slow path.
//
// Instr layout:
//   - Args[0] = table value (NaN-boxed)
//   - Args[1] = key value (NaN-boxed)
//   - Args[2] = value to store (NaN-boxed)
func (ec *emitContext) emitSetTableNative(instr *Instr) {
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("settable_deopt")
	doneLabel := ec.uniqueLabel("settable_done")
	intArrayLabel := ec.uniqueLabel("settable_intarr")
	boolArrayLabel := ec.uniqueLabel("settable_boolarr")
	floatArrayLabel := ec.uniqueLabel("settable_floatarr")

	// Load table value (NaN-boxed) into X0.
	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}

	// Check table pointer (tag=0xFFFF, sub=0).
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)

	// Extract raw *Table pointer.
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)

	// Check metatable is nil (tables with metatables need __newindex).
	asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X1, deoptLabel)

	// Load key into X1 with type-specialized fast paths.
	keyID := instr.Args[1].ID

	if ec.hasReg(keyID) && ec.rawIntRegs[keyID] {
		// Fast path 1: key is raw int in a register (rawIntRegs).
		reg := ec.physReg(keyID)
		if reg != jit.X1 {
			asm.MOVreg(jit.X1, reg)
		}
		// Key is already a raw int64 — skip boxing, tag check, and unbox.
	} else if ec.irTypes[keyID] == TypeInt {
		// Fast path 2: key is known TypeInt but NaN-boxed — skip tag check, just unbox.
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.SBFX(jit.X1, jit.X1, 0, 48)
	} else {
		// Slow path: full NaN-boxed key with tag check.
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.LSRimm(jit.X2, jit.X1, 48)
		asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		asm.CMPreg(jit.X2, jit.X3)
		asm.BCond(jit.CondNE, deoptLabel)
		asm.SBFX(jit.X1, jit.X1, 0, 48)
	}

	// Check key >= 0 (shared by all paths).
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, deoptLabel)

	// Dispatch on arrayKind: 0=Mixed, 1=Int, 2=Float, 3=Bool, else=slow.
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondEQ, boolArrayLabel)
	asm.CMPimm(jit.X2, jit.AKFloat)
	asm.BCond(jit.CondEQ, floatArrayLabel)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondEQ, intArrayLabel)
	asm.CBNZ(jit.X2, deoptLabel) // not Mixed(0) -> deopt

	// --- ArrayMixed fast path ---
	asm.LDR(jit.X2, jit.X0, jit.TableOffArrayLen) // array.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	// Load value to store into X4.
	valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
	if valReg != jit.X4 {
		asm.MOVreg(jit.X4, valReg)
	}
	asm.LDR(jit.X2, jit.X0, jit.TableOffArray) // array data pointer
	asm.STRreg(jit.X4, jit.X2, jit.X1)          // array[key] = value
	// Set keysDirty flag.
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArrayLen) // intArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	// Load value to store and check it's an integer.
	valReg2 := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
	if valReg2 != jit.X4 {
		asm.MOVreg(jit.X4, valReg2)
	}
	asm.LSRimm(jit.X5, jit.X4, 48)
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondNE, deoptLabel) // value not int -> deopt
	// Unbox int64 from NaN-boxed value.
	asm.SBFX(jit.X4, jit.X4, 0, 48)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray) // intArray data pointer
	asm.STRreg(jit.X4, jit.X2, jit.X1)             // intArray[key] = int64
	// Set keysDirty flag.
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// --- ArrayFloat fast path ---
	asm.Label(floatArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArrayLen) // floatArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	// Load value to store.
	valRegFloat := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
	if valRegFloat != jit.X4 {
		asm.MOVreg(jit.X4, valRegFloat)
	}
	// Check value is a float (NOT tagged — bits 50-62 NOT all set).
	// Tagged values have (val >> 50) == 0x3FFF. Floats don't.
	jit.EmitIsTagged(asm, jit.X4, jit.X5) // sets flags: EQ = tagged, NE = float
	asm.BCond(jit.CondEQ, deoptLabel)      // tagged (int/bool/nil/ptr) → deopt
	// Float64 bits ARE the NaN-boxed representation — store directly.
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // floatArray data pointer
	asm.STRreg(jit.X4, jit.X2, jit.X1)              // floatArray[key] = float64
	// Set keysDirty flag.
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// --- ArrayBool fast path ---
	asm.Label(boolArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArrayLen) // boolArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	// Load value to store.
	valRegBool := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
	if valRegBool != jit.X4 {
		asm.MOVreg(jit.X4, valRegBool)
	}
	// Check value type: must be bool (tag=0xFFFD) or nil (0xFFFC).
	asm.LSRimm(jit.X5, jit.X4, 48)
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagBoolShr48))
	asm.CMPreg(jit.X5, jit.X6)
	boolOkLabel := ec.uniqueLabel("settable_bool_isbool")
	asm.BCond(jit.CondEQ, boolOkLabel)
	// Check if nil.
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagNilShr48))
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondNE, deoptLabel) // not bool, not nil → deopt
	// Nil → byte 0.
	asm.MOVimm16(jit.X4, 0)
	setByteLabel := ec.uniqueLabel("settable_bool_store")
	asm.B(setByteLabel)
	asm.Label(boolOkLabel)
	// Bool: extract payload bit 0. false=0xFFFD000000000000 (payload=0) → byte 1
	//                                true=0xFFFD000000000001 (payload=1) → byte 2
	// Conversion: byte = payload + 1
	asm.LoadImm64(jit.X5, 1)
	asm.ANDreg(jit.X4, jit.X4, jit.X5) // extract bit 0 (payload)
	asm.ADDimm(jit.X4, jit.X4, 1)      // 0→1 (false), 1→2 (true)
	asm.Label(setByteLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray) // boolArray data pointer
	asm.STRBreg(jit.X4, jit.X2, jit.X1)            // boolArray[key] = byte
	// Set keysDirty flag.
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// Deopt: fall back to exit-resume.
	asm.Label(deoptLabel)
	// Save rawIntRegs before deopt path emission (see emitGetTableNative).
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitSetTableExit(instr)
	// After the exit-resume reload, registers hold NaN-boxed values.
	// Unbox any that were raw-int so both paths converge with raw ints.
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
}

// emitSetTableExit emits a table-exit for OpSetTable (dynamic key access).
//
// Instr layout:
//   - Args[0] = table value
//   - Args[1] = key value
//   - Args[2] = value to store
func (ec *emitContext) emitSetTableExit(instr *Instr) {
	asm := ec.asm

	// Store table arg to its home slot.
	if len(instr.Args) > 0 {
		tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	// Store key arg to its home slot.
	if len(instr.Args) > 1 {
		keyReg := ec.resolveValueNB(instr.Args[1].ID, jit.X0)
		if keyReg != jit.X0 {
			asm.MOVreg(jit.X0, keyReg)
		}
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	// Store value arg to its home slot.
	if len(instr.Args) > 2 {
		valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		if s, ok := ec.slotMap[instr.Args[2].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot, keySlot, valSlot := 0, 0, 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > 1 {
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			keySlot = s
		}
	}
	if len(instr.Args) > 2 {
		if s, ok := ec.slotMap[instr.Args[2].ID]; ok {
			valSlot = s
		}
	}

	// Store all active register-resident values to memory.
	ec.emitStoreAllActiveRegs()

	// Write table-exit descriptor.
	asm.LoadImm64(jit.X0, int64(TableOpSetTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(keySlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(valSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label.
	continueLabel := fmt.Sprintf("table_continue_%d", instr.ID)
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}
