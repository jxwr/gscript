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

// newSlotAlloc performs frequency-based slot allocation on the SSA function.
// It identifies the hottest VM slots and assigns them to X20-X24.
func newSlotAlloc(f *SSAFunc) *slotAlloc {
	sa := &slotAlloc{
		slotToReg: make(map[int]Reg),
		regToSlot: make(map[Reg]int),
	}

	// Build slot usage frequency from the trace IR — ONLY for integer arithmetic ops.
	// Table/string slots must NOT be allocated (they hold non-integer Values).
	freq := make(map[int]int)
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			switch ir.Op {
			case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_UNM,
				vm.OP_LOADINT, vm.OP_MOVE,
				vm.OP_EQ, vm.OP_LT, vm.OP_LE:
				if ir.B < 256 {
					freq[ir.B]++
				}
				if ir.C < 256 {
					freq[ir.C]++
				}
				freq[ir.A]++
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
	slotRefs := buildSSASlotRefs(f)
	for slot := range slotRefs {
		freq[slot]++
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
		case SSA_LOAD_SLOT, SSA_UNBOX_INT, SSA_STORE_SLOT, SSA_BOX_INT:
			slot := int(inst.Slot)
			m.refToSlot[ref] = slot
			m.slotToLatestRef[slot] = ref
		case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_CONST_INT:
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
func CompileSSA(f *SSAFunc) (*CompiledTrace, error) {
	if f == nil || len(f.Insts) == 0 {
		return nil, fmt.Errorf("ssa codegen: empty SSA function")
	}

	if !ssaIsIntegerOnly(f) {
		return nil, fmt.Errorf("ssa codegen: trace contains non-integer ops")
	}

	asm := NewAssembler()
	sa := newSlotAlloc(f)
	sm := newSSASlotMapper(f)

	// === Prologue ===
	asm.STPpre(X29, X30, SP, -96)
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)

	trCtx := X19
	asm.MOVreg(trCtx, X0)
	asm.LDR(regRegs, trCtx, 0)
	asm.LDR(regConsts, trCtx, 8)

	// === Pre-LOOP: guards + initial loads ===
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
		emitSSAInstSlot(asm, f, SSARef(i), &inst, sa, sm)
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
	for slot, armReg := range sa.slotToReg {
		if !loadedSlots[slot] {
			asm.LDR(armReg, regRegs, slot*ValueSize+OffsetData)
		}
	}

	// === LOOP header ===
	asm.Label("trace_loop")

	// === Loop body ===
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)

		if inst.Op == SSA_LE_INT {
			// Loop exit condition: CMP newIdx, limit; B.GT loop_done
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
			asm.CMPreg(arg1Reg, arg2Reg)
			asm.BCond(CondGT, "loop_done")
			continue
		}

		emitSSAInstSlot(asm, f, ref, inst, sa, sm)
	}

	// Loop back-edge
	asm.B("trace_loop")

	// Build set of slots actually written by the loop body
	ssaWrittenSlots := make(map[int]bool)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		switch inst.Op {
		case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
			SSA_CONST_INT, SSA_CONST_FLOAT:
			slot := int(inst.Slot)
			if slot >= 0 {
				ssaWrittenSlots[slot] = true
			}
		}
	}
	// FORLOOP writes to A (idx) and A+3 (loop variable)
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			if ir.Op == vm.OP_FORLOOP {
				ssaWrittenSlots[ir.A] = true
				ssaWrittenSlots[ir.A+3] = true
			}
		}
	}

	// === Side exit ===
	asm.Label("side_exit")
	emitSlotStoreBack(asm, sa, sm, ssaWrittenSlots)
	asm.STR(X9, X19, 16)  // ctx.ExitPC = X9
	asm.LoadImm64(X0, 1)  // ExitCode = 1
	asm.B("epilogue")

	// === Loop done ===
	asm.Label("loop_done")
	emitSlotStoreBack(asm, sa, sm, ssaWrittenSlots)
	asm.LoadImm64(X0, 0)  // ExitCode = 0

	// === Epilogue ===
	asm.Label("epilogue")
	asm.STR(X0, X19, 24)  // ctx.ExitCode

	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	asm.LDPpost(X29, X30, SP, 96)
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
	if f.Trace != nil {
		proto = f.Trace.LoopProto
	}

	return &CompiledTrace{code: block, proto: proto, constants: constants}, nil
}

