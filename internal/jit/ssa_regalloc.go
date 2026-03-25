//go:build darwin && arm64

package jit

import (
	"sort"
)

// ────────────────────────────────────────────────────────────────────────────
// Available registers for trace values
// ────────────────────────────────────────────────────────────────────────────

// Callee-saved GPR for trace values (4 available: X20-X23).
// X24 is reserved for the NaN-boxing int tag constant (regTagInt).
var allocableGPR = []Reg{X20, X21, X22, X23}

// Callee-saved FPR for trace values (8 available: D4-D11).
var allocableFPR = []FReg{D4, D5, D6, D7, D8, D9, D10, D11}

// ────────────────────────────────────────────────────────────────────────────
// Slot-based integer register allocator
// ────────────────────────────────────────────────────────────────────────────

// slotAlloc maps VM slots to ARM64 GPR registers based on usage frequency.
type slotAlloc struct {
	slotToReg map[int]Reg
}

// getReg returns the GPR allocated to a VM slot, if any.
func (a *slotAlloc) getReg(slot int) (Reg, bool) {
	r, ok := a.slotToReg[slot]
	return r, ok
}

// newSlotAlloc builds a slot-based integer register allocation.
// It counts how many times each integer-typed slot is referenced in the SSA
// instructions, then allocates the available GPRs to the most frequently
// used slots.
func newSlotAlloc(f *SSAFunc) *slotAlloc {
	if f == nil {
		return &slotAlloc{slotToReg: map[int]Reg{}}
	}

	// Count slot frequency for integer-typed instructions.
	freq := map[int]int{}
	floatSlots := map[int]bool{} // track float-typed slots to exclude them

	for _, inst := range f.Insts {
		if inst.Op == SSA_LOOP || inst.Op == SSA_NOP || inst.Op == SSA_SNAPSHOT {
			continue
		}

		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}

		if inst.Type == SSATypeFloat || isFloatOp(inst.Op) {
			floatSlots[slot] = true
			continue
		}

		if inst.Type == SSATypeInt || inst.Type == SSATypeBool {
			freq[slot]++
		}
	}

	// Also count from trace IR for slots referenced as operands.
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			reads, readTypes := traceReads(&ir)
			for i, slot := range reads {
				if floatSlots[slot] {
					continue
				}
				if i < len(readTypes) {
					switch readTypes[i] {
					case 3: // runtime.TypeFloat
						floatSlots[slot] = true
						continue
					}
				}
				freq[slot]++
			}
			for _, slot := range traceWrites(&ir) {
				if floatSlots[slot] {
					continue
				}
				freq[slot]++
			}
		}
	}

	// Remove float-only slots from freq.
	for slot := range floatSlots {
		delete(freq, slot)
	}

	// Sort slots by frequency (descending).
	type slotFreq struct {
		slot int
		freq int
	}
	var sorted []slotFreq
	for s, f := range freq {
		if f >= 1 {
			sorted = append(sorted, slotFreq{s, f})
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].freq != sorted[j].freq {
			return sorted[i].freq > sorted[j].freq
		}
		return sorted[i].slot < sorted[j].slot // deterministic tie-break
	})

	// Allocate GPRs to top slots.
	alloc := &slotAlloc{slotToReg: make(map[int]Reg)}
	for i, sf := range sorted {
		if i >= len(allocableGPR) {
			break
		}
		alloc.slotToReg[sf.slot] = allocableGPR[i]
	}

	return alloc
}

// ────────────────────────────────────────────────────────────────────────────
// Slot-based float register allocator
// ────────────────────────────────────────────────────────────────────────────

// floatSlotAlloc maps VM slots to ARM64 FPR registers based on usage frequency.
type floatSlotAlloc struct {
	slotToReg map[int]FReg
}

// getReg returns the FPR allocated to a VM slot, if any.
func (a *floatSlotAlloc) getReg(slot int) (FReg, bool) {
	r, ok := a.slotToReg[slot]
	return r, ok
}

