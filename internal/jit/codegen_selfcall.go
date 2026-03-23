//go:build darwin && arm64

package jit

import (
	"fmt"
)

// ──────────────────────────────────────────────────────────────────────────────
// Call-exit analysis and resume dispatch
// ──────────────────────────────────────────────────────────────────────────────

// analyzeCallExitPCs collects bytecode PCs that will use call-exit (ExitCode=2).
// These are unsupported opcodes (GETGLOBAL, SETGLOBAL, CALL) that the executor
// can handle and then re-enter JIT at the next instruction.
//
// Optimization: A call-exit is only worthwhile if the successor instruction
// (pc+1) can do useful JIT work — i.e., it is supported, an inline candidate,
// or itself a call-exit. If the successor would immediately cause a permanent
// side-exit, we demote the current instruction to permanent side-exit too,
// avoiding a wasted exit/re-enter cycle.
// Processing in reverse order handles cascading: if pc+2 is unsupported, pc+1
// gets demoted, and then pc gets demoted as well.

// emitSelfCallReturn handles RETURN for functions with self-recursive calls.
// If X25 > 0 (we're inside a self-call), return via RET with result in X0.
// If X25 == 0 (outermost call), write to JITContext and go to epilogue.
func (cg *Codegen) emitSelfCallReturn(pc, aReg, b int) error {
	a := cg.asm

	if b == 0 {
		// Variable return count (RETURN A B=0).
		// In self-call functions, treat as a single-value return at all depths.
		// The JIT doesn't maintain vm.top, so we can't side-exit to the interpreter
		// for variable returns — it would compute a wrong return count.
		// Self-call functions only return single values through the JIT path anyway.
		cg.loadRegIval(X0, aReg)
		outerVarLabel := fmt.Sprintf("outermost_varret_%d", pc)
		a.CBZ(regSelfDepth, outerVarLabel)
		a.RET() // nested self-call: return single value in X0

		// Outermost: return single value via JITContext.
		// Write type tag for the return register so the executor reads a valid Value.
		a.Label(outerVarLabel)
		a.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
		a.LoadImm64(X1, int64(aReg))
		a.STR(X1, regCtx, ctxOffRetBase)
		a.LoadImm64(X1, 1) // 1 return value
		a.STR(X1, regCtx, ctxOffRetCount)
		a.LoadImm64(X0, 0) // ExitCode = 0 (normal return)
		a.B("epilogue")
		return nil
	}

	nret := 0
	if b > 1 {
		nret = b - 1
		// Load first return value into X0.
		cg.loadRegIval(X0, aReg)
	} else {
		// Return nothing — X0 = 0.
		a.LoadImm64(X0, 0)
	}

	// Check depth: if > 0, this is a nested self-call return.
	outerLabel := fmt.Sprintf("outermost_ret_%d", pc)
	a.CBZ(regSelfDepth, outerLabel)
	// Self-call return: X0 = result, just RET back to BL caller.
	a.RET()

	// Outermost return: write type tag for the return register so the executor
	// reads a valid Value from the register array.
	a.Label(outerLabel)
	if nret > 0 {
		a.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
	}
	a.LoadImm64(X1, int64(aReg))
	a.STR(X1, regCtx, ctxOffRetBase)
	a.LoadImm64(X1, int64(nret))
	a.STR(X1, regCtx, ctxOffRetCount)
	a.LoadImm64(X0, 0)
	a.B("epilogue")
	return nil
}

// Maximum self-recursion depth before falling back to interpreter.
const maxSelfRecursionDepth = 200

