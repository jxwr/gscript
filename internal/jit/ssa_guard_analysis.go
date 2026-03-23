package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// computeLiveIn determines which VM slots are "live-in" at the loop header:
// their value from the previous iteration is read before being overwritten.
// Only these slots need pre-loop type guards.
//
// For traces containing non-numeric ops (GETTABLE, SETTABLE, GETFIELD,
// SETFIELD, GETGLOBAL, CALL), this function falls back to the old per-slot
// WBR analysis (isWrittenBeforeFirstRead / isWrittenBeforeFirstReadExt)
// to avoid exposing codegen bugs with type-conflicting slot reuse.
//
// For purely numeric traces, it uses a clean forward scan where ANY write
// (arithmetic, MOVE, loads, FORLOOP) kills liveness. This handles the
// nbody case where dead float slots held stale values from the previous
// iteration, causing false guard failures.
//
// Returns three maps:
//   - liveIn: set of slot numbers that are live at the loop header
//   - slotRuntimeType: the expected runtime.ValueType for each live-in slot
//   - slotPC: the bytecode PC to associate with each guard (first read site)
func computeLiveIn(trace *Trace) (liveIn map[int]bool, slotRuntimeType map[int]runtime.ValueType, slotPC map[int]int) {
	liveIn = make(map[int]bool)
	slotRuntimeType = make(map[int]runtime.ValueType)
	slotPC = make(map[int]int)

	// Check if the trace has non-numeric ops.
	hasNonNumeric := false
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_GETGLOBAL, vm.OP_CALL:
			hasNonNumeric = true
		}
		if hasNonNumeric {
			break
		}
	}

	if hasNonNumeric {
		// For traces with table/field/global/call ops, use legacy per-opcode
		// guard collection. The liveness-based approach needs in-loop guards
		// for all table ops, which currently crashes on some patterns.
		computeLiveInLegacy(trace, liveIn, slotRuntimeType, slotPC)
		return
	}

	// Purely numeric traces: forward liveness scan.
	// Float slots always remain live-in: D register allocator needs pre-loop load.
	floatSlots := make(map[int]bool)
	for _, ir := range trace.IR {
		if ir.BType == runtime.TypeFloat && ir.B < 256 {
			floatSlots[ir.B] = true
		}
		if ir.CType == runtime.TypeFloat && ir.C < 256 {
			floatSlots[ir.C] = true
		}
	}

	written := make(map[int]bool)

	for _, ir := range trace.IR {
		// Check reads first
		readSlots := getReadSlots(&ir)
		for _, rs := range readSlots {
			if !written[rs.slot] && !liveIn[rs.slot] {
				liveIn[rs.slot] = true
				slotRuntimeType[rs.slot] = rs.typ
				slotPC[rs.slot] = ir.PC
			}
		}
		// Only constant loads and FORLOOP/FORPREP kill liveness.
		// These produce values of a known, fixed type (int for FORLOOP,
		// constant type for LOADK/LOADINT). Other writes (GETGLOBAL,
		// GETFIELD, arithmetic) may produce values of types that conflict
		// with what a later instruction expects.
		switch ir.Op {
		case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL:
			if !floatSlots[ir.A] {
				written[ir.A] = true
			}
		case vm.OP_FORLOOP:
			if !floatSlots[ir.A] {
				written[ir.A] = true
			}
			if !floatSlots[ir.A+3] {
				written[ir.A+3] = true
			}
		case vm.OP_FORPREP:
			if !floatSlots[ir.A] {
				written[ir.A] = true
			}
		}
	}

	return
}

