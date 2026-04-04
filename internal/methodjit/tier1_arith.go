//go:build darwin && arm64

// tier1_arith.go emits ARM64 templates for baseline arithmetic, comparison,
// constant loading, MOVE, RETURN, JMP, FORPREP, FORLOOP, TEST, and TESTSET.
//
// All values are NaN-boxed in the VM register file. The baseline compiler
// handles int-int fast paths natively and falls back to float operations
// when types don't match.
//
// Integer operations:
//   1. Load NaN-boxed values from register file
//   2. Check both are int (tag == 0xFFFE via LSR #48)
//   3. Extract 48-bit payloads (SBFX #0, #48)
//   4. Perform integer operation
//   5. Re-box result (UBFX + ORR with tag register)
//   6. Store to register file
//
// Float fallback:
//   If either operand is not an int, treat both as float64.
//   Use FMOV to/from FP registers, perform float op, store result.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

// slotOff returns the byte offset for a VM register slot.
func slotOff(slot int) int {
	return slot * jit.ValueSize
}

// loadRK loads an RK operand (register or constant) into the given scratch register.
// If idx >= RKBit, loads from constants; otherwise from regs.
func loadRK(asm *jit.Assembler, dst jit.Reg, idx int) {
	if idx >= vm.RKBit {
		// Constant: load from constants[idx - RKBit]
		constIdx := idx - vm.RKBit
		asm.LDR(dst, mRegConsts, slotOff(constIdx))
	} else {
		// Register: load from regs[idx]
		asm.LDR(dst, mRegRegs, slotOff(idx))
	}
}

// checkIntTag checks if a NaN-boxed value in reg has the int tag (0xFFFE).
// Sets condition flags: EQ if int, NE if not int.
// Uses scratch register for comparison.
func checkIntTag(asm *jit.Assembler, valReg, scratch jit.Reg) {
	asm.LSRimm(scratch, valReg, 48)
	asm.MOVimm16(jit.X7, uint16(jit.NB_TagIntShr48)) // 0xFFFE
	asm.CMPreg(scratch, jit.X7)
}

// ---------------------------------------------------------------------------
// Constants & Loads
// ---------------------------------------------------------------------------

// emitBaselineLoadNil: R(A)..R(A+B) = nil
func emitBaselineLoadNil(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	// Load NaN-boxed nil value.
	asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
	for i := a; i <= a+b; i++ {
		asm.STR(jit.X0, mRegRegs, slotOff(i))
	}
}

// emitBaselineLoadBool: R(A) = bool(B); if C then PC++
func emitBaselineLoadBool(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	if b != 0 {
		// true: tag_bool | 1
		asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	} else {
		// false: tag_bool | 0
		asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool))
	}
	asm.STR(jit.X0, mRegRegs, slotOff(a))

	if c != 0 {
		// Skip next instruction.
		target := pc + 2 // skip pc+1
		if target <= len(code) {
			asm.B(pcLabel(target))
		}
	}
}

// emitBaselineLoadInt: R(A) = sBx (small signed integer)
func emitBaselineLoadInt(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)
	// Box the integer: tag_int | (sbx & 0x0000FFFFFFFFFFFF)
	boxed := jit.NB_TagInt | (uint64(int64(sbx)) & jit.NB_PayloadMask)
	asm.LoadImm64(jit.X0, int64(boxed))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
}

// emitBaselineLoadK: R(A) = Constants[Bx]
func emitBaselineLoadK(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)
	asm.LDR(jit.X0, mRegConsts, slotOff(bx))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
}

// ---------------------------------------------------------------------------
// MOVE
// ---------------------------------------------------------------------------

// emitBaselineMove: R(A) = R(B)
func emitBaselineMove(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	if a == b {
		return // no-op
	}
	asm.LDR(jit.X0, mRegRegs, slotOff(b))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
}

// ---------------------------------------------------------------------------
// Arithmetic: ADD, SUB, MUL
// ---------------------------------------------------------------------------

