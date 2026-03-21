//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// SSA Codegen: compiles SSAFunc to ARM64 native code.
//
// Key design: slot-based register allocation with unboxed integers.
//
// The SSA IR represents one loop iteration. The native code loops by branching
// back to the loop header. For this to work, loop-carried values must persist
// across iterations in the same ARM64 registers.
//
// Solution: allocate ARM64 registers by VM slot (not by SSA ref).
// All SSA operations that read/write the same VM slot use the same register.
// This ensures values carry over correctly across loop back-edges.
//
// Register layout:
//   X19 = trace context pointer
//   X20-X24 = allocated VM slots (up to 5 hot slots)
//   X25 = (unused in SSA codegen)
//   X26 = regRegs (pointer to vm.regs[base])
//   X27 = regConsts (pointer to trace constants)
//   X0-X9 = scratch registers

// slotAlloc maps VM register slots to ARM64 physical registers.
type slotAlloc struct {
	// VM slot → ARM64 register
	slotToReg map[int]Reg
	// ARM64 register → VM slot
	regToSlot map[Reg]int
}

// floatSlotAlloc maps hot VM float slots to ARM64 SIMD D registers.
// D4-D7 are used (caller-saved on ARM64 ABI, no save/restore needed).
type floatSlotAlloc struct {
	slotToReg map[int]FReg
	regToSlot map[FReg]int
}

// D4-D7: caller-saved (no save needed). D8-D11: callee-saved (saved in prologue).
var allocableFloatRegs = []FReg{D4, D5, D6, D7, D8, D9, D10, D11}

const maxAllocFloatRegs = 8

func newFloatSlotAlloc(f *SSAFunc) *floatSlotAlloc {
	fa := &floatSlotAlloc{
		slotToReg: make(map[int]FReg),
		regToSlot: make(map[FReg]int),
	}
	freq := make(map[int]int)
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			if ir.AType == runtime.TypeFloat {
				freq[ir.A]++
			}
			if ir.BType == runtime.TypeFloat && ir.B < 256 {
				freq[ir.B]++
			}
			if ir.CType == runtime.TypeFloat && ir.C < 256 {
				freq[ir.C]++
			}
		}
	}
	type sf struct{ slot, count int }
	var candidates []sf
	for slot, count := range freq {
		candidates = append(candidates, sf{slot, count})
	}
	for i := 0; i < len(candidates) && i < maxAllocFloatRegs; i++ {
		maxIdx := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].count > candidates[maxIdx].count {
				maxIdx = j
			}
		}
		if i != maxIdx {
			candidates[i], candidates[maxIdx] = candidates[maxIdx], candidates[i]
		}
	}
	for i := 0; i < len(candidates) && i < maxAllocFloatRegs; i++ {
		if candidates[i].count < 2 {
			break
		}
		fa.slotToReg[candidates[i].slot] = allocableFloatRegs[i]
		fa.regToSlot[allocableFloatRegs[i]] = candidates[i].slot
	}
	return fa
}

func (fa *floatSlotAlloc) getReg(slot int) (FReg, bool) {
	r, ok := fa.slotToReg[slot]
	return r, ok
}

// newSlotAlloc performs frequency-based slot allocation on the SSA function.
// It identifies the hottest VM slots and assigns them to X20-X24.
func newSlotAlloc(f *SSAFunc) *slotAlloc {
	sa := &slotAlloc{
		slotToReg: make(map[int]Reg),
		regToSlot: make(map[Reg]int),
	}

	// Build slot usage frequency from the trace IR — ONLY for integer ops.
	// Float slots use SIMD D registers (not X registers) and must NOT be allocated.
	// Table/string slots must NOT be allocated either.
	floatSlots := make(map[int]bool)
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			if ir.AType == runtime.TypeFloat {
				floatSlots[ir.A] = true
			}
			if ir.BType == runtime.TypeFloat && ir.B < 256 {
				floatSlots[ir.B] = true
			}
			if ir.CType == runtime.TypeFloat && ir.C < 256 {
				floatSlots[ir.C] = true
			}
		}
	}
	freq := make(map[int]int)
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			switch ir.Op {
			case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_UNM,
				vm.OP_LOADINT, vm.OP_MOVE,
				vm.OP_EQ, vm.OP_LT, vm.OP_LE:
				if ir.B < 256 && !floatSlots[ir.B] {
					freq[ir.B]++
				}
				if ir.C < 256 && !floatSlots[ir.C] {
					freq[ir.C]++
				}
				if !floatSlots[ir.A] {
					freq[ir.A]++
				}
			case vm.OP_FORLOOP:
				freq[ir.A] += 3   // idx
				freq[ir.A+1] += 3 // limit
				freq[ir.A+2] += 3 // step
				freq[ir.A+3] += 3 // loop var
			case vm.OP_FORPREP:
				freq[ir.A]++
			}
		}
	}

	// Also count from SSA instructions (for traces without IR)
	// Skip float slots — they use SIMD registers, not X registers.
	slotRefs := buildSSASlotRefs(f)
	for slot := range slotRefs {
		if !floatSlots[slot] {
			freq[slot]++
		}
	}

	// Find top N most-used slots
	type slotFreq struct {
		slot  int
		count int
	}
	var candidates []slotFreq
	for slot, count := range freq {
		candidates = append(candidates, slotFreq{slot, count})
	}

	// Selection sort for top N
	for i := 0; i < len(candidates) && i < maxAllocRegs; i++ {
		maxIdx := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].count > candidates[maxIdx].count {
				maxIdx = j
			}
		}
		if i != maxIdx {
			candidates[i], candidates[maxIdx] = candidates[maxIdx], candidates[i]
		}
	}

	// Assign physical registers
	for i := 0; i < len(candidates) && i < maxAllocRegs; i++ {
		if candidates[i].count < 1 {
			break
		}
		slot := candidates[i].slot
		armReg := allocableRegs[i]
		sa.slotToReg[slot] = armReg
		sa.regToSlot[armReg] = slot
	}

	return sa
}

// getReg returns the ARM64 register for a VM slot, or (0, false) if not allocated.
func (sa *slotAlloc) getReg(slot int) (Reg, bool) {
	r, ok := sa.slotToReg[slot]
	return r, ok
}

// buildSSASlotRefs finds which SSA refs correspond to which VM slots.
func buildSSASlotRefs(f *SSAFunc) map[int][]SSARef {
	result := make(map[int][]SSARef)
	for i, inst := range f.Insts {
		ref := SSARef(i)
		switch inst.Op {
		case SSA_LOAD_SLOT:
			result[int(inst.Slot)] = append(result[int(inst.Slot)], ref)
		case SSA_UNBOX_INT:
			if int(inst.Arg1) < len(f.Insts) {
				loadInst := &f.Insts[inst.Arg1]
				if loadInst.Op == SSA_LOAD_SLOT {
					slot := int(loadInst.Slot)
					result[slot] = append(result[slot], ref)
				}
			}
		}
	}
	return result
}

// ssaSlotMapper maps SSA instruction indices (refs) to VM register slots.
// Uses the Slot field embedded in each SSAInst by the SSA builder.
type ssaSlotMapper struct {
	// refToSlot: SSA ref → VM slot (the slot this ref's value corresponds to)
	refToSlot map[SSARef]int
	// slotToLatestRef: VM slot → latest SSA ref that defines this slot
	slotToLatestRef map[int]SSARef
	// forloopSlots: maps FORLOOP idx slot → also writes to A+3 (loop variable)
	forloopA3 map[int]int // slot A → slot A+3
}

func newSSASlotMapper(f *SSAFunc) *ssaSlotMapper {
	m := &ssaSlotMapper{
		refToSlot:       make(map[SSARef]int),
		slotToLatestRef: make(map[int]SSARef),
		forloopA3:       make(map[int]int),
	}

	// Map every instruction with a Slot field to its slot
	for i, inst := range f.Insts {
		ref := SSARef(i)
		switch inst.Op {
		case SSA_LOAD_SLOT, SSA_UNBOX_INT, SSA_UNBOX_FLOAT, SSA_STORE_SLOT, SSA_BOX_INT, SSA_BOX_FLOAT:
			slot := int(inst.Slot)
			m.refToSlot[ref] = slot
			m.slotToLatestRef[slot] = ref
		case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_MOVE:
			slot := int(inst.Slot)
			if slot >= 0 {
				m.refToSlot[ref] = slot
				m.slotToLatestRef[slot] = ref
			}
		}
	}

	// Detect FORLOOP pattern: ADD_INT followed by LE_INT
	// The ADD_INT also writes to slot A+3 (loop variable copy)
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			if ir.Op == vm.OP_FORLOOP {
				m.forloopA3[ir.A] = ir.A + 3
				// Also update slotToLatestRef for A+3 to point to
				// the same ref as slot A
				if ref, ok := m.slotToLatestRef[ir.A]; ok {
					m.slotToLatestRef[ir.A+3] = ref
				}
			}
		}
	}

	return m
}

// getSlotForRef returns the VM slot for an SSA ref, or -1 if unknown.
func (m *ssaSlotMapper) getSlotForRef(ref SSARef) int {
	if slot, ok := m.refToSlot[ref]; ok {
		return slot
	}
	return -1
}

// CompileSSA compiles an SSAFunc to native ARM64 code, producing a CompiledTrace.
//
// Pipeline:
//   1. Analysis passes: register allocation, liveness analysis, use-def chains
//   2. Emit ARM64 code using the analysis results
func CompileSSA(f *SSAFunc) (*CompiledTrace, error) {
	if f == nil || len(f.Insts) == 0 {
		return nil, fmt.Errorf("ssa codegen: empty SSA function")
	}

	if !ssaIsIntegerOnly(f) {
		return nil, fmt.Errorf("ssa codegen: trace contains non-integer ops")
	}

	// Phase 1: Analysis passes
	ud := BuildUseDef(f)
	regMap := AllocateRegisters(f)
	liveInfo := AnalyzeLiveness(f)

	// Phase 2: Emit ARM64
	_ = ud // reserved for future optimization passes
	return emitSSA(f, regMap, liveInfo)
}

