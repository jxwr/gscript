//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// SSA Codegen: compiles SSAFunc to ARM64 native code.
//
// Key design: slot-based register allocation with unboxed integers.
//
// The SSA IR represents one loop iteration. The native code loops by branching
// back to the loop header. For this to work, loop-carried values must persist
// across iterations in the same ARM64 registers.
//
// Solution: allocate ARM64 registers by VM slot (not by SSA ref).
// All SSA operations that read/write the same VM slot use the same register.
// This ensures values carry over correctly across loop back-edges.
//
// Register layout:
//   X19 = trace context pointer
//   X20-X23 = allocated VM slots (up to 4 hot slots)
//   X24 = regTagInt (pinned NaN-boxing int tag 0xFFFE000000000000)
//   X25 = (unused in SSA codegen)
//   X26 = regRegs (pointer to vm.regs[base])
//   X27 = regConsts (pointer to trace constants)
//   X0-X9 = scratch registers

// CompileSSA compiles an SSAFunc to native ARM64 code, producing a CompiledTrace.
//
// Pipeline:
//   1. Analysis passes: register allocation, liveness analysis, use-def chains
//   2. Emit ARM64 code using the analysis results
func CompileSSA(f *SSAFunc) (*CompiledTrace, error) {
	if f == nil || len(f.Insts) == 0 {
		return nil, fmt.Errorf("ssa codegen: empty SSA function")
	}

	if !ssaIsIntegerOnly(f) {
		return nil, fmt.Errorf("ssa codegen: trace contains non-integer ops")
	}

	// Phase 1: Analysis passes
	ud := BuildUseDef(f)
	regMap := AllocateRegisters(f)
	liveInfo := AnalyzeLiveness(f)

	// DEBUG: dump allocation info
	if debugSSAStoreBack {
		fmt.Printf("[SSA-COMPILE] loopPC=%d\n", f.Trace.LoopPC)
		fmt.Printf("[SSA-COMPILE] Int slots: ")
		for slot, reg := range regMap.Int.slotToReg {
			fmt.Printf("R%d→%v ", slot, reg)
		}
		fmt.Printf("\n")
		fmt.Printf("[SSA-COMPILE] Float slots: ")
		for slot, reg := range regMap.Float.slotToReg {
			fmt.Printf("R%d→%v ", slot, reg)
		}
		fmt.Printf("\n")
		fmt.Printf("[SSA-COMPILE] WrittenSlots: ")
		for slot := range liveInfo.WrittenSlots {
			fmt.Printf("R%d(type=%v) ", slot, liveInfo.SlotTypes[slot])
		}
		fmt.Printf("\n")
		for i, inst := range f.Insts {
			fmt.Printf("[SSA-COMPILE]   [%3d] Op=%d Type=%d Slot=%d Arg1=%d Arg2=%d AuxInt=%d\n",
				i, inst.Op, inst.Type, inst.Slot, inst.Arg1, inst.Arg2, inst.AuxInt)
		}
	}

	// Phase 2: Emit ARM64
	_ = ud // reserved for future optimization passes
	return emitSSA(f, regMap, liveInfo)
}

// buildFloatRefSpill computes a map of written float slots NOT in Float.slotToReg
// but with ref-level D register allocations. Maps each such slot to the D register
// of the LAST ref-level writer in the loop body. This is needed for store-back:
// ref-level allocated floats skip memory writes during the loop body, but the
// store-back only iterates Float.slotToReg.
func buildFloatRefSpill(f *SSAFunc, regMap *RegMap) map[int]FReg {
	result := make(map[int]FReg)
	if f == nil || regMap.FloatRef == nil {
		return result
	}
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return result
	}
	// Find the last ref-level float writer for each slot in the loop body
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		ref := SSARef(i)
		dreg, hasRefReg := regMap.FloatRefReg(ref)
		if !hasRefReg {
			continue
		}
		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}
		// Skip if already in slot-level map (handled by normal store-back)
		if _, ok := regMap.FloatReg(slot); ok {
			continue
		}
		// Skip if in int register map
		if _, ok := regMap.IntReg(slot); ok {
			continue
		}
		// Record (last writer wins)
		result[slot] = dreg
	}
	return result
}

