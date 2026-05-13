//go:build darwin && arm64

// emit_table_array.go implements ARM64 code generation for table array/dynamic
// key operations (OpNewTable, OpGetTable, OpSetTable) in the Method JIT.
// These handle integer-keyed array access with type-specialized fast paths
// and exit-resume fallbacks for complex cases.

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const (
	// tableArrayStoreFlagAllowGrow lets OpTableArrayStore use the same
	// capacity-only append/sparse typed-array path as OpSetTable. Misses still
	// precise-deopt unless tableArrayStoreFlagExitResumeOnMiss is also set.
	tableArrayStoreFlagAllowGrow int64 = 1 << iota
	// tableArrayStoreFlagExitResumeOnMiss routes misses through SetTable and
	// resumes. The resume path refreshes table-array data/len facts so later
	// raw array operations do not keep stale backing pointers after fallback.
	tableArrayStoreFlagExitResumeOnMiss
)

type tableArrayBoundKey struct {
	tableID int
	keyID   int
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

func tableArrayStoreOffsets(kind int64) (dataOff, lenOff, capOff int, ok bool) {
	switch kind {
	case int64(vm.FBKindMixed):
		return jit.TableOffArray, jit.TableOffArrayLen, jit.TableOffArrayCap, true
	case int64(vm.FBKindInt):
		return jit.TableOffIntArray, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, true
	case int64(vm.FBKindFloat):
		return jit.TableOffFloatArray, jit.TableOffFloatArrayLen, jit.TableOffFloatArrayCap, true
	case int64(vm.FBKindBool):
		return jit.TableOffBoolArray, jit.TableOffBoolArrayLen, jit.TableOffBoolArrayCap, true
	default:
		return 0, 0, 0, false
	}
}

type tableArrayRawStoreConfig struct {
	labelPrefix             string
	kind                    int64
	valueID                 int
	tableReg                jit.Reg
	keyReg                  jit.Reg
	dataReg                 jit.Reg
	lenReg                  jit.Reg
	missLabel               string
	successLabel            string
	loadDataFromTable       bool
	priorLoadBounds         bool
	upperBoundSafe          bool
	keysDirtyAlreadyWritten bool
	allowGrowWithinCapacity bool
	carryLenOnGrow          bool
	fallthroughOnSuccess    bool
}

func (ec *emitContext) emitTableArrayRawStore(cfg tableArrayRawStoreConfig) bool {
	if cfg.labelPrefix == "" {
		cfg.labelPrefix = "tarr_raw_store"
	}
	dataOff, lenOff, capOff, ok := tableArrayStoreOffsets(cfg.kind)
	if !ok {
		return false
	}

	asm := ec.asm
	storeLabel := ec.uniqueLabel(cfg.labelPrefix + "_store")
	appendLabel := ec.uniqueLabel(cfg.labelPrefix + "_append")
	sparseLabel := ec.uniqueLabel(cfg.labelPrefix + "_sparse")
	allowGrow := cfg.allowGrowWithinCapacity && !cfg.priorLoadBounds && !cfg.upperBoundSafe
	allowSparse := allowGrow && cfg.kind != int64(vm.FBKindMixed)

	emitBounds := func(boundsOnly bool) {
		switch {
		case cfg.upperBoundSafe:
			return
		case cfg.priorLoadBounds:
			asm.CBZ(jit.X17, cfg.missLabel)
		case boundsOnly:
			emitTypedArraySetBoundsOnlyCheck(asm, cfg.tableReg, cfg.keyReg, cfg.lenReg, lenOff, cfg.missLabel)
		case allowGrow:
			if allowSparse {
				emitTypedArraySetBoundsAppendOrSparseCheck(asm, cfg.tableReg, cfg.keyReg, cfg.lenReg, lenOff, appendLabel, sparseLabel, cfg.missLabel)
			} else {
				emitTypedArraySetBoundsOrAppendCheck(asm, cfg.tableReg, cfg.keyReg, cfg.lenReg, lenOff, appendLabel, cfg.missLabel)
			}
		default:
			asm.CMPreg(cfg.keyReg, cfg.lenReg)
			asm.BCond(jit.CondGE, cfg.missLabel)
		}
	}

	emitLoadData := func() {
		if cfg.loadDataFromTable {
			asm.LDR(cfg.dataReg, cfg.tableReg, dataOff)
		}
	}
	emitKeysDirty := func() {
		if cfg.keysDirtyAlreadyWritten {
			return
		}
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, cfg.tableReg, jit.TableOffKeysDirty)
	}
	emitSuccess := func() {
		if !cfg.fallthroughOnSuccess {
			asm.B(cfg.successLabel)
		}
	}
	emitGrowPaths := func(markDirty bool) {
		if !allowGrow {
			return
		}
		if markDirty {
			if cfg.carryLenOnGrow {
				emitTypedArraySetAppendPathCarryLenDirty(asm, cfg.tableReg, cfg.keyReg, jit.X6, cfg.lenReg, lenOff, capOff, appendLabel, cfg.missLabel, storeLabel, true)
			} else {
				emitTypedArraySetAppendPathDirty(asm, cfg.tableReg, cfg.keyReg, jit.X6, lenOff, capOff, appendLabel, cfg.missLabel, storeLabel)
			}
			if allowSparse {
				if cfg.carryLenOnGrow {
					emitTypedArraySetSparsePathCarryLenDirty(asm, cfg.tableReg, cfg.keyReg, jit.X6, cfg.lenReg, lenOff, capOff, sparseLabel, cfg.missLabel, storeLabel, true)
				} else {
					emitTypedArraySetSparsePathDirty(asm, cfg.tableReg, cfg.keyReg, jit.X6, lenOff, capOff, sparseLabel, cfg.missLabel, storeLabel)
				}
			}
			return
		}
		if cfg.carryLenOnGrow {
			emitTypedArraySetAppendPathCarryLenDirty(asm, cfg.tableReg, cfg.keyReg, jit.X6, cfg.lenReg, lenOff, capOff, appendLabel, cfg.missLabel, storeLabel, false)
		} else {
			emitTypedArraySetAppendPath(asm, cfg.tableReg, cfg.keyReg, jit.X6, lenOff, capOff, appendLabel, cfg.missLabel, storeLabel)
		}
		if allowSparse {
			if cfg.carryLenOnGrow {
				emitTypedArraySetSparsePathCarryLenDirty(asm, cfg.tableReg, cfg.keyReg, jit.X6, cfg.lenReg, lenOff, capOff, sparseLabel, cfg.missLabel, storeLabel, false)
			} else {
				emitTypedArraySetSparsePath(asm, cfg.tableReg, cfg.keyReg, jit.X6, lenOff, capOff, sparseLabel, cfg.missLabel, storeLabel)
			}
		}
	}

	switch cfg.kind {
	case int64(vm.FBKindMixed):
		valReg := ec.resolveValueNB(cfg.valueID, jit.X4)
		if valReg != jit.X4 {
			asm.MOVreg(jit.X4, valReg)
		}
		emitBounds(false)
		asm.Label(storeLabel)
		emitLoadData()
		asm.STRreg(jit.X4, cfg.dataReg, cfg.keyReg)
		emitKeysDirty()
		emitSuccess()
		emitGrowPaths(false)

	case int64(vm.FBKindInt):
		if val, ok := ec.constInts[cfg.valueID]; ok {
			asm.LoadImm64(jit.X4, val)
		} else if ec.hasReg(cfg.valueID) && ec.valueReprOf(cfg.valueID) == valueReprRawInt {
			reg := ec.physReg(cfg.valueID)
			if reg != jit.X4 {
				asm.MOVreg(jit.X4, reg)
			}
		} else if ec.irTypes[cfg.valueID] == TypeInt {
			valReg := ec.resolveValueNB(cfg.valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			asm.SBFX(jit.X4, jit.X4, 0, 48)
		} else {
			valReg := ec.resolveValueNB(cfg.valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			asm.LSRimm(jit.X5, jit.X4, 48)
			asm.MOVimm16(jit.X6, uint16(jit.NB_TagIntShr48))
			asm.CMPreg(jit.X5, jit.X6)
			asm.BCond(jit.CondNE, cfg.missLabel)
			asm.SBFX(jit.X4, jit.X4, 0, 48)
		}
		emitBounds(false)
		asm.Label(storeLabel)
		emitLoadData()
		asm.STRreg(jit.X4, cfg.dataReg, cfg.keyReg)
		emitSuccess()
		emitGrowPaths(true)

	case int64(vm.FBKindFloat):
		valueIsTypedFloat := ec.irTypes[cfg.valueID] == TypeFloat
		valueHasRawFPR := valueIsTypedFloat && ec.hasFPReg(cfg.valueID)
		if !valueHasRawFPR {
			valReg := ec.resolveValueNB(cfg.valueID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			if !valueIsTypedFloat {
				jit.EmitIsTagged(asm, jit.X4, jit.X5)
				asm.BCond(jit.CondEQ, cfg.missLabel)
			}
		}
		emitBounds(false)
		asm.Label(storeLabel)
		emitLoadData()
		if valueHasRawFPR {
			valFPR := ec.resolveRawFloat(cfg.valueID, jit.D0)
			asm.FSTRdReg(valFPR, cfg.dataReg, cfg.keyReg)
		} else {
			asm.STRreg(jit.X4, cfg.dataReg, cfg.keyReg)
		}
		emitSuccess()
		emitGrowPaths(true)

	case int64(vm.FBKindBool):
		if boolVal, ok := ec.constBools[cfg.valueID]; ok {
			asm.MOVimm16(jit.X4, uint16(boolVal+1))
			emitBounds(false)
			asm.Label(storeLabel)
			emitLoadData()
			asm.STRBreg(jit.X4, cfg.dataReg, cfg.keyReg)
			emitKeysDirty()
			emitSuccess()
			emitGrowPaths(false)
			return true
		}

		valReg := ec.resolveValueNB(cfg.valueID, jit.X4)
		if valReg != jit.X4 {
			asm.MOVreg(jit.X4, valReg)
		}
		asm.LSRimm(jit.X5, jit.X4, 48)
		asm.MOVimm16(jit.X6, uint16(jit.NB_TagBoolShr48))
		asm.CMPreg(jit.X5, jit.X6)
		boolOKLabel := ec.uniqueLabel(cfg.labelPrefix + "_bool_isbool")
		asm.BCond(jit.CondEQ, boolOKLabel)
		asm.MOVimm16(jit.X6, uint16(jit.NB_TagNilShr48))
		asm.CMPreg(jit.X5, jit.X6)
		asm.BCond(jit.CondNE, cfg.missLabel)
		asm.MOVimm16(jit.X4, 0)
		emitBounds(true)
		asm.B(storeLabel)
		asm.Label(boolOKLabel)
		asm.LoadImm64(jit.X5, 1)
		asm.ANDreg(jit.X4, jit.X4, jit.X5)
		asm.ADDimm(jit.X4, jit.X4, 1)
		emitBounds(false)
		asm.Label(storeLabel)
		emitLoadData()
		asm.STRBreg(jit.X4, cfg.dataReg, cfg.keyReg)
		emitKeysDirty()
		emitSuccess()
		emitGrowPaths(false)

	default:
		return false
	}
	return true
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
	ec.storeRawDataPtr(jit.X0, instr.ID)
}

func (ec *emitContext) emitTableArrayLoad(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("tarr_load_deopt")
	successLabel := ec.uniqueLabel("tarr_load_success")
	doneLabel := ec.uniqueLabel("tarr_load_done")

	dataReg := ec.resolveRawDataPtr(instr.Args[0].ID, jit.X2)
	lenReg := ec.resolveRawInt(instr.Args[1].ID, jit.X3)
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
		asm.CMPreg(jit.X1, lenReg)
		asm.BCond(jit.CondGE, deoptLabel)
	}

	switch instr.Aux {
	case int64(vm.FBKindMixed):
		asm.LDRreg(jit.X0, dataReg, jit.X1)
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
			jit.EmitExtractPtr(asm, jit.X0, jit.X0)
			ec.storeRawTablePtr(jit.X0, instr.ID)
		default:
			ec.storeResultNB(jit.X0, instr.ID)
		}
	case int64(vm.FBKindInt):
		asm.LDRreg(jit.X0, dataReg, jit.X1)
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
			asm.FLDRdReg(dstF, dataReg, jit.X1)
			ec.storeRawFloat(dstF, instr.ID)
		} else {
			asm.LDRreg(jit.X0, dataReg, jit.X1)
			ec.storeResultNB(jit.X0, instr.ID)
		}
	case int64(vm.FBKindBool):
		asm.LDRBreg(jit.X3, dataReg, jit.X1)
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
	missLabel := ec.uniqueLabel("tarr_store_miss")
	successLabel := ec.uniqueLabel("tarr_store_success")
	doneLabel := ec.uniqueLabel("tarr_store_done")

	tblID := instr.Args[0].ID
	allowGrow := instr.Aux2&tableArrayStoreFlagAllowGrow != 0
	needsTablePtr := tableArrayStoreNeedsTablePtr(instr.Aux, instr.Aux2)
	if needsTablePtr {
		if len(instr.Args) >= 6 && instr.Args[5] != nil {
			tblReg := ec.resolveRawTablePtr(instr.Args[5].ID, jit.X0)
			if tblReg != jit.X0 {
				asm.MOVreg(jit.X0, tblReg)
			}
		} else {
			tblReg := ec.resolveValueNB(tblID, jit.X0)
			if tblReg != jit.X0 {
				asm.MOVreg(jit.X0, tblReg)
			}
			jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		}
	}

	dataReg := ec.resolveRawDataPtr(instr.Args[1].ID, jit.X2)
	lenReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if !ec.emitTableArrayKeyToReg(instr.Args[3], missLabel) {
		ec.emitDeopt(instr)
		return
	}
	keyID := instr.Args[3].ID
	if kv, isConst := ec.constInts[keyID]; (!isConst || kv < 0) && !ec.intNonNegative(keyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, missLabel)
	}

	if !ec.emitTableArrayRawStore(tableArrayRawStoreConfig{
		labelPrefix:             "tarr_store",
		kind:                    instr.Aux,
		valueID:                 instr.Args[4].ID,
		tableReg:                jit.X0,
		keyReg:                  jit.X1,
		dataReg:                 dataReg,
		lenReg:                  lenReg,
		missLabel:               missLabel,
		successLabel:            successLabel,
		upperBoundSafe:          !allowGrow && ec.tableArrayUpperBoundSafe(instr.ID),
		allowGrowWithinCapacity: allowGrow,
		carryLenOnGrow:          allowGrow,
		fallthroughOnSuccess:    !allowGrow,
	}) {
		ec.emitDeopt(instr)
		return
	}

	asm.Label(successLabel)
	ec.recordTableArrayStoreBoundedKey(instr)
	asm.B(doneLabel)

	asm.Label(missLabel)
	if instr.Aux2&tableArrayStoreFlagExitResumeOnMiss != 0 {
		savedReprs := ec.snapshotValueReprs()
		ec.emitTableArrayStoreExit(instr)
		ec.emitUnboxRawIntRegs(savedReprs)
		ec.restoreValueReprSnapshot(savedReprs)
		ec.refreshTableArrayStoreFactsAfterExit(instr)
		asm.MOVimm16(jit.X17, 0)
		asm.B(doneLabel)
	} else {
		ec.emitPreciseDeopt(instr)
	}
	asm.Label(doneLabel)
}

