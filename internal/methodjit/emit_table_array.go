//go:build darwin && arm64

// emit_table_array.go implements ARM64 code generation for table array/dynamic
// key operations (OpNewTable, OpGetTable, OpSetTable) in the Method JIT.
// These handle integer-keyed array access with type-specialized fast paths
// and exit-resume fallbacks for complex cases.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const tier2SparseArrayMax = 1024 // must match runtime.sparseArrayMax

type tableArrayBoundKey struct {
	tableID int
	keyID   int
}

// emitTypedArraySetBoundsOrAppendCheck accepts either an in-bounds typed-array
// store or a capacity-only append at key == len. The hot in-bounds case falls
// through to the caller's store block; append is emitted out-of-line by
// emitTypedArraySetAppendPath.
func emitTypedArraySetBoundsOrAppendCheck(asm *jit.Assembler, tableReg, keyReg, lenReg jit.Reg, lenOff int, appendLabel, deoptLabel string) {
	emitTypedArraySetBoundsAppendOrSparseCheck(asm, tableReg, keyReg, lenReg, lenOff, appendLabel, "", deoptLabel)
}

// emitTypedArraySetBoundsAppendOrSparseCheck is the Tier 2 variant that can
// route key > len to a typed sparse-grow path when the backing has capacity.
func emitTypedArraySetBoundsAppendOrSparseCheck(asm *jit.Assembler, tableReg, keyReg, lenReg jit.Reg, lenOff int, appendLabel, sparseLabel, deoptLabel string) {
	asm.LDR(lenReg, tableReg, lenOff)
	asm.CMPreg(keyReg, lenReg)
	asm.BCond(jit.CondEQ, appendLabel)
	if sparseLabel != "" {
		asm.BCond(jit.CondGT, sparseLabel)
	} else {
		asm.BCond(jit.CondGT, deoptLabel)
	}
}

// emitTypedArraySetBoundsOnlyCheck accepts only in-bounds stores. It is used
// for nil bool-array writes because RawSetInt clears existing bool slots but
// does not grow typed arrays for nil sparse/append writes.
func emitTypedArraySetBoundsOnlyCheck(asm *jit.Assembler, tableReg, keyReg, lenReg jit.Reg, lenOff int, deoptLabel string) {
	asm.LDR(lenReg, tableReg, lenOff)
	asm.CMPreg(keyReg, lenReg)
	asm.BCond(jit.CondGE, deoptLabel)
}

func emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm *jit.Assembler, priorLoadBounds bool, tableReg, keyReg, lenReg jit.Reg, lenOff int, appendLabel, sparseLabel, deoptLabel string) {
	if priorLoadBounds {
		asm.CBZ(jit.X17, deoptLabel)
		return
	}
	emitTypedArraySetBoundsAppendOrSparseCheck(asm, tableReg, keyReg, lenReg, lenOff, appendLabel, sparseLabel, deoptLabel)
}

func emitTypedArraySetPriorLoadOrBoundsAppendCheck(asm *jit.Assembler, priorLoadBounds bool, tableReg, keyReg, lenReg jit.Reg, lenOff int, appendLabel, deoptLabel string) {
	emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, priorLoadBounds, tableReg, keyReg, lenReg, lenOff, appendLabel, "", deoptLabel)
}

func emitTypedArraySetPriorLoadOrBoundsOnlyCheck(asm *jit.Assembler, priorLoadBounds bool, tableReg, keyReg, lenReg jit.Reg, lenOff int, deoptLabel string) {
	if priorLoadBounds {
		asm.CBZ(jit.X17, deoptLabel)
		return
	}
	emitTypedArraySetBoundsOnlyCheck(asm, tableReg, keyReg, lenReg, lenOff, deoptLabel)
}

// emitTypedArraySetAppendPath extends the typed array length for key == len
// when capacity is already available. It stays conservative: imap/hash must be
// nil so skipping RawSetInt.absorbKeys cannot change later length/iteration
// semantics.
func emitTypedArraySetAppendPath(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, appendLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetAppendPathMaybeDirty(asm, tableReg, keyReg, scratchReg, lenOff, capOff, appendLabel, deoptLabel, storeLabel, false)
}

func emitTypedArraySetAppendPathDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, appendLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetAppendPathMaybeDirty(asm, tableReg, keyReg, scratchReg, lenOff, capOff, appendLabel, deoptLabel, storeLabel, true)
}

func emitTypedArraySetAppendPathMaybeDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, appendLabel, deoptLabel, storeLabel string, markKeysDirty bool) {
	asm.Label(appendLabel)
	asm.LDR(scratchReg, tableReg, jit.TableOffImap)
	asm.CBNZ(scratchReg, deoptLabel)
	asm.LDR(scratchReg, tableReg, jit.TableOffHash)
	asm.CBNZ(scratchReg, deoptLabel)
	asm.LDR(scratchReg, tableReg, capOff)
	asm.CMPreg(keyReg, scratchReg)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.ADDimm(scratchReg, keyReg, 1)
	if markKeysDirty {
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, tableReg, jit.TableOffKeysDirty)
	}
	asm.STR(scratchReg, tableReg, lenOff)
	asm.B(storeLabel)
}

// emitTypedArraySetSparsePath handles key > len when the typed backing already
// has capacity. This mirrors RawSetInt's typed sparse-expansion path and stays
// conservative by requiring empty imap/hash because it does not run absorbKeys.
func emitTypedArraySetSparsePath(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetSparsePathMaybeDirty(asm, tableReg, keyReg, scratchReg, lenOff, capOff, sparseLabel, deoptLabel, storeLabel, false)
}

func emitTypedArraySetSparsePathDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetSparsePathMaybeDirty(asm, tableReg, keyReg, scratchReg, lenOff, capOff, sparseLabel, deoptLabel, storeLabel, true)
}

func emitTypedArraySetSparsePathMaybeDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string, markKeysDirty bool) {
	asm.Label(sparseLabel)
	asm.CMPimm(keyReg, tier2SparseArrayMax)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(scratchReg, tableReg, jit.TableOffImap)
	asm.CBNZ(scratchReg, deoptLabel)
	asm.LDR(scratchReg, tableReg, jit.TableOffHash)
	asm.CBNZ(scratchReg, deoptLabel)
	asm.LDR(scratchReg, tableReg, capOff)
	asm.CMPreg(keyReg, scratchReg)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.ADDimm(scratchReg, keyReg, 1)
	if markKeysDirty {
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, tableReg, jit.TableOffKeysDirty)
	}
	asm.STR(scratchReg, tableReg, lenOff)
	asm.B(storeLabel)
}

func fbKindToAK(kind int64) (uint16, bool) {
	switch kind {
	case int64(vm.FBKindMixed):
		return jit.AKMixed, true
	case int64(vm.FBKindInt):
		return jit.AKInt, true
	case int64(vm.FBKindFloat):
		return jit.AKFloat, true
	case int64(vm.FBKindBool):
		return jit.AKBool, true
	default:
		return 0, false
	}
}

func tableArrayOffsets(kind int64) (dataOff, lenOff int, ok bool) {
	switch kind {
	case int64(vm.FBKindMixed):
		return jit.TableOffArray, jit.TableOffArrayLen, true
	case int64(vm.FBKindInt):
		return jit.TableOffIntArray, jit.TableOffIntArrayLen, true
	case int64(vm.FBKindFloat):
		return jit.TableOffFloatArray, jit.TableOffFloatArrayLen, true
	case int64(vm.FBKindBool):
		return jit.TableOffBoolArray, jit.TableOffBoolArrayLen, true
	default:
		return 0, 0, false
	}
}

