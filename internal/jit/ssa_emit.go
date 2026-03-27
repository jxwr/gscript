//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
)

// ────────────────────────────────────────────────────────────────────────────
// SSA pipeline pass stubs (no-op for now)
// ────────────────────────────────────────────────────────────────────────────

// ConstHoist hoists loop-invariant constants out of the loop body.
// The real implementation is in ssa_const_hoist.go.
func ConstHoist(f *SSAFunc) *SSAFunc { return constHoistImpl(f) }

// CSE performs common subexpression elimination.
// The real implementation is in ssa_opt.go.
func CSE(f *SSAFunc) *SSAFunc { return cseImpl(f) }

// FuseMultiplyAdd is defined in ssa_fma.go.

// ────────────────────────────────────────────────────────────────────────────
// SSA analysis helpers
// ────────────────────────────────────────────────────────────────────────────

// ssaIsIntegerOnly returns true if all SSA ops in the function are compilable.
func ssaIsIntegerOnly(f *SSAFunc) bool {
	hasForloopExit := false
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_LOAD_SLOT, SSA_UNBOX_INT, SSA_UNBOX_FLOAT,
			SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
			SSA_LOAD_FIELD, SSA_STORE_FIELD,
			SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_GLOBAL, SSA_TABLE_LEN,
			SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_LOOP, SSA_SIDE_EXIT, SSA_NOP, SSA_SNAPSHOT,
			SSA_CALL_INNER_TRACE, SSA_INNER_LOOP, SSA_INTRINSIC,
			SSA_CALL, SSA_SELF_CALL,
			SSA_MOVE, SSA_PHI, SSA_BOX_INT, SSA_BOX_FLOAT, SSA_STORE_SLOT:
			// LOAD_FIELD/STORE_FIELD with invalid field index are silently
			// skipped (no code emitted), NOT treated as call-exit.
			// Track loop exit: FORLOOP (AuxInt=-1) or while-loop (AuxInt=-2)
			if isLoopExitCmp(inst.Op, inst.AuxInt) {
				hasForloopExit = true
			}
			continue
		default:
			return false
		}
	}
	// Only compile traces that have a proper loop exit (FORLOOP or while-loop).
	// hasForloopExit is true for both FORLOOP (AuxInt=-1) and while-loop (AuxInt=-2) exits.
	if !hasForloopExit {
		return false
	}
	// Call-exit ops (SSA_CALL, SSA_LOAD_GLOBAL) are emitted as side-exits (ExitCode=1).
	// SSA_TABLE_LEN is now native.
	// The interpreter resumes at ExitPC, executes the instruction, and FORLOOP
	// back-edge re-enters the trace. No resume dispatch needed.
	return true
}

// SSAIsUseful returns true if the SSA function contains meaningful computation.
// A trace that immediately exits (e.g., SSA_CALL as first op after SSA_LOOP) is
// not useful — it would just exit on every entry, wasting the trace overhead.
func SSAIsUseful(f *SSAFunc) bool {
	hasComputation := false
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_LOAD_ARRAY, SSA_STORE_ARRAY,
			SSA_TABLE_LEN, SSA_INTRINSIC:
			hasComputation = true
		}
	}
	if !hasComputation {
		return false
	}

	// Reject traces where the first meaningful instruction after SSA_LOOP is a
	// call-exit op (SSA_CALL, SSA_LOAD_GLOBAL, non-scalar LOAD_ARRAY).
	// SSA_TABLE_LEN is now native and is NOT a call-exit.
	// Such traces would exit immediately on every entry — the trace does no useful
	// work before hitting the side-exit. This prevents infinite re-enter → exit loops.
	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		op := f.Insts[i].Op
		// Skip NOPs, snapshots, loads, unboxes, constants, guards — these are setup, not computation.
		// SSA_TABLE_LEN is native and counts as setup.
		if op == SSA_NOP || op == SSA_SNAPSHOT || op == SSA_LOAD_SLOT ||
			op == SSA_TABLE_LEN ||
			op == SSA_UNBOX_INT || op == SSA_UNBOX_FLOAT ||
			op == SSA_CONST_INT || op == SSA_CONST_FLOAT || op == SSA_CONST_NIL || op == SSA_CONST_BOOL ||
			op == SSA_GUARD_TYPE || op == SSA_GUARD_TRUTHY || op == SSA_GUARD_NNIL || op == SSA_GUARD_NOMETA ||
			op == SSA_PHI || op == SSA_MOVE || op == SSA_BOX_INT || op == SSA_BOX_FLOAT || op == SSA_STORE_SLOT {
			continue
		}
		// If the first real op is a call-exit, the trace is useless
		if op == SSA_CALL {
			return false
		}
		// Non-table LOAD_GLOBAL is a call-exit; table-type is native
		if op == SSA_LOAD_GLOBAL {
			inst := &f.Insts[i]
			if inst.Type != SSATypeTable {
				return false
			}
		}
		// Non-scalar, non-table LOAD_ARRAY is also a call-exit.
		// Table-type LOAD_ARRAY is native (emitLoadArrayTable).
		if op == SSA_LOAD_ARRAY {
			inst := &f.Insts[i]
			if inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool && inst.Type != SSATypeTable {
				return false
			}
		}
		break // first real op is not a call-exit, trace is useful
	}

	// Reject traces that have guaranteed-every-iteration side-exits in the loop body.
	// These instructions always side-exit (the interpreter handles them), so the
	// trace enter→work→side-exit→resume cycle fires on EVERY iteration, which is
	// slower than pure interpretation due to trace entry/exit overhead.
	// SSA_TABLE_LEN is now native and never side-exits.
	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		// SSA_CALL always side-exits.
		if inst.Op == SSA_CALL {
			return false
		}
		// Non-table LOAD_GLOBAL always side-exits; table-type is native.
		if inst.Op == SSA_LOAD_GLOBAL && inst.Type != SSATypeTable {
			return false
		}
		// Non-scalar, non-table LOAD_ARRAY always side-exits.
		// Table-type LOAD_ARRAY is native (emitLoadArrayTable).
		if inst.Op == SSA_LOAD_ARRAY {
			if inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool && inst.Type != SSATypeTable {
				return false
			}
		}
	}
	// Note: Mixed int/float writes to the same slot are safe because the
	// floatWrittenSlots mechanism in store-back skips int GPR writes for slots
	// whose last write was float.

	return true
}

// ssaIsCompilableFunc returns true if a function trace SSA is compilable.
// Unlike loop traces, function traces don't need a FORLOOP exit — they
// execute linearly from entry to RETURN.
func ssaIsCompilableFunc(f *SSAFunc) bool {
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_LOAD_SLOT, SSA_UNBOX_INT, SSA_UNBOX_FLOAT,
			SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
			SSA_LOAD_FIELD, SSA_STORE_FIELD,
			SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_GLOBAL, SSA_TABLE_LEN,
			SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_LOOP, SSA_SIDE_EXIT, SSA_NOP, SSA_SNAPSHOT,
			SSA_CALL_INNER_TRACE, SSA_INNER_LOOP, SSA_INTRINSIC,
			SSA_CALL, SSA_SELF_CALL,
			SSA_MOVE, SSA_PHI, SSA_BOX_INT, SSA_BOX_FLOAT, SSA_STORE_SLOT:
			// LOAD_ARRAY with non-scalar, non-table result falls back to call-exit.
			if inst.Op == SSA_LOAD_ARRAY && inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool && inst.Type != SSATypeTable {
				// Still OK for func traces — will side-exit
			}
			continue
		default:
			return false
		}
	}
	return true
}

// ssaIsFuncUseful returns true if a function trace SSA has meaningful computation.
func ssaIsFuncUseful(f *SSAFunc) bool {
	hasComputation := false
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_LOAD_ARRAY, SSA_STORE_ARRAY,
			SSA_TABLE_LEN, SSA_INTRINSIC, SSA_SELF_CALL:
			hasComputation = true
		}
	}
	return hasComputation
}

// CompileSSAFunc compiles a function-entry trace SSA to native ARM64 code.
// Unlike CompileSSA (for loop traces), this emits a linear code path with
// no loop back-edge — the trace runs once from entry to exit.
func CompileSSAFunc(f *SSAFunc) (*CompiledTrace, error) {
	if f == nil || len(f.Insts) == 0 {
		return nil, fmt.Errorf("empty SSA function")
	}

	regMap := AllocateRegisters(f)
	if debugTrace && regMap.Int != nil {
		fmt.Printf("[REGALLOC-INT-FUNC] ")
		for slot, reg := range regMap.Int.slotToReg {
			fmt.Printf("slot%d->%v ", slot, reg)
		}
		fmt.Println()
	}
	asm := NewAssembler()

	ec := &emitCtx{
		asm:                asm,
		f:                  f,
		regMap:             regMap,
		floatSlotReg:       make(map[int]FReg),
		callExitWriteSlots: make(map[int]bool),
		floatWrittenSlots:  make(map[int]bool),
		rawIntSlots:        make(map[int]bool),
		selfCallExtraRef:   SSARefNone,
	}

	// Pre-scan: track call-exit slots (same as loop traces)
	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		isCallExit := inst.Op == SSA_CALL
		if inst.Op == SSA_LOAD_ARRAY && inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool && inst.Type != SSATypeTable {
			isCallExit = true
		}
		if isCallExit && inst.Slot >= 0 {
			ec.callExitWriteSlots[int(inst.Slot)] = true
		}
		if inst.Op == SSA_STORE_ARRAY && inst.Slot >= 0 {
			ec.callExitWriteSlots[int(inst.Slot)] = true
		}
		if inst.Op == SSA_LOAD_ARRAY {
			if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(f.Insts) {
				tblSlot := int(f.Insts[inst.Arg1].Slot)
				if tblSlot >= 0 {
					ec.callExitWriteSlots[tblSlot] = true
				}
			}
			if inst.Type == SSATypeTable && inst.Slot >= 0 {
				ec.callExitWriteSlots[int(inst.Slot)] = true
			}
		}
		if inst.Op == SSA_STORE_FIELD && inst.Slot >= 0 {
			ec.callExitWriteSlots[int(inst.Slot)] = true
		}
		if inst.Op == SSA_LOAD_FIELD {
			if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(f.Insts) {
				tblSlot := int(f.Insts[inst.Arg1].Slot)
				if tblSlot >= 0 {
					ec.callExitWriteSlots[tblSlot] = true
				}
			}
		}
		if inst.Op == SSA_LOAD_GLOBAL && inst.Slot >= 0 {
			ec.callExitWriteSlots[int(inst.Slot)] = true
		}
	}

	// Pre-scan: detect self-calls
	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		if f.Insts[i].Op == SSA_SELF_CALL {
			ec.hasSelfCalls = true
			break
		}
	}

	// 1. Prologue
	ec.emitPrologue()

	if ec.hasSelfCalls {
		// Initialize self-call depth counter (X25 = 0)
		asm.MOVimm16(X25, 0)
		// Label for self-call re-entry (after prologue, before guards)
		asm.Label("self_call_entry")
	}

	// 2. Pre-loop guards (type checks)
	ec.emitPreLoopGuards()

	// 3. Pre-loop loads
	ec.emitPreLoopLoads()

	// 4. Function body (no loop_top label, no back-edge)
	ec.emitFuncBody()

	// 5. Function done: store back and exit with ExitCode=0
	ec.emitFuncDone()

	// 6. Cold paths: side-exit, guard-fail
	ec.emitSideExit()
	if ec.hasSelfCalls {
		ec.emitSelfCallGuardFail()
	} else {
		ec.emitGuardFail()
	}

	// 7. Epilogue
	ec.emitEpilogue()

	// 8. Finalize
	code, err := asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("assembler finalize: %w", err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		return nil, fmt.Errorf("alloc exec: %w", err)
	}

	if err := block.WriteCode(code); err != nil {
		return nil, fmt.Errorf("write code: %w", err)
	}

	// Compute return value count from the trace's FuncReturnCount.
	// Bytecode B field: B-1 = number of return values (0 means variable).
	nRet := 0
	if f.Trace.FuncReturnCount >= 2 {
		nRet = f.Trace.FuncReturnCount - 1
	} else if f.Trace.FuncReturnCount == 0 {
		nRet = 1 // variable returns: assume 1 for now
	}

	ct := &CompiledTrace{
		code:            block,
		proto:           f.Trace.LoopProto,
		loopPC:          0,
		constants:       f.Trace.Constants,
		hasCallExit:     ec.hasCallExit,
		snapshots:       f.Snapshots,
		isFuncTrace:     true,
		hasSelfCalls:    ec.hasSelfCalls,
		funcReturnSlot:  f.Trace.FuncReturnSlot,
		funcReturnCount: nRet,
	}

	return ct, nil
}

// isLoopExitCmp returns true if the SSA comparison is a loop exit:
// FORLOOP exit (AuxInt=-1) or while-loop exit (AuxInt=-2).
func isLoopExitCmp(op SSAOp, auxInt int64) bool {
	if auxInt != -1 && auxInt != -2 {
		return false
	}
	switch op {
	case SSA_LE_INT, SSA_LE_FLOAT, SSA_LT_INT, SSA_LT_FLOAT:
		return true
	}
	return false
}

