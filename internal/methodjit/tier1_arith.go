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

// mRegR0 pins VM register R(0) to a callee-saved ARM64 register.
// Reads of slot 0 use this register instead of loading from memory.
// Writes to slot 0 update both memory and this register.
const mRegR0 = jit.X22

// loadSlot loads a VM register, using the pinned register for slot 0.
func loadSlot(asm *jit.Assembler, dst jit.Reg, slot int) {
	if slot == 0 {
		asm.MOVreg(dst, mRegR0)
	} else {
		asm.LDR(dst, mRegRegs, slotOff(slot))
	}
}

// storeSlot stores to a VM register, also syncing the pinned register for slot 0.
func storeSlot(asm *jit.Assembler, slot int, src jit.Reg) {
	asm.STR(src, mRegRegs, slotOff(slot))
	if slot == 0 {
		asm.MOVreg(mRegR0, src)
	}
}

// loadRK loads an RK operand (register or constant) into the given scratch register.
// If idx >= RKBit, loads from constants; otherwise from regs.
func loadRK(asm *jit.Assembler, dst jit.Reg, idx int) {
	if idx >= vm.RKBit {
		// Constant: load from constants[idx - RKBit]
		constIdx := idx - vm.RKBit
		asm.LDR(dst, mRegConsts, slotOff(constIdx))
	} else {
		// Register: load from regs[idx], using pinned register for slot 0.
		loadSlot(asm, dst, idx)
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
		storeSlot(asm, i, jit.X0)
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
	storeSlot(asm, a, jit.X0)

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
	storeSlot(asm, a, jit.X0)
}

// emitBaselineLoadK: R(A) = Constants[Bx]
func emitBaselineLoadK(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)
	asm.LDR(jit.X0, mRegConsts, slotOff(bx))
	storeSlot(asm, a, jit.X0)
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
	loadSlot(asm, jit.X0, b)
	storeSlot(asm, a, jit.X0)
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
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	// Int overflow: convert 64-bit int result to float64 and store as NaN-boxed float.
	asm.Label(overflowLabel)
	asm.SCVTF(jit.D0, jit.X4)
	asm.FMOVtoGP(jit.X4, jit.D0)
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	// Float fallback.
	asm.Label(floatLabel)
	emitFloatArith(asm, jit.X0, jit.X1, op)
	storeSlot(asm, a, jit.X0)

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
	storeSlot(asm, a, jit.X0)
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
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	// Float fallback: convert to float, use FDIV + FRINTZS + FMSUB
	// Actually for mod, just do: a - floor(a/b)*b
	// Simpler: exit to Go for float mod. For now, use integer-only fast path.
	asm.Label(floatLabel)
	// For float mod, we do: a - floor(a/b)*b
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.FDIVd(jit.D2, jit.D0, jit.D1) // D2 = a/b
	asm.FRINTMd(jit.D2, jit.D2)       // D2 = floor(a/b)
	asm.FMULd(jit.D2, jit.D2, jit.D1) // D2 = floor(a/b)*b
	asm.FSUBd(jit.D0, jit.D0, jit.D2) // D0 = a - floor(a/b)*b
	asm.FMOVtoGP(jit.X0, jit.D0)
	storeSlot(asm, a, jit.X0)

	asm.Label(doneLabel)
}

// emitBaselineModIntSpec emits MOD assuming both operands are statically known
// ints. It still preserves VM semantics: n%0 deopts to the interpreter, and
// the remainder is adjusted so its sign follows the divisor.
func emitBaselineModIntSpec(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	zeroLabel := nextLabel("mod_spec_zero")
	adjustLabel := nextLabel("mod_spec_adjust")
	storeLabel := nextLabel("mod_spec_store")
	doneLabel := nextLabel("mod_spec_done")

	asm.SBFX(jit.X4, jit.X0, 0, 48) // dividend
	asm.SBFX(jit.X5, jit.X1, 0, 48) // divisor
	asm.CBZ(jit.X5, zeroLabel)

	asm.SDIV(jit.X6, jit.X4, jit.X5)
	asm.MSUB(jit.X4, jit.X6, jit.X5, jit.X4) // X4 = dividend - quotient*divisor

	// VM modulo follows Lua-style sign rules: if remainder and divisor have
	// different signs, add the divisor. Zero needs no adjustment.
	asm.CBZ(jit.X4, storeLabel)
	asm.EORreg(jit.X7, jit.X4, jit.X5)
	asm.TBNZ(jit.X7, 63, adjustLabel)
	asm.B(storeLabel)

	asm.Label(adjustLabel)
	asm.ADDreg(jit.X4, jit.X4, jit.X5)

	asm.Label(storeLabel)
	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	asm.Label(zeroLabel)
	emitIntSpecDeopt(asm, pc)

	asm.Label(doneLabel)
}

