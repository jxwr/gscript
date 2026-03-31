//go:build darwin && arm64

// emit_call.go handles deoptimization and extended operations for the Method JIT.
// When the JIT encounters an unsupported operation (calls, globals, table ops,
// concat, etc.), it "deopts" by setting ExitCode=2 in ExecContext and returning
// to Go. The Go-side Execute method then falls back to the VM interpreter.
//
// This file also implements operations that don't fit in emit.go:
// - OpDiv (float division, always returns float)
// - OpUnm (unary negate for int and float)
// - OpNot (logical not)
// - Float-aware arithmetic (OpAdd, OpSub, OpMul with float operands)

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// emitCheckIsInt emits ARM64 code that checks if a NaN-boxed value in valReg
// is an integer (top 16 bits == 0xFFFE). After this: CondEQ = int, CondNE = not int.
// Uses scratch as temporary register. Also clobbers X3.
func emitCheckIsInt(asm *jit.Assembler, valReg, scratch jit.Reg) {
	asm.LSRimm(scratch, valReg, 48)           // scratch = top 16 bits
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)  // X3 = 0xFFFE
	asm.CMPreg(scratch, jit.X3)                // EQ = int, NE = not int
}

// emitDeopt emits ARM64 code that bails out to the interpreter.
// Sets ExecContext.ExitCode = ExitDeopt (2) and jumps to the deopt epilogue.
func (ec *emitContext) emitDeopt(instr *Instr) {
	asm := ec.asm
	asm.LoadImm64(jit.X0, ExitDeopt)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")
}

// emitGuardType emits a native type check for OpGuardType.
// On success, passes the value through. On failure, deopts.
func (ec *emitContext) emitGuardType(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	asm := ec.asm

	// Load the value to check.
	srcReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if srcReg != jit.X0 {
		asm.MOVreg(jit.X0, srcReg)
	}

	guardType := Type(instr.Aux)
	switch guardType {
	case TypeInt:
		// Check NaN-box int tag: top 16 bits must be 0xFFFE.
		emitCheckIsInt(asm, jit.X0, jit.X2)
		deoptLabel := ec.uniqueLabel("guard_deopt")
		asm.BCond(jit.CondNE, deoptLabel)
		// Success: store the value as the guard's result.
		ec.storeResultNB(jit.X0, instr.ID)
		doneLabel := ec.uniqueLabel("guard_done")
		asm.B(doneLabel)
		// Deopt path.
		asm.Label(deoptLabel)
		ec.emitDeopt(instr)
		asm.Label(doneLabel)

	default:
		// Unsupported guard type: just pass through.
		ec.storeResultNB(jit.X0, instr.ID)
	}
}