// emitSSA emits ARM64 machine code for an SSAFunc using pre-computed analysis results.
func emitSSA(f *SSAFunc, regMap *RegMap, liveInfo *LiveInfo) (*CompiledTrace, error) {
	asm := NewAssembler()
	sm := newSSASlotMapper(f)
	// === Prologue ===
	asm.STPpre(X29, X30, SP, -128)
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)
	// Save callee-saved SIMD registers D8-D11
	asm.FSTP(D8, D9, SP, 96)
	asm.FSTP(D10, D11, SP, 112)

	trCtx := X19
	asm.MOVreg(trCtx, X0)

	asm.LDR(regRegs, trCtx, 0)
	asm.LDR(regConsts, trCtx, 8)

	// === Pre-LOOP: guards + initial loads ===
	// Pre-loop guards branch to "guard_fail" (ExitCode=2) instead of "side_exit".
	// This tells the VM "trace not executed" so the interpreter runs the body normally.

	// Identify write-before-read float slots for relaxed guard emission.
	// These slots may hold non-float types at trace entry (e.g., LOADBOOL on
	// mandelbrot escape path), but the value is overwritten before first read.
	wbrFloatSlots := findWBRFloatSlots(f)

	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
		if inst.Op == SSA_GUARD_TYPE {
			// Emit NaN-boxing type guard
			loadInst := &f.Insts[inst.Arg1]
			slot := int(loadInst.Slot)
			if inst.AuxInt == int64(runtime.TypeFloat) && wbrFloatSlots[slot] {
				// Relaxed guard for write-before-read float slots
				EmitGuardTypeRelaxedFloat(asm, regRegs, slot, "guard_fail")
			} else {
				EmitGuardType(asm, regRegs, slot, int(inst.AuxInt), "guard_fail")
			}
		} else {
			emitSSAInstSlot(asm, f, SSARef(i), &inst, regMap, sm)
		}
	}

	if loopIdx < 0 {
		return nil, fmt.Errorf("ssa codegen: no LOOP marker found")
	}

	// Safety net: ensure all allocated slots are loaded into registers.
	// Some slots may not have been guarded/unboxed by the SSA builder
	// (e.g., OP_UNM operands in older SSA builds, or OP_MOVE targets).
	// Load any allocated slot that wasn't already populated.
	loadedSlots := make(map[int]bool)
	for i := 0; i < loopIdx; i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_UNBOX_INT {
			if int(inst.Arg1) < len(f.Insts) {
				loadInst := &f.Insts[inst.Arg1]
				if loadInst.Op == SSA_LOAD_SLOT {
					loadedSlots[int(loadInst.Slot)] = true
				}
			}
		}
	}
	for slot, armReg := range regMap.Int.slotToReg {
		if !loadedSlots[slot] {
			asm.LDR(armReg, regRegs, slot*ValueSize)
			EmitUnboxInt(asm, armReg, armReg)
		}
	}

	// Load allocated float slots into D registers.
	// With ref-level allocation, pre-loop refs (UNBOX_FLOAT) may have different
	// D registers than loop-body refs for the same slot. Load each pre-loop ref
	// into its specific register.
	preLoopFloatLoaded := make(map[int]bool)
	for i := 0; i <= loopIdx; i++ {
		ref := SSARef(i)
		if dreg, ok := regMap.FloatRefReg(ref); ok {
			inst := &f.Insts[i]
			slot := int(inst.Slot)
			if slot >= 0 && !preLoopFloatLoaded[slot] {
				asm.FLDRd(dreg, regRegs, slot*ValueSize+OffsetData)
				preLoopFloatLoaded[slot] = true
			}
		}
	}
	// Slot-level fallback: load any allocated slot not yet loaded.
	// Skip write-before-read float slots — their values may be garbage
	// (bool/int from a previous iteration's side-exit path).
	for slot, dreg := range regMap.Float.slotToReg {
		if !preLoopFloatLoaded[slot] && !wbrFloatSlots[slot] {
			asm.FLDRd(dreg, regRegs, slot*ValueSize+OffsetData)
			preLoopFloatLoaded[slot] = true
		}
	}

	// Hoist loop-body constants that have ref-level D registers.
	// Their live ranges were extended to the entire loop body by the allocator,
	// so the register won't be reused. Loading once before the loop eliminates
	// per-iteration LoadImm64+FMOVtoFP sequences.
	hoistedConsts := make(map[SSARef]bool)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_CONST_FLOAT {
			ref := SSARef(i)
			if dreg, ok := regMap.FloatRefReg(ref); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
				hoistedConsts[ref] = true
			}
		}
	}

	// === Side-exit continuation analysis ===
	// Detect inner loop structure for side-exit optimization.
	// When a float guard (escape check) fails inside the inner loop, instead of
	// going to the interpreter, we skip the post-inner-loop epilogue (GUARD_TRUTHY +
	// count++) and jump directly to the outer FORLOOP. This eliminates ~9-15
	// interpreter instructions per escaping pixel.
	sideExitInfo := analyzeSideExitContinuation(f, loopIdx)
	_ = sideExitInfo // used below in loop body emission

	// === LOOP header ===
	asm.Label("trace_loop")

	// === Float expression forwarding analysis ===
	// For non-allocated float temps that are produced and immediately consumed
	// by the next instruction, we skip the memory write and keep the value
	// in a scratch D register. This eliminates ~20 memory ops per mandelbrot iteration.
	fwd := newFloatForwarder(f, regMap, sm, loopIdx)

	// Track whether we're currently inside the inner loop body.
	// innerLoopNum makes labels unique when multiple inner loops exist.
	// currentInnerNum tracks the number of the currently active inner loop.
	inInnerLoop := false
	innerLoopNum := 0
	currentInnerNum := 0

	// === Loop body ===
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)

		// Skip hoisted constants — already loaded before the loop
		if hoistedConsts[ref] {
			continue
		}

		// Skip absorbed MULs — their computation is folded into FMADD/FMSUB
		if f.AbsorbedMuls[ref] {
			continue
		}

		// Emit skip_count label before the outer FORLOOP increment.
		// This is the target for inner_escape: skips GUARD_TRUTHY + count++.
		if sideExitInfo != nil && i == sideExitInfo.outerForLoopAddIdx {
			asm.Label("skip_count")
		}

		switch inst.Op {
		case SSA_LE_INT:
			if inst.AuxInt == 1 {
				// Inner loop exit check: branch back to inner_loop on LE,
				// fall through to inner_loop_done on GT.
				innerLabel := fmt.Sprintf("inner_loop_%d", currentInnerNum)
				innerDoneLabel := fmt.Sprintf("inner_loop_done_%d", currentInnerNum)
				arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
				arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
				asm.CMPreg(arg1Reg, arg2Reg)
				asm.BCond(CondLE, innerLabel)
				asm.Label(innerDoneLabel)

				// After inner loop exits, spill inner loop control registers
				// back to memory so the outer body can read them correctly.
				innerSlot := sm.getSlotForRef(inst.Arg1)
				if innerSlot >= 0 {
					for s := innerSlot; s <= innerSlot+3 && s < 256; s++ {
						if r, ok := regMap.IntReg(s); ok {
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
				arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
				arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
				asm.CMPreg(arg1Reg, arg2Reg)
				asm.BCond(CondGT, innerDoneLabel)
				continue
			}
			// Outer loop exit check (AuxInt=0)
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
			asm.CMPreg(arg1Reg, arg2Reg)
			asm.BCond(CondGT, "loop_done")
			continue
		case SSA_LT_INT:
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
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
		if inInnerLoop && sideExitInfo != nil && isFloatGuard(inst.Op) {
			emitFloatGuardWithTarget(asm, f, ref, inst, regMap, sm, fwd, "inner_escape")
			continue
		}

		if sideExitInfo != nil && sideExitInfo.guardTruthyIdx == i && sideExitInfo.countSlot >= 0 && inst.Op == SSA_GUARD_TRUTHY {
			emitGuardTruthyWithContinuation(asm, f, ref, inst, regMap, sm, "truthy_cont")
			continue
		}

		emitSSAInstSlotFwd(asm, f, ref, inst, regMap, sm, fwd)
	}

	// Loop back-edge
	asm.B("trace_loop")

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
	if sideExitInfo != nil {
		asm.Label("inner_escape")
		// Spill inner loop control registers to memory (same as inner_loop_done)
		for s := sideExitInfo.innerLoopSlot; s <= sideExitInfo.innerLoopSlot+3 && s < 256; s++ {
			if r, ok := regMap.IntReg(s); ok {
				off := s * ValueSize
				if off <= 32760 {
					asm.STR(r, regRegs, off+OffsetData)
					asm.MOVimm16(X5, TypeInt)
					asm.STRB(X5, regRegs, off+OffsetTyp)
				}
			}
		}
		asm.B("skip_count")

		// --- GUARD_TRUTHY continuation (non-escaping pixel) ---
		// When escaped=false (inner loop completed without escape):
		// Execute count++ inline and continue the outer FORLOOP.
		// The count variable is at sideExitInfo.countSlot (e.g., R(1)).
		if sideExitInfo.countSlot >= 0 {
			asm.Label("truthy_cont")
			countOff := sideExitInfo.countSlot * ValueSize
			// Load count from memory or register
			if r, ok := regMap.IntReg(sideExitInfo.countSlot); ok {
				// Count is in a register — add 1 directly
				asm.ADDimm(r, r, 1)
				// Store back to memory for consistency
				asm.STR(r, regRegs, countOff+OffsetData)
			} else {
				// Count is in memory — load, increment, store
				asm.LDR(X0, regRegs, countOff+OffsetData)
				asm.ADDimm(X0, X0, 1)
				asm.STR(X0, regRegs, countOff+OffsetData)
			}
			asm.B("skip_count")
		}
	}

	// --- Side exit (guard failure during loop body) ---
	asm.Label("side_exit")
	emitSlotStoreBack(asm, regMap, sm, liveInfo.WrittenSlots, liveInfo)
	asm.STR(X9, X19, 16)  // ctx.ExitPC = X9
	asm.LoadImm64(X0, 1)  // ExitCode = 1
	asm.B("epilogue")

	// --- Guard fail (pre-loop type mismatch) ---
	// ExitCode=2: "not executed" — interpreter should run the body normally.
	// No store-back needed since we haven't modified any registers.
	// X8 holds the index of the failing guard (set before each guard check).
	asm.Label("guard_fail")
	asm.LoadImm64(X0, 2)  // ExitCode = 2 (guard fail, not executed)
	asm.B("epilogue")

	// --- Loop done handler (normal loop completion) ---
	asm.Label("loop_done_handler")
	emitSlotStoreBack(asm, regMap, sm, liveInfo.WrittenSlots, liveInfo)
	asm.LoadImm64(X0, 0)  // ExitCode = 0

	// === Epilogue ===
	asm.Label("epilogue")
	asm.STR(X0, X19, 24)  // ctx.ExitCode

	// Restore callee-saved SIMD registers
	asm.FLDP(D8, D9, SP, 96)
	asm.FLDP(D10, D11, SP, 112)
	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	asm.LDPpost(X29, X30, SP, 128)
	asm.RET()

	// Finalize
	code, err := asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("ssa codegen finalize: %w", err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		return nil, fmt.Errorf("ssa codegen alloc: %w", err)
	}
	if err := block.WriteCode(code); err != nil {
		return nil, fmt.Errorf("ssa codegen write: %w", err)
	}

	var constants []runtime.Value
	if f.Trace != nil {
		constants = f.Trace.Constants
	}
	var proto *vm.FuncProto
	var loopPC int
	if f.Trace != nil {
		proto = f.Trace.LoopProto
		loopPC = f.Trace.LoopPC
	}

	return &CompiledTrace{code: block, proto: proto, loopPC: loopPC, constants: constants}, nil
}

// emitSSAInstSlot emits ARM64 code for one SSA instruction using slot-based allocation.
func emitSSAInstSlot(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	switch inst.Op {
	case SSA_NOP:
		// skip

	case SSA_LOAD_SLOT:
		// No code emitted; UNBOX_INT will load the value.

	case SSA_LOAD_GLOBAL:
		// Load a full Value from the constant pool into the VM register.
		// AuxInt = constant pool index, Slot = destination register.
		constIdx := int(inst.AuxInt)
		dstSlot := int(inst.Slot)
		if dstSlot >= 0 && constIdx >= 0 {
			constOff := constIdx * ValueSize
			dstOff := dstSlot * ValueSize
			// Copy ValueSize bytes (ValueSize/8 words) from constants to registers
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X0, regConsts, constOff+w*8)
				asm.STR(X0, regRegs, dstOff+w*8)
			}
		}

	case SSA_GUARD_TYPE:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		EmitGuardType(asm, regRegs, slot, int(inst.AuxInt), "side_exit")

	case SSA_GUARD_TRUTHY:
		// Guard truthiness of a NaN-boxed value. AuxInt: 0=expect truthy, 1=expect falsy.
		// Truthy: anything except nil (0xFFFC...) and false (0xFFFD...|0).
		slot := int(inst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.LDR(X0, regRegs, slot*ValueSize) // load NaN-boxed value
		if inst.AuxInt == 0 {
			// Expect truthy: exit if nil or bool(false)
			asm.LoadImm64(X1, nb_i64(NB_ValNil))
			asm.CMPreg(X0, X1)
			asm.BCond(CondEQ, "side_exit") // nil → falsy → exit
			asm.LoadImm64(X1, nb_i64(NB_ValFalse))
			asm.CMPreg(X0, X1)
			asm.BCond(CondEQ, "side_exit") // false → falsy → exit
		} else {
			// Expect falsy: exit if truthy (not nil and not false)
			doneLabel := fmt.Sprintf("guard_falsy_%d", ref)
			asm.LoadImm64(X1, nb_i64(NB_ValNil))
			asm.CMPreg(X0, X1)
			asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
			asm.LoadImm64(X1, nb_i64(NB_ValFalse))
			asm.CMPreg(X0, X1)
			asm.BCond(CondNE, "side_exit") // not nil, not false → truthy → exit
			asm.Label(doneLabel)
		}

	case SSA_LOAD_ARRAY:
		// GETTABLE: R(A) = table[key]. table=Arg1's slot, key=Arg2's value.
		// Fast path: table type check, no metatable, key is int, in array bounds.
		// Type-specialized fast path: if arrayKind == ArrayInt or ArrayFloat,
		// load directly from intArray/floatArray (8 bytes) instead of the
		// generic []Value array (24 bytes per element + type check).
		tableSlot := sm.getSlotForRef(inst.Arg1)
		asm.LoadImm64(X9, int64(inst.PC))
		dstSlot := int(inst.Slot)
		// Load key
		keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		// Load *Table (NaN-boxing: extract pointer from NaN-boxed value)
		if tableSlot >= 0 {
			asm.LDR(X0, regRegs, tableSlot*ValueSize)
			EmitExtractPtr(asm, X0, X0, X1)
		}
		asm.CBZ(X0, "side_exit")
		// Check metatable == nil
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")

		// --- Type-specialized int array fast path ---
		if inst.Type == SSATypeInt && dstSlot >= 0 {
			doneLabel := fmt.Sprintf("load_array_done_%d", ref)
			boolLabel := fmt.Sprintf("load_array_bool_%d", ref)
			mixedLabel := fmt.Sprintf("load_array_mixed_%d", ref)

			// Check arrayKind == ArrayInt
			asm.LDRB(X1, X0, TableOffArrayKind)
			asm.CMPimmW(X1, AKInt)
			asm.BCond(CondNE, boolLabel)

			// Int array fast path: bounds check against intArray.len
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffIntArray+8) // intArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			// Load intArray[key]: ptr[key] with LSL #3 (8 bytes per element)
			asm.LDR(X3, X0, TableOffIntArray) // intArray.ptr
			asm.LDRreg(X0, X3, keyReg)        // X0 = *(X3 + keyReg*8)

			// Store result (raw int from intArray → NaN-box it)
			if r, ok := regMap.IntReg(dstSlot); ok {
				asm.MOVreg(r, X0)
			} else {
				EmitBoxInt(asm, X5, X0, X6)
				asm.STR(X5, regRegs, dstSlot*ValueSize)
			}
			asm.B(doneLabel)

			// --- Bool array fast path ---
			// Sentinel encoding: 0=nil, 1=false, 2=true
			// Result: data = b >> 1 (0 for false, 1 for true); nil → side-exit
			asm.Label(boolLabel)
			asm.CMPimmW(X1, AKBool)
			asm.BCond(CondNE, mixedLabel)

			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffBoolArray+8) // boolArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffBoolArray) // boolArray.ptr
			asm.LDRBreg(X0, X3, keyReg)        // X0 = boolArray[key] (byte)
			asm.CBZ(X0, "side_exit")            // 0 = nil → side-exit
			asm.LSRimm(X0, X0, 1)              // 1→0 (false), 2→1 (true)

			if r, ok := regMap.IntReg(dstSlot); ok {
				asm.MOVreg(r, X0)
			} else {
				// Store as NaN-boxed int (bool treated as int in SSA)
				EmitBoxInt(asm, X5, X0, X6)
				asm.STR(X5, regRegs, dstSlot*ValueSize)
			}
			asm.B(doneLabel)

			// Mixed fallback path
			asm.Label(mixedLabel)
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8) // array.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray) // array.ptr
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)

			// NaN-boxing: load full value, check int tag
			asm.LDR(X0, X3, 0) // load NaN-boxed value from array
			asm.LSRimm(X4, X0, 48)
			asm.MOVimm16(X5, NB_TagIntShr48)
			asm.CMPreg(X4, X5)
			typeGuardLabel := fmt.Sprintf("load_array_int_bool_%d", ref)
			asm.BCond(CondEQ, typeGuardLabel)
			asm.MOVimm16(X5, NB_TagBoolShr48)
			asm.CMPreg(X4, X5)
			asm.BCond(CondNE, "side_exit")
			asm.Label(typeGuardLabel)

			// Unbox int (works for both int and bool payloads)
			EmitUnboxInt(asm, X0, X0)
			if r, ok := regMap.IntReg(dstSlot); ok {
				asm.MOVreg(r, X0)
			} else {
				EmitBoxInt(asm, X5, X0, X6)
				asm.STR(X5, regRegs, dstSlot*ValueSize)
			}
			asm.Label(doneLabel)

		} else if inst.Type == SSATypeFloat && dstSlot >= 0 {
			// --- Type-specialized float array fast path ---
			doneLabel := fmt.Sprintf("load_array_done_%d", ref)
			mixedLabel := fmt.Sprintf("load_array_mixed_%d", ref)

			// Check arrayKind == ArrayFloat
			asm.LDRB(X1, X0, TableOffArrayKind)
			asm.CMPimmW(X1, AKFloat)
			asm.BCond(CondNE, mixedLabel)

			// Float array fast path: bounds check against floatArray.len
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffFloatArray+8) // floatArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			// Load floatArray[key]: ptr[key] with LSL #3 (8 bytes per element)
			asm.LDR(X3, X0, TableOffFloatArray) // floatArray.ptr
			asm.LDRreg(X0, X3, keyReg)          // X0 = *(X3 + keyReg*8) = float64 bits

			// Store result (raw float64 bits from floatArray)
			// With NaN-boxing, raw float64 bits ARE the NaN-boxed value
			if fr, ok := regMap.FloatReg(dstSlot); ok {
				asm.FMOVtoFP(fr, X0)
			} else {
				asm.STR(X0, regRegs, dstSlot*ValueSize)
			}
			asm.B(doneLabel)

			// Mixed fallback path
			asm.Label(mixedLabel)
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8) // array.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray) // array.ptr
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)

			// NaN-boxing: load full value, check it's a float (not tagged)
			asm.LDR(X0, X3, 0) // load NaN-boxed value
			// Float check: bits 50-62 NOT all set
			EmitIsTagged(asm, X0, X4)
			asm.BCond(CondEQ, "side_exit") // tagged (not float) → side-exit

			if fr, ok := regMap.FloatReg(dstSlot); ok {
				asm.FMOVtoFP(fr, X0) // raw bits → FP reg
			} else {
				asm.STR(X0, regRegs, dstSlot*ValueSize) // float IS the NaN-boxed form
			}
			asm.Label(doneLabel)

		} else if dstSlot >= 0 {
			// Unspecialized fallback: use []Value array, copy full Value
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8)
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray)
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X0, X3, w*8)
				asm.STR(X0, regRegs, dstSlot*ValueSize+w*8)
			}
		}

	case SSA_STORE_ARRAY:
		// SETTABLE: table[key] = value
		// Type-specialized fast path: if arrayKind matches the value type,
		// store directly to intArray/floatArray (single 8-byte STR) instead
		// of the generic []Value array (3x 8-byte STR for 24-byte Value).
		tableSlot := sm.getSlotForRef(inst.Arg1)
		asm.LoadImm64(X9, int64(inst.PC))
		keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		valRef := SSARef(inst.AuxInt)
		valSlot := sm.getSlotForRef(valRef)

		// Determine value type from register allocation
		valIsInt := false
		valIsFloat := false
		valIsBool := false
		if valSlot >= 0 {
			if _, ok := regMap.IntReg(valSlot); ok {
				valIsInt = true
			} else if _, ok := regMap.FloatReg(valSlot); ok {
				valIsFloat = true
			}
		}
		// Also check SSA type of the value ref
		if !valIsInt && !valIsFloat && int(valRef) < len(f.Insts) {
			valInst := f.Insts[valRef]
			if valInst.Type == SSATypeInt {
				valIsInt = true
			} else if valInst.Type == SSATypeFloat {
				valIsFloat = true
			} else if valInst.Type == SSATypeBool {
				valIsBool = true
			}
		}

		// For the mixed fallback, we need the value spilled to memory as NaN-boxed
		if valSlot >= 0 {
			if r, ok := regMap.IntReg(valSlot); ok {
				EmitBoxInt(asm, X5, r, X6)
				asm.STR(X5, regRegs, valSlot*ValueSize)
			}
		}
		// Load *Table (NaN-boxing: extract pointer from NaN-boxed value)
		if tableSlot >= 0 {
			asm.LDR(X0, regRegs, tableSlot*ValueSize)
			EmitExtractPtr(asm, X0, X0, X1)
		}
		asm.CBZ(X0, "side_exit")
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")

		if valIsInt && valSlot >= 0 {
			// --- Int array fast path for STORE ---
			doneLabel := fmt.Sprintf("store_array_done_%d", ref)
			boolLabel := fmt.Sprintf("store_array_bool_%d", ref)
			mixedLabel := fmt.Sprintf("store_array_mixed_%d", ref)

			asm.LDRB(X1, X0, TableOffArrayKind)
			asm.CMPimmW(X1, AKInt)
			asm.BCond(CondNE, boolLabel)

			// Bounds check against intArray.len
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffIntArray+8) // intArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")

			// Store intArray[key] = value
			asm.LDR(X3, X0, TableOffIntArray) // intArray.ptr
			if r, ok := regMap.IntReg(valSlot); ok {
				asm.STRreg(r, X3, keyReg) // *(X3 + keyReg*8) = r
			} else {
				asm.LDR(X4, regRegs, valSlot*ValueSize+OffsetData)
				asm.STRreg(X4, X3, keyReg)
			}
			asm.B(doneLabel)

			// --- Bool array fallback for int store ---
			// When TypeBool is mapped to SSATypeInt, the data is 0/1.
			// Sentinel encoding: data + 1 (0→1=false, 1→2=true)
			asm.Label(boolLabel)
			asm.CMPimmW(X1, AKBool)
			asm.BCond(CondNE, mixedLabel)

			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffBoolArray+8) // boolArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")

			asm.LDR(X3, X0, TableOffBoolArray) // boolArray.ptr
			if r, ok := regMap.IntReg(valSlot); ok {
				asm.ADDimm(X4, r, 1) // sentinel = data + 1
			} else {
				asm.LDR(X4, regRegs, valSlot*ValueSize+OffsetData)
				asm.ADDimm(X4, X4, 1)
			}
			asm.STRBreg(X4, X3, keyReg)
			asm.MOVimm16(X4, 1)
			asm.STRB(X4, X0, TableOffKeysDirty)
			asm.B(doneLabel)

			// Mixed fallback
			asm.Label(mixedLabel)
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8)
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray)
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X4, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X4, X3, w*8)
			}
			asm.Label(doneLabel)

		} else if valIsFloat && valSlot >= 0 {
			// --- Float array fast path for STORE ---
			doneLabel := fmt.Sprintf("store_array_done_%d", ref)
			mixedLabel := fmt.Sprintf("store_array_mixed_%d", ref)

			asm.LDRB(X1, X0, TableOffArrayKind)
			asm.CMPimmW(X1, AKFloat)
			asm.BCond(CondNE, mixedLabel)

			// Bounds check against floatArray.len
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffFloatArray+8) // floatArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")

			// Store floatArray[key] = value (float64 bits)
			asm.LDR(X3, X0, TableOffFloatArray) // floatArray.ptr
			if fr, ok := regMap.FloatReg(valSlot); ok {
				// Float reg → need to FMOV to GP reg first, then STRreg
				asm.FMOVtoGP(X4, fr)
				asm.STRreg(X4, X3, keyReg)
			} else {
				asm.LDR(X4, regRegs, valSlot*ValueSize+OffsetData)
				asm.STRreg(X4, X3, keyReg)
			}
			asm.B(doneLabel)

			// Mixed fallback
			asm.Label(mixedLabel)
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8)
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray)
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)
			if valSlot >= 0 {
				for w := 0; w < ValueSize/8; w++ {
					asm.LDR(X4, regRegs, valSlot*ValueSize+w*8)
					asm.STR(X4, X3, w*8)
				}
			}
			asm.Label(doneLabel)

		} else if valIsBool {
			// --- Bool array fast path for STORE ---
			// Value is SSA_CONST_BOOL: data=0 (false) or data=1 (true)
			// Sentinel encoding: 0=nil, 1=false, 2=true → store data+1
			doneLabel := fmt.Sprintf("store_array_done_%d", ref)
			mixedLabel := fmt.Sprintf("store_array_mixed_%d", ref)

			asm.LDRB(X1, X0, TableOffArrayKind)
			asm.CMPimmW(X1, AKBool)
			asm.BCond(CondNE, mixedLabel)

			// Bounds check against boolArray.len
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffBoolArray+8) // boolArray.len
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")

			// Store boolArray[key] = data + 1 (sentinel encoding)
			asm.LDR(X3, X0, TableOffBoolArray) // boolArray.ptr
			if valSlot >= 0 {
				asm.LDR(X4, regRegs, valSlot*ValueSize+OffsetData)
				asm.ADDimm(X4, X4, 1) // 0→1 (false), 1→2 (true)
			} else {
				asm.MOVimm16(X4, 1) // default: false sentinel
			}
			asm.STRBreg(X4, X3, keyReg)

			// Set keysDirty = true
			asm.MOVimm16(X4, 1)
			asm.STRB(X4, X0, TableOffKeysDirty)
			asm.B(doneLabel)

			// Mixed fallback
			asm.Label(mixedLabel)
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8)
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray)
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)
			if valSlot >= 0 {
				for w := 0; w < ValueSize/8; w++ {
					asm.LDR(X0, regRegs, valSlot*ValueSize+w*8)
					asm.STR(X0, X3, w*8)
				}
			}
			asm.Label(doneLabel)

		} else {
			// Untyped fallback: use existing []Value array path
			asm.CMPimm(keyReg, 0)
			asm.BCond(CondLT, "side_exit")
			asm.LDR(X3, X0, TableOffArray+8)
			asm.CMPreg(keyReg, X3)
			asm.BCond(CondGE, "side_exit")
			asm.LDR(X3, X0, TableOffArray)
			EmitMulValueSize(asm, X4, keyReg, X5)
			asm.ADDreg(X3, X3, X4)
			if valSlot >= 0 {
				for w := 0; w < ValueSize/8; w++ {
					asm.LDR(X0, regRegs, valSlot*ValueSize+w*8)
					asm.STR(X0, X3, w*8)
				}
			}
		}

	case SSA_LOAD_FIELD:
		// GETFIELD: R(A) = table.field at known skeys index.
		// AuxInt = field index in skeys (captured at recording time).
		// Side-exit if: nil table, has metatable, skeys shrunk, or index unknown.
		fieldIdx := int(inst.AuxInt)
		tableSlot := sm.getSlotForRef(inst.Arg1)
		dstSlot := int(inst.Slot)
		asm.LoadImm64(X9, int64(inst.PC)) // side-exit PC

		if fieldIdx < 0 || tableSlot < 0 {
			// Unknown field index → side-exit (can't compile)
			asm.B("side_exit")
			break
		}

		// Load *Table from register (NaN-boxing: extract pointer)
		asm.LDR(X0, regRegs, tableSlot*ValueSize)
		EmitExtractPtr(asm, X0, X0, X1)
		asm.CBZ(X0, "side_exit") // nil table

		// Guard: no metatable
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")

		// Guard: skeys length > fieldIdx (shape hasn't shrunk)
		asm.LDR(X1, X0, TableOffSkeysLen)
		asm.CMPimm(X1, uint16(fieldIdx+1))
		asm.BCond(CondLT, "side_exit")

		// Load svals[fieldIdx]: svals base + fieldIdx * ValueSize
		asm.LDR(X1, X0, TableOffSvals) // X1 = svals base pointer
		svalsOff := fieldIdx * ValueSize
		// Copy entire Value from svals[fieldIdx] to R(A)
		if dstSlot >= 0 {
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X2, X1, svalsOff+w*8)
				asm.STR(X2, regRegs, dstSlot*ValueSize+w*8)
			}
		}

	case SSA_STORE_FIELD:
		// SETFIELD: table.field = value at known skeys index.
		fieldIdx := int(inst.AuxInt)
		tableSlot := sm.getSlotForRef(inst.Arg1)
		valSlot := sm.getSlotForRef(inst.Arg2)
		asm.LoadImm64(X9, int64(inst.PC))

		if fieldIdx < 0 || tableSlot < 0 {
			asm.B("side_exit")
			break
		}

		// Load *Table (NaN-boxing: extract pointer from NaN-boxed value)
		asm.LDR(X0, regRegs, tableSlot*ValueSize)
		EmitExtractPtr(asm, X0, X0, X1)
		asm.CBZ(X0, "side_exit")

		// Guard: no metatable
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")

		// Guard: skeys length > fieldIdx
		asm.LDR(X1, X0, TableOffSkeysLen)
		asm.CMPimm(X1, uint16(fieldIdx+1))
		asm.BCond(CondLT, "side_exit")

		// Store value to svals[fieldIdx]
		asm.LDR(X1, X0, TableOffSvals)
		svalsOff := fieldIdx * ValueSize
		if valSlot >= 0 {
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X2, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X2, X1, svalsOff+w*8)
			}
		}

	case SSA_UNBOX_INT:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LDR(dstReg, regRegs, slot*ValueSize)
		EmitUnboxInt(asm, dstReg, dstReg)

	case SSA_CONST_INT:
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LoadImm64(dstReg, inst.AuxInt)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_ADD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.ADDreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)
		// If this slot has a FORLOOP A+3 alias, copy to that register too
		if a3Slot, ok := sm.forloopA3[slot]; ok {
			if a3Reg, ok := regMap.IntReg(a3Slot); ok && a3Reg != dstReg {
				asm.MOVreg(a3Reg, dstReg)
			}
		}

	case SSA_SUB_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.SUBreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_MUL_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.MUL(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_MOD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X1)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CBZ(arg2Reg, "side_exit")
		asm.SDIV(X3, arg1Reg, arg2Reg)
		asm.MSUB(dstReg, X3, arg2Reg, arg1Reg)
		// Lua-style modulo: result has same sign as divisor
		doneLabel := fmt.Sprintf("mod_done_%d", ref)
		asm.CBZ(dstReg, doneLabel)
		asm.EORreg(X3, dstReg, arg2Reg)
		asm.CMPreg(X3, XZR)
		asm.BCond(CondGE, doneLabel)
		asm.ADDreg(dstReg, dstReg, arg2Reg)
		asm.Label(doneLabel)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_NEG_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.NEG(dstReg, arg1Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_EQ_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondNE, "side_exit")

	case SSA_LT_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondGE, "side_exit")

	case SSA_LE_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondGT, "side_exit")

	// --- Float operations (using SIMD registers D0-D7) ---

	case SSA_UNBOX_FLOAT:
		// Float values loaded at loop entry or on demand

	case SSA_ADD_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FADDd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_SUB_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FSUBd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_MUL_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FMULd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_DIV_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FDIVd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_FMADD:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		addendD := resolveFloatRef(asm, f, SSARef(inst.AuxInt), regMap, sm, D3)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FMADDd(dstD, arg1D, arg2D, addendD)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_FMSUB:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		addendD := resolveFloatRef(asm, f, SSARef(inst.AuxInt), regMap, sm, D3)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FMSUBd(dstD, arg1D, arg2D, addendD)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_CONST_FLOAT:
		slot := sm.getSlotForRef(ref)
		if slot >= 0 {
			if dreg, ok := regMap.FloatReg(slot); ok {
				// Slot is allocated — load directly into D register, skip memory
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
			} else {
				// Not allocated — write to memory
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(D0, X0)
				asm.FSTRd(D0, regRegs, slot*ValueSize+OffsetData)
				asm.MOVimm16(X0, uint16(runtime.TypeFloat))
				asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
			}
		}

	case SSA_CONST_BOOL:
		slot := sm.getSlotForRef(ref)
		if slot >= 0 {
			asm.LoadImm64(X0, inst.AuxInt)
			asm.STR(X0, regRegs, slot*ValueSize+OffsetData)
			asm.MOVimm16(X0, uint16(runtime.TypeBool))
			asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		}

	case SSA_LT_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
		asm.FCMPd(arg1D, arg2D)
		if inst.AuxInt == 0 {
			asm.BCond(CondGE, "side_exit")
		} else {
			asm.BCond(CondLT, "side_exit")
		}

	case SSA_LE_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
		asm.FCMPd(arg1D, arg2D)
		if inst.AuxInt == 0 {
			asm.BCond(CondGT, "side_exit")
		} else {
			asm.BCond(CondLE, "side_exit")
		}

	case SSA_GT_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
		asm.FCMPd(arg1D, arg2D)
		if inst.AuxInt == 0 {
			asm.BCond(CondLE, "side_exit")
		} else {
			asm.BCond(CondGT, "side_exit")
		}

	case SSA_MOVE:
		slot := sm.getSlotForRef(ref)
		if inst.Type == SSATypeFloat {
			// Float move: use D register allocation
			srcD := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
			dstD := getFloatSlotReg(regMap, slot, D1)
			if dstD != srcD {
				asm.FMOVd(dstD, srcD)
			}
			// If destination is not allocated, write to memory
			if _, ok := regMap.FloatReg(slot); !ok && slot >= 0 {
				asm.FSTRd(srcD, regRegs, slot*ValueSize+OffsetData)
				asm.MOVimm16(X0, uint16(runtime.TypeFloat))
				asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
			}
		} else if inst.Type == SSATypeInt || inst.Type == SSATypeBool {
			// Integer/Bool move: copy data field, write TypeInt tag
			srcReg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			dstReg := getSlotReg(regMap, sm, ref, slot, X0)
			if dstReg != srcReg {
				asm.MOVreg(dstReg, srcReg)
			}
			spillIfNotAllocated(asm, regMap, slot, dstReg)
		} else {
			// Unknown type (table, function, string, etc.): full 24-byte copy.
			// Must copy typ + data + ptr to preserve the type tag and pointer.
			// Copying only the data field and writing TypeInt (as spillIfNotAllocated
			// does) would corrupt table references, causing "attempt to index a
			// number value" errors when the interpreter resumes after side-exit.
			srcSlot := sm.getSlotForRef(inst.Arg1)
			if srcSlot >= 0 && slot >= 0 {
				for w := 0; w < ValueSize/8; w++ {
					asm.LDR(X0, regRegs, srcSlot*ValueSize+w*8)
					asm.STR(X0, regRegs, slot*ValueSize+w*8)
				}
			}
		}

	case SSA_INTRINSIC:
		// Inline GoFunction calls
		dstSlot := int(inst.Slot)
		switch int(inst.AuxInt) {
		case IntrinsicSqrt:
			// math.sqrt(x): load float arg, FSQRT, store result
			// Arg1 is the input value ref (R(A+1) in original CALL)
			srcD := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
			asm.FSQRTd(D1, srcD)
			// Store result to destination slot
			if dstSlot >= 0 {
				if dstDreg, ok := regMap.FloatReg(dstSlot); ok {
					asm.FMOVd(dstDreg, D1)
				} else {
					asm.FSTRd(D1, regRegs, dstSlot*ValueSize+OffsetData)
					asm.MOVimm16(X0, uint16(runtime.TypeFloat))
					asm.STRB(X0, regRegs, dstSlot*ValueSize+OffsetTyp)
				}
			}
		case IntrinsicBxor:
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
			asm.EORreg(X2, arg1Reg, arg2Reg)
			if dstSlot >= 0 {
				asm.STR(X2, regRegs, dstSlot*ValueSize+OffsetData)
				asm.MOVimm16(X0, TypeInt)
				asm.STRB(X0, regRegs, dstSlot*ValueSize+OffsetTyp)
			}
		case IntrinsicBand:
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
			asm.ANDreg(X2, arg1Reg, arg2Reg)
			if dstSlot >= 0 {
				asm.STR(X2, regRegs, dstSlot*ValueSize+OffsetData)
				asm.MOVimm16(X0, TypeInt)
				asm.STRB(X0, regRegs, dstSlot*ValueSize+OffsetTyp)
			}
		default:
			// Unknown intrinsic → side-exit
			asm.LoadImm64(X9, int64(inst.PC))
			asm.B("side_exit")
		}

	case SSA_CALL_INNER_TRACE:
		// Sub-trace calling: spill all allocated registers, call the inner trace's
		// compiled code, check exit code, reload registers.
		//
		// The inner trace uses the same TraceContext (X19) and the same register
		// array (regRegs/X26). It has its own prologue/epilogue that saves and
		// restores callee-saved registers.

		// Step 1: Store all allocated int/float registers back to memory.
		// The inner trace reads/writes directly to the VM register array.
		for slot, armReg := range regMap.Int.slotToReg {
			off := slot*ValueSize + OffsetData
			if off <= 32760 {
				asm.STR(armReg, regRegs, off)
				asm.MOVimm16(X0, TypeInt)
				asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
			}
		}
		// Spill float D registers: use ref-level map for precise spilling,
		// then slot-level as fallback.
		spilledFloatSlots := make(map[int]bool)
		if regMap.FloatRef != nil {
			for fref, dreg := range regMap.FloatRef.refToReg {
				if int(fref) >= len(f.Insts) {
					continue
				}
				finst := &f.Insts[fref]
				slot := int(finst.Slot)
				if slot >= 0 && !spilledFloatSlots[slot] {
					off := slot*ValueSize + OffsetData
					if off <= 32760 {
						asm.FSTRd(dreg, regRegs, off)
						asm.MOVimm16(X0, uint16(runtime.TypeFloat))
						asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
						spilledFloatSlots[slot] = true
					}
				}
			}
		}
		for slot, dreg := range regMap.Float.slotToReg {
			if spilledFloatSlots[slot] {
				continue
			}
			off := slot*ValueSize + OffsetData
			if off <= 32760 {
				asm.FSTRd(dreg, regRegs, off)
				asm.MOVimm16(X0, uint16(runtime.TypeFloat))
				asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
			}
		}

		// Step 2: Swap Constants pointer in TraceContext to inner trace's constants.
		// The inner trace's prologue reads ctx.Constants, so we must set it
		// to the inner trace's constant pool before calling.
		// Save outer constants and set inner constants:
		asm.LDR(X0, X19, TraceCtxOffInnerConstants) // X0 = inner constants ptr
		asm.LDR(X1, X19, TraceCtxOffConstants)       // X1 = outer constants ptr (save)
		asm.STR(X0, X19, TraceCtxOffConstants)        // ctx.Constants = inner constants

		// Step 3: Load inner trace code pointer and call.
		asm.LDR(X8, X19, TraceCtxOffInnerCode)  // X8 = inner code pointer
		// Save outer constants on stack (X1 is caller-saved, won't survive BLR)
		asm.STPpre(X29, X1, SP, -16)
		asm.MOVreg(X0, X19)                     // X0 = TraceContext pointer (argument)
		asm.BLR(X8)                              // call inner trace

		// Step 4: Restore outer constants pointer.
		asm.LDPpost(X29, X1, SP, 16)
		asm.STR(X1, X19, TraceCtxOffConstants) // ctx.Constants = outer constants

		// Step 5: Check exit code.
		// ExitCode=2 (guard fail) means inner trace guard failed → outer side-exit.
		// ExitCode=0 (loop done) or 1 (side exit from inner) → inner loop finished,
		// continue outer loop.
		asm.LDR(X0, X19, TraceCtxOffExitCode)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPimm(X0, 2)
		asm.BCond(CondEQ, "side_exit")

		// Step 6: Reload regConsts and regRegs from TraceContext.
		// The inner trace's epilogue restored callee-saved regs (X19, X26, X27),
		// but our regConsts needs to point to the outer trace's constants.
		asm.LDR(regConsts, X19, TraceCtxOffConstants)

		// Step 7: Reload all allocated registers from memory.
		// The inner trace may have modified any VM slot.
		for slot, armReg := range regMap.Int.slotToReg {
			off := slot*ValueSize + OffsetData
			if off <= 32760 {
				asm.LDR(armReg, regRegs, off)
			}
		}
		for slot, dreg := range regMap.Float.slotToReg {
			off := slot*ValueSize + OffsetData
			if off <= 32760 {
				asm.FLDRd(dreg, regRegs, off)
			}
		}

	case SSA_SIDE_EXIT:
		asm.LoadImm64(X9, int64(inst.PC))
		asm.B("side_exit")

	case SSA_LOOP:
		// Handled in CompileSSA

	case SSA_INNER_LOOP:
		// Handled in CompileSSA loop body emission (emits label)
	}
}