// computeLiveInLegacy collects live-in slots using the old per-opcode logic.
// This matches the old Phase 2 behavior exactly, including the WBR checks.
//
// For traces with inlined function calls (Depth > 0 instructions), slots that
// are ONLY referenced at Depth > 0 are callee-internal temporaries. These slots
// are recomputed from scratch every iteration by the inlined function body, so
// their values at the loop header are irrelevant. Guards for these slots are
// skipped to prevent false guard failures (the slots may hold nil or stale
// values at trace entry that don't match the expected type).
func computeLiveInLegacy(trace *Trace, liveIn map[int]bool, slotRuntimeType map[int]runtime.ValueType, slotPC map[int]int) {
	// Phase 0: Identify callee-only slots (slots only referenced at Depth > 0).
	// These are inlined function internal registers that don't need pre-loop guards.
	calleeOnlySlots := computeCalleeOnlySlots(trace)

	// Build a temporary ssaBuilder just for the WBR checks
	tmpBuilder := &ssaBuilder{
		trace:    trace,
		slotDefs: make(map[int]SSARef),
		slotType: make(map[int]SSAType),
	}
	// Phase 1 from old code: set slot types
	for _, ir := range trace.IR {
		if ir.BType == runtime.TypeInt {
			tmpBuilder.slotType[ir.B] = SSATypeInt
		} else if ir.BType == runtime.TypeFloat {
			tmpBuilder.slotType[ir.B] = SSATypeFloat
		}
		if ir.CType == runtime.TypeInt {
			tmpBuilder.slotType[ir.C] = SSATypeInt
		} else if ir.CType == runtime.TypeFloat {
			tmpBuilder.slotType[ir.C] = SSATypeFloat
		}
	}

	seen := make(map[int]bool) // tracks which slots have been considered (like guardedSlots)
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV,
			vm.OP_LT, vm.OP_LE:
			if ir.B < 256 && !seen[ir.B] && !calleeOnlySlots[ir.B] {
				if !tmpBuilder.isWrittenBeforeFirstRead(ir.B) {
					liveIn[ir.B] = true
					slotRuntimeType[ir.B] = ir.BType
					slotPC[ir.B] = ir.PC
				}
				seen[ir.B] = true
			}
			if ir.C < 256 && !seen[ir.C] && !calleeOnlySlots[ir.C] {
				if !tmpBuilder.isWrittenBeforeFirstRead(ir.C) {
					liveIn[ir.C] = true
					slotRuntimeType[ir.C] = ir.CType
					slotPC[ir.C] = ir.PC
				}
				seen[ir.C] = true
			}
		case vm.OP_UNM:
			if ir.B < 256 && !seen[ir.B] && !calleeOnlySlots[ir.B] {
				if !tmpBuilder.isWrittenBeforeFirstRead(ir.B) {
					liveIn[ir.B] = true
					slotRuntimeType[ir.B] = ir.BType
					slotPC[ir.B] = ir.PC
				}
				seen[ir.B] = true
			}
		case vm.OP_GETTABLE, vm.OP_SETTABLE:
			tableSlot := ir.B
			if ir.Op == vm.OP_SETTABLE {
				tableSlot = ir.A
			}
			if tableSlot < 256 && !seen[tableSlot] && !calleeOnlySlots[tableSlot] {
				if !tmpBuilder.isWrittenBeforeFirstReadExt(tableSlot) {
					liveIn[tableSlot] = true
					slotRuntimeType[tableSlot] = runtime.TypeTable
					slotPC[tableSlot] = ir.PC
				}
				seen[tableSlot] = true
			}
			keySlot := ir.C
			if ir.Op == vm.OP_SETTABLE {
				keySlot = ir.B
			}
			if keySlot < 256 && !seen[keySlot] && !calleeOnlySlots[keySlot] {
				if !tmpBuilder.isWrittenBeforeFirstReadExt(keySlot) {
					liveIn[keySlot] = true
					slotRuntimeType[keySlot] = ir.CType
					slotPC[keySlot] = ir.PC
				}
				seen[keySlot] = true
			}
		case vm.OP_GETFIELD:
			if ir.B < 256 && !seen[ir.B] && !calleeOnlySlots[ir.B] {
				if !tmpBuilder.isWrittenBeforeFirstReadExt(ir.B) {
					liveIn[ir.B] = true
					slotRuntimeType[ir.B] = runtime.TypeTable
					slotPC[ir.B] = ir.PC
				}
				seen[ir.B] = true
			}
		case vm.OP_SETFIELD:
			if ir.A < 256 && !seen[ir.A] && !calleeOnlySlots[ir.A] {
				if !tmpBuilder.isWrittenBeforeFirstReadExt(ir.A) {
					liveIn[ir.A] = true
					slotRuntimeType[ir.A] = runtime.TypeTable
					slotPC[ir.A] = ir.PC
				}
				seen[ir.A] = true
			}
		case vm.OP_FORLOOP:
			for _, slot := range []int{ir.A, ir.A + 1, ir.A + 2} {
				if !seen[slot] && !calleeOnlySlots[slot] {
					if !tmpBuilder.isWrittenBeforeFirstReadExt(slot) {
						liveIn[slot] = true
						slotRuntimeType[slot] = runtime.TypeInt
						slotPC[slot] = ir.PC
					}
					seen[slot] = true
				}
			}
		}
	}

}

