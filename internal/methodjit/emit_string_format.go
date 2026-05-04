//go:build darwin && arm64

package methodjit

import (
	"math"
	"strings"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

type stringFormatIntPatternNative struct {
	prefix string
	suffix string
	width  int
	pad    byte
}

func parseStringFormatIntPatternNative(pattern string) (stringFormatIntPatternNative, bool) {
	pct := strings.IndexByte(pattern, '%')
	if pct < 0 || strings.IndexByte(pattern[pct+1:], '%') >= 0 {
		return stringFormatIntPatternNative{}, false
	}
	i := pct + 1
	pad := byte(' ')
	if i < len(pattern) && pattern[i] == '0' {
		pad = '0'
		i++
	}
	width := 0
	for i < len(pattern) && pattern[i] >= '0' && pattern[i] <= '9' {
		width = width*10 + int(pattern[i]-'0')
		i++
	}
	if i >= len(pattern) || pattern[i] != 'd' {
		return stringFormatIntPatternNative{}, false
	}
	i++
	return stringFormatIntPatternNative{
		prefix: pattern[:pct],
		suffix: pattern[i:],
		width:  width,
		pad:    pad,
	}, true
}

func stringDataPtr(s string) uintptr {
	if s == "" {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.StringData(s)))
}

func (ec *emitContext) emitStringFormatIntNative(instr *Instr) {
	// Disabled while the native arena string path is under investigation. The
	// OpExit fallback still uses runtime.StringFormatSingleInt, so correctness
	// and the generic specialization mechanism remain intact without exposing
	// tests to non-Go string headers from generated code.
	ec.emitStringFormatIntExit(instr)
	return
	if instr == nil || len(instr.Args) != 3 || ec.fn == nil {
		ec.emitStringFormatIntExit(instr)
		return
	}
	patternIdx := int(instr.Aux)
	if patternIdx < 0 || patternIdx >= len(ec.fn.StringFormatPatterns) {
		ec.emitStringFormatIntExit(instr)
		return
	}
	pat, ok := parseStringFormatIntPatternNative(ec.fn.StringFormatPatterns[patternIdx])
	if !ok {
		ec.emitStringFormatIntExit(instr)
		return
	}
	if !runtime.NativeStringArenaEnsure() {
		ec.emitStringFormatIntExit(instr)
		return
	}

	asm := ec.asm
	slowLabel := ec.uniqueLabel("strfmt_slow")
	slowAfterStackLabel := ec.uniqueLabel("strfmt_slow_stack")
	doneLabel := ec.uniqueLabel("strfmt_done")

	callee := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if callee != jit.X0 {
		asm.MOVreg(jit.X0, callee)
	}
	ec.emitStdStringFormatGuard(jit.X0, slowLabel)

	patternVal := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	if patternVal != jit.X1 {
		asm.MOVreg(jit.X1, patternVal)
	}
	ec.emitStringValueEqualsConstGuard(jit.X1, ec.fn.StringFormatPatterns[patternIdx], slowLabel)

	intVal := ec.resolveValueNB(instr.Args[2].ID, jit.X1)
	if intVal != jit.X1 {
		asm.MOVreg(jit.X1, intVal)
	}
	emitCheckIsInt(asm, jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.LoadImm64(jit.X3, math.MinInt64)
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondEQ, slowLabel)

	asm.SUBimm(jit.SP, jit.SP, 64)

	nonNegLabel := ec.uniqueLabel("strfmt_nonneg")
	digitLoopLabel := ec.uniqueLabel("strfmt_digit_loop")
	digitDoneLabel := ec.uniqueLabel("strfmt_digit_done")
	widthOKLabel := ec.uniqueLabel("strfmt_width_ok")
	signLabel := ec.uniqueLabel("strfmt_sign")
	signZeroPadLabel := ec.uniqueLabel("strfmt_sign_zeropad")
	padLoopLabel := ec.uniqueLabel("strfmt_pad_loop")
	padDoneLabel := ec.uniqueLabel("strfmt_pad_done")
	digitCopyLoopLabel := ec.uniqueLabel("strfmt_digit_copy")
	digitCopyDoneLabel := ec.uniqueLabel("strfmt_digit_copy_done")
	arenaCASLoopLabel := ec.uniqueLabel("strfmt_arena_cas")
	arenaNoSpaceLabel := ec.uniqueLabel("strfmt_arena_full")

	asm.MOVimm16(jit.X2, 0) // sign flag
	asm.MOVreg(jit.X3, jit.X1)
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondGE, nonNegLabel)
	asm.MOVimm16(jit.X2, 1)
	asm.NEG(jit.X3, jit.X1)
	asm.Label(nonNegLabel)

	asm.MOVimm16(jit.X4, 0) // reversed digit count
	asm.MOVimm16(jit.X10, 10)
	asm.Label(digitLoopLabel)
	asm.SDIV(jit.X11, jit.X3, jit.X10)
	asm.MSUB(jit.X12, jit.X11, jit.X10, jit.X3)
	asm.ADDimm(jit.X12, jit.X12, uint16('0'))
	asm.STRBreg(jit.X12, jit.SP, jit.X4)
	asm.ADDimm(jit.X4, jit.X4, 1)
	asm.MOVreg(jit.X3, jit.X11)
	asm.CBNZ(jit.X3, digitLoopLabel)
	asm.Label(digitDoneLabel)

	asm.ADDreg(jit.X13, jit.X4, jit.X2) // digits plus optional sign
	asm.LoadImm64(jit.X14, int64(pat.width))
	asm.CMPreg(jit.X14, jit.X13)
	asm.BCond(jit.CondLE, widthOKLabel)
	asm.MOVreg(jit.X13, jit.X14)
	asm.Label(widthOKLabel)
	asm.SUBreg(jit.X14, jit.X13, jit.X4)
	asm.SUBreg(jit.X14, jit.X14, jit.X2) // pad count

	totalStatic := len(pat.prefix) + len(pat.suffix)
	ec.emitAddConst(jit.X15, jit.X13, totalStatic, jit.X17)
	asm.ADDimm(jit.X16, jit.X15, 31) // header 16 + align 15
	asm.LoadImm64(jit.X17, -16)
	asm.ANDreg(jit.X16, jit.X16, jit.X17)

	asm.LoadImm64(jit.X17, int64(uintptr(unsafe.Pointer(runtime.NativeStringArenaCursorPtr()))))
	asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(runtime.NativeStringArenaEndPtr()))))
	asm.LDR(jit.X3, jit.X3, 0)
	asm.Label(arenaCASLoopLabel)
	asm.LDAXR(jit.X0, jit.X17) // string header address
	asm.ADDreg(jit.X5, jit.X0, jit.X16)
	asm.CMPreg(jit.X5, jit.X3)
	asm.BCond(jit.CondHI, arenaNoSpaceLabel)
	asm.STLXR(jit.X6, jit.X5, jit.X17)
	asm.CBNZ(jit.X6, arenaCASLoopLabel)
	asm.B(arenaNoSpaceLabel + "_done")
	asm.Label(arenaNoSpaceLabel)
	asm.CLREX()
	asm.B(slowAfterStackLabel)
	asm.Label(arenaNoSpaceLabel + "_done")

	asm.ADDimm(jit.X5, jit.X0, 16) // data pointer
	asm.STR(jit.X5, jit.X0, 0)
	asm.STR(jit.X15, jit.X0, 8)

	ec.emitCopyConstBytes(jit.X5, pat.prefix)
	if len(pat.prefix) > 0 {
		ec.emitAddConst(jit.X5, jit.X5, len(pat.prefix), jit.X17)
	}

	asm.CBNZ(jit.X2, signLabel)
	ec.emitRepeatByte(jit.X5, jit.X14, pat.pad, padLoopLabel, padDoneLabel)
	asm.B(digitCopyLoopLabel)

	asm.Label(signLabel)
	if pat.pad == '0' {
		asm.B(signZeroPadLabel)
	}
	ec.emitRepeatByte(jit.X5, jit.X14, pat.pad, padLoopLabel+"_sign", padDoneLabel+"_sign")
	asm.MOVimm16(jit.X12, uint16('-'))
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.B(digitCopyLoopLabel)

	asm.Label(signZeroPadLabel)
	asm.MOVimm16(jit.X12, uint16('-'))
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)
	ec.emitRepeatByte(jit.X5, jit.X14, '0', padLoopLabel+"_zero", padDoneLabel+"_zero")

	asm.Label(digitCopyLoopLabel)
	asm.CBZ(jit.X4, digitCopyDoneLabel)
	asm.SUBimm(jit.X4, jit.X4, 1)
	asm.LDRBreg(jit.X12, jit.SP, jit.X4)
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.B(digitCopyLoopLabel)
	asm.Label(digitCopyDoneLabel)

	ec.emitCopyConstBytes(jit.X5, pat.suffix)

	asm.ADDimm(jit.SP, jit.SP, 64)
	asm.LoadImm64(jit.X1, nb64(jit.NB_TagPtr|(1<<jit.NB_PtrSubShift)))
	asm.ORRreg(jit.X0, jit.X0, jit.X1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(slowAfterStackLabel)
	asm.ADDimm(jit.SP, jit.SP, 64)
	asm.Label(slowLabel)
	ec.emitStringFormatIntExit(instr)
	asm.Label(doneLabel)
}

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