// getSlotReg returns the ARM64 register for an SSA ref based on its VM slot.
// If the slot is allocated, returns the allocated register.
// Otherwise returns the scratch register.
func getSlotReg(regMap *RegMap, sm *ssaSlotMapper, ref SSARef, slot int, scratch Reg) Reg {
	if slot >= 0 {
		if r, ok := regMap.IntReg(slot); ok {
			return r
		}
	}
	return scratch
}

// spillIfNotAllocated stores a computed value to memory if its slot is not allocated
// to a physical register. This ensures the value survives across instructions that
// clobber scratch registers.
func spillIfNotAllocated(asm *Assembler, regMap *RegMap, slot int, valReg Reg) {
	if slot < 0 {
		return
	}
	if _, ok := regMap.IntReg(slot); ok {
		return // already in a register, no spill needed
	}
	// Box the raw int and store as NaN-boxed value
	off := slot * ValueSize
	if off <= 32760 {
		EmitBoxInt(asm, X9, valReg, X8)
		asm.STR(X9, regRegs, off)
	}
}

// resolveSSARefSlot returns the ARM64 register holding the value for an SSA ref.
// Uses slot-based allocation: looks up the ref's VM slot, then the slot's register.
func resolveSSARefSlot(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, scratch Reg) Reg {
	if int(ref) >= len(f.Insts) {
		asm.MOVreg(scratch, XZR)
		return scratch
	}

	// Check if this ref has a known slot, and that slot is allocated
	slot := sm.getSlotForRef(ref)
	if slot >= 0 {
		if r, ok := regMap.IntReg(slot); ok {
			return r
		}
		// Slot is known but not allocated → load NaN-boxed value and unbox int
		asm.LDR(scratch, regRegs, slot*ValueSize)
		EmitUnboxInt(asm, scratch, scratch)
		return scratch
	}

	// No slot known → rematerialize based on instruction type
	inst := &f.Insts[ref]
	switch inst.Op {
	case SSA_CONST_INT:
		asm.LoadImm64(scratch, inst.AuxInt)
		return scratch
	case SSA_UNBOX_INT:
		if int(inst.Arg1) < len(f.Insts) {
			loadInst := &f.Insts[inst.Arg1]
			if loadInst.Op == SSA_LOAD_SLOT {
				s := int(loadInst.Slot)
				if r, ok := regMap.IntReg(s); ok {
					return r
				}
				asm.LDR(scratch, regRegs, s*ValueSize)
				EmitUnboxInt(asm, scratch, scratch)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if r, ok := regMap.IntReg(s); ok {
			return r
		}
		asm.LDR(scratch, regRegs, s*ValueSize)
		EmitUnboxInt(asm, scratch, scratch)
		return scratch
	}

	asm.MOVreg(scratch, XZR)
	return scratch
}

// emitSlotStoreBack writes modified allocated slot values back to memory.
// Only slots that were actually written by the loop body are stored back.
// Writing unmodified slots (e.g., table references) would corrupt their type.
func emitSlotStoreBack(asm *Assembler, regMap *RegMap, sm *ssaSlotMapper, writtenSlots map[int]bool, liveInfo ...*LiveInfo) {
	// Integer register writeback: box raw int → NaN-boxed IntValue
	for slot, armReg := range regMap.Int.slotToReg {
		if !writtenSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			EmitBoxInt(asm, X0, armReg, X1)
			asm.STR(X0, regRegs, off)
		}

		if a3, ok := sm.forloopA3[slot]; ok {
			off3 := a3 * ValueSize
			if off3 <= 32760 {
				EmitBoxInt(asm, X0, armReg, X1)
				asm.STR(X0, regRegs, off3)
			}
		}
	}
	// Float D-register writeback: float bits ARE the NaN-boxed value.
	// Just FSTRd directly — no tag needed.
	for slot, dreg := range regMap.Float.slotToReg {
		if !writtenSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.FSTRd(dreg, regRegs, off)
		}
	}
	// Unallocated float slots: with NaN-boxing, float values stored via
	// FSTRd are already correct NaN-boxed values. No separate type tag needed.
	// (The old code wrote a type tag byte; with NaN-boxing, this is unnecessary.)
}

