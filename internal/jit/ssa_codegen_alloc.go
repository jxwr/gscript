//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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
		case SSA_LOAD_FIELD, SSA_LOAD_ARRAY, SSA_LOAD_GLOBAL:
			slot := int(inst.Slot)
			if slot >= 0 {
				result[slot] = append(result[slot], ref)
			}
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
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_MOVE,
			SSA_LOAD_FIELD, SSA_LOAD_ARRAY, SSA_LOAD_GLOBAL, SSA_INTRINSIC:
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