func (ec *emitContext) emitTableArrayHeader(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("tarr_header_deopt")
	doneLabel := ec.uniqueLabel("tarr_header_done")

	expectedKind, ok := fbKindToAK(instr.Aux)
	if !ok {
		ec.emitDeopt(instr)
		return
	}

	tblID := instr.Args[0].ID
	tblReg := ec.resolveValueNB(tblID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	if ec.tableVerified[tblID] {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else if ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		ec.tableVerified[tblID] = true
	} else if ec.irTypes[tblID] == TypeTable {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		// TypeTable producers/guards already exclude nil. Keep the dynamic
		// metatable and array-kind checks, but avoid repeating the nil check
		// for row tables loaded from mixed table arrays.
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, deoptLabel)
		ec.tableVerified[tblID] = true
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, deoptLabel)
		ec.tableVerified[tblID] = true
	}
	if fbKind, ok := ec.localNewTableFBKind(instr.Args[0]); ok && fbKind == uint16(instr.Aux) {
		ec.kindVerified[tblID] = fbKind
	}
	if ec.kindVerified[tblID] != uint16(instr.Aux) {
		asm.LDRB(jit.X1, jit.X0, jit.TableOffArrayKind)
		asm.CMPimm(jit.X1, expectedKind)
		asm.BCond(jit.CondNE, deoptLabel)
	}
	ec.kindVerified[tblID] = uint16(instr.Aux)
	ec.storeRawTablePtr(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	ec.emitPreciseDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableArrayLen(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	_, lenOff, ok := tableArrayOffsets(instr.Aux)
	if !ok {
		ec.emitDeopt(instr)
		return
	}
	hdr := ec.resolveRawTablePtr(instr.Args[0].ID, jit.X0)
	if hdr != jit.X0 {
		ec.asm.MOVreg(jit.X0, hdr)
	}
	ec.asm.LDR(jit.X0, jit.X0, lenOff)
	ec.storeRawInt(jit.X0, instr.ID)
}

func (ec *emitContext) emitTableArrayData(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	dataOff, _, ok := tableArrayOffsets(instr.Aux)
	if !ok {
		ec.emitDeopt(instr)
		return
	}
	hdr := ec.resolveRawTablePtr(instr.Args[0].ID, jit.X0)
	if hdr != jit.X0 {
		ec.asm.MOVreg(jit.X0, hdr)
	}
	ec.asm.LDR(jit.X0, jit.X0, dataOff)
	ec.storeRawInt(jit.X0, instr.ID)
}

func (ec *emitContext) emitTableArrayLoad(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("tarr_load_deopt")
	successLabel := ec.uniqueLabel("tarr_load_success")
	doneLabel := ec.uniqueLabel("tarr_load_done")

	dataReg := ec.resolveRawInt(instr.Args[0].ID, jit.X2)
	if dataReg != jit.X2 {
		asm.MOVreg(jit.X2, dataReg)
	}
	lenReg := ec.resolveRawInt(instr.Args[1].ID, jit.X3)
	if lenReg != jit.X3 {
		asm.MOVreg(jit.X3, lenReg)
	}
	keyID := instr.Args[2].ID
	if kv, isConst := ec.constInts[keyID]; isConst {
		asm.LoadImm64(jit.X1, kv)
	} else if ec.hasReg(keyID) && ec.valueReprOf(keyID) == valueReprRawInt {
		reg := ec.physReg(keyID)
		if reg != jit.X1 {
			asm.MOVreg(jit.X1, reg)
		}
	} else if ec.irTypes[keyID] == TypeInt {
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.SBFX(jit.X1, jit.X1, 0, 48)
	} else {
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.LSRimm(jit.X4, jit.X1, 48)
		asm.MOVimm16(jit.X5, uint16(jit.NB_TagIntShr48))
		asm.CMPreg(jit.X4, jit.X5)
		asm.BCond(jit.CondNE, deoptLabel)
		asm.SBFX(jit.X1, jit.X1, 0, 48)
	}
	if kv, isConst := ec.constInts[keyID]; (!isConst || kv < 0) && !ec.intNonNegative(keyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	if !ec.tableArrayUpperBoundSafe(instr.ID) {
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondGE, deoptLabel)
	}

	switch instr.Aux {
	case int64(vm.FBKindMixed):
		asm.LDRreg(jit.X0, jit.X2, jit.X1)
		switch instr.Type {
		case TypeInt:
			asm.LSRimm(jit.X2, jit.X0, 48)
			asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
			asm.CMPreg(jit.X2, jit.X3)
			asm.BCond(jit.CondNE, deoptLabel)
			asm.SBFX(jit.X0, jit.X0, 0, 48)
			ec.storeRawInt(jit.X0, instr.ID)
		case TypeFloat:
			jit.EmitIsTagged(asm, jit.X0, jit.X2)
			asm.BCond(jit.CondEQ, deoptLabel)
			asm.FMOVtoFP(jit.D0, jit.X0)
			ec.storeRawFloat(jit.D0, instr.ID)
		case TypeTable:
			jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
			ec.storeResultNB(jit.X0, instr.ID)
		default:
			ec.storeResultNB(jit.X0, instr.ID)
		}
	case int64(vm.FBKindInt):
		asm.LDRreg(jit.X0, jit.X2, jit.X1)
		if instr.Type == TypeInt {
			ec.storeRawInt(jit.X0, instr.ID)
		} else {
			jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
			ec.storeResultNB(jit.X0, instr.ID)
		}
	case int64(vm.FBKindFloat):
		if instr.Type == TypeFloat {
			dstF := jit.D0
			if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
				dstF = jit.FReg(pr.Reg)
			}
			asm.FLDRdReg(dstF, jit.X2, jit.X1)
			ec.storeRawFloat(dstF, instr.ID)
		} else {
			asm.LDRreg(jit.X0, jit.X2, jit.X1)
			ec.storeResultNB(jit.X0, instr.ID)
		}
	case int64(vm.FBKindBool):
		asm.LDRBreg(jit.X3, jit.X2, jit.X1)
		nilLabel := ec.uniqueLabel("tarr_bool_nil")
		falseLabel := ec.uniqueLabel("tarr_bool_false")
		asm.CBZ(jit.X3, nilLabel)
		asm.CMPimm(jit.X3, 1)
		asm.BCond(jit.CondEQ, falseLabel)
		asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
		ec.storeResultNB(jit.X0, instr.ID)
		asm.B(successLabel)
		asm.Label(falseLabel)
		asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool))
		ec.storeResultNB(jit.X0, instr.ID)
		asm.B(successLabel)
		asm.Label(nilLabel)
		asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
		ec.storeResultNB(jit.X0, instr.ID)
	default:
		ec.emitDeopt(instr)
	}
	asm.B(successLabel)

	asm.Label(successLabel)
	ec.recordTableArrayBoundedKey(instr)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	savedReprs := ec.snapshotValueReprs()
	if ec.emitTableArrayLoadExit(instr) {
		typeDeoptLabel := ec.uniqueLabel("tarr_load_exit_type_deopt")
		ec.emitCheckTableArrayLoadExitResult(instr, typeDeoptLabel)
		ec.emitUnboxRawIntRegs(savedReprs)
		ec.restoreValueReprSnapshot(savedReprs)
		asm.MOVimm16(jit.X17, 0)
		asm.B(doneLabel)
		asm.Label(typeDeoptLabel)
		ec.emitPreciseDeopt(instr)
	}
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableArrayStore(instr *Instr) {
	if len(instr.Args) < 5 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("tarr_store_deopt")
	doneLabel := ec.uniqueLabel("tarr_store_done")

	dataReg := ec.resolveRawInt(instr.Args[1].ID, jit.X2)
	if dataReg != jit.X2 {
		asm.MOVreg(jit.X2, dataReg)
	}
	lenReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if lenReg != jit.X3 {
		asm.MOVreg(jit.X3, lenReg)
	}
	if !ec.emitTableArrayKeyToReg(instr.Args[3], deoptLabel) {
		ec.emitDeopt(instr)
		return
	}
	keyID := instr.Args[3].ID
	if kv, isConst := ec.constInts[keyID]; (!isConst || kv < 0) && !ec.intNonNegative(keyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	if !ec.tableArrayUpperBoundSafe(instr.ID) {
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondGE, deoptLabel)
	}

	valueID := instr.Args[4].ID
	switch instr.Aux {
	case int64(vm.FBKindMixed):
		valReg := ec.resolveValueNB(valueID, jit.X4)
		if valReg != jit.X4 {
			asm.MOVreg(jit.X4, valReg)
		}
		asm.STRreg(jit.X4, jit.X2, jit.X1)
		tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)

	case int64(vm.FBKindInt):
		if val, ok := ec.constInts[valueID]; ok {
			asm.LoadImm64(jit.X4, val)
		} else if ec.hasReg(valueID) && ec.valueReprOf(valueID) == valueReprRawInt {
			reg := ec.physReg(valueID)
			if reg != jit.X4 {
				asm.MOVreg(jit.X4, reg)
			}
		} else if ec.irTypes[valueID] == TypeInt {
			valReg := ec.resolveValueNB(valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			asm.SBFX(jit.X4, jit.X4, 0, 48)
		} else {
			valReg := ec.resolveValueNB(valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			asm.LSRimm(jit.X5, jit.X4, 48)
			asm.MOVimm16(jit.X6, uint16(jit.NB_TagIntShr48))
			asm.CMPreg(jit.X5, jit.X6)
			asm.BCond(jit.CondNE, deoptLabel)
			asm.SBFX(jit.X4, jit.X4, 0, 48)
		}
		asm.STRreg(jit.X4, jit.X2, jit.X1)

	case int64(vm.FBKindFloat):
		if ec.irTypes[valueID] == TypeFloat && ec.hasFPReg(valueID) {
			valFPR := ec.resolveRawFloat(valueID, jit.D0)
			asm.FSTRdReg(valFPR, jit.X2, jit.X1)
		} else {
			valReg := ec.resolveValueNB(valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			if ec.irTypes[valueID] != TypeFloat {
				jit.EmitIsTagged(asm, jit.X4, jit.X5)
				asm.BCond(jit.CondEQ, deoptLabel)
			}
			asm.STRreg(jit.X4, jit.X2, jit.X1)
		}

	case int64(vm.FBKindBool):
		if boolVal, ok := ec.constBools[valueID]; ok {
			asm.MOVimm16(jit.X4, uint16(boolVal+1))
		} else {
			valReg := ec.resolveValueNB(valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			asm.LSRimm(jit.X5, jit.X4, 48)
			asm.MOVimm16(jit.X6, uint16(jit.NB_TagBoolShr48))
			asm.CMPreg(jit.X5, jit.X6)
			boolOK := ec.uniqueLabel("tarr_store_bool_ok")
			asm.BCond(jit.CondEQ, boolOK)
			asm.MOVimm16(jit.X6, uint16(jit.NB_TagNilShr48))
			asm.CMPreg(jit.X5, jit.X6)
			asm.BCond(jit.CondNE, deoptLabel)
			asm.MOVimm16(jit.X4, 0)
			boolStore := ec.uniqueLabel("tarr_store_bool_store")
			asm.B(boolStore)
			asm.Label(boolOK)
			asm.LoadImm64(jit.X5, 1)
			asm.ANDreg(jit.X4, jit.X4, jit.X5)
			asm.ADDimm(jit.X4, jit.X4, 1)
			asm.Label(boolStore)
		}
		asm.STRBreg(jit.X4, jit.X2, jit.X1)

	default:
		ec.emitDeopt(instr)
		return
	}

	ec.recordTableArrayStoreBoundedKey(instr)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	ec.emitPreciseDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableBoolArrayFill(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	fallbackLabel := ec.uniqueLabel("boolfill_fallback")
	doneLabel := ec.uniqueLabel("boolfill_done")
	storeLoopLabel := ec.uniqueLabel("boolfill_loop")
	storeDoneLabel := ec.uniqueLabel("boolfill_store_done")

	tblValueID := instr.Args[0].ID
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	if ec.tableVerified[tblValueID] || ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		ec.tableVerified[tblValueID] = true
	} else if ec.irTypes[tblValueID] == TypeTable {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, fallbackLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, fallbackLabel)
		ec.tableVerified[tblValueID] = true
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, fallbackLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, fallbackLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, fallbackLabel)
		ec.tableVerified[tblValueID] = true
	}
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondNE, fallbackLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffImap)
	asm.CBNZ(jit.X2, fallbackLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffHash)
	asm.CBNZ(jit.X2, fallbackLabel)

	if !ec.emitTableArrayKeyToReg(instr.Args[1], fallbackLabel) {
		ec.emitDeopt(instr)
		return
	}
	asm.MOVreg(jit.X7, jit.X1) // start
	if !ec.emitTableArrayKeyToReg(instr.Args[2], fallbackLabel) {
		ec.emitDeopt(instr)
		return
	}
	asm.MOVreg(jit.X3, jit.X1) // end
	asm.MOVreg(jit.X1, jit.X7) // current index
	asm.CMPreg(jit.X3, jit.X1)
	asm.BCond(jit.CondLT, doneLabel)
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, fallbackLabel)
	asm.ADDimm(jit.X5, jit.X3, 1) // needed len
	asm.CMPreg(jit.X5, jit.X3)
	asm.BCond(jit.CondLE, fallbackLabel)
	asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayCap)
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondGT, fallbackLabel)

	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray)
	asm.MOVimm16(jit.X4, uint16(instr.Aux))
	asm.Label(storeLoopLabel)
	asm.STRBreg(jit.X4, jit.X2, jit.X1)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondEQ, storeDoneLabel)
	asm.ADDimm(jit.X1, jit.X1, 1)
	asm.B(storeLoopLabel)

	asm.Label(storeDoneLabel)
	asm.MOVimm16(jit.X6, 1)
	asm.STRB(jit.X6, jit.X0, jit.TableOffKeysDirty)
	asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayLen)
	asm.CMPreg(jit.X6, jit.X5)
	asm.BCond(jit.CondGE, doneLabel)
	asm.STR(jit.X5, jit.X0, jit.TableOffBoolArrayLen)
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	ec.emitTableBoolArrayFillExit(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableBoolArrayFillExit(instr *Instr) {
	asm := ec.asm
	for i := 0; i < 3 && i < len(instr.Args); i++ {
		arg := instr.Args[i]
		if arg == nil {
			continue
		}
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		if s, ok := ec.slotMap[arg.ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot, startSlot, endSlot := 0, 0, 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > 1 {
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			startSlot = s
		}
	}
	if len(instr.Args) > 2 {
		if s, ok := ec.slotMap[instr.Args[2].ID]; ok {
			endSlot = s
		}
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, nil, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpBoolArrayFill))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(startSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(endSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	boolVal := int64(0)
	if instr.Aux == 2 {
		boolVal = 1
	}
	asm.LoadImm64(jit.X0, boolVal)
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
	asm.Label(continueLabel)
	ec.emitReloadAllActiveRegs()

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

func (ec *emitContext) emitTableArrayNestedLoad(instr *Instr) {
	if len(instr.Args) < 5 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("tarr_nested_deopt")
	doneLabel := ec.uniqueLabel("tarr_nested_done")
	normalLabel := ec.uniqueLabel("tarr_nested_normal")

	rowDataOff, rowLenOff, ok := tableArrayOffsets(instr.Aux)
	if !ok || instr.Aux != int64(vm.FBKindFloat) || instr.Type != TypeFloat {
		ec.emitDeopt(instr)
		return
	}
	expectedRowKind, ok := fbKindToAK(instr.Aux)
	if !ok {
		ec.emitDeopt(instr)
		return
	}

	outerTblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if outerTblReg != jit.X0 {
		asm.MOVreg(jit.X0, outerTblReg)
	}
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDRW(jit.X6, jit.X0, jit.TableOffDMStride)
	asm.CBZ(jit.X6, normalLabel)
	if !ec.emitTableArrayKeyToReg(instr.Args[3], deoptLabel) {
		ec.emitDeopt(instr)
		return
	}
	outerKeyID := instr.Args[3].ID
	if kv, isConst := ec.constInts[outerKeyID]; (!isConst || kv < 0) && !ec.intNonNegative(outerKeyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	asm.MOVreg(jit.X2, jit.X1)
	outerLenReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if outerLenReg != jit.X3 {
		asm.MOVreg(jit.X3, outerLenReg)
	}
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondGE, deoptLabel)
	if !ec.emitTableArrayKeyToReg(instr.Args[4], deoptLabel) {
		ec.emitDeopt(instr)
		return
	}
	innerKeyID := instr.Args[4].ID
	if kv, isConst := ec.constInts[innerKeyID]; (!isConst || kv < 0) && !ec.intNonNegative(innerKeyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	asm.CMPreg(jit.X1, jit.X6)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.MUL(jit.X4, jit.X2, jit.X6)
	asm.ADDreg(jit.X4, jit.X4, jit.X1)
	asm.LDR(jit.X5, jit.X0, jit.TableOffDMFlat)
	dstF := jit.D0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
		dstF = jit.FReg(pr.Reg)
	}
	asm.FLDRdReg(dstF, jit.X5, jit.X4)
	ec.storeRawFloat(dstF, instr.ID)
	asm.B(doneLabel)

	asm.Label(normalLabel)
	outerDataReg := ec.resolveRawInt(instr.Args[1].ID, jit.X2)
	if outerDataReg != jit.X2 {
		asm.MOVreg(jit.X2, outerDataReg)
	}
	outerLenReg = ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if outerLenReg != jit.X3 {
		asm.MOVreg(jit.X3, outerLenReg)
	}
	if !ec.emitTableArrayKeyToReg(instr.Args[3], deoptLabel) {
		ec.emitDeopt(instr)
		return
	}
	outerKeyID = instr.Args[3].ID
	if kv, isConst := ec.constInts[outerKeyID]; (!isConst || kv < 0) && !ec.intNonNegative(outerKeyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGE, deoptLabel)

	asm.LDRreg(jit.X0, jit.X2, jit.X1)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, deoptLabel)
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, expectedRowKind)
	asm.BCond(jit.CondNE, deoptLabel)
	asm.LDR(jit.X3, jit.X0, rowLenOff)
	asm.LDR(jit.X2, jit.X0, rowDataOff)

	if !ec.emitTableArrayKeyToReg(instr.Args[4], deoptLabel) {
		ec.emitDeopt(instr)
		return
	}
	innerKeyID = instr.Args[4].ID
	if kv, isConst := ec.constInts[innerKeyID]; (!isConst || kv < 0) && !ec.intNonNegative(innerKeyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGE, deoptLabel)
	dstF = jit.D0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
		dstF = jit.FReg(pr.Reg)
	}
	asm.FLDRdReg(dstF, jit.X2, jit.X1)
	ec.storeRawFloat(dstF, instr.ID)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	ec.emitPreciseDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableArrayKeyToReg(key *Value, deoptLabel string) bool {
	if key == nil {
		return false
	}
	asm := ec.asm
	keyID := key.ID
	if kv, isConst := ec.constInts[keyID]; isConst {
		asm.LoadImm64(jit.X1, kv)
		return true
	}
	if ec.hasReg(keyID) && ec.valueReprOf(keyID) == valueReprRawInt {
		reg := ec.physReg(keyID)
		if reg != jit.X1 {
			asm.MOVreg(jit.X1, reg)
		}
		return true
	}
	if ec.irTypes[keyID] == TypeInt {
		keyReg := ec.resolveValueNB(keyID, jit.X1)
		if keyReg != jit.X1 {
			asm.MOVreg(jit.X1, keyReg)
		}
		asm.SBFX(jit.X1, jit.X1, 0, 48)
		return true
	}
	keyReg := ec.resolveValueNB(keyID, jit.X1)
	if keyReg != jit.X1 {
		asm.MOVreg(jit.X1, keyReg)
	}
	asm.LSRimm(jit.X4, jit.X1, 48)
	asm.MOVimm16(jit.X5, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X4, jit.X5)
	asm.BCond(jit.CondNE, deoptLabel)
	asm.SBFX(jit.X1, jit.X1, 0, 48)
	return true
}

func tableArrayLoadTableValue(instr *Instr) (*Value, bool) {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return nil, false
	}
	data := instr.Args[0].Def
	if data == nil || data.Op != OpTableArrayData || len(data.Args) < 1 || data.Args[0] == nil {
		return nil, false
	}
	header := data.Args[0].Def
	if header == nil || header.Op != OpTableArrayHeader || len(header.Args) < 1 || header.Args[0] == nil {
		return nil, false
	}
	return header.Args[0], true
}

func (ec *emitContext) emitCheckTableArrayLoadExitResult(instr *Instr, deoptLabel string) {
	if instr == nil {
		return
	}
	asm := ec.asm
	switch instr.Aux {
	case int64(vm.FBKindMixed):
		switch instr.Type {
		case TypeInt:
			asm.LSRimm(jit.X2, jit.X0, 48)
			asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
			asm.CMPreg(jit.X2, jit.X3)
			asm.BCond(jit.CondNE, deoptLabel)
		case TypeFloat:
			jit.EmitIsTagged(asm, jit.X0, jit.X2)
			asm.BCond(jit.CondEQ, deoptLabel)
		case TypeTable:
			jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
		}
	case int64(vm.FBKindInt):
		if instr.Type == TypeInt {
			asm.LSRimm(jit.X2, jit.X0, 48)
			asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
			asm.CMPreg(jit.X2, jit.X3)
			asm.BCond(jit.CondNE, deoptLabel)
		}
	case int64(vm.FBKindFloat):
		if instr.Type == TypeFloat {
			jit.EmitIsTagged(asm, jit.X0, jit.X2)
			asm.BCond(jit.CondEQ, deoptLabel)
		}
	}
}

// emitTableArrayLoadExit handles a typed-array load miss by executing the
// original dynamic GetTable operation in Go and resuming after this IR
// instruction. The receiver is recovered from data -> header -> table metadata,
// so the hot TableArrayLoad operand list stays data/len/key only.
func (ec *emitContext) emitTableArrayLoadExit(instr *Instr) bool {
	asm := ec.asm

	resultSlot, hasResultSlot := ec.slotMap[instr.ID]
	tableValue, hasTableValue := tableArrayLoadTableValue(instr)
	if !hasResultSlot || !hasTableValue || len(instr.Args) < 3 || instr.Args[2] == nil {
		ec.emitPreciseDeopt(instr)
		return false
	}

	tblReg := ec.resolveValueNB(tableValue.ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	tblSlot, hasTblSlot := ec.slotMap[tableValue.ID]
	if !hasTblSlot {
		ec.emitPreciseDeopt(instr)
		return false
	}
	asm.STR(jit.X0, mRegRegs, slotOffset(tblSlot))

	keyValue := instr.Args[2]
	keyReg := ec.resolveValueNB(keyValue.ID, jit.X0)
	if keyReg != jit.X0 {
		asm.MOVreg(jit.X0, keyReg)
	}
	keySlot, hasKeySlot := ec.slotMap[keyValue.ID]
	if !hasKeySlot {
		ec.emitPreciseDeopt(instr)
		return false
	}
	asm.STR(jit.X0, mRegRegs, slotOffset(keySlot))

	ec.recordTableArrayLoadExitResumeCheckSite(instr, resultSlot)
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpGetTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(keySlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
	asm.Label(continueLabel)
	ec.emitReloadAllActiveRegs()
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
	return true
}

func (ec *emitContext) recordTableArrayLoadExitResumeCheckSite(instr *Instr, resultSlot int) {
	if ec.exitResumeCheck == nil || instr == nil {
		return
	}
	gprLive := ec.activeRegs
	fprLive := ec.activeFPRegs
	if gprLive[instr.ID] {
		gprLive = make(map[int]bool, len(ec.activeRegs))
		for valueID, live := range ec.activeRegs {
			if valueID != instr.ID {
				gprLive[valueID] = live
			}
		}
	}
	if fprLive[instr.ID] {
		fprLive = make(map[int]bool, len(ec.activeFPRegs))
		for valueID, live := range ec.activeFPRegs {
			if valueID != instr.ID {
				fprLive[valueID] = live
			}
		}
	}
	ec.recordExitResumeCheckSiteWithLive(instr, ExitTableExit, ec.exitResumeCheckLiveSlots(gprLive, fprLive), []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
}

func (ec *emitContext) intNonNegative(id int) bool {
	if ec.fn == nil || ec.fn.IntNonNegative == nil {
		return false
	}
	return ec.fn.IntNonNegative[id]
}

func (ec *emitContext) tableArrayUpperBoundSafe(id int) bool {
	if ec.fn == nil || ec.fn.TableArrayUpperBoundSafe == nil {
		return false
	}
	return ec.fn.TableArrayUpperBoundSafe[id]
}

func (ec *emitContext) recordTableArrayBoundedKey(instr *Instr) {
	if ec == nil || instr == nil || len(instr.Args) < 3 || instr.Args[2] == nil {
		return
	}
	tableValue, ok := tableArrayLoadTableValue(instr)
	if !ok || tableValue == nil {
		return
	}
	ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool, 1)
	ec.asm.MOVimm16(jit.X17, 1)
	ec.tableArrayBoundedKeys[tableArrayBoundKey{tableID: tableValue.ID, keyID: instr.Args[2].ID}] = true
}

func (ec *emitContext) recordTableArrayStoreBoundedKey(instr *Instr) {
	if ec == nil || instr == nil || len(instr.Args) < 4 || instr.Args[0] == nil || instr.Args[3] == nil {
		return
	}
	ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool, 1)
	ec.asm.MOVimm16(jit.X17, 1)
	ec.tableArrayBoundedKeys[tableArrayBoundKey{tableID: instr.Args[0].ID, keyID: instr.Args[3].ID}] = true
}

func (ec *emitContext) tableArrayKeyBounded(tableID, keyID int) bool {
	if ec == nil || ec.tableArrayBoundedKeys == nil {
		return false
	}
	return ec.tableArrayBoundedKeys[tableArrayBoundKey{tableID: tableID, keyID: keyID}]
}

func (ec *emitContext) clearTableArrayBoundedKeys() {
	if ec != nil && len(ec.tableArrayBoundedKeys) > 0 {
		ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool)
	}
}

func (ec *emitContext) isLocalNewTableWithoutMetatable(v *Value) bool {
	return ec != nil && ec.localNewTablesNoMetatable && v != nil && v.Def != nil && v.Def.Op == OpNewTable
}

func (ec *emitContext) localNewTableFBKind(v *Value) (uint16, bool) {
	if !ec.isLocalNewTableWithoutMetatable(v) {
		return 0, false
	}
	_, kind := unpackNewTableAux2(v.Def.Aux2)
	switch kind {
	case runtime.ArrayMixed:
		return uint16(vm.FBKindMixed), true
	case runtime.ArrayInt:
		return uint16(vm.FBKindInt), true
	case runtime.ArrayFloat:
		return uint16(vm.FBKindFloat), true
	case runtime.ArrayBool:
		return uint16(vm.FBKindBool), true
	default:
		return 0, false
	}
}

func functionHasNoTableMetatableMutationSurface(fn *Function) bool {
	if fn == nil {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall, OpSelf, OpSetGlobal, OpSetUpval, OpAppend, OpSetList,
				OpConcat, OpPow, OpClosure, OpClose, OpTForCall, OpTForLoop,
				OpVararg, OpTestSet, OpGo, OpMakeChan, OpSend, OpRecv:
				return false
			}
		}
	}
	return true
}

