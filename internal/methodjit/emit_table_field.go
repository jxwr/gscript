//go:build darwin && arm64

// emit_table_field.go implements ARM64 code generation for table field
// operations (OpGetField, OpSetField) in the Method JIT. These use inline
// shape-guarded access with deopt fallback when the field cache is available.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) hasFieldSvalsCache(tblValueID int, shapeID uint32) bool {
	return ec.fieldSvalsCacheValid &&
		ec.fieldSvalsCacheTableID == tblValueID &&
		ec.fieldSvalsCacheShapeID == shapeID
}

func (ec *emitContext) rememberFieldSvalsCache(tblValueID int, shapeID uint32) {
	if shapeID == 0 {
		ec.invalidateFieldSvalsCache()
		return
	}
	ec.fieldSvalsCacheValid = true
	ec.fieldSvalsCacheTableID = tblValueID
	ec.fieldSvalsCacheShapeID = shapeID
}

func (ec *emitContext) invalidateFieldSvalsCache() {
	ec.fieldSvalsCacheValid = false
	ec.fieldSvalsCacheTableID = 0
	ec.fieldSvalsCacheShapeID = 0
}

// emitPrepareFieldTablePtr leaves the raw *Table pointer in X0 and returns
// true when the field shape was already verified in this block. TypeTable
// producers, such as TableArrayLoad, have already proved the NaN-boxed value is
// a non-string table pointer, so the first field access can skip the full tag
// and pointer-subtype check and go straight to the shape guard.
func (ec *emitContext) emitPrepareFieldTablePtr(tblValueID int, shapeID uint32, deoptLabel string) bool {
	asm := ec.asm
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	if prevShape, ok := ec.shapeVerified[tblValueID]; ok && prevShape == shapeID {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		return true
	}
	if ec.irTypes[tblValueID] != TypeTable {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
	}
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	asm.LoadImm64(jit.X2, int64(shapeID))
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, deoptLabel)
	ec.shapeVerified[tblValueID] = shapeID
	return false
}

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
		if ec.emitGetFieldDynamicCache(instr) {
			return
		}
		ec.invalidateFieldSvalsCache()
		ec.emitGetFieldExit(instr)
		return
	}

	asm := ec.asm
	tblValueID := instr.Args[0].ID

	typeDeoptLabel := ec.uniqueLabel("getfield_type_deopt")
	doneLabel := ec.uniqueLabel("getfield_done")
	deoptLabel := ec.uniqueLabel("getfield_deopt")
	if ec.hasFieldSvalsCache(tblValueID, shapeID) {
		asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize)
		if instr.Type == TypeFloat {
			ec.emitStoreTypedFieldLoad(instr, jit.X0, typeDeoptLabel)
			asm.B(doneLabel)
			asm.Label(typeDeoptLabel)
			ec.emitDeopt(instr)
			asm.Label(doneLabel)
			return
		}
		ec.emitStoreTypedFieldLoad(instr, jit.X0, "")
		return
	}

	shapeWasVerified := ec.emitPrepareFieldTablePtr(tblValueID, shapeID, deoptLabel)
	if shapeWasVerified {
		asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)
		asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize)
		if instr.Type == TypeFloat {
			ec.emitStoreTypedFieldLoad(instr, jit.X0, typeDeoptLabel)
			ec.rememberFieldSvalsCache(tblValueID, shapeID)
			asm.B(doneLabel)
			asm.Label(typeDeoptLabel)
			ec.emitDeopt(instr)
			asm.Label(doneLabel)
			return
		}
		ec.emitStoreTypedFieldLoad(instr, jit.X0, "")
		ec.rememberFieldSvalsCache(tblValueID, shapeID)
		return
	}

	// Direct field access: svals[fieldIndex].
	// svals is a Go slice: first 8 bytes = data pointer.
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)      // X1 = svals data pointer
	asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize) // X0 = svals[fieldIndex]

	ec.emitStoreTypedFieldLoad(instr, jit.X0, typeDeoptLabel)
	ec.invalidateFieldSvalsCache()

	// Skip the deopt fallback.
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

	if instr.Type == TypeFloat {
		asm.Label(typeDeoptLabel)
		ec.emitDeopt(instr)
	}

	asm.Label(doneLabel)
}

