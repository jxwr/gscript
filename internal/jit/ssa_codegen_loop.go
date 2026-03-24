//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// emitSSAResumeDispatch emits the call-exit resume dispatch table.
// If this is a resume after a call-exit (ResumePC != 0), it skips pre-loop
// guards/loads and jumps directly to the resume point.
func (g *ssaCodegen) emitSSAResumeDispatch() {
	asm := g.asm
	// Collect call-exit PCs for the dispatch table.
	for i, inst := range g.f.Insts {
		if inst.Op == SSA_CALL {
			g.callExits = append(g.callExits, callExitInfo{ssaIdx: i, pc: inst.PC})
		}
	}

	if len(g.callExits) > 0 {
		asm.LDR(X0, g.trCtx, TraceCtxOffResumePC)
		asm.CBZ(X0, "normal_entry")
		// Clear ResumePC for next iteration
		asm.STR(XZR, g.trCtx, TraceCtxOffResumePC)
		// Dispatch to the correct resume point based on ResumePC value
		for _, ce := range g.callExits {
			resumePC := ce.pc + 1 // resume at the instruction after the CALL
			if resumePC < 4096 {
				asm.CMPimm(X0, uint16(resumePC))
			} else {
				asm.LoadImm64(X1, int64(resumePC))
				asm.CMPreg(X0, X1)
			}
			asm.BCond(CondEQ, fmt.Sprintf("resume_call_%d", ce.ssaIdx))
		}
		// Unknown resume PC → side exit
		asm.LoadImm64(X9, 0)
		asm.STR(X9, g.trCtx, TraceCtxOffExitPC)
		asm.LoadImm64(X0, 1)
		asm.B("epilogue")
		asm.Label("normal_entry")
	}
}

// emitSSAPreLoopGuards emits pre-loop type guards and non-guard instructions up to
// the SSA_LOOP marker. Sets g.loopIdx and g.wbrFloatSlots.
//
// Uses SSA use-def chains to eliminate unnecessary guards: a pre-loop GUARD_TYPE
// for a LOAD_SLOT ref is eliminated if that LOAD_SLOT (and its UNBOX) have zero
// users in the loop body. When a guard is eliminated, the slot is also removed
// from register allocation to prevent store-back corruption.
func (g *ssaCodegen) emitSSAPreLoopGuards() error {
	asm := g.asm

	// Identify write-before-read float slots for relaxed guard emission.
	g.wbrFloatSlots = findWBRFloatSlots(g.f)

	// SSA-level guard elimination using use-def chains.
	// Eliminates pre-loop guards for FORLOOP control slots (A, A+1, A+2) whose
	// pre-loop LOAD_SLOT values have zero loop-body users. These slots are always
	// int at trace entry (guaranteed by FORLOOP), and the loop body writes them
	// before reading (WBR). The guard, pre-loop load, and store-back are all skipped.
	g.deadGuardSlots = g.computeDeadGuardSlots()

	g.loopIdx = -1
	for i, inst := range g.f.Insts {
		if inst.Op == SSA_LOOP {
			g.loopIdx = i
			break
		}
		if inst.Op == SSA_GUARD_TYPE {
			loadInst := &g.f.Insts[inst.Arg1]
			slot := int(loadInst.Slot)
			if g.deadGuardSlots[slot] {
				// Guard eliminated — skip guard AND associated LOAD_SLOT/UNBOX.
				continue
			}
			// Emit NaN-boxing type guard
			if inst.AuxInt == int64(runtime.TypeFloat) && g.wbrFloatSlots[slot] {
				EmitGuardTypeRelaxedFloat(asm, regRegs, slot, "guard_fail")
			} else {
				EmitGuardType(asm, regRegs, slot, int(inst.AuxInt), "guard_fail")
			}
		} else if g.isDeadGuardDerived(&inst) {
			// Skip LOAD_SLOT and UNBOX for eliminated guards
			continue
		} else {
			emitSSAInstSlot(asm, g.f, SSARef(i), &inst, g.regMap, g.sm)
		}
	}

	if g.loopIdx < 0 {
		return fmt.Errorf("ssa codegen: no LOOP marker found")
	}
	return nil
}