// isForLoopIncrement checks if an SSA instruction is the FORLOOP's idx += step.
// It's the ADD_INT that writes to the FORLOOP control register (matches the
// LE_INT's first operand).
func isForLoopIncrement(f *SSAFunc, ref SSARef) bool {
	inst := &f.Insts[ref]
	if inst.Op != SSA_ADD_INT {
		return false
	}
	// Check if this ADD's result is used by an LE_INT (FORLOOP check)
	for i := int(ref) + 1; i < len(f.Insts); i++ {
		if f.Insts[i].Op == SSA_LE_INT && f.Insts[i].Arg1 == ref {
			return true
		}
	}
	return false
}

// ssaIsCompilable returns true if all SSA ops in the function are supported by the codegen.
func ssaIsCompilable(f *SSAFunc) bool {
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA, SSA_GUARD_TRUTHY,
			SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_LOAD_SLOT, SSA_STORE_SLOT,
			SSA_UNBOX_INT, SSA_BOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_FLOAT,
			SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_TABLE_LEN, SSA_LOAD_GLOBAL,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
			SSA_LOOP, SSA_PHI, SSA_SNAPSHOT,
			SSA_MOVE, SSA_NOP,
			SSA_INTRINSIC,
			SSA_CALL_INNER_TRACE,
			SSA_INNER_LOOP:
			continue
		case SSA_SIDE_EXIT:
			continue
		default:
			return false
		}
	}
	return true
}

