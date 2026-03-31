//go:build darwin && arm64

// tier2_emit.go implements per-instruction ARM64 emission for the Tier 2
// memory-to-memory code emitter. Every SSA value lives in a memory slot.
// For each instruction:
//   1. Load operands from slots into scratch registers (X0, X1)
//   2. Execute the ARM64 operation
//   3. Store result back to a slot
//
// Type-specialized ops (OpAddInt, etc.) emit direct integer/float ARM64
// instructions without runtime type dispatch. Generic ops use the NaN-boxing
// helpers for type checks.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// emitInstr dispatches a single IR instruction to the appropriate emitter.
func (tc *tier2Context) emitInstr(instr *Instr, block *Block) {
	switch instr.Op {
	// --- Constants ---
	case OpConstInt:
		tc.t2EmitConstInt(instr)
	case OpConstFloat:
		tc.t2EmitConstFloat(instr)
	case OpConstBool:
		tc.t2EmitConstBool(instr)
	case OpConstNil:
		tc.t2EmitConstNil(instr)
	case OpConstString:
		tc.t2EmitOpExit(instr) // exit to Go

	// --- Slot access ---
	case OpLoadSlot:
		// No code needed: slot is already the home.
	case OpStoreSlot:
		tc.t2EmitStoreSlot(instr)

	// --- Type-specialized integer arithmetic ---
	case OpAddInt:
		tc.t2EmitIntBinOp(instr, "add")
	case OpSubInt:
		tc.t2EmitIntBinOp(instr, "sub")
	case OpMulInt:
		tc.t2EmitIntBinOp(instr, "mul")
	case OpModInt:
		tc.t2EmitIntBinOp(instr, "mod")
	case OpNegInt:
		tc.t2EmitNegInt(instr)

	// --- Type-specialized float arithmetic ---
	case OpAddFloat:
		tc.t2EmitFloatBinOp(instr, "add")
	case OpSubFloat:
		tc.t2EmitFloatBinOp(instr, "sub")
	case OpMulFloat:
		tc.t2EmitFloatBinOp(instr, "mul")
	case OpDivFloat:
		tc.t2EmitFloatBinOp(instr, "div")
	case OpNegFloat:
		tc.t2EmitNegFloat(instr)

	// --- Generic arithmetic (needs type dispatch) ---
	case OpAdd:
		tc.t2EmitGenericArith(instr, "add")
	case OpSub:
		tc.t2EmitGenericArith(instr, "sub")
	case OpMul:
		tc.t2EmitGenericArith(instr, "mul")
	case OpDiv:
		tc.t2EmitGenericArith(instr, "div")
	case OpMod:
		tc.t2EmitGenericArith(instr, "mod")
	case OpUnm:
		tc.t2EmitGenericUnm(instr)
	case OpNot:
		tc.t2EmitNot(instr)

	// --- Type-specialized comparison ---
	case OpEqInt:
		tc.t2EmitIntCmp(instr, jit.CondEQ)
	case OpLtInt:
		tc.t2EmitIntCmp(instr, jit.CondLT)
	case OpLeInt:
		tc.t2EmitIntCmp(instr, jit.CondLE)
	case OpLtFloat:
		tc.t2EmitFloatCmp(instr, jit.CondLT)
	case OpLeFloat:
		tc.t2EmitFloatCmp(instr, jit.CondLE)

	// --- Generic comparison ---
	case OpEq:
		tc.t2EmitGenericEq(instr)
	case OpLt:
		tc.t2EmitGenericLt(instr)
	case OpLe:
		tc.t2EmitGenericLe(instr)

	// --- Control flow ---
	case OpJump:
		tc.t2EmitJump(instr, block)
	case OpBranch:
		tc.t2EmitBranch(instr, block)
	case OpReturn:
		tc.t2EmitReturn(instr)

	// --- Phi (handled at block transitions) ---
	case OpPhi:
		// No code emitted here; phi moves happen in Jump/Branch emitters.

	// --- Type operations ---
	case OpBoxInt, OpBoxFloat, OpUnboxInt, OpUnboxFloat:
		// In memory-to-memory mode, values are always NaN-boxed in slots.
		// Box/Unbox ops just copy.
		if len(instr.Args) > 0 {
			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			tc.t2StoreValue(jit.X0, instr.ID)
		}

	// --- Guards ---
	case OpGuardType:
		tc.t2EmitGuardType(instr)
	case OpGuardNonNil, OpGuardTruthy:
		if len(instr.Args) > 0 {
			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			tc.t2StoreValue(jit.X0, instr.ID)
		}

	// --- Len ---
	case OpLen:
		tc.t2EmitOpExit(instr)

	// --- Exit-resume ops ---
	case OpCall:
		tc.t2EmitCallExit(instr)
	case OpGetGlobal:
		tc.t2EmitGlobalExit(instr)
	case OpSetGlobal:
		tc.t2EmitOpExit(instr)
	case OpNewTable, OpGetTable, OpSetTable:
		tc.t2EmitTableExit(instr)
	case OpGetField, OpSetField:
		tc.t2EmitTableExit(instr)
	case OpSetList, OpAppend:
		tc.t2EmitOpExit(instr)
	case OpSelf:
		tc.t2EmitOpExit(instr)
	case OpClosure, OpClose:
		tc.t2EmitOpExit(instr)
	case OpGetUpval, OpSetUpval:
		tc.t2EmitOpExit(instr)
	case OpConcat, OpPow:
		tc.t2EmitOpExit(instr)
	case OpVararg, OpTestSet:
		tc.t2EmitOpExit(instr)
	case OpForPrep, OpForLoop:
		tc.t2EmitOpExit(instr)
	case OpTForCall, OpTForLoop:
		tc.t2EmitOpExit(instr)
	case OpGo, OpMakeChan, OpSend, OpRecv:
		tc.t2EmitOpExit(instr)

	case OpNop:
		// No-op.

	default:
		// Unknown op: emit as op-exit for safety.
		tc.t2EmitOpExit(instr)
	}
}

