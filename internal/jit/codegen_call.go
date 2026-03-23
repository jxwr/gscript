//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// hasCrossCallExits checks if the function has non-self, non-inline CALL instructions
// that would become call-exits. These are incompatible with the self-call ARM64 stack
// mechanism: call-exit jumps to epilogue which assumes a clean stack, but self-calls
// push frames on the ARM64 stack that would be orphaned.
func (cg *Codegen) hasCrossCallExits() bool {
	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op == vm.OP_CALL {
			// Skip self-call and inline candidates (they don't go through call-exit).
			if _, ok := cg.inlineCandidates[pc]; ok {
				continue
			}
			return true
		}
		// Non-CALL call-exit ops (NEWTABLE, LEN, GETGLOBAL, SETGLOBAL, etc.)
		// that are NOT natively supported will become call-exits. These corrupt
		// the ARM64 stack when executed within self-call frames, because the
		// call-exit epilogue doesn't unwind self-call stack frames.
		if isCallExitOp(op) && !cg.isSupported(op) {
			// Skip GETGLOBAL/SETGLOBAL that are part of self-call patterns
			// (consumed by inline analysis, never emitted as instructions).
			if cg.inlineSkipPCs[pc] {
				continue
			}
			return true
		}
	}
	return false
}

// hasCrossCallExitsExcluding checks if there are CALL instructions that are NOT
// handled by self-call, inline, OR cross-call BLR. Returns true if some CALLs
// would still need call-exit.
func (cg *Codegen) hasCrossCallExitsExcluding() bool {
	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op == vm.OP_CALL {
			if _, ok := cg.inlineCandidates[pc]; ok {
				continue
			}
			if _, ok := cg.crossCalls[pc]; ok {
				continue
			}
			return true
		}
		// Non-CALL unsupported call-exit ops can't be handled by cross-call BLR.
		if isCallExitOp(op) && !cg.isSupported(op) {
			if cg.inlineSkipPCs[pc] {
				continue
			}
			return true
		}
	}
	return false
}

// analyzeCrossCalls detects GETGLOBAL + CALL patterns where the global
// resolves to a known VM function. For each detected pattern, allocates
// a cross-call slot in the engine for direct BLR optimization.
func (cg *Codegen) analyzeCrossCalls() {
	cg.crossCalls = make(map[int]*crossCallInfo)
	cg.crossCallSkipPCs = make(map[int]bool)
	if cg.engine == nil || cg.globals == nil {
		return
	}

	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		// Skip if already handled by inline or self-call.
		if cg.inlineSkipPCs[pc] {
			continue
		}

		globalA := vm.DecodeA(inst)
		globalBx := vm.DecodeBx(inst)
		if globalBx >= len(cg.proto.Constants) {
			continue
		}
		name := cg.proto.Constants[globalBx].Str()

		// Look for the CALL that uses R(globalA).
		for pc2 := pc + 1; pc2 < len(code) && pc2 <= pc+10; pc2++ {
			inst2 := code[pc2]
			op2 := vm.DecodeOp(inst2)
			if op2 == vm.OP_CALL && vm.DecodeA(inst2) == globalA {
				// Skip if already handled.
				if _, ok := cg.inlineCandidates[pc2]; ok {
					break
				}

				b := vm.DecodeB(inst2)
				c := vm.DecodeC(inst2)

				// Resolve the callee's proto from globals.
				fnVal, ok := cg.globals[name]
				if !ok || !fnVal.IsFunction() {
					break
				}
				vcl, _ := fnVal.Ptr().(*vm.Closure)
				if vcl == nil {
					break
				}

				// Allocate a cross-call slot.
				slot := cg.engine.allocCrossCallSlot(name, vcl.Proto)

				info := &crossCallInfo{
					getglobalPC: pc,
					callPC:      pc2,
					calleeName:  name,
					calleeProto: vcl.Proto,
					fnReg:       globalA,
					nArgs:       b - 1,
					nResults:    c - 1,
					slot:        slot,
				}
				cg.crossCalls[pc2] = info
				cg.crossCallSkipPCs[pc] = true // skip the GETGLOBAL
				break
			}
			if vm.DecodeA(inst2) == globalA {
				break // register overwritten
			}
		}
	}
}

// Maximum cross-call depth before falling back to interpreter.
const maxCrossCallDepthNative = 200

