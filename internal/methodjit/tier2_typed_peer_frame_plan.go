//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sort"
)

// Tier2TypedPeerFramePlan describes whether a typed peer entry may use a thin
// JIT-to-JIT frame. It is deliberately separate from ABI eligibility: a callee
// can have a valid typed table/int ABI while still requiring the conservative
// full frame for register preservation and runtime unwinding.
type Tier2TypedPeerFramePlan struct {
	CanUseThinEntry      bool     `json:"can_use_thin_entry"`
	RequiredGPRs         []int    `json:"required_gprs,omitempty"`
	RequiredFPRs         []int    `json:"required_fprs,omitempty"`
	FullFrameBytes       int      `json:"full_frame_bytes,omitempty"`
	CompactFrameBytes    int      `json:"compact_frame_bytes,omitempty"`
	EstimatedSavedStores int      `json:"estimated_saved_stores,omitempty"`
	EstimatedSavedLoads  int      `json:"estimated_saved_loads,omitempty"`
	Reasons              []string `json:"reasons,omitempty"`
}

func AnalyzeTypedPeerFramePlan(fn *Function, alloc *RegAllocation, abi TypedSelfABI) Tier2TypedPeerFramePlan {
	plan := Tier2TypedPeerFramePlan{}
	if fn == nil || fn.Proto == nil {
		plan.addReason("missing proto")
		return plan
	}
	if !abi.Eligible {
		plan.addReason("typed peer ABI is not eligible")
		return plan
	}
	if !fn.Proto.LeafNoCall {
		plan.addReason("callee bytecode is not leaf")
	}
	if irHasNestedCallLike(fn) {
		plan.addReason("optimized IR contains nested call-like op")
	}
	if alloc == nil {
		plan.addReason("missing register allocation")
		return plan
	}
	plan.FullFrameBytes = frameSize
	plan.RequiredGPRs = typedPeerAllocatedCalleeSavedGPRs(alloc)
	plan.RequiredFPRs = typedPeerAllocatedCalleeSavedFPRs(alloc)
	plan.CompactFrameBytes = typedPeerCompactFrameBytes(plan.RequiredGPRs, plan.RequiredFPRs)
	plan.EstimatedSavedStores = typedPeerFullFrameSaveOps() - typedPeerCompactFrameSaveOps(plan.RequiredGPRs, plan.RequiredFPRs)
	plan.EstimatedSavedLoads = plan.EstimatedSavedStores
	if regs := plan.RequiredGPRs; len(regs) > 0 {
		plan.addReason(fmt.Sprintf("allocated callee-saved GPRs %v", regs))
	}
	if regs := plan.RequiredFPRs; len(regs) > 0 {
		plan.addReason(fmt.Sprintf("allocated callee-saved FPRs %v", regs))
	}
	// Current executable JIT blocks are entered from Go and can appear in fault
	// stacks. Until thin JIT frames have an explicit unwind-safe protocol, keep
	// publishing the conservative full-frame typed entry.
	plan.addReason("thin typed peer entry lacks unwind-safe frame protocol")
	plan.CanUseThinEntry = len(plan.Reasons) == 0
	return plan
}

func (p *Tier2TypedPeerFramePlan) addReason(reason string) {
	if reason != "" {
		p.Reasons = append(p.Reasons, reason)
	}
}

func typedPeerAllocatedCalleeSavedGPRs(alloc *RegAllocation) []int {
	if alloc == nil {
		return nil
	}
	seen := make(map[int]bool)
	for _, pr := range alloc.ValueRegs {
		if pr.IsFloat {
			continue
		}
		switch pr.Reg {
		case 20, 21, 22, 23, 28:
			seen[pr.Reg] = true
		}
	}
	return sortedIntKeys(seen)
}

func typedPeerCompactFrameBytes(gprs, fprs []int) int {
	return typedPeerActualFrameBytes(gprs, fprs)
}

func typedPeerActualFrameBytes(gprs, fprs []int) int {
	// Keep FP/LR at SP+0 for conventional native return chains. GPR and FPR
	// saves are each pair-packed from SP+16, with class alignment matching the
	// typed entry emitter.
	off := 16
	off += ((len(gprs) + 1) / 2) * 16
	off = (off + 15) &^ 15
	off += ((len(fprs) + 1) / 2) * 16
	return (off + 15) &^ 15
}

func typedPeerFullFrameSaveOps() int {
	// Full Tier 2 direct/typed entry:
	// STP FP/LR + five GPR STPs + two FPR STPs.
	return 8
}

func typedPeerCompactFrameSaveOps(gprs, fprs []int) int {
	// One FP/LR STP, plus enough pair stores to cover each saved class.
	return 1 + (len(gprs)+1)/2 + (len(fprs)+1)/2
}

func typedPeerAllocatedCalleeSavedFPRs(alloc *RegAllocation) []int {
	if alloc == nil {
		return nil
	}
	seen := make(map[int]bool)
	for _, pr := range alloc.ValueRegs {
		if !pr.IsFloat {
			continue
		}
		if pr.Reg >= 8 && pr.Reg <= 15 {
			seen[pr.Reg] = true
		}
	}
	return sortedIntKeys(seen)
}

func sortedIntKeys(values map[int]bool) []int {
	if len(values) == 0 {
		return nil
	}
	out := make([]int, 0, len(values))
	for v := range values {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}