// isDeadGuardDerived returns true if the instruction is a LOAD_SLOT or UNBOX
// for a dead-guard slot and should be skipped during code emission.
func (g *ssaCodegen) isDeadGuardDerived(inst *SSAInst) bool {
	if inst.Op == SSA_LOAD_SLOT {
		return g.deadGuardSlots[int(inst.Slot)]
	}
	if inst.Op == SSA_UNBOX_INT || inst.Op == SSA_UNBOX_FLOAT {
		if int(inst.Arg1) < len(g.f.Insts) {
			loadSlot := int(g.f.Insts[inst.Arg1].Slot)
			return g.deadGuardSlots[loadSlot]
		}
	}
	return false
}

// computeDeadGuardSlots uses the SSA use-def chain to find pre-loop guards
// whose guarded LOAD_SLOT values have no users in the loop body. These guards
// are unnecessary because the slot is overwritten before any read.
//
// Returns a set of slot numbers whose guards should be eliminated.
// Also removes these slots from the register map to prevent store-back corruption.
func (g *ssaCodegen) computeDeadGuardSlots() map[int]bool {
	result := make(map[int]bool)
	if g.ud == nil {
		return result
	}

	// Find loop marker
	loopIdx := -1
	for i, inst := range g.f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return result
	}

	// Scan loop body for slots accessed via memory (not SSA refs).
	// These slots are read by table operations (LOAD_FIELD, STORE_FIELD,
	// LOAD_ARRAY, STORE_ARRAY) through regs[slot*ValueSize], which the SSA
	// use-def chain does NOT track. Guards for these slots must be preserved.
	memAccessedSlots := make(map[int]bool)
	for i := loopIdx + 1; i < len(g.f.Insts); i++ {
		inst := &g.f.Insts[i]
		switch inst.Op {
		case SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_LOAD_ARRAY, SSA_STORE_ARRAY:
			// These ops read/write a table via the slot referenced by their
			// Arg1 SSA ref. If Arg1 points to a pre-loop LOAD_SLOT, the slot
			// is memory-accessed. Check what slot the table ref maps to.
			if int(inst.Arg1) < len(g.f.Insts) {
				tableInst := &g.f.Insts[inst.Arg1]
				if tableInst.Op == SSA_LOAD_SLOT {
					memAccessedSlots[int(tableInst.Slot)] = true
				}
			}
		}
	}

	for guardIdx := 0; guardIdx < loopIdx; guardIdx++ {
		inst := &g.f.Insts[guardIdx]
		if inst.Op != SSA_GUARD_TYPE {
			continue
		}

		loadRef := inst.Arg1
		if loadRef < 0 || int(loadRef) >= len(g.f.Insts) {
			continue
		}
		if g.f.Insts[loadRef].Op != SSA_LOAD_SLOT {
			continue
		}
		slot := int(g.f.Insts[loadRef].Slot)

		// Collect all pre-loop refs derived from loadRef
		derivedRefs := []SSARef{loadRef}
		for i := 0; i < loopIdx; i++ {
			ref := SSARef(i)
			if ref == loadRef || ref == SSARef(guardIdx) {
				continue
			}
			preInst := &g.f.Insts[i]
			if (preInst.Op == SSA_UNBOX_INT || preInst.Op == SSA_UNBOX_FLOAT) && preInst.Arg1 == loadRef {
				derivedRefs = append(derivedRefs, ref)
			}
		}

		// Check if any derived ref has users in the loop body
		hasLoopUser := false
		for _, dRef := range derivedRefs {
			for _, userRef := range g.ud.Users[dRef] {
				if int(userRef) > loopIdx {
					hasLoopUser = true
					break
				}
			}
			if hasLoopUser {
				break
			}
		}

		if !hasLoopUser {
			// This LOAD_SLOT has no loop-body users → the slot is written
			// before read (WBR) at the SSA level.
			//
			// Currently restricted to FORLOOP control slots (A, A+1, A+2)
			// which are guaranteed to hold int at trace entry. General
			// elimination for arbitrary slots requires tracking memory
			// accesses (LOAD_FIELD table bases) that the SSA use-def chain
			// doesn't capture. See: slot-reuse architecture issue.
			if !g.isForloopControlSlot(slot, loopIdx) {
				continue
			}
			if debugSSAGuardElim {
				fmt.Printf("[GUARD-ELIM] eliminate guard at %d for slot=%d (type=%d)\n",
					guardIdx, slot, inst.AuxInt)
			}
			result[slot] = true
		}
	}

	return result
}

