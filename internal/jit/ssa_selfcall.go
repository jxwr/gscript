//go:build darwin && arm64

package jit

import "fmt"

// ────────────────────────────────────────────────────────────────────────────
// Native self-recursion for function traces
// ────────────────────────────────────────────────────────────────────────────
//
// When a function trace contains SSA_SELF_CALL instructions, the emitter
// generates ARM64 BL (branch-and-link) instructions to recursively call the
// same native code. This avoids the interpreter overhead of side-exiting and
// re-entering for each recursive call.
//
// Register convention:
//   X25: self-call depth counter (0 = outermost, incremented on each BL)
//   X20-X23: allocated GPR values (callee-saved, pushed on ARM64 stack before BL)
//   X26 (regRegs): VM register array pointer (shared across depths)
//   X28: extra callee-saved register for self-call results without GPR allocation
//
// Stack frame per self-call (16-byte aligned, 48 bytes):
//   [SP+0]  = saved X30 (LR)
//   [SP+8]  = saved X20
//   [SP+16] = saved X21
//   [SP+24] = saved X22
//   [SP+32] = saved X23
//   [SP+40] = saved X28 (self-call result overflow register)
//
// Base case handling:
//   When a comparison guard (e.g., n < 2) fires during a nested self-call
//   (depth > 0), the self_call_body_guard path handles the base case natively:
//   it writes the parameter (slot 0) to the return slot and RETs to the caller.
//   At depth == 0, the normal side-exit path is taken.

// maxSelfCallDepth is the maximum native recursion depth before falling back.
const maxSelfCallDepth = 400

// regSelfDepth is the ARM64 register used for the self-call depth counter.
const regSelfDepth = X25

// regSelfExtra is an extra register saved across self-calls for results
// that don't have a GPR allocation. X28 is callee-saved by the JIT prologue.
const regSelfExtra = X28

// emitSelfCall emits native ARM64 code for a self-recursive function call.
//
// The sequence:
//  1. Save LR + live GPRs + X28 on the ARM64 stack (48 bytes)
//  2. Write argument(s) to memory slot 0 (callee's parameter)
//  3. Increment depth counter
//  4. BL to self_call_entry (re-runs guards + body)
//  5. After return: decrement depth, restore GPRs, load result from memory
//
// For result slots without a GPR allocation, the result is loaded into X28
// which is saved/restored across subsequent self-calls.
func (ec *emitCtx) emitSelfCall(ref SSARef, inst *SSAInst) {
	asm := ec.asm
	seq := ec.selfCallSeq
	ec.selfCallSeq++

	overflowLabel := fmt.Sprintf("self_overflow_%d", seq)
	resultSlot := ec.f.Trace.FuncReturnSlot

	// Resolve argument value BEFORE saving registers (it might be in a GPR).
	argReg := ec.resolveIntRef(inst.Arg1, X0)

	// Save LR + all live GPRs + X28 on ARM64 stack.
	// 32 bytes: {X30, X20}, {X21, X22}, {X23, X28} — but use minimal frame.
	// For fib-like functions, X20 holds n which must survive across BL.
	// Save all 4 GPRs + LR + X28 = 6 regs = 48 bytes (16-byte aligned).
	asm.STPpre(X30, X20, SP, -48)
	asm.STP(X21, X22, SP, 16)
	asm.STP(X23, regSelfExtra, SP, 32)

	// Increment depth counter
	asm.ADDimm(regSelfDepth, regSelfDepth, 1)

	// Check depth limit
	asm.CMPimm(regSelfDepth, maxSelfCallDepth)
	asm.BCond(CondGE, overflowLabel)

	// Write argument to slot 0 (callee's first parameter) in memory.
	EmitBoxIntFast(asm, X0, argReg, regTagInt)
	asm.STR(X0, regRegs, 0*ValueSize)

	// If there's a second argument (Arg2), write it to slot 1.
	if inst.Arg2 != SSARefNone {
		arg2Reg := ec.resolveIntRef(inst.Arg2, X1)
		EmitBoxIntFast(asm, X1, arg2Reg, regTagInt)
		asm.STR(X1, regRegs, 1*ValueSize)
	}

	// BL to self_call_entry — re-enters at pre-loop guards + loads.
	asm.BL("self_call_entry")

	// After return: callee stored result to memory at returnSlot.
	asm.SUBimm(regSelfDepth, regSelfDepth, 1)

	// Restore all GPRs + LR + X28 from stack.
	asm.LDP(X23, regSelfExtra, SP, 32)
	asm.LDP(X21, X22, SP, 16)
	asm.LDPpost(X30, X20, SP, 48)

	// Load result from memory (return slot) into the destination register.
	// The result is NaN-boxed in memory; unbox to raw int.
	dstSlot := int(inst.Slot)
	if dstReg, ok := ec.regMap.IntReg(dstSlot); ok {
		asm.LDR(dstReg, regRegs, resultSlot*ValueSize)
		EmitUnboxInt(asm, dstReg, dstReg)
	} else {
		// No GPR for this slot. Load the NaN-boxed result and spill to dstSlot.
		// This ensures the value survives across subsequent self-calls that may
		// overwrite the returnSlot. Use X0 as scratch (safe after BL return).
		asm.LDR(X0, regRegs, resultSlot*ValueSize)
		// Spill NaN-boxed value directly to dstSlot memory.
		asm.STR(X0, regRegs, dstSlot*ValueSize)
	}

	// Overflow handler: depth exceeded, unwind everything and side-exit.
	ec.emitSelfCallOverflow(overflowLabel, inst.PC)
}

