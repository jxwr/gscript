//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
)

func (ec *emitContext) emitNewFixedTable(instr *Instr) {
	asm := ec.asm

	resultSlot, hasResultSlot := ec.slotMap[instr.ID]
	if !hasResultSlot {
		ec.emitDeopt(instr)
		return
	}
	if instr.Aux2 != 2 || len(instr.Args) != 2 {
		ec.emitDeopt(instr)
		return
	}

	doneLabel := ec.uniqueLabel("newfixed_done")
	missLabel := ec.uniqueLabel("newfixed_miss")
	if ec.emitNewFixedTable2CacheFastPath(instr, doneLabel, missLabel) {
		asm.Label(missLabel)
	}
	ec.emitNewFixedTable2Exit(instr, resultSlot)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitNewFixedTable2CacheFastPath(instr *Instr, doneLabel, missLabel string) bool {
	if ec == nil || instr == nil || instr.ID < 0 || instr.ID >= len(ec.newTableCaches) {
		return false
	}
	if ec.fn == nil || !fixedTableCtor2Cacheable(ec.fn.Proto, instr) {
		return false
	}
	asm := ec.asm

	val1Reg := ec.resolveValueNB(instr.Args[0].ID, jit.X5)
	if val1Reg != jit.X5 {
		asm.MOVreg(jit.X5, val1Reg)
	}
	val2Reg := ec.resolveValueNB(instr.Args[1].ID, jit.X6)
	if val2Reg != jit.X6 {
		asm.MOVreg(jit.X6, val2Reg)
	}
	asm.LoadImm64(jit.X7, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X5, jit.X7)
	asm.BCond(jit.CondEQ, missLabel)
	asm.CMPreg(jit.X6, jit.X7)
	asm.BCond(jit.CondEQ, missLabel)

	cacheBase := uintptr(unsafe.Pointer(&ec.newTableCaches[0]))
	entryOff := instr.ID * newTableCacheEntrySize
	asm.LoadImm64(jit.X2, int64(cacheBase))
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X2, jit.X2, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X3, int64(entryOff))
			asm.ADDreg(jit.X2, jit.X2, jit.X3)
		}
	}

	asm.LDR(jit.X0, jit.X2, newTableCacheEntryValuesOff)
	asm.CBZ(jit.X0, missLabel)
	asm.LDR(jit.X3, jit.X2, newTableCacheEntryPosOff)
	asm.LDR(jit.X4, jit.X2, newTableCacheEntryLenOff)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondGE, missLabel)
	asm.LDRreg(jit.X0, jit.X0, jit.X3)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X2, newTableCacheEntryPosOff)

	jit.EmitExtractPtr(asm, jit.X1, jit.X0)
	asm.LDR(jit.X2, jit.X1, jit.TableOffSvals)
	asm.STR(jit.X5, jit.X2, 0)
	asm.STR(jit.X6, jit.X2, jit.ValueSize)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)
	return true
}

func (ec *emitContext) emitNewFixedTable2Exit(instr *Instr, resultSlot int) {
	asm := ec.asm

	val1Slot, ok1 := fixedTableArgSlot(ec, instr, 0)
	val2Slot, ok2 := fixedTableArgSlot(ec, instr, 1)
	if !ok1 || !ok2 {
		ec.emitDeopt(instr)
		return
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpNewFixedTable2))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
	asm.LoadImm64(jit.X0, int64(val1Slot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	asm.LoadImm64(jit.X0, int64(val2Slot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	asm.LoadImm64(jit.X0, instr.Aux)
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
	ec.storeResultNB(jit.X0, instr.ID)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

func fixedTableArgSlot(ec *emitContext, instr *Instr, argIdx int) (int, bool) {
	if instr == nil || argIdx < 0 || argIdx >= len(instr.Args) || instr.Args[argIdx] == nil {
		return 0, false
	}
	slot, ok := ec.slotMap[instr.Args[argIdx].ID]
	return slot, ok
}
