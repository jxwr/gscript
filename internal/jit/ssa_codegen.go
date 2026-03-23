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
//   X20-X23 = allocated VM slots (up to 4 hot slots)
//   X24 = regTagInt (pinned NaN-boxing int tag 0xFFFE000000000000)
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
// It identifies the hottest VM slots and assigns them to X20-X23 (X24 reserved for regTagInt).
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

	// DEBUG: dump allocation info
	if debugSSAStoreBack {
		fmt.Printf("[SSA-COMPILE] loopPC=%d\n", f.Trace.LoopPC)
		fmt.Printf("[SSA-COMPILE] Int slots: ")
		for slot, reg := range regMap.Int.slotToReg {
			fmt.Printf("R%d→%v ", slot, reg)
		}
		fmt.Printf("\n")
		fmt.Printf("[SSA-COMPILE] Float slots: ")
		for slot, reg := range regMap.Float.slotToReg {
			fmt.Printf("R%d→%v ", slot, reg)
		}
		fmt.Printf("\n")
		fmt.Printf("[SSA-COMPILE] WrittenSlots: ")
		for slot := range liveInfo.WrittenSlots {
			fmt.Printf("R%d(type=%v) ", slot, liveInfo.SlotTypes[slot])
		}
		fmt.Printf("\n")
		for i, inst := range f.Insts {
			fmt.Printf("[SSA-COMPILE]   [%3d] Op=%d Type=%d Slot=%d Arg1=%d Arg2=%d AuxInt=%d\n",
				i, inst.Op, inst.Type, inst.Slot, inst.Arg1, inst.Arg2, inst.AuxInt)
		}
	}

	// Phase 2: Emit ARM64
	_ = ud // reserved for future optimization passes
	return emitSSA(f, regMap, liveInfo)
}

// buildFloatRefSpill computes a map of written float slots NOT in Float.slotToReg
// but with ref-level D register allocations. Maps each such slot to the D register
// of the LAST ref-level writer in the loop body. This is needed for store-back:
// ref-level allocated floats skip memory writes during the loop body, but the
// store-back only iterates Float.slotToReg.
func buildFloatRefSpill(f *SSAFunc, regMap *RegMap) map[int]FReg {
	result := make(map[int]FReg)
	if f == nil || regMap.FloatRef == nil {
		return result
	}
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return result
	}
	// Find the last ref-level float writer for each slot in the loop body
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)
		dreg, hasRefReg := regMap.FloatRefReg(ref)
		if !hasRefReg {
			continue
		}
		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}
		// Skip if already in slot-level map (handled by normal store-back)
		if _, ok := regMap.FloatReg(slot); ok {
			continue
		}
		// Skip if in int register map
		if _, ok := regMap.IntReg(slot); ok {
			continue
		}
		// Record (last writer wins)
		result[slot] = dreg
	}
	return result
}

