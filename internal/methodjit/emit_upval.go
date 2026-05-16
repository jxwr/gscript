//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func (ec *emitContext) emitInlinedGetUpval(instr *Instr) {
	if len(instr.Args) == 0 {
		ec.emitOpExit(instr)
		return
	}
	asm := ec.asm
	missLabel := ec.uniqueLabel("inl_getupval_deopt")
	doneLabel := ec.uniqueLabel("inl_getupval_done")
	closureReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if closureReg != jit.X0 {
		asm.MOVreg(jit.X0, closureReg)
	}
	ec.emitExtractVMClosurePtrOrDeopt(jit.X0, jit.X0, jit.X1, jit.X2, missLabel)
	emitLoadClosureUpvalueRef(asm, jit.X0, int(instr.Aux), inlinedClosureUpvalueCount(instr),
		jit.X3, jit.X1, jit.X2, missLabel)
	asm.LDR(jit.X0, jit.X3, 0)
	switch instr.Type {
	case TypeInt:
		emitCheckIsInt(asm, jit.X0, jit.X1)
		asm.BCond(jit.CondNE, missLabel)
		jit.EmitUnboxInt(asm, jit.X0, jit.X0)
		ec.storeRawInt(jit.X0, instr.ID)
		asm.B(doneLabel)
	case TypeFloat:
		jit.EmitIsTagged(asm, jit.X0, jit.X1)
		asm.BCond(jit.CondEQ, missLabel)
		asm.FMOVtoFP(jit.D0, jit.X0)
		ec.storeRawFloat(jit.D0, instr.ID)
		asm.B(doneLabel)
	default:
		ec.storeResultNB(jit.X0, instr.ID)
		asm.B(doneLabel)
	}
	asm.Label(missLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitInlinedSetUpval(instr *Instr) {
	if len(instr.Args) < 2 {
		ec.emitOpExit(instr)
		return
	}
	asm := ec.asm
	missLabel := ec.uniqueLabel("inl_setupval_deopt")
	doneLabel := ec.uniqueLabel("inl_setupval_done")
	closureReg := ec.resolveValueNB(instr.Args[1].ID, jit.X0)
	if closureReg != jit.X0 {
		asm.MOVreg(jit.X0, closureReg)
	}
	ec.emitExtractVMClosurePtrOrDeopt(jit.X0, jit.X0, jit.X1, jit.X2, missLabel)
	emitLoadClosureUpvalueRef(asm, jit.X0, int(instr.Aux), inlinedClosureUpvalueCount(instr),
		jit.X3, jit.X1, jit.X2, missLabel)
	valueReg := ec.resolveValueNB(instr.Args[0].ID, jit.X4)
	if valueReg != jit.X4 {
		asm.MOVreg(jit.X4, valueReg)
	}
	asm.STR(jit.X4, jit.X3, 0)
	asm.B(doneLabel)
	asm.Label(missLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitExtractVMClosurePtrOrDeopt(src, dst, scratch, tagScratch jit.Reg, missLabel string) {
	asm := ec.asm
	if src != dst {
		asm.MOVreg(dst, src)
	}
	asm.LSRimm(scratch, dst, 48)
	asm.MOVimm16(tagScratch, jit.NB_TagPtrShr48)
	asm.CMPreg(scratch, tagScratch)
	asm.BCond(jit.CondNE, missLabel)
	asm.LSRimm(scratch, dst, uint8(nbPtrSubShift))
	asm.LoadImm64(tagScratch, 0xF)
	asm.ANDreg(scratch, scratch, tagScratch)
	asm.CMPimm(scratch, nbPtrSubVMClosure)
	asm.BCond(jit.CondNE, missLabel)
	jit.EmitExtractPtr(asm, dst, dst)
}

func inlinedClosureUpvalueCount(instr *Instr) int {
	if instr == nil || instr.SourceProto == nil {
		return int(instr.Aux) + 1
	}
	return len(instr.SourceProto.Upvalues)
}
