//go:build darwin && arm64

package jit

import "fmt"

// emitSSAInstSlot emits ARM64 code for one SSA instruction using slot-based allocation.
func emitSSAInstSlot(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	switch inst.Op {
	case SSA_NOP:
		// skip

	case SSA_LOAD_SLOT:
		// No code emitted; UNBOX_INT will load the value.

	case SSA_LOAD_GLOBAL:
		emitSSALoadGlobal(asm, inst)

	case SSA_GUARD_TYPE:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		EmitGuardType(asm, regRegs, slot, int(inst.AuxInt), "side_exit")

	case SSA_GUARD_TRUTHY:
		// Guard truthiness of a NaN-boxed value. AuxInt: 0=expect truthy, 1=expect falsy.
		// Truthy: anything except nil (0xFFFC...) and false (0xFFFD...|0).
		slot := int(inst.Slot)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.LDR(X0, regRegs, slot*ValueSize) // load NaN-boxed value
		if inst.AuxInt == 0 {
			// Expect truthy: exit if nil or bool(false)
			asm.LoadImm64(X1, nb_i64(NB_ValNil))
			asm.CMPreg(X0, X1)
			asm.BCond(CondEQ, "side_exit") // nil → falsy → exit
			asm.LoadImm64(X1, nb_i64(NB_ValFalse))
			asm.CMPreg(X0, X1)
			asm.BCond(CondEQ, "side_exit") // false → falsy → exit
		} else {
			// Expect falsy: exit if truthy (not nil and not false)
			doneLabel := fmt.Sprintf("guard_falsy_%d", ref)
			asm.LoadImm64(X1, nb_i64(NB_ValNil))
			asm.CMPreg(X0, X1)
			asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
			asm.LoadImm64(X1, nb_i64(NB_ValFalse))
			asm.CMPreg(X0, X1)
			asm.BCond(CondNE, "side_exit") // not nil, not false → truthy → exit
			asm.Label(doneLabel)
		}

	case SSA_LOAD_ARRAY:
		emitSSALoadArray(asm, f, ref, inst, regMap, sm)

	case SSA_STORE_ARRAY:
		emitSSAStoreArray(asm, f, ref, inst, regMap, sm)

	case SSA_LOAD_FIELD:
		emitSSALoadField(asm, inst, regMap, sm)

	case SSA_STORE_FIELD:
		emitSSAStoreField(asm, inst, regMap, sm)

	case SSA_UNBOX_INT:
		loadInst := &f.Insts[inst.Arg1]
		slot := int(loadInst.Slot)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LDR(dstReg, regRegs, slot*ValueSize)
		EmitUnboxInt(asm, dstReg, dstReg)

	case SSA_CONST_INT:
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LoadImm64(dstReg, inst.AuxInt)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_ADD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.ADDreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)
		// If this slot has a FORLOOP A+3 alias, copy to that register too
		if a3Slot, ok := sm.forloopA3[slot]; ok {
			if a3Reg, ok := regMap.IntReg(a3Slot); ok && a3Reg != dstReg {
				asm.MOVreg(a3Reg, dstReg)
			}
		}

	case SSA_SUB_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.SUBreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_MUL_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.MUL(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_MOD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X1)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CBZ(arg2Reg, "side_exit")
		asm.SDIV(X3, arg1Reg, arg2Reg)
		asm.MSUB(dstReg, X3, arg2Reg, arg1Reg)
		// Lua-style modulo: result has same sign as divisor
		doneLabel := fmt.Sprintf("mod_done_%d", ref)
		asm.CBZ(dstReg, doneLabel)
		asm.EORreg(X3, dstReg, arg2Reg)
		asm.CMPreg(X3, XZR)
		asm.BCond(CondGE, doneLabel)
		asm.ADDreg(dstReg, dstReg, arg2Reg)
		asm.Label(doneLabel)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_NEG_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.NEG(dstReg, arg1Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_EQ_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondNE, "side_exit")

	case SSA_LT_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondGE, "side_exit")

	case SSA_LE_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CMPreg(arg1Reg, arg2Reg)
		asm.BCond(CondGT, "side_exit")

	// --- Float operations (using SIMD registers D0-D7) ---

	case SSA_UNBOX_FLOAT:
		// Float values loaded at loop entry or on demand

	case SSA_ADD_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FADDd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_SUB_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FSUBd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_MUL_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FMULd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_DIV_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FDIVd(dstD, arg1D, arg2D)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_FMADD:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		addendD := resolveFloatRef(asm, f, SSARef(inst.AuxInt), regMap, sm, D3)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FMADDd(dstD, arg1D, arg2D, addendD)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_FMSUB:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)
		addendD := resolveFloatRef(asm, f, SSARef(inst.AuxInt), regMap, sm, D3)
		dstD := getFloatSlotReg(regMap, slot, D0)
		asm.FMSUBd(dstD, arg1D, arg2D, addendD)
		storeFloatResult(asm, regMap, slot, dstD)

	case SSA_CONST_FLOAT:
		slot := sm.getSlotForRef(ref)
		if slot >= 0 {
			if dreg, ok := regMap.FloatReg(slot); ok {
				// Slot is allocated — load directly into D register, skip memory
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
			} else {
				// Not allocated — write float bits to memory.
				// With NaN-boxing, raw float64 bits ARE the NaN-boxed value.
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(D0, X0)
				asm.FSTRd(D0, regRegs, slot*ValueSize)
			}
		}

	case SSA_CONST_BOOL:
		slot := sm.getSlotForRef(ref)
		if slot >= 0 {
			// NaN-boxing: box the bool value (0=false, 1=true)
			asm.LoadImm64(X0, inst.AuxInt)
			EmitBoxBool(asm, X5, X0, X6)
			asm.STR(X5, regRegs, slot*ValueSize)
		}

	case SSA_LT_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
		asm.FCMPd(arg1D, arg2D)
		if inst.AuxInt == 0 {
			asm.BCond(CondGE, "side_exit")
		} else {
			asm.BCond(CondLT, "side_exit")
		}

	case SSA_LE_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
		asm.FCMPd(arg1D, arg2D)
		if inst.AuxInt == 0 {
			asm.BCond(CondGT, "side_exit")
		} else {
			asm.BCond(CondLE, "side_exit")
		}

	case SSA_GT_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
		asm.FCMPd(arg1D, arg2D)
		if inst.AuxInt == 0 {
			asm.BCond(CondLE, "side_exit")
		} else {
			asm.BCond(CondGT, "side_exit")
		}

	case SSA_MOVE:
		slot := sm.getSlotForRef(ref)
		if inst.Type == SSATypeFloat {
			// Float move: use D register allocation
			srcD := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
			dstD := getFloatSlotReg(regMap, slot, D1)
			if dstD != srcD {
				asm.FMOVd(dstD, srcD)
			}
			// If destination is not allocated, write to memory.
			// With NaN-boxing, raw float64 bits ARE the NaN-boxed value.
			if _, ok := regMap.FloatReg(slot); !ok && slot >= 0 {
				asm.FSTRd(srcD, regRegs, slot*ValueSize)
			}
		} else if inst.Type == SSATypeInt || inst.Type == SSATypeBool {
			// Integer/Bool move: copy data field, write TypeInt tag
			srcReg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			dstReg := getSlotReg(regMap, sm, ref, slot, X0)
			if dstReg != srcReg {
				asm.MOVreg(dstReg, srcReg)
			}
			spillIfNotAllocated(asm, regMap, slot, dstReg)
		} else {
			// Unknown type (table, function, string, etc.): full 24-byte copy.
			// Must copy typ + data + ptr to preserve the type tag and pointer.
			// Copying only the data field and writing TypeInt (as spillIfNotAllocated
			// does) would corrupt table references, causing "attempt to index a
			// number value" errors when the interpreter resumes after side-exit.
			srcSlot := sm.getSlotForRef(inst.Arg1)
			if srcSlot >= 0 && slot >= 0 {
				for w := 0; w < ValueSize/8; w++ {
					asm.LDR(X0, regRegs, srcSlot*ValueSize+w*8)
					asm.STR(X0, regRegs, slot*ValueSize+w*8)
				}
			}
		}

	case SSA_INTRINSIC:
		// Inline GoFunction calls
		dstSlot := int(inst.Slot)
		switch int(inst.AuxInt) {
		case IntrinsicSqrt:
			// math.sqrt(x): load float arg, FSQRT, store result
			// Arg1 is the input value ref (R(A+1) in original CALL)
			srcD := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
			asm.FSQRTd(D1, srcD)
			// Store result to destination slot
			if dstSlot >= 0 {
				if dstDreg, ok := regMap.FloatReg(dstSlot); ok {
					asm.FMOVd(dstDreg, D1)
				} else {
					// With NaN-boxing, raw float64 bits ARE the NaN-boxed value.
					asm.FSTRd(D1, regRegs, dstSlot*ValueSize)
				}
			}
		case IntrinsicBxor:
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
			asm.EORreg(X2, arg1Reg, arg2Reg)
			if dstSlot >= 0 {
				EmitBoxInt(asm, X5, X2, X6)
				asm.STR(X5, regRegs, dstSlot*ValueSize)
			}
		case IntrinsicBand:
			arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
			arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
			asm.ANDreg(X2, arg1Reg, arg2Reg)
			if dstSlot >= 0 {
				EmitBoxInt(asm, X5, X2, X6)
				asm.STR(X5, regRegs, dstSlot*ValueSize)
			}
		default:
			// Unknown intrinsic → side-exit
			asm.LoadImm64(X9, int64(inst.PC))
			asm.B("side_exit")
		}

	case SSA_CALL_INNER_TRACE:
		emitSSACallInnerTrace(asm, f, inst, regMap)

	case SSA_SIDE_EXIT:
		asm.LoadImm64(X9, int64(inst.PC))
		asm.B("side_exit")

	case SSA_LOOP:
		// Handled in CompileSSA

	case SSA_INNER_LOOP:
		// Handled in CompileSSA loop body emission (emits label)
	}
}

