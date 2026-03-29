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

// emitDiv emits ARM64 code for OpDiv (a / b, always returns float).
// Both operands may be int or float. Result is always NaN-boxed float.
func (ec *emitContext) emitDiv(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm

	// Load both operands from their home slots (NaN-boxed).
	ec.loadValue(jit.X0, instr.Args[0].ID) // X0 = NaN-boxed lhs
	ec.loadValue(jit.X1, instr.Args[1].ID) // X1 = NaN-boxed rhs

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

	// Store NaN-boxed float to home slot.
	ec.storeValue(jit.X0, instr.ID)
}

// emitUnm emits ARM64 code for OpUnm (-a).
// If the operand is int, uses NEG. If float, uses FNEGd.
func (ec *emitContext) emitUnm(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm

	ec.loadValue(jit.X0, instr.Args[0].ID)

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
	ec.storeValue(jit.X0, instr.ID)
}

// emitNot emits ARM64 code for OpNot (!a).
// Returns true if the operand is falsy (nil or false), false otherwise.
func (ec *emitContext) emitNot(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := ec.asm

	ec.loadValue(jit.X0, instr.Args[0].ID)

	// Check for nil: val == NB_ValNil
	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	isFalsy := ec.uniqueLabel("not_falsy")
	asm.BCond(jit.CondEQ, isFalsy)

	// Check for false: val == NB_TagBool|0
	asm.LoadImm64(jit.X1, nb64(jit.NB_TagBool))
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, isFalsy)

	// Truthy value: return false.
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool)) // false = NB_TagBool|0
	done := ec.uniqueLabel("not_done")
	asm.B(done)

	// Nil or false: return true.
	asm.Label(isFalsy)
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1)) // true = NB_TagBool|1

	asm.Label(done)
	ec.storeValue(jit.X0, instr.ID)
}

// emitFloatBinOp emits ARM64 code for type-generic binary arithmetic
// that handles both int and float operands. For int+int, produces int result.
// For any float operand, promotes to float and produces float result.
func (ec *emitContext) emitFloatBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}
	asm := ec.asm

	// Load both operands.
	ec.loadValue(jit.X0, instr.Args[0].ID)
	ec.loadValue(jit.X1, instr.Args[1].ID)

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
	ec.storeValue(jit.X0, instr.ID)
}

// uniqueLabel generates a unique label for the emitter to avoid collisions.
func (ec *emitContext) uniqueLabel(prefix string) string {
	ec.labelCounter++
	return fmt.Sprintf("%s_%d", prefix, ec.labelCounter)
}
