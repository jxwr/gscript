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

type stringFormatConstIntSpecNative struct {
	litBefore string
	width     int
	prec      int
	pad       byte
	kind      byte
}

type stringFormatConstIntPatternNative struct {
	specs     []stringFormatConstIntSpecNative
	tail      string
	staticLen int
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

func parseStringFormatConstIntPatternNative(pattern string) (stringFormatConstIntPatternNative, bool) {
	var pat stringFormatConstIntPatternNative
	litStart := 0
	for i := 0; i < len(pattern); {
		if pattern[i] != '%' {
			i++
			continue
		}
		lit := pattern[litStart:i]
		i++
		if i >= len(pattern) || pattern[i] == '%' {
			return stringFormatConstIntPatternNative{}, false
		}
		pad := byte(' ')
		width := 0
		kind := pattern[i]
		if kind == 's' {
			i++
			pat.specs = append(pat.specs, stringFormatConstIntSpecNative{
				litBefore: lit,
				kind:      's',
			})
			pat.staticLen += len(lit)
			litStart = i
			continue
		}
		if pattern[i] == '0' {
			pad = '0'
			i++
		}
		for i < len(pattern) && pattern[i] >= '0' && pattern[i] <= '9' {
			width = width*10 + int(pattern[i]-'0')
			i++
		}
		prec := 6
		if i < len(pattern) && pattern[i] == '.' {
			i++
			prec = 0
			if i >= len(pattern) || pattern[i] < '0' || pattern[i] > '9' {
				return stringFormatConstIntPatternNative{}, false
			}
			for i < len(pattern) && pattern[i] >= '0' && pattern[i] <= '9' {
				prec = prec*10 + int(pattern[i]-'0')
				i++
			}
		}
		if i >= len(pattern) {
			return stringFormatConstIntPatternNative{}, false
		}
		kind = pattern[i]
		if kind != 'd' && kind != 'f' {
			return stringFormatConstIntPatternNative{}, false
		}
		if kind == 'd' && prec != 6 {
			return stringFormatConstIntPatternNative{}, false
		}
		if kind == 'f' && (pad != ' ' || prec > 9) {
			return stringFormatConstIntPatternNative{}, false
		}
		i++
		pat.specs = append(pat.specs, stringFormatConstIntSpecNative{
			litBefore: lit,
			width:     width,
			prec:      prec,
			pad:       pad,
			kind:      kind,
		})
		pat.staticLen += len(lit)
		litStart = i
	}
	pat.tail = pattern[litStart:]
	pat.staticLen += len(pat.tail)
	return pat, len(pat.specs) >= 1
}

func stringDataPtr(s string) uintptr {
	if s == "" {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.StringData(s)))
}

func pow10IntNative(n int) int64 {
	v := int64(1)
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}

func (ec *emitContext) emitStringFormatIntCacheProbe(pattern string, intReg jit.Reg, instrID int, doneLabel string) {
	if pattern == "" {
		return
	}
	asm := ec.asm
	missLabel := ec.uniqueLabel("strfmt_cache_miss")

	asm.LoadImm64(jit.X2, int64(runtime.NativeStringFormatIntCacheSize-1))
	asm.ANDreg(jit.X3, intReg, jit.X2)
	asm.ADDregLSL(jit.X4, jit.X3, jit.X3, 1) // idx * 3
	asm.LSLimm(jit.X4, jit.X4, 4)            // idx * 48
	asm.LoadImm64(jit.X2, int64(uintptr(runtime.NativeStringFormatIntCachePtr())))
	asm.ADDreg(jit.X4, jit.X2, jit.X4)

	asm.LDR(jit.X5, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.PatternData)))
	asm.LoadImm64(jit.X6, int64(stringDataPtr(pattern)))
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X5, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.PatternLen)))
	asm.LoadImm64(jit.X6, int64(len(pattern)))
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X5, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.N)))
	asm.CMPreg(jit.X5, intReg)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X0, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.Value)))
	ec.storeResultNB(jit.X0, instrID)
	asm.B(doneLabel)
	asm.Label(missLabel)
}