// emitSSACallInnerTrace emits code for SSA_CALL_INNER_TRACE.
// Sub-trace calling: spill all allocated registers, call the inner trace's
// compiled code, check exit code, reload registers.
//
// The inner trace uses the same TraceContext (X19) and the same register
// array (regRegs/X26). It has its own prologue/epilogue that saves and
// restores callee-saved registers.
func emitSSACallInnerTrace(asm *Assembler, f *SSAFunc, inst *SSAInst, regMap *RegMap) {
	// Step 1: Store all allocated int/float registers back to memory.
	// The inner trace reads/writes directly to the VM register array.
	for slot, armReg := range regMap.Int.slotToReg {
		off := slot * ValueSize
		if off <= 32760 {
			EmitBoxInt(asm, X5, armReg, X6)
			asm.STR(X5, regRegs, off)
		}
	}
	// Spill float D registers: use ref-level map for precise spilling,
	// then slot-level as fallback.
	// With NaN-boxing, raw float64 bits ARE the NaN-boxed value.
	spilledFloatSlots := make(map[int]bool)
	if regMap.FloatRef != nil {
		for fref, dreg := range regMap.FloatRef.refToReg {
			if int(fref) >= len(f.Insts) {
				continue
			}
			finst := &f.Insts[fref]
			slot := int(finst.Slot)
			if slot >= 0 && !spilledFloatSlots[slot] {
				off := slot * ValueSize
				if off <= 32760 {
					asm.FSTRd(dreg, regRegs, off)
					spilledFloatSlots[slot] = true
				}
			}
		}
	}
	for slot, dreg := range regMap.Float.slotToReg {
		if spilledFloatSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.FSTRd(dreg, regRegs, off)
		}
	}

	// Step 2: Swap Constants pointer in TraceContext to inner trace's constants.
	// The inner trace's prologue reads ctx.Constants, so we must set it
	// to the inner trace's constant pool before calling.
	// Save outer constants and set inner constants:
	asm.LDR(X0, X19, TraceCtxOffInnerConstants) // X0 = inner constants ptr
	asm.LDR(X1, X19, TraceCtxOffConstants)       // X1 = outer constants ptr (save)
	asm.STR(X0, X19, TraceCtxOffConstants)        // ctx.Constants = inner constants

	// Step 3: Load inner trace code pointer and call.
	asm.LDR(X8, X19, TraceCtxOffInnerCode)  // X8 = inner code pointer
	// Save outer constants on stack (X1 is caller-saved, won't survive BLR)
	asm.STPpre(X29, X1, SP, -16)
	asm.MOVreg(X0, X19)                     // X0 = TraceContext pointer (argument)
	asm.BLR(X8)                              // call inner trace

	// Step 4: Restore outer constants pointer.
	asm.LDPpost(X29, X1, SP, 16)
	asm.STR(X1, X19, TraceCtxOffConstants) // ctx.Constants = outer constants

	// Step 5: Check exit code.
	// ExitCode=2 (guard fail) means inner trace guard failed → outer side-exit.
	// ExitCode=0 (loop done) or 1 (side exit from inner) → inner loop finished,
	// continue outer loop.
	asm.LDR(X0, X19, TraceCtxOffExitCode)
	asm.LoadImm64(X9, int64(inst.PC))
	asm.CMPimm(X0, 2)
	asm.BCond(CondEQ, "side_exit")

	// Step 6: Reload regConsts and regRegs from TraceContext.
	// The inner trace's epilogue restored callee-saved regs (X19, X26, X27),
	// but our regConsts needs to point to the outer trace's constants.
	asm.LDR(regConsts, X19, TraceCtxOffConstants)

	// Step 7: Reload all allocated registers from memory.
	// The inner trace may have modified any VM slot.
	// Reload NaN-boxed values and unbox.
	for slot, armReg := range regMap.Int.slotToReg {
		off := slot * ValueSize
		if off <= 32760 {
			asm.LDR(armReg, regRegs, off)
			EmitUnboxInt(asm, armReg, armReg)
		}
	}
	for slot, dreg := range regMap.Float.slotToReg {
		off := slot * ValueSize
		if off <= 32760 {
			asm.FLDRd(dreg, regRegs, off)
		}
	}
}

