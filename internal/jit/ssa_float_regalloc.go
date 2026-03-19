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
		for _, argRef := range []SSARef{inst.Arg1, inst.Arg2} {
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
