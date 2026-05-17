//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func (ec *emitContext) emitOddEvenAffineLengthTotalLoop(instr *Instr) {
	if instr == nil || len(instr.Args) < 1 {
		return
	}
	asm := ec.asm
	limit := ec.resolveRawInt(instr.Args[0].ID, jit.X9)
	if limit != jit.X9 {
		asm.MOVreg(jit.X9, limit)
	}

	outerLoop := ec.uniqueLabel("odd_even_affine_length_outer")
	innerLoop := ec.uniqueLabel("odd_even_affine_length_inner")
	oddLabel := ec.uniqueLabel("odd_even_affine_length_odd")
	innerDone := ec.uniqueLabel("odd_even_affine_length_inner_done")
	done := ec.uniqueLabel("odd_even_affine_length_done")

	asm.MOVimm16(jit.X7, 0) // total
	asm.MOVimm16(jit.X8, 2) // n

	asm.Label(outerLoop)
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondGT, done)
	asm.MOVreg(jit.X1, jit.X8) // x
	asm.MOVimm16(jit.X2, 0)    // steps

	asm.Label(innerLoop)
	asm.CMPimm(jit.X1, 1)
	asm.BCond(jit.CondEQ, innerDone)
	asm.MOVimm16(jit.X3, 1)
	asm.ANDreg(jit.X3, jit.X1, jit.X3)
	asm.CMPimm(jit.X3, 0)
	asm.BCond(jit.CondNE, oddLabel)
	asm.LSRimm(jit.X1, jit.X1, 1)
	asm.ADDimm(jit.X2, jit.X2, 1)
	asm.B(innerLoop)

	asm.Label(oddLabel)
	asm.ADDregLSL(jit.X1, jit.X1, jit.X1, 1) // 3*x
	asm.ADDimm(jit.X1, jit.X1, 1)
	asm.ADDimm(jit.X2, jit.X2, 1)
	asm.B(innerLoop)

	asm.Label(innerDone)
	asm.ADDreg(jit.X7, jit.X7, jit.X2)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(outerLoop)

	asm.Label(done)
	ec.storeRawInt(jit.X7, instr.ID)
}