// === Float expression forwarding ===
// Eliminates memory roundtrips for temp float values that are immediately consumed.

// floatForwarder tracks which SSA refs can be kept in scratch D registers
// instead of spilling to memory. Uses D0 and D3 as forwarding registers
// (D1/D2 are reserved for arg loading in resolveFloatRefFwd).
type floatForwarder struct {
	eligible map[SSARef]bool
	live     map[SSARef]FReg
	nextReg  int // cycles between 0 (D0) and 1 (D3)
}

var fwdRegs = [2]FReg{D0, D3}

func newFloatForwarder(f *SSAFunc, regMap *RegMap, sm *ssaSlotMapper, loopIdx int) *floatForwarder {
	fwd := &floatForwarder{
		eligible: make(map[SSARef]bool),
		live:     make(map[SSARef]FReg),
	}

	// Count uses of each ref within the loop body
	useCount := make(map[SSARef]int)
	firstUse := make(map[SSARef]int) // index of first use
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Arg1 >= 0 {
			useCount[inst.Arg1]++
			if _, ok := firstUse[inst.Arg1]; !ok {
				firstUse[inst.Arg1] = i
			}
		}
		if inst.Arg2 >= 0 {
			useCount[inst.Arg2]++
			if _, ok := firstUse[inst.Arg2]; !ok {
				firstUse[inst.Arg2] = i
			}
		}
		// FMADD/FMSUB store a third operand ref in AuxInt
		if inst.Op == SSA_FMADD || inst.Op == SSA_FMSUB {
			auxRef := SSARef(inst.AuxInt)
			if auxRef >= 0 {
				useCount[auxRef]++
				if _, ok := firstUse[auxRef]; !ok {
					firstUse[auxRef] = i
				}
			}
		}
	}

	// Mark eligible: single-use float results to non-allocated temp slots
	// where the use is within 3 instructions (allows MUL→MUL→SUB pattern)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)
		switch inst.Op {
		case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT:
			// Skip refs that already have ref-level D register allocation
			if _, ok := regMap.FloatRefReg(ref); ok {
				continue
			}
			slot := sm.getSlotForRef(ref)
			if slot < 0 {
				continue
			}
			if _, ok := regMap.FloatReg(slot); ok {
				continue
			}
			if useCount[ref] == 1 {
				if use, ok := firstUse[ref]; ok && use-i <= 3 {
					fwd.eligible[ref] = true
				}
			}
		}
	}

	return fwd
}

