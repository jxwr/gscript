//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

const maxInt48StringSplitNative = int64(1<<47 - 1)

func (ec *emitContext) emitStringSplitSubstrNative(instr *Instr) {
	if instr == nil || ec.fn == nil || len(instr.Args) < 4 {
		ec.emitStringFormatConstExit(instr)
		return
	}
	aux := int(instr.Aux)
	if aux < 0 || aux >= len(ec.fn.StringSplitSubSpecs) {
		ec.emitStringFormatConstExit(instr)
		return
	}
	spec := ec.fn.StringSplitSubSpecs[aux]
	if spec.TokenIndex < 1 || spec.Start < 1 || (spec.HasEnd && spec.End < 1) {
		ec.emitStringFormatConstExit(instr)
		return
	}
	if !runtime.NativeStringArenaEnsure() {
		ec.emitStringFormatConstExit(instr)
		return
	}

	ec.emitSpillAndClearActiveRegsForNativeHelper()

	asm := ec.asm
	slowLabel := ec.uniqueLabel("splitsub_slow")
	doneLabel := ec.uniqueLabel("splitsub_done")

	lastCalleeArg := len(instr.Args) - 3
	for i := 0; i <= lastCalleeArg; i++ {
		callee := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if callee != jit.X0 {
			asm.MOVreg(jit.X0, callee)
		}
		if i == 0 {
			ec.emitStdNativeFunctionGuard(jit.X0, runtime.NativeKindStdStringSplit, runtime.StdStringSplitIdentityPtr(), slowLabel)
		} else {
			ec.emitStdNativeFunctionGuard(jit.X0, runtime.NativeKindStdStringSub, runtime.StdStringSubIdentityPtr(), slowLabel)
		}
	}

	sepVal := ec.resolveValueNB(instr.Args[len(instr.Args)-1].ID, jit.X1)
	if sepVal != jit.X1 {
		asm.MOVreg(jit.X1, sepVal)
	}
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, jit.X1)
	asm.LDR(jit.X3, jit.X2, 0)
	asm.LDR(jit.X5, jit.X2, 8)
	asm.CMPimm(jit.X5, 1)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDRB(jit.X6, jit.X3, 0)

	srcVal := ec.resolveValueNB(instr.Args[len(instr.Args)-2].ID, jit.X1)
	if srcVal != jit.X1 {
		asm.MOVreg(jit.X1, srcVal)
	}
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, jit.X1)
	asm.LDR(jit.X4, jit.X2, 0)
	asm.LDR(jit.X5, jit.X2, 8)

	findTokenLabel := ec.uniqueLabel("splitsub_find_token")
	findEndLabel := ec.uniqueLabel("splitsub_find_end")
	endReadyLabel := ec.uniqueLabel("splitsub_end_ready")
	endCapOKLabel := ec.uniqueLabel("splitsub_end_cap_ok")
	copyLoopLabel := ec.uniqueLabel("splitsub_copy_loop")
	copyDoneLabel := ec.uniqueLabel("splitsub_copy_done")
	arenaCASLoopLabel := ec.uniqueLabel("splitsub_arena_cas")
	arenaNoSpaceLabel := ec.uniqueLabel("splitsub_arena_full")

	asm.MOVimm16(jit.X7, 0)
	if spec.TokenIndex > 1 {
		asm.MOVimm16(jit.X8, 1)
		asm.MOVimm16(jit.X9, 0)
		asm.LoadImm64(jit.X10, spec.TokenIndex)
		asm.Label(findTokenLabel)
		asm.CMPreg(jit.X9, jit.X5)
		asm.BCond(jit.CondGE, slowLabel)
		asm.LDRBreg(jit.X11, jit.X4, jit.X9)
		asm.ADDimm(jit.X9, jit.X9, 1)
		asm.CMPreg(jit.X11, jit.X6)
		asm.BCond(jit.CondNE, findTokenLabel)
		asm.MOVreg(jit.X7, jit.X9)
		asm.ADDimm(jit.X8, jit.X8, 1)
		asm.CMPreg(jit.X8, jit.X10)
		asm.BCond(jit.CondLT, findTokenLabel)
	}

	asm.MOVreg(jit.X9, jit.X7)
	asm.Label(findEndLabel)
	asm.CMPreg(jit.X9, jit.X5)
	asm.BCond(jit.CondGE, endReadyLabel)
	asm.LDRBreg(jit.X11, jit.X4, jit.X9)
	asm.CMPreg(jit.X11, jit.X6)
	asm.BCond(jit.CondEQ, endReadyLabel)
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.B(findEndLabel)
	asm.Label(endReadyLabel)

	ec.emitAddConst(jit.X12, jit.X7, int(spec.Start-1), jit.X17)
	if spec.HasEnd {
		ec.emitAddConst(jit.X13, jit.X7, int(spec.End), jit.X17)
		asm.CMPreg(jit.X13, jit.X9)
		asm.BCond(jit.CondLE, endCapOKLabel)
		asm.MOVreg(jit.X13, jit.X9)
		asm.Label(endCapOKLabel)
	} else {
		asm.MOVreg(jit.X13, jit.X9)
	}
	asm.CMPreg(jit.X12, jit.X13)
	asm.BCond(jit.CondGE, slowLabel)
	asm.SUBreg(jit.X15, jit.X13, jit.X12)

	asm.ADDimm(jit.X16, jit.X15, 31)
	asm.LoadImm64(jit.X17, -16)
	asm.ANDreg(jit.X16, jit.X16, jit.X17)
	asm.LoadImm64(jit.X17, int64(uintptr(unsafe.Pointer(runtime.NativeStringArenaCursorPtr()))))
	asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(runtime.NativeStringArenaEndPtr()))))
	asm.LDR(jit.X3, jit.X3, 0)
	asm.Label(arenaCASLoopLabel)
	asm.LDAXR(jit.X0, jit.X17)
	asm.ADDreg(jit.X5, jit.X0, jit.X16)
	asm.CMPreg(jit.X5, jit.X3)
	asm.BCond(jit.CondHI, arenaNoSpaceLabel)
	asm.STLXR(jit.X6, jit.X5, jit.X17)
	asm.CBNZ(jit.X6, arenaCASLoopLabel)
	asm.B(arenaNoSpaceLabel + "_done")
	asm.Label(arenaNoSpaceLabel)
	asm.CLREX()
	asm.B(slowLabel)
	asm.Label(arenaNoSpaceLabel + "_done")

	asm.ADDimm(jit.X5, jit.X0, 16)
	asm.STR(jit.X5, jit.X0, 0)
	asm.STR(jit.X15, jit.X0, 8)
	asm.ADDreg(jit.X12, jit.X4, jit.X12)
	asm.MOVimm16(jit.X8, 0)
	asm.Label(copyLoopLabel)
	asm.CMPreg(jit.X8, jit.X15)
	asm.BCond(jit.CondGE, copyDoneLabel)
	asm.LDRBreg(jit.X9, jit.X12, jit.X8)
	asm.STRBreg(jit.X9, jit.X5, jit.X8)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(copyLoopLabel)
	asm.Label(copyDoneLabel)

	asm.LoadImm64(jit.X1, nb64(jit.NB_TagPtr|(1<<jit.NB_PtrSubShift)))
	asm.ORRreg(jit.X0, jit.X0, jit.X1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(slowLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitStringSplitSubstrNumberNative(instr *Instr) {
	if instr == nil || ec.fn == nil || len(instr.Args) < 5 {
		ec.emitStringFormatConstExit(instr)
		return
	}
	aux := int(instr.Aux)
	if aux < 0 || aux >= len(ec.fn.StringSplitSubSpecs) {
		ec.emitStringFormatConstExit(instr)
		return
	}
	spec := ec.fn.StringSplitSubSpecs[aux]
	if spec.TokenIndex < 1 || spec.Start < 1 || (spec.HasEnd && spec.End < 1) {
		ec.emitStringFormatConstExit(instr)
		return
	}

	ec.emitSpillAndClearActiveRegsForNativeHelper()

	asm := ec.asm
	slowLabel := ec.uniqueLabel("splitnum_slow")
	nilLabel := ec.uniqueLabel("splitnum_nil")
	doneLabel := ec.uniqueLabel("splitnum_done")

	lastCalleeArg := len(instr.Args) - 3
	for i := 0; i <= lastCalleeArg; i++ {
		callee := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if callee != jit.X0 {
			asm.MOVreg(jit.X0, callee)
		}
		switch {
		case i == 0:
			ec.emitStdNativeFunctionGuard(jit.X0, runtime.NativeKindStdStringSplit, runtime.StdStringSplitIdentityPtr(), slowLabel)
		case i == lastCalleeArg:
			ec.emitStdNativeFunctionGuard(jit.X0, runtime.NativeKindStdToNumber, runtime.StdToNumberIdentityPtr(), slowLabel)
		default:
			ec.emitStdNativeFunctionGuard(jit.X0, runtime.NativeKindStdStringSub, runtime.StdStringSubIdentityPtr(), slowLabel)
		}
	}

	sepVal := ec.resolveValueNB(instr.Args[len(instr.Args)-1].ID, jit.X1)
	if sepVal != jit.X1 {
		asm.MOVreg(jit.X1, sepVal)
	}
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, jit.X1)
	asm.LDR(jit.X3, jit.X2, 0)
	asm.LDR(jit.X5, jit.X2, 8)
	asm.CMPimm(jit.X5, 1)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDRB(jit.X6, jit.X3, 0)

	srcVal := ec.resolveValueNB(instr.Args[len(instr.Args)-2].ID, jit.X1)
	if srcVal != jit.X1 {
		asm.MOVreg(jit.X1, srcVal)
	}
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, jit.X1)
	asm.LDR(jit.X4, jit.X2, 0)
	asm.LDR(jit.X5, jit.X2, 8)

	findTokenLabel := ec.uniqueLabel("splitnum_find_token")
	findEndLabel := ec.uniqueLabel("splitnum_find_end")
	endReadyLabel := ec.uniqueLabel("splitnum_end_ready")
	endCapOKLabel := ec.uniqueLabel("splitnum_end_cap_ok")
	parseLoopLabel := ec.uniqueLabel("splitnum_parse_loop")
	parseDoneLabel := ec.uniqueLabel("splitnum_parse_done")
	signMinusLabel := ec.uniqueLabel("splitnum_sign_minus")
	signPlusLabel := ec.uniqueLabel("splitnum_sign_plus")
	afterSignLabel := ec.uniqueLabel("splitnum_after_sign")
	negativeLabel := ec.uniqueLabel("splitnum_negative")
	storeLabel := ec.uniqueLabel("splitnum_store")

	asm.MOVimm16(jit.X7, 0)
	if spec.TokenIndex > 1 {
		asm.MOVimm16(jit.X8, 1)
		asm.MOVimm16(jit.X9, 0)
		asm.LoadImm64(jit.X10, spec.TokenIndex)
		asm.Label(findTokenLabel)
		asm.CMPreg(jit.X9, jit.X5)
		asm.BCond(jit.CondGE, slowLabel)
		asm.LDRBreg(jit.X11, jit.X4, jit.X9)
		asm.ADDimm(jit.X9, jit.X9, 1)
		asm.CMPreg(jit.X11, jit.X6)
		asm.BCond(jit.CondNE, findTokenLabel)
		asm.MOVreg(jit.X7, jit.X9)
		asm.ADDimm(jit.X8, jit.X8, 1)
		asm.CMPreg(jit.X8, jit.X10)
		asm.BCond(jit.CondLT, findTokenLabel)
	}

	asm.MOVreg(jit.X9, jit.X7)
	asm.Label(findEndLabel)
	asm.CMPreg(jit.X9, jit.X5)
	asm.BCond(jit.CondGE, endReadyLabel)
	asm.LDRBreg(jit.X11, jit.X4, jit.X9)
	asm.CMPreg(jit.X11, jit.X6)
	asm.BCond(jit.CondEQ, endReadyLabel)
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.B(findEndLabel)
	asm.Label(endReadyLabel)

	ec.emitAddConst(jit.X12, jit.X7, int(spec.Start-1), jit.X17)
	if spec.HasEnd {
		ec.emitAddConst(jit.X13, jit.X7, int(spec.End), jit.X17)
		asm.CMPreg(jit.X13, jit.X9)
		asm.BCond(jit.CondLE, endCapOKLabel)
		asm.MOVreg(jit.X13, jit.X9)
		asm.Label(endCapOKLabel)
	} else {
		asm.MOVreg(jit.X13, jit.X9)
	}
	asm.CMPreg(jit.X12, jit.X13)
	asm.BCond(jit.CondGE, nilLabel)

	asm.MOVimm16(jit.X14, 0)
	asm.LDRBreg(jit.X11, jit.X4, jit.X12)
	asm.CMPimm(jit.X11, uint16('-'))
	asm.BCond(jit.CondEQ, signMinusLabel)
	asm.CMPimm(jit.X11, uint16('+'))
	asm.BCond(jit.CondEQ, signPlusLabel)
	asm.B(afterSignLabel)

	asm.Label(signMinusLabel)
	asm.MOVimm16(jit.X14, 1)
	asm.ADDimm(jit.X12, jit.X12, 1)
	asm.B(afterSignLabel)

	asm.Label(signPlusLabel)
	asm.ADDimm(jit.X12, jit.X12, 1)

	asm.Label(afterSignLabel)
	asm.CMPreg(jit.X12, jit.X13)
	asm.BCond(jit.CondGE, nilLabel)
	asm.SUBreg(jit.X2, jit.X13, jit.X12)
	asm.CMPimm(jit.X2, 15)
	asm.BCond(jit.CondGT, slowLabel)

	asm.MOVimm16(jit.X15, 10)
	asm.MOVimm16(jit.X0, 0)
	asm.Label(parseLoopLabel)
	asm.CMPreg(jit.X12, jit.X13)
	asm.BCond(jit.CondGE, parseDoneLabel)
	asm.LDRBreg(jit.X11, jit.X4, jit.X12)
	asm.CMPimm(jit.X11, uint16('0'))
	asm.BCond(jit.CondLT, slowLabel)
	asm.CMPimm(jit.X11, uint16('9'))
	asm.BCond(jit.CondGT, slowLabel)
	asm.SUBimm(jit.X11, jit.X11, uint16('0'))
	asm.MADD(jit.X0, jit.X0, jit.X15, jit.X11)
	asm.ADDimm(jit.X12, jit.X12, 1)
	asm.B(parseLoopLabel)

	asm.Label(parseDoneLabel)
	asm.CBNZ(jit.X14, negativeLabel)
	asm.LoadImm64(jit.X15, maxInt48StringSplitNative)
	asm.CMPreg(jit.X0, jit.X15)
	asm.BCond(jit.CondGT, slowLabel)
	asm.B(storeLabel)

	asm.Label(negativeLabel)
	asm.LoadImm64(jit.X15, 1<<47)
	asm.CMPreg(jit.X0, jit.X15)
	asm.BCond(jit.CondGT, slowLabel)
	asm.NEG(jit.X0, jit.X0)

	asm.Label(storeLabel)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(nilLabel)
	jit.EmitBoxNil(asm, jit.X0)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(slowLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitStdNativeFunctionGuard(val jit.Reg, kind uint8, identity unsafe.Pointer, slowLabel string) {
	asm := ec.asm
	asm.LSRimm(jit.X2, val, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LSRimm(jit.X2, val, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X3, 0xF)
	asm.ANDreg(jit.X2, jit.X2, jit.X3)
	asm.CMPimm(jit.X2, 3)
	asm.BCond(jit.CondNE, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, val)
	asm.LDRB(jit.X3, jit.X2, goFunctionOffNativeKind)
	asm.CMPimm(jit.X3, uint16(kind))
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDR(jit.X2, jit.X2, goFunctionOffNativeData)
	asm.LoadImm64(jit.X3, int64(uintptr(identity)))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, slowLabel)
}
