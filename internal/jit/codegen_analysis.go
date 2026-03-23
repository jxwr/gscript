//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/vm"
)

// regSet is a bitmask for VM register sets (supports registers 0-63).
type regSet = uint64

func regBit(r int) regSet {
	if r < 0 || r >= 64 {
		return 0
	}
	return 1 << uint(r)
}

func regSetHas(s regSet, r int) bool {
	return r >= 0 && r < 64 && s&regBit(r) != 0
}

// ──────────────────────────────────────────────────────────────────────────────
// Type guard hoisting — forward data-flow analysis for known-int registers
// ──────────────────────────────────────────────────────────────────────────────

// analyzeKnownIntRegs computes, for each bytecode PC, which VM registers are
// guaranteed to hold TypeInt values. Uses bitmask-based set operations for speed.
func (cg *Codegen) analyzeKnownIntRegs() {
	code := cg.proto.Code
	n := len(code)
	if n == 0 {
		return
	}

	cg.knownInt = make([]regSet, n)
	cg.reachable = make([]bool, n)
	cg.reachable[0] = true // entry point

	// For self-call functions, parameters are guaranteed TypeInt at function entry.
	// The outermost call validates parameter types before self_call_entry;
	// all recursive self-calls pass int results from SUB/ADD which are always int.
	// This eliminates redundant type guards on parameters throughout the function body.
	if cg.hasSelfCalls && cg.proto.NumParams > 0 {
		for i := 0; i < cg.proto.NumParams && i < 64; i++ {
			cg.knownInt[0] |= regBit(i)
		}
	}

	changed := true
	for changed {
		changed = false
		for pc := 0; pc < n; pc++ {
			if !cg.reachable[pc] {
				continue
			}
			out := cg.intTransfer(pc)
			for _, succ := range cg.pcSuccessors(pc) {
				if succ < 0 || succ >= n {
					continue
				}
				if !cg.reachable[succ] {
					cg.reachable[succ] = true
					cg.knownInt[succ] = out
					changed = true
				} else {
					// Intersect: only keep bits present in both.
					merged := cg.knownInt[succ] & out
					if merged != cg.knownInt[succ] {
						cg.knownInt[succ] = merged
						changed = true
					}
				}
			}
		}
	}
}

// intTransfer computes the output known-int set after executing instruction at pc.
func (cg *Codegen) intTransfer(pc int) regSet {
	inst := cg.proto.Code[pc]
	op := vm.DecodeOp(inst)
	out := cg.knownInt[pc]

	switch op {
	case vm.OP_LOADINT:
		out |= regBit(vm.DecodeA(inst))
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_UNM:
		out |= regBit(vm.DecodeA(inst))
	case vm.OP_FORPREP:
		a := vm.DecodeA(inst)
		out |= regBit(a) | regBit(a+1) | regBit(a+2)
	case vm.OP_FORLOOP:
		a := vm.DecodeA(inst)
		out |= regBit(a) | regBit(a+3)
	case vm.OP_MOVE:
		a := vm.DecodeA(inst)
		if regSetHas(cg.knownInt[pc], vm.DecodeB(inst)) {
			out |= regBit(a)
		} else {
			out &^= regBit(a)
		}
	case vm.OP_LOADK:
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		if bx < len(cg.proto.Constants) && cg.proto.Constants[bx].IsInt() {
			out |= regBit(a)
		} else {
			out &^= regBit(a)
		}
	case vm.OP_LOADNIL:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		for i := a; i <= a+b; i++ {
			out &^= regBit(i)
		}
	case vm.OP_LOADBOOL, vm.OP_NOT:
		out &^= regBit(vm.DecodeA(inst))
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_JMP:
		// No register writes.
	case vm.OP_CALL:
		// For inline/self-call candidates, the result is placed as TypeInt.
		// If the result destination was traced, mark that register too.
		if cg.inlineCandidates != nil {
			if candidate, ok := cg.inlineCandidates[pc]; ok {
				out |= regBit(candidate.fnReg)
				if candidate.resultDest >= 0 {
					out |= regBit(candidate.resultDest)
				}
				break
			}
		}
		out &^= regBit(vm.DecodeA(inst))
	case vm.OP_GETGLOBAL:
		// Skipped GETGLOBALs don't modify registers.
		if cg.inlineSkipPCs != nil && cg.inlineSkipPCs[pc] {
			break
		}
		out &^= regBit(vm.DecodeA(inst))
	default:
		out &^= regBit(vm.DecodeA(inst))
	}
	return out
}