// --- Constant emission ---

func (tc *tier2Context) t2EmitConstInt(instr *Instr) {
	tc.asm.LoadImm64(jit.X0, instr.Aux)
	jit.EmitBoxIntFast(tc.asm, jit.X0, jit.X0, mRegTagInt)
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitConstFloat(instr *Instr) {
	// Float constants stored as raw IEEE 754 bits.
	tc.asm.LoadImm64(jit.X0, instr.Aux)
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitConstBool(instr *Instr) {
	if instr.Aux != 0 {
		tc.asm.ADDimm(jit.X0, mRegTagBool, 1)
	} else {
		tc.asm.MOVreg(jit.X0, mRegTagBool)
	}
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitConstNil(instr *Instr) {
	jit.EmitBoxNil(tc.asm, jit.X0)
	tc.t2StoreValue(jit.X0, instr.ID)
}

// --- Slot access ---

func (tc *tier2Context) t2EmitStoreSlot(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	slot := int(instr.Aux)
	tc.asm.STR(jit.X0, mRegRegs, t2SlotOffset(slot))
}

// --- Guards ---

// t2EmitGuardType emits a native type check that deopts if the value doesn't
// match the expected type. Currently supports TypeInt guards: checks the NaN-box
// tag (top 16 bits == 0xFFFE). On success, passes the value through. On failure,
// sets ExitCode=ExitDeopt and jumps to the exit.
func (tc *tier2Context) t2EmitGuardType(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	asm := tc.asm
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)

	guardType := Type(instr.Aux)
	switch guardType {
	case TypeInt:
		// Check NaN-box int tag: top 16 bits must be 0xFFFE.
		asm.LSRimm(jit.X2, jit.X0, 48)
		asm.MOVimm16(jit.X3, jit.NB_TagIntShr48) // 0xFFFE
		asm.CMPreg(jit.X2, jit.X3)
		deoptLabel := fmt.Sprintf("t2_guard_deopt_%d", instr.ID)
		asm.BCond(jit.CondNE, deoptLabel)
		// Success: pass through.
		tc.t2StoreValue(jit.X0, instr.ID)
		doneLabel := fmt.Sprintf("t2_guard_done_%d", instr.ID)
		asm.B(doneLabel)
		// Deopt path.
		asm.Label(deoptLabel)
		asm.LoadImm64(jit.X0, ExitDeopt)
		asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
		asm.B("t2_exit")
		asm.Label(doneLabel)

	default:
		// Unsupported guard type: just pass through (no check).
		tc.t2StoreValue(jit.X0, instr.ID)
	}
}

// --- Type-specialized integer binary ops ---

func (tc *tier2Context) t2EmitIntBinOp(instr *Instr, op string) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Load both operands (NaN-boxed), unbox to raw int.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0) // X0 = raw int lhs

	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1) // X1 = raw int rhs

	// Compute.
	switch op {
	case "add":
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case "sub":
		asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case "mul":
		asm.MUL(jit.X0, jit.X0, jit.X1)
	case "mod":
		asm.SDIV(jit.X2, jit.X0, jit.X1)
		asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	}

	// Rebox and store.
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitNegInt(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.NEG(jit.X0, jit.X0)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	tc.t2StoreValue(jit.X0, instr.ID)
}

