//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

func (ec *emitContext) emitNewFixedTable(instr *Instr) {
	asm := ec.asm

	resultSlot, hasResultSlot := ec.slotMap[instr.ID]
	if !hasResultSlot {
		ec.emitDeopt(instr)
		return
	}
	if instr.Aux2 != 2 || len(instr.Args) != 2 {
		ec.emitNewFixedTableN(instr, resultSlot)
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

func (ec *emitContext) emitNewFixedTableN(instr *Instr, resultSlot int) {
	if instr == nil || instr.Aux2 <= 2 || len(instr.Args) != int(instr.Aux2) {
		ec.emitDeopt(instr)
		return
	}
	doneLabel := ec.uniqueLabel("newfixedn_done")
	missLabel := ec.uniqueLabel("newfixedn_miss")
	if ec.emitNewFixedTableNCacheFastPath(instr, doneLabel, missLabel) {
		ec.asm.Label(missLabel)
	}
	ec.emitNewFixedTableNExit(instr, resultSlot)
	ec.asm.Label(doneLabel)
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
	val1NilLabel := ec.uniqueLabel("newfixed_val1_nil")
	emptyLabel := ec.uniqueLabel("newfixed_empty")
	asm.BCond(jit.CondEQ, val1NilLabel)
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

	asm.Label(val1NilLabel)
	asm.CMPreg(jit.X6, jit.X7)
	asm.BCond(jit.CondEQ, emptyLabel)
	asm.B(missLabel)

	asm.Label(emptyLabel)
	asm.LoadImm64(jit.X2, int64(cacheBase))
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X2, jit.X2, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X3, int64(entryOff))
			asm.ADDreg(jit.X2, jit.X2, jit.X3)
		}
	}
	asm.LDR(jit.X0, jit.X2, newTableCacheEntryEmptyValuesOff)
	asm.CBZ(jit.X0, missLabel)
	asm.LDR(jit.X3, jit.X2, newTableCacheEntryEmptyPosOff)
	asm.LDR(jit.X4, jit.X2, newTableCacheEntryEmptyLenOff)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondGE, missLabel)
	asm.LDRreg(jit.X0, jit.X0, jit.X3)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X2, newTableCacheEntryEmptyPosOff)
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

func (ec *emitContext) emitNewFixedTableNCacheFastPath(instr *Instr, doneLabel, missLabel string) bool {
	if ec == nil || instr == nil || instr.ID < 0 || instr.ID >= len(ec.newTableCaches) {
		return false
	}
	if ec.fn == nil || !fixedTableCtorNCacheable(ec.fn.Proto, instr) {
		return false
	}
	slots := fixedTableArgSlots(ec, instr)
	if len(slots) != len(instr.Args) {
		return false
	}
	asm := ec.asm

	nilBits := nb64(jit.NB_ValNil)
	for i, arg := range instr.Args {
		valReg := ec.resolveValueNB(arg.ID, jit.X5)
		if valReg != jit.X5 {
			asm.MOVreg(jit.X5, valReg)
		}
		asm.STR(jit.X5, mRegRegs, slotOffset(slots[i]))
		asm.LoadImm64(jit.X6, nilBits)
		asm.CMPreg(jit.X5, jit.X6)
		asm.BCond(jit.CondEQ, missLabel)
	}

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
	for i, slot := range slots {
		asm.LDR(jit.X5, mRegRegs, slotOffset(slot))
		asm.STR(jit.X5, jit.X2, i*jit.ValueSize)
	}
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)
	return true
}

func (ec *emitContext) emitNewFixedTableNExit(instr *Instr, resultSlot int) {
	asm := ec.asm

	slots := fixedTableArgSlots(ec, instr)
	if len(slots) != len(instr.Args) {
		ec.emitDeopt(instr)
		return
	}
	if ec.fixedTableArgSlots != nil {
		ec.fixedTableArgSlots[instr.ID] = append([]int(nil), slots...)
	}
	for i, arg := range instr.Args {
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(slots[i]))
	}

	ec.recordExitResumeCheckSite(instr, ExitTableExit, []int{resultSlot}, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(TableOpNewFixedTableN))
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
}

func fixedTableArgSlots(ec *emitContext, instr *Instr) []int {
	if ec == nil || instr == nil || len(instr.Args) == 0 || len(instr.Args) > runtime.SmallFieldCap {
		return nil
	}
	slots := make([]int, len(instr.Args))
	for i := range instr.Args {
		slot, ok := fixedTableArgSlot(ec, instr, i)
		if !ok {
			return nil
		}
		slots[i] = slot
	}
	return slots
}

func fixedTableArgSlot(ec *emitContext, instr *Instr, argIdx int) (int, bool) {
	if instr == nil || argIdx < 0 || argIdx >= len(instr.Args) || instr.Args[argIdx] == nil {
		return 0, false
	}
	slot, ok := ec.slotMap[instr.Args[argIdx].ID]
	return slot, ok
}
