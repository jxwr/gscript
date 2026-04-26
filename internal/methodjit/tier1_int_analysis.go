//go:build darwin && arm64

// tier1_int_analysis.go implements forward-scan KnownInt tracking for the
// Tier 1 baseline compiler. For each bytecode PC, it computes the set of VM
// register slots known to hold a NaN-boxed int48, so ADD/SUB/MUL/MOD/EQ/LT/LE
// emitters can dispatch to integer-specialized templates without dispatch
// overhead. Algorithm: see opt/knowledge/tier1-int-spec.md.

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// knownIntInfo is the result of the forward scan. perPC[pc] is the bitmap
// of KnownInt slots before pc executes; bit i is set iff slot i is known
// to hold int48. MaxStack > 64 is ineligible (ack/fib/mutual_recursion all
// use <10 slots).
//
// guardedParams is the bitmap of param slots that the emitter must check
// with a runtime tag-guard at function entry. Only params that are actually
// used as operands of arith/compare ops are guarded; params used as tables
// or callables (quicksort's `arr`) are excluded so their non-int runtime
// type doesn't trigger a spurious deopt.
type knownIntInfo struct {
	perPC         []uint64
	numParams     int
	guardedParams uint64
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
func (k *knownIntInfo) isKnownIntOperand(pc int, idx int, consts []runtime.Value) bool {
	if k == nil {
		return false
	}
	if idx >= vm.RKBit {
		cidx := idx - vm.RKBit
		if cidx < 0 || cidx >= len(consts) {
			return false
		}
		return consts[cidx].IsInt()
	}
	if idx < 0 || idx >= maxTrackedSlots {
		return false
	}
	return k.knownIntAt(pc)&(uint64(1)<<uint(idx)) != 0
}

// setSlot returns b with slot i added.
func setSlot(b uint64, i int) uint64 {
	if i < 0 || i >= maxTrackedSlots {
		return b
	}
	return b | (uint64(1) << uint(i))
}

// clearSlot returns b with slot i removed.
func clearSlot(b uint64, i int) uint64 {
	if i < 0 || i >= maxTrackedSlots {
		return b
	}
	return b &^ (uint64(1) << uint(i))
}

// hasSlot reports whether slot i is present in b.
func hasSlot(b uint64, i int) bool {
	if i < 0 || i >= maxTrackedSlots {
		return false
	}
	return b&(uint64(1)<<uint(i)) != 0
}

// rkIsKnownInt tests whether an RK operand is a known int (register bit set,
// or a constant pool entry that IsInt).
func rkIsKnownInt(b uint64, idx int, consts []runtime.Value) bool {
	if idx >= vm.RKBit {
		cidx := idx - vm.RKBit
		if cidx < 0 || cidx >= len(consts) {
			return false
		}
		return consts[cidx].IsInt()
	}
	return hasSlot(b, idx)
}

// computeKnownIntSlots performs the forward linear scan. Returns (nil, false)
// if the proto is ineligible. Eligibility gate: (1) MaxStack > 64,
// (2) any blacklisted op (CONCAT/LEN/POW/DIV/CLOSURE/GETFIELD/SETFIELD/
// SELF/VARARG/TFORCALL/TFORLOOP/MAKECHAN/SEND/RECV/GO), (3) any OP_LOADK of
// a non-int constant, (4) any ADD/SUB/MUL/MOD/EQ/LT/LE RK operand that is a
// non-int constant, (5) any instruction writes a parameter slot with a
// value not known to be int.
func computeKnownIntSlots(proto *vm.FuncProto) (*knownIntInfo, bool) {
	if proto == nil {
		return nil, false
	}
	if proto.MaxStack > maxTrackedSlots {
		return nil, false
	}
	if proto.NumParams < 0 || proto.NumParams > maxTrackedSlots {
		return nil, false
	}

	consts := proto.Constants
	code := proto.Code

	// Eligibility gate (pre-scan).
	for _, inst := range code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CONCAT, vm.OP_LEN, vm.OP_POW, vm.OP_DIV,
			vm.OP_CLOSURE, vm.OP_GETFIELD, vm.OP_SETFIELD, vm.OP_SELF,
			vm.OP_VARARG, vm.OP_TFORCALL, vm.OP_TFORLOOP,
			vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV, vm.OP_GO:
			return nil, false
		case vm.OP_LOADK:
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(consts) || !consts[bx].IsInt() {
				return nil, false
			}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			b := vm.DecodeB(inst)
			c := vm.DecodeC(inst)
			if b >= vm.RKBit {
				cidx := b - vm.RKBit
				if cidx < 0 || cidx >= len(consts) || !consts[cidx].IsInt() {
					return nil, false
				}
			}
			if c >= vm.RKBit {
				cidx := c - vm.RKBit
				if cidx < 0 || cidx >= len(consts) || !consts[cidx].IsInt() {
					return nil, false
				}
			}
		}
	}

	// Classify params by how they're used in the body:
	//   arithUse   — appears as B or C of ADD/SUB/MUL/MOD/EQ/LT/LE (as a register)
	//   nonIntUse  — appears as the table slot in GETTABLE/SETTABLE/SETLIST/APPEND
	//                or as the callable in CALL
	// A param with both classifications is inconsistent (can't be int AND table);
	// the proto is ineligible. A param used only as non-int is excluded from
	// the seed and not guarded at entry — that's exactly quicksort's `arr`.
	var arithUse, nonIntUse uint64
	for _, inst := range code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		switch op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if b < vm.RKBit && b < proto.NumParams {
				arithUse = setSlot(arithUse, b)
			}
			if c < vm.RKBit && c < proto.NumParams {
				arithUse = setSlot(arithUse, c)
			}
		case vm.OP_GETTABLE:
			if b < proto.NumParams {
				nonIntUse = setSlot(nonIntUse, b)
			}
		case vm.OP_SETTABLE, vm.OP_SETLIST, vm.OP_APPEND:
			if a < proto.NumParams {
				nonIntUse = setSlot(nonIntUse, a)
			}
		case vm.OP_CALL:
			if a < proto.NumParams {
				nonIntUse = setSlot(nonIntUse, a)
			}
		}
	}
	if arithUse&nonIntUse != 0 {
		return nil, false
	}

	// Build initial paramSet: only the arith-used param slots. This is also
	// the set of slots the param-entry guard will runtime-check.
	paramSet := arithUse

	// Pre-pass: record branch targets.
	branchTargets := make(map[int]bool)
	for pc, inst := range code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_JMP:
			tgt := pc + 1 + vm.DecodesBx(inst)
			if tgt >= 0 && tgt < len(code) {
				branchTargets[tgt] = true
			}
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
			tgt := pc + 2
			if tgt >= 0 && tgt < len(code) {
				branchTargets[tgt] = true
			}
		case vm.OP_FORPREP, vm.OP_FORLOOP:
			tgt := pc + 1 + vm.DecodesBx(inst)
			if tgt >= 0 && tgt < len(code) {
				branchTargets[tgt] = true
			}
		}
	}

	info := &knownIntInfo{
		perPC:         make([]uint64, len(code)),
		numParams:     proto.NumParams,
		guardedParams: paramSet,
	}

	known := paramSet
	for pc, inst := range code {
		// At branch targets, reset to paramSet: params survive across
		// branches because the eligibility gate (check below) rejects any
		// proto that writes a non-int value into a param slot.
		if pc > 0 && branchTargets[pc] {
			known = paramSet
		}
		info.perPC[pc] = known

		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		switch op {
		case vm.OP_LOADINT:
			known = setSlot(known, a)
		case vm.OP_LOADK:
			bx := vm.DecodeBx(inst)
			if bx >= 0 && bx < len(consts) && consts[bx].IsInt() {
				known = setSlot(known, a)
			} else {
				known = clearSlot(known, a)
			}
		case vm.OP_LOADBOOL:
			known = clearSlot(known, a)
		case vm.OP_LOADNIL:
			// Clears R(A)..R(A+B).
			for i := a; i <= a+b && i < maxTrackedSlots; i++ {
				known = clearSlot(known, i)
			}
		case vm.OP_MOVE:
			if hasSlot(known, b) {
				known = setSlot(known, a)
			} else {
				known = clearSlot(known, a)
			}
		case vm.OP_GETGLOBAL, vm.OP_GETUPVAL, vm.OP_NEWTABLE, vm.OP_GETTABLE:
			known = clearSlot(known, a)
		case vm.OP_SETGLOBAL, vm.OP_SETUPVAL, vm.OP_SETTABLE,
			vm.OP_SETLIST, vm.OP_APPEND:
			// No register destination written (A holds the table / source).
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
			if rkIsKnownInt(known, b, consts) && rkIsKnownInt(known, c, consts) {
				known = setSlot(known, a)
			} else {
				known = clearSlot(known, a)
			}
		case vm.OP_UNM:
			if hasSlot(known, b) {
				known = setSlot(known, a)
			} else {
				known = clearSlot(known, a)
			}
		case vm.OP_NOT:
			known = clearSlot(known, a)
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET,
			vm.OP_JMP, vm.OP_RETURN, vm.OP_CLOSE:
			// No register write we need to track (TESTSET writes A only when
			// the test passes — conservatively clear A).
			if op == vm.OP_TESTSET {
				known = clearSlot(known, a)
			}
		case vm.OP_CALL:
			// C == 1 → no return values. C >= 2 → C-1 return values at A..A+C-2.
			// C == 0 → variadic "use top", conservatively clear from A to end.
			if c == 0 {
				for i := a; i < maxTrackedSlots; i++ {
					known = clearSlot(known, i)
				}
			} else {
				// Always clear A (callable slot is overwritten).
				known = clearSlot(known, a)
				for i := a; i <= a+c-2 && i < maxTrackedSlots; i++ {
					known = clearSlot(known, i)
				}
			}
		case vm.OP_FORPREP:
			known = clearSlot(known, a)
		case vm.OP_FORLOOP:
			known = clearSlot(known, a)
			known = clearSlot(known, a+3)
		default:
			// Unknown op: conservative — clear A if in tracked range.
			// (Blacklisted ops were rejected in the eligibility gate.)
			known = clearSlot(known, a)
		}

		// Parameter-slot invariant: if an instruction with a destination in A
		// writes a param slot and the result is not known-int, the proto is
		// ineligible (breaks the branch-target reset invariant).
		if writesSlotA(op) && a < proto.NumParams && a < maxTrackedSlots {
			if !hasSlot(known, a) {
				return nil, false
			}
		}
	}

	return info, true
}

// writesSlotA reports whether the opcode writes a value into register slot A.
// Used to enforce the "params stay int" invariant during the forward scan.
func writesSlotA(op vm.Opcode) bool {
	switch op {
	case vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_LOADINT, vm.OP_LOADK,
		vm.OP_MOVE, vm.OP_GETGLOBAL, vm.OP_GETUPVAL, vm.OP_NEWTABLE,
		vm.OP_GETTABLE, vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_UNM,
		vm.OP_NOT, vm.OP_CALL, vm.OP_TESTSET, vm.OP_FORPREP, vm.OP_FORLOOP:
		return true
	}
	return false
}