// computeCalleeOnlySlots scans the trace IR and returns a set of slot numbers
// that are ONLY referenced at Depth > 0 (inlined callee instructions). These
// slots are internal to the inlined function and are recomputed from scratch
// on every iteration. They don't need pre-loop type guards because:
//   - Their values at the loop header are irrelevant (they get overwritten)
//   - At first trace entry they may be nil, causing false guard failures
func computeCalleeOnlySlots(trace *Trace) map[int]bool {
	// Track the minimum depth at which each slot is referenced.
	// If a slot is referenced at Depth 0, it's NOT callee-only.
	slotMinDepth := make(map[int]int) // slot → minimum depth seen

	for _, ir := range trace.IR {
		// Collect all slots referenced by this instruction
		allSlots := getWriteSlots(&ir)
		for _, sr := range getReadSlots(&ir) {
			allSlots = append(allSlots, sr.slot)
		}
		// Also include A for all instructions that write to A
		// (getWriteSlots already handles this, but be safe)

		for _, slot := range allSlots {
			if slot >= 256 {
				continue // RK constant, not a register
			}
			if prev, ok := slotMinDepth[slot]; !ok || ir.Depth < prev {
				slotMinDepth[slot] = ir.Depth
			}
		}
	}

	// Callee-only slots: those whose minimum depth > 0
	result := make(map[int]bool)
	for slot, minDepth := range slotMinDepth {
		if minDepth > 0 {
			result[slot] = true
		}
	}
	return result
}

// slotRead pairs a slot number with the expected runtime type for that read.
type slotRead struct {
	slot int
	typ  runtime.ValueType
}

// getReadSlots returns the VM register slots read by a trace instruction.
// RK operands with idx >= 256 are constants, not registers — excluded.
func getReadSlots(ir *TraceIR) []slotRead {
	var reads []slotRead
	switch ir.Op {
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV:
		if ir.B < 256 {
			reads = append(reads, slotRead{ir.B, ir.BType})
		}
		if ir.C < 256 {
			reads = append(reads, slotRead{ir.C, ir.CType})
		}
	case vm.OP_LT, vm.OP_LE, vm.OP_EQ:
		if ir.B < 256 {
			reads = append(reads, slotRead{ir.B, ir.BType})
		}
		if ir.C < 256 {
			reads = append(reads, slotRead{ir.C, ir.CType})
		}
	case vm.OP_UNM:
		reads = append(reads, slotRead{ir.B, ir.BType})
	case vm.OP_MOVE:
		reads = append(reads, slotRead{ir.B, ir.BType})
	case vm.OP_TEST:
		reads = append(reads, slotRead{ir.A, ir.AType})
	case vm.OP_LEN:
		reads = append(reads, slotRead{ir.B, ir.BType})
	case vm.OP_GETFIELD:
		// B is the table
		reads = append(reads, slotRead{ir.B, runtime.TypeTable})
	case vm.OP_SETFIELD:
		// A is the table, C is the value (if register)
		reads = append(reads, slotRead{ir.A, runtime.TypeTable})
		if ir.C < 256 {
			reads = append(reads, slotRead{ir.C, ir.CType})
		}
	case vm.OP_GETTABLE:
		// B is the table, C is the key (if register)
		reads = append(reads, slotRead{ir.B, runtime.TypeTable})
		if ir.C < 256 {
			reads = append(reads, slotRead{ir.C, ir.CType})
		}
	case vm.OP_SETTABLE:
		// A is the table, B is the key (if register), C is the value (if register)
		reads = append(reads, slotRead{ir.A, runtime.TypeTable})
		if ir.B < 256 {
			reads = append(reads, slotRead{ir.B, ir.BType})
		}
		if ir.C < 256 {
			reads = append(reads, slotRead{ir.C, ir.CType})
		}
	case vm.OP_FORLOOP:
		// Reads idx (A), limit (A+1), step (A+2)
		reads = append(reads,
			slotRead{ir.A, runtime.TypeInt},
			slotRead{ir.A + 1, runtime.TypeInt},
			slotRead{ir.A + 2, runtime.TypeInt},
		)
	case vm.OP_FORPREP:
		// Reads init (A) and step (A+2)
		reads = append(reads,
			slotRead{ir.A, runtime.TypeInt},
			slotRead{ir.A + 2, runtime.TypeInt},
		)
	case vm.OP_CALL:
		// Reads fn (A), args (A+1..A+B-1)
		reads = append(reads, slotRead{ir.A, ir.AType})
		for s := ir.A + 1; s < ir.A+ir.B; s++ {
			reads = append(reads, slotRead{s, runtime.TypeInt}) // approximate
		}
	}
	return reads
}

