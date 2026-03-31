//go:build darwin && arm64

// tier1_control.go emits ARM64 templates for baseline control flow:
// JMP, FORPREP, FORLOOP, RETURN, TFORLOOP.
//
// These operations are all emitted as native ARM64 code (no op-exit).

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

// ---------------------------------------------------------------------------
// Jump
// ---------------------------------------------------------------------------

// emitBaselineJmp: PC += sBx
func emitBaselineJmp(asm *jit.Assembler, inst uint32, pc int) {
	sbx := vm.DecodesBx(inst)
	target := pc + 1 + sbx
	asm.B(pcLabel(target))
}

// ---------------------------------------------------------------------------
// For Loop
// ---------------------------------------------------------------------------

// emitBaselineForPrep: R(A) -= R(A+2); PC += sBx
func emitBaselineForPrep(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	// Load init (R(A)) and step (R(A+2))
	asm.LDR(jit.X0, mRegRegs, slotOff(a))   // init
	asm.LDR(jit.X1, mRegRegs, slotOff(a+2)) // step

	floatLabel := nextLabel("forprep_float")
	doneLabel := nextLabel("forprep_done")

	// Check both are int
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	// Both int: R(A) = init - step
	asm.SBFX(jit.X4, jit.X0, 0, 48)
	asm.SBFX(jit.X5, jit.X1, 0, 48)
	asm.SUBreg(jit.X4, jit.X4, jit.X5)
	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
	asm.STR(jit.X4, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Float fallback
	asm.Label(floatLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.FSUBd(jit.D0, jit.D0, jit.D1)
	asm.FMOVtoGP(jit.X0, jit.D0)
	asm.STR(jit.X0, mRegRegs, slotOff(a))

	asm.Label(doneLabel)
	// Jump to loop test.
	target := pc + 1 + sbx
	asm.B(pcLabel(target))
}

// emitBaselineForLoop: R(A) += R(A+2); if R(A) <?= R(A+1) then PC += sBx; R(A+3) = R(A)
// Includes OSR counter: each back-edge decrements ctx.OSRCounter. When it
// reaches 0, exits with ExitOSR so the TieringManager can compile Tier 2.
func emitBaselineForLoop(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	// Load idx (R(A)), limit (R(A+1)), step (R(A+2))
	asm.LDR(jit.X0, mRegRegs, slotOff(a))   // idx
	asm.LDR(jit.X1, mRegRegs, slotOff(a+1)) // limit
	asm.LDR(jit.X2, mRegRegs, slotOff(a+2)) // step

	floatLabel := nextLabel("forloop_float")
	exitLabel := nextLabel("forloop_exit")
	osrCheckLabel := nextLabel("forloop_osr")
	contLabel := pcLabel(pc + 1 + sbx) // loop body

	// Check all three are int
	asm.LSRimm(jit.X3, jit.X0, 48)
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X3, jit.X6)
	asm.BCond(jit.CondNE, floatLabel)

	asm.LSRimm(jit.X3, jit.X2, 48)
	asm.CMPreg(jit.X3, jit.X6)
	asm.BCond(jit.CondNE, floatLabel)

	asm.LSRimm(jit.X3, jit.X1, 48)
	asm.CMPreg(jit.X3, jit.X6)
	asm.BCond(jit.CondNE, floatLabel)

	// All int: idx += step
	asm.SBFX(jit.X3, jit.X0, 0, 48) // idx
	asm.SBFX(jit.X4, jit.X2, 0, 48) // step
	asm.SBFX(jit.X5, jit.X1, 0, 48) // limit
	asm.ADDreg(jit.X3, jit.X3, jit.X4) // idx += step

	// Check direction: if step > 0, check idx <= limit; else idx >= limit
	positiveStepLabel := nextLabel("forloop_pos")
	asm.CMPimm(jit.X4, 0)
	asm.BCond(jit.CondGT, positiveStepLabel)

	// Negative step: idx >= limit
	asm.CMPreg(jit.X3, jit.X5)
	asm.BCond(jit.CondLT, exitLabel)
	// Continue: store idx, R(A+3) = idx, jump to OSR check then body.
	jit.EmitBoxIntFast(asm, jit.X3, jit.X3, mRegTagInt)
	asm.STR(jit.X3, mRegRegs, slotOff(a))
	asm.STR(jit.X3, mRegRegs, slotOff(a+3))
	asm.B(osrCheckLabel)

	asm.Label(positiveStepLabel)
	// Positive step: idx <= limit
	asm.CMPreg(jit.X3, jit.X5)
	asm.BCond(jit.CondGT, exitLabel)
	jit.EmitBoxIntFast(asm, jit.X3, jit.X3, mRegTagInt)
	asm.STR(jit.X3, mRegRegs, slotOff(a))
	asm.STR(jit.X3, mRegRegs, slotOff(a+3))
	asm.B(osrCheckLabel)

	// Float fallback
	asm.Label(floatLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X3, jit.X4) // idx
	emitToFloat(asm, jit.D1, jit.X1, jit.X3, jit.X4) // limit
	emitToFloat(asm, jit.D2, jit.X2, jit.X3, jit.X4) // step

	asm.FADDd(jit.D0, jit.D0, jit.D2) // idx += step
	asm.FMOVtoGP(jit.X0, jit.D0)
	asm.STR(jit.X0, mRegRegs, slotOff(a)) // store updated idx

	// Check step direction with FCMP
	floatPosLabel := nextLabel("forloop_fpos")
	// Load zero for comparison
	asm.MOVimm16(jit.X3, 0)
	asm.SCVTF(jit.D3, jit.X3)

	asm.FCMPd(jit.D2, jit.D3) // step vs 0
	asm.BCond(jit.CondGT, floatPosLabel)

	// Negative step: idx >= limit
	asm.FCMPd(jit.D0, jit.D1)
	asm.BCond(jit.CondMI, exitLabel) // idx < limit -> exit

	// Continue
	asm.STR(jit.X0, mRegRegs, slotOff(a+3)) // R(A+3) = idx
	asm.B(osrCheckLabel)

	asm.Label(floatPosLabel)
	// Positive step: idx <= limit
	asm.FCMPd(jit.D0, jit.D1)
	asm.BCond(jit.CondGT, exitLabel) // idx > limit -> exit

	// Continue
	asm.STR(jit.X0, mRegRegs, slotOff(a+3))
	asm.B(osrCheckLabel)

	// --- OSR check: decrement counter and exit if zero ---
	asm.Label(osrCheckLabel)
	// Load OSRCounter from ctx.
	asm.LDR(jit.X7, mRegCtx, execCtxOffOSRCounter)
	// If counter < 0 (disabled), skip directly to loop body.
	asm.CMPimm(jit.X7, 0)
	asm.BCond(jit.CondLT, contLabel) // counter < 0 -> disabled, continue loop
	// Decrement counter.
	asm.SUBimm(jit.X7, jit.X7, 1)
	asm.STR(jit.X7, mRegCtx, execCtxOffOSRCounter)
	// If counter > 0, continue loop normally.
	asm.CMPimm(jit.X7, 0)
	asm.BCond(jit.CondGT, contLabel) // counter > 0 -> continue
	// Counter reached 0: request OSR exit.
	asm.LoadImm64(jit.X0, ExitOSR)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	// Check CallMode to choose the right exit path.
	asm.LDR(jit.X0, mRegCtx, execCtxOffCallMode)
	asm.CBNZ(jit.X0, "direct_exit")
	asm.B("baseline_exit")

	asm.Label(exitLabel)
}

// ---------------------------------------------------------------------------
// Return
// ---------------------------------------------------------------------------

// emitBaselineReturn: return R(A)..R(A+B-2); B=0 variable, B=1 nothing
// Checks CallMode to decide between normal exit (baseline_epilogue) and
// direct exit (direct_epilogue) for native BLR calls.
func emitBaselineReturn(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	if b == 1 {
		// Return nothing: store nil in ctx.BaselineReturnValue.
		asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
		asm.STR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	} else if b >= 2 {
		// Return b-1 values starting from R(A).
		// Store the first return value in ctx.BaselineReturnValue.
		if a != 0 {
			asm.LDR(jit.X0, mRegRegs, slotOff(a))
		} else {
			asm.LDR(jit.X0, mRegRegs, slotOff(0))
		}
		asm.STR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	} else {
		// b == 0: variable return. Store R(A) in ctx.BaselineReturnValue.
		asm.LDR(jit.X0, mRegRegs, slotOff(a))
		asm.STR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	}

	// Check CallMode: 0 = normal entry, 1 = direct entry (native BLR call).
	asm.LDR(jit.X1, mRegCtx, execCtxOffCallMode)
	asm.CBNZ(jit.X1, "direct_epilogue")
	asm.B("baseline_epilogue")
}

// ---------------------------------------------------------------------------
// TFORLOOP
// ---------------------------------------------------------------------------

// emitBaselineTForLoop: if R(A+1) != nil { R(A) = R(A+1); PC += sBx }
func emitBaselineTForLoop(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	asm.LDR(jit.X0, mRegRegs, slotOff(a+1))
	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)

	doneLabel := nextLabel("tforloop_done")
	asm.BCond(jit.CondEQ, doneLabel) // nil -> exit loop

	// Not nil: R(A) = R(A+1), jump back
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	target := pc + 1 + sbx
	asm.B(pcLabel(target))

	asm.Label(doneLabel)
}