// --- Type-specialized float binary ops ---

func (tc *tier2Context) t2EmitFloatBinOp(instr *Instr, op string) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Load NaN-boxed values and move bits to FP registers.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	asm.FMOVtoFP(jit.D0, jit.X0)

	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	asm.FMOVtoFP(jit.D1, jit.X1)

	// For int operands that were promoted to float (e.g., DivFloat with int args),
	// we need to check if the value is an int and convert. But after TypeSpec,
	// DivFloat operands may be ints. Handle by checking the tag.
	// Actually, the NaN-boxed float IS its IEEE 754 bits directly, so FMOV works.
	// For ints, the NaN-boxed representation has tag bits that make it a NaN in
	// float domain. We need to unbox ints and SCVTF them.
	// However, after TypeSpec, if the op is OpDivFloat, the interpreter handles
	// both int and float inputs via Number(). We need to handle both cases.

	// For simplicity in memory-to-memory mode: detect int tag and convert.
	// For pure float inputs (after float TypeSpec), FMOV works directly.
	// For DivFloat which can take int inputs, we do the check.
	if op == "div" || op == "add" || op == "sub" || op == "mul" {
		tc.t2EmitFloatBinOpWithIntCheck(instr, op)
		return
	}

	switch op {
	case "add":
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case "sub":
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case "mul":
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	case "div":
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
	}

	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t2StoreValue(jit.X0, instr.ID)
}

// t2EmitFloatBinOpWithIntCheck emits a float binary op that handles both
// int and float NaN-boxed inputs. For each operand, if it's an int (tag=0xFFFE),
// unbox and SCVTF; otherwise use FMOV.
func (tc *tier2Context) t2EmitFloatBinOpWithIntCheck(instr *Instr, op string) {
	asm := tc.asm

	// Operand 1: X0 already loaded, check if int.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	lhsIntLabel := fmt.Sprintf("t2_fop_lint_%d", instr.ID)
	lhsDoneLabel := fmt.Sprintf("t2_fop_ldone_%d", instr.ID)
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, lhsIntLabel)
	// Float path: FMOV directly.
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.B(lhsDoneLabel)
	asm.Label(lhsIntLabel)
	// Int path: unbox and convert to float.
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.SCVTF(jit.D0, jit.X0)
	asm.Label(lhsDoneLabel)

	// Operand 2.
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	rhsIntLabel := fmt.Sprintf("t2_fop_rint_%d", instr.ID)
	rhsDoneLabel := fmt.Sprintf("t2_fop_rdone_%d", instr.ID)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, rhsIntLabel)
	asm.FMOVtoFP(jit.D1, jit.X1)
	asm.B(rhsDoneLabel)
	asm.Label(rhsIntLabel)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.SCVTF(jit.D1, jit.X1)
	asm.Label(rhsDoneLabel)

	// Compute.
	switch op {
	case "add":
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case "sub":
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case "mul":
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	case "div":
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
	}

	// Store result as float (raw IEEE 754 bits).
	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitNegFloat(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t2StoreValue(jit.X0, instr.ID)
}

// --- Generic arithmetic (type dispatch at runtime) ---

func (tc *tier2Context) t2EmitGenericArith(instr *Instr, op string) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Load both operands.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID) // lhs NaN-boxed
	tc.t2LoadValue(jit.X1, instr.Args[1].ID) // rhs NaN-boxed

	// Check if both are int (tag = 0xFFFE).
	intPathLabel := fmt.Sprintf("t2_arith_int_%d", instr.ID)
	floatPathLabel := fmt.Sprintf("t2_arith_float_%d", instr.ID)
	doneLabel := fmt.Sprintf("t2_arith_done_%d", instr.ID)

	// Check lhs tag.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	// Check rhs tag.
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	// Both int: unbox, compute, rebox.
	asm.Label(intPathLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)

	switch op {
	case "add":
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case "sub":
		asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case "mul":
		asm.MUL(jit.X0, jit.X0, jit.X1)
	case "mod":
		asm.SDIV(jit.X2, jit.X0, jit.X1)
		asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	case "div":
		// Int / Int -> Float (Lua semantics).
		asm.SCVTF(jit.D0, jit.X0)
		asm.SCVTF(jit.D1, jit.X1)
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
		asm.FMOVtoGP(jit.X0, jit.D0)
		tc.t2StoreValue(jit.X0, instr.ID)
		asm.B(doneLabel)
	}

	if op != "div" {
		jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		tc.t2StoreValue(jit.X0, instr.ID)
	}
	asm.B(doneLabel)

	// Float path: convert both to float, compute, store.
	asm.Label(floatPathLabel)
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)

	// Convert lhs to float.
	tc.t2ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t2_ga_lf_%d", instr.ID),
		fmt.Sprintf("t2_ga_ld_%d", instr.ID))
	// Convert rhs to float.
	tc.t2ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t2_ga_rf_%d", instr.ID),
		fmt.Sprintf("t2_ga_rd_%d", instr.ID))

	switch op {
	case "add":
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case "sub":
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case "mul":
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	case "div":
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
	case "mod":
		// float mod: fmod(a, b) = a - floor(a/b) * b
		asm.FDIVd(jit.D2, jit.D0, jit.D1)
		asm.FRINTMd(jit.D2, jit.D2) // floor
		asm.FMSUBd(jit.D0, jit.D2, jit.D1, jit.D0)
	}

	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t2StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