// findWBRFloatSlots identifies float slots that are written before their first
// read in the trace body. These slots may hold non-float types at trace entry
// (e.g., LOADBOOL on mandelbrot escape path), but the value is overwritten
// before first use. For these slots, the pre-loop GUARD_TYPE can be relaxed to
// accept any non-pointer type instead of requiring exact TypeFloat.
func findWBRFloatSlots(f *SSAFunc) map[int]bool {
	result := make(map[int]bool)
	if f == nil || f.Trace == nil {
		return result
	}

	// Safety check: if the trace has any non-numeric operations (table/field
	// access, globals, calls), don't relax any float guards. The WBR analysis
	// works correctly for pure numeric loops (e.g., mandelbrot) but can cause
	// wrong results in traces with mixed numeric/table operations (e.g.,
	// spectral_norm) because the relaxed guard + skipped pre-loop D register
	// load can leave float registers with garbage values that corrupt
	// unrelated computations via register reuse.
	for _, ir := range f.Trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_GETGLOBAL, vm.OP_CALL:
			return result // bail: non-numeric trace
		}
	}

	// Build set of float slots from trace type info
	floatSlots := make(map[int]bool)
	for _, ir := range f.Trace.IR {
		if ir.BType == runtime.TypeFloat && ir.B < 256 {
			floatSlots[ir.B] = true
		}
		if ir.CType == runtime.TypeFloat && ir.C < 256 {
			floatSlots[ir.C] = true
		}
	}

	// For each float slot, check if it's write-before-read using the same
	// logic as ssaBuilder.isWrittenBeforeFirstReadImpl
	for slot := range floatSlots {
		if isSlotWBR(f.Trace, slot) {
			result[slot] = true
		}
	}
	return result
}