// emitBaselineUnm: R(A) = -R(B)
func emitBaselineUnm(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	loadSlot(asm, jit.X0, b)

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
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	// Overflow: convert to float.
	asm.Label(unmOverflowLabel)
	asm.SCVTF(jit.D0, jit.X4)
	asm.FMOVtoGP(jit.X4, jit.D0)
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	// Float: negate the float.
	asm.Label(floatLabel)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	storeSlot(asm, a, jit.X0)

	asm.Label(doneLabel)
}

// emitBaselineNot: R(A) = !R(B) — logical not (truthy check)
func emitBaselineNot(asm *jit.Assembler, inst uint32) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	loadSlot(asm, jit.X0, b)

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
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	// Not truthy: result = true
	asm.Label(notTruthyLabel)
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	storeSlot(asm, a, jit.X0)

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
	stringLabel := nextLabel("eq_string")

	// Fast path: raw bit equality (works for int==int, nil==nil, bool==bool, ptr==ptr)
	asm.CMPreg(jit.X0, jit.X1)
	if a != 0 {
		asm.BCond(jit.CondEQ, doneLabel) // equal → don't skip
	} else {
		asm.BCond(jit.CondEQ, skipLabel) // equal → skip
	}

	// Raw bits differ. Strings compare by content, not pointer identity.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagPtrShr48)) // 0xFFFF
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, stringLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, notEqualLabel)

	// Slow path: raw bits differ. Check if both are numbers (int/float mismatch).
	// A value is a number if: tag < 0xFFFC (float) OR tag == 0xFFFE (int).
	// Check X0
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagNilShr48)) // 0xFFFC
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondLT, floatCmpLabel)             // tag < 0xFFFC → float → is number
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48)) // 0xFFFE
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

	asm.Label(stringLabel)
	jit.EmitCheckIsString(asm, jit.X0, jit.X2, jit.X3, notEqualLabel)
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, notEqualLabel)
	stringTrueLabel := nextLabel("eq_string_true")
	stringFalseLabel := nextLabel("eq_string_false")
	emitBaselineStringEq(asm, stringTrueLabel, stringFalseLabel)
	emitBaselineCmpBoolBranch(asm, a, stringTrueLabel, stringFalseLabel, skipLabel, doneLabel)

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
	stringLabel := nextLabel("lt_string")
	slowLabel := nextLabel("lt_slow")
	skipLabel := pcLabel(pc + 2) // skip next instruction

	// Pointer values cannot fall through to FCMP: NaN-boxed pointer bits are
	// unordered as floats. Strings are common enough to compare natively; other
	// pointer types still exit to Go for the semantic error path.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagPtrShr48)) // 0xFFFF
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, stringLabel)
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
	ltGenericFloatLabel := nextLabel("lt_generic_float")
	ltCompareFloatLabel := nextLabel("lt_compare_float")
	jit.EmitIsTagged(asm, jit.X0, jit.X4)
	asm.BCond(jit.CondEQ, ltGenericFloatLabel)
	jit.EmitIsTagged(asm, jit.X1, jit.X4)
	asm.BCond(jit.CondEQ, ltGenericFloatLabel)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FMOVtoFP(jit.D1, jit.X1)
	asm.B(ltCompareFloatLabel)
	asm.Label(ltGenericFloatLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.Label(ltCompareFloatLabel)
	asm.FCMPd(jit.D0, jit.D1)

	if a != 0 {
		asm.BCond(jit.CondGE, skipLabel) // not LT → skip
	} else {
		// Note: FCMP sets MI for LT
		asm.BCond(jit.CondMI, skipLabel) // LT → skip
	}
	asm.B(doneLabel)

	// String fast path: both operands must be string values. Mixed pointer
	// types exit so Value.LessThan can preserve the VM's error behavior.
	asm.Label(stringLabel)
	jit.EmitCheckIsString(asm, jit.X0, jit.X2, jit.X3, slowLabel)
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	stringTrueLabel := nextLabel("lt_string_true")
	stringFalseLabel := nextLabel("lt_string_false")
	emitBaselineStringCmp(asm, jit.CondLT, stringTrueLabel, stringFalseLabel)
	emitBaselineCmpBoolBranch(asm, a, stringTrueLabel, stringFalseLabel, skipLabel, doneLabel)

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
	stringLabel := nextLabel("le_string")
	slowLabel := nextLabel("le_slow")
	skipLabel := pcLabel(pc + 2) // skip next instruction

	// Pointer values cannot fall through to FCMP; strings get a native path.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagPtrShr48)) // 0xFFFF
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, stringLabel)
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
	leGenericFloatLabel := nextLabel("le_generic_float")
	leCompareFloatLabel := nextLabel("le_compare_float")
	jit.EmitIsTagged(asm, jit.X0, jit.X4)
	asm.BCond(jit.CondEQ, leGenericFloatLabel)
	jit.EmitIsTagged(asm, jit.X1, jit.X4)
	asm.BCond(jit.CondEQ, leGenericFloatLabel)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FMOVtoFP(jit.D1, jit.X1)
	asm.B(leCompareFloatLabel)
	asm.Label(leGenericFloatLabel)
	emitToFloat(asm, jit.D0, jit.X0, jit.X4, jit.X5)
	emitToFloat(asm, jit.D1, jit.X1, jit.X4, jit.X5)
	asm.Label(leCompareFloatLabel)
	asm.FCMPd(jit.D0, jit.D1)

	if a != 0 {
		asm.BCond(jit.CondGT, skipLabel) // not LE → skip
	} else {
		asm.BCond(jit.CondLS, skipLabel) // LE → skip
	}
	asm.B(doneLabel)

	// String fast path: both operands must be string values. Mixed pointer
	// types exit so Value.LessThan can preserve the VM's error behavior.
	asm.Label(stringLabel)
	jit.EmitCheckIsString(asm, jit.X0, jit.X2, jit.X3, slowLabel)
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	stringTrueLabel := nextLabel("le_string_true")
	stringFalseLabel := nextLabel("le_string_false")
	emitBaselineStringCmp(asm, jit.CondLE, stringTrueLabel, stringFalseLabel)
	emitBaselineCmpBoolBranch(asm, a, stringTrueLabel, stringFalseLabel, skipLabel, doneLabel)

	// Slow path: exit to Go; the handler computes LE via Value.LessThan
	// and overrides BaselinePC to pc+1 or pc+2 as needed.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_LE, pc, a, bidx, cidx)

	asm.Label(doneLabel)
}