func (ec *emitContext) setTablePreservesLocalArrayFacts(instr *Instr) bool {
	if ec == nil || instr == nil || len(instr.Args) < 3 || instr.Args[0] == nil || !ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		return false
	}
	switch instr.Aux2 {
	case int64(vm.FBKindMixed):
		return true
	case int64(vm.FBKindInt):
		valueID := instr.Args[2].ID
		_, isConst := ec.constInts[valueID]
		return isConst || (ec.hasReg(valueID) && ec.valueReprOf(valueID) == valueReprRawInt) || ec.irTypes[valueID] == TypeInt
	case int64(vm.FBKindFloat):
		return ec.irTypes[instr.Args[2].ID] == TypeFloat
	case int64(vm.FBKindBool):
		valueID := instr.Args[2].ID
		_, isConst := ec.constBools[valueID]
		return isConst || ec.irTypes[valueID] == TypeBool || ec.irTypes[valueID] == TypeUnknown && instr.Args[2].Def != nil && instr.Args[2].Def.Op == OpConstNil
	default:
		return false
	}
}

// emitNewTableExit emits a table-exit for OpNewTable. Table allocation is
// complex (Go heap, slice allocation), so always exits to Go.
//
// Instr layout:
//   - Aux = array hint
//   - Aux2 = packed hash hint and array kind
func (ec *emitContext) emitNewTableExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	doneLabel := ec.uniqueLabel("newtable_done")
	missLabel := ec.uniqueLabel("newtable_cache_miss")
	hasCacheFastPath := ec.emitNewTableCacheFastPath(instr, doneLabel, missLabel)
	if hasCacheFastPath {
		asm.Label(missLabel)
	}

	// Store all active register-resident values to memory.
	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
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
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label.
	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
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
		numericPass:   ec.numericMode,
	})
	if hasCacheFastPath {
		asm.Label(doneLabel)
	}
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
	tblValueID := instr.Args[0].ID
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}

	if ec.tableVerified[tblValueID] {
		// Table already validated in this block — skip type/nil/metatable checks.
		// Just extract the raw pointer.
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else if ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		ec.tableVerified[tblValueID] = true
	} else if ec.irTypes[tblValueID] == TypeTable {
		// The producer already guards/proves table-ness. Re-check the dynamic
		// metatable because table identity can still carry metamethods.
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, deoptLabel)
		ec.tableVerified[tblValueID] = true
	} else {
		// Full validation.
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, deoptLabel)
		ec.tableVerified[tblValueID] = true
	}

	// Load key into X1 with type-specialized fast paths.
	keyID := instr.Args[1].ID

	if kv, isConst := ec.constInts[keyID]; isConst {
		// R98: const int key — load the immediate directly, bypass reg
		// resolution, tag check, and unbox.
		asm.LoadImm64(jit.X1, kv)
	} else if ec.hasReg(keyID) && ec.valueReprOf(keyID) == valueReprRawInt {
		// Fast path 1: key is raw int in a register.
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

	// Check key >= 0 (shared by all paths). R97: skip when key is a
	// ConstInt with a non-negative compile-time value.
	if kv, isConst := ec.constInts[keyID]; (!isConst || kv < 0) && !ec.intNonNegative(keyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}

	// Kind-specialized dispatch: when Aux2 carries feedback, emit a kind
	// guard (3 insns) instead of the 4-way cascade (8 insns). When the
	// same (table, kind) pair has already been verified earlier in this
	// block, skip the guard entirely — emit only the direct jump.
	mixedArrayLabel := ec.uniqueLabel("gettable_mixedarr")
	knownGetKind := int(instr.Aux2) // 0=unknown, 1..4=known FBKind
	if knownGetKind >= 1 && knownGetKind <= 4 {
		expectedKind := uint16(knownGetKind - 1) // convert FBKind to AK constant
		if fbKind, ok := ec.localNewTableFBKind(instr.Args[0]); ok && fbKind == uint16(knownGetKind) {
			ec.kindVerified[tblValueID] = uint16(knownGetKind)
		}
		if ec.kindVerified[tblValueID] != uint16(knownGetKind) {
			asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
			asm.CMPimm(jit.X2, expectedKind)
			cacheKind := expectedKind == jit.AKMixed
			if expectedKind == jit.AKMixed {
				asm.BCond(jit.CondNE, deoptLabel) // kind mismatch → deopt
			} else {
				getKindOKLabel := ec.uniqueLabel("gettable_kind_ok")
				asm.BCond(jit.CondEQ, getKindOKLabel)
				asm.CMPimm(jit.X2, jit.AKMixed)
				asm.BCond(jit.CondEQ, mixedArrayLabel)
				asm.B(deoptLabel)
				asm.Label(getKindOKLabel)
			}
			if cacheKind {
				ec.kindVerified[tblValueID] = uint16(knownGetKind)
			}
		}
		// Jump directly to the matching kind path.
		switch expectedKind {
		case jit.AKMixed:
			asm.B(mixedArrayLabel)
		case jit.AKInt:
			asm.B(intArrayLabel)
		case jit.AKFloat:
			asm.B(floatArrayLabel)
		case jit.AKBool:
			asm.B(boolArrayLabel)
		}
	} else {
		// Unknown kind: use existing 4-way dispatch cascade.
		asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
		asm.CMPimm(jit.X2, jit.AKBool)
		asm.BCond(jit.CondEQ, boolArrayLabel)
		asm.CMPimm(jit.X2, jit.AKFloat)
		asm.BCond(jit.CondEQ, floatArrayLabel)
		asm.CMPimm(jit.X2, jit.AKInt)
		asm.BCond(jit.CondEQ, intArrayLabel)
		asm.CBNZ(jit.X2, deoptLabel) // not Mixed(0) -> deopt
	}

	// --- ArrayMixed fast path ---
	asm.Label(mixedArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArrayLen) // array.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArray) // array data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)         // value = array[key]
	switch instr.Type {
	case TypeInt:
		asm.LSRimm(jit.X2, jit.X0, 48)
		asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		asm.CMPreg(jit.X2, jit.X3)
		asm.BCond(jit.CondNE, deoptLabel)
		asm.SBFX(jit.X0, jit.X0, 0, 48)
		ec.storeRawInt(jit.X0, instr.ID)
	case TypeFloat:
		jit.EmitIsTagged(asm, jit.X0, jit.X2)
		asm.BCond(jit.CondEQ, deoptLabel)
		asm.FMOVtoFP(jit.D0, jit.X0)
		ec.storeRawFloat(jit.D0, instr.ID)
	case TypeTable:
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
		ec.storeResultNB(jit.X0, instr.ID)
	default:
		ec.storeResultNB(jit.X0, instr.ID)
	}
	asm.B(doneLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArrayLen) // intArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray) // intArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)            // raw int64 = intArray[key]
	if instr.Type == TypeInt {
		ec.storeRawInt(jit.X0, instr.ID)
	} else {
		// NaN-box the int64: UBFX + ORR with pinned tag register.
		jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		ec.storeResultNB(jit.X0, instr.ID)
	}
	asm.B(doneLabel)

	// --- ArrayFloat fast path ---
	asm.Label(floatArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArrayLen) // floatArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // floatArray data pointer
	if instr.Type == TypeFloat {
		asm.FLDRdReg(jit.D0, jit.X2, jit.X1) // raw float64 = floatArray[key]
		ec.storeRawFloat(jit.D0, instr.ID)
	} else {
		asm.LDRreg(jit.X0, jit.X2, jit.X1) // raw float64 bits = floatArray[key]
		// Float64 bits ARE the NaN-boxed value — no conversion needed!
		ec.storeResultNB(jit.X0, instr.ID)
	}
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
	asm.CBZ(jit.X3, nilLabel) // byte == 0 → nil
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
	savedReprs := ec.snapshotValueReprs()
	ec.emitGetTableExit(instr)
	ec.emitUnboxRawIntRegs(savedReprs)
	ec.restoreValueReprSnapshot(savedReprs)

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
	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
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
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label.
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
	tblValueID := instr.Args[0].ID
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}

	if ec.tableVerified[tblValueID] {
		// Table already validated in this block — skip type/nil/metatable checks.
		// Just extract the raw pointer.
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else if ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		ec.tableVerified[tblValueID] = true
	} else if ec.irTypes[tblValueID] == TypeTable {
		// The producer already guards/proves table-ness. Re-check the dynamic
		// metatable because table identity can still carry metamethods.
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, deoptLabel)
		ec.tableVerified[tblValueID] = true
	} else {
		// Full validation.
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, deoptLabel)
		ec.tableVerified[tblValueID] = true
	}

	// Load key into X1 with type-specialized fast paths.
	keyID := instr.Args[1].ID

	if kv, isConst := ec.constInts[keyID]; isConst {
		// R98: const int key — direct immediate load.
		asm.LoadImm64(jit.X1, kv)
	} else if ec.hasReg(keyID) && ec.valueReprOf(keyID) == valueReprRawInt {
		// Fast path 1: key is raw int in a register.
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

	// Check key >= 0 (shared by all paths). R97: skip when key is a
	// ConstInt with a non-negative compile-time value.
	if kv, isConst := ec.constInts[keyID]; (!isConst || kv < 0) && !ec.intNonNegative(keyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	keyBoundsAlreadyChecked := ec.tableArrayKeyBounded(tblValueID, keyID)

	// Mixed array stores of table rows are the construction side of ordinary
	// table-of-float-row matrices. First/complex stores still route through
	// RawSetInt so the runtime owns allocation and invalidation semantics. Once
	// a DenseMatrix backing exists, the safe sequential append case can stay
	// native: copy the row into the existing flat backing and rebind the row
	// wrapper to that slice.
	if instr.Aux2 == int64(vm.FBKindMixed) && ec.irTypes[instr.Args[2].ID] == TypeTable {
		ec.emitDenseMatrixRowAppendFastPath(instr, deoptLabel, doneLabel)
		asm.Label(deoptLabel)
		savedReprs := ec.snapshotValueReprs()
		ec.emitSetTableExit(instr)
		ec.emitUnboxRawIntRegs(savedReprs)
		ec.restoreValueReprSnapshot(savedReprs)
		asm.Label(doneLabel)
		delete(ec.tableVerified, tblValueID)
		delete(ec.kindVerified, tblValueID)
		delete(ec.keysDirtyWritten, tblValueID)
		return
	}

	// Kind-specialized dispatch: when Aux2 carries feedback, emit a kind
	// guard instead of the 4-way cascade. When the same (table, kind) pair
	// has already been verified earlier in this block, skip the guard
	// entirely and omit the mixed fallback that the guard cannot reach.
	mixedArrayLabel := ec.uniqueLabel("settable_mixedarr")
	knownSetKind := int(instr.Aux2) // 0=unknown, 1..4=known FBKind
	expectedKind, hasKnownSetKind := fbKindToAK(instr.Aux2)
	kindAlreadyVerified := hasKnownSetKind && ec.kindVerified[tblValueID] == uint16(knownSetKind)
	emitMixedArrayPath := !hasKnownSetKind || expectedKind == jit.AKMixed || !kindAlreadyVerified
	emitIntArrayPath := !hasKnownSetKind || expectedKind == jit.AKInt
	emitFloatArrayPath := !hasKnownSetKind || expectedKind == jit.AKFloat
	emitBoolArrayPath := !hasKnownSetKind || expectedKind == jit.AKBool
	fastPathAlwaysWritesKeysDirty := !emitIntArrayPath && !emitFloatArrayPath
	if hasKnownSetKind {
		if fbKind, ok := ec.localNewTableFBKind(instr.Args[0]); ok && fbKind == uint16(knownSetKind) {
			ec.kindVerified[tblValueID] = uint16(knownSetKind)
		}
		if ec.kindVerified[tblValueID] != uint16(knownSetKind) {
			asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
			asm.CMPimm(jit.X2, expectedKind)
			cacheKind := expectedKind == jit.AKMixed
			if expectedKind == jit.AKMixed {
				asm.BCond(jit.CondNE, deoptLabel) // kind mismatch → deopt
			} else {
				setKindOKLabel := ec.uniqueLabel("settable_kind_ok")
				asm.BCond(jit.CondEQ, setKindOKLabel)
				asm.CMPimm(jit.X2, jit.AKMixed)
				asm.BCond(jit.CondEQ, mixedArrayLabel)
				asm.B(deoptLabel)
				asm.Label(setKindOKLabel)
			}
			if cacheKind {
				ec.kindVerified[tblValueID] = uint16(knownSetKind)
			}
		}
		// Jump directly to the matching kind path.
		switch expectedKind {
		case jit.AKMixed:
			asm.B(mixedArrayLabel)
		case jit.AKInt:
			asm.B(intArrayLabel)
		case jit.AKFloat:
			asm.B(floatArrayLabel)
		case jit.AKBool:
			asm.B(boolArrayLabel)
		}
	} else {
		// Unknown kind: use existing 4-way dispatch cascade.
		asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
		asm.CMPimm(jit.X2, jit.AKBool)
		asm.BCond(jit.CondEQ, boolArrayLabel)
		asm.CMPimm(jit.X2, jit.AKFloat)
		asm.BCond(jit.CondEQ, floatArrayLabel)
		asm.CMPimm(jit.X2, jit.AKInt)
		asm.BCond(jit.CondEQ, intArrayLabel)
		asm.CBNZ(jit.X2, deoptLabel) // not Mixed(0) -> deopt
	}

	if emitMixedArrayPath {
		// --- ArrayMixed fast path ---
		asm.Label(mixedArrayLabel)
		mixedStoreLabel := ec.uniqueLabel("settable_mixed_store")
		mixedAppendLabel := ec.uniqueLabel("settable_mixed_append")
		emitTypedArraySetPriorLoadOrBoundsAppendCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffArrayLen, mixedAppendLabel, deoptLabel)
		asm.Label(mixedStoreLabel)
		// Load value to store into X4.
		valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
		if valReg != jit.X4 {
			asm.MOVreg(jit.X4, valReg)
		}
		asm.LDR(jit.X2, jit.X0, jit.TableOffArray) // array data pointer
		asm.STRreg(jit.X4, jit.X2, jit.X1)         // array[key] = value
		// Set keysDirty flag (elided if already set in this block).
		if !ec.keysDirtyWritten[tblValueID] {
			asm.MOVimm16(jit.X5, 1)
			asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
		}
		asm.B(doneLabel)
		if !keyBoundsAlreadyChecked {
			emitTypedArraySetAppendPath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffArrayLen, jit.TableOffArrayCap, mixedAppendLabel, deoptLabel, mixedStoreLabel)
		}
	}

	// --- ArrayInt fast path ---
	if emitIntArrayPath {
		asm.Label(intArrayLabel)

		if val, ok := ec.constInts[instr.Args[2].ID]; ok {
			// Constant int bypass: load immediate, skip tag check and unbox.
			intStoreLabel := ec.uniqueLabel("settable_int_store")
			intAppendLabel := ec.uniqueLabel("settable_int_append")
			intSparseLabel := ec.uniqueLabel("settable_int_sparse")
			asm.LoadImm64(jit.X4, val)
			emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffIntArrayLen, intAppendLabel, intSparseLabel, deoptLabel)
			asm.Label(intStoreLabel)
			asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray)
			asm.STRreg(jit.X4, jit.X2, jit.X1)
			asm.B(doneLabel)
			if !keyBoundsAlreadyChecked {
				emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intAppendLabel, deoptLabel, intStoreLabel)
				emitTypedArraySetSparsePathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intSparseLabel, deoptLabel, intStoreLabel)
			}
		} else if ec.hasReg(instr.Args[2].ID) && ec.valueReprOf(instr.Args[2].ID) == valueReprRawInt {
			// Raw int register bypass: value already unboxed, skip tag check.
			intStoreLabel := ec.uniqueLabel("settable_int_store")
			intAppendLabel := ec.uniqueLabel("settable_int_append")
			intSparseLabel := ec.uniqueLabel("settable_int_sparse")
			reg := ec.physReg(instr.Args[2].ID)
			if reg != jit.X4 {
				asm.MOVreg(jit.X4, reg)
			}
			emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffIntArrayLen, intAppendLabel, intSparseLabel, deoptLabel)
			asm.Label(intStoreLabel)
			asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray)
			asm.STRreg(jit.X4, jit.X2, jit.X1)
			asm.B(doneLabel)
			if !keyBoundsAlreadyChecked {
				emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intAppendLabel, deoptLabel, intStoreLabel)
				emitTypedArraySetSparsePathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intSparseLabel, deoptLabel, intStoreLabel)
			}
		} else if ec.irTypes[instr.Args[2].ID] == TypeInt {
			// Known-int value: unbox directly and skip the redundant tag check.
			intStoreLabel := ec.uniqueLabel("settable_int_store")
			intAppendLabel := ec.uniqueLabel("settable_int_append")
			intSparseLabel := ec.uniqueLabel("settable_int_sparse")
			valReg2 := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
			if valReg2 != jit.X4 {
				asm.MOVreg(jit.X4, valReg2)
			}
			asm.SBFX(jit.X4, jit.X4, 0, 48)
			emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffIntArrayLen, intAppendLabel, intSparseLabel, deoptLabel)
			asm.Label(intStoreLabel)
			asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray)
			asm.STRreg(jit.X4, jit.X2, jit.X1)
			asm.B(doneLabel)
			if !keyBoundsAlreadyChecked {
				emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intAppendLabel, deoptLabel, intStoreLabel)
				emitTypedArraySetSparsePathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intSparseLabel, deoptLabel, intStoreLabel)
			}
		} else {
			// Load value to store and check it's an integer.
			intStoreLabel := ec.uniqueLabel("settable_int_store")
			intAppendLabel := ec.uniqueLabel("settable_int_append")
			intSparseLabel := ec.uniqueLabel("settable_int_sparse")
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
			emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffIntArrayLen, intAppendLabel, intSparseLabel, deoptLabel)
			asm.Label(intStoreLabel)
			asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray) // intArray data pointer
			asm.STRreg(jit.X4, jit.X2, jit.X1)            // intArray[key] = int64
			asm.B(doneLabel)
			if !keyBoundsAlreadyChecked {
				emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intAppendLabel, deoptLabel, intStoreLabel)
				emitTypedArraySetSparsePathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intSparseLabel, deoptLabel, intStoreLabel)
			}
		}
	}

	if emitFloatArrayPath {
		// --- ArrayFloat fast path ---
		asm.Label(floatArrayLabel)
		// Load value to store.
		floatStoreLabel := ec.uniqueLabel("settable_float_store")
		floatAppendLabel := ec.uniqueLabel("settable_float_append")
		floatSparseLabel := ec.uniqueLabel("settable_float_sparse")
		valueID := instr.Args[2].ID
		valueIsTypedFloat := ec.irTypes[valueID] == TypeFloat
		valueHasRawFPR := valueIsTypedFloat && ec.hasFPReg(valueID)
		if !valueHasRawFPR {
			valRegFloat := ec.resolveValueNB(valueID, jit.X4)
			if valRegFloat != jit.X4 {
				asm.MOVreg(jit.X4, valRegFloat)
			}
			if !valueIsTypedFloat {
				// Check value is a float (NOT tagged — bits 50-62 NOT all set).
				// Tagged values have (val >> 50) == 0x3FFF. Floats don't.
				jit.EmitIsTagged(asm, jit.X4, jit.X5) // sets flags: EQ = tagged, NE = float
				asm.BCond(jit.CondEQ, deoptLabel)     // tagged (int/bool/nil/ptr) → deopt
			}
		}
		emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffFloatArrayLen, floatAppendLabel, floatSparseLabel, deoptLabel)
		asm.Label(floatStoreLabel)
		// Float64 bits ARE the NaN-boxed representation — store directly.
		asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // floatArray data pointer
		if valueHasRawFPR {
			valFPR := ec.resolveRawFloat(valueID, jit.D0)
			asm.FSTRdReg(valFPR, jit.X2, jit.X1) // floatArray[key] = float64
		} else {
			asm.STRreg(jit.X4, jit.X2, jit.X1) // floatArray[key] = float64 bits
		}
		asm.B(doneLabel)
		if !keyBoundsAlreadyChecked {
			emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffFloatArrayLen, jit.TableOffFloatArrayCap, floatAppendLabel, deoptLabel, floatStoreLabel)
			emitTypedArraySetSparsePathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffFloatArrayLen, jit.TableOffFloatArrayCap, floatSparseLabel, deoptLabel, floatStoreLabel)
		}
	}

	if emitBoolArrayPath {
		// --- ArrayBool fast path ---
		asm.Label(boolArrayLabel)

		// Constant bool bypass: skip value load, tag check, and payload extraction.
		if boolVal, ok := ec.constBools[instr.Args[2].ID]; ok {
			// false(0)→byte 1, true(1)→byte 2
			boolStoreLabel := ec.uniqueLabel("settable_bool_store")
			boolAppendLabel := ec.uniqueLabel("settable_bool_append")
			boolSparseLabel := ec.uniqueLabel("settable_bool_sparse")
			byteVal := uint16(boolVal + 1)
			asm.MOVimm16(jit.X4, byteVal)
			emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffBoolArrayLen, boolAppendLabel, boolSparseLabel, deoptLabel)
			asm.Label(boolStoreLabel)
			asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray) // boolArray data pointer
			asm.STRBreg(jit.X4, jit.X2, jit.X1)            // boolArray[key] = byte
			if !ec.keysDirtyWritten[tblValueID] {
				asm.MOVimm16(jit.X5, 1)
				asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
			}
			asm.B(doneLabel)
			if !keyBoundsAlreadyChecked {
				emitTypedArraySetAppendPath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffBoolArrayLen, jit.TableOffBoolArrayCap, boolAppendLabel, deoptLabel, boolStoreLabel)
				emitTypedArraySetSparsePath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffBoolArrayLen, jit.TableOffBoolArrayCap, boolSparseLabel, deoptLabel, boolStoreLabel)
			}
		} else {
			// Load value to store.
			boolStoreLabel := ec.uniqueLabel("settable_bool_store")
			boolAppendLabel := ec.uniqueLabel("settable_bool_append")
			boolSparseLabel := ec.uniqueLabel("settable_bool_sparse")
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
			// Nil clears an existing bool slot only. Appending/sparse-growing
			// nil must deopt so RawSetInt can preserve table length semantics.
			asm.MOVimm16(jit.X4, 0)
			emitTypedArraySetPriorLoadOrBoundsOnlyCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffBoolArrayLen, deoptLabel)
			asm.B(boolStoreLabel)
			asm.Label(boolOkLabel)
			// Bool: extract payload bit 0. false=0xFFFD000000000000 (payload=0) → byte 1
			//                                true=0xFFFD000000000001 (payload=1) → byte 2
			// Conversion: byte = payload + 1
			asm.LoadImm64(jit.X5, 1)
			asm.ANDreg(jit.X4, jit.X4, jit.X5) // extract bit 0 (payload)
			asm.ADDimm(jit.X4, jit.X4, 1)      // 0→1 (false), 1→2 (true)
			emitTypedArraySetPriorLoadOrBoundsAppendSparseCheck(asm, keyBoundsAlreadyChecked, jit.X0, jit.X1, jit.X2, jit.TableOffBoolArrayLen, boolAppendLabel, boolSparseLabel, deoptLabel)
			asm.Label(boolStoreLabel)
			asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray) // boolArray data pointer
			asm.STRBreg(jit.X4, jit.X2, jit.X1)            // boolArray[key] = byte
			if !ec.keysDirtyWritten[tblValueID] {
				asm.MOVimm16(jit.X5, 1)
				asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
			}
			asm.B(doneLabel)
			if !keyBoundsAlreadyChecked {
				emitTypedArraySetAppendPath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffBoolArrayLen, jit.TableOffBoolArrayCap, boolAppendLabel, deoptLabel, boolStoreLabel)
				emitTypedArraySetSparsePath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffBoolArrayLen, jit.TableOffBoolArrayCap, boolSparseLabel, deoptLabel, boolStoreLabel)
			}
		}
	}

	// Deopt: fall back to exit-resume.
	asm.Label(deoptLabel)
	savedReprs := ec.snapshotValueReprs()
	ec.emitSetTableExit(instr)
	ec.emitUnboxRawIntRegs(savedReprs)
	ec.restoreValueReprSnapshot(savedReprs)

	asm.Label(doneLabel)
	// Runtime exit-resume can invoke metamethods or demote unknown tables, so
	// most writes invalidate table/kind facts. A local NewTable in a function
	// with no metatable mutation surface keeps its facts only when the store
	// value is proven compatible with the typed backing, so even a slow-path
	// sparse/append store cannot demote the array kind.
	if ec.setTablePreservesLocalArrayFacts(instr) {
		ec.tableVerified[tblValueID] = true
		if hasKnownSetKind {
			ec.kindVerified[tblValueID] = uint16(knownSetKind)
		}
	} else {
		delete(ec.tableVerified, tblValueID)
		delete(ec.kindVerified, tblValueID)
	}
	// keysDirty is idempotent. Record only when every native path writes it;
	// typed int/float in-bounds overwrites intentionally skip it because they
	// do not change the table's key set.
	if fastPathAlwaysWritesKeysDirty {
		ec.keysDirtyWritten[tblValueID] = true
	} else {
		delete(ec.keysDirtyWritten, tblValueID)
	}
}