func (ec *emitContext) emitStringFormatIntCacheStore(pattern string, intReg, valueReg jit.Reg) {
	if pattern == "" {
		return
	}
	asm := ec.asm
	asm.LoadImm64(jit.X2, int64(runtime.NativeStringFormatIntCacheSize-1))
	asm.ANDreg(jit.X3, intReg, jit.X2)
	asm.ADDregLSL(jit.X4, jit.X3, jit.X3, 1)
	asm.LSLimm(jit.X4, jit.X4, 4)
	asm.LoadImm64(jit.X2, int64(uintptr(runtime.NativeStringFormatIntCachePtr())))
	asm.ADDreg(jit.X4, jit.X2, jit.X4)

	asm.LoadImm64(jit.X5, int64(len(pattern)))
	asm.STR(jit.X5, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.PatternLen)))
	asm.STR(valueReg, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.Value)))
	asm.STR(intReg, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.N)))
	asm.LoadImm64(jit.X5, int64(stringDataPtr(pattern)))
	asm.STR(jit.X5, jit.X4, int(unsafe.Offsetof(runtime.NativeStringFormatIntCacheEntry{}.PatternData)))
}

// emitStringFormatIntArenaBytes writes string.format(pattern, X1) to the
// native string arena and returns key data in X5 and length in X6. The caller
// must reserve at least 64 bytes at SP for temporary reversed digits and must
// already have rejected MinInt64.
func (ec *emitContext) emitStringFormatIntArenaBytes(pat stringFormatIntPatternNative, slowLabel string) {
	asm := ec.asm
	nonNegLabel := ec.uniqueLabel("strfmt_bytes_nonneg")
	digitLoopLabel := ec.uniqueLabel("strfmt_bytes_digit_loop")
	widthOKLabel := ec.uniqueLabel("strfmt_bytes_width_ok")
	signLabel := ec.uniqueLabel("strfmt_bytes_sign")
	signZeroPadLabel := ec.uniqueLabel("strfmt_bytes_sign_zeropad")
	padLoopLabel := ec.uniqueLabel("strfmt_bytes_pad_loop")
	padDoneLabel := ec.uniqueLabel("strfmt_bytes_pad_done")
	digitCopyLoopLabel := ec.uniqueLabel("strfmt_bytes_digit_copy")
	digitCopyDoneLabel := ec.uniqueLabel("strfmt_bytes_digit_done")
	arenaCASLoopLabel := ec.uniqueLabel("strfmt_bytes_arena_cas")
	arenaNoSpaceLabel := ec.uniqueLabel("strfmt_bytes_arena_full")

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

	asm.ADDreg(jit.X13, jit.X4, jit.X2)
	asm.LoadImm64(jit.X14, int64(pat.width))
	asm.CMPreg(jit.X14, jit.X13)
	asm.BCond(jit.CondLE, widthOKLabel)
	asm.MOVreg(jit.X13, jit.X14)
	asm.Label(widthOKLabel)
	asm.SUBreg(jit.X14, jit.X13, jit.X4)
	asm.SUBreg(jit.X14, jit.X14, jit.X2)

	totalStatic := len(pat.prefix) + len(pat.suffix)
	ec.emitAddConst(jit.X15, jit.X13, totalStatic, jit.X17)
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
	asm.STR(jit.X5, jit.SP, 56) // stable data pointer for the caller
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
	asm.LDR(jit.X5, jit.SP, 56)
	asm.MOVreg(jit.X6, jit.X15)
}