// getWriteSlotContains returns true if the instruction writes to the given slot.
func getWriteSlotContains(ir *TraceIR, slot int) bool {
	for _, ws := range getWriteSlots(ir) {
		if ws == slot {
			return true
		}
	}
	return false
}

// getWriteSlots returns the VM register slots written by a trace instruction.
func getWriteSlots(ir *TraceIR) []int {
	switch ir.Op {
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV, vm.OP_UNM:
		return []int{ir.A}
	case vm.OP_MOVE:
		return []int{ir.A}
	case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL:
		return []int{ir.A}
	case vm.OP_GETFIELD, vm.OP_GETTABLE, vm.OP_GETGLOBAL:
		return []int{ir.A}
	case vm.OP_LEN:
		return []int{ir.A}
	case vm.OP_FORLOOP:
		return []int{ir.A, ir.A + 3}
	case vm.OP_FORPREP:
		return []int{ir.A}
	case vm.OP_CALL:
		return []int{ir.A}
	}
	return nil
}

// isWrittenBeforeFirstRead returns true if the given slot is written by a
// numeric-safe instruction before any instruction reads it in the trace body.
// When true, the slot's initial type at loop entry doesn't matter — the trace
// will overwrite it before use, so a type guard is unnecessary.
//
// Float slots always return false here. Skipping their SSA guards would
// disrupt the float register allocator's ref-level live ranges and pre-loop
// loading. Instead, the codegen handles write-before-read float slots by
// emitting a relaxed pre-loop guard (type < TypeString) and skipping the
// slot-level pre-loop D register load. See findWBRFloatSlots in ssa_codegen.go.
//
// Restrictions:
//   - Non-numeric ops (GETTABLE, SETTABLE, GETFIELD, SETFIELD, GETGLOBAL, CALL)
//     cause a bail-out because they may reuse the slot for a different type.
//   - For instructions that both read and write the same slot (e.g. ADD R4 R4 R3),
//     the read is checked FIRST since operands are read before the result is written.
func (b *ssaBuilder) isWrittenBeforeFirstRead(slot int) bool {
	if b.slotType[slot] == SSATypeFloat {
		// Float slots always emit guards (the float register allocator needs
		// the pre-loop SSA_LOAD_SLOT for D register initialization).
		// Guard relaxation for WBR float slots is handled in the codegen
		// via findWBRFloatSlots / isWrittenBeforeFirstReadExt.
		return false
	}
	return b.isWrittenBeforeFirstReadImpl(slot)
}

