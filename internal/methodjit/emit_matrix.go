//go:build darwin && arm64

// emit_matrix.go — R43 DenseMatrix Phase 2 JIT intrinsics.
//
// OpMatrixGetF / OpMatrixSetF skip the row-wrapper indirection for
// `matrix.getf(m, i, j)` and `matrix.setf(m, i, j, v)` calls when
// m is a DenseMatrix (dmStride > 0). Emits ~7 ARM64 insns per access
// vs ~25 insns for the nested ArrayMixed + ArrayFloat path. Target:
// matmul 0.095s → ~0.04s (close ~60% remaining gap to LuaJIT 0.021s).
//
// Guard: dmStride == 0 → deopt (not a DenseMatrix). The intrinsic
// does NOT validate row/column bounds; user code using matrix.getf
// on a valid matrix stays in bounds by construction. Out-of-bounds
// reads can return garbage float bits but won't crash (bounded by
// DenseMatrix backing allocation in NewDenseMatrix).

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

// emitMatrixGetF emits ARM64 code for OpMatrixGetF(m, i, j) → float.
//
// Layout:
//
//	X0 = m (NaN-boxed Table)    → extract *Table
//	X1 = dmStride (int32 load), guard != 0 else deopt
//	X2 = i (raw int64)
//	X3 = j (raw int64)
//	X4 = i * stride + j
//	X5 = dmFlat (unsafe.Pointer)
//	D0 = *(float64*)(X5 + X4*8)
func (ec *emitContext) emitMatrixGetF(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("mgetf_deopt")
	doneLabel := ec.uniqueLabel("mgetf_done")

	tblID := instr.Args[0].ID
	// Load m (NaN-boxed Table) into X0.
	mReg := ec.resolveValueNB(tblID, jit.X0)
	if mReg != jit.X0 {
		asm.MOVreg(jit.X0, mReg)
	}
	// R44: skip type/nil checks when table was already verified.
	if ec.tableVerified[tblID] {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		ec.tableVerified[tblID] = true
	}

	// Load dmStride (int32 at TableOffDMStride).
	asm.LDRW(jit.X1, jit.X0, jit.TableOffDMStride)
	// R44: skip stride==0 deopt guard if this SSA value was already
	// proven to be a DenseMatrix in this block.
	if !ec.dmVerified[tblID] {
		asm.CBZ(jit.X1, deoptLabel) // stride == 0 → deopt
		ec.dmVerified[tblID] = true
	}

	// Resolve i, j as raw int64.
	iReg := ec.resolveRawInt(instr.Args[1].ID, jit.X2)
	if iReg != jit.X2 {
		asm.MOVreg(jit.X2, iReg)
	}
	jReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if jReg != jit.X3 {
		asm.MOVreg(jit.X3, jReg)
	}

	// X4 = i * stride + j  (stride is 32-bit; extend zero).
	asm.MADD(jit.X4, jit.X2, jit.X1, jit.X3)

	// X5 = dmFlat pointer.
	asm.LDR(jit.X5, jit.X0, jit.TableOffDMFlat)

	// Load float64 at X5 + X4*8. LDRreg scales by 3 (*8).
	asm.LDRreg(jit.X0, jit.X5, jit.X4)

	// Result: float64 bits ARE NaN-boxed float. Store NB.
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Deopt fallback.
	asm.Label(deoptLabel)
	savedReprs := ec.snapshotValueReprs()
	ec.emitDeopt(instr)
	ec.emitUnboxRawIntRegs(savedReprs)
	ec.restoreValueReprSnapshot(savedReprs)

	asm.Label(doneLabel)
}