func (ec *emitContext) emitRepeatByte(dst jit.Reg, count jit.Reg, b byte, loopLabel, doneLabel string) {
	asm := ec.asm
	asm.Label(loopLabel)
	asm.CBZ(count, doneLabel)
	asm.MOVimm16(jit.X12, uint16(b))
	asm.STRB(jit.X12, dst, 0)
	asm.ADDimm(dst, dst, 1)
	asm.SUBimm(count, count, 1)
	asm.B(loopLabel)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitAddConst(dst, src jit.Reg, n int, scratch jit.Reg) {
	if n == 0 {
		if dst != src {
			ec.asm.MOVreg(dst, src)
		}
		return
	}
	if n >= 0 && n <= 4095 {
		ec.asm.ADDimm(dst, src, uint16(n))
		return
	}
	ec.asm.LoadImm64(scratch, int64(n))
	ec.asm.ADDreg(dst, src, scratch)
}

func (ec *emitContext) emitCopyConstBytes(dst jit.Reg, s string) {
	if len(s) == 0 {
		return
	}
	asm := ec.asm
	loopLabel := ec.uniqueLabel("strfmt_copy")
	doneLabel := ec.uniqueLabel("strfmt_copy_done")
	asm.LoadImm64(jit.X6, int64(stringDataPtr(s)))
	asm.MOVimm16(jit.X7, 0)
	asm.LoadImm64(jit.X8, int64(len(s)))
	asm.Label(loopLabel)
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondGE, doneLabel)
	asm.LDRBreg(jit.X9, jit.X6, jit.X7)
	asm.STRBreg(jit.X9, dst, jit.X7)
	asm.ADDimm(jit.X7, jit.X7, 1)
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
	jit.EmitExtractPtr(asm, jit.X0, val)
	asm.LDRB(jit.X2, jit.X0, goFunctionOffNativeKind)
	asm.CMPimm(jit.X2, uint16(runtime.NativeKindStdStringFormat))
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDR(jit.X2, jit.X0, goFunctionOffNativeData)
	asm.LoadImm64(jit.X3, int64(uintptr(runtime.StdStringFormatIdentityPtr())))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDR(jit.X2, jit.X0, goFunctionOffFastArg2)
	asm.CBZ(jit.X2, slowLabel)
}