// callExitInfo records the SSA index and bytecode PC of each SSA_CALL instruction.
type callExitInfo struct {
	ssaIdx int // SSA instruction index
	pc     int // bytecode PC of the CALL
}

// ssaCodegen holds shared state across emitSSA's compilation phases.
// This avoids passing 10+ parameters through every phase method.
type ssaCodegen struct {
	asm           *Assembler
	f             *SSAFunc
	regMap        *RegMap
	liveInfo      *LiveInfo
	sm            *ssaSlotMapper
	floatRefSpill map[int]FReg
	trCtx         Reg

	// Phase outputs (populated by earlier phases, consumed by later ones)
	callExits     []callExitInfo
	loopIdx       int
	wbrFloatSlots map[int]bool
	hoistedConsts map[SSARef]bool
	hoistedTables map[int]int
	sideExitInfo  *sideExitContinuation
}

// emitSSA emits ARM64 machine code for an SSAFunc using pre-computed analysis results.
func emitSSA(f *SSAFunc, regMap *RegMap, liveInfo *LiveInfo) (*CompiledTrace, error) {
	g := &ssaCodegen{
		asm:           NewAssembler(),
		f:             f,
		regMap:        regMap,
		liveInfo:      liveInfo,
		sm:            newSSASlotMapper(f),
		floatRefSpill: buildFloatRefSpill(f, regMap),
		trCtx:         X19,
	}

	g.emitSSAPrologue()
	g.emitSSAResumeDispatch()

	if err := g.emitSSAPreLoopGuards(); err != nil {
		return nil, err
	}

	g.emitSSAPreLoopLoads()
	g.emitSSAPreLoopTableGuards()
	g.emitSSALoopBody()
	g.emitSSAColdPaths()
	g.emitSSAEpilogue()

	return g.emitSSAFinalize()
}

// emitSSAPrologue saves callee-saved registers, sets up the trace context pointer,
// loads regRegs/regConsts, and pins regTagInt.
func (g *ssaCodegen) emitSSAPrologue() {
	asm := g.asm
	// === Prologue ===
	asm.STPpre(X29, X30, SP, -128)
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)
	// Save callee-saved SIMD registers D8-D11
	asm.FSTP(D8, D9, SP, 96)
	asm.FSTP(D10, D11, SP, 112)

	asm.MOVreg(g.trCtx, X0)

	asm.LDR(regRegs, g.trCtx, 0)
	asm.LDR(regConsts, g.trCtx, 8)

	// Pin regTagInt (X24) with the NaN-boxing int tag constant.
	asm.LoadImm64(regTagInt, nb_i64(NB_TagInt))
}

// emitSSAEpilogue emits the epilogue label, stores ExitCode, restores callee-saved
// registers, and returns.
func (g *ssaCodegen) emitSSAEpilogue() {
	asm := g.asm
	// === Epilogue ===
	asm.Label("epilogue")
	asm.STR(X0, X19, 24) // ctx.ExitCode

	// Restore callee-saved SIMD registers
	asm.FLDP(D8, D9, SP, 96)
	asm.FLDP(D10, D11, SP, 112)
	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	asm.LDPpost(X29, X30, SP, 128)
	asm.RET()
}

// emitSSAFinalize assembles the machine code, allocates executable memory,
// and returns the CompiledTrace.
func (g *ssaCodegen) emitSSAFinalize() (*CompiledTrace, error) {
	code, err := g.asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("ssa codegen finalize: %w", err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		return nil, fmt.Errorf("ssa codegen alloc: %w", err)
	}
	if err := block.WriteCode(code); err != nil {
		return nil, fmt.Errorf("ssa codegen write: %w", err)
	}

	var constants []runtime.Value
	if g.f.Trace != nil {
		constants = g.f.Trace.Constants
	}
	var proto *vm.FuncProto
	var loopPC int
	if g.f.Trace != nil {
		proto = g.f.Trace.LoopProto
		loopPC = g.f.Trace.LoopPC
	}

	ct := &CompiledTrace{code: block, proto: proto, loopPC: loopPC, constants: constants}
	ct.hasCallExit = len(g.callExits) > 0
	return ct, nil
}