// emitSelfCallOverflow emits the overflow handler for self-call depth exceeded.
// Unwinds the ARM64 stack to the outermost frame and side-exits to the interpreter.
func (ec *emitCtx) emitSelfCallOverflow(label string, pc int) {
	asm := ec.asm

	// Emit as a cold path: forward branch skips over it.
	skipLabel := label + "_skip"
	asm.B(skipLabel)

	asm.Label(label)
	// Unwind: restore SP to frame pointer (unwinds all self-call stack frames).
	// Must use ADD, not MOVreg — MOVreg encodes ORR where reg31=XZR, not SP.
	asm.ADDimm(SP, X29, 0)
	// Reload regRegs from context (may have been modified by nested calls)
	asm.LDR(regRegs, regCtx, TraceCtxOffRegs)
	// Restore function reference to its slot (GETGLOBAL was NOP'd in SSA,
	// but the interpreter needs it for the CALL instruction after side-exit).
	fnSlot := ec.f.Trace.SelfCallFnSlot
	fnConstIdx := ec.f.Trace.SelfCallFnConstIdx
	asm.LDR(X1, regConsts, fnConstIdx*ValueSize)
	asm.STR(X1, regRegs, fnSlot*ValueSize)
	// Reset depth counter
	asm.MOVimm16(regSelfDepth, 0)
	// Set ExitPC
	asm.LoadImm64(X9, int64(pc))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)
	// Side exit (ExitCode = 1)
	asm.LoadImm64(X0, 1)
	asm.B("epilogue")

	asm.Label(skipLabel)
}

// emitSelfCallGuardFail emits two guard paths for function traces with self-calls:
//
// 1. self_call_body_guard: Body comparison guard failure (e.g., n < 2 in fib).
//    If depth > 0, handle base case natively (return parameter value).
//    If depth == 0, normal side-exit to interpreter.
//
// 2. guard_fail: Pre-loop type guard failure. If depth > 0, handle base case.
//    If depth == 0, normal guard fail (ExitCode=2).
func (ec *emitCtx) emitSelfCallGuardFail() {
	asm := ec.asm
	returnSlot := ec.f.Trace.FuncReturnSlot

	// --- Body guard handler (comparison guards in function body) ---
	asm.Label("self_call_body_guard")
	// Check depth: if 0, restore function ref and side-exit to interpreter
	asm.CBNZ(regSelfDepth, "self_call_base_case")
	// Depth 0: restore function reference for interpreter's CALL
	fnSlot := ec.f.Trace.SelfCallFnSlot
	fnConstIdx := ec.f.Trace.SelfCallFnConstIdx
	asm.LDR(X0, regConsts, fnConstIdx*ValueSize)
	asm.STR(X0, regRegs, fnSlot*ValueSize)
	asm.B("side_exit_setup")
	asm.Label("self_call_base_case")
	// Depth > 0: base case. Return the parameter (slot 0) as the result.
	asm.LDR(X0, regRegs, 0*ValueSize)             // load slot 0 (NaN-boxed param)
	asm.STR(X0, regRegs, returnSlot*ValueSize)     // write to return slot
	asm.RET()                                       // return to BL caller

	// --- Pre-loop type guard handler ---
	asm.Label("guard_fail")
	// Check depth: if 0, this is the outermost call → normal guard fail
	asm.CBZ(regSelfDepth, "guard_fail_outer")
	// Depth > 0: type guard failed in nested call. Return parameter as result.
	asm.LDR(X0, regRegs, 0*ValueSize)
	asm.STR(X0, regRegs, returnSlot*ValueSize)
	asm.RET()

	asm.Label("guard_fail_outer")
	// Normal guard fail: type mismatch at outermost level
	asm.LoadImm64(X0, 2) // ExitCode = 2 (guard fail)
	asm.B("epilogue")
}