// isSlotWBR checks if a slot is written before its first read in the trace.
// This mirrors ssaBuilder.isWrittenBeforeFirstReadImpl.
func isSlotWBR(trace *Trace, slot int) bool {
	// First pass: bail out if used by non-numeric ops
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE:
			if ir.A == slot || ir.B == slot || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_SETTABLE:
			if ir.A == slot || (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_GETFIELD:
			if ir.A == slot || ir.B == slot {
				return false
			}
		case vm.OP_SETFIELD:
			if ir.A == slot || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_GETGLOBAL:
			if ir.A == slot {
				return false
			}
		case vm.OP_CALL:
			if slot >= ir.A && slot < ir.A+ir.B {
				return false
			}
			if ir.A == slot {
				return false
			}
		}
	}

	// Second pass: check write-before-read for numeric ops
	for _, ir := range trace.IR {
		isRead := false
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV,
			vm.OP_LT, vm.OP_LE, vm.OP_EQ:
			if (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_UNM:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_MOVE:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_FORLOOP:
			if slot == ir.A || slot == ir.A+1 || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_FORPREP:
			if slot == ir.A || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_TEST:
			if ir.A == slot {
				isRead = true
			}
		}
		if isRead {
			return false
		}

		isWrite := false
		switch ir.Op {
		case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL:
			isWrite = (ir.A == slot)
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV:
			isWrite = (ir.A == slot)
		case vm.OP_UNM:
			isWrite = (ir.A == slot)
		case vm.OP_MOVE:
			isWrite = (ir.A == slot)
		}
		if isWrite {
			return true
		}
	}
	return false
}