// emitDiv emits ARM64 code for OpDiv (a / b, always returns float).
// Both operands may be int or float. Result is always NaN-boxed float.
//
// When the instruction is OpDivFloat with TypeFloat, both operands are known
// to be float, so we use the raw float fast path with no type checks.
func (ec *emitContext) emitDiv(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm

	// Fast path: OpDivFloat with TypeFloat — both operands are float, use raw FPR path.
	if instr.Op == OpDivFloat && instr.Type == TypeFloat {
		lhsF := ec.resolveRawFloat(instr.Args[0].ID, jit.D0)
		rhsF := ec.resolveRawFloat(instr.Args[1].ID, jit.D1)
		dstF := jit.FReg(jit.D0)
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
			dstF = jit.FReg(pr.Reg)
		}
		asm.FDIVd(dstF, lhsF, rhsF)
		ec.storeRawFloat(dstF, instr.ID)
		return
	}

	// Generic path: operands may be int or float, with type checks.
	// Load both operands as NaN-boxed values.
	lhsReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if lhsReg != jit.X0 {
		ec.asm.MOVreg(jit.X0, lhsReg)
	}
	rhsReg := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	if rhsReg != jit.X1 {
		ec.asm.MOVreg(jit.X1, rhsReg)
	}

	// Check if lhs is int.
	emitCheckIsInt(asm, jit.X0, jit.X2)
	lhsNotInt := ec.uniqueLabel("div_lhs_not_int")
	lhsBoth := ec.uniqueLabel("div_both_ready")
	asm.BCond(jit.CondNE, lhsNotInt)

	// LHS is int: unbox, convert to float.
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.SCVTF(jit.D0, jit.X0)
	asm.B(lhsBoth)

	// LHS is float: move bits to FP register.
	asm.Label(lhsNotInt)
	asm.FMOVtoFP(jit.D0, jit.X0)

	asm.Label(lhsBoth)

	// Check if rhs is int.
	emitCheckIsInt(asm, jit.X1, jit.X2)
	rhsNotInt := ec.uniqueLabel("div_rhs_not_int")
	rhsBoth := ec.uniqueLabel("div_do_div")
	asm.BCond(jit.CondNE, rhsNotInt)

	// RHS is int: unbox, convert to float.
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.SCVTF(jit.D1, jit.X1)
	asm.B(rhsBoth)

	asm.Label(rhsNotInt)
	asm.FMOVtoFP(jit.D1, jit.X1)

	asm.Label(rhsBoth)

	// D0 = lhs, D1 = rhs (both float64). Divide.
	asm.FDIVd(jit.D0, jit.D0, jit.D1)

	// Move result bits back to GP register (float stored as raw IEEE bits).
	asm.FMOVtoGP(jit.X0, jit.D0)

	// Store NaN-boxed float result.
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitUnm emits ARM64 code for OpUnm (-a).
// If the operand is int, uses NEG. If float, uses FNEGd.
func (ec *emitContext) emitUnm(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm

	// Load operand as NaN-boxed for type dispatch.
	unmSrc := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if unmSrc != jit.X0 {
		ec.asm.MOVreg(jit.X0, unmSrc)
	}

	// Check if int.
	emitCheckIsInt(asm, jit.X0, jit.X2)
	notInt := ec.uniqueLabel("unm_not_int")
	done := ec.uniqueLabel("unm_done")
	asm.BCond(jit.CondNE, notInt)

	// Int path: unbox, negate, rebox.
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.NEG(jit.X0, jit.X0)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	asm.B(done)

	// Float path: move to FP, negate, move back.
	asm.Label(notInt)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)

	asm.Label(done)
	// Store NaN-boxed result (int or float).
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitNot emits ARM64 code for OpNot (!a).
// Returns true if the operand is falsy (nil or false), false otherwise.
func (ec *emitContext) emitNot(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm

	// Load operand as NaN-boxed for truthiness check.
	notSrc := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if notSrc != jit.X0 {
		ec.asm.MOVreg(jit.X0, notSrc)
	}

	// Check for nil: val == NB_ValNil (1 instruction: MOVZ with top chunk)
	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	isFalsy := ec.uniqueLabel("not_falsy")
	asm.BCond(jit.CondEQ, isFalsy)

	// Check for false: val == NB_TagBool|0. Use pinned X25 directly.
	asm.CMPreg(jit.X0, mRegTagBool)
	asm.BCond(jit.CondEQ, isFalsy)

	// Truthy value: return false (NB_TagBool|0). Use pinned X25.
	asm.MOVreg(jit.X0, mRegTagBool)
	done := ec.uniqueLabel("not_done")
	asm.B(done)

	// Nil or false: return true (NB_TagBool|1). Compute from pinned X25.
	asm.Label(isFalsy)
	asm.ADDimm(jit.X0, mRegTagBool, 1)

	asm.Label(done)
	// Store NaN-boxed bool result.
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitGuardTruthy emits ARM64 code for OpGuardTruthy.
// Converts any value to a NaN-boxed bool based on truthiness:
// nil and false are falsy (returns NB_TagBool|0), everything else is truthy
// (returns NB_TagBool|1). This is the non-inverted version of emitNot.
func (ec *emitContext) emitGuardTruthy(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm

	// Load operand as NaN-boxed for truthiness check.
	src := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if src != jit.X0 {
		asm.MOVreg(jit.X0, src)
	}

	// Check for nil: val == NB_ValNil.
	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	isFalsy := ec.uniqueLabel("truthy_falsy")
	asm.BCond(jit.CondEQ, isFalsy)

	// Check for false: val == NB_TagBool|0. Use pinned X25.
	asm.CMPreg(jit.X0, mRegTagBool)
	asm.BCond(jit.CondEQ, isFalsy)

	// Truthy value: return true (NB_TagBool|1).
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	done := ec.uniqueLabel("truthy_done")
	asm.B(done)

	// Nil or false: return false (NB_TagBool|0).
	asm.Label(isFalsy)
	asm.MOVreg(jit.X0, mRegTagBool)

	asm.Label(done)
	// Store NaN-boxed bool result.
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitFloatBinOp emits ARM64 code for type-generic binary arithmetic
// that handles both int and float operands. For int+int, produces int result.
// For any float operand, promotes to float and produces float result.
func (ec *emitContext) emitFloatBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm

	// Load both operands as NaN-boxed for type dispatch.
	lhsReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if lhsReg != jit.X0 {
		ec.asm.MOVreg(jit.X0, lhsReg)
	}
	rhsReg := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	if rhsReg != jit.X1 {
		ec.asm.MOVreg(jit.X1, rhsReg)
	}

	done := ec.uniqueLabel("arith_done")

	// Check if LHS is int.
	emitCheckIsInt(asm, jit.X0, jit.X2)
	lhsNotInt := ec.uniqueLabel("arith_lhs_not_int")
	asm.BCond(jit.CondNE, lhsNotInt)

	// LHS is int. Check RHS.
	emitCheckIsInt(asm, jit.X1, jit.X2)
	rhsNotInt := ec.uniqueLabel("arith_rhs_not_int")
	asm.BCond(jit.CondNE, rhsNotInt)

	// Both are int: fast integer path.
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	switch op {
	case intBinAdd:
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case intBinSub:
		asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case intBinMul:
		asm.MUL(jit.X0, jit.X0, jit.X1)
	case intBinMod:
		asm.SDIV(jit.X2, jit.X0, jit.X1)
		asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	}
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	asm.B(done)

	// LHS is float (not int).
	asm.Label(lhsNotInt)
	asm.FMOVtoFP(jit.D0, jit.X0) // D0 = lhs as float

	// Check if RHS is int.
	emitCheckIsInt(asm, jit.X1, jit.X2)
	bothFloat := ec.uniqueLabel("arith_both_float")
	asm.BCond(jit.CondNE, bothFloat)

	// RHS is int, LHS is float: convert RHS to float.
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.SCVTF(jit.D1, jit.X1)
	doFloat := ec.uniqueLabel("arith_do_float")
	asm.B(doFloat)

	// RHS is not int while LHS was int: convert LHS to float.
	asm.Label(rhsNotInt)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.SCVTF(jit.D0, jit.X0)
	asm.FMOVtoFP(jit.D1, jit.X1) // D1 = rhs as float
	asm.B(doFloat)

	// Both float.
	asm.Label(bothFloat)
	asm.FMOVtoFP(jit.D1, jit.X1)

	// Float arithmetic.
	asm.Label(doFloat)
	switch op {
	case intBinAdd:
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case intBinSub:
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case intBinMul:
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	case intBinMod:
		// Float mod is complex; deopt for now.
		ec.emitDeopt(instr)
		return
	}

	// Move float result back to GP and store.
	asm.FMOVtoGP(jit.X0, jit.D0)

	asm.Label(done)
	// Store NaN-boxed result (int or float).
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitTypedFloatBinOp emits ARM64 code for type-specialized float binary ops
// (OpAddFloat, OpSubFloat, OpMulFloat). Both operands are known to be float,
// so we skip the type check and go straight to FP arithmetic.
//
// Raw float mode: when the result type is TypeFloat and has an FPR allocation,
// operands are resolved as raw floats in FPRs and the result stays in an FPR.
// This avoids FMOVtoFP/FMOVtoGP conversions between every float op.
func (ec *emitContext) emitTypedFloatBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm

	// Raw float mode: resolve operands into FPRs, compute in FPR, store as raw float.
	if instr.Type == TypeFloat {
		lhsF := ec.resolveRawFloat(instr.Args[0].ID, jit.D0)
		rhsF := ec.resolveRawFloat(instr.Args[1].ID, jit.D1)
		// Destination: use allocated FPR if available, else D0.
		dstF := jit.FReg(jit.D0)
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
			dstF = jit.FReg(pr.Reg)
		}
		switch op {
		case intBinAdd:
			asm.FADDd(dstF, lhsF, rhsF)
		case intBinSub:
			asm.FSUBd(dstF, lhsF, rhsF)
		case intBinMul:
			asm.FMULd(dstF, lhsF, rhsF)
		}
		ec.storeRawFloat(dstF, instr.ID)
		return
	}

	// Fallback: NaN-boxed float ops (original code path for non-TypeFloat).
	lhs := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	asm.FMOVtoFP(jit.D0, lhs)
	rhs := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	asm.FMOVtoFP(jit.D1, rhs)

	switch op {
	case intBinAdd:
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case intBinSub:
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case intBinMul:
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	}

	// Move float result back to GP (raw IEEE 754 bits = NaN-boxed float).
	asm.FMOVtoGP(jit.X0, jit.D0)
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitFloatCmp emits ARM64 code for float comparison (OpLtFloat, OpLeFloat).
// Uses FCMP on FP registers instead of integer CMP, since NaN-boxed floats
// are raw IEEE 754 bits and integer comparison doesn't handle sign/exponent
// ordering correctly for floats.
//
// With raw float mode, resolves operands from FPRs directly when available,
// avoiding the FMOVtoFP conversion from GPR.
func (ec *emitContext) emitFloatCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm

	// Resolve both operands as raw floats in FPRs.
	lhsF := ec.resolveRawFloat(instr.Args[0].ID, jit.D0)
	rhsF := ec.resolveRawFloat(instr.Args[1].ID, jit.D1)

	// Float compare sets NZCV flags.
	asm.FCMPd(lhsF, rhsF)

	// Set result: 1 if condition true, 0 if false.
	asm.CSET(jit.X0, cond)

	// Box as bool: NB_TagBool | (0 or 1).
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)

	// Store NaN-boxed bool result (comparison result is always bool, not float).
	ec.storeResultNB(jit.X0, instr.ID)
}

// emitNegFloat emits ARM64 code for OpNegFloat (-float).
// The operand is known to be float, so we skip the type check.
// With raw float mode, operates directly on FPRs.
func (ec *emitContext) emitNegFloat(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm

	if instr.Type == TypeFloat {
		srcF := ec.resolveRawFloat(instr.Args[0].ID, jit.D0)
		dstF := jit.FReg(jit.D0)
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
			dstF = jit.FReg(pr.Reg)
		}
		asm.FNEGd(dstF, srcF)
		ec.storeRawFloat(dstF, instr.ID)
		return
	}

	// Fallback: NaN-boxed path.
	src := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	asm.FMOVtoFP(jit.D0, src)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	ec.storeResultNB(jit.X0, instr.ID)
}

// uniqueLabel generates a unique label for the emitter to avoid collisions.
func (ec *emitContext) uniqueLabel(prefix string) string {
	ec.labelCounter++
	return fmt.Sprintf("%s_%d", prefix, ec.labelCounter)
}
