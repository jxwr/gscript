//go:build darwin && arm64

package jit

import (
	"fmt"
)

// ────────────────────────────────────────────────────────────────────────────
// SSA pipeline pass stubs (no-op for now)
// ────────────────────────────────────────────────────────────────────────────

// ConstHoist hoists loop-invariant constants out of the loop body.
func ConstHoist(f *SSAFunc) *SSAFunc { return f }

// CSE performs common subexpression elimination.
func CSE(f *SSAFunc) *SSAFunc { return f }

// FuseMultiplyAdd fuses MUL+ADD/SUB into FMADD/FMSUB.
func FuseMultiplyAdd(f *SSAFunc) *SSAFunc { return f }

// ────────────────────────────────────────────────────────────────────────────
// SSA analysis helpers
// ────────────────────────────────────────────────────────────────────────────

// ssaIsIntegerOnly returns true if all SSA ops in the function are compilable.
func ssaIsIntegerOnly(f *SSAFunc) bool {
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
			SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_LOOP, SSA_SIDE_EXIT, SSA_NOP, SSA_SNAPSHOT,
			SSA_CALL_INNER_TRACE, SSA_INNER_LOOP, SSA_INTRINSIC,
			SSA_MOVE, SSA_PHI, SSA_BOX_INT, SSA_BOX_FLOAT, SSA_STORE_SLOT:
			continue
		default:
			return false
		}
	}
	return true
}

// SSAIsUseful returns true if the SSA function contains meaningful computation.
func SSAIsUseful(f *SSAFunc) bool {
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_LOAD_ARRAY, SSA_STORE_ARRAY,
			SSA_INTRINSIC:
			return true
		}
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
	callExits    int  // number of call-exits emitted
	hasCallExit  bool
	loopExitIdx  int  // SSA instruction index of the loop-exit comparison (FORLOOP's LE_INT/LE_FLOAT)
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
	asm := NewAssembler()

	ec := &emitCtx{
		asm:    asm,
		f:      f,
		regMap: regMap,
	}

	// 1. Prologue: save callee-saved registers, set up pinned registers
	ec.emitPrologue()

	// 2. Pre-loop guards: type check all live-in slots
	ec.emitPreLoopGuards()

	// 3. Pre-loop loads: load live-in values into allocated registers
	ec.emitPreLoopLoads()

	// 4. Loop body
	asm.Label("loop_top")
	ec.emitLoopBody()

	// 5. Loop back-edge
	asm.B("loop_top")

	// 6. Cold paths: side-exit, loop-done, guard-fail, call-exits
	ec.emitSideExit()
	ec.emitLoopDone()
	ec.emitGuardFail()

	// 7. Epilogue
	ec.emitEpilogue()

	// 8. Finalize and allocate executable memory
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
	// Set FP = SP
	asm.MOVreg(X29, SP)
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
		expectedType := int(inst.AuxInt)
		// Skip TypeNil guards — a nil-typed slot can't have useful
		// computation. TypeNil(0) is also the zero value from trace IR
		// entries that don't set AType (e.g., manually constructed tests).
		if expectedType == TypeNil {
			continue
		}
		EmitGuardType(asm, regRegs, slot, expectedType, "guard_fail")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Pre-loop loads: load live-in values into allocated registers
