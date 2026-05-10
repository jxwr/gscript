//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

const tier2SparseArrayMax = 1024 // must match runtime.sparseArrayMax

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
