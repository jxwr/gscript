//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// Comparison instructions
// ────────────────────────────────────────────────────────────────────────────

// emitCmpInt handles SSA_EQ_INT.
// AuxInt encodes the "expected comparison result" (A field from OP_EQ).
// If A=1: guard passes when b == c (branch to side_exit if NE)
// If A=0: guard passes when b != c (branch to side_exit if EQ)
func (ec *emitCtx) emitCmpInt(inst *SSAInst, failCond Cond) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	ec.asm.CMPreg(a1, a2)
	if inst.AuxInt == 0 {
		// A=0: guard passes if NOT equal → fail if EQ
		failCond = failCond ^ 1 // invert condition
	}
	ec.emitGuardBranch(failCond, inst.PC)
}

// emitCmpIntLE handles SSA_LE_INT.
// For FORLOOP: guard passes if index <= limit → fail if GT (signed)
func (ec *emitCtx) emitCmpIntLE(idx int, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	ec.asm.CMPreg(a1, a2)
	// LE_INT: guard passes if a1 <= a2; exit if a1 > a2
	if idx == ec.loopExitIdx {
		// This is the OUTER FORLOOP exit check: branch to loop_done
		ec.asm.BCond(CondGT, "loop_done")
	} else if idx == ec.innerLoopExitIdx {
		// Inner FORLOOP exit: branch BACK to inner loop body if index <= limit,
		// fall through (continue outer body) if index > limit.
		// Store back inner loop registers to memory before branching back,
		// so the next iteration sees updated values.
		ec.emitInnerLoopStoreBack()
		ec.asm.BCond(CondLE, "inner_loop_body")
		// Fall through: inner loop done, continue outer body
	} else {
		ec.emitGuardBranch(CondGT, inst.PC)
	}
}

// emitCmpFloat handles float comparisons with a fail condition.
func (ec *emitCtx) emitCmpFloat(inst *SSAInst, failCond Cond) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	if inst.AuxInt == 0 {
		failCond = failCond ^ 1
	}
	ec.emitGuardBranch(failCond, inst.PC)
}

// emitCmpFloatBreak is like emitCmpFloat but branches to break_exit instead.
// Used for float comparison guards inside the inner loop body that represent
// break conditions (e.g., `if zr2+zi2 > 4.0 { break }`).
// The break_exit exits to the guard's PC so the VM re-executes the comparison
// and takes the break path (including any escaped=true assignments).
func (ec *emitCtx) emitCmpFloatBreak(inst *SSAInst, failCond Cond) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	if inst.AuxInt == 0 {
		failCond = failCond ^ 1
	}
	// Store the guard's PC for break_exit to use
	ec.breakGuardPC = inst.PC
	ec.asm.BCond(failCond, "break_exit")
}

// emitCmpFloatLE handles SSA_LE_FLOAT.
func (ec *emitCtx) emitCmpFloatLE(idx int, inst *SSAInst) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	// LE: guard passes if a1 <= a2; exit if GT
	if idx == ec.loopExitIdx {
		ec.asm.BCond(CondGT, "loop_done")
	} else if idx == ec.innerLoopExitIdx {
		ec.emitInnerLoopStoreBack()
		ec.asm.BCond(CondLE, "inner_loop_body")
	} else {
		ec.emitGuardBranch(CondGT, inst.PC)
	}
}

// emitGuardBranch emits a conditional branch to the side-exit path.
// Sets X9 = ExitPC before the conditional branch so side_exit_setup
// knows where the interpreter should resume.
//
// For function traces with self-calls, body guards branch to
// "self_call_body_guard" instead of "side_exit_setup". This allows
// the base case (e.g., n < 2 for fib) to be handled natively when
// inside a nested self-call (depth > 0), rather than side-exiting
// through the epilogue which would corrupt the ARM64 stack.
func (ec *emitCtx) emitGuardBranch(failCond Cond, pc int) {
	// Set ExitPC BEFORE the branch (X9 must be ready when side_exit_setup runs).
	// This is safe because X9 is a scratch register not used by the trace.
	ec.asm.LoadImm64(X9, int64(pc))
	if ec.hasSelfCalls {
		ec.asm.BCond(failCond, "self_call_body_guard")
	} else {
		ec.asm.BCond(failCond, "side_exit_setup")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Guard truthy
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitGuardTruthy(inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

	// If Arg1 refers to a compile-time constant (CONST_BOOL or CONST_NIL),
	// resolve the guard statically. These constants don't write to memory,
	// so reading from memory would give stale values.
	if int(inst.Arg1) < len(ec.f.Insts) {
		srcInst := &ec.f.Insts[inst.Arg1]
		if srcInst.Op == SSA_CONST_BOOL || srcInst.Op == SSA_CONST_NIL {
			isTruthy := srcInst.Op == SSA_CONST_BOOL && srcInst.AuxInt != 0
			if inst.AuxInt != 0 {
				// Guard passes if truthy
				if !isTruthy {
					// Constant is falsy → guard fails → unconditional side-exit
					ec.asm.LoadImm64(X9, int64(inst.PC))
					ec.asm.B("side_exit_setup")
				}
				// else: guard passes, emit nothing
			} else {
				// Guard passes if falsy
				if isTruthy {
					// Constant is truthy → guard fails → unconditional side-exit
					ec.asm.LoadImm64(X9, int64(inst.PC))
					ec.asm.B("side_exit_setup")
				}
				// else: guard passes, emit nothing
			}
			return
		}
	}

	// Set ExitPC for guard failure
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Load the NaN-boxed value from memory
	ec.asm.LDR(X0, regRegs, slot*ValueSize)

	// Check if nil: NB_ValNil = 0xFFFC000000000000
	ec.asm.LoadImm64(X1, nb_i64(NB_ValNil))
	ec.asm.CMPreg(X0, X1)

	if inst.AuxInt != 0 {
		// AuxInt=1 (C=1): guard passes if truthy → fail if nil
		ec.asm.BCond(CondEQ, "side_exit_setup")
		// Also check false: NB_ValFalse = 0xFFFD000000000000
		ec.asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		ec.asm.CMPreg(X0, X1)
		ec.asm.BCond(CondEQ, "side_exit_setup")
	} else {
		// AuxInt=0 (C=0): guard passes if falsy → fail if NOT nil AND NOT false
		// i.e., fail if truthy
		label := "guard_truthy_ok_" + itoa(ec.guardTruthyCount)
		ec.guardTruthyCount++
		ec.asm.BCond(CondEQ, label)
		ec.asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		ec.asm.CMPreg(X0, X1)
		ec.asm.BCond(CondEQ, label)
		// Not nil, not false → truthy → fail
		ec.asm.B("side_exit_setup")
		ec.asm.Label(label)
	}
}
