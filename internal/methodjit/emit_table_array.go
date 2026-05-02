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

const (
	// tableArrayStoreFlagAllowGrow lets OpTableArrayStore use the same
	// capacity-only append/sparse typed-array path as OpSetTable. Misses still
	// precise-deopt unless tableArrayStoreFlagExitResumeOnMiss is also set.
	tableArrayStoreFlagAllowGrow int64 = 1 << iota
	// tableArrayStoreFlagExitResumeOnMiss is only safe when later code does
	// not reuse stale table-array data/len facts after RawSetInt fallback.
	tableArrayStoreFlagExitResumeOnMiss
)

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
	emitTypedArraySetAppendPathMaybeDirty(asm, tableReg, keyReg, scratchReg, 0, false, lenOff, capOff, appendLabel, deoptLabel, storeLabel, false)
}

func emitTypedArraySetAppendPathDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, appendLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetAppendPathMaybeDirty(asm, tableReg, keyReg, scratchReg, 0, false, lenOff, capOff, appendLabel, deoptLabel, storeLabel, true)
}

func emitTypedArraySetAppendPathCarryLenDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg, lenReg jit.Reg, lenOff, capOff int, appendLabel, deoptLabel, storeLabel string, markKeysDirty bool) {
	emitTypedArraySetAppendPathMaybeDirty(asm, tableReg, keyReg, scratchReg, lenReg, true, lenOff, capOff, appendLabel, deoptLabel, storeLabel, markKeysDirty)
}

func emitTypedArraySetAppendPathMaybeDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg, lenReg jit.Reg, carryLen bool, lenOff, capOff int, appendLabel, deoptLabel, storeLabel string, markKeysDirty bool) {
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
	if carryLen && scratchReg != lenReg {
		asm.MOVreg(lenReg, scratchReg)
	}
	asm.B(storeLabel)
}

// emitTypedArraySetSparsePath handles key > len when the typed backing already
// has capacity. This mirrors RawSetInt's typed sparse-expansion path and stays
// conservative by requiring empty imap/hash because it does not run absorbKeys.
func emitTypedArraySetSparsePath(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetSparsePathMaybeDirty(asm, tableReg, keyReg, scratchReg, 0, false, lenOff, capOff, sparseLabel, deoptLabel, storeLabel, false)
}

func emitTypedArraySetSparsePathDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg jit.Reg, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string) {
	emitTypedArraySetSparsePathMaybeDirty(asm, tableReg, keyReg, scratchReg, 0, false, lenOff, capOff, sparseLabel, deoptLabel, storeLabel, true)
}

func emitTypedArraySetSparsePathCarryLenDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg, lenReg jit.Reg, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string, markKeysDirty bool) {
	emitTypedArraySetSparsePathMaybeDirty(asm, tableReg, keyReg, scratchReg, lenReg, true, lenOff, capOff, sparseLabel, deoptLabel, storeLabel, markKeysDirty)
}

func emitTypedArraySetSparsePathMaybeDirty(asm *jit.Assembler, tableReg, keyReg, scratchReg, lenReg jit.Reg, carryLen bool, lenOff, capOff int, sparseLabel, deoptLabel, storeLabel string, markKeysDirty bool) {
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
	if carryLen && scratchReg != lenReg {
		asm.MOVreg(lenReg, scratchReg)
	}
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
			ec.storeResultNB(jit.X0, instr.ID)
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
		asm.MOVimm16(jit.X17, 0)
		asm.B(doneLabel)
	} else {
		ec.emitPreciseDeopt(instr)
	}
	asm.Label(doneLabel)
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

