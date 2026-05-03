//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitWholeCallKernelOpExitIfEligible(instr *Instr) bool {
	if ec == nil || ec.fn == nil || instr == nil || ec.tailCallInstrs[instr.ID] {
		return false
	}
	if !ec.fn.WholeCallNoResultKernels[instr.ID] {
		return false
	}
	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := callResultCountFromAux2(instr.Aux2)
	if nRets != 0 || !vmWholeCallKernelArity(nArgs) {
		return false
	}

	asm := ec.asm
	for i, arg := range instr.Args {
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	ec.recordExitResumeCheckSite(instr, ExitOpExit, nil, exitResumeCheckOptions{RequireCallFunc: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(OpCall))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitOp)
	asm.LoadImm64(jit.X0, int64(funcSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitSlot)
	asm.LoadImm64(jit.X0, int64(nArgs))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)
	asm.LoadImm64(jit.X0, int64(nRets))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)
	asm.LoadImm64(jit.X0, instr.Aux2)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("op_continue_%d", instr.ID))
	asm.Label(continueLabel)
	ec.emitReloadAllActiveRegs()
	ec.invalidateCallClobberedFactsAfterResume()
	if fact, ok := ec.fn.WholeCallNoResultBatches[instr.ID]; ok && fact.ExitPC > 0 {
		if target := ec.blockLabelAtOrAfterSourcePC(fact.ExitPC); target != "" {
			noBatchLabel := ec.uniqueLabel("wholecall_no_batch")
			asm.LDR(jit.X0, mRegCtx, execCtxOffOpExitAux)
			asm.CBZ(jit.X0, noBatchLabel)
			asm.MOVimm16(jit.X0, 0)
			asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)
			asm.B(target)
			asm.Label(noBatchLabel)
		}
	}

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
	return true
}

func (ec *emitContext) blockLabelAtOrAfterSourcePC(pc int) string {
	if ec == nil || ec.fn == nil {
		return ""
	}
	var best *Block
	bestPC := int(^uint(0) >> 1)
	for _, block := range ec.fn.Blocks {
		if block == nil {
			continue
		}
		blockPC := bestPC
		for _, instr := range block.Instrs {
			if instr != nil && instr.HasSource {
				blockPC = instr.SourcePC
				break
			}
		}
		if blockPC >= pc && blockPC < bestPC {
			best = block
			bestPC = blockPC
		}
	}
	if best == nil {
		return ""
	}
	return ec.blockLabelFor(best)
}