func (ec *emitContext) emitStringFormatIntNative(instr *Instr) {
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
	ec.emitStringFormatIntCacheProbe(ec.fn.StringFormatPatterns[patternIdx], jit.X1, instr.ID, doneLabel)

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
	asm.MOVreg(jit.X7, jit.X1)
	asm.LoadImm64(jit.X1, nb64(jit.NB_TagPtr|(1<<jit.NB_PtrSubShift)))
	asm.ORRreg(jit.X0, jit.X0, jit.X1)
	ec.emitStringFormatIntCacheStore(ec.fn.StringFormatPatterns[patternIdx], jit.X7, jit.X0)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(slowAfterStackLabel)
	asm.ADDimm(jit.SP, jit.SP, 64)
	asm.Label(slowLabel)
	ec.emitStringFormatIntExit(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitStringFormatConstNative(instr *Instr) {
	if instr == nil || len(instr.Args) < 3 || ec.fn == nil {
		ec.emitStringFormatConstExit(instr)
		return
	}
	patternIdx := int(instr.Aux)
	if patternIdx < 0 || patternIdx >= len(ec.fn.StringFormatPatterns) {
		ec.emitStringFormatConstExit(instr)
		return
	}
	pattern := ec.fn.StringFormatPatterns[patternIdx]
	pat, ok := parseStringFormatConstIntPatternNative(pattern)
	if !ok || len(pat.specs) != len(instr.Args)-2 || len(pat.specs) > 8 {
		ec.emitStringFormatConstExit(instr)
		return
	}
	if !runtime.NativeStringArenaEnsure() {
		ec.emitStringFormatConstExit(instr)
		return
	}

	asm := ec.asm
	slowLabel := ec.uniqueLabel("strfmtc_slow")
	slowAfterStackLabel := ec.uniqueLabel("strfmtc_slow_stack")
	doneLabel := ec.uniqueLabel("strfmtc_done")

	callee := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if callee != jit.X0 {
		asm.MOVreg(jit.X0, callee)
	}
	ec.emitStdStringFormatGuard(jit.X0, slowLabel)

	patternVal := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	if patternVal != jit.X1 {
		asm.MOVreg(jit.X1, patternVal)
	}
	ec.emitStringValueEqualsConstGuard(jit.X1, pattern, slowLabel)

	nSpecs := len(pat.specs)
	metaBytes := nSpecs * 32
	digitBase := metaBytes
	frameSize := (metaBytes + nSpecs*32 + 15) &^ 15
	asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))

	if pat.staticLen > 0 {
		asm.LoadImm64(jit.X15, int64(pat.staticLen))
	} else {
		asm.MOVimm16(jit.X15, 0)
	}
	asm.LoadImm64(jit.X10, 10)
	for i, spec := range pat.specs {
		arg := ec.resolveValueNB(instr.Args[i+2].ID, jit.X1)
		if arg != jit.X1 {
			asm.MOVreg(jit.X1, arg)
		}
		if spec.kind == 's' {
			jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowAfterStackLabel)
			jit.EmitExtractPtr(asm, jit.X2, jit.X1)
			asm.LDR(jit.X3, jit.X2, 0)
			asm.LDR(jit.X4, jit.X2, 8)
			asm.STR(jit.X3, jit.SP, i*32)
			asm.STR(jit.X4, jit.SP, i*32+8)
			asm.ADDreg(jit.X15, jit.X15, jit.X4)
			continue
		}
		if spec.kind == 'f' {
			emitToFloatNumberOrMiss(asm, jit.D0, jit.X1, jit.X2, slowAfterStackLabel)

			asm.LoadImm64(jit.X3, 0)
			asm.FMOVtoFP(jit.D1, jit.X3)
			asm.FCMPd(jit.D0, jit.D1)
			asm.BCond(jit.CondVS, slowAfterStackLabel)
			asm.BCond(jit.CondLT, slowAfterStackLabel)

			scale := pow10IntNative(spec.prec)
			asm.LoadImm64(jit.X3, int64(math.Float64bits(float64(math.MaxInt64/scale))))
			asm.FMOVtoFP(jit.D1, jit.X3)
			asm.FCMPd(jit.D0, jit.D1)
			asm.BCond(jit.CondVS, slowAfterStackLabel)
			asm.BCond(jit.CondGE, slowAfterStackLabel)

			asm.LoadImm64(jit.X3, int64(math.Float64bits(float64(scale))))
			asm.FMOVtoFP(jit.D1, jit.X3)
			asm.FMULd(jit.D0, jit.D0, jit.D1)
			asm.LoadImm64(jit.X3, int64(math.Float64bits(0.5)))
			asm.FMOVtoFP(jit.D1, jit.X3)
			asm.FADDd(jit.D0, jit.D0, jit.D1)
			asm.FCVTZS(jit.X1, jit.D0)

			asm.LoadImm64(jit.X10, scale)
			asm.SDIV(jit.X3, jit.X1, jit.X10)
			asm.MSUB(jit.X14, jit.X3, jit.X10, jit.X1)
			asm.STR(jit.X3, jit.SP, i*32)
			asm.STR(jit.X14, jit.SP, i*32+24)

			digitLoopLabel := ec.uniqueLabel("strfmtc_f_len_digit_loop")
			digitDoneLabel := ec.uniqueLabel("strfmtc_f_len_digit_done")
			widthOKLabel := ec.uniqueLabel("strfmtc_f_len_width_ok")

			asm.ADDimm(jit.X5, jit.SP, uint16(digitBase+i*32))
			asm.MOVimm16(jit.X10, 10)
			asm.MOVimm16(jit.X4, 0)
			asm.Label(digitLoopLabel)
			asm.SDIV(jit.X11, jit.X3, jit.X10)
			asm.MSUB(jit.X12, jit.X11, jit.X10, jit.X3)
			asm.ADDimm(jit.X12, jit.X12, uint16('0'))
			asm.STRBreg(jit.X12, jit.X5, jit.X4)
			asm.ADDimm(jit.X4, jit.X4, 1)
			asm.MOVreg(jit.X3, jit.X11)
			asm.CBNZ(jit.X3, digitLoopLabel)
			asm.Label(digitDoneLabel)

			asm.STR(jit.X4, jit.SP, i*32+16)
			ec.emitAddConst(jit.X13, jit.X4, 1+spec.prec, jit.X17)
			asm.LoadImm64(jit.X14, int64(spec.width))
			asm.CMPreg(jit.X14, jit.X13)
			asm.BCond(jit.CondLE, widthOKLabel)
			asm.MOVreg(jit.X13, jit.X14)
			asm.Label(widthOKLabel)
			asm.STR(jit.X13, jit.SP, i*32+8)
			asm.ADDreg(jit.X15, jit.X15, jit.X13)
			continue
		}
		emitCheckIsInt(asm, jit.X1, jit.X2)
		asm.BCond(jit.CondNE, slowAfterStackLabel)
		jit.EmitUnboxInt(asm, jit.X1, jit.X1)
		asm.LoadImm64(jit.X3, math.MinInt64)
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondEQ, slowAfterStackLabel)
		asm.STR(jit.X1, jit.SP, i*32)

		nonNegLabel := ec.uniqueLabel("strfmtc_len_nonneg")
		digitLoopLabel := ec.uniqueLabel("strfmtc_len_digit_loop")
		digitDoneLabel := ec.uniqueLabel("strfmtc_len_digit_done")
		widthOKLabel := ec.uniqueLabel("strfmtc_len_width_ok")

		asm.MOVimm16(jit.X2, 0)
		asm.MOVreg(jit.X3, jit.X1)
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondGE, nonNegLabel)
		asm.MOVimm16(jit.X2, 1)
		asm.NEG(jit.X3, jit.X1)
		asm.Label(nonNegLabel)

		asm.ADDimm(jit.X5, jit.SP, uint16(digitBase+i*32))
		asm.MOVimm16(jit.X4, 0)
		asm.Label(digitLoopLabel)
		asm.SDIV(jit.X11, jit.X3, jit.X10)
		asm.MSUB(jit.X12, jit.X11, jit.X10, jit.X3)
		asm.ADDimm(jit.X12, jit.X12, uint16('0'))
		asm.STRBreg(jit.X12, jit.X5, jit.X4)
		asm.ADDimm(jit.X4, jit.X4, 1)
		asm.MOVreg(jit.X3, jit.X11)
		asm.CBNZ(jit.X3, digitLoopLabel)
		asm.Label(digitDoneLabel)

		asm.STR(jit.X4, jit.SP, i*32+16)
		asm.ADDreg(jit.X13, jit.X4, jit.X2)
		asm.LoadImm64(jit.X14, int64(pat.specs[i].width))
		asm.CMPreg(jit.X14, jit.X13)
		asm.BCond(jit.CondLE, widthOKLabel)
		asm.MOVreg(jit.X13, jit.X14)
		asm.Label(widthOKLabel)
		asm.STR(jit.X13, jit.SP, i*32+8)
		asm.ADDreg(jit.X15, jit.X15, jit.X13)
	}

	arenaCASLoopLabel := ec.uniqueLabel("strfmtc_arena_cas")
	arenaNoSpaceLabel := ec.uniqueLabel("strfmtc_arena_full")
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
	asm.B(slowAfterStackLabel)
	asm.Label(arenaNoSpaceLabel + "_done")

	asm.ADDimm(jit.X5, jit.X0, 16)
	asm.STR(jit.X5, jit.X0, 0)
	asm.STR(jit.X15, jit.X0, 8)

	for i, spec := range pat.specs {
		ec.emitCopyConstBytes(jit.X5, spec.litBefore)
		if len(spec.litBefore) > 0 {
			ec.emitAddConst(jit.X5, jit.X5, len(spec.litBefore), jit.X17)
		}
		if spec.kind == 's' {
			ec.emitCopyFormatConstStringArgNative(i)
		} else if spec.kind == 'f' {
			ec.emitFormatConstFloatArgNative(i, digitBase+i*32, spec.prec)
		} else {
			ec.emitFormatConstIntArgNative(i, digitBase+i*32, spec.pad)
		}
	}
	ec.emitCopyConstBytes(jit.X5, pat.tail)

	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	asm.LoadImm64(jit.X1, nb64(jit.NB_TagPtr|(1<<jit.NB_PtrSubShift)))
	asm.ORRreg(jit.X0, jit.X0, jit.X1)
	ec.storeResultNB(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(slowAfterStackLabel)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	asm.Label(slowLabel)
	ec.emitStringFormatConstExit(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitStringFormatConstLenNative(instr *Instr) {
	if instr == nil || len(instr.Args) < 3 || ec.fn == nil {
		ec.emitDeopt(instr)
		return
	}
	patternIdx := int(instr.Aux)
	if patternIdx < 0 || patternIdx >= len(ec.fn.StringFormatPatterns) {
		ec.emitDeopt(instr)
		return
	}
	pattern := ec.fn.StringFormatPatterns[patternIdx]
	pat, ok := parseStringFormatConstIntPatternNative(pattern)
	if !ok || len(pat.specs) != len(instr.Args)-2 || !stringFormatConstLenPatternIsIntOnly(pat) {
		ec.emitDeopt(instr)
		return
	}
	asm := ec.asm
	slowLabel := ec.uniqueLabel("strfmt_len_slow")
	doneLabel := ec.uniqueLabel("strfmt_len_done")

	callee := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if callee != jit.X0 {
		asm.MOVreg(jit.X0, callee)
	}
	ec.emitStdStringFormatGuard(jit.X0, slowLabel)

	patternVal := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	if patternVal != jit.X1 {
		asm.MOVreg(jit.X1, patternVal)
	}
	ec.emitStringValueEqualsConstGuard(jit.X1, pattern, slowLabel)

	if pat.staticLen > 0 {
		asm.LoadImm64(jit.X15, int64(pat.staticLen))
	} else {
		asm.MOVimm16(jit.X15, 0)
	}
	asm.LoadImm64(jit.X10, 10)
	for i, spec := range pat.specs {
		arg := ec.resolveValueNB(instr.Args[i+2].ID, jit.X1)
		if arg != jit.X1 {
			asm.MOVreg(jit.X1, arg)
		}
		emitCheckIsInt(asm, jit.X1, jit.X2)
		asm.BCond(jit.CondNE, slowLabel)
		jit.EmitUnboxInt(asm, jit.X1, jit.X1)
		asm.LoadImm64(jit.X3, math.MinInt64)
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondEQ, slowLabel)

		nonNegLabel := ec.uniqueLabel("strfmt_len_only_nonneg")
		digitLoopLabel := ec.uniqueLabel("strfmt_len_only_digit_loop")
		digitDoneLabel := ec.uniqueLabel("strfmt_len_only_digit_done")
		widthOKLabel := ec.uniqueLabel("strfmt_len_only_width_ok")

		asm.MOVimm16(jit.X2, 0)
		asm.MOVreg(jit.X3, jit.X1)
		asm.CMPimm(jit.X1, 0)
		asm.BCond(jit.CondGE, nonNegLabel)
		asm.MOVimm16(jit.X2, 1)
		asm.NEG(jit.X3, jit.X1)
		asm.Label(nonNegLabel)

		asm.MOVimm16(jit.X4, 0)
		asm.Label(digitLoopLabel)
		asm.SDIV(jit.X11, jit.X3, jit.X10)
		asm.ADDimm(jit.X4, jit.X4, 1)
		asm.MOVreg(jit.X3, jit.X11)
		asm.CBNZ(jit.X3, digitLoopLabel)
		asm.Label(digitDoneLabel)

		asm.ADDreg(jit.X13, jit.X4, jit.X2)
		asm.LoadImm64(jit.X14, int64(spec.width))
		asm.CMPreg(jit.X14, jit.X13)
		asm.BCond(jit.CondLE, widthOKLabel)
		asm.MOVreg(jit.X13, jit.X14)
		asm.Label(widthOKLabel)
		asm.ADDreg(jit.X15, jit.X15, jit.X13)
	}

	if instr.Type == TypeInt {
		ec.storeRawInt(jit.X15, instr.ID)
		asm.B(doneLabel)
	} else {
		jit.EmitBoxIntFast(asm, jit.X0, jit.X15, mRegTagInt)
		ec.storeResultNB(jit.X0, instr.ID)
		asm.B(doneLabel)
	}

	asm.Label(slowLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitFormatConstIntArgNative(argIdx, digitOff int, pad byte) {
	asm := ec.asm
	signLabel := ec.uniqueLabel("strfmtc_sign")
	signZeroPadLabel := ec.uniqueLabel("strfmtc_sign_zeropad")
	padLoopLabel := ec.uniqueLabel("strfmtc_pad_loop")
	padDoneLabel := ec.uniqueLabel("strfmtc_pad_done")
	digitCopyLoopLabel := ec.uniqueLabel("strfmtc_digit_copy")
	digitCopyDoneLabel := ec.uniqueLabel("strfmtc_digit_copy_done")
	noSignLabel := ec.uniqueLabel("strfmtc_no_sign")

	asm.LDR(jit.X1, jit.SP, argIdx*32)
	asm.LDR(jit.X4, jit.SP, argIdx*32+16)
	asm.MOVimm16(jit.X2, 0)
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondGE, noSignLabel)
	asm.MOVimm16(jit.X2, 1)
	asm.Label(noSignLabel)
	asm.LDR(jit.X13, jit.SP, argIdx*32+8)
	asm.SUBreg(jit.X14, jit.X13, jit.X4)
	asm.SUBreg(jit.X14, jit.X14, jit.X2)

	asm.CBNZ(jit.X2, signLabel)
	ec.emitRepeatByte(jit.X5, jit.X14, pad, padLoopLabel, padDoneLabel)
	asm.B(digitCopyLoopLabel)

	asm.Label(signLabel)
	if pad == '0' {
		asm.B(signZeroPadLabel)
	}
	ec.emitRepeatByte(jit.X5, jit.X14, pad, padLoopLabel+"_sign", padDoneLabel+"_sign")
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
	asm.ADDimm(jit.X6, jit.SP, uint16(digitOff))
	asm.LDRBreg(jit.X12, jit.X6, jit.X4)
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.B(digitCopyLoopLabel)
	asm.Label(digitCopyDoneLabel)
}

func (ec *emitContext) emitFormatConstFloatArgNative(argIdx, digitOff, prec int) {
	asm := ec.asm
	padLoopLabel := ec.uniqueLabel("strfmtc_f_pad_loop")
	padDoneLabel := ec.uniqueLabel("strfmtc_f_pad_done")
	digitCopyLoopLabel := ec.uniqueLabel("strfmtc_f_digit_copy")
	digitCopyDoneLabel := ec.uniqueLabel("strfmtc_f_digit_copy_done")
	fracLoopLabel := ec.uniqueLabel("strfmtc_f_frac_loop")
	fracDoneLabel := ec.uniqueLabel("strfmtc_f_frac_done")

	asm.LDR(jit.X4, jit.SP, argIdx*32+16)
	asm.LDR(jit.X13, jit.SP, argIdx*32+8)
	ec.emitAddConst(jit.X14, jit.X4, 1+prec, jit.X17)
	asm.SUBreg(jit.X14, jit.X13, jit.X14)
	ec.emitRepeatByte(jit.X5, jit.X14, ' ', padLoopLabel, padDoneLabel)

	asm.Label(digitCopyLoopLabel)
	asm.CBZ(jit.X4, digitCopyDoneLabel)
	asm.SUBimm(jit.X4, jit.X4, 1)
	asm.ADDimm(jit.X6, jit.SP, uint16(digitOff))
	asm.LDRBreg(jit.X12, jit.X6, jit.X4)
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.B(digitCopyLoopLabel)
	asm.Label(digitCopyDoneLabel)

	if prec == 0 {
		return
	}
	asm.MOVimm16(jit.X12, uint16('.'))
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)

	asm.LDR(jit.X1, jit.SP, argIdx*32+24)
	asm.LoadImm64(jit.X10, pow10IntNative(prec-1))
	asm.MOVimm16(jit.X4, uint16(prec))
	asm.Label(fracLoopLabel)
	asm.CBZ(jit.X4, fracDoneLabel)
	asm.SDIV(jit.X11, jit.X1, jit.X10)
	asm.MSUB(jit.X1, jit.X11, jit.X10, jit.X1)
	asm.ADDimm(jit.X12, jit.X11, uint16('0'))
	asm.STRB(jit.X12, jit.X5, 0)
	asm.ADDimm(jit.X5, jit.X5, 1)
	asm.MOVimm16(jit.X12, 10)
	asm.SDIV(jit.X10, jit.X10, jit.X12)
	asm.SUBimm(jit.X4, jit.X4, 1)
	asm.B(fracLoopLabel)
	asm.Label(fracDoneLabel)
}

func (ec *emitContext) emitCopyFormatConstStringArgNative(argIdx int) {
	asm := ec.asm
	loopLabel := ec.uniqueLabel("strfmtc_str_copy")
	doneLabel := ec.uniqueLabel("strfmtc_str_copy_done")
	asm.LDR(jit.X6, jit.SP, argIdx*32)
	asm.LDR(jit.X8, jit.SP, argIdx*32+8)
	asm.MOVimm16(jit.X7, 0)
	asm.Label(loopLabel)
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondGE, doneLabel)
	asm.LDRBreg(jit.X9, jit.X6, jit.X7)
	asm.STRBreg(jit.X9, jit.X5, jit.X7)
	asm.ADDimm(jit.X7, jit.X7, 1)
	asm.B(loopLabel)
	asm.Label(doneLabel)
	asm.ADDreg(jit.X5, jit.X5, jit.X8)
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
