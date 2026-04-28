//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func (ec *emitContext) emitShiftAddOverflowVersion(spec *shiftAddOverflowVersion) {
	if spec == nil {
		return
	}
	asm := ec.asm

	asm.Label(ec.blockLabelFor(spec.entry))
	ec.currentBlockID = spec.entry.ID

	leftReg := jit.X20
	rightReg := jit.X21
	counterReg := jit.X22
	boundReg := jit.X23
	sumReg := jit.X28

	guardOK := ec.uniqueLabel("ov_shiftadd_guard_ok")
	asm.LDR(jit.X0, mRegRegs, slotOffset(spec.boundParamSlot))
	emitCheckIsInt(asm, jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, guardOK)
	ec.emitDeopt(spec.cond)

	asm.Label(guardOK)
	jit.EmitUnboxInt(asm, boundReg, jit.X0)
	switch {
	case spec.boundAdjust > 0:
		asm.ADDimm(boundReg, boundReg, uint16(spec.boundAdjust))
	case spec.boundAdjust < 0:
		asm.SUBimm(boundReg, boundReg, uint16(-spec.boundAdjust))
	}
	asm.LoadImm64(leftReg, spec.leftInitConst)
	asm.LoadImm64(rightReg, spec.rightInitConst)
	asm.LoadImm64(counterReg, spec.counterInitConst)

	rawLoop := ec.uniqueLabel("ov_shiftadd_raw_loop")
	rawShortLoop := ec.uniqueLabel("ov_shiftadd_raw_short_loop")
	rawLongLoop := ec.uniqueLabel("ov_shiftadd_raw_long_loop")
	rawExit := ec.uniqueLabel("ov_shiftadd_raw_exit")
	overflow := ec.uniqueLabel("ov_shiftadd_overflow")
	knownOverflow := ec.uniqueLabel("ov_shiftadd_known_overflow")
	floatLoop := ec.uniqueLabel("ov_shiftadd_float_loop")
	floatBody := ec.uniqueLabel("ov_shiftadd_float_body")
	floatExit := ec.uniqueLabel("ov_shiftadd_float_exit")
	overflowIntExit := ec.uniqueLabel("ov_shiftadd_overflow_int_exit")

	if spec.hasCheckFreePrefix {
		asm.CMPimm(boundReg, uint16(shiftAddSafeBoundForCond(spec)))
		asm.BCond(jit.CondLE, rawShortLoop)

		asm.Label(rawLongLoop)
		asm.ADDimm(counterReg, counterReg, uint16(spec.step))
		asm.CMPimm(counterReg, uint16(spec.safeLastCounter))
		asm.BCond(jit.CondGT, knownOverflow)
		addStart := len(asm.Code())
		asm.ADDreg(sumReg, leftReg, rightReg)
		ec.recordCustomInstrRange(spec.add, spec.body, addStart, len(asm.Code()), "normal")
		asm.MOVreg(leftReg, rightReg)
		asm.MOVreg(rightReg, sumReg)
		jumpStart := len(asm.Code())
		asm.B(rawLongLoop)
		ec.recordCustomInstrRange(blockTerminator(spec.body), spec.body, jumpStart, len(asm.Code()), "normal")

		asm.Label(rawShortLoop)
		asm.ADDimm(counterReg, counterReg, uint16(spec.step))
		asm.CMPreg(counterReg, boundReg)
		emitShiftAddLoopExitBranch(asm, spec.cond.Op, rawExit)
		addStart = len(asm.Code())
		asm.ADDreg(sumReg, leftReg, rightReg)
		ec.recordCustomInstrRange(spec.add, spec.body, addStart, len(asm.Code()), "normal")
		asm.MOVreg(leftReg, rightReg)
		asm.MOVreg(rightReg, sumReg)
		jumpStart = len(asm.Code())
		asm.B(rawShortLoop)
		ec.recordCustomInstrRange(blockTerminator(spec.body), spec.body, jumpStart, len(asm.Code()), "normal")

		asm.Label(knownOverflow)
		asm.ADDreg(sumReg, leftReg, rightReg)
	} else {
		asm.Label(rawLoop)
		asm.ADDimm(counterReg, counterReg, uint16(spec.step))
		asm.CMPreg(counterReg, boundReg)
		emitShiftAddLoopExitBranch(asm, spec.cond.Op, rawExit)
		addStart := len(asm.Code())
		asm.ADDreg(sumReg, leftReg, rightReg)
		asm.SBFX(jit.X0, sumReg, 0, 48)
		asm.CMPreg(jit.X0, sumReg)
		ec.recordCustomInstrRange(spec.add, spec.body, addStart, len(asm.Code()), "normal")
		asm.BCond(jit.CondNE, overflow)
		asm.MOVreg(leftReg, rightReg)
		asm.MOVreg(rightReg, sumReg)
		jumpStart := len(asm.Code())
		asm.B(rawLoop)
		ec.recordCustomInstrRange(blockTerminator(spec.body), spec.body, jumpStart, len(asm.Code()), "normal")
	}

	asm.Label(overflow)
	asm.SCVTF(jit.D1, sumReg)
	asm.ADDimm(counterReg, counterReg, uint16(spec.step))
	asm.CMPreg(counterReg, boundReg)
	emitShiftAddLoopExitBranch(asm, spec.cond.Op, overflowIntExit)
	asm.SCVTF(jit.D0, rightReg)
	asm.B(floatBody)

	asm.Label(floatLoop)
	asm.ADDimm(counterReg, counterReg, uint16(spec.step))
	asm.CMPreg(counterReg, boundReg)
	emitShiftAddLoopExitBranch(asm, spec.cond.Op, floatExit)
	asm.Label(floatBody)
	asm.FADDd(jit.D2, jit.D0, jit.D1)
	asm.FMOVd(jit.D0, jit.D1)
	asm.FMOVd(jit.D1, jit.D2)
	asm.B(floatLoop)

	asm.Label(rawExit)
	jit.EmitBoxIntFast(asm, jit.X0, leftReg, mRegTagInt)
	ec.emitReturnBoxedInX0()

	asm.Label(floatExit)
	asm.FMOVtoGP(jit.X0, jit.D0)
	ec.emitReturnBoxedInX0()

	asm.Label(overflowIntExit)
	jit.EmitBoxIntFast(asm, jit.X0, rightReg, mRegTagInt)
	ec.emitReturnBoxedInX0()
}