func (ec *emitContext) refreshTableArrayStoreFactsAfterExit(instr *Instr) {
	if instr == nil || len(instr.Args) < 3 || instr.Args[0] == nil || instr.Args[1] == nil || instr.Args[2] == nil {
		return
	}
	dataOff, lenOff, ok := tableArrayOffsets(instr.Aux)
	if !ok {
		return
	}
	asm := ec.asm
	tblReg := ec.resolveRawTablePtr(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	asm.LDR(jit.X16, jit.X0, dataOff)
	asm.LDR(jit.X17, jit.X0, lenOff)
	ec.storeRawDataPtr(jit.X16, instr.Args[1].ID)
	ec.storeRawInt(jit.X17, instr.Args[2].ID)
}

func (ec *emitContext) emitTableArraySwap(instr *Instr) {
	if len(instr.Args) < 5 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("tarr_swap_deopt")
	doneLabel := ec.uniqueLabel("tarr_swap_done")

	ec.emitTableIntArrayKernelKeyToReg(instr.Args[3], jit.X1, deoptLabel)
	keyAID := instr.Args[3].ID
	if kv, isConst := ec.constInts[keyAID]; (!isConst || kv < 0) && !ec.intNonNegative(keyAID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[4], jit.X4, deoptLabel)
	keyBID := instr.Args[4].ID
	if kv, isConst := ec.constInts[keyBID]; (!isConst || kv < 0) && !ec.intNonNegative(keyBID) {
		asm.CMPimm(jit.X4, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	dataReg := ec.resolveRawDataPtr(instr.Args[1].ID, jit.X2)
	lenReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	asm.CMPreg(jit.X1, lenReg)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.CMPreg(jit.X4, lenReg)
	asm.BCond(jit.CondGE, deoptLabel)

	switch instr.Aux {
	case int64(vm.FBKindInt), int64(vm.FBKindFloat):
		asm.LDRreg(jit.X5, dataReg, jit.X1)
		asm.LDRreg(jit.X6, dataReg, jit.X4)
		asm.STRreg(jit.X6, dataReg, jit.X1)
		asm.STRreg(jit.X5, dataReg, jit.X4)
	default:
		ec.emitDeopt(instr)
		return
	}
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	ec.emitPreciseDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableArraySwapPairs(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	failLabel := ec.uniqueLabel("tarr_swappairs_fail")
	successNoMutLabel := ec.uniqueLabel("tarr_swappairs_success_nomut")
	successMutLabel := ec.uniqueLabel("tarr_swappairs_success_mut")
	loopLabel := ec.uniqueLabel("tarr_swappairs_loop")
	doneLabel := ec.uniqueLabel("tarr_swappairs_done")

	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[1], jit.X1, failLabel)
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[2], jit.X4, failLabel)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	var lenOff, dataOff int
	switch instr.Aux {
	case int64(vm.FBKindInt):
		asm.CMPimm(jit.X2, jit.AKInt)
		lenOff, dataOff = jit.TableOffIntArrayLen, jit.TableOffIntArray
	case int64(vm.FBKindFloat):
		asm.CMPimm(jit.X2, jit.AKFloat)
		lenOff, dataOff = jit.TableOffFloatArrayLen, jit.TableOffFloatArray
	default:
		ec.emitDeopt(instr)
		return
	}
	asm.BCond(jit.CondNE, failLabel)

	asm.CMPreg(jit.X1, jit.X4)
	asm.BCond(jit.CondGT, successNoMutLabel)
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, failLabel)
	asm.LDR(jit.X3, jit.X0, lenOff)
	asm.CMPreg(jit.X4, jit.X3)
	asm.BCond(jit.CondGE, failLabel)
	asm.ADDimm(jit.X5, jit.X4, 1)
	asm.CMPreg(jit.X5, jit.X3)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X6, jit.X0, dataOff)
	asm.CBZ(jit.X6, failLabel)

	asm.Label(loopLabel)
	asm.CMPreg(jit.X1, jit.X4)
	asm.BCond(jit.CondGT, successMutLabel)
	asm.ADDimm(jit.X5, jit.X1, 1)
	asm.LDRreg(jit.X7, jit.X6, jit.X1)
	asm.LDRreg(jit.X8, jit.X6, jit.X5)
	asm.STRreg(jit.X8, jit.X6, jit.X1)
	asm.STRreg(jit.X7, jit.X6, jit.X5)
	asm.ADDimm(jit.X1, jit.X1, 2)
	asm.B(loopLabel)

	asm.Label(successMutLabel)
	asm.MOVimm16(jit.X7, 1)
	asm.STRB(jit.X7, jit.X0, jit.TableOffKeysDirty)
	asm.Label(successNoMutLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(failLabel)
	asm.MOVreg(jit.X0, mRegTagBool)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.Label(doneLabel)
}

func tableArrayStoreNeedsTablePtr(kind, flags int64) bool {
	return flags&tableArrayStoreFlagAllowGrow != 0 ||
		kind == int64(vm.FBKindMixed) ||
		kind == int64(vm.FBKindBool)
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
	if !ok || !tableArrayNestedLoadSupported(instr.Aux, instr.Type) {
		ec.emitDeopt(instr)
		return
	}
	expectedRowKind, ok := fbKindToAK(instr.Aux)
	if !ok {
		ec.emitDeopt(instr)
		return
	}

	if instr.Aux == int64(vm.FBKindFloat) {
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
	}

	asm.Label(normalLabel)
	outerDataReg := ec.resolveRawDataPtr(instr.Args[1].ID, jit.X2)
	if outerDataReg != jit.X2 {
		asm.MOVreg(jit.X2, outerDataReg)
	}
	outerLenReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if outerLenReg != jit.X3 {
		asm.MOVreg(jit.X3, outerLenReg)
	}
	if !ec.emitTableArrayKeyToReg(instr.Args[3], deoptLabel) {
		ec.emitDeopt(instr)
		return
	}
	outerKeyID := instr.Args[3].ID
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
	innerKeyID := instr.Args[4].ID
	if kv, isConst := ec.constInts[innerKeyID]; (!isConst || kv < 0) && !ec.intNonNegative(innerKeyID) {
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, deoptLabel)
	}
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGE, deoptLabel)
	switch instr.Aux {
	case int64(vm.FBKindInt):
		asm.LDRreg(jit.X0, jit.X2, jit.X1)
		ec.storeRawInt(jit.X0, instr.ID)
	case int64(vm.FBKindFloat):
		dstF := jit.D0
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
			dstF = jit.FReg(pr.Reg)
		}
		asm.FLDRdReg(dstF, jit.X2, jit.X1)
		ec.storeRawFloat(dstF, instr.ID)
	default:
		ec.emitDeopt(instr)
		return
	}
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
	if ec.tableArrayBoundedKeys == nil {
		ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool, 1)
	}
	ec.asm.MOVimm16(jit.X17, 1)
	ec.tableArrayBoundedKeys[tableArrayBoundKey{tableID: tableValue.ID, keyID: instr.Args[2].ID}] = true
}

