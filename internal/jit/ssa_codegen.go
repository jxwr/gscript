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
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
		if inst.Op == SSA_GUARD_TYPE {
			// Emit guard that branches to guard_fail on type mismatch
			loadInst := &f.Insts[inst.Arg1]
			slot := int(loadInst.Slot)
			asm.LDRB(X0, regRegs, slot*ValueSize+OffsetTyp)
			asm.CMPimmW(X0, uint16(inst.AuxInt))
			asm.BCond(CondNE, "guard_fail")
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
			asm.LDR(armReg, regRegs, slot*ValueSize+OffsetData)
		}
	}

	// Load allocated float slots into D registers
	for slot, dreg := range regMap.Float.slotToReg {
		asm.FLDRd(dreg, regRegs, slot*ValueSize+OffsetData)
	}

	// === LOOP header ===
	asm.Label("trace_loop")

	// === Float expression forwarding analysis ===
	// For non-allocated float temps that are produced and immediately consumed
	// by the next instruction, we skip the memory write and keep the value
	// in a scratch D register. This eliminates ~20 memory ops per mandelbrot iteration.
	fwd := newFloatForwarder(f, regMap, sm, loopIdx)

	// === Loop body ===
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)

		switch inst.Op {
		case SSA_LE_INT:
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
		}

		emitSSAInstSlotFwd(asm, f, ref, inst, regMap, sm, fwd)
	}

	// Loop back-edge
	asm.B("trace_loop")

	// === Guard fail (pre-loop type mismatch) ===
	// ExitCode=2: "not executed" — interpreter should run the body normally.
	// No store-back needed since we haven't modified any registers.
	asm.Label("guard_fail")
	asm.LoadImm64(X0, 2)  // ExitCode = 2 (guard fail, not executed)
	asm.B("epilogue")

	// === Side exit ===
	asm.Label("side_exit")
	emitSlotStoreBack(asm, regMap, sm, liveInfo.WrittenSlots, liveInfo)
	asm.STR(X9, X19, 16)  // ctx.ExitPC = X9
	asm.LoadImm64(X0, 1)  // ExitCode = 1
	asm.B("epilogue")

	// === Loop done ===
	asm.Label("loop_done")
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
	if f.Trace != nil {
		proto = f.Trace.LoopProto
	}

	return &CompiledTrace{code: block, proto: proto, constants: constants}, nil
}