// emitSelfCall emits native ARM64 code for a self-recursive function call.
// For 1-parameter functions: saves LR (X30) + X19 in a 16-byte frame.
// For 2+-parameter functions: saves LR (X30) + X19 + X22 in a 32-byte frame.
// regRegs (X26) is restored by subtraction after the call.
// The depth counter (X25) is managed via increment/decrement.
//
// Tail call optimization: if isTailCall is true, the CALL is immediately
// followed by RETURN. Instead of BL + save/restore + result handling, we
// load the new arguments into the pinned registers and B (jump) directly
// to self_call_entry. This reuses the caller's stack frame and depth level.
func (cg *Codegen) emitSelfCall(pc int, candidate *inlineCandidate) error {
	if candidate.isTailCall {
		return cg.emitSelfTailCall(pc, candidate)
	}
	return cg.emitSelfCallFull(pc, candidate)
}

// emitSelfTailCall emits a tail-call-optimized self-recursive call.
// Instead of the full BL/save/restore sequence (~15 instructions), this emits
// direct register writes + unconditional branch to self_call_entry.
// When arg traces are available, arguments are written directly to pinned
// registers without intermediate memory stores/loads.
func (cg *Codegen) emitSelfTailCall(pc int, candidate *inlineCandidate) error {
	a := cg.asm
	fnReg := candidate.fnReg
	numParams := cg.proto.NumParams

	// Check if all args are traced (needed for direct register passing).
	allTraced := len(candidate.argTraces) >= numParams
	if allTraced {
		for i := 0; i < numParams; i++ {
			if !candidate.argTraces[i].traced {
				allTraced = false
				break
			}
		}
	}

	if !allTraced {
		// Fallback: load from memory (NaN-boxed → unbox int).
		a.LDR(regSelfArg, regRegs, regValOffset(fnReg+1))
		EmitUnboxInt(a, regSelfArg, regSelfArg)
		if numParams > 1 {
			a.LDR(regSelfArg2, regRegs, regValOffset(fnReg+2))
			EmitUnboxInt(a, regSelfArg2, regSelfArg2)
		}
		a.B("self_call_entry")
		return nil
	}

	// Direct register passing: write args directly to X19/X22 and jump.
	// The arg setup instructions (SUB/LOADINT/MOVE) were skipped, so we
	// emit the equivalent operations targeting pinned registers directly.
	cg.emitDirectArgs(candidate, numParams)

	a.B("self_call_entry")
	return nil
}

// emitDirectArgs computes call arguments directly into pinned registers (X19/X22)
// using traced argument sources, avoiding the store→window advance→load roundtrip.
// Used by both emitSelfTailCall and emitSelfCallFull (directArgs mode).

// emitDirectArgs computes call arguments directly into pinned registers (X19/X22)
// using traced argument sources, avoiding the store→window advance→load roundtrip.
// Used by both emitSelfTailCall and emitSelfCallFull (directArgs mode).
func (cg *Codegen) emitDirectArgs(candidate *inlineCandidate, numParams int) {
	a := cg.asm
	pinnedDst := [2]Reg{regSelfArg, regSelfArg2}

	// Check for dependency: does writing arg0 clobber a register that arg1 reads?
	emitOrder := [2]int{0, 1}
	if numParams == 2 {
		t0 := candidate.argTraces[0]
		t1 := candidate.argTraces[1]
		// arg0 writes to X19. Check if arg1 reads from X19.
		if t0.arithOp != "" || !t0.isConst {
			readsSrc := -1
			if t1.arithOp != "" {
				readsSrc = t1.arithSrc
			} else if !t1.isConst {
				readsSrc = t1.fromReg
			}
			if readsSrc >= 0 {
				if srcArm, ok := cg.pinnedRegs[readsSrc]; ok && srcArm == pinnedDst[0] {
					emitOrder = [2]int{1, 0} // emit arg1 first
				}
			}
		}
	}

	for idx := 0; idx < numParams; idx++ {
		i := emitOrder[idx]
		t := candidate.argTraces[i]
		dst := pinnedDst[i]

		if t.isConst && t.arithOp == "" {
			// Constant: MOVimm directly to pinned register.
			a.LoadImm64(dst, t.fromConst)
		} else if t.arithOp != "" {
			// SUB/ADD with const: emit directly to pinned register.
			srcArm, srcPinned := cg.pinnedRegs[t.arithSrc]
			src := X0
			if srcPinned {
				src = srcArm
			} else {
				a.LDR(X0, regRegs, regValOffset(t.arithSrc))
				EmitUnboxInt(a, X0, X0)
			}
			switch t.arithOp {
			case "SUB":
				a.SUBimm(dst, src, uint16(t.fromConst))
			case "ADD":
				a.ADDimm(dst, src, uint16(t.fromConst))
			}
		} else {
			// MOVE: copy from source register.
			srcArm, srcPinned := cg.pinnedRegs[t.fromReg]
			if srcPinned {
				if srcArm != dst {
					a.MOVreg(dst, srcArm)
				}
			} else {
				a.LDR(dst, regRegs, regValOffset(t.fromReg))
				EmitUnboxInt(a, dst, dst)
			}
		}
	}
}