func (ec *emitContext) recordTableArrayStoreBoundedKey(instr *Instr) {
	if ec == nil || instr == nil || len(instr.Args) < 4 || instr.Args[0] == nil || instr.Args[3] == nil {
		return
	}
	if ec.tableArrayBoundedKeys == nil {
		ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool, 1)
	}
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
	ec.emitDynamicStringGetTableCache(instr, doneLabel)

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
	ec.emitDynamicStringSetTableCache(instr, doneLabel)

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
	// table-of-row arrays. Prefer the dense-matrix append path when its full
	// contract is already present, but let non-dense row arrays fall through to
	// the generic mixed-array append/store fast path instead of forcing one
	// exit per row.
	if instr.Aux2 == int64(vm.FBKindMixed) && ec.irTypes[instr.Args[2].ID] == TypeTable {
		denseMissLabel := ec.uniqueLabel("settable_dense_row_miss")
		ec.emitDenseMatrixRowAppendFastPath(instr, denseMissLabel, doneLabel)
		asm.Label(denseMissLabel)
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
		if !ec.emitTableArrayRawStore(tableArrayRawStoreConfig{
			labelPrefix:             "settable_mixed",
			kind:                    int64(vm.FBKindMixed),
			valueID:                 instr.Args[2].ID,
			tableReg:                jit.X0,
			keyReg:                  jit.X1,
			dataReg:                 jit.X2,
			lenReg:                  jit.X2,
			missLabel:               deoptLabel,
			successLabel:            doneLabel,
			loadDataFromTable:       true,
			priorLoadBounds:         keyBoundsAlreadyChecked,
			keysDirtyAlreadyWritten: ec.keysDirtyWritten[tblValueID],
			allowGrowWithinCapacity: true,
		}) {
			ec.emitDeopt(instr)
			return
		}
	}

	// --- ArrayInt fast path ---
	if emitIntArrayPath {
		asm.Label(intArrayLabel)
		if !ec.emitTableArrayRawStore(tableArrayRawStoreConfig{
			labelPrefix:             "settable_int",
			kind:                    int64(vm.FBKindInt),
			valueID:                 instr.Args[2].ID,
			tableReg:                jit.X0,
			keyReg:                  jit.X1,
			dataReg:                 jit.X2,
			lenReg:                  jit.X2,
			missLabel:               deoptLabel,
			successLabel:            doneLabel,
			loadDataFromTable:       true,
			priorLoadBounds:         keyBoundsAlreadyChecked,
			keysDirtyAlreadyWritten: ec.keysDirtyWritten[tblValueID],
			allowGrowWithinCapacity: true,
		}) {
			ec.emitDeopt(instr)
			return
		}
	}

	if emitFloatArrayPath {
		// --- ArrayFloat fast path ---
		asm.Label(floatArrayLabel)
		if !ec.emitTableArrayRawStore(tableArrayRawStoreConfig{
			labelPrefix:             "settable_float",
			kind:                    int64(vm.FBKindFloat),
			valueID:                 instr.Args[2].ID,
			tableReg:                jit.X0,
			keyReg:                  jit.X1,
			dataReg:                 jit.X2,
			lenReg:                  jit.X2,
			missLabel:               deoptLabel,
			successLabel:            doneLabel,
			loadDataFromTable:       true,
			priorLoadBounds:         keyBoundsAlreadyChecked,
			keysDirtyAlreadyWritten: ec.keysDirtyWritten[tblValueID],
			allowGrowWithinCapacity: true,
		}) {
			ec.emitDeopt(instr)
			return
		}
	}

	if emitBoolArrayPath {
		// --- ArrayBool fast path ---
		asm.Label(boolArrayLabel)
		if !ec.emitTableArrayRawStore(tableArrayRawStoreConfig{
			labelPrefix:             "settable_bool",
			kind:                    int64(vm.FBKindBool),
			valueID:                 instr.Args[2].ID,
			tableReg:                jit.X0,
			keyReg:                  jit.X1,
			dataReg:                 jit.X2,
			lenReg:                  jit.X2,
			missLabel:               deoptLabel,
			successLabel:            doneLabel,
			loadDataFromTable:       true,
			priorLoadBounds:         keyBoundsAlreadyChecked,
			keysDirtyAlreadyWritten: ec.keysDirtyWritten[tblValueID],
			allowGrowWithinCapacity: true,
		}) {
			ec.emitDeopt(instr)
			return
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

func (ec *emitContext) emitGuardTableKind(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	expectedKind, ok := fbKindToAK(instr.Aux)
	if !ok {
		ec.emitDeopt(instr)
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("guard_table_kind_deopt")
	doneLabel := ec.uniqueLabel("guard_table_kind_done")
	tableID := instr.Args[0].ID
	srcReg := ec.resolveValueNB(tableID, jit.X0)
	if srcReg != jit.X0 {
		asm.MOVreg(jit.X0, srcReg)
	}
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
	jit.EmitExtractPtr(asm, jit.X1, jit.X0)
	asm.CBZ(jit.X1, deoptLabel)
	asm.LDR(jit.X2, jit.X1, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, deoptLabel)
	asm.LDRB(jit.X2, jit.X1, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, expectedKind)
	asm.BCond(jit.CondNE, deoptLabel)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)
	asm.Label(deoptLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}
