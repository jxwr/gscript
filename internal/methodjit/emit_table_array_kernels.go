//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitTableBoolArrayFill(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	fallbackLabel := ec.uniqueLabel("boolfill_fallback")
	doneLabel := ec.uniqueLabel("boolfill_done")
	storeLoopLabel := ec.uniqueLabel("boolfill_loop")
	storeDoneLabel := ec.uniqueLabel("boolfill_store_done")
	strideLoopLabel := ec.uniqueLabel("boolfill_stride_loop")
	strideDoneLabel := ec.uniqueLabel("boolfill_stride_done")

	tblValueID := instr.Args[0].ID
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	if ec.tableVerified[tblValueID] || ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		ec.tableVerified[tblValueID] = true
	} else if ec.irTypes[tblValueID] == TypeTable {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, fallbackLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, fallbackLabel)
		ec.tableVerified[tblValueID] = true
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, fallbackLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, fallbackLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, fallbackLabel)
		ec.tableVerified[tblValueID] = true
	}
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondNE, fallbackLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffImap)
	asm.CBNZ(jit.X2, fallbackLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffHash)
	asm.CBNZ(jit.X2, fallbackLabel)

	if !ec.emitTableArrayKeyToReg(instr.Args[1], fallbackLabel) {
		ec.emitDeopt(instr)
		return
	}
	asm.MOVreg(jit.X7, jit.X1) // start
	if !ec.emitTableArrayKeyToReg(instr.Args[2], fallbackLabel) {
		ec.emitDeopt(instr)
		return
	}
	asm.MOVreg(jit.X3, jit.X1) // end
	if len(instr.Args) >= 4 {
		if !ec.emitTableArrayKeyToReg(instr.Args[3], fallbackLabel) {
			ec.emitDeopt(instr)
			return
		}
		asm.MOVreg(jit.X8, jit.X1) // positive stride
		asm.MOVreg(jit.X1, jit.X7) // current index
		asm.CMPreg(jit.X3, jit.X1)
		asm.BCond(jit.CondLT, doneLabel)
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondLT, fallbackLabel)
		asm.CMPimm(jit.X8, 0)
		asm.BCond(jit.CondLE, fallbackLabel)
		asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayLen)
		asm.CMPreg(jit.X3, jit.X6)
		asm.BCond(jit.CondGE, fallbackLabel)

		asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray)
		asm.MOVimm16(jit.X4, uint16(instr.Aux))
		if instr.Aux2&boolFillFlagNoStrideOverflow != 0 {
			asm.Label(strideLoopLabel)
			asm.STRBreg(jit.X4, jit.X2, jit.X1)
			asm.ADDreg(jit.X1, jit.X1, jit.X8)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondGT, strideDoneLabel)
			asm.STRBreg(jit.X4, jit.X2, jit.X1)
			asm.ADDreg(jit.X1, jit.X1, jit.X8)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondLE, strideLoopLabel)
		} else {
			asm.Label(strideLoopLabel)
			asm.STRBreg(jit.X4, jit.X2, jit.X1)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondEQ, strideDoneLabel)
			asm.MOVreg(jit.X9, jit.X1)
			asm.ADDreg(jit.X1, jit.X1, jit.X8)
			asm.CMPreg(jit.X1, jit.X9)
			asm.BCond(jit.CondLE, fallbackLabel)
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondLE, strideLoopLabel)
		}
		asm.Label(strideDoneLabel)
		asm.MOVimm16(jit.X6, 1)
		asm.STRB(jit.X6, jit.X0, jit.TableOffKeysDirty)
		asm.B(doneLabel)
	}
	asm.MOVreg(jit.X1, jit.X7) // current index
	asm.CMPreg(jit.X3, jit.X1)
	asm.BCond(jit.CondLT, doneLabel)
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, fallbackLabel)
	asm.ADDimm(jit.X5, jit.X3, 1) // needed len
	asm.CMPreg(jit.X5, jit.X3)
	asm.BCond(jit.CondLE, fallbackLabel)
	asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayCap)
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondGT, fallbackLabel)

	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray)
	asm.MOVimm16(jit.X4, uint16(instr.Aux))
	asm.Label(storeLoopLabel)
	asm.STRBreg(jit.X4, jit.X2, jit.X1)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondEQ, storeDoneLabel)
	asm.ADDimm(jit.X1, jit.X1, 1)
	asm.B(storeLoopLabel)

	asm.Label(storeDoneLabel)
	asm.MOVimm16(jit.X6, 1)
	asm.STRB(jit.X6, jit.X0, jit.TableOffKeysDirty)
	asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayLen)
	asm.CMPreg(jit.X6, jit.X5)
	asm.BCond(jit.CondGE, doneLabel)
	asm.STR(jit.X5, jit.X0, jit.TableOffBoolArrayLen)
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	ec.emitTableBoolArrayFillExit(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableBoolArrayFillExit(instr *Instr) {
	asm := ec.asm
	for i := 0; i < 4 && i < len(instr.Args); i++ {
		arg := instr.Args[i]
		if arg == nil {
			continue
		}
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		if s, ok := ec.slotMap[arg.ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot, startSlot, endSlot := 0, 0, 0
	stepSlot := 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > 1 {
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			startSlot = s
		}
	}
	if len(instr.Args) > 2 {
		if s, ok := ec.slotMap[instr.Args[2].ID]; ok {
			endSlot = s
		}
	}
	if len(instr.Args) > 3 {
		if s, ok := ec.slotMap[instr.Args[3].ID]; ok {
			stepSlot = s
		}
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, nil, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpBoolArrayFill))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(startSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(endSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	boolVal := int64(0)
	if instr.Aux == 2 {
		boolVal = 1
	}
	asm.LoadImm64(jit.X0, boolVal)
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(stepSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
	asm.Label(continueLabel)
	ec.emitReloadAllActiveRegs()

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

func (ec *emitContext) emitTableBoolArrayCount(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	fallbackLabel := ec.uniqueLabel("boolcount_fallback")
	doneLabel := ec.uniqueLabel("boolcount_done")
	quadLoopLabel := ec.uniqueLabel("boolcount_quad_loop")
	tailLabel := ec.uniqueLabel("boolcount_tail")
	loopLabel := ec.uniqueLabel("boolcount_loop")

	if !ec.emitTableArrayKeyToReg(instr.Args[1], fallbackLabel) {
		ec.emitDeopt(instr)
		return
	}
	asm.MOVreg(jit.X7, jit.X1) // start
	if !ec.emitTableArrayKeyToReg(instr.Args[2], fallbackLabel) {
		ec.emitDeopt(instr)
		return
	}
	asm.MOVreg(jit.X3, jit.X1) // end
	asm.MOVimm16(jit.X4, 0)    // count
	asm.CMPreg(jit.X3, jit.X7)
	asm.BCond(jit.CondLT, doneLabel)
	asm.CMPimm(jit.X7, 0)
	asm.BCond(jit.CondLT, fallbackLabel)

	tblValueID := instr.Args[0].ID
	tblReg := ec.resolveValueNB(tblValueID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	if ec.tableVerified[tblValueID] || ec.isLocalNewTableWithoutMetatable(instr.Args[0]) {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		ec.tableVerified[tblValueID] = true
	} else if ec.irTypes[tblValueID] == TypeTable {
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, fallbackLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, fallbackLabel)
		ec.tableVerified[tblValueID] = true
	} else {
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, fallbackLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
		asm.CBZ(jit.X0, fallbackLabel)
		asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
		asm.CBNZ(jit.X1, fallbackLabel)
		ec.tableVerified[tblValueID] = true
	}
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondNE, fallbackLabel)
	asm.LDR(jit.X6, jit.X0, jit.TableOffBoolArrayLen)
	asm.CMPreg(jit.X3, jit.X6)
	asm.BCond(jit.CondGE, fallbackLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray)
	asm.MOVreg(jit.X1, jit.X7)

	asm.SUBimm(jit.X8, jit.X3, 3)
	asm.CMPreg(jit.X1, jit.X8)
	asm.BCond(jit.CondGT, tailLabel)
	asm.Label(quadLoopLabel)
	asm.ADDreg(jit.X9, jit.X2, jit.X1)
	asm.LDRB(jit.X5, jit.X9, 0)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.LDRB(jit.X5, jit.X9, 1)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.LDRB(jit.X5, jit.X9, 2)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.LDRB(jit.X5, jit.X9, 3)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.ADDimm(jit.X1, jit.X1, 4)
	asm.CMPreg(jit.X1, jit.X8)
	asm.BCond(jit.CondLE, quadLoopLabel)

	asm.Label(tailLabel)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGT, doneLabel)
	asm.Label(loopLabel)
	asm.LDRBreg(jit.X5, jit.X2, jit.X1)
	asm.CMPimm(jit.X5, 2)
	asm.CSET(jit.X5, jit.CondEQ)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondEQ, doneLabel)
	asm.ADDimm(jit.X1, jit.X1, 1)
	asm.B(loopLabel)

	asm.Label(fallbackLabel)
	ec.emitTableBoolArrayCountExit(instr)
	asm.Label(doneLabel)
	ec.storeRawInt(jit.X4, instr.ID)
}

func (ec *emitContext) emitTableBoolArrayCountExit(instr *Instr) {
	asm := ec.asm
	for i := 0; i < 3 && i < len(instr.Args); i++ {
		arg := instr.Args[i]
		if arg == nil {
			continue
		}
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		if s, ok := ec.slotMap[arg.ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	resultSlot, hasResultSlot := ec.slotMap[instr.ID]
	if !hasResultSlot {
		ec.emitDeopt(instr)
		return
	}
	tblSlot, startSlot, endSlot := 0, 0, 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > 1 {
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			startSlot = s
		}
	}
	if len(instr.Args) > 2 {
		if s, ok := ec.slotMap[instr.Args[2].ID]; ok {
			endSlot = s
		}
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpBoolArrayCount))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(startSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(endSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("table_continue_%d", instr.ID))
	asm.Label(continueLabel)
	ec.emitReloadAllActiveRegs()
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	jit.EmitUnboxInt(asm, jit.X4, jit.X0)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

func (ec *emitContext) emitTableIntArrayReversePrefix(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm
	failLabel := ec.uniqueLabel("tarr_reverse_prefix_fail")
	successNoMutLabel := ec.uniqueLabel("tarr_reverse_prefix_success_nomut")
	successMutLabel := ec.uniqueLabel("tarr_reverse_prefix_success_mut")
	loopLabel := ec.uniqueLabel("tarr_reverse_prefix_loop")
	doneLabel := ec.uniqueLabel("tarr_reverse_prefix_done")

	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[1], jit.X1, failLabel)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondNE, failLabel)

	asm.CMPimm(jit.X1, 1)
	asm.BCond(jit.CondLE, successNoMutLabel)
	asm.LDR(jit.X3, jit.X0, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X4, jit.X0, jit.TableOffIntArray)
	asm.CBZ(jit.X4, failLabel)

	asm.MOVimm16(jit.X5, 1)
	asm.MOVreg(jit.X6, jit.X1)
	asm.Label(loopLabel)
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondGE, successMutLabel)
	asm.LDRreg(jit.X7, jit.X4, jit.X5)
	asm.LDRreg(jit.X8, jit.X4, jit.X6)
	asm.STRreg(jit.X8, jit.X4, jit.X5)
	asm.STRreg(jit.X7, jit.X4, jit.X6)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.SUBimm(jit.X6, jit.X6, 1)
	asm.B(loopLabel)

	asm.Label(successMutLabel)
	asm.MOVimm16(jit.X7, 1)
	asm.STRB(jit.X7, jit.X0, jit.TableOffKeysDirty)
	asm.Label(successNoMutLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(failLabel)
	asm.MOVreg(jit.X0, mRegTagBool)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableIntArrayCopyPrefix(instr *Instr) {
	if len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	failLabel := ec.uniqueLabel("tarr_copy_prefix_fail")
	successNoMutLabel := ec.uniqueLabel("tarr_copy_prefix_success_nomut")
	successMutLabel := ec.uniqueLabel("tarr_copy_prefix_success_mut")
	loopLabel := ec.uniqueLabel("tarr_copy_prefix_loop")
	doneLabel := ec.uniqueLabel("tarr_copy_prefix_done")

	dstReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if dstReg != jit.X0 {
		asm.MOVreg(jit.X0, dstReg)
	}
	srcReg := ec.resolveValueNB(instr.Args[1].ID, jit.X9)
	if srcReg != jit.X9 {
		asm.MOVreg(jit.X9, srcReg)
	}
	ec.emitTableIntArrayKernelKeyToReg(instr.Args[2], jit.X1, failLabel)

	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, failLabel)
	jit.EmitCheckIsTableFull(asm, jit.X9, jit.X2, jit.X3, failLabel)
	jit.EmitExtractPtr(asm, jit.X9, jit.X9)
	asm.CBZ(jit.X9, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X9, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDR(jit.X2, jit.X9, jit.TableOffLazyTree)
	asm.CBNZ(jit.X2, failLabel)
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondNE, failLabel)
	asm.LDRB(jit.X2, jit.X9, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondNE, failLabel)

	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLE, successNoMutLabel)
	asm.LDR(jit.X5, jit.X0, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X5)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X6, jit.X9, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X6)
	asm.BCond(jit.CondGE, failLabel)
	asm.LDR(jit.X7, jit.X0, jit.TableOffIntArray)
	asm.CBZ(jit.X7, failLabel)
	asm.LDR(jit.X8, jit.X9, jit.TableOffIntArray)
	asm.CBZ(jit.X8, failLabel)

	asm.MOVimm16(jit.X4, 1)
	asm.Label(loopLabel)
	asm.CMPreg(jit.X4, jit.X1)
	asm.BCond(jit.CondGT, successMutLabel)
	asm.LDRreg(jit.X3, jit.X8, jit.X4)
	asm.STRreg(jit.X3, jit.X7, jit.X4)
	asm.ADDimm(jit.X4, jit.X4, 1)
	asm.B(loopLabel)

	asm.Label(successMutLabel)
	asm.MOVimm16(jit.X3, 1)
	asm.STRB(jit.X3, jit.X0, jit.TableOffKeysDirty)
	asm.Label(successNoMutLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(failLabel)
	asm.MOVreg(jit.X0, mRegTagBool)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitTableIntArrayKernelKeyToReg(key *Value, dst jit.Reg, failLabel string) {
	if key == nil {
		ec.asm.B(failLabel)
		return
	}
	keyID := key.ID
	if kv, ok := ec.constInts[keyID]; ok {
		ec.asm.LoadImm64(dst, kv)
	} else if ec.hasReg(keyID) && ec.valueReprOf(keyID) == valueReprRawInt {
		reg := ec.physReg(keyID)
		if reg != dst {
			ec.asm.MOVreg(dst, reg)
		}
	} else if ec.irTypes[keyID] == TypeInt {
		keyReg := ec.resolveValueNB(keyID, dst)
		if keyReg != dst {
			ec.asm.MOVreg(dst, keyReg)
		}
		ec.asm.SBFX(dst, dst, 0, 48)
	} else {
		keyReg := ec.resolveValueNB(keyID, dst)
		if keyReg != dst {
			ec.asm.MOVreg(dst, keyReg)
		}
		ec.asm.LSRimm(jit.X2, dst, 48)
		ec.asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		ec.asm.CMPreg(jit.X2, jit.X3)
		ec.asm.BCond(jit.CondNE, failLabel)
		ec.asm.SBFX(dst, dst, 0, 48)
	}
}