// emitBaselineArith emits code for ADD/SUB/MUL with int fast-path and float fallback.
func emitBaselineArith(asm *jit.Assembler, inst uint32, op string) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	// Load RK(B) -> X0, RK(C) -> X1
	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	doneLabel := nextLabel("arith_done")
	floatLabel := nextLabel("arith_float")

	// Check X0 is int
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	// Check X1 is int
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3) // X3 still has 0xFFFE
	asm.BCond(jit.CondNE, floatLabel)

	// Both are int. Extract 48-bit signed payload.
	asm.SBFX(jit.X4, jit.X0, 0, 48) // X4 = sign-extended int from X0
	asm.SBFX(jit.X5, jit.X1, 0, 48) // X5 = sign-extended int from X1

	// Perform operation.
	switch op {
	case "add":
		asm.ADDreg(jit.X4, jit.X4, jit.X5)
	case "sub":
		asm.SUBreg(jit.X4, jit.X4, jit.X5)
	case "mul":
		asm.MUL(jit.X4, jit.X4, jit.X5)
	}

	// Check for int48 overflow: SBFX sign-extends lower 48 bits; if it
	// differs from the full 64-bit result, the value doesn't fit.
	overflowLabel := nextLabel("arith_overflow")
	asm.SBFX(jit.X6, jit.X4, 0, 48)
	asm.CMPreg(jit.X6, jit.X4)
	asm.BCond(jit.CondNE, overflowLabel)

	// Re-box: UBFX to clear top 16 bits, ORR with tag register.
	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)

	// Store result.
	asm.STR(jit.X4, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Int overflow: convert 64-bit int result to float64 and store as NaN-boxed float.
	asm.Label(overflowLabel)
	asm.SCVTF(jit.D0, jit.X4)
	asm.FMOVtoGP(jit.X4, jit.D0)
	asm.STR(jit.X4, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Float fallback.
	asm.Label(floatLabel)
	emitFloatArith(asm, jit.X0, jit.X1, op)
	asm.STR(jit.X0, mRegRegs, slotOff(a))

	asm.Label(doneLabel)
}

// emitFloatArith converts two NaN-boxed values to float64, performs the operation,
// and leaves the NaN-boxed float result in X0.
func emitFloatArith(asm *jit.Assembler, valA, valB jit.Reg, op string) {
	// Convert A to float64 in D0
	emitToFloat(asm, jit.D0, valA, jit.X4, jit.X5)
	// Convert B to float64 in D1
	emitToFloat(asm, jit.D1, valB, jit.X4, jit.X5)

	// Perform float operation
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

	// Move float result to GP register (NaN-boxed float = raw IEEE 754 bits)
	asm.FMOVtoGP(valA, jit.D0)
}

// baselineLabelID is a global counter for generating unique labels.
var baselineLabelID int

func nextLabel(prefix string) string {
	id := baselineLabelID
	baselineLabelID++
	return fmt.Sprintf("%s_%d", prefix, id)
}

// emitToFloat converts a NaN-boxed value in gpReg to float64 in fpReg.
// If the value is an int, converts via SCVTF. If float, moves bits directly.
// Uses scratch1, scratch2 as temporaries.
func emitToFloat(asm *jit.Assembler, fpReg jit.FReg, gpReg jit.Reg, scratch1, scratch2 jit.Reg) {
	isIntLabel := nextLabel("tofloat_int")
	doneLabel := nextLabel("tofloat_done")

	// Check if int
	asm.LSRimm(scratch1, gpReg, 48)
	asm.MOVimm16(scratch2, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(scratch1, scratch2)
	asm.BCond(jit.CondEQ, isIntLabel)

	// Not int: assume float. Move bits directly.
	asm.FMOVtoFP(fpReg, gpReg)
	asm.B(doneLabel)

	// Int: extract and convert.
	asm.Label(isIntLabel)
	asm.SBFX(scratch1, gpReg, 0, 48)
	asm.SCVTF(fpReg, scratch1)

	asm.Label(doneLabel)
}

// emitBaselineDiv: R(A) = RK(B) / RK(C) — always returns float.
func emitBaselineDiv(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	// DIV always returns float in GScript (5/2 = 2.5).
	emitFloatArith(asm, jit.X0, jit.X1, "div")
	asm.STR(jit.X0, mRegRegs, slotOff(a))
}

// emitBaselineMod: R(A) = RK(B) % RK(C)
func emitBaselineMod(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	doneLabel := nextLabel("mod_done")
	floatLabel := nextLabel("mod_float")

	// Check both are int
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	// Both int: a % b = a - (a/b)*b using SDIV + MSUB
	asm.SBFX(jit.X4, jit.X0, 0, 48)
	asm.SBFX(jit.X5, jit.X1, 0, 48)
	asm.SDIV(jit.X6, jit.X4, jit.X5)
	asm.MSUB(jit.X4, jit.X6, jit.X5, jit.X4) // X4 = X4 - X6*X5

	// Re-box as int.
	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
	asm.STR(jit.X4, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Float fallback: convert to float, use FDIV + FRINTZS + FMSUB
	// Actually for mod, just do: a - floor(a/b)*b
	// Simpler: exit to Go for float mod. For now, use integer-only fast path.
	asm.Label(floatLabel)
	// For float mod, we do: a - floor(a/b)*b
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.FDIVd(jit.D2, jit.D0, jit.D1)    // D2 = a/b
	asm.FRINTMd(jit.D2, jit.D2)           // D2 = floor(a/b)
	asm.FMULd(jit.D2, jit.D2, jit.D1)     // D2 = floor(a/b)*b
	asm.FSUBd(jit.D0, jit.D0, jit.D2)     // D0 = a - floor(a/b)*b
	asm.FMOVtoGP(jit.X0, jit.D0)
	asm.STR(jit.X0, mRegRegs, slotOff(a))

	asm.Label(doneLabel)
}

// emitBaselineUnm: R(A) = -R(B)
func emitBaselineUnm(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	asm.LDR(jit.X0, mRegRegs, slotOff(b))

	doneLabel := nextLabel("unm_done")
	floatLabel := nextLabel("unm_float")

	// Check if int
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	// Int: negate the 48-bit payload.
	asm.SBFX(jit.X4, jit.X0, 0, 48)
	asm.NEG(jit.X4, jit.X4)

	// Check for int48 overflow (negating minInt48 = -2^47 produces 2^47 which doesn't fit).
	unmOverflowLabel := nextLabel("unm_overflow")
	asm.SBFX(jit.X5, jit.X4, 0, 48)
	asm.CMPreg(jit.X5, jit.X4)
	asm.BCond(jit.CondNE, unmOverflowLabel)

	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
	asm.STR(jit.X4, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Overflow: convert to float.
	asm.Label(unmOverflowLabel)
	asm.SCVTF(jit.D0, jit.X4)
	asm.FMOVtoGP(jit.X4, jit.D0)
	asm.STR(jit.X4, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Float: negate the float.
	asm.Label(floatLabel)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	asm.STR(jit.X0, mRegRegs, slotOff(a))

	asm.Label(doneLabel)
}

// emitBaselineNot: R(A) = !R(B) — logical not (truthy check)
func emitBaselineNot(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	asm.LDR(jit.X0, mRegRegs, slotOff(b))

	// Truthy: value != nil AND value != false
	// nil = 0xFFFC000000000000, false = 0xFFFD000000000000
	// NOT truthy → true, truthy → false
	asm.LoadImm64(jit.X2, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X2)
	notTruthyLabel := nextLabel("not_true")
	doneLabel := nextLabel("not_done")

	asm.BCond(jit.CondEQ, notTruthyLabel) // nil → not truthy → result = true

	asm.LoadImm64(jit.X2, nb64(jit.NB_ValFalse))
	asm.CMPreg(jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, notTruthyLabel) // false → not truthy → result = true

	// Truthy: result = false
	asm.LoadImm64(jit.X0, nb64(jit.NB_ValFalse))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Not truthy: result = true
	asm.Label(notTruthyLabel)
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	asm.STR(jit.X0, mRegRegs, slotOff(a))

	asm.Label(doneLabel)
}

// ---------------------------------------------------------------------------
// Comparison: EQ, LT, LE
// ---------------------------------------------------------------------------

// emitBaselineEQ: if (RK(B) == RK(C)) != bool(A) then PC++
func emitBaselineEQ(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	skipLabel := pcLabel(pc + 2) // skip next instruction
	doneLabel := nextLabel("eq_done")
	notEqualLabel := nextLabel("eq_ne")
	floatCmpLabel := nextLabel("eq_fcmp")
	bothNumLabel := nextLabel("eq_both_num")

	// Fast path: raw bit equality (works for int==int, nil==nil, bool==bool, ptr==ptr)
	asm.CMPreg(jit.X0, jit.X1)
	if a != 0 {
		asm.BCond(jit.CondEQ, doneLabel) // equal → don't skip
	} else {
		asm.BCond(jit.CondEQ, skipLabel) // equal → skip
	}

	// Slow path: raw bits differ. Check if both are numbers (int/float mismatch).
	// A value is a number if: tag < 0xFFFC (float) OR tag == 0xFFFE (int).
	// Check X0
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagNilShr48)) // 0xFFFC
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondLT, floatCmpLabel) // tag < 0xFFFC → float → is number
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))  // 0xFFFE
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, notEqualLabel) // tag != 0xFFFE → not int, not float → not number

	// X0 is a number. Check X1.
	asm.Label(floatCmpLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagNilShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondLT, bothNumLabel) // float → is number
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, notEqualLabel) // not int → not number

	// Both are numbers: convert to float and compare
	asm.Label(bothNumLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.FCMPd(jit.D0, jit.D1)
	if a != 0 {
		asm.BCond(jit.CondNE, skipLabel) // not equal → skip
	} else {
		asm.BCond(jit.CondEQ, skipLabel) // equal → skip
	}
	asm.B(doneLabel)

	// Not equal (different types, not both numbers)
	asm.Label(notEqualLabel)
	if a != 0 {
		asm.B(skipLabel) // not equal → skip
	}
	// else: fall through to doneLabel (not equal, don't skip)

	asm.Label(doneLabel)
}