// ssaIsNumericOnly kept for backward compat
func ssaIsNumericOnly(f *SSAFunc) bool { return ssaIsCompilable(f) }

// Keep old name as alias
func ssaIsIntegerOnly(f *SSAFunc) bool { return ssaIsNumericOnly(f) }

// resolveFloatRef returns the FReg holding a float SSA ref's value.
// If the ref's slot is allocated to a D register, returns that register (no load).
// Otherwise loads from memory or rematerializes the constant into scratch.
func resolveFloatRef(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, scratch FReg) FReg {
	if int(ref) >= len(f.Insts) {
		return scratch
	}
	inst := &f.Insts[ref]

	// Constant rematerialization
	if inst.Op == SSA_CONST_FLOAT {
		asm.LoadImm64(X0, inst.AuxInt)
		asm.FMOVtoFP(scratch, X0)
		return scratch
	}

	// Check slot allocation
	slot := sm.getSlotForRef(ref)
	if slot >= 0 {
		if dreg, ok := regMap.FloatReg(slot); ok {
			return dreg // already in D register
		}
		// NaN-boxing: float IS the raw value bits, so FLDRd loads correctly
		asm.FLDRd(scratch, regRegs, slot*ValueSize)
		return scratch
	}

	// Fallback: UNBOX_FLOAT / LOAD_SLOT
	switch inst.Op {
	case SSA_UNBOX_FLOAT:
		if int(inst.Arg1) < len(f.Insts) {
			li := &f.Insts[inst.Arg1]
			if li.Op == SSA_LOAD_SLOT {
				s := int(li.Slot)
				if dreg, ok := regMap.FloatReg(s); ok {
					return dreg
				}
				asm.FLDRd(scratch, regRegs, s*ValueSize)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if s >= 0 {
			if dreg, ok := regMap.FloatReg(s); ok {
				return dreg
			}
			asm.FLDRd(scratch, regRegs, s*ValueSize)
			return scratch
		}
	}
	return scratch
}

// getFloatSlotReg returns the allocated D register for a slot, or scratch.
func getFloatSlotReg(regMap *RegMap, slot int, scratch FReg) FReg {
	if slot >= 0 {
		if dreg, ok := regMap.FloatReg(slot); ok {
			return dreg
		}
	}
	return scratch
}

// storeFloatResult stores a float result. If the slot is allocated to a D register,
// moves the value there (deferred writeback at loop exit). Otherwise writes to memory.
func storeFloatResult(asm *Assembler, regMap *RegMap, slot int, src FReg) {
	if slot < 0 {
		return
	}
	if dreg, ok := regMap.FloatReg(slot); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return // stays in register, written back at exit
	}
	// Not allocated — write float to memory.
	// NaN-boxing: float bits ARE the NaN-boxed value, so FSTRd is correct.
	asm.FSTRd(src, regRegs, slot*ValueSize)
}

// loadFloatArg is a compatibility wrapper for resolveFloatRef that always loads into dst.
func loadFloatArg(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, dst FReg) {
	src := resolveFloatRef(asm, f, ref, regMap, sm, dst)
	if src != dst {
		asm.FMOVd(dst, src)
	}
}

// === Float expression forwarding ===
// Eliminates memory roundtrips for temp float values that are immediately consumed.

// floatForwarder tracks which SSA refs can be kept in scratch D registers
// instead of spilling to memory. Uses D0 and D3 as forwarding registers
// (D1/D2 are reserved for arg loading in resolveFloatRefFwd).
type floatForwarder struct {
	eligible map[SSARef]bool
	live     map[SSARef]FReg
	nextReg  int // cycles between 0 (D0) and 1 (D3)
}

var fwdRegs = [2]FReg{D0, D3}

func newFloatForwarder(f *SSAFunc, regMap *RegMap, sm *ssaSlotMapper, loopIdx int) *floatForwarder {
	fwd := &floatForwarder{
		eligible: make(map[SSARef]bool),
		live:     make(map[SSARef]FReg),
	}

	// Count uses of each ref within the loop body
	useCount := make(map[SSARef]int)
	firstUse := make(map[SSARef]int) // index of first use
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Arg1 >= 0 {
			useCount[inst.Arg1]++
			if _, ok := firstUse[inst.Arg1]; !ok {
				firstUse[inst.Arg1] = i
			}
		}
		if inst.Arg2 >= 0 {
			useCount[inst.Arg2]++
			if _, ok := firstUse[inst.Arg2]; !ok {
				firstUse[inst.Arg2] = i
			}
		}
		// FMADD/FMSUB store a third operand ref in AuxInt
		if inst.Op == SSA_FMADD || inst.Op == SSA_FMSUB {
			auxRef := SSARef(inst.AuxInt)
			if auxRef >= 0 {
				useCount[auxRef]++
				if _, ok := firstUse[auxRef]; !ok {
					firstUse[auxRef] = i
				}
			}
		}
	}

	// Mark eligible: single-use float results to non-allocated temp slots
	// where the use is within 3 instructions (allows MUL→MUL→SUB pattern)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)
		switch inst.Op {
		case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT:
			// Skip refs that already have ref-level D register allocation
			if _, ok := regMap.FloatRefReg(ref); ok {
				continue
			}
			slot := sm.getSlotForRef(ref)
			if slot < 0 {
				continue
			}
			if _, ok := regMap.FloatReg(slot); ok {
				continue
			}
			if useCount[ref] == 1 {
				if use, ok := firstUse[ref]; ok && use-i <= 3 {
					fwd.eligible[ref] = true
				}
			}
		}
	}

	return fwd
}

func resolveFloatRefFwd(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder, scratch FReg) FReg {
	if dreg, ok := fwd.live[ref]; ok {
		delete(fwd.live, ref)
		return dreg
	}
	// Check ref-level allocation first (more precise than slot-level)
	if dreg, ok := regMap.FloatRefReg(ref); ok {
		return dreg
	}
	return resolveFloatRef(asm, f, ref, regMap, sm, scratch)
}

// getFloatRefReg returns the D register for an SSA ref (ref-level allocation),
// falling back to the slot-level allocation, or scratch.
func getFloatRefReg(regMap *RegMap, ref SSARef, slot int, scratch FReg) FReg {
	// Ref-level first
	if dreg, ok := regMap.FloatRefReg(ref); ok {
		return dreg
	}
	// Slot-level fallback
	if slot >= 0 {
		if dreg, ok := regMap.FloatReg(slot); ok {
			return dreg
		}
	}
	return scratch
}

// storeFloatResultRef stores a float result using ref-level allocation.
// If the ref has a D register, moves the value there. If the slot has a D register
// (slot-level fallback), moves there. Otherwise writes to memory.
func storeFloatResultRef(asm *Assembler, regMap *RegMap, ref SSARef, slot int, src FReg) {
	if slot < 0 {
		return
	}
	// Check ref-level allocation
	if dreg, ok := regMap.FloatRefReg(ref); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return // stays in register, written back at exit
	}
	// Slot-level fallback
	if dreg, ok := regMap.FloatReg(slot); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return
	}
	// Not allocated — write data to memory
	asm.FSTRd(src, regRegs, slot*ValueSize+OffsetData)
}

// emitSSAInstSlotFwd is the forwarding-aware version of emitSSAInstSlot.
func emitSSAInstSlotFwd(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder) {
	switch inst.Op {
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D1)
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D2)

		// Choose destination register: ref-level D reg, slot-level D reg,
		// cycling scratch for forwarding, or plain scratch
		var dstD FReg
		if dreg, ok := regMap.FloatRefReg(ref); ok {
			dstD = dreg
		} else if _, ok := regMap.FloatReg(slot); ok {
			dstD = getFloatSlotReg(regMap, slot, D0)
		} else if fwd.eligible[ref] {
			dstD = fwdRegs[fwd.nextReg%2]
			fwd.nextReg++
		} else {
			dstD = D0
		}

		switch inst.Op {
		case SSA_ADD_FLOAT:
			asm.FADDd(dstD, arg1D, arg2D)
		case SSA_SUB_FLOAT:
			asm.FSUBd(dstD, arg1D, arg2D)
		case SSA_MUL_FLOAT:
			asm.FMULd(dstD, arg1D, arg2D)
		case SSA_DIV_FLOAT:
			asm.FDIVd(dstD, arg1D, arg2D)
		}

		if fwd.eligible[ref] {
			fwd.live[ref] = dstD
			return // skip memory write — value forwarded in scratch register
		}
		storeFloatResultRef(asm, regMap, ref, slot, dstD)

	case SSA_FMADD, SSA_FMSUB:
		// FMADD: Dd = Da + Dn * Dm (Arg1=Dn, Arg2=Dm, AuxInt=Da ref)
		// FMSUB: Dd = Da - Dn * Dm (Arg1=Dn, Arg2=Dm, AuxInt=Da ref)
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D1) // Dn
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D2) // Dm
		addendRef := SSARef(inst.AuxInt)
		addendD := resolveFloatRefFwd(asm, f, addendRef, regMap, sm, fwd, D3) // Da

		var dstD FReg
		if dreg, ok := regMap.FloatRefReg(ref); ok {
			dstD = dreg
		} else if _, ok := regMap.FloatReg(slot); ok {
			dstD = getFloatSlotReg(regMap, slot, D0)
		} else {
			dstD = D0
		}

		switch inst.Op {
		case SSA_FMADD:
			asm.FMADDd(dstD, arg1D, arg2D, addendD)
		case SSA_FMSUB:
			asm.FMSUBd(dstD, arg1D, arg2D, addendD)
		}

		storeFloatResultRef(asm, regMap, ref, slot, dstD)

	case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D1)
		asm.FCMPd(arg1D, arg2D)
		switch inst.Op {
		case SSA_LT_FLOAT:
			if inst.AuxInt == 0 {
				asm.BCond(CondGE, "side_exit")
			} else {
				asm.BCond(CondLT, "side_exit")
			}
		case SSA_LE_FLOAT:
			if inst.AuxInt == 0 {
				asm.BCond(CondGT, "side_exit")
			} else {
				asm.BCond(CondLE, "side_exit")
			}
		case SSA_GT_FLOAT:
			if inst.AuxInt == 0 {
				asm.BCond(CondLE, "side_exit")
			} else {
				asm.BCond(CondGT, "side_exit")
			}
		}

	case SSA_MOVE:
		if inst.Type == SSATypeFloat {
			slot := sm.getSlotForRef(ref)
			srcD := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
			dstD := getFloatRefReg(regMap, ref, slot, D1)
			if dstD != srcD {
				asm.FMOVd(dstD, srcD)
			}
			// If neither ref-level nor slot-level allocated, write to memory
			if _, refOk := regMap.FloatRefReg(ref); !refOk {
				if _, slotOk := regMap.FloatReg(slot); !slotOk && slot >= 0 {
					asm.FSTRd(srcD, regRegs, slot*ValueSize+OffsetData)
				}
			}
		} else {
			emitSSAInstSlot(asm, f, ref, inst, regMap, sm)
		}

	case SSA_CONST_FLOAT:
		slot := sm.getSlotForRef(ref)
		if slot >= 0 {
			// Check ref-level allocation first
			if dreg, ok := regMap.FloatRefReg(ref); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
			} else if dreg, ok := regMap.FloatReg(slot); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
			} else {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(D0, X0)
				asm.FSTRd(D0, regRegs, slot*ValueSize+OffsetData)
				asm.MOVimm16(X0, uint16(runtime.TypeFloat))
				asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
			}
		}

	default:
		emitSSAInstSlot(asm, f, ref, inst, regMap, sm)
	}
}