// emitDirectArgLoad replaces a single LDR (load arg from register window) with
// a single equivalent instruction computed from the traced arg source.
// Emits exactly 1 instruction to maintain code alignment.
func (cg *Codegen) emitDirectArgLoad(t inlineArgTrace, dst Reg) {
	a := cg.asm
	if t.arithOp != "" {
		// SUB/ADD with const: compute directly into pinned register.
		srcArm, srcPinned := cg.pinnedRegs[t.arithSrc]
		src := X0
		if srcPinned {
			src = srcArm
		} else {
			// Source not pinned — must load from memory. Load NaN-boxed + unbox.
			a.LDR(dst, regRegs, regValOffset(t.arithSrc))
			EmitUnboxInt(a, dst, dst)
			return
		}
		switch t.arithOp {
		case "SUB":
			a.SUBimm(dst, src, uint16(t.fromConst))
		case "ADD":
			a.ADDimm(dst, src, uint16(t.fromConst))
		}
	} else if t.isConst {
		// Constant: load immediate. May be >1 instruction for large values.
		a.LoadImm64(dst, t.fromConst)
	} else {
		// MOVE: copy from source register.
		srcArm, srcPinned := cg.pinnedRegs[t.fromReg]
		if srcPinned && srcArm == dst {
			// Same register — emit NOP to maintain instruction count.
			a.NOP()
		} else if srcPinned {
			a.MOVreg(dst, srcArm)
		} else {
			a.LDR(dst, regRegs, regValOffset(t.fromReg))
			EmitUnboxInt(a, dst, dst)
		}
	}
}