// emitBaselineLT: if (RK(B) < RK(C)) != bool(A) then PC++
func emitBaselineLT(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	doneLabel := nextLabel("lt_done")
	floatLabel := nextLabel("lt_float")
	slowLabel := nextLabel("lt_slow")
	skipLabel := pcLabel(pc + 2) // skip next instruction

	// String/pointer fast exit: if either operand has the pointer tag
	// (0xFFFF), the FCMP float fallback would treat the raw pointer bits as
	// a float and produce wrong results (FCMP of a NaN-boxed ptr is
	// "unordered", never LT). Exit to Go so Value.LessThan handles it.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagPtrShr48)) // 0xFFFF
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, slowLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3) // X3 still 0xFFFF
	asm.BCond(jit.CondEQ, slowLabel)

	// Check both are int
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	// Both int: compare signed 48-bit values.
	asm.SBFX(jit.X4, jit.X0, 0, 48)
	asm.SBFX(jit.X5, jit.X1, 0, 48)
	asm.CMPreg(jit.X4, jit.X5)

	if a != 0 {
		// if (B < C) != true → if B >= C then skip
		asm.BCond(jit.CondGE, skipLabel)
	} else {
		// if (B < C) != false → if B < C then skip
		asm.BCond(jit.CondLT, skipLabel)
	}
	asm.B(doneLabel)

	// Float fallback
	asm.Label(floatLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.FCMPd(jit.D0, jit.D1)

	if a != 0 {
		asm.BCond(jit.CondGE, skipLabel) // not LT → skip
	} else {
		// Note: FCMP sets MI for LT
		asm.BCond(jit.CondMI, skipLabel) // LT → skip
	}
	asm.B(doneLabel)

	// Slow path: exit to Go; the handler computes LT via Value.LessThan
	// and overrides BaselinePC to pc+1 (no skip) or pc+2 (skip) as needed.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_LT, pc, a, bidx, cidx)

	asm.Label(doneLabel)
}

