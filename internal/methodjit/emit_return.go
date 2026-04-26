//go:build darwin && arm64

// emit_return.go contains the OpReturn emit helper. Extracted from
// emit_dispatch.go to keep that file under rule 13's 1000-line cap.

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
)

// emitReturn emits ARM64 code for OpReturn. Numeric pass returns leave a raw
// int in X0 and branch to num_epilogue. The boxed VM ABI writes the NaN-boxed
// result to regs[0] and ctx.BaselineReturnValue, then branches to
// t2_direct_epilogue (CallMode=1, BLR caller) or epilogue (CallMode=0,
// trampoline).
func (ec *emitContext) emitReturn(instr *Instr, block *Block) {
	if ec.numericMode && len(instr.Args) > 0 {
		src := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
		if src != jit.X0 {
			ec.asm.MOVreg(jit.X0, src)
		}
		ec.asm.B("num_epilogue")
		return
	}

	if len(instr.Args) > 0 {
		valID := instr.Args[0].ID
		// If the return value is a raw float in FPR, move bits to GPR.
		// Float bits ARE the NaN-boxed representation.
		if ec.hasFPReg(valID) {
			fpr := ec.physFPReg(valID)
			ec.asm.FMOVtoGP(jit.X0, fpr)
			ec.asm.STR(jit.X0, mRegRegs, 0)
		} else if ec.hasReg(valID) && ec.rawIntRegs[valID] {
			// Raw int in register: box it first.
			reg := ec.physReg(valID)
			jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
			ec.asm.STR(jit.X0, mRegRegs, 0)
		} else {
			// NaN-boxed: resolve and store directly.
			retReg := ec.resolveValueNB(valID, jit.X0)
			if retReg != jit.X0 {
				ec.asm.MOVreg(jit.X0, retReg)
			}
			ec.asm.STR(jit.X0, mRegRegs, 0)
		}
	} else {
		// No return value: use nil.
		ec.asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
		ec.asm.STR(jit.X0, mRegRegs, 0)
	}
	// Also write to ctx.BaselineReturnValue for BLR caller compatibility.
	// When called via BLR from Tier 1, the caller reads BaselineReturnValue.
	ec.asm.STR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	// Check CallMode: 0 = normal entry (from Execute/callJIT), 1 = direct entry (from BLR).
	// Both use a full 128B frame, but the direct epilogue returns to the BLR caller
	// while the normal epilogue returns to the callJIT trampoline.
	ec.asm.LDR(jit.X1, mRegCtx, execCtxOffCallMode)
	ec.asm.CBNZ(jit.X1, "t2_direct_epilogue")
	ec.asm.B("epilogue")
}