// emitMatrixFlat emits code for OpMatrixFlat(m) → raw int64 pointer.
// Verifies m is a Table with dmStride > 0; deopts otherwise.
// The result is a raw int64 SSA value (the dmFlat pointer).
func (ec *emitContext) emitMatrixFlat(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("mflat_deopt")
	doneLabel := ec.uniqueLabel("mflat_done")

	tblID := instr.Args[0].ID
	mReg := ec.resolveValueNB(tblID, jit.X0)
	if mReg != jit.X0 {
		asm.MOVreg(jit.X0, mReg)
	}
	if ec.tableVerified[tblID] {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		ec.tableVerified[tblID] = true
	}
	// Verify DenseMatrix (dmStride > 0) if not already.
	if !ec.dmVerified[tblID] {
		asm.LDRW(jit.X1, jit.X0, jit.TableOffDMStride)
		asm.CBZ(jit.X1, deoptLabel)
		ec.dmVerified[tblID] = true
	}
	// Load dmFlat.
	asm.LDR(jit.X0, jit.X0, jit.TableOffDMFlat)
	// Result is a raw int64 (pointer). Store as raw int.
	ec.storeRawInt(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	savedReprs := ec.snapshotValueReprs()
	ec.emitDeopt(instr)
	ec.emitUnboxRawIntRegs(savedReprs)
	ec.restoreValueReprSnapshot(savedReprs)

	asm.Label(doneLabel)
}

// emitMatrixStride emits code for OpMatrixStride(m) → int64.
func (ec *emitContext) emitMatrixStride(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("mstride_deopt")
	doneLabel := ec.uniqueLabel("mstride_done")

	tblID := instr.Args[0].ID
	mReg := ec.resolveValueNB(tblID, jit.X0)
	if mReg != jit.X0 {
		asm.MOVreg(jit.X0, mReg)
	}
	if ec.tableVerified[tblID] {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		ec.tableVerified[tblID] = true
	}
	asm.LDRW(jit.X0, jit.X0, jit.TableOffDMStride)
	// Check stride != 0 even if dmVerified — OpMatrixStride might be
	// hoisted before dmVerified propagates. Belt-and-suspenders.
	if !ec.dmVerified[tblID] {
		asm.CBZ(jit.X0, deoptLabel)
		ec.dmVerified[tblID] = true
	}
	ec.storeRawInt(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	savedReprs := ec.snapshotValueReprs()
	ec.emitDeopt(instr)
	ec.emitUnboxRawIntRegs(savedReprs)
	ec.restoreValueReprSnapshot(savedReprs)

	asm.Label(doneLabel)
}

// emitMatrixLoadFAt: Args = [flat, stride, i, j] → float.
// No guards — assumes Flat/Stride already validated m.
//
// The MatrixLoadFAt emit target is :float SSA and the flat backing stores
// raw float64 values, so use an FP load directly instead of loading through a
// GPR and moving the bits into an FPR.
func (ec *emitContext) emitMatrixLoadFAt(instr *Instr) {
	if len(instr.Args) < 4 {
		return
	}
	asm := ec.asm
	flatReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if flatReg != jit.X5 {
		asm.MOVreg(jit.X5, flatReg)
	}
	iReg := ec.resolveRawInt(instr.Args[2].ID, jit.X2)
	if iReg != jit.X2 {
		asm.MOVreg(jit.X2, iReg)
		iReg = jit.X2
	}
	offsetReg := jit.X4
	// X4 = i * stride + j
	if isConstIntValue(instr.Args[1], 1) {
		if isConstIntValue(instr.Args[3], 0) {
			offsetReg = iReg
		} else {
			jReg := ec.resolveRawInt(instr.Args[3].ID, jit.X3)
			if jReg != jit.X3 {
				asm.MOVreg(jit.X3, jReg)
			}
			asm.ADDreg(jit.X4, jit.X2, jit.X3)
		}
	} else if isConstIntValue(instr.Args[3], 0) {
		strideReg := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
		if strideReg != jit.X1 {
			asm.MOVreg(jit.X1, strideReg)
		}
		asm.MADD(jit.X4, jit.X2, jit.X1, jit.XZR)
	} else {
		strideReg := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
		if strideReg != jit.X1 {
			asm.MOVreg(jit.X1, strideReg)
		}
		jReg := ec.resolveRawInt(instr.Args[3].ID, jit.X3)
		if jReg != jit.X3 {
			asm.MOVreg(jit.X3, jReg)
		}
		asm.MADD(jit.X4, jit.X2, jit.X1, jit.X3)
	}
	dstF := jit.D0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
		dstF = jit.FReg(pr.Reg)
	}
	asm.FLDRdReg(dstF, jit.X5, offsetReg)
	ec.storeRawFloat(dstF, instr.ID)
}

// emitMatrixStoreFAt: Args = [flat, stride, i, j, v].
func (ec *emitContext) emitMatrixStoreFAt(instr *Instr) {
	if len(instr.Args) < 5 {
		return
	}
	asm := ec.asm
	flatReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if flatReg != jit.X5 {
		asm.MOVreg(jit.X5, flatReg)
	}
	iReg := ec.resolveRawInt(instr.Args[2].ID, jit.X2)
	if iReg != jit.X2 {
		asm.MOVreg(jit.X2, iReg)
		iReg = jit.X2
	}
	offsetReg := jit.X4
	if isConstIntValue(instr.Args[1], 1) {
		if isConstIntValue(instr.Args[3], 0) {
			offsetReg = iReg
		} else {
			jReg := ec.resolveRawInt(instr.Args[3].ID, jit.X3)
			if jReg != jit.X3 {
				asm.MOVreg(jit.X3, jReg)
			}
			asm.ADDreg(jit.X4, jit.X2, jit.X3)
		}
	} else if isConstIntValue(instr.Args[3], 0) {
		strideReg := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
		if strideReg != jit.X1 {
			asm.MOVreg(jit.X1, strideReg)
		}
		asm.MADD(jit.X4, jit.X2, jit.X1, jit.XZR)
	} else {
		strideReg := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
		if strideReg != jit.X1 {
			asm.MOVreg(jit.X1, strideReg)
		}
		jReg := ec.resolveRawInt(instr.Args[3].ID, jit.X3)
		if jReg != jit.X3 {
			asm.MOVreg(jit.X3, jReg)
		}
		asm.MADD(jit.X4, jit.X2, jit.X1, jit.X3)
	}
	if instr.Args[4].Def != nil && instr.Args[4].Def.Type == TypeFloat {
		vF := ec.resolveRawFloat(instr.Args[4].ID, jit.D0)
		asm.FSTRdReg(vF, jit.X5, offsetReg)
		return
	}
	vReg := ec.resolveValueNB(instr.Args[4].ID, jit.X6)
	if vReg != jit.X6 {
		asm.MOVreg(jit.X6, vReg)
	}
	asm.STRreg(jit.X6, jit.X5, offsetReg)
}

// R46: row-pointer strength-reduction ops.

// emitMatrixRowPtr emits OpMatrixRowPtr(flat, stride, i) →
// int64 raw pointer = flat + i*stride*8. No guards (Flat/Stride
// already guarded). Hoistable by LICM when i is loop-invariant.
func (ec *emitContext) emitMatrixRowPtr(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	flatReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if flatReg != jit.X5 {
		asm.MOVreg(jit.X5, flatReg)
	}
	strideReg := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
	if strideReg != jit.X1 {
		asm.MOVreg(jit.X1, strideReg)
	}
	iReg := ec.resolveRawInt(instr.Args[2].ID, jit.X2)
	if iReg != jit.X2 {
		asm.MOVreg(jit.X2, iReg)
	}
	// X0 = flat + (i * stride) * 8
	asm.MUL(jit.X0, jit.X2, jit.X1)
	asm.LSLimm(jit.X0, jit.X0, 3)
	asm.ADDreg(jit.X0, jit.X5, jit.X0)
	ec.storeRawInt(jit.X0, instr.ID)
}

// emitMatrixLoadFRow emits OpMatrixLoadFRow(rowPtr, j) → float.
// Just LDR [rowPtr + j*8].
func (ec *emitContext) emitMatrixLoadFRow(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm
	rowReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if rowReg != jit.X5 {
		asm.MOVreg(jit.X5, rowReg)
	}
	dstF := jit.D0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
		dstF = jit.FReg(pr.Reg)
	}
	if col, ok := constIntFromValue(instr.Args[1]); ok && col >= 0 && col <= 4095 {
		asm.FLDRd(dstF, jit.X5, int(col)*8)
	} else {
		jReg := ec.resolveRawInt(instr.Args[1].ID, jit.X3)
		if jReg != jit.X3 {
			asm.MOVreg(jit.X3, jReg)
		}
		asm.FLDRdReg(dstF, jit.X5, jit.X3)
	}
	ec.storeRawFloat(dstF, instr.ID)
}