// newFloatSlotAlloc builds a slot-based float register allocation.
func newFloatSlotAlloc(f *SSAFunc) *floatSlotAlloc {
	if f == nil {
		return &floatSlotAlloc{slotToReg: map[int]FReg{}}
	}

	freq := map[int]int{}

	for _, inst := range f.Insts {
		if inst.Op == SSA_LOOP || inst.Op == SSA_NOP || inst.Op == SSA_SNAPSHOT {
			continue
		}

		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}

		if inst.Type == SSATypeFloat || isFloatOp(inst.Op) {
			freq[slot]++
		}
	}

	// Also count from trace IR.
	if f.Trace != nil {
		for _, ir := range f.Trace.IR {
			reads, readTypes := traceReads(&ir)
			for i, slot := range reads {
				if i < len(readTypes) && readTypes[i] == 3 { // runtime.TypeFloat
					freq[slot]++
				}
			}
		}
	}

	type slotFreq struct {
		slot int
		freq int
	}
	var sorted []slotFreq
	for s, f := range freq {
		if f >= 1 {
			sorted = append(sorted, slotFreq{s, f})
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].freq != sorted[j].freq {
			return sorted[i].freq > sorted[j].freq
		}
		return sorted[i].slot < sorted[j].slot
	})

	alloc := &floatSlotAlloc{slotToReg: make(map[int]FReg)}
	for i, sf := range sorted {
		if i >= len(allocableFPR) {
			break
		}
		alloc.slotToReg[sf.slot] = allocableFPR[i]
	}

	return alloc
}

// ────────────────────────────────────────────────────────────────────────────
// Ref-level float register allocator (linear scan with coalescing)
// ────────────────────────────────────────────────────────────────────────────

// floatRefAlloc maps SSA refs to ARM64 FPR registers.
type floatRefAlloc struct {
	refToReg map[SSARef]FReg
}

// getReg returns the FPR allocated to an SSA ref, if any.
func (a *floatRefAlloc) getReg(ref SSARef) (FReg, bool) {
	if a == nil {
		return 0, false
	}
	r, ok := a.refToReg[ref]
	return r, ok
}

// liveRange tracks the live range of an SSA ref.
type liveRange struct {
	ref   SSARef
	start int // instruction index of definition
	end   int // instruction index of last use
	typ   SSAType
	slot  int16 // VM slot, for coalescing
}

// floatRefAllocLR performs linear-scan register allocation on float SSA refs.
// It allocates D4-D11 registers based on live range analysis with coalescing
// for MOVE instructions that write to loop-carried slots.
func floatRefAllocLR(f *SSAFunc) *floatRefAlloc {
	result := &floatRefAlloc{refToReg: make(map[SSARef]FReg)}
	if f == nil || len(f.Insts) == 0 {
		return result
	}

	// Find LOOP marker.
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

	// Build live ranges for float-typed refs.
	ranges := computeFloatLiveRanges(f, loopIdx)
	if len(ranges) == 0 {
		return result
	}

	// Build pre-loop slot→ref map for coalescing.
	preLoopSlotRef := map[int]SSARef{}
	for i := 0; i < loopIdx; i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_UNBOX_FLOAT && inst.Slot >= 0 {
			preLoopSlotRef[int(inst.Slot)] = SSARef(i)
		}
	}

	// Sort by start position for linear scan.
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start != ranges[j].start {
			return ranges[i].start < ranges[j].start
		}
		return ranges[i].ref < ranges[j].ref
	})

	// Coalescing: if a MOVE writes to a loop-carried slot, force it to use
	// the same register as the pre-loop ref for that slot.
	coalesced := map[SSARef]SSARef{} // MOVE ref → pre-loop ref
	for _, lr := range ranges {
		idx := int(lr.ref)
		if idx < len(f.Insts) {
			inst := &f.Insts[idx]
			if inst.Op == SSA_MOVE && inst.Type == SSATypeFloat && inst.Slot >= 0 {
				if preRef, ok := preLoopSlotRef[int(inst.Slot)]; ok {
					coalesced[lr.ref] = preRef
				}
			}
		}
	}

	// Linear scan allocation.
	freeFPR := make([]FReg, len(allocableFPR))
	copy(freeFPR, allocableFPR)

	type activeRange struct {
		liveRange
		reg FReg
	}
	var active []activeRange

	expireOld := func(pos int) {
		newActive := active[:0]
		for _, ar := range active {
			if ar.end <= pos {
				// This range expired — return register to free pool.
				freeFPR = append(freeFPR, ar.reg)
			} else {
				newActive = append(newActive, ar)
			}
		}
		active = newActive
	}

	for _, lr := range ranges {
		expireOld(lr.start)

		// Check if this ref is coalesced with a pre-loop ref.
		if preRef, ok := coalesced[lr.ref]; ok {
			if reg, allocated := result.refToReg[preRef]; allocated {
				result.refToReg[lr.ref] = reg
				active = append(active, activeRange{lr, reg})
				continue
			}
		}

		// Allocate a register.
		if len(freeFPR) > 0 {
			reg := freeFPR[0]
			freeFPR = freeFPR[1:]
			result.refToReg[lr.ref] = reg
			active = append(active, activeRange{lr, reg})

			// If this is a pre-loop unbox ref, check if any coalesced MOVE
			// should share this register.
			for moveRef, preRef := range coalesced {
				if preRef == lr.ref {
					result.refToReg[moveRef] = reg
				}
			}
		}
		// else: spill — ref not allocated (will use scratch register)
	}

	return result
}