// isForloopControlSlot returns true if the given slot is a FORLOOP control variable
// (A, A+1, or A+2) for ANY for-loop in the trace. These slots are guaranteed to hold
// int values at trace entry because the FORLOOP instruction writes them before the
// trace fires. Slot A+3 (external loop variable) is NOT included because the loop
// body may overwrite it with a different type.
func (g *ssaCodegen) isForloopControlSlot(slot int, loopIdx int) bool {
	if g.f.Trace == nil {
		return false
	}
	for _, ir := range g.f.Trace.IR {
		if ir.Op == vm.OP_FORLOOP {
			if slot == ir.A || slot == ir.A+1 || slot == ir.A+2 {
				return true
			}
		}
	}
	return false
}

// emitSSAPreLoopLoads loads allocated slots into registers, loads float slots into
// D registers, and hoists loop-body float constants. Sets g.hoistedConsts.
func (g *ssaCodegen) emitSSAPreLoopLoads() {
	asm := g.asm

	// Safety net: ensure all allocated slots are loaded into registers.
	// Some slots may not have been guarded/unboxed by the SSA builder
	// (e.g., OP_UNM operands in older SSA builds, or OP_MOVE targets).
	// Load any allocated slot that wasn't already populated.
	// For dead-guard slots, initialize the register to 0 instead of loading
	// from memory (which may contain a non-int value like a table pointer).
	loadedSlots := make(map[int]bool)
	for i := 0; i < g.loopIdx; i++ {
		inst := &g.f.Insts[i]
		if inst.Op == SSA_UNBOX_INT {
			if int(inst.Arg1) < len(g.f.Insts) {
				loadInst := &g.f.Insts[inst.Arg1]
				if loadInst.Op == SSA_LOAD_SLOT {
					loadedSlots[int(loadInst.Slot)] = true
				}
			}
		}
	}
	for slot, armReg := range g.regMap.Int.slotToReg {
		if !loadedSlots[slot] && !g.deadGuardSlots[slot] {
			asm.LDR(armReg, regRegs, slot*ValueSize)
			EmitUnboxInt(asm, armReg, armReg)
		}
	}

	// Load allocated float slots into D registers.
	// With ref-level allocation, pre-loop refs (UNBOX_FLOAT) may have different
	// D registers than loop-body refs for the same slot. Load each pre-loop ref
	// into its specific register.
	preLoopFloatLoaded := make(map[int]bool)
	for i := 0; i <= g.loopIdx; i++ {
		ref := SSARef(i)
		if dreg, ok := g.regMap.FloatRefReg(ref); ok {
			inst := &g.f.Insts[i]
			slot := int(inst.Slot)
			if slot >= 0 && !preLoopFloatLoaded[slot] && !g.deadGuardSlots[slot] {
				asm.FLDRd(dreg, regRegs, slot*ValueSize+OffsetData)
				preLoopFloatLoaded[slot] = true
			}
		}
	}
	// Slot-level fallback: load any allocated slot not yet loaded.
	// Skip write-before-read float slots — their values may be garbage
	// (bool/int from a previous iteration's side-exit path).
	for slot, dreg := range g.regMap.Float.slotToReg {
		if !preLoopFloatLoaded[slot] && !g.wbrFloatSlots[slot] {
			asm.FLDRd(dreg, regRegs, slot*ValueSize+OffsetData)
			preLoopFloatLoaded[slot] = true
		}
	}

	// Hoist loop-body constants that have ref-level D registers.
	// Their live ranges were extended to the entire loop body by the allocator,
	// so the register won't be reused. Loading once before the loop eliminates
	// per-iteration LoadImm64+FMOVtoFP sequences.
	g.hoistedConsts = make(map[SSARef]bool)
	for i := g.loopIdx + 1; i < len(g.f.Insts); i++ {
		inst := &g.f.Insts[i]
		if inst.Op == SSA_CONST_FLOAT {
			ref := SSARef(i)
			if dreg, ok := g.regMap.FloatRefReg(ref); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
				g.hoistedConsts[ref] = true
			}
		}
	}
}

