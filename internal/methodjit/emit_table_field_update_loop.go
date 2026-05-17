//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func (ec *emitContext) emitTableFieldUpdateLoop(instr *Instr) {
	if instr == nil || len(instr.Args) < 5 {
		return
	}
	pairs := unpackTableFieldUpdatePairs(instr.Aux2)
	if len(pairs) == 0 {
		ec.emitPreciseDeopt(instr)
		return
	}
	shapeID := uint32(instr.Aux)
	maxField := 0
	for _, pair := range pairs {
		if pair.pos < 0 || pair.vel < 0 {
			ec.emitPreciseDeopt(instr)
			return
		}
		if pair.pos > maxField {
			maxField = pair.pos
		}
		if pair.vel > maxField {
			maxField = pair.vel
		}
	}

	asm := ec.asm
	dataReg := ec.resolveRawDataPtr(instr.Args[0].ID, jit.X9)
	if dataReg != jit.X9 {
		asm.MOVreg(jit.X9, dataReg)
	}
	lenReg := ec.resolveRawInt(instr.Args[1].ID, jit.X10)
	if lenReg != jit.X10 {
		asm.MOVreg(jit.X10, lenReg)
	}
	limitReg := ec.resolveRawInt(instr.Args[2].ID, jit.X11)
	if limitReg != jit.X11 {
		asm.MOVreg(jit.X11, limitReg)
	}
	scaleReg := ec.resolveRawFloat(instr.Args[3].ID, jit.D6)
	if scaleReg != jit.D6 {
		asm.FMOVd(jit.D6, scaleReg)
	}
	dampReg := ec.resolveRawFloat(instr.Args[4].ID, jit.D7)
	if dampReg != jit.D7 {
		asm.FMOVd(jit.D7, dampReg)
	}

	deoptLabel := ec.uniqueLabel("table_field_update_deopt")
	doneLabel := ec.uniqueLabel("table_field_update_done")
	validateLoop := ec.uniqueLabel("table_field_update_validate")
	validateDone := ec.uniqueLabel("table_field_update_validate_done")
	updateLoop := ec.uniqueLabel("table_field_update_loop")

	asm.CMPimm(jit.X11, 0)
	asm.BCond(jit.CondLE, doneLabel)
	asm.CMPreg(jit.X11, jit.X10)
	asm.BCond(jit.CondGE, deoptLabel)

	asm.MOVimm16(jit.X8, 1)
	asm.Label(validateLoop)
	asm.LDRreg(jit.X0, jit.X9, jit.X8)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, deoptLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, deoptLabel)
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	emitCMPWConst(asm, jit.X1, jit.X2, int64(shapeID))
	asm.BCond(jit.CondNE, deoptLabel)
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvalsLen)
	asm.LoadImm64(jit.X2, int64(maxField))
	asm.CMPreg(jit.X2, jit.X1)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.CMPreg(jit.X8, jit.X11)
	asm.BCond(jit.CondEQ, validateDone)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(validateLoop)

	asm.Label(validateDone)
	asm.MOVimm16(jit.X8, 1)
	asm.Label(updateLoop)
	asm.LDRreg(jit.X0, jit.X9, jit.X8)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDR(jit.X12, jit.X0, jit.TableOffSvals)
	for _, pair := range pairs {
		asm.FLDRd(jit.D0, jit.X12, pair.pos*jit.ValueSize)
		asm.FLDRd(jit.D1, jit.X12, pair.vel*jit.ValueSize)
		asm.FMADDd(jit.D0, jit.D1, jit.D6, jit.D0)
		asm.FSTRd(jit.D0, jit.X12, pair.pos*jit.ValueSize)
		asm.FMULd(jit.D1, jit.D1, jit.D7)
		asm.FSTRd(jit.D1, jit.X12, pair.vel*jit.ValueSize)
	}
	asm.CMPreg(jit.X8, jit.X11)
	asm.BCond(jit.CondEQ, doneLabel)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(updateLoop)

	asm.Label(deoptLabel)
	ec.emitPreciseDeopt(instr)
	asm.Label(doneLabel)
}