func (ec *emitContext) emitReturnBoxedInX0() {
	ec.asm.STR(jit.X0, mRegRegs, 0)
	ec.asm.STR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	ec.asm.LDR(jit.X1, mRegCtx, execCtxOffCallMode)
	ec.asm.CBNZ(jit.X1, "t2_direct_epilogue")
	ec.asm.B("epilogue")
}

func (ec *emitContext) emitShiftAddOverflowVersionDirect(spec *shiftAddOverflowVersion) {
	if spec == nil {
		return
	}
	asm := ec.asm
	ctxReg := jit.X9
	regsReg := jit.X10
	tagReg := jit.X11
	leftReg := jit.X4
	rightReg := jit.X5
	counterReg := jit.X6
	boundReg := jit.X7
	sumReg := jit.X8

	asm.Label("t2_direct_entry")
	ec.emitTier2EntryMark()
	asm.MOVreg(ctxReg, jit.X0)
	asm.LDR(regsReg, ctxReg, execCtxOffRegs)
	asm.LoadImm64(tagReg, nb64(jit.NB_TagInt))

	guardOK := ec.uniqueLabel("ov_shiftadd_direct_guard_ok")
	asm.LDR(jit.X0, regsReg, slotOffset(spec.boundParamSlot))
	emitCheckIsInt(asm, jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, guardOK)
	asm.LoadImm64(jit.X0, int64(spec.cond.ID))
	asm.STR(jit.X0, ctxReg, execCtxOffDeoptInstrID)
	asm.LoadImm64(jit.X0, ExitDeopt)
	asm.STR(jit.X0, ctxReg, execCtxOffExitCode)
	asm.RET()

	asm.Label(guardOK)
	jit.EmitUnboxInt(asm, boundReg, jit.X0)
	switch {
	case spec.boundAdjust > 0:
		asm.ADDimm(boundReg, boundReg, uint16(spec.boundAdjust))
	case spec.boundAdjust < 0:
		asm.SUBimm(boundReg, boundReg, uint16(-spec.boundAdjust))
	}
	asm.LoadImm64(leftReg, spec.leftInitConst)
	asm.LoadImm64(rightReg, spec.rightInitConst)
	asm.LoadImm64(counterReg, spec.counterInitConst)

	rawLoop := ec.uniqueLabel("ov_shiftadd_direct_raw_loop")
	rawShortLoop := ec.uniqueLabel("ov_shiftadd_direct_raw_short_loop")
	rawLongLoop := ec.uniqueLabel("ov_shiftadd_direct_raw_long_loop")
	rawExit := ec.uniqueLabel("ov_shiftadd_direct_raw_exit")
	overflow := ec.uniqueLabel("ov_shiftadd_direct_overflow")
	knownOverflow := ec.uniqueLabel("ov_shiftadd_direct_known_overflow")
	floatLoop := ec.uniqueLabel("ov_shiftadd_direct_float_loop")
	floatBody := ec.uniqueLabel("ov_shiftadd_direct_float_body")
	floatExit := ec.uniqueLabel("ov_shiftadd_direct_float_exit")
	overflowIntExit := ec.uniqueLabel("ov_shiftadd_direct_overflow_int_exit")

	if spec.hasCheckFreePrefix {
		asm.CMPimm(boundReg, uint16(shiftAddSafeBoundForCond(spec)))
		asm.BCond(jit.CondLE, rawShortLoop)

		asm.Label(rawLongLoop)
		asm.ADDimm(counterReg, counterReg, uint16(spec.step))
		asm.CMPimm(counterReg, uint16(spec.safeLastCounter))
		asm.BCond(jit.CondGT, knownOverflow)
		asm.ADDreg(sumReg, leftReg, rightReg)
		asm.MOVreg(leftReg, rightReg)
		asm.MOVreg(rightReg, sumReg)
		asm.B(rawLongLoop)

		asm.Label(rawShortLoop)
		asm.ADDimm(counterReg, counterReg, uint16(spec.step))
		asm.CMPreg(counterReg, boundReg)
		emitShiftAddLoopExitBranch(asm, spec.cond.Op, rawExit)
		asm.ADDreg(sumReg, leftReg, rightReg)
		asm.MOVreg(leftReg, rightReg)
		asm.MOVreg(rightReg, sumReg)
		asm.B(rawShortLoop)

		asm.Label(knownOverflow)
		asm.ADDreg(sumReg, leftReg, rightReg)
	} else {
		asm.Label(rawLoop)
		asm.ADDimm(counterReg, counterReg, uint16(spec.step))
		asm.CMPreg(counterReg, boundReg)
		emitShiftAddLoopExitBranch(asm, spec.cond.Op, rawExit)
		asm.ADDreg(sumReg, leftReg, rightReg)
		asm.SBFX(jit.X0, sumReg, 0, 48)
		asm.CMPreg(jit.X0, sumReg)
		asm.BCond(jit.CondNE, overflow)
		asm.MOVreg(leftReg, rightReg)
		asm.MOVreg(rightReg, sumReg)
		asm.B(rawLoop)
	}

	asm.Label(overflow)
	asm.SCVTF(jit.D1, sumReg)
	asm.ADDimm(counterReg, counterReg, uint16(spec.step))
	asm.CMPreg(counterReg, boundReg)
	emitShiftAddLoopExitBranch(asm, spec.cond.Op, overflowIntExit)
	asm.SCVTF(jit.D0, rightReg)
	asm.B(floatBody)

	asm.Label(floatLoop)
	asm.ADDimm(counterReg, counterReg, uint16(spec.step))
	asm.CMPreg(counterReg, boundReg)
	emitShiftAddLoopExitBranch(asm, spec.cond.Op, floatExit)
	asm.Label(floatBody)
	asm.FADDd(jit.D2, jit.D0, jit.D1)
	asm.FMOVd(jit.D0, jit.D1)
	asm.FMOVd(jit.D1, jit.D2)
	asm.B(floatLoop)

	asm.Label(rawExit)
	jit.EmitBoxIntFast(asm, jit.X0, leftReg, tagReg)
	ec.emitDirectLeafReturn(regsReg, ctxReg)

	asm.Label(floatExit)
	asm.FMOVtoGP(jit.X0, jit.D0)
	ec.emitDirectLeafReturn(regsReg, ctxReg)

	asm.Label(overflowIntExit)
	jit.EmitBoxIntFast(asm, jit.X0, rightReg, tagReg)
	ec.emitDirectLeafReturn(regsReg, ctxReg)
}