// ────────────────────────────────────────────────────────────────────────────
// Register conventions
// ────────────────────────────────────────────────────────────────────────────
//
// X19: TraceContext pointer (pinned, received in X0 from callJIT trampoline)
// X20-X23: allocated GPR values (4 available for integer trace values)
// X24: NaN-boxing int tag constant (0xFFFE000000000000)
// X25: scratch (available)
// X26: regRegs pointer (vm.regs[base]) — loaded from TraceContext.Regs
// X27: constants pointer — loaded from TraceContext.Constants
// X28: scratch (available)
// D4-D11: allocated FPR values (8 available for float trace values)
// X0-X15: scratch/temporaries
// D0-D3: scratch FPR

const (
	regCtx      = X19 // TraceContext pointer (pinned)
	regTagInt   = X24 // NaN-boxing int tag constant
	regRegs     = X26 // vm.regs[base]
	regConsts   = X27 // trace constants pointer
)

// ────────────────────────────────────────────────────────────────────────────
// emitCtx holds state during code generation
// ────────────────────────────────────────────────────────────────────────────

type emitCtx struct {
	asm          *Assembler
	f            *SSAFunc
	regMap       *RegMap
	snapIdx      int  // current snapshot index for side-exit
	hasCallExit  bool
	loopExitIdx  int  // SSA instruction index of the OUTER loop-exit comparison (FORLOOP's LE_INT/LE_FLOAT)
	// Inner loop support (for full nesting):
	innerLoopBodyStart int // SSA index where the inner loop body starts (label emitted here)
	innerLoopExitIdx   int // SSA index of the inner loop's FORLOOP LE check (-1 if none)
	// Float slot tracking: maps slot → FReg that holds the slot's value at end of loop body.
	// Updated during emit as we process each SSA instruction.
	floatSlotReg map[int]FReg
	// breakGuardPC is the bytecode PC of the break guard (LT_FLOAT inside inner loop).
	// Used by break_exit to set ExitPC so the VM re-executes the comparison.
	breakGuardPC int
	// reloadSeq is a monotonically increasing counter for unique reload labels.
	reloadSeq int
	// callExitWriteSlots tracks slots that are written by call-exit (side-exit) instructions.
	// These slots should NOT be overwritten by storeBack (the interpreter's value is authoritative).
	callExitWriteSlots map[int]bool
	// floatWrittenSlots tracks slots whose last write was a float value.
	// Int store-back must skip these to avoid overwriting a float with a stale int GPR.
	floatWrittenSlots map[int]bool
	// rawIntSlots tracks slots that contain raw int values (not NaN-boxed).
	// Store-back must box these before writing to the VM's memory.
	rawIntSlots map[int]bool
	// arraySeq is a monotonically increasing counter for unique array access labels.
	arraySeq int
	// guardTruthyCount is a monotonically increasing counter for unique guard_truthy labels.
	guardTruthyCount int
	// Self-call support for function traces with native recursion.
	hasSelfCalls    bool            // true if function trace has SSA_SELF_CALL instructions
	selfCallSeq     int             // monotonically increasing counter for unique self-call labels
	selfCallExtraRef SSARef         // SSARef whose result is in regSelfExtra (X28), -1 if none
}

// ────────────────────────────────────────────────────────────────────────────
// CompileSSA: main entry point
// ────────────────────────────────────────────────────────────────────────────

// CompileSSA compiles an SSAFunc to native ARM64 code.
func CompileSSA(f *SSAFunc) (*CompiledTrace, error) {
	if f == nil || len(f.Insts) == 0 {
		return nil, fmt.Errorf("empty SSA function")
	}

	regMap := AllocateRegisters(f)
	if debugTrace && regMap.Int != nil {
		fmt.Printf("[REGALLOC-INT] ")
		for slot, reg := range regMap.Int.slotToReg {
			fmt.Printf("slot%d->%v ", slot, reg)
		}
		fmt.Println()
	}
	asm := NewAssembler()

	ec := &emitCtx{
		asm:                asm,
		f:                  f,
		regMap:             regMap,
		floatSlotReg:       make(map[int]FReg),
		callExitWriteSlots: make(map[int]bool),
		floatWrittenSlots:  make(map[int]bool),
		rawIntSlots:        make(map[int]bool),
	}

	// Pre-scan: track which slots are written by call-exit instructions.
	// These slots must NOT be overwritten by storeBack (the interpreter's value is authoritative).
	// LOAD_ARRAY with scalar result is native. Others use call-exit (side-exit).
	// Also track table slots used by native LOAD_ARRAY/STORE_ARRAY: these hold table
	// pointers that must not be overwritten by int store-back.
	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		isCallExit := inst.Op == SSA_CALL
		// LOAD_ARRAY with non-scalar, non-table result falls back to call-exit.
		// Table-type LOAD_ARRAY is native (emitLoadArrayTable).
		if inst.Op == SSA_LOAD_ARRAY && inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool && inst.Type != SSATypeTable {
			isCallExit = true
		}
		if isCallExit {
			// Track output slots (slots written by the interpreter after side-exit)
			if inst.Slot >= 0 {
				ec.callExitWriteSlots[int(inst.Slot)] = true
			}
		}
		// Protect table slots used by native LOAD_ARRAY and STORE_ARRAY.
		// The table slot holds a NaN-boxed table pointer; if an int register is
		// allocated for the same slot number, store-back must NOT overwrite it.
		if inst.Op == SSA_STORE_ARRAY {
			// STORE_ARRAY: inst.Slot IS the table slot
			if inst.Slot >= 0 {
				ec.callExitWriteSlots[int(inst.Slot)] = true
			}
		}
		if inst.Op == SSA_LOAD_ARRAY {
			// LOAD_ARRAY: the source table is referenced via Arg1
			if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(f.Insts) {
				tblSlot := int(f.Insts[inst.Arg1].Slot)
				if tblSlot >= 0 {
					ec.callExitWriteSlots[tblSlot] = true
				}
			}
			// Table-type LOAD_ARRAY: the destination slot holds a NaN-boxed table pointer.
			// Protect it from int/float store-back which would corrupt the pointer.
			if inst.Type == SSATypeTable && inst.Slot >= 0 {
				ec.callExitWriteSlots[int(inst.Slot)] = true
			}
		}
		// Similarly protect table slots for LOAD_FIELD and STORE_FIELD
		if inst.Op == SSA_STORE_FIELD {
			if inst.Slot >= 0 {
				ec.callExitWriteSlots[int(inst.Slot)] = true
			}
		}
		if inst.Op == SSA_LOAD_FIELD {
			if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(f.Insts) {
				tblSlot := int(f.Insts[inst.Arg1].Slot)
				if tblSlot >= 0 {
					ec.callExitWriteSlots[tblSlot] = true
				}
			}
		}
		// Protect LOAD_GLOBAL destination slots from store-back.
		// The slot may be reused for a float/int value later in the iteration;
		// without protection, store-back would write the wrong type to the slot,
		// and LOAD_ARRAY/LOAD_FIELD that use the slot as a table source would
		// read corrupted data. Native LOAD_GLOBAL reads from regConsts, but
		// other ops may read from regRegs when the source is not LOAD_GLOBAL.
		if inst.Op == SSA_LOAD_GLOBAL && inst.Slot >= 0 {
			ec.callExitWriteSlots[int(inst.Slot)] = true
		}
	}

	// 1. Prologue: save callee-saved registers, set up pinned registers
	ec.emitPrologue()

	// 3. Pre-loop guards: type check all live-in slots
	ec.emitPreLoopGuards()

	// 4. Pre-loop loads: load live-in values into allocated registers
	ec.emitPreLoopLoads()

	// 5. Loop body
	asm.Label("loop_top")

	// Iteration tracing for debugging:
	// Increment ctx.IterationCount and check against ctx.MaxIterations.
	// If MaxIterations > 0 && IterationCount >= MaxIterations, exit with code 5.
	// Uses X9, X15 as scratch registers.
	asm.LDR(X9, regCtx, TraceCtxOffIterCount)  // X9 = ctx.IterationCount
	asm.ADDimm(X9, X9, 1)                       // X9++
	asm.STR(X9, regCtx, TraceCtxOffIterCount)  // ctx.IterationCount = X9
	asm.LDR(X15, regCtx, TraceCtxOffMaxIter)   // X15 = ctx.MaxIterations
	asm.CBZ(X15, "skip_max_iter")               // if MaxIterations == 0, skip
	asm.CMPreg(X9, X15)                         // compare IterationCount vs MaxIterations
	asm.BCond(CondGE, "max_iter_exit")          // if IterCount >= MaxIter, exit
	asm.Label("skip_max_iter")

	// Reload all float registers from memory at loop top.
	// This ensures that ref-level FPRs (which may hold stale values from
	// previous iterations) are overwritten with the correct values from
	// the store-back that happened just before the backward branch.
	ec.emitReloadAll()
	ec.emitLoopBody()

	// 6. Loop back-edge
	// Store back all register values to memory before branching back.
	// This is critical for float values: the ref-level FPR holds the result
	// of the last operation, but the next iteration's resolveFloatRef may
	// look up a different SSA ref that maps to the same slot. Without
	// store-back + reload, the ref-level FPR has stale data.
	ec.emitStoreBack()
	asm.B("loop_top")

	// 7. Cold paths: side-exit, break-exit, loop-done, guard-fail, max-iter
	ec.emitSideExit()
	ec.emitBreakExit()
	ec.emitLoopDone()
	ec.emitGuardFail()
	ec.emitMaxIterExit()

	// 8. Epilogue
	ec.emitEpilogue()

	// 9. Finalize and allocate executable memory
	code, err := asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("assembler finalize: %w", err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		return nil, fmt.Errorf("alloc exec: %w", err)
	}

	if err := block.WriteCode(code); err != nil {
		return nil, fmt.Errorf("write code: %w", err)
	}

	ct := &CompiledTrace{
		code:        block,
		proto:       f.Trace.LoopProto,
		loopPC:      f.Trace.LoopPC,
		constants:   f.Trace.Constants,
		hasCallExit: ec.hasCallExit,
		snapshots:   f.Snapshots,
		regMap:      regMap, // for debugging
	}

	return ct, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Prologue / Epilogue
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitPrologue() {
	asm := ec.asm

	// Save callee-saved registers: X19-X28, X29(FP), X30(LR)
	// ARM64 ABI: X19-X28 are callee-saved, D8-D15 are callee-saved
	asm.STPpre(X29, X30, SP, -16)
	asm.STP(X19, X20, SP, -16*1) // Note: using negative offsets from SP after push
	// We'll use a frame big enough for all callee-saved regs
	// Actually let's do it properly with a single stack frame.

	// Re-do: allocate stack frame for all callee-saved
	// We need to save: X19-X28 (10 regs = 80 bytes), FP, LR (16 bytes),
	// D8-D11 (4 FP regs = 32 bytes if used) = total ~128 bytes
	// Use a 160-byte frame for alignment.
	// But STPpre already pushed FP/LR. Let's restart cleanly.
	// Reset assembler
	asm.buf = asm.buf[:0]
	asm.fixups = asm.fixups[:0]
	for k := range asm.labels {
		delete(asm.labels, k)
	}

	// Frame layout (growing downward from SP):
	//   [SP+0]   = saved X29 (FP)
	//   [SP+8]   = saved X30 (LR)
	//   [SP+16]  = saved X19
	//   [SP+24]  = saved X20
	//   [SP+32]  = saved X21
	//   [SP+40]  = saved X22
	//   [SP+48]  = saved X23
	//   [SP+56]  = saved X24
	//   [SP+64]  = saved X25
	//   [SP+72]  = saved X26
	//   [SP+80]  = saved X27
	//   [SP+88]  = saved X28
	//   [SP+96]  = saved D8
	//   [SP+104] = saved D9
	//   [SP+112] = saved D10
	//   [SP+120] = saved D11
	const frameSize = 128 // 16 regs * 8 bytes, 16-byte aligned

	// SUB SP, SP, #frameSize
	asm.SUBimm(SP, SP, uint16(frameSize))
	// Save FP, LR
	asm.STP(X29, X30, SP, 0)
	// Set FP = SP (must use ADD, not MOV — MOVreg encodes ORR with XZR,
	// but register 31 in ORR context is XZR not SP)
	asm.ADDimm(X29, SP, 0)
	// Save callee-saved GPRs
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)
	// Save callee-saved FPRs
	asm.FSTP(D8, D9, SP, 96)
	asm.FSTP(D10, D11, SP, 112)

	// Set up pinned registers
	// X0 holds TraceContext pointer (from callJIT trampoline)
	asm.MOVreg(regCtx, X0)                        // X19 = ctx
	asm.LDR(regRegs, regCtx, TraceCtxOffRegs)      // X26 = ctx.Regs (vm.regs[base])
	asm.LDR(regConsts, regCtx, TraceCtxOffConstants) // X27 = ctx.Constants

	// Load NaN-boxing int tag constant into X24
	asm.LoadImm64(regTagInt, nb_i64(NB_TagInt)) // X24 = 0xFFFE000000000000
}

func (ec *emitCtx) emitEpilogue() {
	asm := ec.asm
	const frameSize = 128

	asm.Label("epilogue")
	// X0 already holds ExitCode (set by caller)
	// Store ExitCode to TraceContext before restoring callee-saved registers
	// (X19 = regCtx is still valid here)
	asm.STR(X0, regCtx, TraceCtxOffExitCode)

	// Restore callee-saved FPRs
	asm.FLDP(D8, D9, SP, 96)
	asm.FLDP(D10, D11, SP, 112)
	// Restore callee-saved GPRs
	asm.LDP(X27, X28, SP, 80)
	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	// Restore FP, LR
	asm.LDP(X29, X30, SP, 0)
	// Deallocate stack frame
	asm.ADDimm(SP, SP, uint16(frameSize))
	// Return
	asm.RET()
}