func resolveFloatRefFwd(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder, scratch FReg) FReg {
	if dreg, ok := fwd.live[ref]; ok {
		delete(fwd.live, ref)
		return dreg
	}
	// Check ref-level allocation first (more precise than slot-level)
	if dreg, ok := regMap.FloatRefReg(ref); ok {
		return dreg
	}
	return resolveFloatRef(asm, f, ref, regMap, sm, scratch)
}

// emitSSAInstSlotFwd is the forwarding-aware version of emitSSAInstSlot.
func emitSSAInstSlotFwd(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper, fwd *floatForwarder) {
	switch inst.Op {
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT:
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D1)
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D2)

		// Choose destination register: ref-level D reg, slot-level D reg,
		// cycling scratch for forwarding, or plain scratch
		var dstD FReg
		if dreg, ok := regMap.FloatRefReg(ref); ok {
			dstD = dreg
		} else if _, ok := regMap.FloatReg(slot); ok {
			dstD = getFloatSlotReg(regMap, slot, D0)
		} else if fwd.eligible[ref] {
			dstD = fwdRegs[fwd.nextReg%2]
			fwd.nextReg++
		} else {
			dstD = D0
		}

		switch inst.Op {
		case SSA_ADD_FLOAT:
			asm.FADDd(dstD, arg1D, arg2D)
		case SSA_SUB_FLOAT:
			asm.FSUBd(dstD, arg1D, arg2D)
		case SSA_MUL_FLOAT:
			asm.FMULd(dstD, arg1D, arg2D)
		case SSA_DIV_FLOAT:
			asm.FDIVd(dstD, arg1D, arg2D)
		}

		if fwd.eligible[ref] {
			fwd.live[ref] = dstD
			return // skip memory write — value forwarded in scratch register
		}
		storeFloatResultRef(asm, regMap, ref, slot, dstD)

	case SSA_FMADD, SSA_FMSUB:
		// FMADD: Dd = Da + Dn * Dm (Arg1=Dn, Arg2=Dm, AuxInt=Da ref)
		// FMSUB: Dd = Da - Dn * Dm (Arg1=Dn, Arg2=Dm, AuxInt=Da ref)
		slot := sm.getSlotForRef(ref)
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D1) // Dn
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D2) // Dm
		addendRef := SSARef(inst.AuxInt)
		addendD := resolveFloatRefFwd(asm, f, addendRef, regMap, sm, fwd, D3) // Da

		var dstD FReg
		if dreg, ok := regMap.FloatRefReg(ref); ok {
			dstD = dreg
		} else if _, ok := regMap.FloatReg(slot); ok {
			dstD = getFloatSlotReg(regMap, slot, D0)
		} else {
			dstD = D0
		}

		switch inst.Op {
		case SSA_FMADD:
			asm.FMADDd(dstD, arg1D, arg2D, addendD)
		case SSA_FMSUB:
			asm.FMSUBd(dstD, arg1D, arg2D, addendD)
		}

		storeFloatResultRef(asm, regMap, ref, slot, dstD)

	case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		asm.LoadImm64(X9, int64(inst.PC))
		arg1D := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
		arg2D := resolveFloatRefFwd(asm, f, inst.Arg2, regMap, sm, fwd, D1)
		asm.FCMPd(arg1D, arg2D)
		switch inst.Op {
		case SSA_LT_FLOAT:
			if inst.AuxInt == 0 {
				asm.BCond(CondGE, "side_exit")
			} else {
				asm.BCond(CondLT, "side_exit")
			}
		case SSA_LE_FLOAT:
			if inst.AuxInt == 0 {
				asm.BCond(CondGT, "side_exit")
			} else {
				asm.BCond(CondLE, "side_exit")
			}
		case SSA_GT_FLOAT:
			if inst.AuxInt == 0 {
				asm.BCond(CondLE, "side_exit")
			} else {
				asm.BCond(CondGT, "side_exit")
			}
		}

	case SSA_MOVE:
		if inst.Type == SSATypeFloat {
			slot := sm.getSlotForRef(ref)
			srcD := resolveFloatRefFwd(asm, f, inst.Arg1, regMap, sm, fwd, D0)
			dstD := getFloatRefReg(regMap, ref, slot, D1)
			if dstD != srcD {
				asm.FMOVd(dstD, srcD)
			}
			// If neither ref-level nor slot-level allocated, write to memory.
			// Slots with ref-level allocation are handled by floatRefSpill store-back.
			if _, refOk := regMap.FloatRefReg(ref); !refOk {
				if _, slotOk := regMap.FloatReg(slot); !slotOk && slot >= 0 {
					asm.FSTRd(srcD, regRegs, slot*ValueSize)
				}
			}
		} else {
			emitSSAInstSlot(asm, f, ref, inst, regMap, sm)
		}

	case SSA_CONST_FLOAT:
		slot := sm.getSlotForRef(ref)
		if slot >= 0 {
			// Check ref-level allocation first
			if dreg, ok := regMap.FloatRefReg(ref); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
			} else if dreg, ok := regMap.FloatReg(slot); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(dreg, X0)
			} else {
				// With NaN-boxing, raw float64 bits ARE the NaN-boxed value.
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(D0, X0)
				asm.FSTRd(D0, regRegs, slot*ValueSize)
			}
		}

	default:
		emitSSAInstSlot(asm, f, ref, inst, regMap, sm)
	}
}
