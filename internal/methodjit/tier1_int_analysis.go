//go:build darwin && arm64

// tier1_int_analysis.go implements forward-scan KnownInt tracking for the
// Tier 1 baseline compiler. For each bytecode PC, it computes the set of VM
// register slots known to hold a NaN-boxed int48, so ADD/SUB/MUL/EQ/LT/LE
// emitters can dispatch to integer-specialized templates without runtime
// dispatch overhead.
//
// Algorithm and rationale: see opt/knowledge/tier1-int-spec.md
//
// Task 1 scope: types and stub only. Task 2 implements computeKnownIntSlots
// and wires it into tier1_compile.go.

package methodjit

import (
	"github.com/gscript/gscript/internal/vm"
)

// knownIntInfo is the result of the forward scan. It stores, for each PC,
// the set of register slots known to hold an int48 *before* that PC executes.
//
// slotSet is a uint64 bitmap (indexed by slot number). For MaxStack > 64 the
// proto is considered ineligible — ack/fib/mutual_recursion all use <10 slots.
type knownIntInfo struct {
	// perPC[pc] is the bitmap of KnownInt slots before pc executes.
	// Len == len(proto.Code).
	perPC []uint64

	// numParams is stored so emitters can look up the param-guard count.
	numParams int
}

// maxTrackedSlots is the limit on MaxStack for int-spec eligibility. Protos
// with more live slots fall back to the generic polymorphic templates.
const maxTrackedSlots = 64

// knownIntAt returns the KnownInt bitmap before the given PC.
func (k *knownIntInfo) knownIntAt(pc int) uint64 {
	if k == nil || pc < 0 || pc >= len(k.perPC) {
		return 0
	}
	return k.perPC[pc]
}

// isKnownIntOperand returns true if the RK-operand idx is statically known
// to be an int at the given PC. For RK constants, it consults the proto's
// constant pool; for registers, it consults the bitmap.
func (k *knownIntInfo) isKnownIntOperand(pc int, idx int, consts []runtimeValue) bool {
	if idx >= vm.RKBit {
		cidx := idx - vm.RKBit
		if cidx < 0 || cidx >= len(consts) {
			return false
		}
		return consts[cidx].isInt
	}
	if idx < 0 || idx >= 64 {
		return false
	}
	return k.knownIntAt(pc)&(uint64(1)<<uint(idx)) != 0
}

// runtimeValue is a thin wrapper over vm.FuncProto's constant pool entry so
// the analysis doesn't pull in the runtime package. Task 2 will fill this in
// using vm.FuncProto.Constants and runtime.Value introspection.
type runtimeValue struct {
	isInt bool
}

// computeKnownIntSlots performs the forward scan. Task 1 returns (nil, false)
// for all protos — this stub exists so tier1_compile.go can be wired
// defensively without enabling any specialization yet.
//
// Task 2 will implement the full algorithm described in
// opt/knowledge/tier1-int-spec.md.
func computeKnownIntSlots(proto *vm.FuncProto) (*knownIntInfo, bool) {
	_ = proto
	return nil, false
}
