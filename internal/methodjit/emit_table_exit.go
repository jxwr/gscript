//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// emitTableArrayLoadExit handles a typed-array load miss by executing the
// original dynamic GetTable operation in Go and resuming after this IR
// instruction. The receiver is recovered from data -> header -> table metadata,
// so the hot TableArrayLoad operand list stays data/len/key only.
func (ec *emitContext) emitTableArrayLoadExit(instr *Instr) bool {
	asm := ec.asm

	resultSlot, hasResultSlot := ec.slotMap[instr.ID]
	tableValue, hasTableValue := tableArrayLoadTableValue(instr)
	if !hasResultSlot || !hasTableValue || len(instr.Args) < 3 || instr.Args[2] == nil {
		ec.emitPreciseDeopt(instr)
		return false
	}

	tblReg := ec.resolveValueNB(tableValue.ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	tblSlot, hasTblSlot := ec.slotMap[tableValue.ID]
	if !hasTblSlot {
		ec.emitPreciseDeopt(instr)
		return false
	}
	asm.STR(jit.X0, mRegRegs, slotOffset(tblSlot))

	keyValue := instr.Args[2]
	keyReg := ec.resolveValueNB(keyValue.ID, jit.X0)
	if keyReg != jit.X0 {
		asm.MOVreg(jit.X0, keyReg)
	}
	keySlot, hasKeySlot := ec.slotMap[keyValue.ID]
	if !hasKeySlot {
		ec.emitPreciseDeopt(instr)
		return false
	}
	asm.STR(jit.X0, mRegRegs, slotOffset(keySlot))

	ec.recordTableArrayLoadExitResumeCheckSite(instr, resultSlot)
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpGetTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(keySlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
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

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
	return true
}

func (ec *emitContext) recordTableArrayLoadExitResumeCheckSite(instr *Instr, resultSlot int) {
	if ec.exitResumeCheck == nil || instr == nil {
		return
	}
	gprLive := ec.activeRegs
	fprLive := ec.activeFPRegs
	if gprLive[instr.ID] {
		gprLive = make(map[int]bool, len(ec.activeRegs))
		for valueID, live := range ec.activeRegs {
			if valueID != instr.ID {
				gprLive[valueID] = live
			}
		}
	}
	if fprLive[instr.ID] {
		fprLive = make(map[int]bool, len(ec.activeFPRegs))
		for valueID, live := range ec.activeFPRegs {
			if valueID != instr.ID {
				fprLive[valueID] = live
			}
		}
	}
	ec.recordExitResumeCheckSiteWithLive(instr, ExitTableExit, ec.exitResumeCheckLiveSlots(gprLive, fprLive), []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
}

// emitNewTableExit emits a table-exit for OpNewTable. Table allocation is
// complex (Go heap, slice allocation), so always exits to Go.
//
// Instr layout:
//   - Aux = array hint
//   - Aux2 = packed hash hint and array kind
func (ec *emitContext) emitNewTableExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	doneLabel := ec.uniqueLabel("newtable_done")
	missLabel := ec.uniqueLabel("newtable_cache_miss")
	hasCacheFastPath := ec.emitNewTableCacheFastPath(instr, doneLabel, missLabel)
	if hasCacheFastPath {
		asm.Label(missLabel)
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpNewTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, instr.Aux2)
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

	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
	if hasCacheFastPath {
		asm.Label(doneLabel)
	}
}

func tableExitSourcePC(instr *Instr) int64 {
	if instr != nil && instr.HasSource && instr.SourcePC >= 0 {
		return int64(instr.SourcePC)
	}
	return -1
}

// emitGetTableExit emits a table-exit for OpGetTable (dynamic key access).
//
// Instr layout:
//   - Args[0] = table value
//   - Args[1] = key value
func (ec *emitContext) emitGetTableExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	if len(instr.Args) > 0 {
		tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	if len(instr.Args) > 1 {
		keyReg := ec.resolveValueNB(instr.Args[1].ID, jit.X0)
		if keyReg != jit.X0 {
			asm.MOVreg(jit.X0, keyReg)
		}
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot := 0
	keySlot := 0
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > 1 {
		if s, ok := ec.slotMap[instr.Args[1].ID]; ok {
			keySlot = s
		}
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpGetTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(keySlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, tableExitSourcePC(instr))
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

	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

// emitSetTableExit emits a table-exit for OpSetTable (dynamic key access).
//
// Instr layout:
//   - Args[0] = table value
//   - Args[1] = key value
//   - Args[2] = value to store
func (ec *emitContext) emitSetTableExit(instr *Instr) {
	ec.emitSetTableExitArgs(instr, 0, 1, 2)
}

func (ec *emitContext) emitTableArrayStoreExit(instr *Instr) {
	ec.emitSetTableExitArgs(instr, 0, 3, 4)
}

func (ec *emitContext) emitSetTableExitArgs(instr *Instr, tableArg, keyArg, valueArg int) {
	asm := ec.asm

	if len(instr.Args) > tableArg && instr.Args[tableArg] != nil {
		tblReg := ec.resolveValueNB(instr.Args[tableArg].ID, jit.X0)
		if tblReg != jit.X0 {
			asm.MOVreg(jit.X0, tblReg)
		}
		if s, ok := ec.slotMap[instr.Args[tableArg].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	if len(instr.Args) > keyArg && instr.Args[keyArg] != nil {
		keyReg := ec.resolveValueNB(instr.Args[keyArg].ID, jit.X0)
		if keyReg != jit.X0 {
			asm.MOVreg(jit.X0, keyReg)
		}
		if s, ok := ec.slotMap[instr.Args[keyArg].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	if len(instr.Args) > valueArg && instr.Args[valueArg] != nil {
		valReg := ec.resolveValueNB(instr.Args[valueArg].ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		if s, ok := ec.slotMap[instr.Args[valueArg].ID]; ok {
			asm.STR(jit.X0, mRegRegs, slotOffset(s))
		}
	}

	tblSlot, keySlot, valSlot := 0, 0, 0
	if len(instr.Args) > tableArg && instr.Args[tableArg] != nil {
		if s, ok := ec.slotMap[instr.Args[tableArg].ID]; ok {
			tblSlot = s
		}
	}
	if len(instr.Args) > keyArg && instr.Args[keyArg] != nil {
		if s, ok := ec.slotMap[instr.Args[keyArg].ID]; ok {
			keySlot = s
		}
	}
	if len(instr.Args) > valueArg && instr.Args[valueArg] != nil {
		if s, ok := ec.slotMap[instr.Args[valueArg].ID]; ok {
			valSlot = s
		}
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, nil, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpSetTable))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(tblSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(keySlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(valSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	asm.LoadImm64(jit.X0, tableExitSourcePC(instr))
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
