package jit

import "github.com/gscript/gscript/internal/vm"

// Trace register allocator: maps hot VM registers to ARM64 physical registers.
//
// Available ARM64 registers for allocation: X20, X21, X22, X23, X24 (5 total).
// Reserved: X19 (trCtx), X25 (self-call depth), X26 (regRegs), X27 (regConsts).
//
// Algorithm: simple frequency-based allocation.
// 1. Count how many times each VM register is read/written in the trace
// 2. Assign the top N most-used VM registers to physical registers
// 3. Generate spill/reload code at trace entry/exit

// allocableRegs are the ARM64 registers available for trace register allocation.
var allocableRegs = []Reg{X20, X21, X22, X23, X24}

const maxAllocRegs = 5

// RegAlloc holds the register allocation for a trace.
type RegAlloc struct {
	// VMReg → ARM64 register mapping (only for allocated registers)
	Mapping map[int]Reg
	// Reverse: ARM64 register → VM register
	Reverse map[Reg]int
	// Frequency count per VM register
	Freq map[int]int
}

// NewRegAlloc creates a register allocation for a trace.
func NewRegAlloc(trace *Trace) *RegAlloc {
	ra := &RegAlloc{
		Mapping: make(map[int]Reg),
		Reverse: make(map[Reg]int),
		Freq:    make(map[int]int),
	}

	// Count register usage frequency — ONLY for integer arithmetic ops.
	// Registers used in table operations must NOT be allocated (they hold
	// non-integer Values like tables, strings, etc.)
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_UNM,
			vm.OP_FORLOOP, vm.OP_FORPREP,
			vm.OP_LOADINT, vm.OP_MOVE,
			vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if ir.B < 256 {
				ra.Freq[ir.B]++
			}
			if ir.C < 256 {
				ra.Freq[ir.C]++
			}
			ra.Freq[ir.A]++
		}
	}

	// Find the top N most-used VM registers
	type regFreq struct {
		vmReg int
		count int
	}
	var sorted []regFreq
	for reg, count := range ra.Freq {
		sorted = append(sorted, regFreq{reg, count})
	}
	// Simple selection sort (N is small)
	for i := 0; i < len(sorted) && i < maxAllocRegs; i++ {
		maxIdx := i
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[maxIdx].count {
				maxIdx = j
			}
		}
		sorted[i], sorted[maxIdx] = sorted[maxIdx], sorted[i]
	}

	// Assign physical registers to the top VM registers
	// Only allocate registers used more than 2 times (worth the spill/reload)
	for i := 0; i < len(sorted) && i < maxAllocRegs; i++ {
		if sorted[i].count < 3 {
			break
		}
		vmReg := sorted[i].vmReg
		armReg := allocableRegs[i]
		ra.Mapping[vmReg] = armReg
		ra.Reverse[armReg] = vmReg
	}

	return ra
}

// IsAllocated returns true if the VM register has a physical register.
func (ra *RegAlloc) IsAllocated(vmReg int) bool {
	_, ok := ra.Mapping[vmReg]
	return ok
}

// Get returns the ARM64 register for a VM register, or 0 if not allocated.
func (ra *RegAlloc) Get(vmReg int) (Reg, bool) {
	r, ok := ra.Mapping[vmReg]
	return r, ok
}

// Count returns the number of allocated registers.
func (ra *RegAlloc) Count() int {
	return len(ra.Mapping)
}
