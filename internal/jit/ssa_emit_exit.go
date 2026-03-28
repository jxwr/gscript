//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// Store-back: write all register values to memory before loop back-edge
// ────────────────────────────────────────────────────────────────────────────

// emitStoreBack writes all allocated register values back to memory.
// If typeSafe is true, only writes to slots whose memory value has a matching type.
// This prevents call-exit results (e.g., tables, booleans) from being overwritten
// by stale register values of a different type.
func (ec *emitCtx) emitStoreBack() {
	ec.emitStoreBackImpl(false)
}

func (ec *emitCtx) emitStoreBackTypeSafe() {
	ec.emitStoreBackImpl(true)
}

func (ec *emitCtx) emitStoreBackImpl(typeSafe bool) {
	asm := ec.asm
	_ = typeSafe // used for call-exit slot skipping

	// Store all allocated integer registers back to memory (NaN-boxed).
	// Skip call-exit output slots (interpreter's value is authoritative).
	// Skip float-written slots: a float operation was the last writer, so the
	// int GPR holds a stale value. The correct float value is either in an FPR
	// (handled by float store-back below) or already in memory.
	// Skip callee temporaries: slots above maxDepth0Slot belong to inlined
	// function bodies and must not be stored back (they'd corrupt the caller's state).
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			if ec.callExitWriteSlots[slot] {
				continue
			}
			if ec.floatWrittenSlots[slot] {
				continue
			}
			if ec.maxDepth0Slot > 0 && slot > ec.maxDepth0Slot {
				continue // callee temporary — don't store back
			}
			EmitBoxIntFast(asm, X0, reg, regTagInt)
			asm.STR(X0, regRegs, slot*ValueSize)
		}
	}

	// Store float registers back to memory.
	// We must store all float values that were written in the loop body.
	// Use floatSlotReg to get the FPR that has the current value for each slot.
	// Then ensure the value is also in the slot-level FPR (if allocated) for consistency with reload.
	for slot, currentFPR := range ec.floatSlotReg {
		if ec.callExitWriteSlots[slot] {
			continue
		}
		if ec.maxDepth0Slot > 0 && slot > ec.maxDepth0Slot {
			continue // callee temporary — don't store back
		}
		// If there's a slot-level FPR allocation and it differs from currentFPR,
		// move the value to the slot-level FPR first
		if ec.regMap.Float != nil {
			if slotFPR, ok := ec.regMap.Float.getReg(slot); ok && slotFPR != currentFPR {
				asm.FMOVd(slotFPR, currentFPR)
				currentFPR = slotFPR // now store from slot-level FPR
			}
		}
		asm.FSTRd(currentFPR, regRegs, slot*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Reload all registers from memory (after call-exit resume)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitReloadAll() {
	asm := ec.asm
	seq := ec.reloadSeq
	ec.reloadSeq++

	// Reload integer registers with type-safe unboxing.
	// After a call-exit, slots may contain values of unexpected types (e.g., a bool
	// in a slot that the register allocator assigned an int GPR). We must verify the
	// NaN-box tag is actually int (0xFFFE) before unboxing; otherwise, skip the reload
	// and leave the register as-is (the code path will read from memory if needed).
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			skipLabel := "reload_skip_int_" + itoa(seq) + "_" + itoa(slot)
			asm.LDR(reg, regRegs, slot*ValueSize)
			// Check if this is actually an integer (top 16 bits == 0xFFFE)
			asm.LSRimm(X0, reg, 48)
			asm.MOVimm16(X1, NB_TagIntShr48)
			asm.CMPreg(X0, X1)
			asm.BCond(CondNE, skipLabel) // not int → skip unbox, register holds raw NaN-boxed value
			EmitUnboxInt(asm, reg, reg)
			asm.Label(skipLabel)
		}
	}

	// Reload ALL float registers from their slot's memory.
	// We must reload every FPR that the loop body might read, including both
	// slot-level and ref-level allocations. A given slot may have both a
	// slot-level FPR and one or more ref-level FPRs (possibly different registers).
	// All must be loaded from the same memory slot.
	reloadedFPR := make(map[FReg]bool)
	if ec.regMap.Float != nil {
		for slot, freg := range ec.regMap.Float.slotToReg {
			asm.FLDRd(freg, regRegs, slot*ValueSize)
			reloadedFPR[freg] = true
		}
	}
	if ec.regMap.FloatRef != nil {
		for ref, freg := range ec.regMap.FloatRef.refToReg {
			if reloadedFPR[freg] {
				continue
			}
			if int(ref) >= len(ec.f.Insts) {
				continue
			}
			inst := &ec.f.Insts[ref]
			slot := int(inst.Slot)
			if slot < 0 {
				continue
			}
			asm.FLDRd(freg, regRegs, slot*ValueSize)
			reloadedFPR[freg] = true
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Inner loop store-back and reload
// ────────────────────────────────────────────────────────────────────────────

// emitInnerLoopStoreBack stores the latest float register values back to memory.
// Called before the inner loop backward branch so that the next iteration
// reads updated values from memory.
func (ec *emitCtx) emitInnerLoopStoreBack() {
	// Reuse the same store-back logic (latest ref per slot)
	ec.emitStoreBack()
}

// emitInnerLoopReload reloads all float registers from memory.
// Called at the inner_loop_body label start so that stale ref-based register
// values are overwritten with the correct values from the previous iteration.
func (ec *emitCtx) emitInnerLoopReload() {
	ec.emitReloadAll()
}

// ────────────────────────────────────────────────────────────────────────────
// Cold paths
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitSideExit() {
	asm := ec.asm

	asm.Label("side_exit_setup")
	// Store back all register values to memory before exiting.
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// X9 was set by the guard instruction that branched here.
	// It holds the correct ExitPC (the bytecode PC of the failing guard).
	// DO NOT overwrite X9 — just store it.
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Save ExitState: GPR registers
	if ec.regMap.Int != nil {
		off := TraceCtxOffExitGPR
		for i, gpr := range allocableGPR {
			if i >= 4 {
				break // ExitGPR only has 4 slots
			}
			asm.STR(gpr, regCtx, off+i*8)
		}
	}

	// Save ExitState: FPR registers
	asm.FSTP(D4, D5, regCtx, TraceCtxOffExitFPR)
	asm.FSTP(D6, D7, regCtx, TraceCtxOffExitFPR+16)
	asm.FSTP(D8, D9, regCtx, TraceCtxOffExitFPR+32)
	asm.FSTP(D10, D11, regCtx, TraceCtxOffExitFPR+48)

	// Set ExitCode = 1 (side exit)
	asm.LoadImm64(X0, 1)
	asm.B("epilogue")
}

// emitBreakExit emits the break-exit path for inner loop break guards.
// Like side_exit_setup but exits AFTER the FORLOOP (loopPC + 1) so the VM
// skips past the inner loop, simulating a break statement.
func (ec *emitCtx) emitBreakExit() {
	asm := ec.asm
	asm.Label("break_exit")

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Set ExitPC to the break guard's PC so the VM re-executes the comparison.
	// The VM will evaluate the LT/LE, take the "escape" branch, execute
	// any break body (e.g., escaped=true), and then break out of the loop.
	asm.LoadImm64(X9, int64(ec.breakGuardPC))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Save ExitState
	if ec.regMap.Int != nil {
		off := TraceCtxOffExitGPR
		for i, gpr := range allocableGPR {
			if i >= 4 {
				break
			}
			asm.STR(gpr, regCtx, off+i*8)
		}
	}
	asm.FSTP(D4, D5, regCtx, TraceCtxOffExitFPR)
	asm.FSTP(D6, D7, regCtx, TraceCtxOffExitFPR+16)
	asm.FSTP(D8, D9, regCtx, TraceCtxOffExitFPR+32)
	asm.FSTP(D10, D11, regCtx, TraceCtxOffExitFPR+48)

	// Set ExitCode = 4 (break exit) so VM resumes at ExitPC.
	// Break exits are expected behavior (e.g., mandelbrot escape check)
	// and should NOT count toward side-exit blacklisting.
	asm.LoadImm64(X0, 4)
	asm.B("epilogue")
}

func (ec *emitCtx) emitLoopDone() {
	asm := ec.asm
	asm.Label("loop_done")

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Set ExitPC to the FORLOOP PC + 1 (instruction after the loop)
	loopPC := 0
	if ec.f.Trace != nil {
		loopPC = ec.f.Trace.LoopPC
	}
	asm.LoadImm64(X9, int64(loopPC+1))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Set ExitCode = 0 (loop done)
	asm.LoadImm64(X0, 0)
	asm.B("epilogue")
}

func (ec *emitCtx) emitGuardFail() {
	asm := ec.asm
	asm.Label("guard_fail")

	// Set ExitCode = 2 (guard fail — pre-loop type mismatch)
	asm.LoadImm64(X0, 2)
	asm.B("epilogue")
}

func (ec *emitCtx) emitGuardFailCommon(bailoutID int) {
	asm := ec.asm
	// Common guard fail handler - store bailout ID in ExitPC for deopt handler
	asm.Label("guard_fail_common")

	// Set ExitCode = 2 (guard fail — pre-loop type mismatch)
	asm.LoadImm64(X0, 2)

	// Store bailout ID in ExitPC (reused field for bailout info)
	// The deopt handler can look up bailout details from DeoptMetadata
	asm.LoadImm64(X9, int64(bailoutID))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	asm.B("epilogue")
}

func (ec *emitCtx) emitMaxIterExit() {
	asm := ec.asm
	asm.Label("max_iter_exit")

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Set ExitCode = 5 (max iterations reached for debugging)
	asm.LoadImm64(X0, 5)
	asm.B("epilogue")
}