// pcSuccessors returns the successor PCs for an instruction.
func (cg *Codegen) pcSuccessors(pc int) []int {
	inst := cg.proto.Code[pc]
	op := vm.DecodeOp(inst)

	if !cg.isSupported(op) {
		// Check if this is an inline/self-call CALL that we handle natively.
		if op == vm.OP_CALL && cg.inlineCandidates != nil {
			if _, ok := cg.inlineCandidates[pc]; ok {
				return []int{pc + 1}
			}
		}
		// Call-exit opcodes resume at pc+1 (executor handles the instruction).
		if isCallExitOp(op) {
			return []int{pc + 1}
		}
		return nil // permanent side-exit, no JIT successors
	}

	switch op {
	case vm.OP_RETURN:
		return nil
	case vm.OP_JMP:
		return []int{pc + 1 + vm.DecodesBx(inst)}
	case vm.OP_FORPREP:
		return []int{pc + 1 + vm.DecodesBx(inst)}
	case vm.OP_FORLOOP:
		return []int{pc + 1, pc + 1 + vm.DecodesBx(inst)}
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST:
		return []int{pc + 1, pc + 2}
	case vm.OP_LOADBOOL:
		if vm.DecodeC(inst) != 0 {
			return []int{pc + 2}
		}
		return []int{pc + 1}
	default:
		return []int{pc + 1}
	}
}