func emitBaselineCmpBoolBranch(asm *jit.Assembler, a int, trueLabel, falseLabel, skipLabel, doneLabel string) {
	asm.Label(trueLabel)
	if a == 0 {
		asm.B(skipLabel)
	} else {
		asm.B(doneLabel)
	}

	asm.Label(falseLabel)
	if a != 0 {
		asm.B(skipLabel)
	} else {
		asm.B(doneLabel)
	}
}

// emitBaselineStringCmp compares two NaN-boxed string values in X0 and X1.
// Both operands must already be checked as strings. It branches to trueLabel
// when X0 < X1 (or <= for CondLE), otherwise falseLabel.
func emitBaselineStringCmp(asm *jit.Assembler, cond jit.Cond, trueLabel, falseLabel string) {
	loopLabel := nextLabel("str_cmp_loop")
	prefixLabel := nextLabel("str_cmp_prefix")

	// Strip NaN-boxing tag/subtype bits and recover *string pointers.
	asm.LSLimm(jit.X2, jit.X0, 20)
	asm.LSRimm(jit.X2, jit.X2, 20)
	asm.LSLimm(jit.X3, jit.X1, 20)
	asm.LSRimm(jit.X3, jit.X3, 20)

	// Go string header: data pointer at +0, length at +8.
	asm.LDR(jit.X4, jit.X2, 0)
	asm.LDR(jit.X5, jit.X2, 8)
	asm.LDR(jit.X6, jit.X3, 0)
	asm.LDR(jit.X7, jit.X3, 8)
	asm.MOVimm16(jit.X8, 0)

	asm.Label(loopLabel)
	asm.CMPreg(jit.X8, jit.X5)
	asm.BCond(jit.CondHS, prefixLabel)
	asm.CMPreg(jit.X8, jit.X7)
	asm.BCond(jit.CondHS, prefixLabel)

	asm.LDRBreg(jit.X9, jit.X4, jit.X8)
	asm.LDRBreg(jit.X10, jit.X6, jit.X8)
	asm.CMPreg(jit.X9, jit.X10)
	asm.BCond(jit.CondLO, trueLabel)
	asm.BCond(jit.CondHI, falseLabel)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.B(loopLabel)

	asm.Label(prefixLabel)
	asm.CMPreg(jit.X5, jit.X7)
	if cond == jit.CondLE {
		asm.BCond(jit.CondLS, trueLabel)
	} else {
		asm.BCond(jit.CondLO, trueLabel)
	}
	asm.B(falseLabel)
}

