//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// emitTableSlotGuards emits pre-loop guards for loop-invariant table slots.
// These verify: is-table, no-metatable, and cache the extracted table pointer
// and array base pointer for use in the loop body.
func emitTableSlotGuards(asm *Assembler, hoisted map[int]int) {
	for slot, ssaType := range hoisted {
		asm.LDR(X0, regRegs, slot*ValueSize)
		EmitCheckIsTableFull(asm, X0, X1, X3, "guard_fail")
		EmitExtractPtr(asm, X0, X0)
		asm.CBZ(X0, "guard_fail")
		// Check metatable == nil
		asm.LDR(X1, X0, TableOffMetatable)
		asm.CBNZ(X1, "guard_fail")
		// Check array kind matches expected type
		asm.LDRB(X1, X0, TableOffArrayKind)
		switch ssaType {
		case int(SSATypeInt):
			// Accept int or bool or mixed
		case int(SSATypeFloat):
			asm.CMPimmW(X1, AKFloat)
			asm.BCond(CondNE, "guard_fail")
		}
		_ = slot
	}
}

// emitSSALoadGlobal emits code for SSA_LOAD_GLOBAL: loads a full Value from the constant pool.
func emitSSALoadGlobal(asm *Assembler, inst *SSAInst) {
	constIdx := int(inst.AuxInt)
	dstSlot := int(inst.Slot)
	if dstSlot >= 0 && constIdx >= 0 {
		constOff := constIdx * ValueSize
		dstOff := dstSlot * ValueSize
		// Copy ValueSize bytes (ValueSize/8 words) from constants to registers
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X0, regConsts, constOff+w*8)
			asm.STR(X0, regRegs, dstOff+w*8)
		}
	}
}

// emitSSALoadField emits code for SSA_LOAD_FIELD: R(A) = table.field at known skeys index.
func emitSSALoadField(asm *Assembler, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	fieldIdx := int(int32(inst.AuxInt)) // low 32 bits, sign-extended
	tableSlot := sm.getSlotForRef(inst.Arg1)
	dstSlot := int(inst.Slot)
	// Unpack AuxInt: low 32 bits = fieldIdx, high 32 bits = shapeID
	shapeID := uint32(inst.AuxInt >> 32)
	asm.LoadImm64(X9, int64(inst.PC)) // side-exit PC

	if fieldIdx < 0 || tableSlot < 0 {
		// Unknown field index → side-exit (can't compile)
		asm.B("side_exit")
		return
	}

	// In-loop table type guard: slot may have been reused by arithmetic
	// in a previous iteration (slot reuse across iterations).
	asm.LDR(X0, regRegs, tableSlot*ValueSize)
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit")

	// Extract *Table pointer
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit") // nil table

	// Guard: no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit")

	// Guard: shapeID matches (replaces skeys length check — stronger and faster)
	if shapeID != 0 {
		asm.LDRW(X1, X0, TableOffShapeID) // load uint32 shapeID
		asm.LoadImm64(X2, int64(shapeID))
		asm.CMPreg(X1, X2)
		asm.BCond(CondNE, "side_exit")
	} else {
		// Fallback: skeys length check (no shape info from recording)
		asm.LDR(X1, X0, TableOffSkeysLen)
		asm.CMPimm(X1, uint16(fieldIdx+1))
		asm.BCond(CondLT, "side_exit")
	}

	// Load svals[fieldIdx]: svals base + fieldIdx * ValueSize
	asm.LDR(X1, X0, TableOffSvals) // X1 = svals base pointer
	svalsOff := fieldIdx * ValueSize
	// Copy entire Value from svals[fieldIdx] to R(A)
	if dstSlot >= 0 {
		off := dstSlot * ValueSize
		if inst.Type == SSATypeFloat {
			// Float result: NaN-boxed float bits = raw float64 bits.
			// Load into X register, then move to D register if allocated.
			asm.LDR(X2, X1, svalsOff)
			if fr, ok := regMap.FloatReg(dstSlot); ok {
				asm.FMOVtoFP(fr, X2)
			}
			// Write-through: always store to memory
			if off <= 32760 {
				asm.STR(X2, regRegs, off)
			}
		} else if inst.Type == SSATypeInt {
			// Int result: load NaN-boxed value, unbox, store to int register or memory.
			asm.LDR(X2, X1, svalsOff)
			EmitUnboxInt(asm, X2, X2)
			if r, ok := regMap.IntReg(dstSlot); ok {
				asm.MOVreg(r, X2)
			}
			// Write-through: always store to memory
			if off <= 32760 {
				EmitBoxIntFast(asm, X5, X2, regTagInt)
				asm.STR(X5, regRegs, off)
			}
		} else {
			// Unknown type: raw copy
			for w := 0; w < ValueSize/8; w++ {
				asm.LDR(X2, X1, svalsOff+w*8)
				asm.STR(X2, regRegs, dstSlot*ValueSize+w*8)
			}
		}
	}
}