// emitBaselineLE: if (RK(B) <= RK(C)) != bool(A) then PC++
func emitBaselineLE(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	doneLabel := nextLabel("le_done")
	floatLabel := nextLabel("le_float")
	slowLabel := nextLabel("le_slow")
	skipLabel := pcLabel(pc + 2) // skip next instruction

	// String/pointer fast exit: see emitBaselineLT for rationale.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagPtrShr48)) // 0xFFFF
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, slowLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3) // X3 still 0xFFFF
	asm.BCond(jit.CondEQ, slowLabel)

	// Check both are int
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatLabel)

	// Both int: compare.
	asm.SBFX(jit.X4, jit.X0, 0, 48)
	asm.SBFX(jit.X5, jit.X1, 0, 48)
	asm.CMPreg(jit.X4, jit.X5)

	if a != 0 {
		// if (B <= C) != true → if B > C then skip
		asm.BCond(jit.CondGT, skipLabel)
	} else {
		// if (B <= C) != false → if B <= C then skip
		asm.BCond(jit.CondLE, skipLabel)
	}
	asm.B(doneLabel)

	// Float fallback
	asm.Label(floatLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.FCMPd(jit.D0, jit.D1)

	if a != 0 {
		asm.BCond(jit.CondGT, skipLabel) // not LE → skip
	} else {
		asm.BCond(jit.CondLS, skipLabel) // LE → skip
	}
	asm.B(doneLabel)

	// Slow path: exit to Go; the handler computes LE via Value.LessThan
	// and overrides BaselinePC to pc+1 or pc+2 as needed.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_LE, pc, a, bidx, cidx)

	asm.Label(doneLabel)
}

