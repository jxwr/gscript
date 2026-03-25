//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// SlotClass describes how a VM slot is used in a trace.
type SlotClass int

const (
	SlotLiveIn SlotClass = iota // first reference is read → needs pre-loop guard
	SlotWBR                     // first reference is write → no guard needed
	SlotDead                    // never referenced
)

// SlotInfo holds classification data for a single VM slot.
type SlotInfo struct {
	Class     SlotClass
	GuardType runtime.ValueType // for live-in: expected type at trace entry
	FirstPC   int               // PC of first reference
}

// classifySlots performs a single forward scan of trace IR to classify each
// VM slot as live-in (first ref is read), write-before-read (first ref is
// write), or dead (never referenced). After the scan, slots referenced only
// at inlining depth > 0 (callee-only) are marked dead.
func classifySlots(trace *Trace) map[int]*SlotInfo {
	slots := make(map[int]*SlotInfo)

	// Track which slots are referenced at depth 0.
	depthZeroSlots := make(map[int]bool)

	for i := range trace.IR {
		ir := &trace.IR[i]

		reads, readTypes := traceReads(ir)
		writes := traceWrites(ir)

		// Reads come before writes within the same instruction.
		for j, slot := range reads {
			if ir.Depth == 0 {
				depthZeroSlots[slot] = true
			}
			if _, seen := slots[slot]; !seen {
				guardType := runtime.TypeNil
				if j < len(readTypes) {
					guardType = readTypes[j]
				}
				slots[slot] = &SlotInfo{
					Class:     SlotLiveIn,
					GuardType: guardType,
					FirstPC:   ir.PC,
				}
			}
		}

		for _, slot := range writes {
			if ir.Depth == 0 {
				depthZeroSlots[slot] = true
			}
			if _, seen := slots[slot]; !seen {
				slots[slot] = &SlotInfo{
					Class:   SlotWBR,
					FirstPC: ir.PC,
				}
			}
		}
	}

	// Callee-only slots (referenced only at Depth > 0) are dead.
	for slot, info := range slots {
		if !depthZeroSlots[slot] {
			info.Class = SlotDead
		}
	}

	return slots
}

// traceReads returns the VM slots read by a trace IR instruction, along with
// the observed type for each read slot (used for live-in guard type).
func traceReads(ir *TraceIR) (slots []int, types []runtime.ValueType) {
	switch ir.Op {
	// Arithmetic: read B and C operands (skip constants via RK encoding).
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_POW:
		if !vm.IsRK(ir.B) {
			slots = append(slots, ir.B)
			types = append(types, ir.BType)
		}
		if !vm.IsRK(ir.C) {
			slots = append(slots, ir.C)
			types = append(types, ir.CType)
		}

	// Unary: read B.
	case vm.OP_UNM:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// NOT: read B.
	case vm.OP_NOT:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// MOVE: read B (source).
	case vm.OP_MOVE:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// GETFIELD: read B (table base).
	case vm.OP_GETFIELD:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// GETTABLE: read B (table base), C (key, unless constant).
	case vm.OP_GETTABLE:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)
		if !vm.IsRK(ir.C) {
			slots = append(slots, ir.C)
			types = append(types, ir.CType)
		}

	// SETFIELD: read A (table base), C (value, unless constant).
	case vm.OP_SETFIELD:
		slots = append(slots, ir.A)
		types = append(types, ir.AType)
		if !vm.IsRK(ir.C) {
			slots = append(slots, ir.C)
			types = append(types, ir.CType)
		}

	// SETTABLE: read A (table base), B (key, unless constant), C (value, unless constant).
	case vm.OP_SETTABLE:
		slots = append(slots, ir.A)
		types = append(types, ir.AType)
		if !vm.IsRK(ir.B) {
			slots = append(slots, ir.B)
			types = append(types, ir.BType)
		}
		if !vm.IsRK(ir.C) {
			slots = append(slots, ir.C)
			types = append(types, ir.CType)
		}

	// FORLOOP: read A (index), A+1 (limit), A+2 (step).
	case vm.OP_FORLOOP:
		slots = append(slots, ir.A, ir.A+1, ir.A+2)
		types = append(types, ir.AType, ir.AType, ir.AType)

	// FORPREP: read A (initial), A+2 (step).
	case vm.OP_FORPREP:
		slots = append(slots, ir.A, ir.A+2)
		types = append(types, ir.AType, ir.AType)

	// TEST: read A.
	case vm.OP_TEST:
		slots = append(slots, ir.A)
		types = append(types, ir.AType)

	// TESTSET: read B.
	case vm.OP_TESTSET:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// CALL: read A (function), A+1..A+B-1 (args).
	case vm.OP_CALL:
		slots = append(slots, ir.A)
		types = append(types, ir.AType)
		if ir.B > 1 {
			for j := 1; j < ir.B; j++ {
				slots = append(slots, ir.A+j)
				// Args don't have individual recorded types; use TypeNil as placeholder.
				types = append(types, runtime.TypeNil)
			}
		}

	// Comparisons: read B and C operands (skip constants via RK).
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
		if !vm.IsRK(ir.B) {
			slots = append(slots, ir.B)
			types = append(types, ir.BType)
		}
		if !vm.IsRK(ir.C) {
			slots = append(slots, ir.C)
			types = append(types, ir.CType)
		}

	// LEN: read B (table/string).
	case vm.OP_LEN:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// CONCAT: read B..C range.
	case vm.OP_CONCAT:
		for j := ir.B; j <= ir.C; j++ {
			slots = append(slots, j)
			types = append(types, runtime.TypeString)
		}

	// RETURN: read A..A+B-2 (when B > 1).
	case vm.OP_RETURN:
		if ir.B > 1 {
			for j := 0; j < ir.B-1; j++ {
				slots = append(slots, ir.A+j)
				types = append(types, runtime.TypeNil)
			}
		}

	// SELF: read B (table).
	case vm.OP_SELF:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// SEND: read A (channel), B (value).
	case vm.OP_SEND:
		slots = append(slots, ir.A, ir.B)
		types = append(types, ir.AType, ir.BType)

	// RECV: read B (channel).
	case vm.OP_RECV:
		slots = append(slots, ir.B)
		types = append(types, ir.BType)

	// SETGLOBAL: read A (value to store).
	case vm.OP_SETGLOBAL:
		slots = append(slots, ir.A)
		types = append(types, ir.AType)

	// APPEND: read A (table), B (value).
	case vm.OP_APPEND:
		slots = append(slots, ir.A, ir.B)
		types = append(types, ir.AType, ir.BType)

	// SETLIST: read A (table), A+1..A+B (values).
	case vm.OP_SETLIST:
		slots = append(slots, ir.A)
		types = append(types, ir.AType)
		for j := 1; j <= ir.B; j++ {
			slots = append(slots, ir.A+j)
			types = append(types, runtime.TypeNil)
		}

	// GETGLOBAL, LOADNIL, LOADBOOL, LOADINT, LOADK, NEWTABLE, CLOSURE,
	// JMP, CLOSE, MAKECHAN, GETUPVAL, SETUPVAL, VARARG, GO:
	// no register reads (or reads are handled differently).
	}

	return slots, types
}

