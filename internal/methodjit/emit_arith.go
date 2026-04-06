//go:build darwin && arm64

// emit_arith.go contains ARM64 emission for constants, slot access,
// integer arithmetic (NaN-boxed and raw-int), comparisons, and unary ops.
// Split from emit.go to keep files under 1000 lines.

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
)

// --- Constant emission ---
// Each stores the NaN-boxed constant to the value's home slot via X0 scratch.

func (ec *emitContext) emitConstInt(instr *Instr) {
	// If type-specialized (TypeInt), store as raw int64. This avoids boxing
	// the constant and then immediately unboxing it for type-specialized ops.
	// The raw int will be boxed on demand by resolveValueNB if a generic op needs it.
	if instr.Type == TypeInt {
		dst := jit.X0
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
			dst = jit.Reg(pr.Reg)
		}
		ec.asm.LoadImm64(dst, instr.Aux)
		ec.storeRawInt(dst, instr.ID)
		return
	}
	// Fallback: Load raw int value, NaN-box it, store as NaN-boxed.
	ec.asm.LoadImm64(jit.X0, instr.Aux)
	jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeResultNB(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstNil(instr *Instr) {
	jit.EmitBoxNil(ec.asm, jit.X0)
	ec.storeResultNB(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstBool(instr *Instr) {
	if instr.Aux != 0 {
		// true = NB_TagBool|1. Compute from pinned X25 (1 ADD instruction).
		ec.asm.ADDimm(jit.X0, mRegTagBool, 1)
	} else {
		// false = NB_TagBool|0. Use pinned X25 directly (1 MOV instruction).
		ec.asm.MOVreg(jit.X0, mRegTagBool)
	}
	ec.storeResultNB(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstFloat(instr *Instr) {
	// If type-specialized (TypeFloat) with FPR allocation, load directly into FPR.
	// The constant's Aux is math.Float64bits(value), which we load into a GPR
	// and then FMOV to the allocated FPR.
	if instr.Type == TypeFloat {
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
			ec.asm.LoadImm64(jit.X0, instr.Aux)
			dstF := jit.FReg(pr.Reg)
			ec.asm.FMOVtoFP(dstF, jit.X0)
			ec.storeRawFloat(dstF, instr.ID)
			return
		}
	}
	// Fallback: NaN-boxed path (float bits ARE NaN-boxed representation).
	ec.asm.LoadImm64(jit.X0, instr.Aux)
	ec.storeResultNB(jit.X0, instr.ID)
}

// --- Slot access ---

func (ec *emitContext) emitLoadSlot(instr *Instr) {
	// Check if this value has a register allocation (don't use hasReg which
	// checks activeRegs -- this is where we ACTIVATE the register).
	_, ok := ec.alloc.ValueRegs[instr.ID]
	if ok {
		// Register-resident: load from VM slot into allocated register.
		// Handles both GPR (int, any) and FPR (float) allocations.
		ec.emitLoadSlotToReg(instr)
		return
	}
	// Memory-to-memory: LoadSlot's home IS the VM slot; no code needed.
}

func (ec *emitContext) emitStoreSlot(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	// Get the NaN-boxed value from register or memory, store to target VM slot.
	// resolveValueNB handles raw-int values by boxing them automatically.
	reg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	slot := int(instr.Aux)
	ec.asm.STR(reg, mRegRegs, slotOffset(slot))
}

// --- Integer binary operations (NaN-boxed) ---

type intBinOp int

const (
	intBinAdd intBinOp = iota
	intBinSub
	intBinMul
	intBinMod
)

func (ec *emitContext) emitIntBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}

	// Resolve both operands: NaN-boxed from register or memory, then unbox.
	lhsSrc := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	jit.EmitUnboxInt(ec.asm, jit.X0, lhsSrc) // X0 = raw int lhs

	rhsSrc := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	jit.EmitUnboxInt(ec.asm, jit.X1, rhsSrc) // X1 = raw int rhs

	// Perform the operation into X0.
	switch op {
	case intBinAdd:
		ec.asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case intBinSub:
		ec.asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case intBinMul:
		ec.asm.MUL(jit.X0, jit.X0, jit.X1)
	case intBinMod:
		ec.asm.SDIV(jit.X2, jit.X0, jit.X1)
		ec.asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	}

	// Check for int48 overflow on ADD/SUB/MUL (MOD cannot overflow).
	// Skip for loop counter increments (Aux2=1): bounded by loop limit.
	// Skip when range analysis proved the result fits in int48.
	if op != intBinMod && instr.Aux2 == 0 && !ec.int48Safe(instr.ID) {
		ec.emitInt48OverflowCheck(jit.X0, instr)
	}

	// Rebox result and store to register or memory.
	jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeResultNB(jit.X0, instr.ID)
}

// --- Raw int binary operation (type-specialized, no unbox/box) ---
// When TypeSpec has proven both operands are int, we keep raw int64 values
// in registers. This saves 4 instructions per operation (2 unbox + 1 box + 1 MOV).
//
// When one operand is a small constant (fits in 12-bit unsigned), uses
// ADDimm/SUBimm instead of ADDreg/SUBreg, saving the register load.
func (ec *emitContext) emitRawIntBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}

	// Compute directly with raw ints — destination can be the allocated register.
	dst := jit.X0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
		dst = jit.Reg(pr.Reg)
	}

	// Try immediate form for add/sub when one operand is a small constant.
	if op == intBinAdd || op == intBinSub {
		if imm, ok := ec.constIntImm12(instr.Args[1].ID); ok {
			lhs := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
			if op == intBinAdd {
				ec.asm.ADDimm(dst, lhs, imm)
			} else {
				ec.asm.SUBimm(dst, lhs, imm)
			}
			if instr.Aux2 == 0 && !ec.int48Safe(instr.ID) {
				ec.emitInt48OverflowCheck(dst, instr)
			}
			ec.storeRawInt(dst, instr.ID)
			return
		}
		// Also check if LHS is constant (for ADD which is commutative).
		if op == intBinAdd {
			if imm, ok := ec.constIntImm12(instr.Args[0].ID); ok {
				rhs := ec.resolveRawInt(instr.Args[1].ID, jit.X1)
				ec.asm.ADDimm(dst, rhs, imm)
				if instr.Aux2 == 0 && !ec.int48Safe(instr.ID) {
					ec.emitInt48OverflowCheck(dst, instr)
				}
				ec.storeRawInt(dst, instr.ID)
				return
			}
		}
	}

	lhs := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
	rhs := ec.resolveRawInt(instr.Args[1].ID, jit.X1)

	switch op {
	case intBinAdd:
		ec.asm.ADDreg(dst, lhs, rhs)
	case intBinSub:
		ec.asm.SUBreg(dst, lhs, rhs)
	case intBinMul:
		ec.asm.MUL(dst, lhs, rhs)
	case intBinMod:
		ec.asm.SDIV(jit.X2, lhs, rhs)
		ec.asm.MSUB(dst, jit.X2, rhs, lhs)
	}

	// Check for int48 overflow on ADD/SUB/MUL (MOD cannot overflow).
	// Skip for loop counter increments (Aux2=1): bounded by loop limit.
	// Skip when range analysis proved the result fits in int48.
	if op != intBinMod && instr.Aux2 == 0 && !ec.int48Safe(instr.ID) {
		ec.emitInt48OverflowCheck(dst, instr)
	}

	// Mark as raw int in register (no box needed until block boundary/return).
	ec.storeRawInt(dst, instr.ID)
}