// floatRefAllocLRExclude is like floatRefAllocLR but excludes registers already
// allocated by the slot-level float allocator. This prevents conflicts where both
// allocators assign the same FPR to different entities.
func floatRefAllocLRExclude(f *SSAFunc, slotAlloc *floatSlotAlloc) *floatRefAlloc {
	result := &floatRefAlloc{refToReg: make(map[SSARef]FReg)}
	if f == nil || len(f.Insts) == 0 {
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

	ranges := computeFloatLiveRanges(f, loopIdx)
	if len(ranges) == 0 {
		return result
	}

	// Build set of registers used by slot-level allocator
	usedBySlot := make(map[FReg]bool)
	if slotAlloc != nil {
		for _, freg := range slotAlloc.slotToReg {
			usedBySlot[freg] = true
		}
	}

	// Available registers: only those NOT used by slot-level allocator
	var freeFPRBase []FReg
	for _, freg := range allocableFPR {
		if !usedBySlot[freg] {
			freeFPRBase = append(freeFPRBase, freg)
		}
	}

	if len(freeFPRBase) == 0 {
		return result // no registers available
	}

	// Build pre-loop slot→ref map for coalescing.
	preLoopSlotRef := map[int]SSARef{}
	for i := 0; i < loopIdx; i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_UNBOX_FLOAT && inst.Slot >= 0 {
			preLoopSlotRef[int(inst.Slot)] = SSARef(i)
		}
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start != ranges[j].start {
			return ranges[i].start < ranges[j].start
		}
		return ranges[i].ref < ranges[j].ref
	})

	coalesced := map[SSARef]SSARef{}
	for _, lr := range ranges {
		idx := int(lr.ref)
		if idx < len(f.Insts) {
			inst := &f.Insts[idx]
			if inst.Op == SSA_MOVE && inst.Type == SSATypeFloat && inst.Slot >= 0 {
				if preRef, ok := preLoopSlotRef[int(inst.Slot)]; ok {
					coalesced[lr.ref] = preRef
				}
			}
		}
	}

	freeFPR := make([]FReg, len(freeFPRBase))
	copy(freeFPR, freeFPRBase)

	type activeRange struct {
		liveRange
		reg FReg
	}
	var active []activeRange

	expireOld := func(pos int) {
		newActive := active[:0]
		for _, ar := range active {
			if ar.end <= pos {
				freeFPR = append(freeFPR, ar.reg)
			} else {
				newActive = append(newActive, ar)
			}
		}
		active = newActive
	}

	for _, lr := range ranges {
		expireOld(lr.start)

		if preRef, ok := coalesced[lr.ref]; ok {
			if reg, allocated := result.refToReg[preRef]; allocated {
				result.refToReg[lr.ref] = reg
				active = append(active, activeRange{lr, reg})
				continue
			}
		}

		if len(freeFPR) > 0 {
			reg := freeFPR[0]
			freeFPR = freeFPR[1:]
			result.refToReg[lr.ref] = reg
			active = append(active, activeRange{lr, reg})

			for moveRef, preRef := range coalesced {
				if preRef == lr.ref {
					result.refToReg[moveRef] = reg
				}
			}
		}
	}

	return result
}