// traceWrites returns the VM slots written by a trace IR instruction.
func traceWrites(ir *TraceIR) []int {
	switch ir.Op {
	// All ops that write to A.
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_POW,
		vm.OP_UNM, vm.OP_NOT,
		vm.OP_MOVE,
		vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL,
		vm.OP_GETFIELD, vm.OP_GETTABLE, vm.OP_GETGLOBAL,
		vm.OP_LEN, vm.OP_CONCAT,
		vm.OP_NEWTABLE, vm.OP_CLOSURE,
		vm.OP_GETUPVAL,
		vm.OP_MAKECHAN, vm.OP_RECV:
		return []int{ir.A}

	// LOADNIL: writes A..A+B.
	case vm.OP_LOADNIL:
		slots := make([]int, ir.B+1)
		for j := 0; j <= ir.B; j++ {
			slots[j] = ir.A + j
		}
		return slots

	// CALL: writes A..A+C-2 (when C > 0; C=0 means variable results).
	case vm.OP_CALL:
		if ir.C > 0 {
			slots := make([]int, ir.C-1)
			for j := 0; j < ir.C-1; j++ {
				slots[j] = ir.A + j
			}
			return slots
		}
		// C=0: variable results, conservatively write just A.
		return []int{ir.A}

	// FORLOOP: writes A (index) and A+3 (exposed loop variable).
	case vm.OP_FORLOOP:
		return []int{ir.A, ir.A + 3}

	// FORPREP: writes A (adjusted index).
	case vm.OP_FORPREP:
		return []int{ir.A}

	// TESTSET: writes A.
	case vm.OP_TESTSET:
		return []int{ir.A}

	// SELF: writes A (method) and A+1 (self/table).
	case vm.OP_SELF:
		return []int{ir.A, ir.A + 1}

	// VARARG: writes A..A+B-2 (when B > 0).
	case vm.OP_VARARG:
		if ir.B > 0 {
			slots := make([]int, ir.B-1)
			for j := 0; j < ir.B-1; j++ {
				slots[j] = ir.A + j
			}
			return slots
		}
		return []int{ir.A}

	// TFORCALL: writes A+3..A+2+C.
	case vm.OP_TFORCALL:
		if ir.C > 0 {
			slots := make([]int, ir.C)
			for j := 0; j < ir.C; j++ {
				slots[j] = ir.A + 3 + j
			}
			return slots
		}

	// TFORLOOP: writes A.
	case vm.OP_TFORLOOP:
		return []int{ir.A}

	// These ops don't write to any register slot:
	// OP_SETTABLE, OP_SETFIELD, OP_SETLIST, OP_SETGLOBAL, OP_SETUPVAL,
	// OP_EQ, OP_LT, OP_LE, OP_TEST, OP_JMP, OP_RETURN, OP_CLOSE,
	// OP_SEND, OP_GO, OP_APPEND
	}

	return nil
}
