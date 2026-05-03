//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func (ec *emitContext) emitProtocolConstCallIfEligible(instr *Instr) bool {
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

	for i, arg := range instr.Args {
		reg := ec.resolveValueNB(arg.ID, jit.X0)
		if reg != jit.X0 {
			asm.MOVreg(jit.X0, reg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	if !ec.protocolConstCallEntryGuarded(fact) || len(fact.IntGuardConsts) != 0 {
		deoptLabel := ec.uniqueLabel("protocol_const_call_deopt")
		doneGuardLabel := ec.uniqueLabel("protocol_const_call_guard_done")
		if !ec.protocolConstCallEntryGuarded(fact) {
			for i, constIdx := range fact.GuardConsts {
				ec.emitIndexedGlobalAddress(constIdx, deoptLabel)
				asm.LDRreg(jit.X0, jit.X16, jit.X17)
				ec.emitVMClosureProtoGuard(jit.X0, fact.GuardProtos[i], deoptLabel)
			}
		}
		for i, constIdx := range fact.IntGuardConsts {
			ec.emitIndexedGlobalAddress(constIdx, deoptLabel)
			asm.LDRreg(jit.X0, jit.X16, jit.X17)
			asm.LoadImm64(jit.X1, int64(uint64(runtime.IntValue(fact.IntGuardValues[i]))))
			asm.CMPreg(jit.X0, jit.X1)
			asm.BCond(jit.CondNE, deoptLabel)
		}
		asm.B(doneGuardLabel)
		asm.Label(deoptLabel)
		asm.LoadImm64(jit.X0, ExitDeopt)
		asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
		if ec.numericMode {
			asm.B("num_deopt_epilogue")
		} else {
			asm.B("deopt_epilogue")
		}
		asm.Label(doneGuardLabel)
	}

	for _, proto := range fact.GuardProtos {
		ec.emitProtocolConstCallEntryMark(proto)
	}

	asm.LoadImm64(jit.X0, fact.Result)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)
	return true
}

func (ec *emitContext) protocolConstCallEntryGuarded(fact ProtocolConstCallFoldFact) bool {
	if ec == nil || ec.fn == nil || len(ec.fn.ProtocolConstCallFolds) == 0 {
		return false
	}
	writes := protocolConstCallSetGlobalConsts(ec.fn)
	for _, constIdx := range fact.GuardConsts {
		if writes[constIdx] {
			return false
		}
	}
	return true
}

func (ec *emitContext) emitProtocolConstCallEntryGuards() {
	if ec == nil || ec.fn == nil || len(ec.fn.ProtocolConstCallFolds) == 0 {
		return
	}
	seen := make(map[int]*vm.FuncProto)
	writes := protocolConstCallSetGlobalConsts(ec.fn)
	for _, fact := range ec.fn.ProtocolConstCallFolds {
		if len(fact.IntGuardConsts) != 0 || len(fact.GuardConsts) != len(fact.GuardProtos) {
			continue
		}
		for i, constIdx := range fact.GuardConsts {
			if writes[constIdx] {
				continue
			}
			if _, ok := seen[constIdx]; !ok {
				seen[constIdx] = fact.GuardProtos[i]
			}
		}
	}
	if len(seen) == 0 {
		return
	}
	deoptLabel := ec.uniqueLabel("protocol_const_entry_deopt")
	doneLabel := ec.uniqueLabel("protocol_const_entry_done")
	for constIdx, proto := range seen {
		ec.globalCacheConsts = append(ec.globalCacheConsts, constIdx)
		ec.emitIndexedGlobalAddress(constIdx, deoptLabel)
		ec.asm.LDRreg(jit.X0, jit.X16, jit.X17)
		ec.emitVMClosureProtoGuard(jit.X0, proto, deoptLabel)
	}
	ec.asm.B(doneLabel)
	ec.asm.Label(deoptLabel)
	ec.asm.LoadImm64(jit.X0, ExitDeopt)
	ec.asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		ec.asm.B("num_deopt_epilogue")
	} else {
		ec.asm.B("deopt_epilogue")
	}
	ec.asm.Label(doneLabel)
}

func protocolConstCallSetGlobalConsts(fn *Function) map[int]bool {
	out := make(map[int]bool)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == OpSetGlobal {
				out[int(instr.Aux)] = true
			}
		}
	}
	return out
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