// emitSSAInstSlot emits ARM64 code for one SSA instruction using slot-based allocation.
func emitSSAInstSlot(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, sa *slotAlloc, sm *ssaSlotMapper) {
	switch inst.Op {
	case SSA_NOP:
		// skip

	case SSA_LOAD_SLOT:
		// No code emitted; UNBOX_INT will load the value.

	case SSA_GUARD_TYPE:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.LDRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		asm.CMPimmW(X0, uint16(inst.AuxInt))
		asm.BCond(CondNE, "side_exit")

	case SSA_UNBOX_INT:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
		asm.LDR(dstReg, regRegs, slot*ValueSize+OffsetData)

	case SSA_CONST_INT:
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
		asm.LoadImm64(dstReg, inst.AuxInt)
		spillIfNotAllocated(asm, sa, slot, dstReg)

	case SSA_ADD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
		asm.ADDreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, sa, slot, dstReg)
		// If this slot has a FORLOOP A+3 alias, copy to that register too
		if a3Slot, ok := sm.forloopA3[slot]; ok {
			if a3Reg, ok := sa.getReg(a3Slot); ok && a3Reg != dstReg {
				asm.MOVreg(a3Reg, dstReg)
			}
		}

	case SSA_SUB_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
		asm.SUBreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, sa, slot, dstReg)

	case SSA_MUL_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
		asm.MUL(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, sa, slot, dstReg)

	case SSA_MOD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X1)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X2)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
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
		spillIfNotAllocated(asm, sa, slot, dstReg)

	case SSA_NEG_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(sa, sm, ref, slot, X0)
		asm.NEG(dstReg, arg1Reg)
		spillIfNotAllocated(asm, sa, slot, dstReg)

	case SSA_EQ_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondNE, "side_exit")

	case SSA_LT_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondGE, "side_exit")

	case SSA_LE_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, sa, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, sa, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondGT, "side_exit")

	// --- Float operations (using SIMD registers D0-D3) ---

	case SSA_UNBOX_FLOAT:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		// Load float64 bits from data field into a SIMD register
		// For now, keep in memory — float slot-alloc is separate
		_ = slot // float values stay in memory for now

	case SSA_ADD_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1Slot := sm.getSlotForRef(inst.Arg1)
		arg2Slot := sm.getSlotForRef(inst.Arg2)
		// Load float operands from memory into SIMD regs
		if arg1Slot >= 0 {
			asm.FLDRd(D0, regRegs, arg1Slot*ValueSize+OffsetData)
		}
		if arg2Slot >= 0 {
			asm.FLDRd(D1, regRegs, arg2Slot*ValueSize+OffsetData)
		}
		asm.FADDd(D0, D0, D1)
		if slot >= 0 {
			asm.FSTRd(D0, regRegs, slot*ValueSize+OffsetData)
			// Write type byte = TypeFloat
			asm.MOVimm16(X0, uint16(runtime.TypeFloat))
			asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		}

	case SSA_SUB_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1Slot := sm.getSlotForRef(inst.Arg1)
		arg2Slot := sm.getSlotForRef(inst.Arg2)
		if arg1Slot >= 0 {
			asm.FLDRd(D0, regRegs, arg1Slot*ValueSize+OffsetData)
		}
		if arg2Slot >= 0 {
			asm.FLDRd(D1, regRegs, arg2Slot*ValueSize+OffsetData)
		}
		asm.FSUBd(D0, D0, D1)
		if slot >= 0 {
			asm.FSTRd(D0, regRegs, slot*ValueSize+OffsetData)
			asm.MOVimm16(X0, uint16(runtime.TypeFloat))
			asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		}

	case SSA_MUL_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1Slot := sm.getSlotForRef(inst.Arg1)
		arg2Slot := sm.getSlotForRef(inst.Arg2)
		if arg1Slot >= 0 {
			asm.FLDRd(D0, regRegs, arg1Slot*ValueSize+OffsetData)
		}
		if arg2Slot >= 0 {
			asm.FLDRd(D1, regRegs, arg2Slot*ValueSize+OffsetData)
		}
		asm.FMULd(D0, D0, D1)
		if slot >= 0 {
			asm.FSTRd(D0, regRegs, slot*ValueSize+OffsetData)
			asm.MOVimm16(X0, uint16(runtime.TypeFloat))
			asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		}

	case SSA_DIV_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1Slot := sm.getSlotForRef(inst.Arg1)
		arg2Slot := sm.getSlotForRef(inst.Arg2)
		if arg1Slot >= 0 {
			asm.FLDRd(D0, regRegs, arg1Slot*ValueSize+OffsetData)
		}
		if arg2Slot >= 0 {
			asm.FLDRd(D1, regRegs, arg2Slot*ValueSize+OffsetData)
		}
		asm.FDIVd(D0, D0, D1)
		if slot >= 0 {
			asm.FSTRd(D0, regRegs, slot*ValueSize+OffsetData)
			asm.MOVimm16(X0, uint16(runtime.TypeFloat))
			asm.STRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		}

	case SSA_SIDE_EXIT:
		asm.LoadImm64(X9, int64(inst.PC))
		asm.B("side_exit")

	case SSA_LOOP:
		// Handled in CompileSSA
	}
}