func (ec *emitContext) emitDenseMatrixRowAppendFastPath(instr *Instr, missLabel, doneLabel string) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm

	valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
	if valReg != jit.X4 {
		asm.MOVreg(jit.X4, valReg)
	}
	jit.EmitCheckIsTableFull(asm, jit.X4, jit.X8, jit.X9, missLabel)
	jit.EmitExtractPtr(asm, jit.X7, jit.X4) // X7 = row *Table
	asm.CBZ(jit.X7, missLabel)

	// Outer table: only the exact RawSetInt-compatible append shape.
	asm.LDRB(jit.X8, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X8, jit.AKMixed)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X8, jit.X0, jit.TableOffImap)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDR(jit.X8, jit.X0, jit.TableOffHash)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArrayLen)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, missLabel) // replacements must invalidate via RawSetInt
	asm.LDR(jit.X3, jit.X0, jit.TableOffArrayCap)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGE, missLabel)

	asm.LDRW(jit.X6, jit.X0, jit.TableOffDMStride)
	asm.CBZ(jit.X6, missLabel)
	asm.CMPimm(jit.X6, uint16(runtime.AutoDenseMatrixMinStride))
	asm.BCond(jit.CondLT, missLabel)
	asm.LDR(jit.X5, jit.X0, jit.TableOffDMFlat)
	asm.CBZ(jit.X5, missLabel)
	asm.LDR(jit.X9, jit.X0, jit.TableOffDMMeta)
	asm.CBZ(jit.X9, missLabel)
	asm.LDR(jit.X8, jit.X9, jit.DenseMatrixMetaOffParent)
	asm.CMPreg(jit.X8, jit.X0)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X8, jit.X9, jit.DenseMatrixMetaOffBackingData)
	asm.CMPreg(jit.X8, jit.X5)
	asm.BCond(jit.CondNE, missLabel)

	// Row table: unattached ArrayFloat row with the same stride and no maps.
	asm.LDR(jit.X8, jit.X7, jit.TableOffMetatable)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDR(jit.X8, jit.X7, jit.TableOffImap)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDR(jit.X8, jit.X7, jit.TableOffHash)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDR(jit.X8, jit.X7, jit.TableOffDMMeta)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDRB(jit.X8, jit.X7, jit.TableOffArrayKind)
	asm.CMPimm(jit.X8, jit.AKFloat)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X8, jit.X7, jit.TableOffFloatArrayLen)
	asm.CMPreg(jit.X8, jit.X6)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X10, jit.X7, jit.TableOffFloatArray)
	asm.CBZ(jit.X10, missLabel)

	// Dense backing must already have enough capacity. Growth remains in Go.
	asm.ADDimm(jit.X8, jit.X1, 1) // row count after append
	asm.MUL(jit.X8, jit.X8, jit.X6)
	asm.LDR(jit.X12, jit.X9, jit.DenseMatrixMetaOffBackingCap)
	asm.CMPreg(jit.X8, jit.X12)
	asm.BCond(jit.CondGT, missLabel)

	// All checks are complete; from here to doneLabel is mutation-only.
	asm.STR(jit.X8, jit.X9, jit.DenseMatrixMetaOffBackingLen)
	asm.ADDimm(jit.X2, jit.X1, 1)
	asm.STR(jit.X2, jit.X0, jit.TableOffArrayLen)
	asm.MOVimm16(jit.X12, 1)
	asm.STRB(jit.X12, jit.X0, jit.TableOffKeysDirty)
	asm.LDR(jit.X12, jit.X0, jit.TableOffArray)
	asm.STRreg(jit.X4, jit.X12, jit.X1)

	asm.MUL(jit.X11, jit.X1, jit.X6)
	asm.LSLimm(jit.X11, jit.X11, 3)
	asm.ADDreg(jit.X11, jit.X5, jit.X11) // destination row base
	asm.MOVimm16(jit.X12, 0)
	copyLoop := ec.uniqueLabel("settable_dense_row_copy")
	copyDone := ec.uniqueLabel("settable_dense_row_copy_done")
	asm.Label(copyLoop)
	asm.CMPreg(jit.X12, jit.X6)
	asm.BCond(jit.CondGE, copyDone)
	asm.LDRreg(jit.X13, jit.X10, jit.X12)
	asm.STRreg(jit.X13, jit.X11, jit.X12)
	asm.ADDimm(jit.X12, jit.X12, 1)
	asm.B(copyLoop)
	asm.Label(copyDone)

	asm.STR(jit.X11, jit.X7, jit.TableOffFloatArray)
	asm.STR(jit.X6, jit.X7, jit.TableOffFloatArrayLen)
	asm.STR(jit.X6, jit.X7, jit.TableOffFloatArrayCap)
	asm.STR(jit.X9, jit.X7, jit.TableOffDMMeta)
	asm.B(doneLabel)
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
	ec.recordExitResumeCheckSite(instr, ExitTableExit, nil, exitResumeCheckOptions{RequireTableInputs: true})
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
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label.
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
