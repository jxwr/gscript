//go:build darwin && arm64

package jit

import "sort"

// liveInterval represents the live range of a float SSA value.
type liveInterval struct {
	ref   SSARef // SSA instruction index
	slot  int    // VM slot (-1 if no slot)
	start int    // first definition (instruction index)
	end   int    // last use (instruction index)
}

// floatRefAlloc maps SSA refs to D registers (ref-level allocation).
// Unlike floatSlotAlloc which maps VM slots, this maps individual SSA values.
// Multiple SSA refs that share the same VM slot can get different D registers
// when their live ranges don't overlap.
type floatRefAlloc struct {
	refToReg map[SSARef]FReg
}

// getReg returns the D register allocated for an SSA ref, or (0, false).
func (fra *floatRefAlloc) getReg(ref SSARef) (FReg, bool) {
	r, ok := fra.refToReg[ref]
	return r, ok
}

// floatRefAllocLR performs ref-level live-range-based float register allocation.
// It computes live intervals per SSA ref (not per slot) and assigns D4-D11
// using linear scan. This enables multiple temporaries from the same VM slot
// to live in different registers simultaneously.
//
// Loop-carried value coalescing: when a MOVE writes to a slot that has a
// pre-loop ref, the MOVE ref is forced to use the same D register as the
// pre-loop ref (because on the next iteration, the code reads via the pre-loop
// ref's register).
func floatRefAllocLR(f *SSAFunc) *floatRefAlloc {
	fra := &floatRefAlloc{
		refToReg: make(map[SSARef]FReg),
	}

	if f == nil || len(f.Insts) == 0 {
		return fra
	}

	// Find LOOP marker
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return fra
	}

	// Step 1: Find which refs produce float values
	isFloatRef := make(map[SSARef]bool)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		// Skip absorbed MULs — they are not emitted by codegen,
		// so their D register would never be written.
		if f.AbsorbedMuls[SSARef(i)] {
			continue
		}
		inst := &f.Insts[i]
		if isFloatOp(inst.Op) || (inst.Type == SSATypeFloat && isValueProducingOp(inst.Op)) {
			isFloatRef[SSARef(i)] = true
		}
	}
	// Also mark pre-loop float refs (UNBOX_FLOAT, LOAD_SLOT with float type)
	for i := 0; i <= loopIdx; i++ {
		inst := &f.Insts[i]
		if inst.Type == SSATypeFloat && (inst.Op == SSA_UNBOX_FLOAT || inst.Op == SSA_LOAD_SLOT) {
			isFloatRef[SSARef(i)] = true
		}
	}

	// Step 2: Count uses of each float ref within the loop body
	refLastUse := make(map[SSARef]int)  // ref → last instruction index that uses it
	refUseCount := make(map[SSARef]int) // ref → number of uses

	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		for _, argRef := range []SSARef{inst.Arg1, inst.Arg2} {
			if argRef < 0 || int(argRef) >= len(f.Insts) {
				continue
			}
			if isFloatRef[argRef] {
				refUseCount[argRef]++
				if i > refLastUse[argRef] {
					refLastUse[argRef] = i
				}
			}
		}
		// FMADD/FMSUB store a third operand ref in AuxInt — track it as a use
		if inst.Op == SSA_FMADD || inst.Op == SSA_FMSUB {
			auxRef := SSARef(inst.AuxInt)
			if auxRef >= 0 && int(auxRef) < len(f.Insts) && isFloatRef[auxRef] {
				refUseCount[auxRef]++
				if i > refLastUse[auxRef] {
					refLastUse[auxRef] = i
				}
			}
		}
	}

	// Step 3: Identify loop-carried slots and build coalescing constraints.
	// A slot is loop-carried if a pre-loop ref for that slot is used in the
	// loop body. The MOVE that writes to that slot at the end of the loop
	// body must use the SAME register as the pre-loop ref.
	preLoopSlotRef := make(map[int]SSARef) // slot → pre-loop ref
	for i := 0; i <= loopIdx; i++ {
		ref := SSARef(i)
		if !isFloatRef[ref] {
			continue
		}
		if refUseCount[ref] == 0 {
			continue
		}
		inst := &f.Insts[i]
		slot := int(inst.Slot)
		if slot >= 0 {
			preLoopSlotRef[slot] = ref
		}
	}

	// Find the LAST float ref that writes to each loop-carried slot.
	// coalesceWith maps that ref → pre-loop ref (they must share a register).
	// This handles both MOVE (e.g., zr = tr) and direct writes (e.g., sum = sum + x).
	lastWriterForSlot := make(map[int]SSARef)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if !isFloatRef[SSARef(i)] && !(inst.Op == SSA_MOVE && inst.Type == SSATypeFloat) {
			continue
		}
		slot := int(inst.Slot)
		if slot >= 0 {
			if _, ok := preLoopSlotRef[slot]; ok {
				lastWriterForSlot[slot] = SSARef(i)
				isFloatRef[SSARef(i)] = true
			}
		}
	}
	coalesceWith := make(map[SSARef]SSARef)
	for slot, lastRef := range lastWriterForSlot {
		if preLoopRef, ok := preLoopSlotRef[slot]; ok {
			coalesceWith[lastRef] = preLoopRef
		}
	}

	// Step 4: Build intervals
	var intervals []liveInterval

	// Pre-loop refs used in the loop body: live range spans entire loop
	for i := 0; i <= loopIdx; i++ {
		ref := SSARef(i)
		if !isFloatRef[ref] {
			continue
		}
		if refUseCount[ref] == 0 {
			continue
		}
		inst := &f.Insts[i]
		slot := int(inst.Slot)

		intervals = append(intervals, liveInterval{
			ref:   ref,
			slot:  slot,
			start: loopIdx + 1,
			end:   len(f.Insts) - 1,
		})
	}

	// Loop body refs
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		ref := SSARef(i)
		if !isFloatRef[ref] {
			continue
		}
		inst := &f.Insts[i]
		slot := int(inst.Slot)

		defPos := i
		endPos := defPos
		if lastUse, ok := refLastUse[ref]; ok {
			endPos = lastUse
		}

		// Constants in the loop body can be hoisted before the loop.
		// Extend their live range to the entire loop body so their register
		// is not reused, enabling safe constant hoisting in the codegen.
		if inst.Op == SSA_CONST_FLOAT && refUseCount[ref] > 0 {
			defPos = loopIdx + 1
			endPos = len(f.Insts) - 1
		}

		// Skip refs with no uses AND no slot write (pure dead values).
		// Keep refs that write to a slot (even without direct SSA uses),
		// because the value persists in the slot for store-back or next iteration.
		if refUseCount[ref] == 0 && slot < 0 {
			continue
		}

		// Coalesced MOVE refs: they don't need their own interval because
		// they'll be forced to use their pre-loop ref's register.
		// But we still include them to track usage.
		if _, isCoalesced := coalesceWith[ref]; isCoalesced {
			// Don't add an interval — this ref will be assigned the same
			// register as the pre-loop ref via coalescing.
			continue
		}

		intervals = append(intervals, liveInterval{
			ref:   ref,
			slot:  slot,
			start: defPos,
			end:   endPos,
		})
	}

	// Sort by start position, then by length (longer first) for tie-breaking
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return (intervals[i].end - intervals[i].start) > (intervals[j].end - intervals[j].start)
	})

	// Step 5: Linear scan allocation using D4-D11
	type activeInterval struct {
		interval liveInterval
		reg      FReg
	}
	var active []activeInterval
	freeRegs := make([]FReg, len(allocableFloatRegs))
	copy(freeRegs, allocableFloatRegs)

	for _, iv := range intervals {
		// Expire old intervals whose end is before this interval's start
		newActive := active[:0]
		for _, a := range active {
			if a.interval.end < iv.start {
				freeRegs = append(freeRegs, a.reg)
			} else {
				newActive = append(newActive, a)
			}
		}
		active = newActive

		if len(freeRegs) > 0 {
			reg := freeRegs[len(freeRegs)-1]
			freeRegs = freeRegs[:len(freeRegs)-1]
			fra.refToReg[iv.ref] = reg
			active = append(active, activeInterval{iv, reg})
		} else if len(active) > 0 {
			// Spill: find the active interval with the furthest end point
			spillIdx := 0
			for i, a := range active {
				if a.interval.end > active[spillIdx].interval.end {
					spillIdx = i
				}
			}
			if active[spillIdx].interval.end > iv.end {
				spillReg := active[spillIdx].reg
				spillRef := active[spillIdx].interval.ref
				delete(fra.refToReg, spillRef)
				fra.refToReg[iv.ref] = spillReg
				active[spillIdx] = activeInterval{iv, spillReg}
			}
		}
	}

	// Step 6: Apply coalescing constraints.
	// For each coalesced MOVE ref, assign it the same register as its pre-loop ref.
	for moveRef, preLoopRef := range coalesceWith {
		if dreg, ok := fra.refToReg[preLoopRef]; ok {
			fra.refToReg[moveRef] = dreg
		}
	}

	return fra
}