// emitBaselineStringEq compares two NaN-boxed string values in X0 and X1.
// Both operands must already be checked as strings.
func emitBaselineStringEq(asm *jit.Assembler, trueLabel, falseLabel string) {
	loopLabel := nextLabel("str_eq_loop")

	asm.LSLimm(jit.X2, jit.X0, 20)
	asm.LSRimm(jit.X2, jit.X2, 20)
	asm.LSLimm(jit.X3, jit.X1, 20)
	asm.LSRimm(jit.X3, jit.X3, 20)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, trueLabel)

	asm.LDR(jit.X4, jit.X2, 0)
	asm.LDR(jit.X5, jit.X2, 8)
	asm.LDR(jit.X6, jit.X3, 0)
	asm.LDR(jit.X7, jit.X3, 8)
	asm.CMPreg(jit.X5, jit.X7)
	asm.BCond(jit.CondNE, falseLabel)
	asm.CBZ(jit.X5, trueLabel)
	asm.CMPreg(jit.X4, jit.X6)
	asm.BCond(jit.CondEQ, trueLabel)

	asm.MOVimm16(jit.X8, 0)
	asm.Label(loopLabel)
	asm.LDRBreg(jit.X9, jit.X4, jit.X8)
	asm.LDRBreg(jit.X10, jit.X6, jit.X8)
	asm.CMPreg(jit.X9, jit.X10)
	asm.BCond(jit.CondNE, falseLabel)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.CMPreg(jit.X8, jit.X5)
	asm.BCond(jit.CondLT, loopLabel)
	asm.B(trueLabel)
}

// ---------------------------------------------------------------------------
// Logical test
// ---------------------------------------------------------------------------

// emitBaselineTest: if bool(R(A)) != bool(C) then PC++
func emitBaselineTest(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	c := vm.DecodeC(inst)

	loadSlot(asm, jit.X0, a)
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

	loadSlot(asm, jit.X0, b)
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
		storeSlot(asm, a, jit.X0)
	}
	asm.B(doneLabel)

	asm.Label(isTruthyLabel)
	if c == 0 {
		// bool(C)=false, truthy=true → skip (no assign)
		asm.B(skipLabel)
	} else {
		// bool(C)=true, truthy=true → assign R(A)=R(B)
		storeSlot(asm, a, jit.X0)
	}

	asm.Label(doneLabel)
}

// ---------------------------------------------------------------------------
// Integer-specialized templates (Tier 1 int-spec — see tier1_int_analysis.go)
// ---------------------------------------------------------------------------
//
// Skips per-operand tag dispatch when operands are statically known-int.
// Correctness: param-entry guard + overflow guard both exit via ExitDeopt,
// which the engine handles by disabling int-spec for the proto and
// recompiling generic. Overflow re-execution may replay side effects — for
// the target benchmarks (ack/fib/mutual_recursion with small values) it
// never fires.

// emitIntSpecDeopt emits the ExitDeopt exit sequence: store the guard PC,
// set ExitCode=2, then branch to the exit epilogue based on CallMode.
// deoptPC is the bytecode PC of the overflowing instruction; it is baked into
// the emitted code so Execute can resume the interpreter at exactly that point
// instead of restarting at pc=0 (which would replay earlier side effects).
func emitIntSpecDeopt(asm *jit.Assembler, deoptPC int) {
	asm.MOVimm16(jit.X0, uint16(deoptPC))
	asm.STR(jit.X0, mRegCtx, execCtxOffExitResumePC)
	asm.LoadImm64(jit.X0, int64(ExitDeopt))
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.LDR(jit.X0, mRegCtx, execCtxOffCallMode)
	asm.CBNZ(jit.X0, "direct_exit")
	asm.B("baseline_exit")
}