// emitSelfCallFull emits the full non-tail self-recursive call sequence.
func (cg *Codegen) emitSelfCallFull(pc int, candidate *inlineCandidate) error {
	a := cg.asm
	fnReg := candidate.fnReg
	hasArg2 := cg.proto.NumParams > 1
	skipSave := candidate.skipArgSave

	overflowLabel := fmt.Sprintf("self_overflow_%d", pc)


	// Increment depth counter (before stack push, so overflow unwind is simpler).
	a.ADDimm(regSelfDepth, regSelfDepth, 1)

	// Check depth limit — side exit if too deep.
	a.CMPimm(regSelfDepth, maxSelfRecursionDepth)
	a.BCond(CondGE, overflowLabel)

	// Save callee-saved registers on the ARM64 stack.
	// SP must remain 16-byte aligned.
	// Must save BEFORE directArgs computation to preserve original X19/X22.
	if skipSave {
		// Lightweight frame: only save LR (X30). X19/X22 are not needed after
		// this call returns (next instruction is a tail self-call or RETURN).
		a.STRpre(X30, SP, -16) // SP -= 16; [SP] = X30
	} else if hasArg2 {
		// 32-byte frame: [SP] = {X30, X19}, [SP+16] = {X22, padding}
		a.STPpre(X30, regSelfArg, SP, -32) // SP -= 32; [SP] = {X30, X19}
		a.STR(regSelfArg2, SP, 16)         // [SP+16] = X22
	} else {
		// 16-byte frame: [SP] = {X30, X19}
		a.STPpre(X30, regSelfArg, SP, -16) // SP -= 16; [SP] = {X30, X19}
	}

	// Advance regRegs to callee's register window.
	// Callee's R(0) = Caller's R(fnReg+1).
	offset := (fnReg + 1) * ValueSize
	if offset <= 4095 {
		a.ADDimm(regRegs, regRegs, uint16(offset))
	} else {
		a.LoadImm64(X0, int64(offset))
		a.ADDreg(regRegs, regRegs, X0)
	}

	if candidate.directArgs {
		// directArgs mode: compute args directly into pinned registers from traced
		// sources instead of loading from the register window. Each LDR is replaced
		// by the equivalent computation (SUBimm/ADDimm/MOVreg/NOP) to maintain
		// the same instruction count for code alignment.
		cg.emitDirectArgLoad(candidate.argTraces[0], regSelfArg)
		if hasArg2 {
			cg.emitDirectArgLoad(candidate.argTraces[1], regSelfArg2)
		}
	} else {
		// Load callee's pinned parameters from the new register window.
		// The callee at self_call_entry skips these loads since args are already loaded.
		a.LDR(regSelfArg, regRegs, regValOffset(0))
		EmitUnboxInt(a, regSelfArg, regSelfArg)
		if hasArg2 {
			a.LDR(regSelfArg2, regRegs, regValOffset(1))
			EmitUnboxInt(a, regSelfArg2, regSelfArg2)
		}
	}

	// BL to self_call_entry (re-enters the function body).
	a.BL("self_call_entry")

	// After return: X0 = result (ival).
	// Restore callee-saved registers from stack.
	if skipSave {
		// Lightweight frame: only restore LR.
		a.LDRpost(X30, SP, 16)                // X30 = [SP]; SP += 16
	} else if hasArg2 {
		a.LDR(regSelfArg2, SP, 16)            // X22 = [SP+16]
		a.LDPpost(X30, regSelfArg, SP, 32)    // {X30, X19} = [SP]; SP += 32
	} else {
		a.LDPpost(X30, regSelfArg, SP, 16)    // {X30, X19} = [SP]; SP += 16
	}

	// Restore regRegs by subtracting the offset (avoids saving/restoring x26).
	if offset <= 4095 {
		a.SUBimm(regRegs, regRegs, uint16(offset))
	} else {
		// Use X1 as scratch since X0 holds the result.
		a.LoadImm64(X1, int64(offset))
		a.SUBreg(regRegs, regRegs, X1)
	}

	a.SUBimm(regSelfDepth, regSelfDepth, 1) // depth--

	// Store result to R(fnReg) in caller's register window as NaN-boxed IntValue.
	// X0 holds the raw int result from the callee.
	EmitBoxIntFast(a, X10, X0, regTagInt)
	a.STR(X10, regRegs, regValOffset(fnReg))

	// For variable-return self-calls (C=0), update ctx.Top so subsequent
	// B=0 CALL instructions know the arg range. Top = fnReg + 1.
	// Skip if the next consumer is a tail self-call (loads by position, not ctx.Top).
	if candidate.nResults < 0 && !candidate.skipTopUpdate {
		a.LoadImm64(X1, int64(fnReg+1))
		a.STR(X1, regCtx, ctxOffTop)
	}

	// Overflow handler deferred to cold section.
	getglobalPC := candidate.getglobalPC
	cg.deferCold(overflowLabel, func() {
		cg.asm.MOVreg(SP, X29)                       // unwind all self-call stack frames
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)      // restore original regRegs from context
		cg.asm.MOVimm16(regSelfDepth, 0)             // reset depth
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
	})

	return nil
}