// computeFloatLiveRanges computes live ranges for all float-typed SSA refs.
func computeFloatLiveRanges(f *SSAFunc, loopIdx int) []liveRange {
	var ranges []liveRange
	defAt := map[SSARef]int{}     // ref → definition index
	lastUse := map[SSARef]int{}   // ref → last use index
	refSlot := map[SSARef]int16{} // ref → slot

	for i := range f.Insts {
		inst := &f.Insts[i]
		ref := SSARef(i)

		// Record definition for float-typed instructions.
		if inst.Type == SSATypeFloat {
			defAt[ref] = i
			lastUse[ref] = i // at minimum, live at definition
			refSlot[ref] = inst.Slot
		}

		// Record uses.
		if inst.Arg1 != SSARefNone && inst.Arg1 >= 0 {
			if _, ok := defAt[inst.Arg1]; ok {
				if i > lastUse[inst.Arg1] {
					lastUse[inst.Arg1] = i
				}
			}
		}
		if inst.Arg2 != SSARefNone && inst.Arg2 >= 0 {
			if _, ok := defAt[inst.Arg2]; ok {
				if i > lastUse[inst.Arg2] {
					lastUse[inst.Arg2] = i
				}
			}
		}
	}

	// For pre-loop refs used in the loop body, extend their live range
	// to cover the entire function (they're loop-carried).
	maxIdx := len(f.Insts) - 1
	for ref, def := range defAt {
		use := lastUse[ref]
		if def < loopIdx && use >= loopIdx {
			// Pre-loop definition used in loop body → extend to end.
			lastUse[ref] = maxIdx
		}
	}

	for ref, def := range defAt {
		ranges = append(ranges, liveRange{
			ref:   ref,
			start: def,
			end:   lastUse[ref],
			typ:   SSATypeFloat,
			slot:  refSlot[ref],
		})
	}

	return ranges
}

// ────────────────────────────────────────────────────────────────────────────
// RegMap: unified register allocation result
// ────────────────────────────────────────────────────────────────────────────

// RegMap holds the complete register allocation for a compiled trace.
type RegMap struct {
	Int      *slotAlloc      // VM slot → GPR
	Float    *floatSlotAlloc // VM slot → FPR (slot-level)
	FloatRef *floatRefAlloc  // SSA ref → FPR (ref-level, for float operations)
}

// IntReg returns the GPR allocated to a VM slot, if any.
func (m *RegMap) IntReg(slot int) (Reg, bool) {
	if m.Int == nil {
		return 0, false
	}
	return m.Int.getReg(slot)
}

// FloatReg returns the FPR allocated to a VM slot, if any.
func (m *RegMap) FloatReg(slot int) (FReg, bool) {
	if m.Float == nil {
		return 0, false
	}
	return m.Float.getReg(slot)
}

// FloatRefReg returns the FPR allocated to an SSA ref, if any.
func (m *RegMap) FloatRefReg(ref SSARef) (FReg, bool) {
	if m.FloatRef == nil {
		return 0, false
	}
	return m.FloatRef.getReg(ref)
}

// IsAllocated returns true if the given VM slot has any register allocated.
func (m *RegMap) IsAllocated(slot int) bool {
	if _, ok := m.IntReg(slot); ok {
		return true
	}
	if _, ok := m.FloatReg(slot); ok {
		return true
	}
	return false
}

// AllocateRegisters performs register allocation for an SSA function.
// It allocates both slot-based (GPR for ints, FPR for floats) and
// ref-based (FPR for float SSA values) registers.
func AllocateRegisters(f *SSAFunc) *RegMap {
	floatSlot := newFloatSlotAlloc(f)
	// Pass slot-level allocation to ref-level allocator so it avoids conflicting registers
	floatRef := floatRefAllocLRExclude(f, floatSlot)
	return &RegMap{
		Int:      newSlotAlloc(f),
		Float:    floatSlot,
		FloatRef: floatRef,
	}
}