// emitSSA emits ARM64 machine code for an SSAFunc using pre-computed analysis results.
func emitSSA(f *SSAFunc, regMap *RegMap, liveInfo *LiveInfo) (*CompiledTrace, error) {
	asm := NewAssembler()
	sm := newSSASlotMapper(f)
	floatRefSpill := buildFloatRefSpill(f, regMap)
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

	// Pin regTagInt (X24) with the NaN-boxing int tag constant.
	asm.LoadImm64(regTagInt, nb_i64(NB_TagInt))

	// === Call-exit resume dispatch ===
	// Check if this is a resume after a call-exit (ResumePC != 0).
	// If so, skip pre-loop guards/loads and jump directly to the resume point.
	// Collect call-exit PCs for the dispatch table.
	type callExitInfo struct {
		ssaIdx int   // SSA instruction index
		pc     int   // bytecode PC of the CALL
	}
	var callExits []callExitInfo
	for i, inst := range f.Insts {
		if inst.Op == SSA_CALL {
			callExits = append(callExits, callExitInfo{ssaIdx: i, pc: inst.PC})
		}
	}

	if len(callExits) > 0 {
		asm.LDR(X0, trCtx, TraceCtxOffResumePC)
		asm.CBZ(X0, "normal_entry")
		// Clear ResumePC for next iteration
		asm.STR(XZR, trCtx, TraceCtxOffResumePC)
		// Dispatch to the correct resume point based on ResumePC value
		for _, ce := range callExits {
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
		asm.STR(X9, trCtx, TraceCtxOffExitPC)
		asm.LoadImm64(X0, 1)
		asm.B("epilogue")
		asm.Label("normal_entry")
	}

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

	// === Hoist loop-invariant table guards to pre-loop ===
	// For tables accessed in LOAD_ARRAY/STORE_ARRAY that are not modified in
	// the loop body, verify is-table + no-metatable + array-kind once before
	// the loop. The in-loop codegen then skips these checks (~5 instructions
	// saved per array access per iteration).
	hoistedTables := findLoopInvariantTableSlots(f, loopIdx, sm)
	if len(hoistedTables) > 0 {
		emitTableSlotGuards(asm, hoistedTables)
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

		// SSA_CALL: call-exit — store all modified slots, set ExitPC, exit with ExitCode=3.
		// The Go executor handles the call, then re-enters the trace at the resume label.
		if inst.Op == SSA_CALL {
			// 1. Store back all modified slots so the VM sees current values
			emitSlotStoreBack(asm, regMap, sm, liveInfo.WrittenSlots, floatRefSpill)
			// 2. Set ExitPC = bytecode PC of the CALL instruction
			asm.LoadImm64(X9, int64(inst.PC))
			asm.STR(X9, trCtx, TraceCtxOffExitPC)
			// 3. Set ExitCode = 3 (call-exit)
			asm.LoadImm64(X0, 3)
			asm.B("epilogue")
			// 4. Resume label — Go executor sets ResumePC and re-enters JIT here
			asm.Label(fmt.Sprintf("resume_call_%d", i))
			// 5. Reload regRegs (regs may have been reallocated by VM during call)
			asm.LDR(regRegs, trCtx, TraceCtxOffRegs)
			// 6. Reload all allocated int registers from memory
			for slot, armReg := range regMap.Int.slotToReg {
				off := slot * ValueSize
				if off <= 32760 {
					asm.LDR(armReg, regRegs, off)
					EmitUnboxInt(asm, armReg, armReg)
				}
			}
			// 7. Reload all allocated float registers from memory
			for slot, freg := range regMap.Float.slotToReg {
				off := slot * ValueSize
				if off <= 32760 {
					asm.FLDRd(freg, regRegs, off+OffsetData)
				}
			}
			continue
		}

		emitSSAInstSlotFwd(asm, f, ref, inst, regMap, sm, fwd, hoistedTables)
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
		if sideExitInfo.countSlot >= 0 {
			asm.Label("truthy_cont")
			countOff := sideExitInfo.countSlot * ValueSize
			// Load count from memory or register
			if r, ok := regMap.IntReg(sideExitInfo.countSlot); ok {
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
	emitSlotStoreBack(asm, regMap, sm, liveInfo.WrittenSlots, floatRefSpill)
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
	emitSlotStoreBack(asm, regMap, sm, liveInfo.WrittenSlots, floatRefSpill)
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

	ct := &CompiledTrace{code: block, proto: proto, loopPC: loopPC, constants: constants}
	ct.hasCallExit = len(callExits) > 0
	return ct, nil
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
func emitSlotStoreBack(asm *Assembler, regMap *RegMap, sm *ssaSlotMapper, writtenSlots map[int]bool, floatRefSpill map[int]FReg) {
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
	// Ref-level float spill: written float slots NOT in Float.slotToReg
	// but allocated to ref-level D registers. These slots need explicit
	// store-back because the loop body may skip memory writes when a
	// ref-level D register is available.
	for slot, dreg := range floatRefSpill {
		if !writtenSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.FSTRd(dreg, regRegs, off)
		}
	}
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
			SSA_INNER_LOOP,
			SSA_CALL:
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

	// Check if the trace has non-numeric operations
	hasNonNumeric := false
	for _, ir := range f.Trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_GETGLOBAL, vm.OP_CALL:
			hasNonNumeric = true
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

	for slot := range floatSlots {
		if hasNonNumeric {
			// For traces with field/table/call ops, use extended WBR check
			// that recognizes GETFIELD/GETTABLE/CALL as valid writes.
			// This allows relaxing guards for intermediate float results
			// like dx/dy/dz in nbody's "dx = bi.x - bj.x" pattern.
			b := &ssaBuilder{trace: f.Trace, slotType: make(map[int]SSAType)}
			if b.isWrittenBeforeFirstReadExt(slot) {
				result[slot] = true
			}
		} else {
			// Pure numeric trace: use original isSlotWBR
			if isSlotWBR(f.Trace, slot) {
				result[slot] = true
			}
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
		return // stays in register, written back by floatRefSpill at exit
	}
	// Slot-level fallback
	if dreg, ok := regMap.FloatReg(slot); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return
	}
	// Not allocated — write data to memory
	asm.FSTRd(src, regRegs, slot*ValueSize)
}