// ────────────────────────────────────────────────────────────────────────────
// Pre-loop guards
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitPreLoopGuards() {
	asm := ec.asm
	f := ec.f

	for i := 0; i < f.LoopIdx; i++ {
		inst := &f.Insts[i]
		if inst.Op != SSA_GUARD_TYPE {
			continue
		}
		slot := int(inst.Slot)
		bailoutID := int(inst.AuxInt)

		// Get guard info from deoptimization metadata
		var expectedType int
		gotDeopt := false
		if f.DeoptMetadata != nil {
			guard := f.DeoptMetadata.Guards[bailoutID]
			if guard != nil && guard.Expected != nil {
				// Expected is runtime.ValueType
				if vt, ok := guard.Expected.(runtime.ValueType); ok {
					expectedType = int(vt)
					gotDeopt = true
				}
			}
		}

		// Fallbacks only when DeoptMetadata is not available.
		// IMPORTANT: bailoutID is a guard index, NOT a type value.
		// Only use it as a type when there's no DeoptMetadata.
		if !gotDeopt {
			// Fallback 1: AuxInt as raw type (legacy manually-constructed SSA)
			if bailoutID >= TypeInt && bailoutID <= TypeTable {
				expectedType = bailoutID
			}
			// Fallback 2: use the SSA instruction's Type field
			if expectedType == TypeNil {
				expectedType = ssaTypeToGuardType(inst.Type)
			}
		}

		// Skip TypeNil guards — a nil-typed slot can't have useful
		// computation. TypeNil(0) is also to zero value from trace IR
		// entries that don't set AType (e.g., manually constructed tests).
		if expectedType == TypeNil {
			continue
		}

		// Emit guard with common fail label for now
		// TODO: In Phase 3, use per-guard fail labels with bailout IDs
		EmitGuardType(asm, regRegs, slot, expectedType, "guard_fail")
	}
}
// Pre-loop loads: load live-in values into allocated registers
// ────────────────────────────────────────────────────────────────────────────

// ssaTypeToGuardType converts an SSAType to a JIT guard type constant.
// SSAType and JIT TypeXxx use different iota orderings for Table/String/Nil.
func ssaTypeToGuardType(t SSAType) int {
	switch t {
	case SSATypeBool:
		return TypeBool
	case SSATypeInt:
		return TypeInt
	case SSATypeFloat:
		return TypeFloat
	case SSATypeString:
		return TypeString
	case SSATypeTable:
		return TypeTable
	case SSATypeNil:
		return TypeNil
	default:
		return TypeNil // Unknown → skip (caller checks TypeNil)
	}
}

func (ec *emitCtx) emitPreLoopLoads() {
	asm := ec.asm
	f := ec.f

	// Track which slots have been loaded by SSA instructions
	loadedIntSlots := make(map[int]bool)
	loadedFloatSlots := make(map[int]bool)

	for i := 0; i < f.LoopIdx; i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)
		slot := int(inst.Slot)

		switch inst.Op {
		case SSA_UNBOX_INT:
			if slot < 0 {
				continue
			}
			if reg, ok := ec.regMap.IntReg(slot); ok {
				asm.LDR(reg, regRegs, slot*ValueSize)
				EmitUnboxInt(asm, reg, reg)
				loadedIntSlots[slot] = true
			}

		case SSA_UNBOX_FLOAT:
			if slot < 0 {
				continue
			}
			if freg, ok := ec.regMap.FloatRefReg(ref); ok {
				asm.FLDRd(freg, regRegs, slot*ValueSize)
				loadedFloatSlots[slot] = true
			} else if freg, ok := ec.regMap.FloatReg(slot); ok {
				asm.FLDRd(freg, regRegs, slot*ValueSize)
				loadedFloatSlots[slot] = true
			}

		case SSA_CONST_INT:
			if slot < 0 {
				continue
			}
			if reg, ok := ec.regMap.IntReg(slot); ok {
				asm.LoadImm64(reg, inst.AuxInt)
				loadedIntSlots[slot] = true
			}

		case SSA_CONST_FLOAT:
			if slot < 0 {
				continue
			}
			if freg, ok := ec.regMap.FloatRefReg(ref); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(freg, X0)
				loadedFloatSlots[slot] = true
			} else if freg, ok := ec.regMap.FloatReg(slot); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(freg, X0)
				loadedFloatSlots[slot] = true
			}
		}
	}

	// Load any allocated integer slots that weren't loaded by SSA instructions.
	// This handles slots where the guard type was TypeNil (zero value)
	// but the slot is still allocated and used in the loop body.
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			if loadedIntSlots[slot] {
				continue
			}
			asm.LDR(reg, regRegs, slot*ValueSize)
			EmitUnboxInt(asm, reg, reg)
		}
	}

	// Load any allocated float slots not yet loaded.
	if ec.regMap.Float != nil {
		for slot, freg := range ec.regMap.Float.slotToReg {
			if loadedFloatSlots[slot] {
				continue
			}
			asm.FLDRd(freg, regRegs, slot*ValueSize)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Loop body emission
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoopBody() {
	f := ec.f

	// Find ALL FORLOOP exit comparisons by their sentinel tag (AuxInt == -1).
	// FORLOOP generates: ADD + LE(AuxInt=-1) + MOVE.
	//
	// With full nesting, there may be multiple LE(AuxInt=-1): one for each
	// inner FORLOOP and one for the outer FORLOOP. The LAST one is always
	// the outer (traced) loop's exit → branches to loop_done.
	// Inner FORLOOP exits → branch back to inner loop body start.
	ec.loopExitIdx = -1
	ec.innerLoopExitIdx = -1
	ec.innerLoopBodyStart = -1
	var allForloopExits []int
	whileLoopExitIdx := -1
	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if (inst.Op == SSA_LE_INT || inst.Op == SSA_LE_FLOAT) && inst.AuxInt == -1 {
			allForloopExits = append(allForloopExits, i)
		}
		// While-loop exit (AuxInt=-2): first comparison after LOOP
		if isLoopExitCmp(inst.Op, inst.AuxInt) && inst.AuxInt == -2 {
			whileLoopExitIdx = i
		}
	}
	if len(allForloopExits) > 0 {
		ec.loopExitIdx = allForloopExits[len(allForloopExits)-1]
	} else if whileLoopExitIdx >= 0 {
		// While-loop: use the while-loop exit as the loop exit
		ec.loopExitIdx = whileLoopExitIdx
	}
	// If there are inner FORLOOP exits, identify the inner loop body start.
	// The inner FORLOOP's LE check is preceded by an ADD (index += step).
	// The inner loop body starts after the FORPREP's SUB instruction that
	// shares the same slot as the ADD.
	if len(allForloopExits) > 1 {
		ec.innerLoopExitIdx = allForloopExits[0]
		// Find the ADD_INT that precedes the inner loop exit (FORLOOP: ADD then LE)
		innerExitIdx := ec.innerLoopExitIdx
		innerAddIdx := innerExitIdx - 1
		if innerAddIdx > f.LoopIdx && f.Insts[innerAddIdx].Op == SSA_ADD_INT {
			counterSlot := f.Insts[innerAddIdx].Slot
			// Scan backward to find the SUB_INT with the same slot (FORPREP)
			for j := innerAddIdx - 1; j > f.LoopIdx; j-- {
				if f.Insts[j].Op == SSA_SUB_INT && f.Insts[j].Slot == counterSlot {
					ec.innerLoopBodyStart = j + 1
					break
				}
			}
		}
		if debugTrace {
			fmt.Printf("[EMIT] Inner loop: bodyStart=%d exitIdx=%d outerExitIdx=%d\n",
				ec.innerLoopBodyStart, ec.innerLoopExitIdx, ec.loopExitIdx)
		}
	}

	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)

		// Skip MUL instructions absorbed by FMADD/FMSUB fusion.
		// The MUL stays in the IR for register allocation live ranges
		// but must not emit ARM64 code.
		if f.AbsorbedMuls[ref] {
			continue
		}

		// Emit label at inner loop body start for backward branching.
		// After the label, reload all float registers from memory so that
		// inner loop iterations use updated values (not stale SSA refs).
		if i == ec.innerLoopBodyStart {
			ec.asm.Label("inner_loop_body")
			ec.emitInnerLoopReload()
		}

		switch inst.Op {
		case SSA_NOP, SSA_SNAPSHOT, SSA_LOOP:
			// No code emitted

		case SSA_LOAD_SLOT:
			// Usually handled in pre-loop; in loop body, this is a reload
			ec.emitLoadSlot(ref, inst)

		case SSA_UNBOX_INT:
			ec.emitUnboxInt(ref, inst)

		case SSA_UNBOX_FLOAT:
			ec.emitUnboxFloat(ref, inst)

		case SSA_CONST_INT:
			ec.emitConstInt(ref, inst)

		case SSA_CONST_FLOAT:
			ec.emitConstFloat(ref, inst)

		case SSA_CONST_NIL, SSA_CONST_BOOL:
			// These don't go into registers in the loop body.
			// The actual values are resolved at use sites (e.g., STORE_ARRAY
			// reads CONST_BOOL's AuxInt directly, not from memory).
			// NOTE: We intentionally do NOT write to memory here because
			// the slot may be shared with a table reference in other traces,
			// and overwriting it would corrupt the table pointer.

		case SSA_ADD_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.ADDreg(dst, a1, a2)
			})

		case SSA_SUB_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.SUBreg(dst, a1, a2)
			})

		case SSA_MUL_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.MUL(dst, a1, a2)
			})

		case SSA_MOD_INT:
			ec.emitModInt(ref, inst)

		case SSA_NEG_INT:
			ec.emitNegInt(ref, inst)

		case SSA_ADD_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FADDd(dst, a1, a2)
			})

		case SSA_SUB_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FSUBd(dst, a1, a2)
			})

		case SSA_MUL_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FMULd(dst, a1, a2)
			})

		case SSA_DIV_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FDIVd(dst, a1, a2)
			})

		case SSA_NEG_FLOAT:
			ec.emitNegFloat(ref, inst)

		case SSA_FMADD:
			ec.emitFMADD(ref, inst)

		case SSA_FMSUB:
			ec.emitFMSUB(ref, inst)

		case SSA_BOX_INT:
			// Used for int→float conversion (SCVTF pattern)
			ec.emitBoxIntAsFloat(ref, inst)

		case SSA_EQ_INT:
			ec.emitCmpInt(inst, CondNE)

		case SSA_LT_INT:
			if i == ec.loopExitIdx && inst.AuxInt == -2 {
				// While-loop exit: branch to loop_done when NOT less-than (GE)
				a1 := ec.resolveIntRef(inst.Arg1, X0)
				a2 := ec.resolveIntRef(inst.Arg2, X1)
				ec.asm.CMPreg(a1, a2)
				ec.asm.BCond(CondGE, "loop_done")
			} else {
				ec.emitCmpInt(inst, CondGE) // branch if NOT less-than
			}

		case SSA_LE_INT:
			ec.emitCmpIntLE(i, inst)

		case SSA_LT_FLOAT:
			// Determine if this is a break guard (should exit past the loop):
			// 1. In a fully nested trace: inside inner loop body
			// 2. In a standalone inner trace: any float comparison is a break guard
			isBreakGuard := false
			if ec.innerLoopExitIdx >= 0 && i >= ec.innerLoopBodyStart && i < ec.innerLoopExitIdx {
				isBreakGuard = true // fully nested: inside inner loop
			} else if ec.innerLoopExitIdx < 0 {
				isBreakGuard = true // standalone inner trace: all float guards are breaks
			}
			if isBreakGuard {
				ec.emitCmpFloatBreak(inst, CondGE)
			} else {
				ec.emitCmpFloat(inst, CondGE)
			}

		case SSA_LE_FLOAT:
			ec.emitCmpFloatLE(i, inst)

		case SSA_GT_FLOAT:
			ec.emitCmpFloat(inst, CondLE) // branch if NOT greater-than

		case SSA_GUARD_TRUTHY:
			ec.emitGuardTruthy(inst)

		case SSA_MOVE:
			ec.emitMove(ref, inst)

		case SSA_LOAD_FIELD:
			ec.emitLoadField(ref, inst)

		case SSA_LOAD_TABLE_SHAPE:
			ec.emitLoadTableShape(ref, inst)

		case SSA_CHECK_SHAPE_ID:
			ec.emitCheckShapeId(inst)

		case SSA_STORE_FIELD:
			ec.emitStoreField(inst)

		case SSA_LOAD_ARRAY:
			ec.emitLoadArray(ref, inst)

		case SSA_STORE_ARRAY:
			ec.emitStoreArray(inst)

		case SSA_TABLE_LEN:
			ec.emitTableLen(ref, inst)

		case SSA_CALL:
			ec.emitCallExit(inst)

		case SSA_INTRINSIC:
			ec.emitIntrinsic(ref, inst)

		case SSA_LOAD_GLOBAL:
			// Table-type GETGLOBAL: native load from constant pool.
			// Non-table (function/int/float): call-exit to avoid slot reuse bugs.
			if inst.Type == SSATypeTable {
				ec.emitLoadGlobal(ref, inst)
			} else {
				ec.emitCallExitInst(inst)
			}

		case SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_CALL_INNER_TRACE, SSA_INNER_LOOP,
			SSA_PHI, SSA_STORE_SLOT, SSA_BOX_FLOAT,
			SSA_SIDE_EXIT, SSA_DIV_INT:
			// Not yet implemented — skip
		}
	}

	// Store-back: write all allocated register values back to memory before loop back-edge.
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Reload ALL allocated registers from memory after store-back.
	// This picks up changes made by the interpreter during side-exits
	// (e.g., `count = count + 1` executed by interpreter after guard failure).
	// Without this reload, loop-carried values like counters would be stale
	// after a side-exit-and-resume cycle.
	ec.emitReloadAll()
}