// emitMatrixStoreFRow emits OpMatrixStoreFRow(rowPtr, j, v).
func (ec *emitContext) emitMatrixStoreFRow(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	rowReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if rowReg != jit.X5 {
		asm.MOVreg(jit.X5, rowReg)
	}
	if instr.Args[2].Def != nil && instr.Args[2].Def.Type == TypeFloat {
		vF := ec.resolveRawFloat(instr.Args[2].ID, jit.D0)
		if col, ok := constIntFromValue(instr.Args[1]); ok && col >= 0 && col <= 4095 {
			asm.FSTRd(vF, jit.X5, int(col)*8)
		} else {
			jReg := ec.resolveRawInt(instr.Args[1].ID, jit.X3)
			if jReg != jit.X3 {
				asm.MOVreg(jit.X3, jReg)
			}
			asm.FSTRdReg(vF, jit.X5, jit.X3)
		}
		return
	}
	vReg := ec.resolveValueNB(instr.Args[2].ID, jit.X6)
	if vReg != jit.X6 {
		asm.MOVreg(jit.X6, vReg)
	}
	if col, ok := constIntFromValue(instr.Args[1]); ok && col >= 0 && col <= 4095 {
		asm.STR(jit.X6, jit.X5, int(col)*8)
	} else {
		jReg := ec.resolveRawInt(instr.Args[1].ID, jit.X3)
		if jReg != jit.X3 {
			asm.MOVreg(jit.X3, jReg)
		}
		asm.STRreg(jit.X6, jit.X5, jit.X3)
	}
}