// getSlotReg returns the ARM64 register for an SSA ref based on its VM slot.
// If the slot is allocated, returns the allocated register.
// Otherwise returns the scratch register.
func getSlotReg(sa *slotAlloc, sm *ssaSlotMapper, ref SSARef, slot int, scratch Reg) Reg {
	if slot >= 0 {
		if r, ok := sa.getReg(slot); ok {
			return r
		}
	}
	return scratch
}

// spillIfNotAllocated stores a computed value to memory if its slot is not allocated
// to a physical register. This ensures the value survives across instructions that
// clobber scratch registers.
func spillIfNotAllocated(asm *Assembler, sa *slotAlloc, slot int, valReg Reg) {
	if slot < 0 {
		return
	}
	if _, ok := sa.getReg(slot); ok {
		return // already in a register, no spill needed
	}
	// Store to memory. Use X9 for the type byte to avoid clobbering valReg (which may be X0).
	off := slot * ValueSize
	if off <= 32760 {
		asm.STR(valReg, regRegs, off+OffsetData)
		asm.MOVimm16(X9, TypeInt)
		asm.STRB(X9, regRegs, off+OffsetTyp)
	}
}

// resolveSSARefSlot returns the ARM64 register holding the value for an SSA ref.
// Uses slot-based allocation: looks up the ref's VM slot, then the slot's register.
func resolveSSARefSlot(asm *Assembler, f *SSAFunc, ref SSARef, sa *slotAlloc, sm *ssaSlotMapper, scratch Reg) Reg {
	if int(ref) >= len(f.Insts) {
		asm.MOVreg(scratch, XZR)
		return scratch
	}

	// Check if this ref has a known slot, and that slot is allocated
	slot := sm.getSlotForRef(ref)
	if slot >= 0 {
		if r, ok := sa.getReg(slot); ok {
			return r
		}
		// Slot is known but not allocated → load from memory (value was spilled)
		asm.LDR(scratch, regRegs, slot*ValueSize+OffsetData)
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
				if r, ok := sa.getReg(s); ok {
					return r
				}
				asm.LDR(scratch, regRegs, s*ValueSize+OffsetData)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if r, ok := sa.getReg(s); ok {
			return r
		}
		asm.LDR(scratch, regRegs, s*ValueSize+OffsetData)
		return scratch
	}

	asm.MOVreg(scratch, XZR)
	return scratch
}

// emitSlotStoreBack writes modified allocated slot values back to memory.
// Only slots that were actually written by the loop body are stored back.
// Writing unmodified slots (e.g., table references) would corrupt their type.
func emitSlotStoreBack(asm *Assembler, sa *slotAlloc, sm *ssaSlotMapper, writtenSlots map[int]bool) {
	for slot, armReg := range sa.slotToReg {
		if !writtenSlots[slot] {
			continue // slot not modified by loop body — don't corrupt it
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.STR(armReg, regRegs, off+OffsetData)
			asm.MOVimm16(X0, TypeInt)
			asm.STRB(X0, regRegs, off+OffsetTyp)
		}

		if a3, ok := sm.forloopA3[slot]; ok {
			off3 := a3 * ValueSize
			if off3 <= 32760 {
				asm.STR(armReg, regRegs, off3+OffsetData)
				asm.MOVimm16(X0, TypeInt)
				asm.STRB(X0, regRegs, off3+OffsetTyp)
			}
		}
	}
}

// ssaIsNumericOnly returns true if the SSA function contains only numeric (int + float) ops.
func ssaIsNumericOnly(f *SSAFunc) bool {
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_LOAD_SLOT, SSA_STORE_SLOT,
			SSA_UNBOX_INT, SSA_BOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_FLOAT,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
			SSA_LOOP, SSA_PHI, SSA_SNAPSHOT,
			SSA_MOVE, SSA_NOP:
			continue
		case SSA_SIDE_EXIT:
			continue
		default:
			return false
		}
	}
	return true
}

// Keep old name as alias
func ssaIsIntegerOnly(f *SSAFunc) bool { return ssaIsNumericOnly(f) }