// int48Safe reports whether range analysis proved that instr's result
// fits in the int48 signed range. When true, the emitter may skip the
// SBFX+CMP+B.NE overflow check (saves 3 ARM64 instructions per op).
func (ec *emitContext) int48Safe(id int) bool {
	if ec.fn == nil || ec.fn.Int48Safe == nil {
		return false
	}
	return ec.fn.Int48Safe[id]
}

// --- Raw int unary negate (type-specialized, no unbox/box) ---
// When TypeSpec has proven the operand is int, we keep raw int64 values
// in registers. This saves ~12 instructions of the generic Unm path.
func (ec *emitContext) emitNegInt(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	src := ec.resolveRawInt(instr.Args[0].ID, jit.X0)

	// Compute directly with raw int — destination can be the allocated register.
	dst := jit.X0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
		dst = jit.Reg(pr.Reg)
	}

	ec.asm.NEG(dst, src)

	// Check for int48 overflow (e.g., negating minInt48 produces maxInt48+1).
	// Skip when range analysis proved the result fits in int48.
	if !ec.int48Safe(instr.ID) {
		ec.emitInt48OverflowCheck(dst, instr)
	}

	// Mark as raw int in register (no box needed until block boundary/return).
	ec.storeRawInt(dst, instr.ID)
}

// emitInt48OverflowCheck emits an overflow check for a raw int64 result.
// If the value does not fit in 48-bit signed range (i.e., SBFX(result,0,48) != result),
// the JIT deopts to the interpreter which handles overflow via float promotion.
// Uses X0 as scratch (safe: X0 is always available for scratch).
func (ec *emitContext) emitInt48OverflowCheck(result jit.Reg, instr *Instr) {
	asm := ec.asm
	okLabel := ec.uniqueLabel("int48_ok")

	// SBFX X0, result, #0, #48 — sign-extend the lower 48 bits to 64 bits.
	// If the result fits in 48-bit signed, this produces the same value.
	scratch := jit.X0
	if result == jit.X0 {
		scratch = jit.X1
	}
	asm.SBFX(scratch, result, 0, 48)
	asm.CMPreg(scratch, result)
	asm.BCond(jit.CondEQ, okLabel)

	// Flush ALL register-resident values to VM register file before deopt.
	// This ensures the interpreter sees correct state when it re-runs.
	// Without this, loopExitBoxPhis values live only in registers and the
	// interpreter would find stale memory (e.g., fibonacci_iterative bug).
	ec.emitStoreAllActiveRegs()
	// Deopt may happen anywhere inside the loop nest; box ALL deferred
	// phis to keep the interpreter's view of memory consistent.
	ec.emitLoopExitBoxing(-1)

	// Overflow: deopt to interpreter.
	asm.LoadImm64(jit.X0, ExitDeopt)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	asm.Label(okLabel)
}

// --- Integer comparison (NaN-boxed) ---

func (ec *emitContext) emitIntCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}

	// Use raw int path if available (from type-specialized ops).
	lhs := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
	rhs := ec.resolveRawInt(instr.Args[1].ID, jit.X1)

	// Compare: sets NZCV flags.
	ec.asm.CMPreg(lhs, rhs)

	// Fused path: preceding CMP already set flags; the following Branch
	// will emit B.cc directly. Skip bool materialization (saves 3 insns).
	if ec.fusedCmps[instr.ID] {
		ec.fusedCond = cond
		ec.fusedActive = true
		return
	}

	// Normal path: materialize NaN-boxed bool.
	// Set result: 1 if condition true, 0 if false.
	ec.asm.CSET(jit.X0, cond)

	// Box as bool: NB_TagBool | (0 or 1). X25 = pinned NB_TagBool.
	ec.asm.ORRreg(jit.X0, jit.X0, mRegTagBool)

	// Store NaN-boxed bool result to register or memory.
	ec.storeResultNB(jit.X0, instr.ID)
}

