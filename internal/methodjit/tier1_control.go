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
func emitBaselineForLoop(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	// Load idx (R(A)), limit (R(A+1)), step (R(A+2))
	asm.LDR(jit.X0, mRegRegs, slotOff(a))   // idx
	asm.LDR(jit.X1, mRegRegs, slotOff(a+1)) // limit
	asm.LDR(jit.X2, mRegRegs, slotOff(a+2)) // step

	floatLabel := nextLabel("forloop_float")
	exitLabel := nextLabel("forloop_exit")
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
	// Continue: store idx, R(A+3) = idx, jump to body.
	jit.EmitBoxIntFast(asm, jit.X3, jit.X3, mRegTagInt)
	asm.STR(jit.X3, mRegRegs, slotOff(a))
	asm.STR(jit.X3, mRegRegs, slotOff(a+3))
	asm.B(contLabel)

	asm.Label(positiveStepLabel)
	// Positive step: idx <= limit
	asm.CMPreg(jit.X3, jit.X5)
	asm.BCond(jit.CondGT, exitLabel)
	jit.EmitBoxIntFast(asm, jit.X3, jit.X3, mRegTagInt)
	asm.STR(jit.X3, mRegRegs, slotOff(a))
	asm.STR(jit.X3, mRegRegs, slotOff(a+3))
	asm.B(contLabel)

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
	asm.B(contLabel)

	asm.Label(floatPosLabel)
	// Positive step: idx <= limit
	asm.FCMPd(jit.D0, jit.D1)
	asm.BCond(jit.CondGT, exitLabel) // idx > limit -> exit

	// Continue
	asm.STR(jit.X0, mRegRegs, slotOff(a+3))
	asm.B(contLabel)

	asm.Label(exitLabel)
}

// ---------------------------------------------------------------------------
// Return
// ---------------------------------------------------------------------------

// emitBaselineReturn: return R(A)..R(A+B-2); B=0 variable, B=1 nothing
func emitBaselineReturn(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	if b == 1 {
		// Return nothing: store nil in slot 0.
		asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
		asm.STR(jit.X0, mRegRegs, slotOff(0))
	} else if b >= 2 {
		// Return b-1 values starting from R(A).
		// For baseline, we put the first return value in slot 0.
		if a != 0 {
			asm.LDR(jit.X0, mRegRegs, slotOff(a))
			asm.STR(jit.X0, mRegRegs, slotOff(0))
		}
	} else {
		// b == 0: variable return. Copy R(A) to slot 0.
		if a != 0 {
			asm.LDR(jit.X0, mRegRegs, slotOff(a))
			asm.STR(jit.X0, mRegRegs, slotOff(0))
		}
	}

	// Jump to epilogue.
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