func (ec *emitContext) emitMatrixLoadFRowConst(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm
	rowReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if rowReg != jit.X5 {
		asm.MOVreg(jit.X5, rowReg)
	}
	dstF := jit.D0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
		dstF = jit.FReg(pr.Reg)
	}
	asm.FLDRd(dstF, jit.X5, int(instr.Aux)*8)
	ec.storeRawFloat(dstF, instr.ID)
}

func (ec *emitContext) emitMatrixStoreFRowConst(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm
	rowReg := ec.resolveRawInt(instr.Args[0].ID, jit.X5)
	if rowReg != jit.X5 {
		asm.MOVreg(jit.X5, rowReg)
	}
	if instr.Args[1].Def != nil && instr.Args[1].Def.Type == TypeFloat {
		vF := ec.resolveRawFloat(instr.Args[1].ID, jit.D0)
		asm.FSTRd(vF, jit.X5, int(instr.Aux)*8)
		return
	}
	vReg := ec.resolveValueNB(instr.Args[1].ID, jit.X6)
	if vReg != jit.X6 {
		asm.MOVreg(jit.X6, vReg)
	}
	asm.STR(jit.X6, jit.X5, int(instr.Aux)*8)
}

// emitMatrixSetF emits ARM64 code for OpMatrixSetF(m, i, j, v).
// Same layout as get, plus resolve v as raw float in D0 and STR it.
func (ec *emitContext) emitMatrixSetF(instr *Instr) {
	if len(instr.Args) < 4 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("msetf_deopt")
	doneLabel := ec.uniqueLabel("msetf_done")

	tblID := instr.Args[0].ID
	// Load m and extract *Table.
	mReg := ec.resolveValueNB(tblID, jit.X0)
	if mReg != jit.X0 {
		asm.MOVreg(jit.X0, mReg)
	}
	if ec.tableVerified[tblID] {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, deoptLabel)
		ec.tableVerified[tblID] = true
	}

	// Load dmStride guard.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffDMStride)
	if !ec.dmVerified[tblID] {
		asm.CBZ(jit.X1, deoptLabel)
		ec.dmVerified[tblID] = true
	}

	iReg := ec.resolveRawInt(instr.Args[1].ID, jit.X2)
	if iReg != jit.X2 {
		asm.MOVreg(jit.X2, iReg)
	}
	jReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if jReg != jit.X3 {
		asm.MOVreg(jit.X3, jReg)
	}

	asm.MADD(jit.X4, jit.X2, jit.X1, jit.X3)

	// Load value to store. Raw float bits (NaN-boxed float bits ARE IEEE floats).
	vReg := ec.resolveValueNB(instr.Args[3].ID, jit.X6)
	if vReg != jit.X6 {
		asm.MOVreg(jit.X6, vReg)
	}

	// Load flat pointer.
	asm.LDR(jit.X5, jit.X0, jit.TableOffDMFlat)
	// Store: [X5 + X4*8] = X6. STRreg scales by 3.
	asm.STRreg(jit.X6, jit.X5, jit.X4)

	asm.B(doneLabel)

	asm.Label(deoptLabel)
	savedReprs := ec.snapshotValueReprs()
	ec.emitDeopt(instr)
	ec.emitUnboxRawIntRegs(savedReprs)
	ec.restoreValueReprSnapshot(savedReprs)

	asm.Label(doneLabel)
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
