//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func (ec *emitContext) emitGcdAccumLoop(instr *Instr) {
	if instr == nil || len(instr.Args) < 6 {
		return
	}
	asm := ec.asm
	loadArg := func(idx int, dst jit.Reg) {
		src := ec.resolveRawInt(instr.Args[idx].ID, dst)
		if src != dst {
			asm.MOVreg(dst, src)
		}
	}
	loadArg(0, jit.X9)  // outer limit
	loadArg(1, jit.X10) // inner limit
	loadArg(2, jit.X11) // a multiplier
	loadArg(3, jit.X12) // a addend
	loadArg(4, jit.X13) // b multiplier
	loadArg(5, jit.X14) // b addend

	outerLoop := ec.uniqueLabel("gcd_accum_outer")
	innerLoop := ec.uniqueLabel("gcd_accum_inner")
	gcdLoop := ec.uniqueLabel("gcd_accum_gcd")
	gcdDone := ec.uniqueLabel("gcd_accum_gcd_done")
	innerDone := ec.uniqueLabel("gcd_accum_inner_done")
	done := ec.uniqueLabel("gcd_accum_done")

	asm.MOVimm16(jit.X7, 0) // total
	asm.MOVimm16(jit.X8, 1) // i

	asm.Label(outerLoop)
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondGT, done)
	asm.MADD(jit.X15, jit.X8, jit.X11, jit.X12) // a base
	asm.MOVimm16(jit.X16, 1)                    // j

	asm.Label(innerLoop)
	asm.CMPreg(jit.X16, jit.X10)
	asm.BCond(jit.CondGT, innerDone)
	asm.MOVreg(jit.X1, jit.X15)
	asm.MADD(jit.X2, jit.X16, jit.X13, jit.X14)

	asm.Label(gcdLoop)
	asm.CMPimm(jit.X2, 0)
	asm.BCond(jit.CondEQ, gcdDone)
	asm.SDIV(jit.X3, jit.X1, jit.X2)
	asm.MSUB(jit.X4, jit.X3, jit.X2, jit.X1)
	asm.MOVreg(jit.X1, jit.X2)
	asm.MOVreg(jit.X2, jit.X4)
	asm.B(gcdLoop)

	asm.Label(gcdDone)
	asm.ADDreg(jit.X7, jit.X7, jit.X1)
	asm.ADDimm(jit.X16, jit.X16, 1)
	asm.B(innerLoop)

	asm.Label(innerDone)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(outerLoop)

	asm.Label(done)
	ec.storeRawInt(jit.X7, instr.ID)
}