func (ec *emitContext) emitDirectLeafReturn(regsReg, ctxReg jit.Reg) {
	ec.asm.STR(jit.X0, regsReg, 0)
	ec.asm.STR(jit.X0, ctxReg, execCtxOffBaselineReturnValue)
	ec.asm.MOVimm16(jit.X1, 0)
	ec.asm.STR(jit.X1, ctxReg, execCtxOffExitCode)
	ec.asm.RET()
}

func emitShiftAddLoopExitBranch(asm *jit.Assembler, cond Op, exitLabel string) {
	if cond == OpLtInt {
		asm.BCond(jit.CondGE, exitLabel)
		return
	}
	asm.BCond(jit.CondGT, exitLabel)
}

func shiftAddSafeBoundForCond(spec *shiftAddOverflowVersion) int64 {
	if spec.cond.Op == OpLtInt {
		return spec.firstOverflowCounter
	}
	return spec.safeLastCounter
}

func (ec *emitContext) recordCustomInstrRange(instr *Instr, block *Block, start, end int, pass string) {
	if instr == nil || block == nil || end <= start {
		return
	}
	ec.instrCodeRanges = append(ec.instrCodeRanges, InstrCodeRange{
		InstrID:   instr.ID,
		BlockID:   block.ID,
		CodeStart: start,
		CodeEnd:   end,
		Pass:      pass,
	})
}