func tableArrayStoreNeedsTablePtr(kind, flags int64) bool {
	return flags&tableArrayStoreFlagAllowGrow != 0 ||
		kind == int64(vm.FBKindMixed) ||
		kind == int64(vm.FBKindBool)
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
	strideLoopLabel := ec.uniqueLabel("boolfill_stride_loop")
	strideDoneLabel := ec.uniqueLabel("boolfill_stride_done")

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
	if len(instr.Args) >= 4 {
		if !ec.emitTableArrayKeyToReg(instr.Args[3], fallbackLabel) {
			ec.emitDeopt(instr)
			return
		}
		asm.MOVreg(jit.X8, jit.X1) // positive stride
		asm.MOVreg(jit.X1, jit.X7) // current index
		asm.CMPreg(jit.X3, jit.X1)
		asm.BCond(jit.CondLT, doneLabel)
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, fallbackLabel)
		asm.CMPimm(jit.X8, 0)
		asm.BCond(jit.CondLE, fallbackLabel)
		asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayLen)
		asm.CMPreg(jit.X3, jit.X6)
		asm.BCond(jit.CondGE, fallbackLabel)

		asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray)
		asm.MOVimm16(jit.X4, uint16(instr.Aux))
		if instr.Aux2&boolFillFlagNoStrideOverflow != 0 {
			asm.Label(strideLoopLabel)
			asm.STRBreg(jit.X4, jit.X2, jit.X1)
			asm.ADDreg(jit.X1, jit.X1, jit.X8)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondGT, strideDoneLabel)
			asm.STRBreg(jit.X4, jit.X2, jit.X1)
			asm.ADDreg(jit.X1, jit.X1, jit.X8)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondLE, strideLoopLabel)
		} else {
			asm.Label(strideLoopLabel)
			asm.STRBreg(jit.X4, jit.X2, jit.X1)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondEQ, strideDoneLabel)
			asm.MOVreg(jit.X9, jit.X1)
			asm.ADDreg(jit.X1, jit.X1, jit.X8)
			asm.CMPreg(jit.X1, jit.X9)
			asm.BCond(jit.CondLE, fallbackLabel)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondLE, strideLoopLabel)
		}
		asm.Label(strideDoneLabel)
		asm.MOVimm16(jit.X6, 1)
		asm.STRB(jit.X6, jit.X0, jit.TableOffKeysDirty)
		asm.B(doneLabel)
	}
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
	for i := 0; i < 4 && i < len(instr.Args); i++ {
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
	stepSlot := 0
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
	if len(instr.Args) > 3 {
		if s, ok := ec.slotMap[instr.Args[3].ID]; ok {
			stepSlot = s
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
	asm.LoadImm64(jit.X0, int64(stepSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)
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

func (ec *emitContext) emitTableBoolArrayCount(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	fallbackLabel := ec.uniqueLabel("boolcount_fallback")
	doneLabel := ec.uniqueLabel("boolcount_done")
	quadLoopLabel := ec.uniqueLabel("boolcount_quad_loop")
	tailLabel := ec.uniqueLabel("boolcount_tail")
	loopLabel := ec.uniqueLabel("boolcount_loop")

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
	asm.MOVimm16(jit.X4, 0)    // count
	asm.CMPreg(jit.X3, jit.X7)
	asm.BCond(jit.CondLT, doneLabel)
	asm.CMPimm(jit.X7, 0)
	asm.BCond(jit.CondLT, fallbackLabel)

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
	asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayLen)
	asm.CMPreg(jit.X3, jit.X6)
	asm.BCond(jit.CondGE, fallbackLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray)
	asm.MOVreg(jit.X1, jit.X7)

	asm.SUBimm(jit.X8, jit.X3, 3)
	asm.CMPreg(jit.X1, jit.X8)
	asm.BCond(jit.CondGT, tailLabel)
	asm.Label(quadLoopLabel)
	asm.ADDreg(jit.X9, jit.X2, jit.X1)
	asm.LDRB(jit.X5, jit.X9, 0)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.LDRB(jit.X5, jit.X9, 1)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.LDRB(jit.X5, jit.X9, 2)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.LDRB(jit.X5, jit.X9, 3)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.ADDimm(jit.X1, jit.X1, 4)
	asm.CMPreg(jit.X1, jit.X8)
	asm.BCond(jit.CondLE, quadLoopLabel)

	asm.Label(tailLabel)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGT, doneLabel)
	asm.Label(loopLabel)
	asm.LDRBreg(jit.X5, jit.X2, jit.X1)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondEQ, doneLabel)
	asm.ADDimm(jit.X1, jit.X1, 1)
	asm.B(loopLabel)

	asm.Label(fallbackLabel)
	ec.emitTableBoolArrayCountExit(instr)
	asm.Label(doneLabel)
	ec.storeRawInt(jit.X4, instr.ID)
}

func (ec *emitContext) emitTableBoolArrayCountExit(instr *Instr) {
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

	resultSlot, hasResultSlot := ec.slotMap[instr.ID]
	if !hasResultSlot {
		ec.emitDeopt(instr)
		return
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

	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpBoolArrayCount))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(startSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(endSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
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
	jit.EmitUnboxInt(asm, jit.X4, jit.X0)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

func (ec *emitContext) emitTableIntArrayReversePrefix(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm
	failLabel := ec.uniqueLabel("tarr_reverse_prefix_fail")
	successNoMutLabel := ec.uniqueLabel("tarr_reverse_prefix_success_nomut")
	successMutLabel := ec.uniqueLabel("tarr_reverse_prefix_success_mut")
	loopLabel := ec.uniqueLabel("tarr_reverse_prefix_loop")
	doneLabel := ec.uniqueLabel("tarr_reverse_prefix_done")

	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[1], jit.X1, failLabel)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondNE, failLabel)

	asm.CMPimm(jit.X1, 1)
	asm.BCond(jit.CondLE, successNoMutLabel)
	asm.LDR(jit.X3, jit.X0, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X4, jit.X0, jit.TableOffIntArray)
	asm.CBZ(jit.X4, failLabel)

	asm.MOVimm16(jit.X5, 1)
	asm.MOVreg(jit.X6, jit.X1)
	asm.Label(loopLabel)
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondGE, successMutLabel)
	asm.LDRreg(jit.X7, jit.X4, jit.X5)
	asm.LDRreg(jit.X8, jit.X4, jit.X6)
	asm.STRreg(jit.X8, jit.X4, jit.X5)
	asm.STRreg(jit.X7, jit.X4, jit.X6)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.SUBimm(jit.X6, jit.X6, 1)
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

func (ec *emitContext) emitTableIntArrayCopyPrefix(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	failLabel := ec.uniqueLabel("tarr_copy_prefix_fail")
	successNoMutLabel := ec.uniqueLabel("tarr_copy_prefix_success_nomut")
	successMutLabel := ec.uniqueLabel("tarr_copy_prefix_success_mut")
	loopLabel := ec.uniqueLabel("tarr_copy_prefix_loop")
	doneLabel := ec.uniqueLabel("tarr_copy_prefix_done")

	dstReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if dstReg != jit.X0 {
		asm.MOVreg(jit.X0, dstReg)
	}
	srcReg := ec.resolveValueNB(instr.Args[1].ID, jit.X9)
	if srcReg != jit.X9 {
		asm.MOVreg(jit.X9, srcReg)
	}
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[2], jit.X1, failLabel)

	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, failLabel)
	jit.EmitCheckIsTableFull(asm, jit.X9, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X9, jit.X9)
	asm.CBZ(jit.X9, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X9, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X9, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondNE, failLabel)
	asm.LDRB(jit.X2, jit.X9, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondNE, failLabel)

	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLE, successNoMutLabel)
	asm.LDR(jit.X5, jit.X0, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X5)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X6, jit.X9, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X6)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X7, jit.X0, jit.TableOffIntArray)
	asm.CBZ(jit.X7, failLabel)
	asm.LDR(jit.X8, jit.X9, jit.TableOffIntArray)
	asm.CBZ(jit.X8, failLabel)

	asm.MOVimm16(jit.X4, 1)
	asm.Label(loopLabel)
	asm.CMPreg(jit.X4, jit.X1)
	asm.BCond(jit.CondGT, successMutLabel)
	asm.LDRreg(jit.X3, jit.X8, jit.X4)
	asm.STRreg(jit.X3, jit.X7, jit.X4)
	asm.ADDimm(jit.X4, jit.X4, 1)
	asm.B(loopLabel)

	asm.Label(successMutLabel)
	asm.MOVimm16(jit.X3, 1)
	asm.STRB(jit.X3, jit.X0, jit.TableOffKeysDirty)
	asm.Label(successNoMutLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(failLabel)
	asm.MOVreg(jit.X0, mRegTagBool)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableIntArrayKernelKeyToReg(key *Value, dst jit.Reg, failLabel string) {
	if key == nil {
		ec.asm.B(failLabel)
		return
	}
	keyID := key.ID
	if kv, ok := ec.constInts[keyID]; ok {
		ec.asm.LoadImm64(dst, kv)
	} else if ec.hasReg(keyID) && ec.valueReprOf(keyID) == valueReprRawInt {
		reg := ec.physReg(keyID)
		if reg != dst {
			ec.asm.MOVreg(dst, reg)
		}
	} else if ec.irTypes[keyID] == TypeInt {
		keyReg := ec.resolveValueNB(keyID, dst)
		if keyReg != dst {
			ec.asm.MOVreg(dst, keyReg)
		}
		ec.asm.SBFX(dst, dst, 0, 48)
	} else {
		keyReg := ec.resolveValueNB(keyID, dst)
		if keyReg != dst {
			ec.asm.MOVreg(dst, keyReg)
		}
		ec.asm.LSRimm(jit.X2, dst, 48)
		ec.asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		ec.asm.CMPreg(jit.X2, jit.X3)
		ec.asm.BCond(jit.CondNE, failLabel)
		ec.asm.SBFX(dst, dst, 0, 48)
	}
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
	outerDataReg := ec.resolveRawDataPtr(instr.Args[1].ID, jit.X2)
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

func (ec *emitContext) emitDynamicStringGetTableCache(instr *Instr, doneLabel string) {
	if !ec.shouldEmitDynamicStringKeyCache(instr) {
		return
	}
	asm := ec.asm
	keyID := instr.Args[1].ID
	keyReg := ec.resolveValueNB(keyID, jit.X1)
	if keyReg != jit.X1 {
		asm.MOVreg(jit.X1, keyReg)
	}
	missLabel := ec.uniqueLabel("gettable_string_cache_miss")
	deoptLabel := ec.uniqueLabel("gettable_string_type_deopt")
	ec.emitDynamicStringCacheOrSmallScan(instr, missLabel, func(fieldIdxReg jit.Reg) {
		asm.LDR(jit.X10, jit.X0, jit.TableOffSvals)
		asm.LDRreg(jit.X0, jit.X10, fieldIdxReg)
		ec.emitStoreDynamicStringTableLoad(instr, jit.X0, deoptLabel)
		asm.B(doneLabel)
	}, dynamicStringCacheHandlers{
		valueHit: func(valueReg jit.Reg) {
			ec.emitStoreDynamicStringTableLoad(instr, valueReg, deoptLabel)
			asm.B(doneLabel)
		},
		notFound: func() {
			asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
			ec.emitStoreDynamicStringTableLoad(instr, jit.X0, deoptLabel)
			asm.B(doneLabel)
		},
	})
	asm.Label(deoptLabel)
	ec.emitDeopt(instr)
	asm.Label(missLabel)
}

func (ec *emitContext) emitStoreDynamicStringTableLoad(instr *Instr, valReg jit.Reg, deoptLabel string) {
	asm := ec.asm
	switch instr.Type {
	case TypeInt:
		asm.LSRimm(jit.X2, valReg, 48)
		asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		asm.CMPreg(jit.X2, jit.X3)
		asm.BCond(jit.CondNE, deoptLabel)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.SBFX(jit.X0, jit.X0, 0, 48)
		ec.storeRawInt(jit.X0, instr.ID)
	case TypeFloat:
		jit.EmitIsTagged(asm, valReg, jit.X2)
		asm.BCond(jit.CondEQ, deoptLabel)
		asm.FMOVtoFP(jit.D0, valReg)
		ec.storeRawFloat(jit.D0, instr.ID)
	case TypeTable:
		jit.EmitCheckIsTableFull(asm, valReg, jit.X2, jit.X3, deoptLabel)
		ec.storeResultNB(valReg, instr.ID)
	default:
		ec.storeResultNB(valReg, instr.ID)
	}
}

func (ec *emitContext) shouldEmitDynamicStringKeyCache(instr *Instr) bool {
	if instr == nil || len(instr.Args) < 2 || !instr.HasSource || instr.SourcePC < 0 {
		return false
	}
	if ec.fn == nil || !protoHasDynamicStringKeyCacheAt(ec.fn.Proto, instr.SourcePC) {
		if ec.fn == nil || ec.fn.Proto == nil {
			return false
		}
		if instr.SourcePC < len(ec.fn.Proto.Feedback) &&
			ec.fn.Proto.Feedback[instr.SourcePC].Right == vm.FBString {
			return true
		}
		// Some late loops can compile before their dynamic key sites have
		// feedback. For reads, emit the string-key probe whenever the key is
		// not proven int; non-string keys fall through to the existing array
		// path and preserve the old fallback behavior.
		return instr.Op == OpGetTable && !tableKeyProvenInt(instr.Args[1])
	}
	return true
}

type dynamicStringCacheHandlers struct {
	valueHit func(jit.Reg)
	notFound func()
}

func (ec *emitContext) emitDynamicStringCacheOrSmallScan(instr *Instr, missLabel string, hit func(fieldIdxReg jit.Reg), options ...dynamicStringCacheHandlers) {
	asm := ec.asm
	var handlers dynamicStringCacheHandlers
	if len(options) > 0 {
		handlers = options[0]
	}

	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, missLabel)
	jit.EmitExtractPtr(asm, jit.X4, jit.X1) // X4 = *string header
	asm.CBZ(jit.X4, missLabel)
	asm.LDR(jit.X5, jit.X4, 0) // X5 = key data
	asm.LDR(jit.X6, jit.X4, 8) // X6 = key len

	asm.LDR(jit.X8, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDRW(jit.X7, jit.X0, jit.TableOffShapeID)
	smapCacheLabel := ec.uniqueLabel("dyn_string_smap_cache")
	asm.CBZ(jit.X7, smapCacheLabel)

	scanLabel := ec.uniqueLabel("dyn_string_scan")
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineTableStringKeyCache)
	asm.CBZ(jit.X3, scanLabel)
	entryOff := instr.SourcePC * runtime.TableStringKeyCacheWays * tableStringKeyCacheEntrySize
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X3, jit.X3, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X8, int64(entryOff))
			asm.ADDreg(jit.X3, jit.X3, jit.X8)
		}
	}

	cacheLoopLabel := ec.uniqueLabel("dyn_string_cache_loop")
	cacheNextLabel := ec.uniqueLabel("dyn_string_cache_next")
	asm.MOVimm16(jit.X9, 0)
	asm.Label(cacheLoopLabel)
	asm.LDR(jit.X10, jit.X3, tableStringKeyCacheEntryKeyData)
	asm.CMPreg(jit.X10, jit.X5)
	asm.BCond(jit.CondNE, cacheNextLabel)
	asm.LDR(jit.X10, jit.X3, tableStringKeyCacheEntryKeyLen)
	asm.CMPreg(jit.X10, jit.X6)
	asm.BCond(jit.CondNE, cacheNextLabel)
	asm.LDRW(jit.X10, jit.X3, tableStringKeyCacheEntryShapeID)
	asm.CMPreg(jit.X10, jit.X7)
	asm.BCond(jit.CondNE, cacheNextLabel)
	asm.LDR(jit.X11, jit.X3, tableStringKeyCacheEntryFieldIdx)
	asm.LDR(jit.X10, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X11, jit.X10)
	asm.BCond(jit.CondGE, scanLabel)
	hit(jit.X11)

	asm.Label(cacheNextLabel)
	asm.ADDimm(jit.X3, jit.X3, uint16(tableStringKeyCacheEntrySize))
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.CMPimm(jit.X9, runtime.TableStringKeyCacheWays)
	asm.BCond(jit.CondLT, cacheLoopLabel)

	// Cache associativity is deliberately small. On polymorphic shaped tables
	// (for example, several tables sharing the same key set in different append
	// orders), avoid a per-lookup exit by scanning the small shaped string-key
	// slice natively. Large smap/hash-mode tables keep shapeID zero and fall
	// through to the normal table exit.
	asm.Label(scanLabel)
	asm.LDR(jit.X10, jit.X0, jit.TableOffSkeysLen)
	emptyShapeLabel := missLabel
	if handlers.notFound != nil {
		emptyShapeLabel = ec.uniqueLabel("dyn_string_scan_empty")
	}
	asm.CBZ(jit.X10, emptyShapeLabel)
	asm.LDR(jit.X11, jit.X0, jit.TableOffSkeys)
	asm.CBZ(jit.X11, missLabel)

	scanLoopLabel := ec.uniqueLabel("dyn_string_scan_loop")
	scanNextLabel := ec.uniqueLabel("dyn_string_scan_next")
	byteLoopLabel := ec.uniqueLabel("dyn_string_scan_bytes")
	foundLabel := ec.uniqueLabel("dyn_string_scan_found")
	asm.MOVimm16(jit.X9, 0) // field index
	asm.Label(scanLoopLabel)
	asm.CMPreg(jit.X9, jit.X10)
	missingLabel := missLabel
	if handlers.notFound != nil {
		missingLabel = ec.uniqueLabel("dyn_string_scan_missing")
	}
	asm.BCond(jit.CondGE, missingLabel)
	asm.LSLimm(jit.X12, jit.X9, 4) // Go string header is two machine words.
	asm.ADDreg(jit.X12, jit.X11, jit.X12)
	asm.LDR(jit.X13, jit.X12, 0) // candidate data
	asm.LDR(jit.X14, jit.X12, 8) // candidate len
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, scanNextLabel)
	asm.CMPreg(jit.X13, jit.X5)
	asm.BCond(jit.CondEQ, foundLabel)
	asm.CBZ(jit.X14, foundLabel)
	asm.MOVimm16(jit.X15, 0) // byte index
	asm.Label(byteLoopLabel)
	asm.LDRBreg(jit.X16, jit.X13, jit.X15)
	asm.LDRBreg(jit.X17, jit.X5, jit.X15)
	asm.CMPreg(jit.X16, jit.X17)
	asm.BCond(jit.CondNE, scanNextLabel)
	asm.ADDimm(jit.X15, jit.X15, 1)
	asm.CMPreg(jit.X15, jit.X14)
	asm.BCond(jit.CondLT, byteLoopLabel)

	asm.Label(foundLabel)
	asm.MOVreg(jit.X11, jit.X9)
	hit(jit.X11)

	asm.Label(scanNextLabel)
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.B(scanLoopLabel)
	if handlers.notFound != nil {
		asm.Label(emptyShapeLabel)
		handlers.notFound()
		asm.Label(missingLabel)
		handlers.notFound()
	}

	asm.Label(smapCacheLabel)
	if handlers.valueHit == nil {
		asm.B(missLabel)
		return
	}
	asm.LDR(jit.X8, jit.X0, jit.TableOffStringLookupCache)
	asm.CBZ(jit.X8, missLabel)
	asm.LDR(jit.X3, jit.X8, jit.StringLookupCacheOffEntries)
	asm.CBZ(jit.X3, missLabel)
	asm.LDR(jit.X10, jit.X8, jit.StringLookupCacheOffMask)

	asm.LSRimm(jit.X9, jit.X5, 4)
	asm.LSRimm(jit.X11, jit.X5, 12)
	asm.EORreg(jit.X9, jit.X9, jit.X11)
	asm.EORreg(jit.X9, jit.X9, jit.X6)
	asm.ANDreg(jit.X9, jit.X9, jit.X10)

	smapLoopLabel := ec.uniqueLabel("dyn_string_smap_loop")
	smapNextLabel := ec.uniqueLabel("dyn_string_smap_next")
	asm.MOVimm16(jit.X13, 0)
	asm.Label(smapLoopLabel)
	asm.ADDreg(jit.X11, jit.X9, jit.X13)
	asm.ANDreg(jit.X11, jit.X11, jit.X10)
	asm.ADDregLSL(jit.X12, jit.X11, jit.X11, 1) // idx * 3
	asm.LSLimm(jit.X12, jit.X12, 4)             // idx * 48
	asm.ADDreg(jit.X12, jit.X3, jit.X12)
	asm.LDRB(jit.X14, jit.X12, jit.StringLookupCacheEntryOffValid)
	asm.CBZ(jit.X14, missLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffKeyData)
	asm.CMPreg(jit.X14, jit.X5)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffKeyLen)
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.LDR(jit.X0, jit.X12, jit.StringLookupCacheEntryOffValue)
	handlers.valueHit(jit.X0)

	asm.Label(smapNextLabel)
	asm.ADDimm(jit.X13, jit.X13, 1)
	asm.CMPimm(jit.X13, runtime.StringLookupCacheProbeLimit)
	asm.BCond(jit.CondLT, smapLoopLabel)
	asm.B(missLabel)
}