// === Side-exit continuation for inner loop escape ===

// sideExitContinuation holds analysis results for the inner loop escape optimization.
// When a float guard inside the inner loop fails (e.g., zr²+zi² > 4.0 in mandelbrot),
// instead of side-exiting to the interpreter, we skip the post-inner-loop epilogue
// (GUARD_TRUTHY + count++) and jump directly to the outer FORLOOP increment.
//
// Additionally, when GUARD_TRUTHY fails (non-escaping pixel), instead of side-exiting,
// we execute count++ inline and continue the outer FORLOOP.
type sideExitContinuation struct {
	innerLoopStartIdx   int // index of SSA_INNER_LOOP
	innerLoopEndIdx     int // index of SSA_LE_INT(AuxInt=1)
	innerLoopSlot       int // VM slot of inner loop index (for spilling)
	outerForLoopAddIdx  int // index of the outer FORLOOP's ADD_INT (skip_count target)

	// GUARD_TRUTHY continuation: when escaped=false, execute count++ inline
	guardTruthyIdx int // index of GUARD_TRUTHY in SSA (for redirecting)
	countSlot      int // VM slot of count variable (-1 if unknown)
	countStepSlot  int // VM slot or constant for count increment (-1 if unknown)
	countIsRK      bool // true if countStepSlot is RK (constant)
}

// analyzeSideExitContinuation scans the SSA to detect the inner loop structure
// for the side-exit continuation optimization. Returns nil if no inner loop is found
// or the pattern doesn't match.
func analyzeSideExitContinuation(f *SSAFunc, loopIdx int) *sideExitContinuation {
	info := &sideExitContinuation{
		innerLoopStartIdx:  -1,
		innerLoopEndIdx:    -1,
		innerLoopSlot:      -1,
		outerForLoopAddIdx: -1,
		guardTruthyIdx:     -1,
		countSlot:          -1,
		countStepSlot:      -1,
	}

	// Find SSA_INNER_LOOP and SSA_LE_INT(AuxInt=1) after the main LOOP
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_INNER_LOOP {
			info.innerLoopStartIdx = i
		}
		if inst.Op == SSA_LE_INT && inst.AuxInt == 1 {
			info.innerLoopEndIdx = i
		}
	}

	if info.innerLoopStartIdx < 0 || info.innerLoopEndIdx < 0 {
		return nil // no inner loop
	}

	// Check that there are float guards inside the inner loop
	hasFloatGuard := false
	for i := info.innerLoopStartIdx; i < info.innerLoopEndIdx; i++ {
		if isFloatGuard(f.Insts[i].Op) {
			hasFloatGuard = true
			break
		}
	}
	if !hasFloatGuard {
		return nil // no float guards to optimize
	}

	// Find the inner loop's slot from LE_INT(AuxInt=1)'s Arg1
	leInst := &f.Insts[info.innerLoopEndIdx]
	arg1Ref := leInst.Arg1
	if int(arg1Ref) < len(f.Insts) {
		argInst := &f.Insts[arg1Ref]
		if argInst.Slot >= 0 {
			info.innerLoopSlot = int(argInst.Slot)
		}
	}

	// Find GUARD_TRUTHY between inner_loop_done and the outer FORLOOP
	for i := info.innerLoopEndIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_GUARD_TRUTHY {
			info.guardTruthyIdx = i
			break
		}
		// Stop scanning if we hit the outer exit check
		if (inst.Op == SSA_LE_INT && inst.AuxInt == 0) || inst.Op == SSA_LT_INT {
			break
		}
	}

	// Find the outer FORLOOP's ADD_INT: it's the Arg1 of LE_INT(AuxInt=0)
	for i := info.innerLoopEndIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_LE_INT && inst.AuxInt == 0 {
			// The outer FORLOOP exit check. Its Arg1 is the ADD_INT (idx += step).
			addRef := inst.Arg1
			if int(addRef) >= 0 && int(addRef) < len(f.Insts) {
				info.outerForLoopAddIdx = int(addRef)
			}
			break
		}
		if inst.Op == SSA_LT_INT {
			// While-loop style outer exit check
			addRef := inst.Arg1
			if int(addRef) >= 0 && int(addRef) < len(f.Insts) {
				info.outerForLoopAddIdx = int(addRef)
			}
			break
		}
	}

	if info.outerForLoopAddIdx < 0 {
		return nil // can't find outer FORLOOP increment
	}

	// Analyze count++ from bytecodes: look at the bytecodes between
	// the GUARD_TRUTHY's TEST PC and the outer FORLOOP PC.
	// Pattern: LOADINT Rtemp 1 → ADD Rtemp Rcount Rtemp → MOVE Rcount Rtemp
	// The real count slot is the source B of ADD (= destination A of MOVE).
	if info.guardTruthyIdx >= 0 && f.Trace != nil && f.Trace.LoopProto != nil {
		proto := f.Trace.LoopProto
		guardInst := &f.Insts[info.guardTruthyIdx]
		testPC := guardInst.PC // PC of the TEST instruction

		// The JMP after TEST tells us where count++ is.
		// TEST at testPC, JMP at testPC+1.
		if testPC+1 < len(proto.Code) {
			jmpInst := proto.Code[testPC+1]
			jmpOp := vm.DecodeOp(jmpInst)
			if jmpOp == vm.OP_JMP {
				jmpSBX := vm.DecodesBx(jmpInst)
				jmpTarget := testPC + 1 + jmpSBX + 1
				// Scan the skipped instructions for the ADD+MOVE pattern
				for pc := testPC + 2; pc < jmpTarget && pc < len(proto.Code); pc++ {
					inst := proto.Code[pc]
					op := vm.DecodeOp(inst)
					if op == vm.OP_ADD {
						addB := vm.DecodeB(inst) // source: count slot
						// Look for a MOVE after the ADD that copies result to the count slot
						if pc+1 < jmpTarget && pc+1 < len(proto.Code) {
							moveInst := proto.Code[pc+1]
							moveOp := vm.DecodeOp(moveInst)
							if moveOp == vm.OP_MOVE {
								moveA := vm.DecodeA(moveInst) // destination
								if moveA == addB {
									// Confirmed: count is at addB, and the pattern is
									// ADD Rtemp Rcount Rstep → MOVE Rcount Rtemp
									info.countSlot = addB
								}
							}
						}
						if info.countSlot < 0 {
							// No MOVE after ADD → direct count++: ADD Rcount Rcount Rstep
							info.countSlot = vm.DecodeA(inst)
						}
						break
					}
				}
			}
		}
	}

	return info
}

// isFloatGuard returns true if the SSA op is a float comparison guard.
func isFloatGuard(op SSAOp) bool {
	switch op {
	case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	}
	return false
}

// emitGuardTruthyWithContinuation emits a GUARD_TRUTHY that branches to the
// given target label instead of "side_exit" on failure. Used for the non-escaping
// pixel continuation: instead of side-exiting, jump to truthy_cont which does count++.
func emitGuardTruthyWithContinuation(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, target string) {
	slot := int(inst.Slot)
	asm.LoadImm64(X9, int64(inst.PC))
	asm.LDRB(X0, regRegs, slot*ValueSize+OffsetTyp)
	if inst.AuxInt == 0 {
		// Expect truthy: exit if nil or bool(false)
		asm.CMPimmW(X0, TypeNil)
		asm.BCond(CondEQ, target) // nil → falsy → continuation
		asm.CMPimmW(X0, TypeBool)
		doneLabel := fmt.Sprintf("guard_truthy_cont_%d", ref)
		asm.BCond(CondNE, doneLabel) // not nil, not bool → truthy → OK
		asm.LDR(X1, regRegs, slot*ValueSize+OffsetData)
		asm.CBZ(X1, target) // bool(false) → falsy → continuation
		asm.Label(doneLabel)
	} else {
		// Expect falsy: exit if truthy (not nil and not bool(false))
		asm.CMPimmW(X0, TypeNil)
		doneLabel := fmt.Sprintf("guard_falsy_cont_%d", ref)
		asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
		asm.CMPimmW(X0, TypeBool)
		asm.BCond(CondNE, target) // not nil, not bool → truthy → continuation
		asm.LDR(X1, regRegs, slot*ValueSize+OffsetData)
		asm.CBNZ(X1, target) // bool(true) → truthy → continuation
		asm.Label(doneLabel)
	}
}

// emitFloatGuardWithTarget emits a float comparison guard that branches to the
// given target label instead of "side_exit". Used for inner loop escape optimization.
func emitFloatGuardWithTarget(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder, target string) {
	asm.LoadImm64(X9, int64(inst.PC))
	arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
	arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D1)
	asm.FCMPd(arg1D, arg2D)
	switch inst.Op {
	case SSA_LT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGE, target)
		} else {
			asm.BCond(CondLT, target)
		}
	case SSA_LE_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGT, target)
		} else {
			asm.BCond(CondLE, target)
		}
	case SSA_GT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondLE, target)
		} else {
			asm.BCond(CondGT, target)
		}
	}
}