// emitParamIntGuards emits ARM64 tag checks for each param slot flagged in
// guardedParams. Other param slots are left alone (they aren't read as ints
// anywhere in the body, so their runtime type doesn't affect correctness).
// On any failure, exits via ExitDeopt so the tiering manager disables
// int-spec for this proto and re-executes using generic templates.
func emitParamIntGuards(asm *jit.Assembler, guardedParams uint64) {
	if guardedParams == 0 {
		return
	}
	failLabel := nextLabel("param_guard_fail")
	okLabel := nextLabel("param_guard_ok")
	// X3 = 0xFFFE (int tag). Reused across all param checks.
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	for i := 0; i < maxTrackedSlots; i++ {
		if guardedParams&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		loadSlot(asm, jit.X0, i)
		asm.LSRimm(jit.X2, jit.X0, 48)
		asm.CMPreg(jit.X2, jit.X3)
		asm.BCond(jit.CondNE, failLabel)
	}
	asm.B(okLabel)

	// Fail path: ExitDeopt → tiering manager falls back to generic.
	// PC=0 because param guards fire before any bytecode; no side effects to replay.
	asm.Label(failLabel)
	emitIntSpecDeopt(asm, 0)

	asm.Label(okLabel)
}

// emitBaselineArithIntSpec emits ADD/SUB/MUL assuming both operands are
// statically known to be ints. Skips the polymorphic tag dispatch. On int48
// overflow, exits via ExitDeopt.
func emitBaselineArithIntSpec(asm *jit.Assembler, inst uint32, op string, pc int) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	// Operands known int — skip tag dispatch. Extract 48-bit signed payload.
	asm.SBFX(jit.X4, jit.X0, 0, 48)
	asm.SBFX(jit.X5, jit.X1, 0, 48)

	switch op {
	case "add":
		asm.ADDreg(jit.X4, jit.X4, jit.X5)
	case "sub":
		asm.SUBreg(jit.X4, jit.X4, jit.X5)
	case "mul":
		asm.MUL(jit.X4, jit.X4, jit.X5)
	}

	// int48 overflow check: SBFX sign-extends lower 48 bits. If the result
	// differs from the full 64-bit value, it doesn't fit in 48 bits.
	overflowLabel := nextLabel("arith_spec_overflow")
	doneLabel := nextLabel("arith_spec_done")
	asm.SBFX(jit.X6, jit.X4, 0, 48)
	asm.CMPreg(jit.X6, jit.X4)
	asm.BCond(jit.CondNE, overflowLabel)

	// Box + store.
	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
	storeSlot(asm, a, jit.X4)
	asm.B(doneLabel)

	// Overflow → ExitDeopt (rare; target benchmarks never trip this).
	// Store this instruction's PC so Execute can resume the interpreter at
	// exactly this point, skipping re-execution of earlier side effects.
	asm.Label(overflowLabel)
	emitIntSpecDeopt(asm, pc)

	asm.Label(doneLabel)
}

// emitBaselineEQIntSpec emits EQ assuming both operands are known int.
// Raw CMPreg is correct for int48 equality: same int values have identical
// NaN-box bit patterns.
func emitBaselineEQIntSpec(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	skipLabel := pcLabel(pc + 2)

	asm.CMPreg(jit.X0, jit.X1)
	if a != 0 {
		// if (B==C) != true → if B != C then skip
		asm.BCond(jit.CondNE, skipLabel)
	} else {
		// if (B==C) != false → if B == C then skip
		asm.BCond(jit.CondEQ, skipLabel)
	}
}

// emitBaselineLTIntSpec emits LT assuming both operands are known int.
func emitBaselineLTIntSpec(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	skipLabel := pcLabel(pc + 2)

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
}

// emitBaselineLEIntSpec emits LE assuming both operands are known int.
func emitBaselineLEIntSpec(asm *jit.Assembler, inst uint32, pc int, code []uint32) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	loadRK(asm, jit.X0, bidx)
	loadRK(asm, jit.X1, cidx)

	skipLabel := pcLabel(pc + 2)

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
}

// Jump, ForLoop, Return, TForLoop are in tier1_control.go