// ────────────────────────────────────────────────────────────────────────────
// Function body emission (for function-entry traces)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitFuncBody() {
	f := ec.f

	// Function traces have no loop exit — all comparisons are guards.
	ec.loopExitIdx = -1
	ec.innerLoopExitIdx = -1
	ec.innerLoopBodyStart = -1

	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)

		if f.AbsorbedMuls[ref] {
			continue
		}

		switch inst.Op {
		case SSA_NOP, SSA_SNAPSHOT, SSA_LOOP:
			// No code emitted

		case SSA_LOAD_SLOT:
			ec.emitLoadSlot(ref, inst)

		case SSA_UNBOX_INT:
			ec.emitUnboxInt(ref, inst)

		case SSA_UNBOX_FLOAT:
			ec.emitUnboxFloat(ref, inst)

		case SSA_CONST_NIL, SSA_CONST_BOOL:
			// Resolved at use sites, not emitted

		case SSA_ADD_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.ADDreg(dst, a1, a2)
			})

		case SSA_SUB_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.SUBreg(dst, a1, a2)
			})

		case SSA_MUL_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.MUL(dst, a1, a2)
			})

		case SSA_MOD_INT:
			ec.emitModInt(ref, inst)

		case SSA_NEG_INT:
			ec.emitNegInt(ref, inst)

		case SSA_ADD_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FADDd(dst, a1, a2)
			})

		case SSA_SUB_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FSUBd(dst, a1, a2)
			})

		case SSA_MUL_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FMULd(dst, a1, a2)
			})

		case SSA_DIV_FLOAT:
			ec.emitFloatArith(ref, inst, func(asm *Assembler, dst, a1, a2 FReg) {
				asm.FDIVd(dst, a1, a2)
			})

		case SSA_NEG_FLOAT:
			ec.emitNegFloat(ref, inst)

		case SSA_FMADD:
			ec.emitFMADD(ref, inst)

		case SSA_FMSUB:
			ec.emitFMSUB(ref, inst)

		case SSA_BOX_INT:
			ec.emitBoxIntAsFloat(ref, inst)

		case SSA_EQ_INT:
			ec.emitCmpInt(inst, CondNE)

		case SSA_LT_INT:
			ec.emitCmpInt(inst, CondGE)

		case SSA_LE_INT:
			ec.emitCmpIntLE(i, inst)

		case SSA_LT_FLOAT:
			ec.emitCmpFloat(inst, CondGE)

		case SSA_LE_FLOAT:
			ec.emitCmpFloatLE(i, inst)

		case SSA_GT_FLOAT:
			ec.emitCmpFloat(inst, CondLE)

		case SSA_GUARD_TRUTHY:
			ec.emitGuardTruthy(inst)

		case SSA_MOVE:
			ec.emitMove(ref, inst)

		case SSA_LOAD_FIELD:
			ec.emitLoadField(ref, inst)

		case SSA_LOAD_TABLE_SHAPE:
			ec.emitLoadTableShape(ref, inst)

		case SSA_CHECK_SHAPE_ID:
			ec.emitCheckShapeId(inst)

		case SSA_STORE_FIELD:
			ec.emitStoreField(inst)

		case SSA_LOAD_ARRAY:
			ec.emitLoadArray(ref, inst)

		case SSA_STORE_ARRAY:
			ec.emitStoreArray(inst)

		case SSA_TABLE_LEN:
			ec.emitTableLen(ref, inst)

		case SSA_CALL:
			ec.emitCallExit(inst)

		case SSA_SELF_CALL:
			ec.emitSelfCall(ref, inst)

		case SSA_INTRINSIC:
			ec.emitIntrinsic(ref, inst)

		case SSA_LOAD_GLOBAL:
			// Table-type GETGLOBAL: native load from constant pool.
			// Non-table (function/int/float): call-exit to avoid slot reuse bugs.
			if inst.Type == SSATypeTable {
				ec.emitLoadGlobal(ref, inst)
			} else {
				ec.emitCallExitInst(inst)
			}

		case SSA_CONST_INT:
			ec.emitConstInt(ref, inst)

		case SSA_CONST_FLOAT:
			ec.emitConstFloat(ref, inst)

		case SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_CALL_INNER_TRACE, SSA_INNER_LOOP,
			SSA_PHI, SSA_STORE_SLOT, SSA_BOX_FLOAT,
			SSA_SIDE_EXIT, SSA_DIV_INT:
			// Not yet implemented — skip
		}
	}
}

// emitFuncDone emits the function-done exit path.
// Stores back all registers to memory and exits with ExitCode=0.
// For self-call traces: if depth > 0, just RET to BL caller.
// If depth == 0, exit to VM with ExitCode=0.
func (ec *emitCtx) emitFuncDone() {
	asm := ec.asm

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	if ec.hasSelfCalls {
		// Check depth: if > 0, this is a nested self-call return
		asm.CBZ(X25, "func_done_outer")
		// Nested return: just RET to BL caller
		// The result is already stored back to memory (in the return slot)
		asm.RET()

		asm.Label("func_done_outer")
	}

	// Outermost return: exit to VM
	// Set ExitPC to 0 (function traces don't have a meaningful exit PC)
	asm.LoadImm64(X9, 0)
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Set ExitCode = 0 (function done / return)
	asm.LoadImm64(X0, 0)
	asm.B("epilogue")
}

// ────────────────────────────────────────────────────────────────────────────
// resolveIntRef: get the GPR holding an SSA ref's int value.
// If the ref is in a register, returns that register.
// Otherwise loads from memory into scratch.
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) resolveIntRef(ref SSARef, scratch Reg) Reg {
	if ref == SSARefNone || int(ref) >= len(ec.f.Insts) {
		return scratch
	}
	inst := &ec.f.Insts[ref]
	slot := int(inst.Slot)

	// Check for constant values BEFORE slot-level allocation.
	// A CONST_INT shares a slot with other instructions. The slot-level GPR
	// may have been overwritten by a later instruction targeting the same slot.
	// Always reload from the immediate to guarantee correctness.
	if inst.Op == SSA_CONST_INT {
		ec.asm.LoadImm64(scratch, inst.AuxInt)
		return scratch
	}

	// Check if this ref's value is in regSelfExtra (X28) from a self-call result
	// that had no GPR allocation. X28 is saved/restored across self-calls.
	if ec.selfCallExtraRef == ref {
		return regSelfExtra
	}

	// Slot-level allocation
	if slot >= 0 {
		if reg, ok := ec.regMap.IntReg(slot); ok {
			return reg
		}
	}

	// Load from memory
	if slot >= 0 {
		ec.asm.LDR(scratch, regRegs, slot*ValueSize)
		EmitUnboxInt(ec.asm, scratch, scratch)
		return scratch
	}

	return scratch
}

// resolveFloatRef: get the FPR holding an SSA ref's float value.
func (ec *emitCtx) resolveFloatRef(ref SSARef, scratch FReg) FReg {
	if ref == SSARefNone || int(ref) >= len(ec.f.Insts) {
		return scratch
	}
	inst := &ec.f.Insts[ref]

	// Check ref-level float allocation
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		return freg
	}

	// Check for float constant BEFORE slot-level allocation.
	// A CONST_FLOAT shares a slot with other instructions (e.g., MUL_FLOAT).
	// The slot-level FPR may have been overwritten by a later instruction
	// targeting the same slot, destroying the constant value. Always reload
	// from the immediate to guarantee correctness.
	if inst.Op == SSA_CONST_FLOAT {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(scratch, X0)
		return scratch
	}

	slot := int(inst.Slot)
	// Check slot-level float allocation
	if slot >= 0 {
		if freg, ok := ec.regMap.FloatReg(slot); ok {
			return freg
		}
	}

	// Load from memory
	if slot >= 0 {
		ec.asm.FLDRd(scratch, regRegs, slot*ValueSize)
		return scratch
	}

	return scratch
}

// getIntDst: get the destination GPR for an SSA ref's result.
func (ec *emitCtx) getIntDst(ref SSARef, inst *SSAInst, scratch Reg) Reg {
	slot := int(inst.Slot)
	if slot >= 0 {
		if reg, ok := ec.regMap.IntReg(slot); ok {
			return reg
		}
	}
	return scratch
}

// getFloatDst: get the destination FPR for an SSA ref's result.
func (ec *emitCtx) getFloatDst(ref SSARef, inst *SSAInst, scratch FReg) FReg {
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		return freg
	}
	slot := int(inst.Slot)
	if slot >= 0 {
		if freg, ok := ec.regMap.FloatReg(slot); ok {
			return freg
		}
	}
	return scratch
}

// ────────────────────────────────────────────────────────────────────────────
// Per-instruction emission: integer arithmetic
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitIntArith(ref SSARef, inst *SSAInst, op func(*Assembler, Reg, Reg, Reg)) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	dst := ec.getIntDst(ref, inst, X2)
	op(ec.asm, dst, a1, a2)
	ec.spillInt(ref, inst, dst)
}

func (ec *emitCtx) emitModInt(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	dst := ec.getIntDst(ref, inst, X2)
	// a % b = a - (a / b) * b
	ec.asm.SDIV(X3, a1, a2)     // X3 = a / b
	ec.asm.MSUB(dst, X3, a2, a1) // dst = a - X3 * b
	ec.spillInt(ref, inst, dst)
}

func (ec *emitCtx) emitNegInt(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	dst := ec.getIntDst(ref, inst, X1)
	ec.asm.NEG(dst, a1)
	ec.spillInt(ref, inst, dst)
}

// spillInt: if the dst register is a scratch register (not allocated),
// store the result back to memory.
func (ec *emitCtx) spillInt(ref SSARef, inst *SSAInst, dst Reg) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	// When an int value is written to a slot, remove any stale float tracking.
	// This prevents the float store-back from overwriting the slot with an old
	// float value after an int operation has updated it (e.g., quicksort swap
	// where slot 10 alternates between arr[j] (float) and i+1 (int)).
	delete(ec.floatSlotReg, slot)
	delete(ec.floatWrittenSlots, slot)
	if reg, ok := ec.regMap.IntReg(slot); ok && reg == dst {
		return // already in allocated register, no spill needed
	}
	// dst is scratch — store back to memory (NaN-boxed)
	EmitBoxIntFast(ec.asm, dst, dst, regTagInt)
	ec.asm.STR(dst, regRegs, slot*ValueSize)
}

// ────────────────────────────────────────────────────────────────────────────
// Per-instruction emission: float arithmetic
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitFloatArith(ref SSARef, inst *SSAInst, op func(*Assembler, FReg, FReg, FReg)) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	dst := ec.getFloatDst(ref, inst, D2)
	op(ec.asm, dst, a1, a2)
	ec.spillFloat(ref, inst, dst)
}

func (ec *emitCtx) emitNegFloat(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	dst := ec.getFloatDst(ref, inst, D1)
	// FNEGd: Dd = -Dn. ARM64 encoding: 0|00|11110|01|1|00001|10000|Rn|Rd
	// Not in our assembler yet — emit manually
	ec.asm.emit(0x1E614000 | uint32(a1)<<5 | uint32(dst))
	ec.spillFloat(ref, inst, dst)
}

func (ec *emitCtx) emitFMADD(ref SSARef, inst *SSAInst) {
	// FMADD: dst = Arg1 * Arg2 + AuxInt(ref)
	// ARM64 FMADDd(rd, rn, rm, ra) = ra + rn * rm
	a := ec.resolveFloatRef(inst.Arg1, D0)
	b := ec.resolveFloatRef(inst.Arg2, D1)
	c := ec.resolveFloatRef(SSARef(inst.AuxInt), D3) // addend
	dst := ec.getFloatDst(ref, inst, D2)
	ec.asm.FMADDd(dst, a, b, c)
	ec.spillFloat(ref, inst, dst)
}

func (ec *emitCtx) emitFMSUB(ref SSARef, inst *SSAInst) {
	// FMSUB: dst = AuxInt(ref) - Arg1 * Arg2
	// ARM64 FMSUBd(rd, rn, rm, ra) = ra - rn * rm
	a := ec.resolveFloatRef(inst.Arg1, D0)
	b := ec.resolveFloatRef(inst.Arg2, D1)
	c := ec.resolveFloatRef(SSARef(inst.AuxInt), D3) // minuend
	dst := ec.getFloatDst(ref, inst, D2)
	ec.asm.FMSUBd(dst, a, b, c)
	ec.spillFloat(ref, inst, dst)
}

// emitBoxIntAsFloat: SSA_BOX_INT used as int→float conversion
func (ec *emitCtx) emitBoxIntAsFloat(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	dst := ec.getFloatDst(ref, inst, D0)
	// SCVTF: convert signed int64 to float64
	ec.asm.SCVTF(dst, a1)
	ec.spillFloat(ref, inst, dst)
}