// emitSSAPreLoopTableGuards hoists loop-invariant table guards and runs side-exit
// continuation analysis. Sets g.hoistedTables and g.sideExitInfo.
func (g *ssaCodegen) emitSSAPreLoopTableGuards() {
	// === Hoist loop-invariant table guards to pre-loop ===
	// For tables accessed in LOAD_ARRAY/STORE_ARRAY that are not modified in
	// the loop body, verify is-table + no-metatable + array-kind once before
	// the loop. The in-loop codegen then skips these checks (~5 instructions
	// saved per array access per iteration).
	g.hoistedTables = findLoopInvariantTableSlots(g.f, g.loopIdx, g.sm)
	if len(g.hoistedTables) > 0 {
		emitTableSlotGuards(g.asm, g.hoistedTables)
	}

	// === Side-exit continuation analysis ===
	// Detect inner loop structure for side-exit optimization.
	// When a float guard (escape check) fails inside the inner loop, instead of
	// going to the interpreter, we skip the post-inner-loop epilogue (GUARD_TRUTHY +
	// count++) and jump directly to the outer FORLOOP. This eliminates ~9-15
	// interpreter instructions per escaping pixel.
	g.sideExitInfo = analyzeSideExitContinuation(g.f, g.loopIdx)
}

// emitSSALoopBody emits the loop header label, float expression forwarding setup,
// and the loop body instruction emission (including inner loop control, call-exits,
// and side-exit continuations). Ends with the loop back-edge branch.
func (g *ssaCodegen) emitSSALoopBody() {
	asm := g.asm

	// === LOOP header ===
	asm.Label("trace_loop")

	// === Float expression forwarding analysis ===
	// For non-allocated float temps that are produced and immediately consumed
	// by the next instruction, we skip the memory write and keep the value
	// in a scratch D register. This eliminates ~20 memory ops per mandelbrot iteration.
	fwd := newFloatForwarder(g.f, g.regMap, g.sm, g.loopIdx)

	// Track whether we're currently inside the inner loop body.
	// innerLoopNum makes labels unique when multiple inner loops exist.
	// currentInnerNum tracks the number of the currently active inner loop.
	inInnerLoop := false
	innerLoopNum := 0
	currentInnerNum := 0

	// === Loop body ===
	for i := g.loopIdx + 1; i < len(g.f.Insts); i++ {
		inst := &g.f.Insts[i]
		ref := SSARef(i)

		// Skip hoisted constants — already loaded before the loop
		if g.hoistedConsts[ref] {
			continue
		}

		// Skip absorbed MULs — their computation is folded into FMADD/FMSUB
		if g.f.AbsorbedMuls[ref] {
			continue
		}

		// Emit skip_count label before the outer FORLOOP increment.
		// This is the target for inner_escape: skips GUARD_TRUTHY + count++.
		if g.sideExitInfo != nil && i == g.sideExitInfo.outerForLoopAddIdx {
			asm.Label("skip_count")
		}

		switch inst.Op {
		case SSA_LE_INT:
			if inst.AuxInt == 1 {
				// Inner loop exit check: branch back to inner_loop on LE,
				// fall through to inner_loop_done on GT.
				innerLabel := fmt.Sprintf("inner_loop_%d", currentInnerNum)
				innerDoneLabel := fmt.Sprintf("inner_loop_done_%d", currentInnerNum)
				arg1Reg := resolveSSARefSlot(asm, g.f, inst.Arg1, g.regMap, g.sm, X0)
				arg2Reg := resolveSSARefSlot(asm, g.f, inst.Arg2, g.regMap, g.sm, X1)
				asm.CMPreg(arg1Reg, arg2Reg)
				asm.BCond(CondLE, innerLabel)
				asm.Label(innerDoneLabel)

				// After inner loop exits, spill inner loop control registers
				// back to memory so the outer body can read them correctly.
				innerSlot := g.sm.getSlotForRef(inst.Arg1)
				if innerSlot >= 0 {
					for s := innerSlot; s <= innerSlot+3 && s < 256; s++ {
						if r, ok := g.regMap.IntReg(s); ok {
							off := s * ValueSize
							if off <= 32760 {
								EmitBoxInt(asm, X5, r, X6)
								asm.STR(X5, regRegs, off)
							}
						}
					}
				}
				inInnerLoop = false
				continue
			}
			if inst.AuxInt == 2 {
				// Inner loop entry check: if idx > limit, skip inner loop entirely.
				// currentInnerNum points to the next inner loop about to start
				// (SSA_INNER_LOOP follows shortly after this instruction).
				innerDoneLabel := fmt.Sprintf("inner_loop_done_%d", innerLoopNum)
				arg1Reg := resolveSSARefSlot(asm, g.f, inst.Arg1, g.regMap, g.sm, X0)
				arg2Reg := resolveSSARefSlot(asm, g.f, inst.Arg2, g.regMap, g.sm, X1)
				asm.CMPreg(arg1Reg, arg2Reg)
				asm.BCond(CondGT, innerDoneLabel)
				continue
			}
			// Outer loop exit check (AuxInt=0)
			arg1Reg := resolveSSARefSlot(asm, g.f, inst.Arg1, g.regMap, g.sm, X0)
			arg2Reg := resolveSSARefSlot(asm, g.f, inst.Arg2, g.regMap, g.sm, X1)
			asm.CMPreg(arg1Reg, arg2Reg)
			asm.BCond(CondGT, "loop_done")
			continue
		case SSA_LT_INT:
			arg1Reg := resolveSSARefSlot(asm, g.f, inst.Arg1, g.regMap, g.sm, X0)
			arg2Reg := resolveSSARefSlot(asm, g.f, inst.Arg2, g.regMap, g.sm, X1)
			asm.CMPreg(arg1Reg, arg2Reg)
			asm.BCond(CondGE, "loop_done")
			continue
		case SSA_INNER_LOOP:
			currentInnerNum = innerLoopNum
			innerLabel := fmt.Sprintf("inner_loop_%d", currentInnerNum)
			asm.Label(innerLabel)
			inInnerLoop = true
			innerLoopNum++
			continue
		}

		// Side-exit continuation: float guards inside the inner loop branch to
		// inner_escape instead of side_exit. This keeps escaped pixels in native
		// code instead of falling back to the interpreter.
		if inInnerLoop && g.sideExitInfo != nil && isFloatGuard(inst.Op) {
			emitFloatGuardWithTarget(asm, g.f, ref, inst, g.regMap, g.sm, fwd, "inner_escape")
			continue
		}

		if g.sideExitInfo != nil && g.sideExitInfo.guardTruthyIdx == i && g.sideExitInfo.countSlot >= 0 && inst.Op == SSA_GUARD_TRUTHY {
			emitGuardTruthyWithContinuation(asm, g.f, ref, inst, g.regMap, g.sm, "truthy_cont")
			continue
		}

		// SSA_CALL: call-exit — store all modified slots, set ExitPC, exit with ExitCode=3.
		// The Go executor handles the call, then re-enters the trace at the resume label.
		if inst.Op == SSA_CALL {
			g.emitSSACallExit(i, inst)
			continue
		}

		emitSSAInstSlotFwd(asm, g.f, ref, inst, g.regMap, g.sm, fwd, g.hoistedTables)
	}

	// Loop back-edge
	asm.B("trace_loop")
}