// emitSSAStoreField emits code for SSA_STORE_FIELD: table.field = value at known skeys index.
func emitSSAStoreField(asm *Assembler, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	fieldIdx := int(int32(inst.AuxInt)) // low 32 bits, sign-extended
	shapeID := uint32(inst.AuxInt >> 32) // high 32 bits
	tableSlot := sm.getSlotForRef(inst.Arg1)
	valSlot := sm.getSlotForRef(inst.Arg2)
	asm.LoadImm64(X9, int64(inst.PC))

	if fieldIdx < 0 || tableSlot < 0 {
		asm.B("side_exit")
		return
	}

	// In-loop table type guard
	asm.LDR(X0, regRegs, tableSlot*ValueSize)
	EmitCheckIsTableFull(asm, X0, X1, X2, "side_exit")

	// Extract *Table pointer
	EmitExtractPtr(asm, X0, X0)
	asm.CBZ(X0, "side_exit")

	// Guard: no metatable
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, "side_exit")

	// Guard: shapeID matches
	if shapeID != 0 {
		asm.LDRW(X1, X0, TableOffShapeID)
		asm.LoadImm64(X2, int64(shapeID))
		asm.CMPreg(X1, X2)
		asm.BCond(CondNE, "side_exit")
	} else {
		asm.LDR(X1, X0, TableOffSkeysLen)
		asm.CMPimm(X1, uint16(fieldIdx+1))
		asm.BCond(CondLT, "side_exit")
	}

	// Store value to svals[fieldIdx]
	asm.LDR(X1, X0, TableOffSvals)
	svalsOff := fieldIdx * ValueSize
	if valSlot >= 0 {
		for w := 0; w < ValueSize/8; w++ {
			asm.LDR(X2, regRegs, valSlot*ValueSize+w*8)
			asm.STR(X2, X1, svalsOff+w*8)
		}
	}
}

// === Side-exit continuation for inner loop escape ===

// sideExitContinuation holds analysis results for the inner loop escape optimization.
// When a float guard inside the inner loop fails (e.g., zr²+zi² > 4.0 in mandelbrot),
// instead of side-exiting to the interpreter, we skip the post-inner-loop epilogue
// (GUARD_TRUTHY + count++) and jump directly to the outer FORLOOP increment.
//
// Additionally, when GUARD_TRUTHY fails (non-escaping pixel), instead of side-exiting,
// we execute count++ inline and continue the outer FORLOOP.
type sideExitContinuation struct {
	innerLoopStartIdx   int // index of SSA_INNER_LOOP
	innerLoopEndIdx     int // index of SSA_LE_INT(AuxInt=1)
	innerLoopSlot       int // VM slot of inner loop index (for spilling)
	outerForLoopAddIdx  int // index of the outer FORLOOP's ADD_INT (skip_count target)

	// GUARD_TRUTHY continuation: when escaped=false, execute count++ inline
	guardTruthyIdx int // index of GUARD_TRUTHY in SSA (for redirecting)
	countSlot      int // VM slot of count variable (-1 if unknown)
	countStepSlot  int // VM slot or constant for count increment (-1 if unknown)
	countIsRK      bool // true if countStepSlot is RK (constant)
}