// spillFloat: if the dst FPR is scratch, store back to memory.
// Also tracks the slot→register mapping for the store-back.
func (ec *emitCtx) spillFloat(ref SSARef, inst *SSAInst, dst FReg) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	// Track which register holds this slot's current value
	ec.floatSlotReg[slot] = dst
	// Mark this slot as last-written by float, so int store-back skips it.
	ec.floatWrittenSlots[slot] = true
	if freg, ok := ec.regMap.FloatRefReg(ref); ok && freg == dst {
		return // already in allocated register
	}
	if freg, ok := ec.regMap.FloatReg(slot); ok && freg == dst {
		return // already in allocated register
	}
	// dst is scratch — store back to memory (raw float bits = NaN-boxed float)
	ec.asm.FSTRd(dst, regRegs, slot*ValueSize)
}

// ────────────────────────────────────────────────────────────────────────────
// Comparison instructions
// ────────────────────────────────────────────────────────────────────────────

// emitCmpInt handles SSA_EQ_INT.
// AuxInt encodes the "expected comparison result" (A field from OP_EQ).
// If A=1: guard passes when b == c (branch to side_exit if NE)
// If A=0: guard passes when b != c (branch to side_exit if EQ)
func (ec *emitCtx) emitCmpInt(inst *SSAInst, failCond Cond) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	ec.asm.CMPreg(a1, a2)
	if inst.AuxInt == 0 {
		// A=0: guard passes if NOT equal → fail if EQ
		failCond = failCond ^ 1 // invert condition
	}
	ec.emitGuardBranch(failCond, inst.PC)
}

// emitCmpIntLE handles SSA_LE_INT.
// For FORLOOP: guard passes if index <= limit → fail if GT (signed)
func (ec *emitCtx) emitCmpIntLE(idx int, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	ec.asm.CMPreg(a1, a2)
	// LE_INT: guard passes if a1 <= a2; exit if a1 > a2
	if idx == ec.loopExitIdx {
		// This is the OUTER FORLOOP exit check: branch to loop_done
		ec.asm.BCond(CondGT, "loop_done")
	} else if idx == ec.innerLoopExitIdx {
		// Inner FORLOOP exit: branch BACK to inner loop body if index <= limit,
		// fall through (continue outer body) if index > limit.
		// Store back inner loop registers to memory before branching back,
		// so the next iteration sees updated values.
		ec.emitInnerLoopStoreBack()
		ec.asm.BCond(CondLE, "inner_loop_body")
		// Fall through: inner loop done, continue outer body
	} else {
		ec.emitGuardBranch(CondGT, inst.PC)
	}
}

// emitCmpFloat handles float comparisons with a fail condition.
func (ec *emitCtx) emitCmpFloat(inst *SSAInst, failCond Cond) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	if inst.AuxInt == 0 {
		failCond = failCond ^ 1
	}
	ec.emitGuardBranch(failCond, inst.PC)
}

// emitCmpFloatBreak is like emitCmpFloat but branches to break_exit instead.
// Used for float comparison guards inside the inner loop body that represent
// break conditions (e.g., `if zr2+zi2 > 4.0 { break }`).
// The break_exit exits to the guard's PC so the VM re-executes the comparison
// and takes the break path (including any escaped=true assignments).
func (ec *emitCtx) emitCmpFloatBreak(inst *SSAInst, failCond Cond) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	if inst.AuxInt == 0 {
		failCond = failCond ^ 1
	}
	// Store the guard's PC for break_exit to use
	ec.breakGuardPC = inst.PC
	ec.asm.BCond(failCond, "break_exit")
}

// emitCmpFloatLE handles SSA_LE_FLOAT.
func (ec *emitCtx) emitCmpFloatLE(idx int, inst *SSAInst) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	// LE: guard passes if a1 <= a2; exit if GT
	if idx == ec.loopExitIdx {
		ec.asm.BCond(CondGT, "loop_done")
	} else if idx == ec.innerLoopExitIdx {
		ec.emitInnerLoopStoreBack()
		ec.asm.BCond(CondLE, "inner_loop_body")
	} else {
		ec.emitGuardBranch(CondGT, inst.PC)
	}
}