// floatRegAllocLR performs live-range-based float register allocation.
// Returns a floatSlotAlloc with slot-to-register mappings optimized
// to minimize spills by packing non-overlapping intervals into registers.
func floatRegAllocLR(f *SSAFunc) *floatSlotAlloc {
	fa := &floatSlotAlloc{
		slotToReg: make(map[int]FReg),
		regToSlot: make(map[FReg]int),
	}

	if f == nil || len(f.Insts) == 0 {
		return fa
	}

	// Find LOOP marker
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return fa
	}

	// Step 1: Compute live intervals for float slots in the loop body.
	// We track by VM slot (not SSA ref) because the register allocator
	// maps slots to registers.
	slotFirst := make(map[int]int)  // slot → first appearance (def or use)
	slotLast := make(map[int]int)   // slot → last appearance (def or use)
	slotCount := make(map[int]int)  // slot → number of appearances

	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		// Check if this instruction defines a float slot
		if isFloatOp(inst.Op) || inst.Type == SSATypeFloat {
			slot := int(inst.Slot)
			if slot >= 0 {
				if _, ok := slotFirst[slot]; !ok {
					slotFirst[slot] = i
				}
				slotLast[slot] = i
				slotCount[slot]++
			}
		}

		// Check operand slots (uses)
		operands := []SSARef{inst.Arg1, inst.Arg2}
		// FMADD/FMSUB store a third operand ref in AuxInt
		if inst.Op == SSA_FMADD || inst.Op == SSA_FMSUB {
			operands = append(operands, SSARef(inst.AuxInt))
		}
		for _, argRef := range operands {
			if argRef < 0 || int(argRef) >= len(f.Insts) {
				continue
			}
			argInst := &f.Insts[argRef]
			if argInst.Type == SSATypeFloat || isFloatOp(argInst.Op) {
				slot := int(argInst.Slot)
				if slot >= 0 {
					if _, ok := slotFirst[slot]; !ok {
						slotFirst[slot] = int(argRef)
					}
					if i > slotLast[slot] {
						slotLast[slot] = i
					}
					slotCount[slot]++
				}
			}
		}
	}

	// Step 2: Build sorted list of live intervals
	var intervals []liveInterval
	for slot, first := range slotFirst {
		last := slotLast[slot]
		if slotCount[slot] < 2 {
			continue // single-use, not worth allocating
		}
		intervals = append(intervals, liveInterval{
			slot:  slot,
			start: first,
			end:   last,
		})
	}

	// Sort by start position (ascending), then by length (descending) for tie-breaking
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return (intervals[i].end - intervals[i].start) > (intervals[j].end - intervals[j].start)
	})

	// Step 3: Linear scan allocation
	// Available registers: D4-D11 (8 registers)
	type activeInterval struct {
		interval liveInterval
		reg      FReg
	}
	var active []activeInterval
	freeRegs := make([]FReg, len(allocableFloatRegs))
	copy(freeRegs, allocableFloatRegs)

	for _, iv := range intervals {
		// Expire old intervals whose end is before this interval's start
		newActive := active[:0]
		for _, a := range active {
			if a.interval.end < iv.start {
				// This interval expired — free its register
				freeRegs = append(freeRegs, a.reg)
			} else {
				newActive = append(newActive, a)
			}
		}
		active = newActive

		if len(freeRegs) > 0 {
			// Allocate a register
			reg := freeRegs[len(freeRegs)-1]
			freeRegs = freeRegs[:len(freeRegs)-1]
			fa.slotToReg[iv.slot] = reg
			fa.regToSlot[reg] = iv.slot
			active = append(active, activeInterval{iv, reg})
		} else if len(active) > 0 {
			// Spill: find the active interval with the furthest end point
			spillIdx := 0
			for i, a := range active {
				if a.interval.end > active[spillIdx].interval.end {
					spillIdx = i
				}
			}
			// Only spill if the spill candidate ends later than the current interval
			if active[spillIdx].interval.end > iv.end {
				// Spill the longest-living interval, give its register to the new one
				spillReg := active[spillIdx].reg
				spillSlot := active[spillIdx].interval.slot
				delete(fa.slotToReg, spillSlot)
				delete(fa.regToSlot, spillReg)

				fa.slotToReg[iv.slot] = spillReg
				fa.regToSlot[spillReg] = iv.slot

				active[spillIdx] = activeInterval{iv, spillReg}
			}
			// else: current interval is longer, don't allocate (it spills)
		}
	}

	return fa
}