func (ec *emitContext) emitGetFieldDynamicCache(instr *Instr) bool {
	if instr == nil || instr.SourcePC < 0 || len(instr.Args) == 0 {
		return false
	}
	asm := ec.asm
	tblValueID := instr.Args[0].ID
	typeDeoptLabel := ec.uniqueLabel("getfield_dyn_type_deopt")
	deoptLabel := ec.uniqueLabel("getfield_dyn_deopt")
	doneLabel := ec.uniqueLabel("getfield_dyn_done")

	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineFieldCache)
	asm.CBZ(jit.X3, deoptLabel)
	entryOff := instr.SourcePC * jit.FieldCacheEntrySize
	if entryOff <= 4095 {
		asm.ADDimm(jit.X3, jit.X3, uint16(entryOff))
	} else {
		asm.LoadImm64(jit.X4, int64(entryOff))
		asm.ADDreg(jit.X3, jit.X3, jit.X4)
	}
	asm.LDRW(jit.X5, jit.X3, jit.FieldCacheEntryOffShapeID)
	asm.CBZ(jit.X5, deoptLabel)
	asm.LDR(jit.X4, jit.X3, jit.FieldCacheEntryOffFieldIdx)
	asm.CMPimm(jit.X4, 0)
	asm.BCond(jit.CondLT, deoptLabel)

	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	if ec.irTypes[tblValueID] != TypeTable {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
	}
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	asm.CMPreg(jit.X1, jit.X5)
	asm.BCond(jit.CondNE, deoptLabel)
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)
	asm.LDRreg(jit.X0, jit.X1, jit.X4)
	ec.emitStoreTypedFieldLoad(instr, jit.X0, typeDeoptLabel)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitGetFieldExit(instr)
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	if instr.Type == TypeFloat {
		asm.Label(typeDeoptLabel)
		ec.emitDeopt(instr)
	}

	asm.Label(doneLabel)
	return true
}

func (ec *emitContext) emitGetFieldNumToFloat(instr *Instr) {
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))

	// No field cache or invalid: use table-exit fallback. The resume path
	// applies the same int-or-float conversion as the inline fast path.
	if shapeID == 0 || instr.Aux2 == 0 {
		ec.invalidateFieldSvalsCache()
		ec.emitGetFieldExit(instr)
		return
	}

	asm := ec.asm
	tblValueID := instr.Args[0].ID
	typeDeoptLabel := ec.uniqueLabel("getfield_num_deopt")
	doneLabel := ec.uniqueLabel("getfield_num_done")
	deoptLabel := ec.uniqueLabel("getfield_num_shape_deopt")
	if ec.hasFieldSvalsCache(tblValueID, shapeID) {
		asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize)
		ec.emitStoreNumericFieldLoad(instr, jit.X0, typeDeoptLabel)
		asm.B(doneLabel)
		asm.Label(typeDeoptLabel)
		ec.emitDeopt(instr)
		asm.Label(doneLabel)
		return
	}

	shapeWasVerified := ec.emitPrepareFieldTablePtr(tblValueID, shapeID, deoptLabel)
	if shapeWasVerified {
		asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)
		asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize)
		ec.emitStoreNumericFieldLoad(instr, jit.X0, typeDeoptLabel)
		ec.rememberFieldSvalsCache(tblValueID, shapeID)
		asm.B(doneLabel)
		asm.Label(typeDeoptLabel)
		ec.emitDeopt(instr)
		asm.Label(doneLabel)
		return
	}

	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)
	asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize)
	ec.emitStoreNumericFieldLoad(instr, jit.X0, typeDeoptLabel)
	ec.invalidateFieldSvalsCache()

	asm.B(doneLabel)

	asm.Label(deoptLabel)
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitGetFieldExit(instr)
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs
	asm.B(doneLabel)

	asm.Label(typeDeoptLabel)
	ec.emitDeopt(instr)

	asm.Label(doneLabel)
}

func (ec *emitContext) emitStoreTypedFieldLoad(instr *Instr, valReg jit.Reg, typeDeoptLabel string) {
	if instr.Type == TypeFloat {
		ec.asm.LSRimm(jit.X2, valReg, 48)
		ec.asm.MOVimm16(jit.X3, jit.NB_TagNilShr48)
		ec.asm.CMPreg(jit.X2, jit.X3)
		ec.asm.BCond(jit.CondGE, typeDeoptLabel)
		ec.asm.FMOVtoFP(jit.D0, valReg)
		ec.storeRawFloat(jit.D0, instr.ID)
		return
	}
	ec.storeResultNB(valReg, instr.ID)
}

