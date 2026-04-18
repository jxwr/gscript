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
)

// emitMatrixGetF emits ARM64 code for OpMatrixGetF(m, i, j) → float.
//
// Layout:
//   X0 = m (NaN-boxed Table)    → extract *Table
//   X1 = dmStride (int32 load), guard != 0 else deopt
//   X2 = i (raw int64)
//   X3 = j (raw int64)
//   X4 = i * stride + j
//   X5 = dmFlat (unsafe.Pointer)
//   D0 = *(float64*)(X5 + X4*8)
func (ec *emitContext) emitMatrixGetF(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	deoptLabel := ec.uniqueLabel("mgetf_deopt")
	doneLabel := ec.uniqueLabel("mgetf_done")

	// Load m (NaN-boxed Table) into X0.
	mReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if mReg != jit.X0 {
		asm.MOVreg(jit.X0, mReg)
	}
	// Extract *Table pointer (assumes caller passed a Table; guard via dmStride below).
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)

	// Load dmStride (int32 at TableOffDMStride).
	asm.LDRW(jit.X1, jit.X0, jit.TableOffDMStride)
	asm.CBZ(jit.X1, deoptLabel) // stride == 0 → not a DenseMatrix → deopt

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
	asm.MUL(jit.X4, jit.X2, jit.X1)
	asm.ADDreg(jit.X4, jit.X4, jit.X3)

	// X5 = dmFlat pointer.
	asm.LDR(jit.X5, jit.X0, jit.TableOffDMFlat)

	// Load float64 at X5 + X4*8. LDRreg scales by 3 (*8).
	asm.LDRreg(jit.X0, jit.X5, jit.X4)

	// Result: float64 bits ARE NaN-boxed float. Store NB.
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Deopt fallback.
	asm.Label(deoptLabel)
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitDeopt(instr)
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
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

	// Load m and extract *Table.
	mReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if mReg != jit.X0 {
		asm.MOVreg(jit.X0, mReg)
	}
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, deoptLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)

	// Load dmStride guard.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffDMStride)
	asm.CBZ(jit.X1, deoptLabel)

	iReg := ec.resolveRawInt(instr.Args[1].ID, jit.X2)
	if iReg != jit.X2 {
		asm.MOVreg(jit.X2, iReg)
	}
	jReg := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
	if jReg != jit.X3 {
		asm.MOVreg(jit.X3, jReg)
	}

	asm.MUL(jit.X4, jit.X2, jit.X1)
	asm.ADDreg(jit.X4, jit.X4, jit.X3)

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
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitDeopt(instr)
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
}