// analyzeSideExitContinuation scans the SSA to detect the inner loop structure
// for the side-exit continuation optimization. Returns nil if no inner loop is found
// or the pattern doesn't match.
func analyzeSideExitContinuation(f *SSAFunc, loopIdx int) *sideExitContinuation {
	info := &sideExitContinuation{
		innerLoopStartIdx:  -1,
		innerLoopEndIdx:    -1,
		innerLoopSlot:      -1,
		outerForLoopAddIdx: -1,
		guardTruthyIdx:     -1,
		countSlot:          -1,
		countStepSlot:      -1,
	}

	// Find SSA_INNER_LOOP and SSA_LE_INT(AuxInt=1) after the main LOOP
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_INNER_LOOP {
			info.innerLoopStartIdx = i
		}
		if inst.Op == SSA_LE_INT && inst.AuxInt == 1 {
			info.innerLoopEndIdx = i
		}
	}

	if info.innerLoopStartIdx < 0 || info.innerLoopEndIdx < 0 {
		return nil // no inner loop
	}

	// Check that there are float guards inside the inner loop
	hasFloatGuard := false
	for i := info.innerLoopStartIdx; i < info.innerLoopEndIdx; i++ {
		if isFloatGuard(f.Insts[i].Op) {
			hasFloatGuard = true
			break
		}
	}
	if !hasFloatGuard {
		return nil // no float guards to optimize
	}

	// Find the inner loop's slot from LE_INT(AuxInt=1)'s Arg1
	leInst := &f.Insts[info.innerLoopEndIdx]
	arg1Ref := leInst.Arg1
	if int(arg1Ref) < len(f.Insts) {
		argInst := &f.Insts[arg1Ref]
		if argInst.Slot >= 0 {
			info.innerLoopSlot = int(argInst.Slot)
		}
	}

	// Find GUARD_TRUTHY between inner_loop_done and the outer FORLOOP
	for i := info.innerLoopEndIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_GUARD_TRUTHY {
			info.guardTruthyIdx = i
			break
		}
		// Stop scanning if we hit the outer exit check
		if (inst.Op == SSA_LE_INT && inst.AuxInt == 0) || inst.Op == SSA_LT_INT {
			break
		}
	}

	// Find the outer FORLOOP's ADD_INT: it's the Arg1 of LE_INT(AuxInt=0)
	for i := info.innerLoopEndIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Op == SSA_LE_INT && inst.AuxInt == 0 {
			// The outer FORLOOP exit check. Its Arg1 is the ADD_INT (idx += step).
			addRef := inst.Arg1
			if int(addRef) >= 0 && int(addRef) < len(f.Insts) {
				info.outerForLoopAddIdx = int(addRef)
			}
			break
		}
		if inst.Op == SSA_LT_INT {
			// While-loop style outer exit check
			addRef := inst.Arg1
			if int(addRef) >= 0 && int(addRef) < len(f.Insts) {
				info.outerForLoopAddIdx = int(addRef)
			}
			break
		}
	}

	if info.outerForLoopAddIdx < 0 {
		return nil // can't find outer FORLOOP increment
	}

	// Analyze count++ from bytecodes: look at the bytecodes between
	// the GUARD_TRUTHY's TEST PC and the outer FORLOOP PC.
	// Pattern: LOADINT Rtemp 1 → ADD Rtemp Rcount Rtemp → MOVE Rcount Rtemp
	// The real count slot is the source B of ADD (= destination A of MOVE).
	if info.guardTruthyIdx >= 0 && f.Trace != nil && f.Trace.LoopProto != nil {
		proto := f.Trace.LoopProto
		guardInst := &f.Insts[info.guardTruthyIdx]
		testPC := guardInst.PC // PC of the TEST instruction

		// The JMP after TEST tells us where count++ is.
		// TEST at testPC, JMP at testPC+1.
		if testPC+1 < len(proto.Code) {
			jmpInst := proto.Code[testPC+1]
			jmpOp := vm.DecodeOp(jmpInst)
			if jmpOp == vm.OP_JMP {
				jmpSBX := vm.DecodesBx(jmpInst)
				jmpTarget := testPC + 1 + jmpSBX + 1
				// Scan the skipped instructions for the ADD+MOVE pattern
				for pc := testPC + 2; pc < jmpTarget && pc < len(proto.Code); pc++ {
					inst := proto.Code[pc]
					op := vm.DecodeOp(inst)
					if op == vm.OP_ADD {
						addB := vm.DecodeB(inst) // source: count slot
						// Look for a MOVE after the ADD that copies result to the count slot
						if pc+1 < jmpTarget && pc+1 < len(proto.Code) {
							moveInst := proto.Code[pc+1]
							moveOp := vm.DecodeOp(moveInst)
							if moveOp == vm.OP_MOVE {
								moveA := vm.DecodeA(moveInst) // destination
								if moveA == addB {
									// Confirmed: count is at addB, and the pattern is
									// ADD Rtemp Rcount Rstep → MOVE Rcount Rtemp
									info.countSlot = addB
								}
							}
						}
						if info.countSlot < 0 {
							// No MOVE after ADD → direct count++: ADD Rcount Rcount Rstep
							info.countSlot = vm.DecodeA(inst)
						}
						break
					}
				}
			}
		}
	}

	return info
}