// emitGuardBranch emits a conditional branch to the side-exit path.
// Sets X9 = ExitPC before the conditional branch so side_exit_setup
// knows where the interpreter should resume.
//
// For function traces with self-calls, body guards branch to
// "self_call_body_guard" instead of "side_exit_setup". This allows
// the base case (e.g., n < 2 for fib) to be handled natively when
// inside a nested self-call (depth > 0), rather than side-exiting
// through the epilogue which would corrupt the ARM64 stack.
func (ec *emitCtx) emitGuardBranch(failCond Cond, pc int) {
	// Set ExitPC BEFORE the branch (X9 must be ready when side_exit_setup runs).
	// This is safe because X9 is a scratch register not used by the trace.
	ec.asm.LoadImm64(X9, int64(pc))
	if ec.hasSelfCalls {
		ec.asm.BCond(failCond, "self_call_body_guard")
	} else {
		ec.asm.BCond(failCond, "side_exit_setup")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Guard truthy
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitGuardTruthy(inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

	// If Arg1 refers to a compile-time constant (CONST_BOOL or CONST_NIL),
	// resolve the guard statically. These constants don't write to memory,
	// so reading from memory would give stale values.
	if int(inst.Arg1) < len(ec.f.Insts) {
		srcInst := &ec.f.Insts[inst.Arg1]
		if srcInst.Op == SSA_CONST_BOOL || srcInst.Op == SSA_CONST_NIL {
			isTruthy := srcInst.Op == SSA_CONST_BOOL && srcInst.AuxInt != 0
			if inst.AuxInt != 0 {
				// Guard passes if truthy
				if !isTruthy {
					// Constant is falsy → guard fails → unconditional side-exit
					ec.asm.LoadImm64(X9, int64(inst.PC))
					ec.asm.B("side_exit_setup")
				}
				// else: guard passes, emit nothing
			} else {
				// Guard passes if falsy
				if isTruthy {
					// Constant is truthy → guard fails → unconditional side-exit
					ec.asm.LoadImm64(X9, int64(inst.PC))
					ec.asm.B("side_exit_setup")
				}
				// else: guard passes, emit nothing
			}
			return
		}
	}

	// Set ExitPC for guard failure
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Load the NaN-boxed value from memory
	ec.asm.LDR(X0, regRegs, slot*ValueSize)

	// Check if nil: NB_ValNil = 0xFFFC000000000000
	ec.asm.LoadImm64(X1, nb_i64(NB_ValNil))
	ec.asm.CMPreg(X0, X1)

	if inst.AuxInt != 0 {
		// AuxInt=1 (C=1): guard passes if truthy → fail if nil
		ec.asm.BCond(CondEQ, "side_exit_setup")
		// Also check false: NB_ValFalse = 0xFFFD000000000000
		ec.asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		ec.asm.CMPreg(X0, X1)
		ec.asm.BCond(CondEQ, "side_exit_setup")
	} else {
		// AuxInt=0 (C=0): guard passes if falsy → fail if NOT nil AND NOT false
		// i.e., fail if truthy
		label := "guard_truthy_ok_" + itoa(ec.guardTruthyCount)
		ec.guardTruthyCount++
		ec.asm.BCond(CondEQ, label)
		ec.asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		ec.asm.CMPreg(X0, X1)
		ec.asm.BCond(CondEQ, label)
		// Not nil, not false → truthy → fail
		ec.asm.B("side_exit_setup")
		ec.asm.Label(label)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// MOVE instruction
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitMove(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

	if inst.Type == SSATypeFloat {
		src := ec.resolveFloatRef(inst.Arg1, D0)
		dst := ec.getFloatDst(ref, inst, D1)
		if src != dst {
			ec.asm.FMOVd(dst, src)
		}
		ec.spillFloat(ref, inst, dst)
	} else {
		src := ec.resolveIntRef(inst.Arg1, X0)
		dst := ec.getIntDst(ref, inst, X1)
		if src != dst {
			ec.asm.MOVreg(dst, src)
		}
		ec.spillInt(ref, inst, dst)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_SLOT (in loop body — reload from memory)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadSlot(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

	if inst.Type == SSATypeFloat {
		dst := ec.getFloatDst(ref, inst, D0)
		ec.asm.FLDRd(dst, regRegs, slot*ValueSize)
	} else if inst.Type == SSATypeInt {
		dst := ec.getIntDst(ref, inst, X0)
		ec.asm.LDR(dst, regRegs, slot*ValueSize)
		EmitUnboxInt(ec.asm, dst, dst)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// UNBOX_INT / UNBOX_FLOAT (in loop body)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitUnboxInt(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	if reg, ok := ec.regMap.IntReg(slot); ok {
		ec.asm.LDR(reg, regRegs, slot*ValueSize)
		EmitUnboxInt(ec.asm, reg, reg)
	}
}

func (ec *emitCtx) emitUnboxFloat(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		ec.asm.FLDRd(freg, regRegs, slot*ValueSize)
	} else if freg, ok := ec.regMap.FloatReg(slot); ok {
		ec.asm.FLDRd(freg, regRegs, slot*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CONST_INT / CONST_FLOAT (in loop body)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitConstInt(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	if reg, ok := ec.regMap.IntReg(slot); ok {
		ec.asm.LoadImm64(reg, inst.AuxInt)
	} else {
		// Store directly to memory as NaN-boxed int
		ec.asm.LoadImm64(X0, inst.AuxInt)
		EmitBoxIntFast(ec.asm, X0, X0, regTagInt)
		ec.asm.STR(X0, regRegs, slot*ValueSize)
	}
}

func (ec *emitCtx) emitConstFloat(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	// Always load into ref-level register if one is allocated (even for slot=-1 constants).
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(freg, X0)
		if slot >= 0 {
			ec.floatSlotReg[slot] = freg
		}
		return
	}
	if slot < 0 {
		return
	}
	if freg, ok := ec.regMap.FloatReg(slot); ok {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(freg, X0)
		ec.floatSlotReg[slot] = freg
	} else {
		// Store directly to memory (raw float bits = NaN-boxed float)
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.STR(X0, regRegs, slot*ValueSize)
		delete(ec.floatSlotReg, slot) // value is in memory, not a register
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_FIELD: table field access
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadField(ref SSARef, inst *SSAInst) {
	fieldIdx := int(int32(inst.AuxInt))

	// Invalid field index (not captured during recording) → skip.
	// This happens when GETFIELD targets a library table (e.g., math.sqrt)
	// whose field index wasn't resolved. The result is typically dead code
	// (the CALL was replaced by SSA_INTRINSIC). Emitting nothing is safe
	// because nothing references this instruction's slot.
	if fieldIdx < 0 {
		return
	}

	// Set ExitPC for any guard failure in this instruction
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the TABLE slot from Arg1 (the SSA ref for the table).
	// inst.Slot is the DESTINATION slot (ir.A), NOT the table slot.
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		ec.asm.B("side_exit_setup")
		return
	}

	// Load table NaN-boxed value. If the table source is a LOAD_GLOBAL,
	// load directly from the trace constant pool (regConsts) to avoid
	// slot conflicts with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		ec.asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		ec.asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	// Check it's a table
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")
	// Extract pointer
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Check no metatable
	ec.asm.LDR(X1, X0, TableOffMetatable)
	ec.asm.CBNZ(X1, "side_exit_setup")

	// Load field value: svals[fieldIdx]
	ec.asm.LDR(X1, X0, TableOffSvals) // X1 = svals slice data pointer
	ec.asm.LDR(X2, X1, fieldIdx*ValueSize) // X2 = svals[fieldIdx] (NaN-boxed)
	if inst.Type == SSATypeFloat {
		if freg, ok := ec.regMap.FloatRefReg(ref); ok {
			ec.asm.FMOVtoFP(freg, X2)
		} else if freg, ok := ec.regMap.FloatReg(int(inst.Slot)); ok {
			ec.asm.FMOVtoFP(freg, X2)
		} else {
			// Store to memory (raw float bits)
			ec.asm.STR(X2, regRegs, int(inst.Slot)*ValueSize)
		}
	} else if inst.Type == SSATypeInt {
		EmitUnboxInt(ec.asm, X2, X2)
		if reg, ok := ec.regMap.IntReg(int(inst.Slot)); ok {
			ec.asm.MOVreg(reg, X2)
		} else {
			// Store to memory (NaN-boxed)
			EmitBoxIntFast(ec.asm, X2, X2, regTagInt)
			ec.asm.STR(X2, regRegs, int(inst.Slot)*ValueSize)
		}
	} else {
		// Unknown type — store raw NaN-boxed value to memory
		ec.asm.STR(X2, regRegs, int(inst.Slot)*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_TABLE_SHAPE: load table shape pointer for guard
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadTableShape(ref SSARef, inst *SSAInst) {
	// Set ExitPC for any guard failure
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the table value (Arg1 is the SSA ref for the table)
	tableSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tableSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tableSlot < 0 {
		// Invalid table slot - should not happen in valid traces
		ec.asm.B("side_exit_setup")
		return
	}

	// Load table NaN-boxed value from slot
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		ec.asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		ec.asm.LDR(X0, regRegs, tableSlot*ValueSize)
	}

	// Check it's a table
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")

	// Extract table pointer
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Load shape pointer from table (at offset TableOffShape)
	// Result is in X0: the shape pointer (or nil if no shape)
	ec.asm.LDR(X0, X0, TableOffShape)

	// Store shape pointer in a scratch register for use by CHECK_SHAPE_ID
	// We use X2 for the shape pointer (X2 is a scratch register)
	ec.asm.MOVreg(X2, X0)
}

// ────────────────────────────────────────────────────────────────────────────
// CHECK_SHAPE_ID: guard that table.shape.ID matches expected
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitCheckShapeId(inst *SSAInst) {
	// Set ExitPC (X9) before any potential guard failure
	// BailoutID is stored in AuxInt
	bailoutID := int(inst.AuxInt)
	ec.asm.LoadImm64(X9, int64(bailoutID))

	// X2 contains the shape pointer from LOAD_TABLE_SHAPE
	// CBZ X2, side_exit_setup - if shape is nil, fail guard
	ec.asm.CBZ(X2, "side_exit_setup")

	// Load shape.ID (first field of Shape struct at offset 0)
	// Shape struct: ID uint32, FieldKeys []string, etc.
	// We only need to check the ID at offset 0
	ec.asm.LDRW(X0, X2, 0) // Load 32-bit shape ID

	// Expected shape ID is in inst.AuxInt (from LOAD_TABLE_SHAPE)
	// But for CHECK_SHAPE_ID, AuxInt contains bailout ID
	// We need to get the expected shape ID from the LOAD_TABLE_SHAPE instruction
	// that produced the shape reference (inst.Arg1)
	expectedShapeID := uint32(0)
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		shapeLoadInst := ec.f.Insts[inst.Arg1]
		if shapeLoadInst.Op == SSA_LOAD_TABLE_SHAPE {
			expectedShapeID = uint32(shapeLoadInst.AuxInt)
		}
	}

	// Compare loaded shape ID with expected
	ec.asm.LoadImm64(X1, int64(expectedShapeID))
	ec.asm.CMPreg(X0, X1)

	// Branch to side-exit if shape IDs don't match
	ec.asm.BCond(CondNE, "side_exit_setup")

	// Guard passed - shape is valid, direct svals[idx] access in LOAD_FIELD is safe
	// No additional code needed here
}

// ────────────────────────────────────────────────────────────────────────────
// STORE_FIELD: table field write
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitStoreField(inst *SSAInst) {
	// inst.Slot = table slot (ir.A), inst.AuxInt = fieldIndex
	// inst.Arg1 = table ref, inst.Arg2 = value ref
	fieldIdx := int(int32(inst.AuxInt))
	tblSlot := int(inst.Slot)

	// Invalid field index → side-exit
	if fieldIdx < 0 {
		ec.emitCallExitInst(inst)
		return
	}

	// Set ExitPC for any guard failure
	ec.asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the value to store FIRST (before loading table pointer).
	// resolveFloatRef may clobber X0 (for constant loads), so we must
	// do this before we put the table pointer in X0.
	valInst := &ec.f.Insts[inst.Arg2]
	if valInst.Type == SSATypeFloat {
		freg := ec.resolveFloatRef(inst.Arg2, D0)
		ec.asm.FMOVtoGP(X3, freg)
	} else if valInst.Type == SSATypeInt {
		reg := ec.resolveIntRef(inst.Arg2, X3)
		EmitBoxIntFast(ec.asm, X3, reg, regTagInt)
	} else {
		// Load raw value from memory
		valSlot := int(valInst.Slot)
		if valSlot >= 0 {
			ec.asm.LDR(X3, regRegs, valSlot*ValueSize)
		}
	}

	// Load table pointer (X3 holds the value, X0 is free for table).
	// If the table source is a LOAD_GLOBAL, load from the trace constant pool
	// to avoid slot conflicts with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		ec.asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		ec.asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Check no metatable
	ec.asm.LDR(X1, X0, TableOffMetatable)
	ec.asm.CBNZ(X1, "side_exit_setup")

	// Store to svals[fieldIdx]
	ec.asm.LDR(X1, X0, TableOffSvals)
	ec.asm.STR(X3, X1, fieldIdx*ValueSize)
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_ARRAY / STORE_ARRAY
// ────────────────────────────────────────────────────────────────────────────

// emitLoadArray: R(A) = table[key] (integer index, native codegen)
//
// SSA encoding: Arg1=tableRef, Arg2=keyRef, Slot=destination slot
// The table's register slot is found via ec.f.Insts[inst.Arg1].Slot.
//
// Handles all arrayKind variants (Mixed, Int, Float, Bool) with
// runtime dispatch. Side-exits on bounds check failure or nil table.
// Falls back to call-exit for non-scalar result types (table, string, etc.)
// to avoid nested table access issues.
func (ec *emitCtx) emitLoadArray(ref SSARef, inst *SSAInst) {
	// Table-type results: load NaN-boxed value from Mixed array, store to memory slot.
	// LOAD_FIELD/STORE_FIELD will read the table from memory.
	if inst.Type == SSATypeTable {
		ec.emitLoadArrayTable(inst)
		return
	}
	// Fall back to call-exit for other non-scalar result types (string, nil, unknown).
	if inst.Type != SSATypeInt && inst.Type != SSATypeFloat && inst.Type != SSATypeBool {
		ec.emitCallExit(inst)
		return
	}

	asm := ec.asm
	seq := ec.arraySeq
	ec.arraySeq++

	// Unique labels for this instance
	lMixed := "la_mixed_" + itoa(seq)
	lInt := "la_int_" + itoa(seq)
	lFloat := "la_float_" + itoa(seq)
	lBool := "la_bool_" + itoa(seq)
	lDone := "la_done_" + itoa(seq)

	// Set ExitPC for any guard failure
	asm.LoadImm64(X9, int64(inst.PC))

	// 1. Resolve table slot from Arg1
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		// Can't resolve table → side-exit
		asm.B("side_exit_setup")
		return
	}

	// 2. Load table NaN-boxed value. If the table source is a LOAD_GLOBAL,
	// load from the trace constant pool (regConsts) to avoid slot conflicts
	// with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// 3. Check no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// 4. Resolve key (integer index) into X3
	keyReg := ec.resolveIntRef(inst.Arg2, X3)
	if keyReg != X3 {
		asm.MOVreg(X3, keyReg)
	}
	// X3 = integer key (0-indexed)

	// 5. Load arrayKind and dispatch
	asm.LDRB(X4, X0, TableOffArrayKind)

	asm.CMPimm(X4, AKMixed)
	asm.BCond(CondEQ, lMixed)
	asm.CMPimm(X4, AKInt)
	asm.BCond(CondEQ, lInt)
	asm.CMPimm(X4, AKFloat)
	asm.BCond(CondEQ, lFloat)
	asm.CMPimm(X4, AKBool)
	asm.BCond(CondEQ, lBool)
	// Unknown arrayKind → side-exit
	asm.B("side_exit_setup")

	// --- ArrayMixed: array []Value at TableOffArray ---
	asm.Label(lMixed)
	asm.LDR(X5, X0, TableOffArray)   // X5 = array data ptr
	asm.LDR(X6, X0, TableOffArray+8) // X6 = array len
	asm.CMPreg(X3, X6)               // key < len? (unsigned)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3) // X7 = array[key] (8-byte NaN-boxed Value, LSL #3)
	asm.B(lDone)

	// --- ArrayInt: intArray []int64 at TableOffIntArray ---
	asm.Label(lInt)
	asm.LDR(X5, X0, TableOffIntArray)   // X5 = intArray data ptr
	asm.LDR(X6, X0, TableOffIntArray+8) // X6 = intArray len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3) // X7 = intArray[key] (raw int64)
	// Box as NaN-boxed int
	EmitBoxIntFast(asm, X7, X7, regTagInt)
	asm.B(lDone)

	// --- ArrayFloat: floatArray []float64 at TableOffFloatArray ---
	asm.Label(lFloat)
	asm.LDR(X5, X0, TableOffFloatArray)   // X5 = floatArray data ptr
	asm.LDR(X6, X0, TableOffFloatArray+8) // X6 = floatArray len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3) // X7 = floatArray[key] (raw float64 bits = NaN-boxed float)
	// Float64 bits are already NaN-boxed (identity encoding for non-tagged values)
	asm.B(lDone)

	// --- ArrayBool: boolArray []byte at TableOffBoolArray ---
	asm.Label(lBool)
	asm.LDR(X5, X0, TableOffBoolArray)   // X5 = boolArray data ptr
	asm.LDR(X6, X0, TableOffBoolArray+8) // X6 = boolArray len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// Byte load: LDRB with register offset (X5 + X3)
	asm.LDRBreg(X7, X5, X3) // X7 = boolArray[key] (0=nil, 1=false, 2=true)
	// Convert byte encoding to NaN-boxed value:
	//   0 → nil (NB_ValNil)
	//   1 → false (NB_ValFalse = NB_TagBool | 0)
	//   2 → true (NB_TagBool | 1)
	// CMP X7, #2
	asm.CMPimm(X7, 2)
	asm.BCond(CondEQ, "la_bool_true_"+itoa(seq))
	asm.CMPimm(X7, 1)
	asm.BCond(CondEQ, "la_bool_false_"+itoa(seq))
	// 0 = nil
	EmitBoxNil(asm, X7)
	asm.B(lDone)
	asm.Label("la_bool_true_" + itoa(seq))
	asm.LoadImm64(X7, nb_i64(NB_TagBool|1)) // NB_TagBool | 1 = true
	asm.B(lDone)
	asm.Label("la_bool_false_" + itoa(seq))
	asm.LoadImm64(X7, nb_i64(NB_TagBool)) // NB_TagBool | 0 = false
	// Fall through to done

	// --- Done: X7 = NaN-boxed result value ---
	asm.Label(lDone)

	// Store to destination register based on result type
	dstSlot := int(inst.Slot)
	if inst.Type == SSATypeFloat {
		if freg, ok := ec.regMap.FloatRefReg(ref); ok {
			asm.FMOVtoFP(freg, X7)
		} else if freg, ok := ec.regMap.FloatReg(dstSlot); ok {
			asm.FMOVtoFP(freg, X7)
		} else {
			asm.STR(X7, regRegs, dstSlot*ValueSize)
		}
	} else if inst.Type == SSATypeInt {
		EmitUnboxInt(asm, X7, X7)
		if reg, ok := ec.regMap.IntReg(dstSlot); ok {
			asm.MOVreg(reg, X7)
		} else {
			EmitBoxIntFast(asm, X7, X7, regTagInt)
			asm.STR(X7, regRegs, dstSlot*ValueSize)
		}
	} else if inst.Type == SSATypeBool {
		// Bool result: store NaN-boxed value to memory
		// The trace loop will use GUARD_TRUTHY to test it.
		asm.STR(X7, regRegs, dstSlot*ValueSize)
	} else {
		// Unknown type — store raw NaN-boxed value to memory
		asm.STR(X7, regRegs, dstSlot*ValueSize)
	}
}

// emitLoadArrayTable handles LOAD_ARRAY when the result type is SSATypeTable.
// The source table always uses ArrayMixed for table-valued elements.
// Loads the NaN-boxed table pointer from the Mixed array and stores it to the
// destination slot in memory (tables are never kept in registers).
func (ec *emitCtx) emitLoadArrayTable(inst *SSAInst) {
	asm := ec.asm

	// Set ExitPC for any guard failure
	asm.LoadImm64(X9, int64(inst.PC))

	// 1. Resolve table slot from Arg1
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		asm.B("side_exit_setup")
		return
	}

	// 2. Load source table NaN-boxed value. If the table source is a
	// LOAD_GLOBAL, load from the trace constant pool (regConsts) to avoid
	// slot conflicts with int/float register allocations.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// 3. Check no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// 4. Resolve key (integer index) into X3
	keyReg := ec.resolveIntRef(inst.Arg2, X3)
	if keyReg != X3 {
		asm.MOVreg(X3, keyReg)
	}

	// 5. Load from Mixed array (tables-of-tables are always ArrayMixed)
	asm.LDR(X5, X0, TableOffArray)   // X5 = array data ptr
	asm.LDR(X6, X0, TableOffArray+8) // X6 = array len
	asm.CMPreg(X3, X6)               // key < len? (unsigned)
	asm.BCond(CondGE, "side_exit_setup")
	asm.LDRreg(X7, X5, X3)           // X7 = array[key] (NaN-boxed Value, LSL #3)

	// 6. Verify the loaded value is a table
	EmitCheckIsTableFull(asm, X7, X1, X2, "side_exit_setup")

	// 7. Store the NaN-boxed table value to the destination slot in memory
	dstSlot := int(inst.Slot)
	asm.STR(X7, regRegs, dstSlot*ValueSize)
	// Clear stale float tracking: this slot now holds a table pointer.
	// Without this, store-back would write a stale FPR value to this slot,
	// corrupting the table pointer.
	delete(ec.floatSlotReg, dstSlot)
	delete(ec.floatWrittenSlots, dstSlot)
}

// emitStoreArray: table[key] = value (integer index, native codegen)
//
// SSA encoding (after builder fix): Arg1=keyRef, Arg2=valRef, Slot=table slot
// The table is loaded directly from Slot (the table's register slot).
//
// Handles all arrayKind variants with runtime dispatch.
func (ec *emitCtx) emitStoreArray(inst *SSAInst) {
	asm := ec.asm
	seq := ec.arraySeq
	ec.arraySeq++

	// Unique labels for this instance
	lMixed := "sa_mixed_" + itoa(seq)
	lInt := "sa_int_" + itoa(seq)
	lFloat := "sa_float_" + itoa(seq)
	lBool := "sa_bool_" + itoa(seq)
	lDone := "sa_done_" + itoa(seq)

	tblSlot := int(inst.Slot)

	// Set ExitPC for any guard failure
	asm.LoadImm64(X9, int64(inst.PC))

	// 1. Load table NaN-boxed value. Check if the table slot was produced
	// by a LOAD_GLOBAL — if so, load from the trace constant pool.
	loadedFromConsts := false
	for j := 0; j < len(ec.f.Insts); j++ {
		si := &ec.f.Insts[j]
		if si.Op == SSA_LOAD_GLOBAL && int(si.Slot) == tblSlot {
			asm.LDR(X0, regConsts, int(si.AuxInt)*ValueSize)
			loadedFromConsts = true
			break
		}
	}
	if !loadedFromConsts {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// 2. Check no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// 3. Resolve key (integer index) into X3
	keyReg := ec.resolveIntRef(inst.Arg1, X3)
	if keyReg != X3 {
		asm.MOVreg(X3, keyReg)
	}
	// X3 = integer key (0-indexed)

	// 4. Resolve value to store into X8
	// The value is in Arg2. We need the NaN-boxed form for ArrayMixed,
	// or the raw form for typed arrays.
	valInst := &ec.f.Insts[inst.Arg2]
	// Prepare NaN-boxed value in X8 for ArrayMixed path
	if valInst.Type == SSATypeFloat {
		freg := ec.resolveFloatRef(inst.Arg2, D0)
		asm.FMOVtoGP(X8, freg)
	} else if valInst.Type == SSATypeInt {
		reg := ec.resolveIntRef(inst.Arg2, X8)
		EmitBoxIntFast(asm, X8, reg, regTagInt)
	} else if valInst.Type == SSATypeBool {
		// For bool constants, always use the compile-time constant.
		// Never read from memory because the slot may have been overwritten
		// by a different trace's store-back (e.g., an int count variable
		// reusing the same slot on a subsequent function call).
		if valInst.Op == SSA_CONST_BOOL {
			if valInst.AuxInt != 0 {
				asm.LoadImm64(X8, nb_i64(NB_TagBool|1)) // true
			} else {
				asm.LoadImm64(X8, nb_i64(NB_TagBool)) // false
			}
		} else {
			// Non-constant bool: load from memory
			valSlot := int(valInst.Slot)
			if valSlot >= 0 {
				asm.LDR(X8, regRegs, valSlot*ValueSize)
			} else {
				asm.LoadImm64(X8, nb_i64(NB_ValNil))
			}
		}
	} else if valInst.Op == SSA_CONST_NIL {
		// Constant nil
		asm.LoadImm64(X8, nb_i64(NB_ValNil))
	} else {
		// Unknown type — load from memory
		valSlot := int(valInst.Slot)
		if valSlot >= 0 {
			asm.LDR(X8, regRegs, valSlot*ValueSize)
		} else {
			asm.LoadImm64(X8, nb_i64(NB_ValNil))
		}
	}
	// X8 = NaN-boxed value to store

	// 5. Load arrayKind and dispatch
	// Need to reload X0 (table ptr) since resolveIntRef/resolveFloatRef may have clobbered it
	asm.LDR(X0, regRegs, tblSlot*ValueSize)
	EmitExtractPtr(asm, X0, X0)
	asm.LDRB(X4, X0, TableOffArrayKind)

	asm.CMPimm(X4, AKMixed)
	asm.BCond(CondEQ, lMixed)
	asm.CMPimm(X4, AKInt)
	asm.BCond(CondEQ, lInt)
	asm.CMPimm(X4, AKFloat)
	asm.BCond(CondEQ, lFloat)
	asm.CMPimm(X4, AKBool)
	asm.BCond(CondEQ, lBool)
	asm.B("side_exit_setup")

	// --- ArrayMixed: array []Value at TableOffArray ---
	asm.Label(lMixed)
	asm.LDR(X5, X0, TableOffArray)   // X5 = array data ptr
	asm.LDR(X6, X0, TableOffArray+8) // X6 = array len
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	asm.STRreg(X8, X5, X3) // array[key] = value (8-byte NaN-boxed, LSL #3)
	asm.B(lDone)

	// --- ArrayInt: intArray []int64 at TableOffIntArray ---
	asm.Label(lInt)
	asm.LDR(X5, X0, TableOffIntArray)
	asm.LDR(X6, X0, TableOffIntArray+8)
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// Need raw int64 from NaN-boxed value in X8
	EmitUnboxInt(asm, X7, X8)
	asm.STRreg(X7, X5, X3)
	asm.B(lDone)

	// --- ArrayFloat: floatArray []float64 at TableOffFloatArray ---
	asm.Label(lFloat)
	asm.LDR(X5, X0, TableOffFloatArray)
	asm.LDR(X6, X0, TableOffFloatArray+8)
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// X8 holds NaN-boxed float64 bits = raw IEEE 754 = correct for float64
	asm.STRreg(X8, X5, X3)
	asm.B(lDone)

	// --- ArrayBool: boolArray []byte at TableOffBoolArray ---
	asm.Label(lBool)
	asm.LDR(X5, X0, TableOffBoolArray)
	asm.LDR(X6, X0, TableOffBoolArray+8)
	asm.CMPreg(X3, X6)
	asm.BCond(CondGE, "side_exit_setup")
	// Convert NaN-boxed bool to byte encoding:
	//   NB_ValNil → 0, NB_TagBool|0 (false) → 1, NB_TagBool|1 (true) → 2
	// Check if it's a bool by checking tag
	asm.LSRimm(X7, X8, 48)
	asm.MOVimm16(X6, NB_TagBoolShr48)
	asm.CMPreg(X7, X6)
	asm.BCond(CondNE, "sa_bool_nil_"+itoa(seq))
	// It's a bool. Check payload bit 0: 0=false, 1=true
	asm.LoadImm64(X6, 1)
	asm.ANDreg(X7, X8, X6) // X7 = 0 (false) or 1 (true)
	asm.ADDimm(X7, X7, 1)   // X7 = 1 (false) or 2 (true)
	asm.B("sa_bool_store_" + itoa(seq))
	asm.Label("sa_bool_nil_" + itoa(seq))
	asm.MOVimm16(X7, 0) // nil → 0
	asm.Label("sa_bool_store_" + itoa(seq))
	asm.STRBreg(X7, X5, X3) // boolArray[key] = byte
	// Fall through to done

	asm.Label(lDone)
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_GLOBAL: native load from trace constant pool
// ────────────────────────────────────────────────────────────────────────────

// emitLoadGlobal loads a global variable's value from the trace constant pool.
// At recording time, the GETGLOBAL result was captured into trace.Constants[AuxInt].
// At runtime, we load the NaN-boxed value from regConsts (X27) and store it to
// the destination slot. For table-type globals (the common case for nbody/sieve),
// this replaces the expensive call-exit round-trip with a single load+store.
func (ec *emitCtx) emitLoadGlobal(ref SSARef, inst *SSAInst) {
	constIdx := int(inst.AuxInt) // index into trace constant pool
	dstSlot := int(inst.Slot)

	if dstSlot < 0 {
		return
	}

	// Bounds check: ensure constIdx is valid for the trace constant pool.
	nConsts := len(ec.f.Trace.Constants)
	if constIdx < 0 || constIdx >= nConsts {
		// Out of bounds: fall back to call-exit
		ec.emitCallExitInst(inst)
		return
	}

	asm := ec.asm

	// For table-type globals, we do NOT write to the VM register slot.
	// LOAD_FIELD and STORE_FIELD load the table pointer directly from regConsts
	// when the source is a LOAD_GLOBAL. This avoids conflicts where the same
	// slot is used for both a table pointer (LOAD_GLOBAL) and an int/float value
	// (other instructions) in the same loop iteration.
	if inst.Type == SSATypeTable {
		// Write table pointer to memory. LOAD_FIELD/STORE_FIELD read from
		// regConsts when source is LOAD_GLOBAL, but LOAD_ARRAY reads from
		// the VM register slot.
		asm.LDR(X0, regConsts, constIdx*ValueSize)
		asm.STR(X0, regRegs, dstSlot*ValueSize)
		// Clear stale float tracking: this slot now holds a table pointer.
		delete(ec.floatSlotReg, dstSlot)
		delete(ec.floatWrittenSlots, dstSlot)
		return
	}

	// For non-table globals (int, float), load from constant pool and store to
	// the VM register slot + optional register.
	asm.LDR(X0, regConsts, constIdx*ValueSize)
	asm.STR(X0, regRegs, dstSlot*ValueSize)

	if inst.Type == SSATypeFloat {
		// Float globals: load into FPR if allocated
		if freg, ok := ec.regMap.FloatRefReg(ref); ok {
			asm.FMOVtoFP(freg, X0)
			ec.floatSlotReg[dstSlot] = freg
		} else if freg, ok := ec.regMap.FloatReg(dstSlot); ok {
			asm.FMOVtoFP(freg, X0)
			ec.floatSlotReg[dstSlot] = freg
		}
	} else if inst.Type == SSATypeInt {
		// Int globals: unbox and load into GPR if allocated.
		// Clear stale float tracking: this slot now holds an int, not a float.
		// Without this, store-back would write a stale float FPR value to this
		// slot, overwriting the correct int loaded from the constant pool.
		delete(ec.floatSlotReg, dstSlot)
		delete(ec.floatWrittenSlots, dstSlot)
		if reg, ok := ec.regMap.IntReg(dstSlot); ok {
			EmitUnboxInt(asm, reg, X0)
		}
	}
	// Table and other types: value is in memory, no register allocation needed.
}

// ────────────────────────────────────────────────────────────────────────────
// TABLE_LEN
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitTableLen(ref SSARef, inst *SSAInst) {
	dstSlot := int(inst.Slot)
	if dstSlot < 0 {
		ec.emitCallExit(inst)
		return
	}

	asm := ec.asm

	// Set ExitPC for guard failures
	asm.LoadImm64(X9, int64(inst.PC))

	// Resolve the table source. Arg1 is the SSA ref for the table.
	tblSlot := -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}
	if tblSlot < 0 {
		asm.B("side_exit_setup")
		return
	}

	// Load table NaN-boxed value. If the table source is a LOAD_GLOBAL,
	// load from regConsts to avoid slot conflicts.
	tblSrcInst := &ec.f.Insts[inst.Arg1]
	if tblSrcInst.Op == SSA_LOAD_GLOBAL {
		constIdx := int(tblSrcInst.AuxInt)
		asm.LDR(X0, regConsts, constIdx*ValueSize)
	} else {
		asm.LDR(X0, regRegs, tblSlot*ValueSize)
	}

	// Check it's a table
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit_setup")
	// Extract pointer
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit_setup")

	// Guard: no metatable (metatable could have __len metamethod)
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit_setup")

	// Load array.len: the []Value slice header is at TableOffArray.
	// Slice layout: (ptr, len, cap) = (8, 8, 8). So len is at TableOffArray + 8.
	asm.LDR(X1, X0, TableOffArray+8) // X1 = array length (int64)

	// Store result to destination slot as NaN-boxed int.
	dst := ec.getIntDst(ref, inst, X1)
	if dst != X1 {
		asm.MOVreg(dst, X1)
	}
	ec.spillInt(ref, inst, dst)
}

// ────────────────────────────────────────────────────────────────────────────
// CALL (call-exit)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitCallExit(inst *SSAInst) {
	ec.emitCallExitInst(inst)
}

func (ec *emitCtx) emitCallExitInst(inst *SSAInst) {
	asm := ec.asm
	ec.hasCallExit = true

	// Store back ALL modified registers to memory (type-safe) before exiting.
	// The interpreter needs to see current register values to execute the instruction.
	ec.emitStoreBackTypeSafe()

	// Set ExitPC to the call instruction's bytecode PC
	asm.LoadImm64(X9, int64(inst.PC))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Exit with code 1 (side-exit). The interpreter resumes at ExitPC,
	// executes the CALL instruction (including any nested loops/recursion),
	// then FORLOOP back-edge re-enters the trace. No resume dispatch needed.
	asm.LoadImm64(X0, 1)
	asm.B("epilogue")
}

// ────────────────────────────────────────────────────────────────────────────
// Intrinsics
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitIntrinsic(ref SSARef, inst *SSAInst) {
	switch int(inst.AuxInt) {
	// --- Float unary intrinsics: sqrt, abs, floor, ceil ---
	case IntrinsicSqrt:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FSQRTd(dst, src)
		})
	case IntrinsicAbs:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FABSd(dst, src)
		})
	case IntrinsicFloor:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FRINTMd(dst, src)
		})
	case IntrinsicCeil:
		ec.emitFloatUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src FReg) {
			asm.FRINTPd(dst, src)
		})

	// --- Float binary intrinsics: max, min ---
	case IntrinsicMax:
		ec.emitFloatBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b FReg) {
			asm.FMAXNMd(dst, a, b)
		})
	case IntrinsicMin:
		ec.emitFloatBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b FReg) {
			asm.FMINNMd(dst, a, b)
		})

	// --- Integer binary intrinsics: bit32 ---
	case IntrinsicBand:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.ANDreg(dst, a, b)
		})
	case IntrinsicBor:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.ORRreg(dst, a, b)
		})
	case IntrinsicBxor:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.EORreg(dst, a, b)
		})
	case IntrinsicLshift:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.LSLreg(dst, a, b)
		})
	case IntrinsicRshift:
		ec.emitIntBinaryIntrinsic(ref, inst, func(asm *Assembler, dst, a, b Reg) {
			asm.LSRreg(dst, a, b)
		})

	// --- Integer unary intrinsic: bnot ---
	case IntrinsicBnot:
		ec.emitIntUnaryIntrinsic(ref, inst, func(asm *Assembler, dst, src Reg) {
			// MVN Xd, Xm = ORN Xd, XZR, Xm
			asm.ORNreg(dst, XZR, src)
		})

	default:
		// Unknown intrinsic — fall back to call-exit
		ec.emitCallExitInst(inst)
	}
}

