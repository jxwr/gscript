//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func (ec *emitContext) emitProtocolConstCallIfEligible(instr *Instr) bool {
	// Disabled until the guard-failure fallback can prove it replays the current
	// callee after global rebinding. The analysis pass remains useful for
	// diagnostics and future lowering, but emitting it today can livelock.
	return false
	if ec == nil || ec.fn == nil || instr == nil || ec.tailCallInstrs[instr.ID] {
		return false
	}
	fact, ok := ec.fn.ProtocolConstCallFolds[instr.ID]
	if !ok || fact.CalleeProto == nil || len(fact.GuardConsts) != len(fact.GuardProtos) ||
		len(fact.IntGuardConsts) != len(fact.IntGuardValues) || len(instr.Args) == 0 {
		return false
	}

	asm := ec.asm
	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := callResultCountFromAux2(instr.Aux2)
	if nRets != 1 {
		return false
	}

	for _, constIdx := range fact.GuardConsts {
		ec.globalCacheConsts = append(ec.globalCacheConsts, constIdx)
	}
	for _, constIdx := range fact.IntGuardConsts {
		ec.globalCacheConsts = append(ec.globalCacheConsts, constIdx)
	}

	slowLabel := ec.uniqueLabel("protocol_const_call_slow")
	doneLabel := ec.uniqueLabel("protocol_const_call_done")

	for i, arg := range instr.Args {
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.emitVMClosureProtoGuard(jit.X0, fact.CalleeProto, slowLabel)

	for i, constIdx := range fact.GuardConsts {
		ec.emitIndexedGlobalAddress(constIdx, slowLabel)
		asm.LDRreg(jit.X0, jit.X16, jit.X17)
		ec.emitVMClosureProtoGuard(jit.X0, fact.GuardProtos[i], slowLabel)
	}
	for i, constIdx := range fact.IntGuardConsts {
		ec.emitIndexedGlobalAddress(constIdx, slowLabel)
		asm.LDRreg(jit.X0, jit.X16, jit.X17)
		asm.LoadImm64(jit.X1, int64(uint64(runtime.IntValue(fact.IntGuardValues[i]))))
		asm.CMPreg(jit.X0, jit.X1)
		asm.BCond(jit.CondNE, slowLabel)
	}
	for _, proto := range fact.GuardProtos {
		ec.emitProtocolConstCallEntryMark(proto)
	}

	asm.LoadImm64(jit.X0, fact.Result)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(slowLabel)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)

	asm.Label(doneLabel)
	return true
}

func (ec *emitContext) emitProtocolConstCallEntryMark(protoPtr *vm.FuncProto) {
	if protoPtr == nil {
		return
	}
	ec.asm.LoadImm64(jit.X16, int64(uintptr(unsafe.Pointer(&protoPtr.EnteredTier2))))
	ec.asm.MOVimm16(jit.X17, 1)
	ec.asm.STRB(jit.X17, jit.X16, 0)
}

func (ec *emitContext) emitVMClosureProtoGuard(valueReg jit.Reg, protoPtr *vm.FuncProto, slowLabel string) {
	asm := ec.asm
	if protoPtr == nil {
		asm.B(slowLabel)
		return
	}
	asm.LSRimm(jit.X1, valueReg, 48)
	asm.MOVimm16(jit.X2, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LSRimm(jit.X1, valueReg, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X2, 0xF)
	asm.ANDreg(jit.X1, jit.X1, jit.X2)
	asm.CMPimm(jit.X1, nbPtrSubVMClosure)
	asm.BCond(jit.CondNE, slowLabel)
	if valueReg != jit.X0 {
		asm.MOVreg(jit.X0, valueReg)
	}
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)
	asm.LoadImm64(jit.X2, int64(uintptr(unsafe.Pointer(protoPtr))))
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)
}
