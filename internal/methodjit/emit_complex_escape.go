//go:build darwin && arm64

package methodjit

import (
	"math"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitComplexEscapeInSet(instr *Instr) {
	if instr == nil || len(instr.Args) < 2 || instr.Aux <= 0 {
		return
	}
	asm := ec.asm
	loopLabel := ec.uniqueLabel("complex_escape_loop")
	escapedLabel := ec.uniqueLabel("complex_escape_escaped")
	insideLabel := ec.uniqueLabel("complex_escape_inside")
	doneLabel := ec.uniqueLabel("complex_escape_done")

	if len(instr.Args) >= 6 {
		y := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
		if y != jit.X0 {
			asm.MOVreg(jit.X0, y)
		}
		x := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
		if x != jit.X1 {
			asm.MOVreg(jit.X1, x)
		}
		ec.resolveComplexFloatArg(instr.Args[2], jit.D2)
		ec.resolveComplexFloatArg(instr.Args[3], jit.D3)
		ec.resolveComplexFloatArg(instr.Args[4], jit.D4)
		ec.resolveComplexFloatArg(instr.Args[5], jit.D5)
		asm.SCVTF(jit.D0, jit.X0)
		asm.SCVTF(jit.D1, jit.X1)
		asm.FMULd(jit.D6, jit.D2, jit.D3)
		asm.FMULd(jit.D0, jit.D0, jit.D6)
		asm.FSUBd(jit.D0, jit.D0, jit.D4)
		asm.FMULd(jit.D1, jit.D1, jit.D6)
		asm.FSUBd(jit.D1, jit.D1, jit.D5)
	} else {
		ci := ec.resolveRawFloat(instr.Args[0].ID, jit.D0)
		if ci != jit.D0 {
			asm.FMOVd(jit.D0, ci)
		}
		cr := ec.resolveRawFloat(instr.Args[1].ID, jit.D1)
		if cr != jit.D1 {
			asm.FMOVd(jit.D1, cr)
		}
	}
	loadFloat64Imm(asm, jit.D2, 0.25)
	loadFloat64Imm(asm, jit.D3, 1.0)
	loadFloat64Imm(asm, jit.D4, 0.0625)
	asm.FMULd(jit.D5, jit.D0, jit.D0)          // ci2
	asm.FSUBd(jit.D6, jit.D1, jit.D2)          // cr - 0.25
	asm.FMADDd(jit.D7, jit.D6, jit.D6, jit.D5) // q
	asm.FADDd(jit.D16, jit.D7, jit.D6)
	asm.FMULd(jit.D16, jit.D7, jit.D16)
	asm.FMULd(jit.D17, jit.D2, jit.D5)
	asm.FCMPd(jit.D16, jit.D17)
	asm.BCond(jit.CondLE, insideLabel)
	asm.FADDd(jit.D6, jit.D1, jit.D3)
	asm.FMADDd(jit.D6, jit.D6, jit.D6, jit.D5)
	asm.FCMPd(jit.D6, jit.D4)
	asm.BCond(jit.CondLE, insideLabel)
	asm.MOVimm16(jit.X17, 0)
	asm.SCVTF(jit.D2, jit.X17)
	asm.MOVimm16(jit.X17, uint16(int64(math.Float64frombits(uint64(instr.Aux2)))))
	asm.SCVTF(jit.D7, jit.X17)
	asm.MOVimm16(jit.X0, 0)

	// D0=ci, D1=cr, D2=zr, D3=zi, D7=escape limit.
	asm.FMOVd(jit.D3, jit.D2)
	asm.Label(loopLabel)
	asm.FMULd(jit.D4, jit.D2, jit.D2)          // zr2
	asm.FMSUBd(jit.D5, jit.D3, jit.D3, jit.D4) // zr2 - zi2
	asm.FADDd(jit.D5, jit.D5, jit.D1)          // tr
	asm.FMULd(jit.D6, jit.D2, jit.D3)          // zr * zi
	asm.FADDd(jit.D6, jit.D6, jit.D6)          // 2*zr*zi
	asm.FADDd(jit.D6, jit.D6, jit.D0)          // ti
	asm.FMOVd(jit.D2, jit.D5)
	asm.FMOVd(jit.D3, jit.D6)
	asm.FMULd(jit.D4, jit.D2, jit.D2)
	asm.FMADDd(jit.D4, jit.D3, jit.D3, jit.D4)
	asm.FCMPd(jit.D7, jit.D4)
	asm.BCond(jit.CondLT, escapedLabel)
	asm.ADDimm(jit.X0, jit.X0, 1)
	asm.CMPimm(jit.X0, uint16(instr.Aux))
	asm.BCond(jit.CondLT, loopLabel)

	asm.Label(insideLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	asm.B(doneLabel)
	asm.Label(escapedLabel)
	asm.MOVreg(jit.X0, mRegTagBool)
	asm.Label(doneLabel)
	ec.storeResultNB(jit.X0, instr.ID)
}

func loadFloat64Imm(asm *jit.Assembler, dst jit.FReg, f float64) {
	asm.LoadImm64(jit.X17, int64(math.Float64bits(f)))
	asm.FMOVtoFP(dst, jit.X17)
}

func (ec *emitContext) resolveComplexFloatArg(v *Value, dst jit.FReg) {
	if v == nil {
		return
	}
	if v.Def != nil && v.Def.Op == OpConstFloat {
		loadFloat64Imm(ec.asm, dst, math.Float64frombits(uint64(v.Def.Aux)))
		return
	}
	src := ec.resolveRawFloat(v.ID, dst)
	if src != dst {
		ec.asm.FMOVd(dst, src)
	}
}

func (ec *emitContext) emitComplexEscapeRowCount(instr *Instr) {
	if instr == nil || len(instr.Args) < 5 || instr.Aux <= 0 || instr.Aux2 <= 0 || instr.Aux2 > 4095 {
		return
	}
	asm := ec.asm
	y := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
	if y != jit.X0 {
		asm.MOVreg(jit.X0, y)
	}
	ec.resolveComplexFloatArg(instr.Args[1], jit.D2)
	ec.resolveComplexFloatArg(instr.Args[2], jit.D3)
	ec.resolveComplexFloatArg(instr.Args[3], jit.D4)
	ec.resolveComplexFloatArg(instr.Args[4], jit.D19)

	asm.FMULd(jit.D6, jit.D2, jit.D3) // scale = 2 / size
	asm.SCVTF(jit.D0, jit.X0)
	asm.FMULd(jit.D0, jit.D0, jit.D6)
	asm.FSUBd(jit.D0, jit.D0, jit.D4) // ci
	asm.MOVimm16(jit.X1, 0)           // x
	asm.MOVimm16(jit.X3, 0)           // count
	loadFloat64Imm(asm, jit.D18, 4.0)

	rowLoop := ec.uniqueLabel("complex_row_loop")
	rowNext := ec.uniqueLabel("complex_row_next")
	escapedLabel := ec.uniqueLabel("complex_row_escaped")
	insideLabel := ec.uniqueLabel("complex_row_inside")
	doneLabel := ec.uniqueLabel("complex_row_done")
	escapeLoop := ec.uniqueLabel("complex_row_escape_loop")

	asm.Label(rowLoop)
	asm.SCVTF(jit.D1, jit.X1)
	asm.FMULd(jit.D1, jit.D1, jit.D6)
	asm.FSUBd(jit.D1, jit.D1, jit.D19) // cr

	loadFloat64Imm(asm, jit.D2, 0.25)
	loadFloat64Imm(asm, jit.D3, 1.0)
	loadFloat64Imm(asm, jit.D4, 0.0625)
	asm.FMULd(jit.D5, jit.D0, jit.D0)
	asm.FSUBd(jit.D7, jit.D1, jit.D2)
	asm.FMADDd(jit.D16, jit.D7, jit.D7, jit.D5)
	asm.FADDd(jit.D17, jit.D16, jit.D7)
	asm.FMULd(jit.D17, jit.D16, jit.D17)
	asm.FMULd(jit.D2, jit.D2, jit.D5)
	asm.FCMPd(jit.D17, jit.D2)
	asm.BCond(jit.CondLE, insideLabel)
	asm.FADDd(jit.D7, jit.D1, jit.D3)
	asm.FMADDd(jit.D7, jit.D7, jit.D7, jit.D5)
	asm.FCMPd(jit.D7, jit.D4)
	asm.BCond(jit.CondLE, insideLabel)

	asm.MOVimm16(jit.X2, 0)
	asm.MOVimm16(jit.X17, 0)
	asm.SCVTF(jit.D2, jit.X17) // zr
	asm.FMOVd(jit.D3, jit.D2)  // zi
	asm.Label(escapeLoop)
	loadFloat64Imm(asm, jit.D18, 4.0)
	asm.FMULd(jit.D4, jit.D2, jit.D2)
	asm.FMSUBd(jit.D5, jit.D3, jit.D3, jit.D4)
	asm.FADDd(jit.D5, jit.D5, jit.D1)
	asm.FMULd(jit.D7, jit.D2, jit.D3)
	asm.FADDd(jit.D7, jit.D7, jit.D7)
	asm.FADDd(jit.D7, jit.D7, jit.D0)
	asm.FMOVd(jit.D2, jit.D5)
	asm.FMOVd(jit.D3, jit.D7)
	asm.FMULd(jit.D4, jit.D2, jit.D2)
	asm.FMADDd(jit.D4, jit.D3, jit.D3, jit.D4)
	asm.FCMPd(jit.D18, jit.D4)
	asm.BCond(jit.CondLT, escapedLabel)
	asm.ADDimm(jit.X2, jit.X2, 1)
	asm.CMPimm(jit.X2, uint16(instr.Aux))
	asm.BCond(jit.CondLT, escapeLoop)
	asm.Label(insideLabel)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.B(rowNext)
	asm.Label(escapedLabel)
	asm.Label(rowNext)
	asm.ADDimm(jit.X1, jit.X1, 1)
	asm.CMPimm(jit.X1, uint16(instr.Aux2))
	asm.BCond(jit.CondLT, rowLoop)
	asm.Label(doneLabel)
	ec.storeRawInt(jit.X3, instr.ID)
}
