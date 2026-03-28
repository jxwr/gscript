//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// CALL (call-exit)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitCallExit(inst *SSAInst) {
	ec.emitCallExitInst(inst)
}

func (ec *emitCtx) emitCallExitInst(inst *SSAInst) {
	asm := ec.asm
	ec.hasCallExit = true

	// Store back ALL modified registers to memory (type-safe) before exiting.
	// The interpreter needs to see current register values to execute the instruction.
	ec.emitStoreBackTypeSafe()

	// Set ExitPC to the call instruction's bytecode PC
	asm.LoadImm64(X9, int64(inst.PC))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Exit with code 1 (side-exit). The interpreter resumes at ExitPC,
	// executes the CALL instruction (including any nested loops/recursion),
	// then FORLOOP back-edge re-enters the trace. No resume dispatch needed.
	asm.LoadImm64(X0, 1)
	asm.B("epilogue")
}

// ────────────────────────────────────────────────────────────────────────────
// Intrinsics
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitIntrinsic(ref SSARef, inst *SSAInst) {
	switch int(inst.AuxInt) {
	// --- Float unary intrinsics: sqrt, abs, floor, ceil ---
	case IntrinsicSqrt:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FSQRTd(dst, src)
		})
	case IntrinsicAbs:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FABSd(dst, src)
		})
	case IntrinsicFloor:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FRINTMd(dst, src)
		})
	case IntrinsicCeil:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FRINTPd(dst, src)
		})

	// --- Float binary intrinsics: max, min ---
	case IntrinsicMax:
		ec.emitFloatBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b FReg) {
			asm.FMAXNMd(dst, a, b)
		})
	case IntrinsicMin:
		ec.emitFloatBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b FReg) {
			asm.FMINNMd(dst, a, b)
		})

	// --- Integer binary intrinsics: bit32 ---
	case IntrinsicBand:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.ANDreg(dst, a, b)
		})
	case IntrinsicBor:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.ORRreg(dst, a, b)
		})
	case IntrinsicBxor:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.EORreg(dst, a, b)
		})
	case IntrinsicLshift:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.LSLreg(dst, a, b)
		})
	case IntrinsicRshift:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.LSRreg(dst, a, b)
		})

	// --- Integer unary intrinsic: bnot ---
	case IntrinsicBnot:
		ec.emitIntUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src Reg) {
			// MVN Xd, Xm = ORN Xd, XZR, Xm
			asm.ORNreg(dst, XZR, src)
		})

	default:
		// Unknown intrinsic — fall back to call-exit
		ec.emitCallExitInst(inst)
	}
}

// emitFloatUnaryIntrinsic: R(A) = op(R(A+1))
func (ec *emitCtx) emitFloatUnaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, FReg, FReg)) {
	argSlot := int(inst.Slot) + 1

	var argFReg FReg = D0
	if freg, ok := ec.regMap.FloatReg(argSlot); ok {
		argFReg = freg
	} else {
		ec.asm.FLDRd(D0, regRegs, argSlot*ValueSize)
		argFReg = D0
	}

	dstFReg := ec.getFloatDst(ref, inst, D1)
	op(ec.asm, dstFReg, argFReg)
	ec.spillFloat(ref, inst, dstFReg)
}

// emitFloatBinaryIntrinsic: R(A) = op(R(A+1), R(A+2))
func (ec *emitCtx) emitFloatBinaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, FReg, FReg, FReg)) {
	argSlot1 := int(inst.Slot) + 1
	argSlot2 := int(inst.Slot) + 2

	var a1 FReg = D0
	if freg, ok := ec.regMap.FloatReg(argSlot1); ok {
		a1 = freg
	} else {
		ec.asm.FLDRd(D0, regRegs, argSlot1*ValueSize)
	}

	var a2 FReg = D1
	if freg, ok := ec.regMap.FloatReg(argSlot2); ok {
		a2 = freg
	} else {
		ec.asm.FLDRd(D1, regRegs, argSlot2*ValueSize)
	}

	dstFReg := ec.getFloatDst(ref, inst, D2)
	op(ec.asm, dstFReg, a1, a2)
	ec.spillFloat(ref, inst, dstFReg)
}

// emitIntBinaryIntrinsic: R(A) = op(R(A+1), R(A+2))
func (ec *emitCtx) emitIntBinaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, Reg, Reg, Reg)) {
	argSlot1 := int(inst.Slot) + 1
	argSlot2 := int(inst.Slot) + 2

	// Load arg1
	var a1 Reg = X0
	if reg, ok := ec.regMap.IntReg(argSlot1); ok {
		a1 = reg
	} else {
		ec.asm.LDR(X0, regRegs, argSlot1*ValueSize)
		EmitUnboxInt(ec.asm, X0, X0)
	}

	// Load arg2
	var a2 Reg = X1
	if reg, ok := ec.regMap.IntReg(argSlot2); ok {
		a2 = reg
	} else {
		ec.asm.LDR(X1, regRegs, argSlot2*ValueSize)
		EmitUnboxInt(ec.asm, X1, X1)
	}

	dst := ec.getIntDst(ref, inst, X2)
	op(ec.asm, dst, a1, a2)
	ec.spillInt(ref, inst, dst)
}

// emitIntUnaryIntrinsic: R(A) = op(R(A+1))
func (ec *emitCtx) emitIntUnaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, Reg, Reg)) {
	argSlot := int(inst.Slot) + 1

	var a1 Reg = X0
	if reg, ok := ec.regMap.IntReg(argSlot); ok {
		a1 = reg
	} else {
		ec.asm.LDR(X0, regRegs, argSlot*ValueSize)
		EmitUnboxInt(ec.asm, X0, X0)
	}

	dst := ec.getIntDst(ref, inst, X1)
	op(ec.asm, dst, a1)
	ec.spillInt(ref, inst, dst)
}