// emitSSACallExit emits the call-exit sequence for an SSA_CALL instruction:
// store-back, set ExitPC, exit with ExitCode=3, resume label, and register reload.
func (g *ssaCodegen) emitSSACallExit(i int, inst *SSAInst) {
	asm := g.asm

	// Determine the CALL result slot and its type (from the SSA_LOAD_SLOT that follows)
	callResultSlot := int(inst.Slot)
	callResultIsFloat := false
	if i+1 < len(g.f.Insts) && g.f.Insts[i+1].Op == SSA_LOAD_SLOT && int(g.f.Insts[i+1].Slot) == callResultSlot {
		callResultIsFloat = (g.f.Insts[i+1].Type == SSATypeFloat)
	}

	// 1. Store back all modified slots so the VM sees current values
	emitSlotStoreBack(asm, g.regMap, g.sm, g.liveInfo.WrittenSlots, g.floatRefSpill, g.deadGuardSlots)
	// 2. Set ExitPC = bytecode PC of the CALL instruction
	asm.LoadImm64(X9, int64(inst.PC))
	asm.STR(X9, g.trCtx, TraceCtxOffExitPC)
	// 3. Set ExitCode = 3 (call-exit)
	asm.LoadImm64(X0, 3)
	asm.B("epilogue")
	// 4. Resume label — Go executor sets ResumePC and re-enters JIT here
	asm.Label(fmt.Sprintf("resume_call_%d", i))
	// 5. Reload regRegs (regs may have been reallocated by VM during call)
	asm.LDR(regRegs, g.trCtx, TraceCtxOffRegs)
	// 6. Reload all allocated int registers from memory.
	// Skip the CALL result slot if it holds a float — EmitUnboxInt would
	// destroy the float value stored by the Go call handler.
	for slot, armReg := range g.regMap.Int.slotToReg {
		if callResultIsFloat && slot == callResultSlot {
			continue // float result; don't unbox as int
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.LDR(armReg, regRegs, off)
			EmitUnboxInt(asm, armReg, armReg)
		}
	}
	// 7. Reload all allocated float registers from memory
	for slot, freg := range g.regMap.Float.slotToReg {
		off := slot * ValueSize
		if off <= 32760 {
			asm.FLDRd(freg, regRegs, off+OffsetData)
		}
	}
	// 8. If the CALL result is float and was allocated to a float reg,
	// reload it from the NaN-boxed memory value (not offset by OffsetData,
	// since raw float64 bits ARE the NaN-boxed value).
	if callResultIsFloat {
		if fr, ok := g.regMap.Float.slotToReg[callResultSlot]; ok {
			off := callResultSlot * ValueSize
			if off <= 32760 {
				// Float value in NaN-boxing: the raw float64 bits are the value itself
				asm.FLDRd(fr, regRegs, off+OffsetData)
			}
		}
	}
}