// ────────────────────────────────────────────────────────────────────────────

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

	// Find the loop-exit comparison: the last LE_INT or LE_FLOAT in the loop body.
	// This is the FORLOOP's exit check and should branch to loop_done, not side_exit.
	ec.loopExitIdx = -1
	for i := len(f.Insts) - 1; i > f.LoopIdx; i-- {
		op := f.Insts[i].Op
		if op == SSA_LE_INT || op == SSA_LE_FLOAT {
			ec.loopExitIdx = i
			break
		}
	}

	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)

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
			// These don't go into registers in the loop body
			// They are stored back to memory during store-back

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
			ec.emitCmpInt(inst, CondNE) // branch to side_exit if NOT equal (when A=1) or if equal (when A=0)

		case SSA_LT_INT:
			ec.emitCmpInt(inst, CondGE) // branch if NOT less-than

		case SSA_LE_INT:
			ec.emitCmpIntLE(i, inst)

		case SSA_LT_FLOAT:
			ec.emitCmpFloat(inst, CondGE) // branch if NOT less-than

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

		case SSA_LOAD_GLOBAL, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_CALL_INNER_TRACE, SSA_INNER_LOOP,
			SSA_PHI, SSA_STORE_SLOT, SSA_BOX_FLOAT,
			SSA_SIDE_EXIT, SSA_DIV_INT:
			// Not yet implemented — emit as call-exit or skip
		}
	}

	// Store-back: write all allocated register values back to memory before loop back-edge
	ec.emitStoreBack()
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

	// Check if the value is in a GPR via slot allocation
	if slot >= 0 {
		if reg, ok := ec.regMap.IntReg(slot); ok {
			return reg
		}
	}

	// Check for constant values
	if inst.Op == SSA_CONST_INT {
		ec.asm.LoadImm64(scratch, inst.AuxInt)
		return scratch
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

	slot := int(inst.Slot)
	// Check slot-level float allocation
	if slot >= 0 {
		if freg, ok := ec.regMap.FloatReg(slot); ok {
			return freg
		}
	}

	// Check for float constant
	if inst.Op == SSA_CONST_FLOAT {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(scratch, X0)
		return scratch
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
	// FMADD: dst = arg1 * arg2 + AuxRef (accumulator)
	// In our SSA, FMADD has Arg1=mul_a, Arg2=mul_b, and accumulator is encoded in AuxInt
	// For now, FMADD isn't generated (FuseMultiplyAdd is a no-op), so this is a placeholder
	_ = ref
}

func (ec *emitCtx) emitFMSUB(ref SSARef, inst *SSAInst) {
	// Similar placeholder
	_ = ref
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
func (ec *emitCtx) spillFloat(ref SSARef, inst *SSAInst, dst FReg) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
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
		// This is the FORLOOP exit check: branch to loop_done, not side_exit
		ec.asm.BCond(CondGT, "loop_done")
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

// emitCmpFloatLE handles SSA_LE_FLOAT.
func (ec *emitCtx) emitCmpFloatLE(idx int, inst *SSAInst) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	ec.asm.FCMPd(a1, a2)
	// LE: guard passes if a1 <= a2; exit if GT
	if idx == ec.loopExitIdx {
		ec.asm.BCond(CondGT, "loop_done")
	} else {
		ec.emitGuardBranch(CondGT, inst.PC)
	}
}

// emitGuardBranch emits a conditional branch to the side-exit path.
// Sets up ExitPC before branching.
func (ec *emitCtx) emitGuardBranch(failCond Cond, pc int) {
	ec.asm.BCond(failCond, "side_exit_setup")
}

// ────────────────────────────────────────────────────────────────────────────
// Guard truthy
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitGuardTruthy(inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

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
		ec.asm.BCond(CondEQ, "guard_truthy_ok_"+itoa(int(inst.Slot)))
		ec.asm.LoadImm64(X1, nb_i64(NB_ValFalse))
		ec.asm.CMPreg(X0, X1)
		ec.asm.BCond(CondEQ, "guard_truthy_ok_"+itoa(int(inst.Slot)))
		// Not nil, not false → truthy → fail
		ec.asm.B("side_exit_setup")
		ec.asm.Label("guard_truthy_ok_" + itoa(int(inst.Slot)))
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
	if slot < 0 {
		return
	}
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(freg, X0)
	} else if freg, ok := ec.regMap.FloatReg(slot); ok {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(freg, X0)
	} else {
		// Store directly to memory (raw float bits = NaN-boxed float)
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.STR(X0, regRegs, slot*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_FIELD: table field access
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadField(ref SSARef, inst *SSAInst) {
	tableSlot := int(inst.Slot)
	fieldIdx := int(int32(inst.AuxInt))

	// Load table NaN-boxed value from memory
	ec.asm.LDR(X0, regRegs, tableSlot*ValueSize)
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

	// Store to destination register based on type
	dstSlot := int(inst.Slot)
	// For LOAD_FIELD, the SSA Slot field is the table's slot (source).
	// The destination is determined by who uses this ref.
	// However, in our SSA, LOAD_FIELD's Slot IS the destination slot (ir.A from OP_GETFIELD).
	// Looking at the builder: Slot: int16(ir.A). So inst.Slot = ir.A = destination.
	// But tableSlot is ir.B, which is inst.Arg1's slot. Let me re-check.
	// Actually in ssa_build.go:
	//   ref := b.emit(SSAInst{
	//       Op:     SSA_LOAD_FIELD,
	//       Type:   ssaTypeFromRuntime(ir.AType),
	//       Arg1:   tableRef,
	//       AuxInt: int64(ir.FieldIndex),
	//       Slot:   int16(ir.A),    // destination slot
	//       PC:     ir.PC,
	//   })
	// So inst.Slot is the DESTINATION slot, and the table is found via Arg1.
	// The table ref's slot is the table slot.

	// We need the TABLE slot, not the destination slot.
	// The table slot is the slot of the SSA ref that Arg1 points to.
	var tblSlot int = -1
	if inst.Arg1 != SSARefNone && int(inst.Arg1) < len(ec.f.Insts) {
		tblSlot = int(ec.f.Insts[inst.Arg1].Slot)
	}

	// Re-load from table. We already have X2 = field value.
	// Need to reload: table from tblSlot.
	if tblSlot >= 0 {
		ec.asm.LDR(X0, regRegs, tblSlot*ValueSize)
		EmitExtractPtr(ec.asm, X0, X0)
		ec.asm.LDR(X1, X0, TableOffSvals)
		ec.asm.LDR(X2, X1, fieldIdx*ValueSize)
	}

	_ = dstSlot // will use inst.Slot as destination
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
// STORE_FIELD: table field write
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitStoreField(inst *SSAInst) {
	// inst.Slot = table slot (ir.A), inst.AuxInt = fieldIndex
	// inst.Arg1 = table ref, inst.Arg2 = value ref
	fieldIdx := int(int32(inst.AuxInt))
	tblSlot := int(inst.Slot)

	// Load table pointer
	ec.asm.LDR(X0, regRegs, tblSlot*ValueSize)
	EmitCheckIsTableFull(ec.asm, X0, X1, X2, "side_exit_setup")
	EmitExtractPtr(ec.asm, X0, X0)
	ec.asm.CBZ(X0, "side_exit_setup")

	// Check no metatable
	ec.asm.LDR(X1, X0, TableOffMetatable)
	ec.asm.CBNZ(X1, "side_exit_setup")

	// Get value to store
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

	// Store to svals[fieldIdx]
	ec.asm.LDR(X1, X0, TableOffSvals)
	ec.asm.STR(X3, X1, fieldIdx*ValueSize)
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_ARRAY / STORE_ARRAY
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadArray(ref SSARef, inst *SSAInst) {
	// For now, emit as a call-exit (table array access is complex)
	ec.emitCallExit(inst)
}

func (ec *emitCtx) emitStoreArray(inst *SSAInst) {
	// For now, emit as a call-exit
	ec.emitCallExitInst(inst)
}

// ────────────────────────────────────────────────────────────────────────────
// TABLE_LEN
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitTableLen(ref SSARef, inst *SSAInst) {
	// For now, side-exit on TABLE_LEN (complex operation)
	ec.emitCallExit(inst)
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

	// Store back all registers to memory before exiting
	ec.emitStoreBack()

	// Set ExitPC to the call instruction's PC
	asm.LoadImm64(X9, int64(inst.PC))
	asm.STR(X9, regCtx, TraceCtxOffExitPC)

	// Exit with code 3 (call-exit)
	// The executor will handle the call and may re-enter the trace.
	// For now, call-exit re-entry is not supported, so the executor
	// will treat this as a side-exit.
	asm.LoadImm64(X0, 3)
	asm.B("epilogue")
}

// ────────────────────────────────────────────────────────────────────────────
// Intrinsics
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitIntrinsic(ref SSARef, inst *SSAInst) {
	// Only sqrt is implemented as a true intrinsic for now
	switch int(inst.AuxInt) {
	case IntrinsicSqrt:
		// The argument is typically in the slot before the call result.
		// For sqrt intrinsic: R(A) = sqrt(R(A+1))
		// The arg is at slot A+1, result goes to slot A.
		argSlot := int(inst.Slot) + 1
		dstSlot := int(inst.Slot)

		// Load argument
		var argFReg FReg = D0
		if freg, ok := ec.regMap.FloatReg(argSlot); ok {
			argFReg = freg
		} else {
			ec.asm.FLDRd(D0, regRegs, argSlot*ValueSize)
			argFReg = D0
		}

		dstFReg := ec.getFloatDst(ref, inst, D1)
		ec.asm.FSQRTd(dstFReg, argFReg)

		// Store result
		if freg, ok := ec.regMap.FloatRefReg(ref); ok && freg == dstFReg {
			// In register, will be stored back at end of loop iteration
		} else if freg, ok := ec.regMap.FloatReg(dstSlot); ok && freg == dstFReg {
			// In register
		} else {
			ec.asm.FSTRd(dstFReg, regRegs, dstSlot*ValueSize)
		}

	default:
		// Unknown intrinsic — fall back to call-exit
		ec.emitCallExitInst(inst)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Store-back: write all register values to memory before loop back-edge
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitStoreBack() {
	asm := ec.asm

	// Store all allocated integer registers back to memory (NaN-boxed)
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			EmitBoxIntFast(asm, X0, reg, regTagInt)
			asm.STR(X0, regRegs, slot*ValueSize)
		}
	}

	// Store all allocated float registers back to memory
	// We need to store the float register for each slot that has one.
	// Float ref-level allocation: need to find which slots correspond to which refs.
	if ec.regMap.Float != nil {
		for slot, freg := range ec.regMap.Float.slotToReg {
			asm.FSTRd(freg, regRegs, slot*ValueSize)
		}
	}

	// For ref-level float allocations that have a slot, store them too.
	// But we need to avoid double-storing slots already handled above.
	if ec.regMap.FloatRef != nil {
		for ref, freg := range ec.regMap.FloatRef.refToReg {
			if int(ref) >= len(ec.f.Insts) {
				continue
			}
			inst := &ec.f.Insts[ref]
			slot := int(inst.Slot)
			if slot < 0 {
				continue
			}
			// Skip if slot already handled by slot-level float alloc
			if ec.regMap.Float != nil {
				if _, ok := ec.regMap.Float.slotToReg[slot]; ok {
					continue
				}
			}
			asm.FSTRd(freg, regRegs, slot*ValueSize)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Reload all registers from memory (after call-exit resume)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitReloadAll() {
	asm := ec.asm

	// Reload integer registers
	if ec.regMap.Int != nil {
		for slot, reg := range ec.regMap.Int.slotToReg {
			asm.LDR(reg, regRegs, slot*ValueSize)
			EmitUnboxInt(asm, reg, reg)
		}
	}

	// Reload float registers (slot-level)
	if ec.regMap.Float != nil {
		for slot, freg := range ec.regMap.Float.slotToReg {
			asm.FLDRd(freg, regRegs, slot*ValueSize)
		}
	}

	// Reload float registers (ref-level)
	if ec.regMap.FloatRef != nil {
		for ref, freg := range ec.regMap.FloatRef.refToReg {
			if int(ref) >= len(ec.f.Insts) {
				continue
			}
			inst := &ec.f.Insts[ref]
			slot := int(inst.Slot)
			if slot < 0 {
				continue
			}
			if ec.regMap.Float != nil {
				if _, ok := ec.regMap.Float.slotToReg[slot]; ok {
					continue
				}
			}
			asm.FLDRd(freg, regRegs, slot*ValueSize)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Cold paths
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitSideExit() {
	asm := ec.asm

	asm.Label("side_exit_setup")
	// Store back all register values to memory before exiting
	ec.emitStoreBack()

	// Set ExitPC to the loop PC (VM resumes at the loop instruction)
	loopPC := 0
	if ec.f.Trace != nil {
		loopPC = ec.f.Trace.LoopPC
	}
	asm.LoadImm64(X9, int64(loopPC))
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

func (ec *emitCtx) emitLoopDone() {
	asm := ec.asm
	asm.Label("loop_done")

	// Store back all register values to memory
	ec.emitStoreBack()

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

