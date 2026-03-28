//go:build darwin && arm64

package jit

import (
	"fmt"
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
			SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT, SSA_DIV_INT,
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
	// Self-call support (currently unused; reserved for trace-through-calls).
	selfCallSeq      int    // monotonically increasing counter for unique self-call labels
	selfCallExtraRef SSARef // SSARef whose result is in regSelfExtra (X28), -1 if none
	// maxDepth0Slot: highest slot used at depth=0. Store-back skips slots above this
	// to avoid overwriting caller registers with inlined callee temporaries.
	maxDepth0Slot int
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
		maxDepth0Slot:      f.MaxDepth0Slot,
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

// emitPrologue, emitEpilogue, emitPreLoopGuards, ssaTypeToGuardType, emitPreLoopLoads
// are in ssa_emit_prologue.go

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

		case SSA_DIV_INT:
			ec.emitIntArith(ref, inst, func(asm *Assembler, dst, a1, a2 Reg) {
				asm.SDIV(dst, a1, a2)
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
			SSA_SIDE_EXIT:
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

// resolveIntRef, resolveFloatRef, getIntDst, getFloatDst,
// emitUnboxInt, emitUnboxFloat, emitConstInt, emitConstFloat,
// emitMove, emitLoadSlot are in ssa_emit_resolve.go
//
// emitIntArith, emitModInt, emitNegInt, spillInt,
// emitFloatArith, emitNegFloat, emitFMADD, emitFMSUB,
// emitBoxIntAsFloat, spillFloat are in ssa_emit_arith.go

// emitCmpInt, emitCmpIntLE, emitCmpFloat, emitCmpFloatBreak,
// emitCmpFloatLE, emitGuardBranch, emitGuardTruthy are in ssa_emit_guard.go

// emitLoadField, emitLoadTableShape, emitCheckShapeId, emitStoreField,
// emitLoadArray, emitLoadArrayTable, emitStoreArray, emitLoadGlobal,
// emitTableLen are in ssa_emit_table.go


// emitCallExit, emitCallExitInst, emitIntrinsic,
// emitFloatUnaryIntrinsic, emitFloatBinaryIntrinsic,
// emitIntBinaryIntrinsic, emitIntUnaryIntrinsic are in ssa_emit_intrinsic.go


// emitStoreBack, emitStoreBackTypeSafe, emitStoreBackImpl,
// emitReloadAll, emitInnerLoopStoreBack, emitInnerLoopReload,
// emitSideExit, emitBreakExit, emitLoopDone, emitGuardFail,
// emitGuardFailCommon, emitMaxIterExit are in ssa_emit_exit.go
