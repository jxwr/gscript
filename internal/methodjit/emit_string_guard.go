//go:build darwin && arm64

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

func (ec *emitContext) emitStringValueEqualsConstGuard(val jit.Reg, expected string, slowLabel string) {
	asm := ec.asm
	jit.EmitCheckIsString(asm, val, jit.X2, jit.X3, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, val)
	asm.LDR(jit.X4, jit.X2, 0) // string data
	asm.LDR(jit.X5, jit.X2, 8) // string length
	asm.LoadImm64(jit.X3, int64(len(expected)))
	asm.CMPreg(jit.X5, jit.X3)
	asm.BCond(jit.CondNE, slowLabel)
	if len(expected) == 0 {
		return
	}
	loopLabel := ec.uniqueLabel("strfmt_pattern_guard")
	doneLabel := ec.uniqueLabel("strfmt_pattern_guard_done")
	asm.LoadImm64(jit.X7, int64(stringDataPtr(expected)))
	asm.MOVimm16(jit.X6, 0)
	asm.Label(loopLabel)
	asm.CMPreg(jit.X6, jit.X5)
	asm.BCond(jit.CondGE, doneLabel)
	asm.LDRBreg(jit.X8, jit.X4, jit.X6)
	asm.LDRBreg(jit.X9, jit.X7, jit.X6)
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondNE, slowLabel)
	asm.ADDimm(jit.X6, jit.X6, 1)
	asm.B(loopLabel)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitStdStringFormatGuard(val jit.Reg, slowLabel string) {
	asm := ec.asm
	asm.LSRimm(jit.X2, val, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LSRimm(jit.X2, val, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X3, 0xF)
	asm.ANDreg(jit.X2, jit.X2, jit.X3)
	asm.CMPimm(jit.X2, 3) // ptrSubGoFunction
	asm.BCond(jit.CondNE, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, val)
	asm.LDRB(jit.X3, jit.X2, goFunctionOffNativeKind)
	asm.CMPimm(jit.X3, uint16(runtime.NativeKindStdStringFormat))
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDR(jit.X2, jit.X2, goFunctionOffNativeData)
	asm.LoadImm64(jit.X3, int64(uintptr(runtime.StdStringFormatIdentityPtr())))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, slowLabel)
	jit.EmitExtractPtr(asm, jit.X2, val)
	asm.LDR(jit.X2, jit.X2, goFunctionOffFastArg2)
	asm.CBZ(jit.X2, slowLabel)
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