// emitFloatUnaryIntrinsic: R(A) = op(R(A+1))
func (ec *emitCtx) emitFloatUnaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, FReg, FReg)) {
	argSlot := int(inst.Slot) + 1

	var argFReg FReg = D0
	if freg, ok := ec.regMap.FloatReg(argSlot); ok {
		argFReg = freg
	} else {
		ec.asm.FLDRd(D0, regRegs, argSlot*ValueSize)
		argFReg = D0
	}

	dstFReg := ec.getFloatDst(ref, inst, D1)
	op(ec.asm, dstFReg, argFReg)
	ec.spillFloat(ref, inst, dstFReg)
}

// emitFloatBinaryIntrinsic: R(A) = op(R(A+1), R(A+2))
func (ec *emitCtx) emitFloatBinaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, FReg, FReg, FReg)) {
	argSlot1 := int(inst.Slot) + 1
	argSlot2 := int(inst.Slot) + 2

	var a1 FReg = D0
	if freg, ok := ec.regMap.FloatReg(argSlot1); ok {
		a1 = freg
	} else {
		ec.asm.FLDRd(D0, regRegs, argSlot1*ValueSize)
	}

	var a2 FReg = D1
	if freg, ok := ec.regMap.FloatReg(argSlot2); ok {
		a2 = freg
	} else {
		ec.asm.FLDRd(D1, regRegs, argSlot2*ValueSize)
	}

	dstFReg := ec.getFloatDst(ref, inst, D2)
	op(ec.asm, dstFReg, a1, a2)
	ec.spillFloat(ref, inst, dstFReg)
}

