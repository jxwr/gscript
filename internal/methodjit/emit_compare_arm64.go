//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

func emitCMPWConst(asm *jit.Assembler, reg, scratch jit.Reg, val int64) {
	if val >= 0 && val <= 4095 {
		asm.CMPimmW(reg, uint16(val))
		return
	}
	asm.LoadImm64(scratch, val)
	asm.CMPreg(reg, scratch)
}
