//go:build darwin && arm64

// emit_table_field.go implements ARM64 code generation for table field
// operations (OpGetField, OpSetField) in the Method JIT. These use inline
// shape-guarded access with deopt fallback when the field cache is available.

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
	tblValueID := instr.Args[0].ID

	// Shape guard dedup: if this table was already verified with the same
	// shapeID in this block, skip the type check + nil check + shape guard.
	if prevShape, ok := ec.shapeVerified[tblValueID]; ok && prevShape == shapeID {
		// Fast path: already verified. Just extract pointer and load field.
		tblReg := ec.resolveValueNB(tblValueID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)
		asm.LDR(jit.X0, jit.X1, fieldIdx*jit.ValueSize)
		ec.storeResultNB(jit.X0, instr.ID)
		return
	}

	// Load table value (NaN-boxed) into X0.
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
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

	// Record shape verification for dedup in subsequent field accesses.
	ec.shapeVerified[tblValueID] = shapeID

	// Direct field access: svals[fieldIndex].
	// svals is a Go slice: first 8 bytes = data pointer.
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)      // X1 = svals data pointer
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
	tblValueID := instr.Args[0].ID

	// Shape guard dedup: if this table was already verified with the same
	// shapeID in this block, skip the type check + nil check + shape guard.
	if prevShape, ok := ec.shapeVerified[tblValueID]; ok && prevShape == shapeID {
		// Fast path: shape already verified. Load value, extract ptr, store.
		valReg := ec.resolveValueNB(instr.Args[1].ID, jit.X3)
		if valReg != jit.X3 {
			asm.MOVreg(jit.X3, valReg)
		}
		tblReg := ec.resolveValueNB(tblValueID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)
		asm.STR(jit.X3, jit.X1, fieldIdx*jit.ValueSize)
		return
	}

	// Load value to store into X3 first (before we use X0 for the table).
	valReg := ec.resolveValueNB(instr.Args[1].ID, jit.X3)
	if valReg != jit.X3 {
		asm.MOVreg(jit.X3, valReg)
	}

	// Load table value (NaN-boxed) into X0.
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
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

	// Record shape verification for dedup in subsequent field accesses.
	ec.shapeVerified[tblValueID] = shapeID

	// Direct field store: svals[fieldIndex] = value.
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals)      // X1 = svals data pointer
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
	ec.storeResultNB(jit.X0, instr.ID)

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
