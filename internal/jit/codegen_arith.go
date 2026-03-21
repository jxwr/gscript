//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// emitArithInt emits integer arithmetic with type guards.
// Type guards are skipped for operands known to be TypeInt (type guard hoisting).
func (cg *Codegen) emitArithInt(pc int, inst uint32, arithOp string) error {
	aReg := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needGuardB := !bKnown
	needGuardC := !cKnown

	if needGuardB || needGuardC {
		exitLabel := fmt.Sprintf("arith_exit_%d", pc)
		if needGuardB {
			cg.loadRKTyp(X0, bIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if needGuardC {
			cg.loadRKTyp(X0, cIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}

		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		switch arithOp {
		case "ADD":
			cg.asm.ADDreg(X0, X0, X1)
		case "SUB":
			cg.asm.SUBreg(X0, X0, X1)
		case "MUL":
			cg.asm.MUL(X0, X0, X1)
		}
		cg.storeIntValue(aReg, X0)

		// Guard failure deferred to cold section.
		capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
		for k, v := range cg.pinnedRegs {
			capturedPinned[k] = v
		}
		cg.deferCold(exitLabel, func() {
			for vmReg, armReg := range capturedPinned {
				cg.spillPinnedRegNB(vmReg, armReg)
			}
			cg.asm.LoadImm64(X1, int64(pc))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
		})
	} else {
		// Both operands known TypeInt — no type guards needed.
		// Try direct register-register operation if all operands are pinned.
		aArm, aPinned := cg.pinnedRegs[aReg]
		var bArm, cArm Reg
		var bPinned, cPinned bool
		if !vm.IsRK(bIdx) {
			bArm, bPinned = cg.pinnedRegs[bIdx]
		}
		if !vm.IsRK(cIdx) {
			cArm, cPinned = cg.pinnedRegs[cIdx]
		}

		// Compute known constant values for C and B operands.
		// Check both RK constants and registers set by recent LOADINT.
		cImmVal := cg.rkSmallIntConst(cIdx)
		if cImmVal < 0 && !vm.IsRK(cIdx) {
			cImmVal = cg.regLoadIntConst(cIdx, pc)
		}
		bImmVal := cg.rkSmallIntConst(bIdx)
		if bImmVal < 0 && !vm.IsRK(bIdx) {
			bImmVal = cg.regLoadIntConst(bIdx, pc)
		}

		if aPinned && bPinned && cPinned {
			// All three in ARM registers — emit single instruction.
			switch arithOp {
			case "ADD":
				cg.asm.ADDreg(aArm, bArm, cArm)
			case "SUB":
				cg.asm.SUBreg(aArm, bArm, cArm)
			case "MUL":
				cg.asm.MUL(aArm, bArm, cArm)
			}
		} else if (arithOp == "ADD" || arithOp == "SUB") && cImmVal >= 0 {
			// ADD/SUB with small integer constant: use immediate form.
			// If B is a pinned register, use it directly as the source to avoid a MOV.
			bSrc := X0
			if bPinned {
				bSrc = bArm
			} else {
				cg.loadRKIval(X0, bIdx)
			}
			switch arithOp {
			case "ADD":
				cg.asm.ADDimm(X0, bSrc, uint16(cImmVal))
			case "SUB":
				cg.asm.SUBimm(X0, bSrc, uint16(cImmVal))
			}
			cg.storeIntValue(aReg, X0)
		} else if (arithOp == "ADD" || arithOp == "SUB") && arithOp == "ADD" && bImmVal >= 0 {
			// ADD R(A), imm, R(C) → ADDimm X0, Csrc, #imm (commutative)
			cSrc := X0
			if cPinned {
				cSrc = cArm
			} else {
				cg.loadRKIval(X0, cIdx)
			}
			cg.asm.ADDimm(X0, cSrc, uint16(bImmVal))
			cg.storeIntValue(aReg, X0)
		} else {
			// Fallback: load through X0/X1.
			cg.loadRKIval(X0, bIdx)
			cg.loadRKIval(X1, cIdx)
			switch arithOp {
			case "ADD":
				cg.asm.ADDreg(X0, X0, X1)
			case "SUB":
				cg.asm.SUBreg(X0, X0, X1)
			case "MUL":
				cg.asm.MUL(X0, X0, X1)
			}
			cg.storeIntValue(aReg, X0)
		}
	}
	return nil
}

func (cg *Codegen) emitUNM(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	bKnown := cg.isRKKnownInt(pc, bReg)

	if !bKnown {
		exitLabel := fmt.Sprintf("unm_exit_%d", pc)
		cg.loadRegTyp(X0, bReg)
		cg.emitCmpTag(X0, NB_TagIntShr48)
		cg.asm.BCond(CondNE, exitLabel)

		cg.loadRegIval(X0, bReg)
		cg.asm.NEG(X0, X0)
		cg.storeIntValue(aReg, X0)

		// Guard failure deferred to cold section.
		capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
		for k, v := range cg.pinnedRegs {
			capturedPinned[k] = v
		}
		cg.deferCold(exitLabel, func() {
			for vmReg, armReg := range capturedPinned {
				cg.spillPinnedRegNB(vmReg, armReg)
			}
			cg.asm.LoadImm64(X1, int64(pc))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
		})
	} else {
		cg.loadRegIval(X0, bReg)
		cg.asm.NEG(X0, X0)
		cg.storeIntValue(aReg, X0)
	}
	return nil
}

func (cg *Codegen) emitNOT(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	// NOT: R(A) = !truthy(R(B))
	// Truthy: nil and false are falsy, everything else truthy.
	// NaN-box tag: nil=0xFFFC, bool=0xFFFD. Check against NB_TagXxxShr48.

	cg.loadRegTyp(X0, bReg) // X0 = tag >> 48

	// Check if nil
	cg.emitCmpTag(X0, NB_TagNilShr48)
	cg.asm.BCond(CondEQ, fmt.Sprintf("not_true_%d", pc))

	// Check if false bool
	cg.emitCmpTag(X0, NB_TagBoolShr48)
	cg.asm.BCond(CondNE, fmt.Sprintf("not_false_%d", pc))
	cg.loadRegIval(X0, bReg) // loads NaN-boxed value, unbox (SBFX #0, #48)
	cg.asm.CMPimm(X0, 0)
	cg.asm.BCond(CondEQ, fmt.Sprintf("not_true_%d", pc))

	// Truthy → NOT = false
	cg.asm.Label(fmt.Sprintf("not_false_%d", pc))
	cg.asm.LoadImm64(X0, 0)
	cg.storeBoolValue(aReg, X0)
	cg.asm.B(fmt.Sprintf("not_done_%d", pc))

	// Falsy → NOT = true
	cg.asm.Label(fmt.Sprintf("not_true_%d", pc))
	cg.asm.LoadImm64(X0, 1)
	cg.storeBoolValue(aReg, X0)

	cg.asm.Label(fmt.Sprintf("not_done_%d", pc))
	return nil
}

// emitEQ: if (RK(B) == RK(C)) != bool(A) then PC++ (skip next instruction)
func (cg *Codegen) emitEQ(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needExit := !bKnown || !cKnown

	var exitLabel string
	if needExit {
		exitLabel = fmt.Sprintf("eq_exit_%d", pc)
		if !bKnown {
			cg.loadRKTyp(X0, bIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if !cKnown {
			cg.loadRKTyp(X0, cIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
	}

	// Use CMP immediate when one operand is a small non-negative integer constant.
	// EQ is symmetric so no condition reversal needed.
	// Check both RK constants and registers set by a recent LOADINT.
	cImm := cg.rkSmallIntConst(cIdx)
	if cImm < 0 && !vm.IsRK(cIdx) {
		cImm = cg.regLoadIntConst(cIdx, pc)
	}
	bImm := cg.rkSmallIntConst(bIdx)
	if bImm < 0 && !vm.IsRK(bIdx) {
		bImm = cg.regLoadIntConst(bIdx, pc)
	}

	if cImm >= 0 {
		// Use pinned register directly as source if available.
		bSrc := X0
		if !vm.IsRK(bIdx) {
			if armReg, ok := cg.pinnedRegs[bIdx]; ok {
				bSrc = armReg
			} else {
				cg.loadRKIval(X0, bIdx)
			}
		} else {
			cg.loadRKIval(X0, bIdx)
		}
		cg.asm.CMPimm(bSrc, uint16(cImm))
	} else if bImm >= 0 {
		cSrc := X0
		if !vm.IsRK(cIdx) {
			if armReg, ok := cg.pinnedRegs[cIdx]; ok {
				cSrc = armReg
			} else {
				cg.loadRKIval(X0, cIdx)
			}
		} else {
			cg.loadRKIval(X0, cIdx)
		}
		cg.asm.CMPimm(cSrc, uint16(bImm))
	} else {
		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		cg.asm.CMPreg(X0, X1)
	}

	if aFlag != 0 {
		cg.asm.BCond(CondNE, skipLabel)
	} else {
		cg.asm.BCond(CondEQ, skipLabel)
	}

	if needExit {
		return cg.emitComparisonSideExit(pc, exitLabel)
	}
	return nil
}

// emitLT: if (RK(B) < RK(C)) != bool(A) then PC++

func (cg *Codegen) emitLT(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needExit := !bKnown || !cKnown

	var exitLabel string
	if needExit {
		exitLabel = fmt.Sprintf("lt_exit_%d", pc)
		if !bKnown {
			cg.loadRKTyp(X0, bIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if !cKnown {
			cg.loadRKTyp(X0, cIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
	}

	// Use CMP immediate when one operand is a small non-negative integer constant.
	// Check both RK constants and registers set by a recent LOADINT.
	cImm := cg.rkSmallIntConst(cIdx)
	if cImm < 0 && !vm.IsRK(cIdx) {
		cImm = cg.regLoadIntConst(cIdx, pc)
	}
	bImm := cg.rkSmallIntConst(bIdx)
	if bImm < 0 && !vm.IsRK(bIdx) {
		bImm = cg.regLoadIntConst(bIdx, pc)
	}

	if cImm >= 0 {
		// Use pinned register directly as source if available.
		bSrc := X0
		if !vm.IsRK(bIdx) {
			if armReg, ok := cg.pinnedRegs[bIdx]; ok {
				bSrc = armReg
			} else {
				cg.loadRKIval(X0, bIdx)
			}
		} else {
			cg.loadRKIval(X0, bIdx)
		}
		cg.asm.CMPimm(bSrc, uint16(cImm))
	} else if bImm >= 0 {
		// B < C with B constant: load C, compare reversed.
		// B < C ⟺ C > B, so flip the condition.
		cSrc := X0
		if !vm.IsRK(cIdx) {
			if armReg, ok := cg.pinnedRegs[cIdx]; ok {
				cSrc = armReg
			} else {
				cg.loadRKIval(X0, cIdx)
			}
		} else {
			cg.loadRKIval(X0, cIdx)
		}
		cg.asm.CMPimm(cSrc, uint16(bImm))
		// Reverse the condition: instead of checking B < C, we check C > B.
		if aFlag != 0 {
			// Original: skip if NOT (B < C), i.e., B >= C → with reversal: skip if C <= B
			cg.asm.BCond(CondLE, skipLabel)
		} else {
			// Original: skip if (B < C) → with reversal: skip if C > B
			cg.asm.BCond(CondGT, skipLabel)
		}
		if needExit {
			return cg.emitComparisonSideExit(pc, exitLabel)
		}
		return nil
	} else {
		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		cg.asm.CMPreg(X0, X1)
	}

	if aFlag != 0 {
		cg.asm.BCond(CondGE, skipLabel)
	} else {
		cg.asm.BCond(CondLT, skipLabel)
	}

	if needExit {
		return cg.emitComparisonSideExit(pc, exitLabel)
	}
	return nil
}

// emitLE: if (RK(B) <= RK(C)) != bool(A) then PC++
// Note: the VM implements LE as !(C < B).

func (cg *Codegen) emitLE(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needExit := !bKnown || !cKnown

	var exitLabel string
	if needExit {
		exitLabel = fmt.Sprintf("le_exit_%d", pc)
		if !bKnown {
			cg.loadRKTyp(X0, bIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if !cKnown {
			cg.loadRKTyp(X0, cIdx)
			cg.emitCmpTag(X0, NB_TagIntShr48)
			cg.asm.BCond(CondNE, exitLabel)
		}
	}

	// Use CMP immediate when one operand is a small non-negative integer constant.
	// Check both RK constants and registers set by a recent LOADINT.
	cImmLE := cg.rkSmallIntConst(cIdx)
	if cImmLE < 0 && !vm.IsRK(cIdx) {
		cImmLE = cg.regLoadIntConst(cIdx, pc)
	}
	bImmLE := cg.rkSmallIntConst(bIdx)
	if bImmLE < 0 && !vm.IsRK(bIdx) {
		bImmLE = cg.regLoadIntConst(bIdx, pc)
	}

	if cImmLE >= 0 {
		// B <= C with C as immediate: CMP B, #C then check LE.
		bSrc := X0
		if !vm.IsRK(bIdx) {
			if armReg, ok := cg.pinnedRegs[bIdx]; ok {
				bSrc = armReg
			} else {
				cg.loadRKIval(X0, bIdx)
			}
		} else {
			cg.loadRKIval(X0, bIdx)
		}
		cg.asm.CMPimm(bSrc, uint16(cImmLE))
	} else if bImmLE >= 0 {
		// B <= C with B constant: CMP C, #B, then reverse condition.
		// B <= C ⟺ C >= B
		cSrc := X0
		if !vm.IsRK(cIdx) {
			if armReg, ok := cg.pinnedRegs[cIdx]; ok {
				cSrc = armReg
			} else {
				cg.loadRKIval(X0, cIdx)
			}
		} else {
			cg.loadRKIval(X0, cIdx)
		}
		cg.asm.CMPimm(cSrc, uint16(bImmLE))
		if aFlag != 0 {
			// Original: skip if NOT (B <= C), i.e., B > C → with reversal: skip if C < B
			cg.asm.BCond(CondLT, skipLabel)
		} else {
			// Original: skip if (B <= C) → with reversal: skip if C >= B
			cg.asm.BCond(CondGE, skipLabel)
		}
		if needExit {
			return cg.emitComparisonSideExit(pc, exitLabel)
		}
		return nil
	} else {
		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		cg.asm.CMPreg(X0, X1)
	}

	if aFlag != 0 {
		cg.asm.BCond(CondGT, skipLabel)
	} else {
		cg.asm.BCond(CondLE, skipLabel)
	}

	if needExit {
		return cg.emitComparisonSideExit(pc, exitLabel)
	}
	return nil
}

func (cg *Codegen) emitComparisonSideExit(pc int, exitLabel string) error {
	// Capture current pinning state for the cold stub and resume stubs.
	capturedVars := make([]int, len(cg.pinnedVars))
	copy(capturedVars, cg.pinnedVars)
	capturedRegs := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedRegs[k] = v
	}

	// Guard failure deferred to cold section.
	cg.deferCold(exitLabel, func() {
		for vmReg, armReg := range capturedRegs {
			cg.spillPinnedRegNB(vmReg, armReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // call-exit: executor handles non-integer comparison and resumes
		cg.asm.B("epilogue")
	})

	// Defer resume stubs with captured pinning state.
	// These will be emitted after the main instruction loop.
	cmpStub := fmt.Sprintf("cmp_resume_%d", pc)
	cg.cmpResumeStubs = append(cg.cmpResumeStubs,
		cmpResumeStub{cmpStub + "_1", pcLabel(pc + 1), capturedVars, capturedRegs},
		cmpResumeStub{cmpStub + "_2", pcLabel(pc + 2), capturedVars, capturedRegs},
	)

	return nil
}

func (cg *Codegen) emitJMP(pc int, inst uint32) error {
	sbx := vm.DecodesBx(inst)
	target := pc + 1 + sbx
	cg.asm.B(pcLabel(target))
	return nil
}

func (cg *Codegen) emitTest(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	c := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2) // skip next if test fails

	// Load NaN-box tag (top 16 bits)
	cg.loadRegTyp(X0, aReg) // X0 = tag >> 48

	// Check nil → falsy (NaN-box tag for nil = 0xFFFC)
	cg.emitCmpTag(X0, NB_TagNilShr48)
	if c != 0 {
		// C=1: skip if NOT truthy (i.e., falsy) → skip if nil
		cg.asm.BCond(CondEQ, skipLabel)
	} else {
		// C=0: skip if truthy → don't skip if nil
		notNil := fmt.Sprintf("test_not_nil_%d", pc)
		cg.asm.BCond(CondNE, notNil)
		// nil is falsy, C=0 means skip if truthy → no skip
		cg.asm.B(pcLabel(pc + 1))
		cg.asm.Label(notNil)
	}

	// Check bool (NaN-box tag for bool = 0xFFFD)
	cg.emitCmpTag(X0, NB_TagBoolShr48)
	notBool := fmt.Sprintf("test_truthy_%d", pc)
	cg.asm.BCond(CondNE, notBool)

	// It's a bool — check payload (bit 0)
	cg.loadRegIval(X0, aReg) // loads NaN-boxed value, unbox int (SBFX #0, #48)
	cg.asm.CMPimm(X0, 0)
	if c != 0 {
		// C=1: skip if NOT truthy → skip if bool(false) (payload==0)
		cg.asm.BCond(CondEQ, skipLabel)
	} else {
		// C=0: skip if truthy → skip if bool(true) (payload!=0)
		cg.asm.BCond(CondNE, skipLabel)
	}
	cg.asm.B(pcLabel(pc + 1)) // not skipping
	// Everything else is truthy
	cg.asm.Label(notBool)
	if c == 0 {
		// C=0: skip if truthy → yes, everything non-nil non-false is truthy
		cg.asm.B(skipLabel)
	}
	// C=1: skip if NOT truthy → no, it's truthy → don't skip
	return nil
}