// emitCrossCall emits ARM64 code for a direct BLR to a compiled callee function.
// Uses a shared JITContext approach: modifies the caller's JITContext to point to
// the callee's register window, BLR to callee (whose prologue/epilogue handles
// all register saving), then restores the caller's context fields.
//
// The callee's prologue saves ALL callee-saved registers (X19-X28, X29, X30)
// in its own 96-byte stack frame. The epilogue restores them. So after BLR returns,
// all caller registers are automatically restored. The caller only needs to
// save/restore the JITContext fields (Regs, Constants).
func (cg *Codegen) emitCrossCall(pc int, info *crossCallInfo) error {
	a := cg.asm
	fnReg := info.fnReg
	slotAddr := uintptr(unsafe.Pointer(info.slot))

	fallbackLabel := fmt.Sprintf("xcall_fallback_%d", pc)

	depthLabel := fmt.Sprintf("xcall_depth_%d", pc)
	calleeExitLabel := fmt.Sprintf("xcall_exit_%d", pc)

	// Load callee's code pointer from the cross-call slot.
	a.LoadImm64(X0, int64(slotAddr))
	a.LDR(X1, X0, 0) // X1 = slot.codePtr

	// If code pointer is 0 (not compiled), fall back to call-exit.
	a.CBZ(X1, fallbackLabel)

	// Check depth limit (uses X25 for combined self+cross depth tracking).
	a.ADDimm(regSelfDepth, regSelfDepth, 1)
	a.CMPimm(regSelfDepth, maxCrossCallDepthNative)
	a.BCond(CondGE, depthLabel)

	// Load callee's constants pointer from the slot.
	a.LDR(X2, X0, 8) // X2 = slot.constantsPtr

	// Save caller's Regs and Constants pointers on the ARM64 stack.
	// Also save X1 (callee code ptr) since the callee's prologue will clobber it.
	// 16-byte frame: [SP+0] = regRegs (caller), [SP+8] = regConsts (caller)
	a.STPpre(regRegs, regConsts, SP, -16)

	// Compute callee's register window address.
	// Callee's R(0) = Caller's R(fnReg+1).
	calleeOffset := (fnReg + 1) * ValueSize
	if calleeOffset <= 4095 {
		a.ADDimm(X3, regRegs, uint16(calleeOffset))
	} else {
		a.LoadImm64(X3, int64(calleeOffset))
		a.ADDreg(X3, regRegs, X3)
	}

	// Update the shared JITContext to point to callee's register window and constants.
	a.STR(X3, regCtx, ctxOffRegs)
	a.STR(X2, regCtx, ctxOffConstants)
	// Clear ResumePC so callee starts from the beginning.
	a.STR(XZR, regCtx, ctxOffResumePC)

	// X0 = JITContext pointer, X1 = callee code. BLR to callee.
	a.MOVreg(X0, regCtx)
	a.BLR(X1)

	// After callee returns: X0 = exit code.
	// The callee's epilogue restored all callee-saved registers to the values
	// they had before our BLR, including regCtx (X28).
	// Read RetBase and RetCount from the context.
	a.MOVreg(X3, X0) // X3 = exit code
	a.LDR(X4, regCtx, ctxOffRetBase)  // X4 = RetBase
	a.LDR(X5, regCtx, ctxOffRetCount) // X5 = RetCount

	// Restore caller's Regs and Constants from the stack.
	a.LDPpost(regRegs, regConsts, SP, 16)

	// Restore the JITContext to point to caller's register window.
	a.STR(regRegs, regCtx, ctxOffRegs)
	a.STR(regConsts, regCtx, ctxOffConstants)

	a.SUBimm(regSelfDepth, regSelfDepth, 1) // depth--

	// Check exit code: if not 0, callee had an issue.
	a.CBNZ(X3, calleeExitLabel)

	// Normal return: copy result from callee's register window to caller's R(fnReg).
	// The callee wrote results to the register array at calleeBase + RetBase*ValueSize.
	// Source address: regRegs + calleeOffset + RetBase * ValueSize
	EmitMulValueSize(a, X4, X4, X6) // X4 = RetBase * ValueSize
	a.LoadImm64(X6, int64(calleeOffset))
	a.ADDreg(X4, X4, X6)           // X4 = calleeOffset + RetBase*ValueSize
	a.ADDreg(X4, regRegs, X4)      // X4 = source address in register array

	// Destination: regRegs + fnReg * ValueSize
	dstOff := fnReg * ValueSize
	a.LoadImm64(X6, int64(dstOff))
	a.ADDreg(X6, regRegs, X6)      // X6 = destination address

	// Copy 8 bytes (one NaN-boxed Value) from source to destination.
	a.LDR(X7, X4, 0)
	a.STR(X7, X6, 0)

	// Update ctx.Top for subsequent B=0 calls.
	// Top = fnReg + RetCount
	a.LoadImm64(X7, int64(fnReg))
	a.ADDreg(X7, X7, X5) // X7 = fnReg + RetCount
	a.STR(X7, regCtx, ctxOffTop)

	// All three cold paths deferred to cold section.
	getglobalPC := info.getglobalPC

	// Callee returned non-zero exit code.
	cg.deferCold(calleeExitLabel, func() {
		cg.asm.MOVreg(SP, X29)
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
		cg.asm.LDR(regConsts, regCtx, ctxOffConstants)
		cg.asm.MOVimm16(regSelfDepth, 0)
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
	})

	// Fallback: callee not compiled, use call-exit for the GETGLOBAL.
	capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinned[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinned {
			cg.spillPinnedRegNB(vmReg, armReg)
		}
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // call-exit
		cg.asm.B("epilogue")
	})

	// Depth exceeded: side exit.
	cg.deferCold(depthLabel, func() {
		cg.asm.SUBimm(regSelfDepth, regSelfDepth, 1)
		cg.asm.MOVreg(SP, X29)
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
		cg.asm.MOVimm16(regSelfDepth, 0)
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
	})

	return nil
}