// isRKKnownInt returns true if RK(idx) is guaranteed TypeInt at the given PC.
func (cg *Codegen) isRKKnownInt(pc, rkIdx int) bool {
	if vm.IsRK(rkIdx) {
		constIdx := vm.RKToConstIdx(rkIdx)
		if constIdx < len(cg.proto.Constants) {
			return cg.proto.Constants[constIdx].IsInt()
		}
		return false
	}
	if cg.knownInt != nil && pc < len(cg.knownInt) {
		return regSetHas(cg.knownInt[pc], rkIdx)
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// For-loop analysis and register pinning
// ──────────────────────────────────────────────────────────────────────────────

// analyzeForLoops detects numeric for-loop structures and determines
// step values for optimization.
func (cg *Codegen) analyzeForLoops() {
	code := cg.proto.Code
	cg.forLoops = make(map[int]*forLoopDesc)
	cg.pinnedRegs = make(map[int]Reg)

	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		if vm.DecodeOp(inst) != vm.OP_FORPREP {
			continue
		}
		aReg := vm.DecodeA(inst)
		sbx := vm.DecodesBx(inst)
		flPC := pc + 1 + sbx
		if flPC < 0 || flPC >= len(code) || vm.DecodeOp(code[flPC]) != vm.OP_FORLOOP {
			continue
		}
		flSbx := vm.DecodesBx(code[flPC])
		bodyStart := flPC + 1 + flSbx

		desc := &forLoopDesc{
			prepPC:    pc,
			forloopPC: flPC,
			bodyStart: bodyStart,
			aReg:      aReg,
			canPin:    true,
		}

		// Detect step value by scanning backward from FORPREP for LOADINT R(A+2).
		stepReg := aReg + 2
		for scanPC := pc - 1; scanPC >= 0; scanPC-- {
			si := code[scanPC]
			sa := vm.DecodeA(si)
			if sa == stepReg {
				if vm.DecodeOp(si) == vm.OP_LOADINT {
					desc.stepValue = int64(vm.DecodesBx(si))
					desc.stepKnown = true
				}
				break
			}
		}

		// Check if R(A+3) is written in the body — if not, alias it to R(A).
		loopVarReg := aReg + 3
		loopVarWritten := false
		for scanPC := bodyStart; scanPC < flPC; scanPC++ {
			si := code[scanPC]
			if vm.DecodeA(si) == loopVarReg {
				sop := vm.DecodeOp(si)
				// Skip comparison/test ops that don't write to R(A).
				if sop != vm.OP_EQ && sop != vm.OP_LT && sop != vm.OP_LE &&
					sop != vm.OP_TEST && sop != vm.OP_JMP {
					loopVarWritten = true
					break
				}
			}
		}
		desc.aliasLoopVar = !loopVarWritten

		// Determine body accumulator registers (read+write same reg in ADD/SUB/MUL).
		desc.bodyAccumulators = cg.findAccumulators(bodyStart, flPC, aReg)

		cg.forLoops[flPC] = desc
		cg.forLoops[pc] = desc // also index by prepPC
	}

	// Disable pinning for non-innermost loops (loops whose body contains another FORPREP).
	// Use a set to deduplicate (forLoops is indexed by both prepPC and forloopPC).
	seen := make(map[*forLoopDesc]bool)
	for _, desc := range cg.forLoops {
		if seen[desc] {
			continue
		}
		seen[desc] = true
		for innerPC := desc.bodyStart; innerPC < desc.forloopPC; innerPC++ {
			if vm.DecodeOp(code[innerPC]) == vm.OP_FORPREP {
				desc.canPin = false
				break
			}
		}
	}
}

// isCallExitOp returns true if the opcode should use call-exit (ExitCode=2)
// instead of a permanent side-exit (ExitCode=1).
// Call-exit allows the executor to handle the instruction and re-enter JIT.
func isCallExitOp(op vm.Opcode) bool {
	switch op {
	case vm.OP_CALL, vm.OP_GETGLOBAL, vm.OP_SETGLOBAL,
		vm.OP_GETTABLE, vm.OP_SETTABLE,
		vm.OP_GETFIELD, vm.OP_SETFIELD,
		vm.OP_NEWTABLE, vm.OP_LEN:
		return true
	}
	return false
}

func (cg *Codegen) analyzeCallExitPCs() {
	code := cg.proto.Code
	cg.callExitPCs = nil

	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if cg.inlineSkipPCs[pc] {
			continue
		}
		if cg.crossCallSkipPCs[pc] {
			continue
		}
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		if _, ok := cg.crossCalls[pc]; ok {
			continue
		}
		if !cg.isSupported(op) && isCallExitOp(op) {
			cg.callExitPCs = append(cg.callExitPCs, pc)
		}
		// GETFIELD/SETFIELD/GETTABLE/SETTABLE are "supported" (native fast path) but still need
		// call-exit resume entries for the fallback slow path.
		if op == vm.OP_GETFIELD || op == vm.OP_SETFIELD || op == vm.OP_GETTABLE || op == vm.OP_SETTABLE {
			cg.callExitPCs = append(cg.callExitPCs, pc)
		}
	}

	// Detect comparison call-exit PCs.
	// EQ/LT/LE with non-integer operands will call-exit on type guard failure.
	// The executor may resume at pc+1 (condition false) or pc+2 (condition true/skip).
	cg.cmpCallExitPCs = nil
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op == vm.OP_EQ || op == vm.OP_LT || op == vm.OP_LE {
			bIdx := vm.DecodeB(code[pc])
			cIdx := vm.DecodeC(code[pc])
			bKnown := cg.isRKKnownInt(pc, bIdx)
			cKnown := cg.isRKKnownInt(pc, cIdx)
			if !bKnown || !cKnown {
				cg.cmpCallExitPCs = append(cg.cmpCallExitPCs, pc)
			}
		}
	}
}

// isSupported returns true if the opcode can be compiled directly to native code.
// Note: GETGLOBAL, SETGLOBAL, and CALL are handled via call-exit (ExitCode=2)
// and are NOT listed here — they go through the call-exit path in emitBody.
func (cg *Codegen) isSupported(op vm.Opcode) bool {
	switch op {
	case vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_LOADINT, vm.OP_LOADK,
		vm.OP_MOVE,
		vm.OP_ADD, vm.OP_SUB, vm.OP_MUL,
		vm.OP_UNM, vm.OP_NOT,
		vm.OP_EQ, vm.OP_LT, vm.OP_LE,
		vm.OP_JMP,
		vm.OP_FORPREP, vm.OP_FORLOOP,
		vm.OP_RETURN,
		vm.OP_TEST,
		vm.OP_GETFIELD, vm.OP_SETFIELD,
		vm.OP_GETTABLE,
		vm.OP_SETTABLE:
		return true
	}
	return false
}