// isFloatGuard returns true if the SSA op is a float comparison guard.
func isFloatGuard(op SSAOp) bool {
	switch op {
	case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	}
	return false
}

// emitGuardTruthyWithContinuation emits a GUARD_TRUTHY that branches to the
// given target label instead of "side_exit" on failure. Used for the non-escaping
// pixel continuation: instead of side-exiting, jump to truthy_cont which does count++.
func emitGuardTruthyWithContinuation(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, target string) {
	slot := int(inst.Slot)
	asm.LoadImm64(X9, int64(inst.PC))
	// NaN-boxing: load full 8-byte value and compare against NaN-boxed constants
	asm.LDR(X0, regRegs, slot*ValueSize)
	if inst.AuxInt == 0 {
		// Expect truthy: branch to target if nil or false
		asm.LoadImm64(X1, nb_i64(NB_ValNil))
		asm.CMPreg(X0, X1)
		asm.BCond(CondEQ, target) // nil → falsy → continuation
		asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		asm.CMPreg(X0, X1)
		asm.BCond(CondEQ, target) // false → falsy → continuation
	} else {
		// Expect falsy: branch to target if truthy (not nil and not false)
		doneLabel := fmt.Sprintf("guard_falsy_cont_%d", ref)
		asm.LoadImm64(X1, nb_i64(NB_ValNil))
		asm.CMPreg(X0, X1)
		asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
		asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		asm.CMPreg(X0, X1)
		asm.BCond(CondNE, target) // not nil, not false → truthy → continuation
		asm.Label(doneLabel)
	}
}

// emitFloatGuardWithTarget emits a float comparison guard that branches to the
// given target label instead of "side_exit". Used for inner loop escape optimization.
func emitFloatGuardWithTarget(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder, target string) {
	asm.LoadImm64(X9, int64(inst.PC))
	arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
	arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D1)
	asm.FCMPd(arg1D, arg2D)
	switch inst.Op {
	case SSA_LT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGE, target)
		} else {
			asm.BCond(CondLT, target)
		}
	case SSA_LE_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGT, target)
		} else {
			asm.BCond(CondLE, target)
		}
	case SSA_GT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondLE, target)
		} else {
			asm.BCond(CondGT, target)
		}
	}
}