// emitSSAInstSlot emits ARM64 code for one SSA instruction using slot-based allocation.
func emitSSAInstSlot(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	switch inst.Op {
	case SSA_NOP:
		// skip

	case SSA_LOAD_SLOT:
		// No code emitted; UNBOX_INT will load the value.

	case SSA_LOAD_GLOBAL:
		// Load a full 32-byte Value from the constant pool into the VM register.
		// AuxInt = constant pool index, Slot = destination register.
		constIdx := int(inst.AuxInt)
		dstSlot := int(inst.Slot)
		if dstSlot >= 0 && constIdx >= 0 {
			constOff := constIdx * ValueSize
			dstOff := dstSlot * ValueSize
			// Copy 32 bytes (4 words) from constants to registers
			for w := 0; w < 4; w++ {
				asm.LDR(X0, regConsts, constOff+w*8)
				asm.STR(X0, regRegs, dstOff+w*8)
			}
		}

	case SSA_GUARD_TYPE:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.LDRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		asm.CMPimmW(X0, uint16(inst.AuxInt))
		asm.BCond(CondNE, "side_exit")

	case SSA_GUARD_TRUTHY:
		// Guard truthiness of a value. AuxInt: 0=expect truthy, 1=expect falsy.
		// Truthy: anything except nil and false.
		slot := int(inst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.LDRB(X0, regRegs, slot*ValueSize+OffsetTyp)
		if inst.AuxInt == 0 {
			// Expect truthy: exit if nil or bool(false)
			asm.CMPimmW(X0, TypeNil)
			asm.BCond(CondEQ, "side_exit") // nil → falsy → exit
			asm.CMPimmW(X0, TypeBool)
			doneLabel := fmt.Sprintf("guard_truthy_%d", ref)
			asm.BCond(CondNE, doneLabel) // not nil, not bool → truthy → OK
			asm.LDR(X1, regRegs, slot*ValueSize+OffsetData)
			asm.CBZ(X1, "side_exit") // bool(false) → falsy → exit
			asm.Label(doneLabel)
		} else {
			// Expect falsy: exit if truthy (not nil and not bool(false))
			asm.CMPimmW(X0, TypeNil)
			doneLabel := fmt.Sprintf("guard_falsy_%d", ref)
			asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
			asm.CMPimmW(X0, TypeBool)
			asm.BCond(CondNE, "side_exit") // not nil, not bool → truthy → exit
			asm.LDR(X1, regRegs, slot*ValueSize+OffsetData)
			asm.CBNZ(X1, "side_exit") // bool(true) → truthy → exit
			asm.Label(doneLabel)
		}

	case SSA_LOAD_ARRAY:
		// GETTABLE: R(A) = table[key]. table=Arg1's slot, key=Arg2's value.
		// Fast path: table type check, no metatable, key is int, in array bounds.
		tableSlot := sm.getSlotForRef(inst.Arg1)
		asm.LoadImm64(X9, int64(inst.PC))
		dstSlot := int(inst.Slot)
		// Load key
		keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		// Load *Table
		if tableSlot >= 0 {
			asm.LDR(X0, regRegs, tableSlot*ValueSize+OffsetPtrData)
		}
		asm.CBZ(X0, "side_exit")
		// Check metatable == nil
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")
		// Array bounds: key >= 1 && key < array.len
		asm.CMPimm(keyReg, 1)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8) // array.len
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		// Compute element address: X3 = &array[key]
		asm.LDR(X3, X0, TableOffArray) // array.ptr
		asm.LSLimm(X4, keyReg, 5)      // key * 32
		asm.ADDreg(X3, X3, X4)

		if inst.Type == SSATypeInt && dstSlot >= 0 {
			// Type-specialized int load: guard type byte, load only data field (8 bytes)
			asm.LDRB(X0, X3, OffsetTyp)    // load type byte from element
			asm.CMPimmW(X0, TypeInt)
			typeGuardLabel := fmt.Sprintf("load_array_int_bool_%d", ref)
			asm.BCond(CondEQ, typeGuardLabel)
			// Also accept TypeBool (booleans stored as 0/1 in data field)
			asm.CMPimmW(X0, TypeBool)
			asm.BCond(CondNE, "side_exit") // not int and not bool → side exit
			asm.Label(typeGuardLabel)

			// Load only the data field (8 bytes instead of 32)
			asm.LDR(X0, X3, OffsetData)

			// Store to allocated register if available, else to memory
			if r, ok := regMap.IntReg(dstSlot); ok {
				asm.MOVreg(r, X0)
			} else {
				asm.STR(X0, regRegs, dstSlot*ValueSize+OffsetData)
				asm.MOVimm16(X0, TypeInt)
				asm.STRB(X0, regRegs, dstSlot*ValueSize+OffsetTyp)
			}
		} else if inst.Type == SSATypeFloat && dstSlot >= 0 {
			// Type-specialized float load: guard type byte, load only data field
			asm.LDRB(X0, X3, OffsetTyp)
			asm.CMPimmW(X0, TypeFloat)
			asm.BCond(CondNE, "side_exit")

			// Load data field (float64 bits)
			asm.LDR(X0, X3, OffsetData)

			// Store to float register if available, else to memory
			if fr, ok := regMap.FloatReg(dstSlot); ok {
				asm.FMOVtoFP(fr, X0)
			} else {
				asm.STR(X0, regRegs, dstSlot*ValueSize+OffsetData)
				asm.MOVimm16(X0, TypeFloat)
				asm.STRB(X0, regRegs, dstSlot*ValueSize+OffsetTyp)
			}
		} else if dstSlot >= 0 {
			// Unspecialized fallback: copy full 32 bytes
			for w := 0; w < 4; w++ {
				asm.LDR(X0, X3, w*8)
				asm.STR(X0, regRegs, dstSlot*ValueSize+w*8)
			}
		}

	case SSA_STORE_ARRAY:
		// SETTABLE: table[key] = value
		tableSlot := sm.getSlotForRef(inst.Arg1)
		asm.LoadImm64(X9, int64(inst.PC))
		keyReg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		valRef := SSARef(inst.AuxInt)
		valSlot := sm.getSlotForRef(valRef)
		// Load *Table
		if tableSlot >= 0 {
			asm.LDR(X0, regRegs, tableSlot*ValueSize+OffsetPtrData)
		}
		asm.CBZ(X0, "side_exit")
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "side_exit")
		asm.CMPimm(keyReg, 1)
		asm.BCond(CondLT, "side_exit")
		asm.LDR(X3, X0, TableOffArray+8)
		asm.CMPreg(keyReg, X3)
		asm.BCond(CondGE, "side_exit")
		asm.LDR(X3, X0, TableOffArray)
		asm.LSLimm(X4, keyReg, 5)
		asm.ADDreg(X3, X3, X4)
		// Write value (4 words)
		if valSlot >= 0 {
			for w := 0; w < 4; w++ {
				asm.LDR(X0, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X0, X3, w*8)
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

		// Load *Table from register
		asm.LDR(X0, regRegs, tableSlot*ValueSize+OffsetPtrData)
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
		// Copy entire Value (32 bytes = 4 words) from svals[fieldIdx] to R(A)
		if dstSlot >= 0 {
			for w := 0; w < 4; w++ {
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

		// Load *Table
		asm.LDR(X0, regRegs, tableSlot*ValueSize+OffsetPtrData)
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
			for w := 0; w < 4; w++ {
				asm.LDR(X2, regRegs, valSlot*ValueSize+w*8)
				asm.STR(X2, X1, svalsOff+w*8)
			}
		}

	case SSA_UNBOX_INT:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LDR(dstReg, regRegs, slot*ValueSize+OffsetData)

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
		} else {
			srcReg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			dstReg := getSlotReg(regMap, sm, ref, slot, X0)
			if dstReg != srcReg {
				asm.MOVreg(dstReg, srcReg)
			}
			spillIfNotAllocated(asm, regMap, slot, dstReg)
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
		for slot, dreg := range regMap.Float.slotToReg {
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
				if r, ok := regMap.IntReg(s); ok {
					return r
				}
				asm.LDR(scratch, regRegs, s*ValueSize+OffsetData)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if r, ok := regMap.IntReg(s); ok {
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
func emitSlotStoreBack(asm *Assembler, regMap *RegMap, sm *ssaSlotMapper, writtenSlots map[int]bool, liveInfo ...*LiveInfo) {
	// Integer register writeback
	for slot, armReg := range regMap.Int.slotToReg {
		if !writtenSlots[slot] {
			continue
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
	// Float D-register writeback
	for slot, dreg := range regMap.Float.slotToReg {
		if !writtenSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.FSTRd(dreg, regRegs, off+OffsetData)
			asm.MOVimm16(X0, uint16(runtime.TypeFloat))
			asm.STRB(X0, regRegs, off+OffsetTyp)
		}
	}
	// Write type tags for unallocated float slots.
	// During the loop body, we skip type tag writes for unallocated float temps
	// (they only write the data field). Write the type tag here at exit.
	if len(liveInfo) > 0 && liveInfo[0] != nil {
		li := liveInfo[0]
		for slot := range writtenSlots {
			if _, ok := regMap.Float.slotToReg[slot]; ok {
				continue // already handled above
			}
			if _, ok := regMap.Int.slotToReg[slot]; ok {
				continue // integer slot
			}
			// Check if this slot has a float type
			if slotType, ok := li.SlotTypes[slot]; ok && slotType == SSATypeFloat {
				off := slot * ValueSize
				if off <= 32760 {
					asm.MOVimm16(X0, uint16(runtime.TypeFloat))
					asm.STRB(X0, regRegs, off+OffsetTyp)
				}
			}
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
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_LOAD_SLOT, SSA_STORE_SLOT,
			SSA_UNBOX_INT, SSA_BOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_FLOAT,
			SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_TABLE_LEN, SSA_LOAD_GLOBAL,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
			SSA_LOOP, SSA_PHI, SSA_SNAPSHOT,
			SSA_MOVE, SSA_NOP,
			SSA_INTRINSIC,
			SSA_CALL_INNER_TRACE:
			continue
		case SSA_SIDE_EXIT:
			continue
		default:
			return false
		}
	}
	return true
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
		asm.FLDRd(scratch, regRegs, slot*ValueSize+OffsetData)
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
				asm.FLDRd(scratch, regRegs, s*ValueSize+OffsetData)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if s >= 0 {
			if dreg, ok := regMap.FloatReg(s); ok {
				return dreg
			}
			asm.FLDRd(scratch, regRegs, s*ValueSize+OffsetData)
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
	// Not allocated — write data to memory.
	// Skip type tag write here (deferred to store-back at loop exit).
	// The type tag is written once during store-back, not every iteration.
	asm.FSTRd(src, regRegs, slot*ValueSize+OffsetData)
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
	}

	// Mark eligible: single-use float results to non-allocated temp slots
	// where the use is within 3 instructions (allows MUL→MUL→SUB pattern)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)
		switch inst.Op {
		case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT:
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
	return resolveFloatRef(asm, f, ref, regMap, sm, scratch)
}

// emitSSAInstSlotFwd is the forwarding-aware version of emitSSAInstSlot.
func emitSSAInstSlotFwd(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder) {
	switch inst.Op {
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D1)
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D2)

		// Choose destination register: allocated D reg, or cycling scratch for forwarding
		var dstD FReg
		if _, ok := regMap.FloatReg(slot); ok {
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
		storeFloatResult(asm, regMap, slot, dstD)

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
			dstD := getFloatSlotReg(regMap, slot, D1)
			if dstD != srcD {
				asm.FMOVd(dstD, srcD)
			}
			if _, ok := regMap.FloatReg(slot); !ok && slot >= 0 {
				// Write data only, type tag deferred to store-back
				asm.FSTRd(srcD, regRegs, slot*ValueSize+OffsetData)
			}
		} else {
			emitSSAInstSlot(asm, f, ref, inst, regMap, sm)
		}

	default:
		emitSSAInstSlot(asm, f, ref, inst, regMap, sm)
	}
}