// t2ConvertToFloat converts a NaN-boxed value in srcGPR to float in dstFPR.
// Uses scratch registers. If int, SCVTF; otherwise FMOV.
func (tc *tier2Context) t2ConvertToFloat(srcGPR jit.Reg, dstFPR jit.FReg, scratch1, scratch2 jit.Reg, intLabel, doneLabel string) {
	asm := tc.asm
	asm.LSRimm(scratch1, srcGPR, 48)
	asm.MOVimm16(scratch2, jit.NB_TagIntShr48)
	asm.CMPreg(scratch1, scratch2)
	asm.BCond(jit.CondEQ, intLabel)
	// Float: FMOV bits directly.
	asm.FMOVtoFP(dstFPR, srcGPR)
	asm.B(doneLabel)
	asm.Label(intLabel)
	jit.EmitUnboxInt(asm, srcGPR, srcGPR)
	asm.SCVTF(dstFPR, srcGPR)
	asm.Label(doneLabel)
}

func (tc *tier2Context) t2EmitGenericUnm(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm

	tc.t2LoadValue(jit.X0, instr.Args[0].ID)

	intLabel := fmt.Sprintf("t2_unm_int_%d", instr.ID)
	doneLabel := fmt.Sprintf("t2_unm_done_%d", instr.ID)

	// Check if int.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, intLabel)

	// Float path.
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t2StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Int path.
	asm.Label(intLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.NEG(jit.X0, jit.X0)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	tc.t2StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

func (tc *tier2Context) t2EmitNot(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm

	tc.t2LoadValue(jit.X0, instr.Args[0].ID)

	// Truthy check: nil and false are falsy, everything else is truthy.
	// nil = 0xFFFC000000000000, false = 0xFFFD000000000000
	falseLabel := fmt.Sprintf("t2_not_false_%d", instr.ID)
	doneLabel := fmt.Sprintf("t2_not_done_%d", instr.ID)

	// Check if nil.
	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel) // nil -> !nil = true

	// Check if false.
	asm.MOVreg(jit.X1, mRegTagBool) // false = tag_bool | 0
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel) // false -> !false = true

	// Truthy -> !truthy = false.
	asm.MOVreg(jit.X0, mRegTagBool) // false
	tc.t2StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Falsy -> true.
	asm.Label(falseLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1) // true
	tc.t2StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

// --- Type-specialized comparison ---

func (tc *tier2Context) t2EmitIntCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)

	asm.CMPreg(jit.X0, jit.X1)

	// CSET X0, cond -> X0 = 1 if cond, else 0.
	asm.CSET(jit.X0, cond)

	// Box as bool: tag_bool | X0.
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitFloatCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Load and convert operands to float (they might be int).
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t2_fcmp_li_%d", instr.ID),
		fmt.Sprintf("t2_fcmp_ld_%d", instr.ID))

	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	tc.t2ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t2_fcmp_ri_%d", instr.ID),
		fmt.Sprintf("t2_fcmp_rd_%d", instr.ID))

	asm.FCMPd(jit.D0, jit.D1)
	asm.CSET(jit.X0, cond)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)
}

// --- Generic comparison ---

func (tc *tier2Context) t2EmitGenericEq(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Simple approach: compare raw NaN-boxed uint64 values.
	// This works for int, bool, nil, and pointer equality.
	// For float equality, need special handling (NaN != NaN), but for now
	// bit equality is a good approximation for most cases.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, jit.CondEQ)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)
}