func tableExitSourcePC(instr *Instr) int64 {
	if instr != nil && instr.HasSource && instr.SourcePC >= 0 {
		return int64(instr.SourcePC)
	}
	return -1
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
	asm.LoadImm64(jit.X0, tableExitSourcePC(instr))
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

func (ec *emitContext) emitDynamicStringSetTableCache(instr *Instr, doneLabel string) {
	if !ec.shouldEmitDynamicStringKeyCache(instr) || len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	keyID := instr.Args[1].ID
	keyReg := ec.resolveValueNB(keyID, jit.X1)
	if keyReg != jit.X1 {
		asm.MOVreg(jit.X1, keyReg)
	}
	missLabel := ec.uniqueLabel("settable_string_cache_miss")
	ec.emitDynamicStringCacheOrSmallScan(instr, missLabel, func(fieldIdxReg jit.Reg) {
		valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
		if valReg != jit.X4 {
			asm.MOVreg(jit.X4, valReg)
		}
		asm.LoadImm64(jit.X5, nb64(jit.NB_ValNil))
		asm.CMPreg(jit.X4, jit.X5)
		asm.BCond(jit.CondEQ, missLabel)
		asm.LDR(jit.X10, jit.X0, jit.TableOffSvals)
		asm.STRreg(jit.X4, jit.X10, fieldIdxReg)
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
		asm.B(doneLabel)
	})
	asm.Label(missLabel)
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
	ec.emitSetTableExitArgs(instr, 0, 1, 2)
}

func (ec *emitContext) emitTableArrayStoreExit(instr *Instr) {
	ec.emitSetTableExitArgs(instr, 0, 3, 4)
}

func (ec *emitContext) emitSetTableExitArgs(instr *Instr, tableArg, keyArg, valueArg int) {
	asm := ec.asm

	// Store table arg to its home slot.
	if len(instr.Args) > tableArg && instr.Args[tableArg] != nil {
		tblReg := ec.resolveValueNB(instr.Args[tableArg].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		if s, ok := ec.slotMap[instr.Args[tableArg].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	// Store key arg to its home slot.
	if len(instr.Args) > keyArg && instr.Args[keyArg] != nil {
		keyReg := ec.resolveValueNB(instr.Args[keyArg].ID, jit.X0)
		if keyReg != jit.X0 {
			asm.MOVreg(jit.X0, keyReg)
		}
		if s, ok := ec.slotMap[instr.Args[keyArg].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	// Store value arg to its home slot.
	if len(instr.Args) > valueArg && instr.Args[valueArg] != nil {
		valReg := ec.resolveValueNB(instr.Args[valueArg].ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		if s, ok := ec.slotMap[instr.Args[valueArg].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot, keySlot, valSlot := 0, 0, 0
	if len(instr.Args) > tableArg && instr.Args[tableArg] != nil {
		if s, ok := ec.slotMap[instr.Args[tableArg].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > keyArg && instr.Args[keyArg] != nil {
		if s, ok := ec.slotMap[instr.Args[keyArg].ID]; ok {
			keySlot = s
		}
	}
	if len(instr.Args) > valueArg && instr.Args[valueArg] != nil {
		if s, ok := ec.slotMap[instr.Args[valueArg].ID]; ok {
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
	asm.LoadImm64(jit.X0, tableExitSourcePC(instr))
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

	// Record for deferred resume.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}