// ---------------------------------------------------------------------------
// Logical test
// ---------------------------------------------------------------------------

// emitBaselineTest: if bool(R(A)) != bool(C) then PC++
func emitBaselineTest(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	c := vm.DecodeC(inst)

	asm.LDR(jit.X0, mRegRegs, slotOff(a))
	skipLabel := pcLabel(pc + 2)

	// Truthy = not nil AND not false
	// Check nil
	asm.LoadImm64(jit.X2, nb64(jit.NB_ValNil))
	isFalsyLabel := nextLabel("test_falsy")
	isTruthyLabel := nextLabel("test_truthy")
	doneLabel := nextLabel("test_done")

	asm.CMPreg(jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, isFalsyLabel)

	asm.LoadImm64(jit.X2, nb64(jit.NB_ValFalse))
	asm.CMPreg(jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, isFalsyLabel)

	// Truthy
	asm.B(isTruthyLabel)

	asm.Label(isFalsyLabel)
	// Falsy: truthy=false
	if c != 0 {
		// bool(C)=true, truthy=false, false != true → skip
		asm.B(skipLabel)
	}
	asm.B(doneLabel)

	asm.Label(isTruthyLabel)
	// Truthy: truthy=true
	if c == 0 {
		// bool(C)=false, truthy=true, true != false → skip
		asm.B(skipLabel)
	}

	asm.Label(doneLabel)
}

// emitBaselineTestSet: if bool(R(B)) != bool(C) then PC++ else R(A) = R(B)
func emitBaselineTestSet(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	asm.LDR(jit.X0, mRegRegs, slotOff(b))
	skipLabel := pcLabel(pc + 2)
	isFalsyLabel := nextLabel("testset_falsy")
	isTruthyLabel := nextLabel("testset_truthy")
	doneLabel := nextLabel("testset_done")

	// Truthy check
	asm.LoadImm64(jit.X2, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, isFalsyLabel)

	asm.LoadImm64(jit.X2, nb64(jit.NB_ValFalse))
	asm.CMPreg(jit.X0, jit.X2)
	asm.BCond(jit.CondEQ, isFalsyLabel)

	asm.B(isTruthyLabel)

	asm.Label(isFalsyLabel)
	if c != 0 {
		// bool(C)=true, truthy=false → skip (no assign)
		asm.B(skipLabel)
	} else {
		// bool(C)=false, truthy=false → assign R(A)=R(B)
		asm.STR(jit.X0, mRegRegs, slotOff(a))
	}
	asm.B(doneLabel)

	asm.Label(isTruthyLabel)
	if c == 0 {
		// bool(C)=false, truthy=true → skip (no assign)
		asm.B(skipLabel)
	} else {
		// bool(C)=true, truthy=true → assign R(A)=R(B)
		asm.STR(jit.X0, mRegRegs, slotOff(a))
	}

	asm.Label(doneLabel)
}

// Jump, ForLoop, Return, TForLoop are in tier1_control.go