// isWrittenBeforeFirstReadExt checks if a float slot is written by a
// GETFIELD, GETTABLE, or CALL before any instruction reads it.
// This allows skipping pre-loop guards for intermediate float results
// (e.g., dx from "dx = bi.x - bj.x" where dx only exists inside the loop body).
// More conservative than isWrittenBeforeFirstReadImpl: only GETFIELD/GETTABLE/CALL
// writes count, not arithmetic (which could produce wrong types without guards).
func (b *ssaBuilder) isWrittenBeforeFirstReadExt(slot int) bool {
	for _, ir := range b.trace.IR {
		// Check reads first (all ops that read this slot)
		isRead := false
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV,
			vm.OP_LT, vm.OP_LE, vm.OP_EQ:
			if (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_UNM:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_MOVE:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_FORLOOP:
			if slot == ir.A || slot == ir.A+1 || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_FORPREP:
			if slot == ir.A || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_TEST:
			if ir.A == slot {
				isRead = true
			}
		case vm.OP_GETTABLE:
			if ir.B == slot || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_SETTABLE:
			if ir.A == slot || (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_GETFIELD:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_SETFIELD:
			if ir.A == slot || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_CALL:
			if slot >= ir.A && slot < ir.A+ir.B {
				isRead = true
			}
		}
		if isRead {
			return false
		}

		// Only recognize writes from ops that definitively produce a typed value:
		// GETFIELD/GETTABLE produce the value from the table (known type at recording).
		// CALL produces a result. LOADK/LOADINT produce constants.
		// We do NOT count arithmetic writes here — their output type depends on
		// operand types, which may be wrong if we skip the guard.
		isWrite := false
		switch ir.Op {
		case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL:
			isWrite = (ir.A == slot)
		case vm.OP_GETGLOBAL:
			isWrite = (ir.A == slot)
		case vm.OP_CALL:
			isWrite = (ir.A == slot)
		case vm.OP_FORLOOP:
			// FORLOOP writes idx (A) and loop var (A+3)
			isWrite = (slot == ir.A || slot == ir.A+3)
		case vm.OP_FORPREP:
			// FORPREP writes idx (A)
			isWrite = (ir.A == slot)
		}
		if isWrite {
			return true
		}
	}
	return false
}

func (b *ssaBuilder) isWrittenBeforeFirstReadImpl(slot int) bool {
	// First pass: bail out if the slot is used by non-numeric operations anywhere
	// in the trace. Non-numeric ops (GETTABLE, SETTABLE, GETFIELD, SETFIELD,
	// GETGLOBAL, CALL) may reuse the same slot for a different type (e.g., slot
	// holds int from LOADINT, then table from GETGLOBAL). If we skip the guard,
	// guardedSlots is set, preventing the later table guard from being emitted.
	for _, ir := range b.trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE:
			// A=destination (writes table element), B=table (read), C=key (read)
			if ir.A == slot || ir.B == slot || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_SETTABLE:
			// A=table (read), B=key (read/RK), C=value (read/RK)
			if ir.A == slot || (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_GETFIELD:
			// A=destination, B=table (read)
			if ir.A == slot || ir.B == slot {
				return false
			}
		case vm.OP_SETFIELD:
			// A=table (read), C=value (read/RK)
			if ir.A == slot || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_GETGLOBAL:
			// A=destination (writes full Value)
			if ir.A == slot {
				return false
			}
		case vm.OP_CALL:
			// CALL reads R(A) (function) and R(A+1)..R(A+B-1) (args), writes R(A) (result)
			if slot >= ir.A && slot < ir.A+ir.B {
				return false
			}
			if ir.A == slot {
				return false
			}
		}
	}

	// Second pass: scan for write-before-read among numeric operations.
	for _, ir := range b.trace.IR {
		// Check reads first (within same instruction, reads happen before write)
		isRead := false
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV,
			vm.OP_LT, vm.OP_LE, vm.OP_EQ:
			if (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_UNM:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_MOVE:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_FORLOOP:
			if slot == ir.A || slot == ir.A+1 || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_FORPREP:
			if slot == ir.A || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_TEST:
			if ir.A == slot {
				isRead = true
			}
		}
		if isRead {
			return false // slot read before any write — guard needed
		}

		// Check writes: only constant-loading ops are safe.
		// Arithmetic ops (ADD, SUB, MUL, DIV) and MOVE are NOT safe writes
		// because (a) arithmetic output types depend on operand types, which
		// may not be correctly determined when the guard is skipped (slot
		// reused as float then int produces incorrect SSA), and (b) MOVE
		// may copy non-numeric values (tables) but spillIfNotAllocated
		// writes TypeInt, corrupting the type tag on side-exit.
		// The codegen's findWBRFloatSlots handles float-slot WBR separately
		// with its own isSlotWBR that does recognize MOVE/arithmetic writes.
		isWrite := false
		switch ir.Op {
		case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL:
			isWrite = (ir.A == slot)
		}
		if isWrite {
			return true // slot written before any read — guard not needed
		}
	}
	// Slot never read or written in trace — conservative: guard needed
	return false
}
