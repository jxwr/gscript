//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/runtime"
)

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
			// For slot-less constants (slot=-1), we must still load the value
			// into the ref-level FPR so the loop body can use it.
			if freg, ok := ec.regMap.FloatRefReg(ref); ok {
				asm.LoadImm64(X0, inst.AuxInt)
				asm.FMOVtoFP(freg, X0)
				if slot >= 0 {
					loadedFloatSlots[slot] = true
				}
			} else if slot >= 0 {
				if freg, ok := ec.regMap.FloatReg(slot); ok {
					asm.LoadImm64(X0, inst.AuxInt)
					asm.FMOVtoFP(freg, X0)
					loadedFloatSlots[slot] = true
				}
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