// emitIntBinaryIntrinsic: R(A) = op(R(A+1), R(A+2))
func (ec *emitCtx) emitIntBinaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, Reg, Reg, Reg)) {
	argSlot1 := int(inst.Slot) + 1
	argSlot2 := int(inst.Slot) + 2

	// Load arg1
	var a1 Reg = X0
	if reg, ok := ec.regMap.IntReg(argSlot1); ok {
		a1 = reg
	} else {
		ec.asm.LDR(X0, regRegs, argSlot1*ValueSize)
		EmitUnboxInt(ec.asm, X0, X0)
	}

	// Load arg2
	var a2 Reg = X1
	if reg, ok := ec.regMap.IntReg(argSlot2); ok {
		a2 = reg
	} else {
		ec.asm.LDR(X1, regRegs, argSlot2*ValueSize)
		EmitUnboxInt(ec.asm, X1, X1)
	}

	dst := ec.getIntDst(ref, inst, X2)
	op(ec.asm, dst, a1, a2)
	ec.spillInt(ref, inst, dst)
}

// emitIntUnaryIntrinsic: R(A) = op(R(A+1))
func (ec *emitCtx) emitIntUnaryIntrinsic(ref SSARef, inst *SSAInst, op func(*Assembler, Reg, Reg)) {
	argSlot := int(inst.Slot) + 1

	var a1 Reg = X0
	if reg, ok := ec.regMap.IntReg(argSlot); ok {
		a1 = reg
	} else {
		ec.asm.LDR(X0, regRegs, argSlot*ValueSize)
		EmitUnboxInt(ec.asm, X0, X0)
	}

	dst := ec.getIntDst(ref, inst, X1)
	op(ec.asm, dst, a1)
	ec.spillInt(ref, inst, dst)
}

// ────────────────────────────────────────────────────────────────────────────
// Store-back: write all register values to memory before loop back-edge
// ────────────────────────────────────────────────────────────────────────────

// emitStoreBack writes all allocated register values back to memory.
// If typeSafe is true, only writes to slots whose memory value has a matching type.
// This prevents call-exit results (e.g., tables, booleans) from being overwritten
// by stale register values of a different type.
func (ec *emitCtx) emitStoreBack() {
	ec.emitStoreBackImpl(false)
}

func (ec *emitCtx) emitStoreBackTypeSafe() {
	ec.emitStoreBackImpl(true)
}

func (ec *emitCtx) emitStoreBackImpl(typeSafe bool) {
	asm := ec.asm
	_ = typeSafe // used for call-exit slot skipping

	// Store all allocated integer registers back to memory (NaN-boxed).
	// Skip call-exit output slots (interpreter's value is authoritative).
	// Skip float-written slots: a float operation was the last writer, so the
	// int GPR holds a stale value. The correct float value is either in an FPR
	// (handled by float store-back below) or already in memory.
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			if ec.callExitWriteSlots[slot] {
				continue
			}
			if ec.floatWrittenSlots[slot] {
				continue
			}
			EmitBoxIntFast(asm, X0, reg, regTagInt)
			asm.STR(X0, regRegs, slot*ValueSize)
		}
	}

	// Store float registers back to memory.
	// We must store all float values that were written in the loop body.
	// Use floatSlotReg to get the FPR that has the current value for each slot.
	// Then ensure the value is also in the slot-level FPR (if allocated) for consistency with reload.
	for slot, currentFPR := range ec.floatSlotReg {
		if ec.callExitWriteSlots[slot] {
			continue
		}
		// If there's a slot-level FPR allocation and it differs from currentFPR,
		// move the value to the slot-level FPR first
		if ec.regMap.Float != nil {
			if slotFPR, ok := ec.regMap.Float.getReg(slot); ok && slotFPR != currentFPR {
				asm.FMOVd(slotFPR, currentFPR)
				currentFPR = slotFPR // now store from slot-level FPR
			}
		}
		asm.FSTRd(currentFPR, regRegs, slot*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Reload all registers from memory (after call-exit resume)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitReloadAll() {
	asm := ec.asm
	seq := ec.reloadSeq
	ec.reloadSeq++

	// Reload integer registers with type-safe unboxing.
	// After a call-exit, slots may contain values of unexpected types (e.g., a bool
	// in a slot that the register allocator assigned an int GPR). We must verify the
	// NaN-box tag is actually int (0xFFFE) before unboxing; otherwise, skip the reload
	// and leave the register as-is (the code path will read from memory if needed).
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			skipLabel := "reload_skip_int_" + itoa(seq) + "_" + itoa(slot)
			asm.LDR(reg, regRegs, slot*ValueSize)
			// Check if this is actually an integer (top 16 bits == 0xFFFE)
			asm.LSRimm(X0, reg, 48)
			asm.MOVimm16(X1, NB_TagIntShr48)
			asm.CMPreg(X0, X1)
			asm.BCond(CondNE, skipLabel) // not int → skip unbox, register holds raw NaN-boxed value
			EmitUnboxInt(asm, reg, reg)
			asm.Label(skipLabel)
		}
	}

	// Reload ALL float registers from their slot's memory.
	// We must reload every FPR that the loop body might read, including both
	// slot-level and ref-level allocations. A given slot may have both a
	// slot-level FPR and one or more ref-level FPRs (possibly different registers).
	// All must be loaded from the same memory slot.
	reloadedFPR := make(map[FReg]bool)
	if ec.regMap.Float != nil {
		for slot, freg := range ec.regMap.Float.slotToReg {
			asm.FLDRd(freg, regRegs, slot*ValueSize)
			reloadedFPR[freg] = true
		}
	}
	if ec.regMap.FloatRef != nil {
		for ref, freg := range ec.regMap.FloatRef.refToReg {
			if reloadedFPR[freg] {
				continue
			}
			if int(ref) >= len(ec.f.Insts) {
				continue
			}
			inst := &ec.f.Insts[ref]
			slot := int(inst.Slot)
			if slot < 0 {
				continue
			}
			asm.FLDRd(freg, regRegs, slot*ValueSize)
			reloadedFPR[freg] = true
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Inner loop store-back and reload
// ────────────────────────────────────────────────────────────────────────────

// emitInnerLoopStoreBack stores the latest float register values back to memory.
// Called before the inner loop backward branch so that the next iteration
// reads updated values from memory.
func (ec *emitCtx) emitInnerLoopStoreBack() {
	// Reuse the same store-back logic (latest ref per slot)
	ec.emitStoreBack()
}

// emitInnerLoopReload reloads all float registers from memory.
// Called at the inner_loop_body label start so that stale ref-based register
// values are overwritten with the correct values from the previous iteration.
func (ec *emitCtx) emitInnerLoopReload() {
	ec.emitReloadAll()
}

// ────────────────────────────────────────────────────────────────────────────
// Cold paths
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitSideExit() {
	asm := ec.asm

	asm.Label("side_exit_setup")
	// Store back all register values to memory before exiting.
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// X9 was set by the guard instruction that branched here.
	// It holds the correct ExitPC (the bytecode PC of the failing guard).
	// DO NOT overwrite X9 — just store it.
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Save ExitState: GPR registers
	if ec.regMap.Int != nil {
		off := TraceCtxOffExitGPR
		for i, gpr := range allocableGPR {
			if i >= 4 {
				break // ExitGPR only has 4 slots
			}
			asm.STR(gpr, regCtx, off+i*8)
		}
	}

	// Save ExitState: FPR registers
	asm.FSTP(D4, D5, regCtx, TraceCtxOffExitFPR)
	asm.FSTP(D6, D7, regCtx, TraceCtxOffExitFPR+16)
	asm.FSTP(D8, D9, regCtx, TraceCtxOffExitFPR+32)
	asm.FSTP(D10, D11, regCtx, TraceCtxOffExitFPR+48)

	// Set ExitCode = 1 (side exit)
	asm.LoadImm64(X0, 1)
	asm.B("epilogue")
}

// emitBreakExit emits the break-exit path for inner loop break guards.
// Like side_exit_setup but exits AFTER the FORLOOP (loopPC + 1) so the VM
// skips past the inner loop, simulating a break statement.
func (ec *emitCtx) emitBreakExit() {
	asm := ec.asm
	asm.Label("break_exit")

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Set ExitPC to the break guard's PC so the VM re-executes the comparison.
	// The VM will evaluate the LT/LE, take the "escape" branch, execute
	// any break body (e.g., escaped=true), and then break out of the loop.
	asm.LoadImm64(X9, int64(ec.breakGuardPC))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Save ExitState
	if ec.regMap.Int != nil {
		off := TraceCtxOffExitGPR
		for i, gpr := range allocableGPR {
			if i >= 4 {
				break
			}
			asm.STR(gpr, regCtx, off+i*8)
		}
	}
	asm.FSTP(D4, D5, regCtx, TraceCtxOffExitFPR)
	asm.FSTP(D6, D7, regCtx, TraceCtxOffExitFPR+16)
	asm.FSTP(D8, D9, regCtx, TraceCtxOffExitFPR+32)
	asm.FSTP(D10, D11, regCtx, TraceCtxOffExitFPR+48)

	// Set ExitCode = 4 (break exit) so VM resumes at ExitPC.
	// Break exits are expected behavior (e.g., mandelbrot escape check)
	// and should NOT count toward side-exit blacklisting.
	asm.LoadImm64(X0, 4)
	asm.B("epilogue")
}

func (ec *emitCtx) emitLoopDone() {
	asm := ec.asm
	asm.Label("loop_done")

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Set ExitPC to the FORLOOP PC + 1 (instruction after the loop)
	loopPC := 0
	if ec.f.Trace != nil {
		loopPC = ec.f.Trace.LoopPC
	}
	asm.LoadImm64(X9, int64(loopPC+1))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Set ExitCode = 0 (loop done)
	asm.LoadImm64(X0, 0)
	asm.B("epilogue")
}

func (ec *emitCtx) emitGuardFail() {
	asm := ec.asm
	asm.Label("guard_fail")

	// Set ExitCode = 2 (guard fail — pre-loop type mismatch)
	asm.LoadImm64(X0, 2)
	asm.B("epilogue")
}

func (ec *emitCtx) emitGuardFailCommon(bailoutID int) {
	asm := ec.asm
	// Common guard fail handler - store bailout ID in ExitPC for deopt handler
	asm.Label("guard_fail_common")

	// Set ExitCode = 2 (guard fail — pre-loop type mismatch)
	asm.LoadImm64(X0, 2)

	// Store bailout ID in ExitPC (reused field for bailout info)
	// The deopt handler can look up bailout details from DeoptMetadata
	asm.LoadImm64(X9, int64(bailoutID))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	asm.B("epilogue")
}

func (ec *emitCtx) emitMaxIterExit() {
	asm := ec.asm
	asm.Label("max_iter_exit")

	// Store back all register values to memory
	if ec.hasCallExit {
		ec.emitStoreBackTypeSafe()
	} else {
		ec.emitStoreBack()
	}

	// Set ExitCode = 5 (max iterations reached for debugging)
	asm.LoadImm64(X0, 5)
	asm.B("epilogue")
}