func (tc *tier2Context) t2EmitGenericLt(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Check if both int, use integer comparison.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)

	intPathLabel := fmt.Sprintf("t2_lt_int_%d", instr.ID)
	floatPathLabel := fmt.Sprintf("t2_lt_float_%d", instr.ID)
	doneLabel := fmt.Sprintf("t2_lt_done_%d", instr.ID)

	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	// Int path.
	asm.Label(intPathLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, jit.CondLT)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Float path.
	asm.Label(floatPathLabel)
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	tc.t2ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t2_lt_fli_%d", instr.ID),
		fmt.Sprintf("t2_lt_fld_%d", instr.ID))
	tc.t2ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t2_lt_fri_%d", instr.ID),
		fmt.Sprintf("t2_lt_frd_%d", instr.ID))
	asm.FCMPd(jit.D0, jit.D1)
	asm.CSET(jit.X0, jit.CondLT)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

func (tc *tier2Context) t2EmitGenericLe(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)

	intPathLabel := fmt.Sprintf("t2_le_int_%d", instr.ID)
	floatPathLabel := fmt.Sprintf("t2_le_float_%d", instr.ID)
	doneLabel := fmt.Sprintf("t2_le_done_%d", instr.ID)

	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	asm.Label(intPathLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, jit.CondLE)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(floatPathLabel)
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)
	tc.t2LoadValue(jit.X1, instr.Args[1].ID)
	tc.t2ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t2_le_fli_%d", instr.ID),
		fmt.Sprintf("t2_le_fld_%d", instr.ID))
	tc.t2ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t2_le_fri_%d", instr.ID),
		fmt.Sprintf("t2_le_frd_%d", instr.ID))
	asm.FCMPd(jit.D0, jit.D1)
	asm.CSET(jit.X0, jit.CondLE)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t2StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

// --- Control flow ---

func (tc *tier2Context) t2EmitJump(instr *Instr, block *Block) {
	if len(block.Succs) == 0 {
		return
	}
	target := block.Succs[0]

	// Emit phi moves for the target block.
	tc.t2EmitPhiMoves(block, target)

	tc.asm.B(fmt.Sprintf("t2_B%d", target.ID))
}

func (tc *tier2Context) t2EmitBranch(instr *Instr, block *Block) {
	if len(instr.Args) == 0 || len(block.Succs) < 2 {
		return
	}
	asm := tc.asm

	trueBlock := block.Succs[0]
	falseBlock := block.Succs[1]

	// Load the condition value.
	tc.t2LoadValue(jit.X0, instr.Args[0].ID)

	// Truthy check: nil and false are falsy.
	// nil = 0xFFFC000000000000, false = 0xFFFD000000000000.
	falseLabel := fmt.Sprintf("t2_br_false_%d", instr.ID)

	// Check if nil.
	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel)

	// Check if false (bool false = tag_bool | 0).
	asm.MOVreg(jit.X1, mRegTagBool)
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel)

	// Truthy -> go to true block.
	tc.t2EmitPhiMoves(block, trueBlock)
	asm.B(fmt.Sprintf("t2_B%d", trueBlock.ID))

	// Falsy -> go to false block.
	asm.Label(falseLabel)
	tc.t2EmitPhiMoves(block, falseBlock)
	asm.B(fmt.Sprintf("t2_B%d", falseBlock.ID))
}

func (tc *tier2Context) t2EmitReturn(instr *Instr) {
	if len(instr.Args) > 0 {
		// Store return value to slot 0.
		tc.t2LoadValue(jit.X0, instr.Args[0].ID)
		tc.asm.STR(jit.X0, mRegRegs, 0) // slot 0
	}
	tc.asm.B("t2_epilogue")
}

// --- Phi moves ---

// t2EmitPhiMoves emits copies for phi nodes in the target block.
// For each phi in the target, copy the source value (from the current block)
// to the phi's slot. Uses X4/X5 as scratch to avoid clobbering X0/X1.
func (tc *tier2Context) t2EmitPhiMoves(fromBlock *Block, toBlock *Block) {
	// Find which predecessor index this block is.
	predIdx := -1
	for i, pred := range toBlock.Preds {
		if pred == fromBlock {
			predIdx = i
			break
		}
	}
	if predIdx < 0 {
		return
	}

	for _, instr := range toBlock.Instrs {
		if instr.Op != OpPhi {
			break // Phis are always at the beginning.
		}
		if predIdx >= len(instr.Args) {
			continue
		}
		srcArg := instr.Args[predIdx]
		if srcArg == nil {
			continue
		}

		// Load source value and store to phi's slot.
		// Use X4 as scratch to avoid conflicts with multiple phi copies.
		tc.t2LoadValue(jit.X4, srcArg.ID)
		tc.t2StoreValue(jit.X4, instr.ID)
	}
}