// emitSSAColdPaths emits all cold code paths after the hot loop: loop_done trampoline,
// inner escape, side exit, guard fail, and loop-done handler with store-back.
func (g *ssaCodegen) emitSSAColdPaths() {
	asm := g.asm

	// === BOLT-style hot/cold code splitting ===
	// The hot loop (trace_loop → B trace_loop) should occupy as few cache lines
	// as possible. Cold code (side exits, guard failures, loop-done store-back,
	// epilogue) is grouped together AFTER the hot loop with minimal trampolines.
	// On Apple M4, L1 icache line = 64 bytes. A 26-instruction inner loop ≈ 104
	// bytes ≈ 2 cache lines. Keeping cold code out avoids polluting those lines.

	// loop_done trampoline: single branch keeps the hot-adjacent area tiny.
	asm.Label("loop_done")
	asm.B("loop_done_handler")

	// === Cold section: all infrequently-executed code grouped together ===

	// --- Inner escape (float guard failure inside inner loop) ---
	// Instead of side-exiting to interpreter, spill inner loop state and skip
	// the post-inner-loop epilogue (GUARD_TRUTHY + count++), jumping directly
	// to the outer FORLOOP increment. Saves ~9-15 interpreter instructions per
	// escaping pixel (~40% of all pixels in mandelbrot).
	if g.sideExitInfo != nil {
		asm.Label("inner_escape")
		// Spill inner loop control registers to memory (same as inner_loop_done)
		for s := g.sideExitInfo.innerLoopSlot; s <= g.sideExitInfo.innerLoopSlot+3 && s < 256; s++ {
			if r, ok := g.regMap.IntReg(s); ok {
				off := s * ValueSize
				if off <= 32760 {
					EmitBoxInt(asm, X5, r, X6)
					asm.STR(X5, regRegs, off)
				}
			}
		}
		asm.B("skip_count")

		// --- GUARD_TRUTHY continuation (non-escaping pixel) ---
		// When escaped=false (inner loop completed without escape):
		// Execute count++ inline and continue the outer FORLOOP.
		// The count variable is at sideExitInfo.countSlot (e.g., R(1)).
		if g.sideExitInfo.countSlot >= 0 {
			asm.Label("truthy_cont")
			countOff := g.sideExitInfo.countSlot * ValueSize
			// Load count from memory or register
			if r, ok := g.regMap.IntReg(g.sideExitInfo.countSlot); ok {
				// Count is in a register — add 1 directly
				asm.ADDimm(r, r, 1)
				// Store back to memory as NaN-boxed int
				EmitBoxInt(asm, X5, r, X6)
				asm.STR(X5, regRegs, countOff)
			} else {
				// Count is in memory (NaN-boxed) — load, unbox, increment, rebox, store
				asm.LDR(X0, regRegs, countOff)
				EmitUnboxInt(asm, X0, X0)
				asm.ADDimm(X0, X0, 1)
				EmitBoxIntFast(asm, X5, X0, regTagInt)
				asm.STR(X5, regRegs, countOff)
			}
			asm.B("skip_count")
		}
	}

	// --- Side exit (guard failure during loop body) ---
	asm.Label("side_exit")
	emitSlotStoreBack(asm, g.regMap, g.sm, g.liveInfo.WrittenSlots, g.floatRefSpill, g.deadGuardSlots)
	asm.STR(X9, X19, 16) // ctx.ExitPC = X9
	asm.LoadImm64(X0, 1) // ExitCode = 1
	asm.B("epilogue")

	// --- Guard fail (pre-loop type mismatch) ---
	// ExitCode=2: "not executed" — interpreter should run the body normally.
	// No store-back needed since we haven't modified any registers.
	// X8 holds the index of the failing guard (set before each guard check).
	asm.Label("guard_fail")
	asm.LoadImm64(X0, 2) // ExitCode = 2 (guard fail, not executed)
	asm.B("epilogue")

	// --- Loop done handler (normal loop completion) ---
	asm.Label("loop_done_handler")
	emitSlotStoreBack(asm, g.regMap, g.sm, g.liveInfo.WrittenSlots, g.floatRefSpill, g.deadGuardSlots)
	asm.LoadImm64(X0, 0) // ExitCode = 0
}