func (ec *emitContext) emitStoreNumericFieldLoad(instr *Instr, valReg jit.Reg, deoptLabel string) {
	asm := ec.asm
	intLabel := ec.uniqueLabel("field_num_int")
	storeLabel := ec.uniqueLabel("field_num_store")

	asm.LSRimm(jit.X2, valReg, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagNilShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondGE, intLabel)
	asm.FMOVtoFP(jit.D0, valReg)
	asm.B(storeLabel)

	asm.Label(intLabel)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, deoptLabel)
	if valReg != jit.X0 {
		asm.MOVreg(jit.X0, valReg)
	}
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.SCVTF(jit.D0, jit.X0)

	asm.Label(storeLabel)
	ec.storeRawFloat(jit.D0, instr.ID)
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
		ec.invalidateFieldSvalsCache()
		ec.emitSetFieldExit(instr)
		return
	}

	asm := ec.asm
	tblValueID := instr.Args[0].ID
	valueID := instr.Args[1].ID

	deoptLabel := ec.uniqueLabel("setfield_deopt")
	valStore := ec.prepareFieldStoreValue(valueID)
	if !valStore.isFPR {
		// Load boxed values into X3 first, before table preparation uses
		// X0-X2. X3 is scratch but not touched by emitPrepareFieldTablePtr.
		valReg := ec.resolveValueNB(valueID, jit.X3)
		if valReg != jit.X3 {
			asm.MOVreg(jit.X3, valReg)
		}
		valStore.gpr = jit.X3
	}

	if ec.hasFieldSvalsCache(tblValueID, shapeID) {
		ec.emitPreparedFieldStore(valStore, fieldIdx)
		return
	}

	shapeWasVerified := ec.emitPrepareFieldTablePtr(tblValueID, shapeID, deoptLabel)

	// Direct field store: svals[fieldIndex] = value.
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	ec.emitPreparedFieldStore(valStore, fieldIdx)
	if shapeWasVerified {
		ec.rememberFieldSvalsCache(tblValueID, shapeID)
		return
	}
	ec.invalidateFieldSvalsCache()

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

type fieldStoreValue struct {
	isFPR bool
	fpr   jit.FReg
	gpr   jit.Reg
}

func (ec *emitContext) prepareFieldStoreValue(valueID int) fieldStoreValue {
	if ec.hasFPReg(valueID) {
		return fieldStoreValue{isFPR: true, fpr: ec.physFPReg(valueID)}
	}
	return fieldStoreValue{gpr: jit.X3}
}

func (ec *emitContext) emitPreparedFieldStore(val fieldStoreValue, fieldIdx int) {
	if val.isFPR {
		ec.asm.FSTRd(val.fpr, jit.X1, fieldIdx*jit.ValueSize)
		return
	}
	ec.asm.STR(val.gpr, jit.X1, fieldIdx*jit.ValueSize)
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
	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
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
	asm.LoadImm64(jit.X0, int64(instr.SourcePC))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label: resume entry jumps here.
	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Load result from register file.
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	if instr.Op == OpGetFieldNumToFloat {
		typeDeoptLabel := ec.uniqueLabel("getfield_exit_num_deopt")
		doneLabel := ec.uniqueLabel("getfield_exit_num_done")
		ec.emitStoreNumericFieldLoad(instr, jit.X0, typeDeoptLabel)
		asm.B(doneLabel)
		asm.Label(typeDeoptLabel)
		ec.emitDeopt(instr)
		asm.Label(doneLabel)
	} else if instr.Type == TypeFloat {
		typeDeoptLabel := ec.uniqueLabel("getfield_exit_type_deopt")
		doneLabel := ec.uniqueLabel("getfield_exit_typed_done")
		ec.emitStoreTypedFieldLoad(instr, jit.X0, typeDeoptLabel)
		asm.B(doneLabel)
		asm.Label(typeDeoptLabel)
		ec.emitDeopt(instr)
		asm.Label(doneLabel)
	} else {
		ec.emitStoreTypedFieldLoad(instr, jit.X0, "")
	}

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
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
	ec.recordExitResumeCheckSite(instr, ExitTableExit, nil, exitResumeCheckOptions{RequireTableInputs: true})
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
	asm.LoadImm64(jit.X0, int64(instr.SourcePC))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label: resume entry jumps here.
	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}
