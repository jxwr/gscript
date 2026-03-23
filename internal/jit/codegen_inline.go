//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/vm"
)

// inlineCandidate describes a GETGLOBAL + CALL pattern that can be inlined.
type inlineCandidate struct {
	getglobalPC   int              // PC of GETGLOBAL instruction
	callPC        int              // PC of CALL instruction
	callee        *vm.FuncProto    // the function to inline
	fnReg         int              // register holding the function (A field of GETGLOBAL/CALL)
	nArgs         int              // number of arguments
	nResults      int              // number of expected results
	isSelfCall    bool             // true if this is a self-recursive call
	isTailCall    bool             // true if CALL is immediately followed by RETURN of same register
	skipArgSave   bool             // true if X19/X22 don't need saving (next op is self-call or RETURN)
	skipTopUpdate bool             // true if ctx.Top write can be skipped (result consumed by tail call)
	directArgs    bool             // true if non-tail call args are computed directly into X19/X22 (skip memory roundtrip)
	argTraces     []inlineArgTrace // traced argument sources (populated during analysis)
	resultDest    int              // final destination register for result (-1 if no tracing)
	resultMovePC  int              // PC of the MOVE that copies result to resultDest (-1 if none)
}

// ──────────────────────────────────────────────────────────────────────────────
// Function inlining analysis
// ──────────────────────────────────────────────────────────────────────────────

// analyzeInlineCandidates detects GETGLOBAL + CALL patterns where the global
// resolves to a simple bytecode function that can be inlined, or where the
// global is a self-recursive call to the current function.
func (cg *Codegen) analyzeInlineCandidates() {
	cg.inlineCandidates = make(map[int]*inlineCandidate)
	cg.inlineSkipPCs = make(map[int]bool)
	cg.inlineArgSkipPCs = make(map[int]bool)

	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		globalA := vm.DecodeA(inst)
		globalBx := vm.DecodeBx(inst)
		if globalBx >= len(cg.proto.Constants) {
			continue
		}
		name := cg.proto.Constants[globalBx].Str()

		// Find the CALL that uses R(globalA) within the next few instructions
		for pc2 := pc + 1; pc2 < len(code) && pc2 <= pc+10; pc2++ {
			inst2 := code[pc2]
			op2 := vm.DecodeOp(inst2)
			if op2 == vm.OP_CALL && vm.DecodeA(inst2) == globalA {
				b := vm.DecodeB(inst2)
				c := vm.DecodeC(inst2)

				// Check for self-recursive call first.
				if name == cg.proto.Name && cg.proto.Name != "" {
					// Detect tail call: CALL immediately followed by RETURN of same register.
					isTail := false
					if pc2+1 < len(code) {
						nextInst := code[pc2+1]
						nextOp := vm.DecodeOp(nextInst)
						nextA := vm.DecodeA(nextInst)
						if nextOp == vm.OP_RETURN && nextA == globalA {
							isTail = true
						}
					}
					candidate := &inlineCandidate{
						getglobalPC: pc,
						callPC:      pc2,
						fnReg:       globalA,
						nArgs:       b - 1,
						nResults:    c - 1,
						isSelfCall:  true,
						isTailCall:  isTail,
					}

					cg.traceSelfCallArgs(candidate, code, pc, pc2, globalA, b)

					// For tail self-calls with all args traced, mark arg setup PCs
					// as skippable. The emitSelfTailCall will emit direct register writes.
					if isTail {
						allArgTraced := true
						for i := 0; i < cg.proto.NumParams; i++ {
							if !candidate.argTraces[i].traced {
								allArgTraced = false
								break
							}
						}
						if allArgTraced {
							for _, trace := range candidate.argTraces {
								if trace.traced {
									cg.inlineArgSkipPCs[trace.setupPC] = true
									if trace.auxSetupPC >= 0 {
										cg.inlineArgSkipPCs[trace.auxSetupPC] = true
									}
								}
							}
						}
					}

					// For non-tail self-calls with all args traced, enable directArgs mode:
					// emitSelfCallFull will compute args directly into X19/X22 instead of
					// loading from the register window after window advance.
					// The MOVE/SUB arg setup instructions are NOT marked as dead to
					// preserve code alignment (Apple Silicon is sensitive to alignment).
					// The stores they emit are redundant but harmless (store buffer absorbs them).
					if !isTail {
						allArgTraced := true
						for i := 0; i < cg.proto.NumParams; i++ {
							if !candidate.argTraces[i].traced {
								allArgTraced = false
								break
							}
						}
						if allArgTraced {
							candidate.directArgs = true
						}
					}

					cg.inlineCandidates[pc2] = candidate
					cg.inlineSkipPCs[pc] = true
					cg.hasSelfCalls = true
					break
				}

				// Check for inlineable global function.
				if cg.globals != nil {
					candidate := cg.buildGlobalCallCandidate(name, pc, pc2, globalA, b, c)
					if candidate != nil {
						cg.inlineCandidates[pc2] = candidate
						cg.inlineSkipPCs[pc] = true
					}
				}
				break
			}
			// If the register is overwritten by another instruction, stop
			if vm.DecodeA(inst2) == globalA {
				break
			}
		}
	}

	// Second pass: detect skipArgSave for non-tail self-calls.
	cg.analyzeSkipArgSave(code)

	// Third pass: detect skipTopUpdate for C=0 non-tail self-calls.
	// If the next self-call after this one is a tail call (which loads args by
	// position, not from ctx.Top), the ctx.Top write is dead and can be skipped.
	for _, cand := range cg.inlineCandidates {
		if !cand.isSelfCall || cand.isTailCall || cand.nResults >= 0 {
			continue // only applies to C=0 non-tail self-calls
		}
		for scanPC := cand.callPC + 1; scanPC < len(code); scanPC++ {
			if nextCand, ok := cg.inlineCandidates[scanPC]; ok && nextCand.isSelfCall {
				// The next self-call (tail or non-tail) will set its own args.
				// Tail calls load from fixed positions, non-tail calls advance
				// X26 and load from the new window. Neither reads ctx.Top.
				cand.skipTopUpdate = true
				break
			}
			inst := code[scanPC]
			op := vm.DecodeOp(inst)
			if op == vm.OP_RETURN {
				// RETURN doesn't read ctx.Top.
				cand.skipTopUpdate = true
				break
			}
			// Any other non-skipped instruction might depend on ctx.Top.
			if !cg.inlineSkipPCs[scanPC] && !cg.inlineArgSkipPCs[scanPC] {
				break
			}
		}
	}
}

// traceSelfCallArgs traces argument sources for a self-recursive call by scanning
// backward from the CALL instruction to find MOVE/LOADINT/SUB/ADD that set up
// each argument register. This detects when pinned registers already hold the
// correct values, enabling direct register writes instead of memory round-trips.
func (cg *Codegen) traceSelfCallArgs(candidate *inlineCandidate, code []uint32, pc, pc2, globalA, b int) {
	numParams := cg.proto.NumParams
	nArgs := b - 1
	if nArgs < 0 {
		nArgs = numParams // B=0: variable args, assume NumParams
	}
	candidate.argTraces = make([]inlineArgTrace, numParams)
	for i := 0; i < numParams && i < nArgs; i++ {
		argReg := globalA + 1 + i
		// Scan backward from CALL to find MOVE/LOADINT/SUB that set argReg.
		for scanPC := pc2 - 1; scanPC > pc && scanPC >= pc2-10; scanPC-- {
			si := code[scanPC]
			sop := vm.DecodeOp(si)
			sa := vm.DecodeA(si)
			if sa != argReg {
				continue
			}
			if sop == vm.OP_MOVE {
				srcReg := vm.DecodeB(si)
				candidate.argTraces[i] = inlineArgTrace{
					fromReg: srcReg, traced: true, setupPC: scanPC,
				}
			} else if sop == vm.OP_LOADINT {
				sbxVal := vm.DecodesBx(si)
				candidate.argTraces[i] = inlineArgTrace{
					fromConst: int64(sbxVal), isConst: true, traced: true, setupPC: scanPC,
				}
			} else if sop == vm.OP_SUB || sop == vm.OP_ADD {
				// Trace SUB/ADD with constant operand (e.g., m-1).
				sb := vm.DecodeB(si)
				sc := vm.DecodeC(si)
				opName := "SUB"
				if sop == vm.OP_ADD {
					opName = "ADD"
				}
				constVal := int64(-1)
				auxPC := -1
				if vm.IsRK(sc) {
					constIdx := vm.RKToConstIdx(sc)
					if constIdx < len(cg.proto.Constants) {
						cv := cg.proto.Constants[constIdx]
						if cv.IsInt() && cv.Int() >= 0 && cv.Int() <= 4095 {
							constVal = cv.Int()
						}
					}
				} else {
					// C is a register — check if set by a recent LOADINT.
					for scanPC2 := scanPC - 1; scanPC2 >= 0 && scanPC2 >= scanPC-5; scanPC2-- {
						si2 := code[scanPC2]
						if vm.DecodeOp(si2) == vm.OP_LOADINT && vm.DecodeA(si2) == sc {
							v := int64(vm.DecodesBx(si2))
							if v >= 0 && v <= 4095 {
								constVal = v
								auxPC = scanPC2
							}
							break
						}
					}
				}
				if constVal >= 0 {
					candidate.argTraces[i] = inlineArgTrace{
						arithSrc: sb, arithOp: opName,
						fromConst: constVal,
						traced: true, setupPC: scanPC,
						auxSetupPC: auxPC,
					}
				}
			}
			break
		}
	}
}

// buildGlobalCallCandidate checks whether a global function call can be inlined
// and builds the candidate with traced argument sources and result destination.
// Returns nil if the function is not inlineable.
func (cg *Codegen) buildGlobalCallCandidate(name string, pc, pc2, globalA, b, c int) *inlineCandidate {
	code := cg.proto.Code
	fnVal, ok := cg.globals[name]
	if !ok || !fnVal.IsFunction() {
		return nil
	}
	vcl, _ := fnVal.Ptr().(*vm.Closure)
	if vcl == nil {
		return nil
	}
	if !cg.isInlineable(vcl.Proto) {
		return nil
	}
	nArgs := b - 1
	candidate := &inlineCandidate{
		getglobalPC: pc,
		callPC:      pc2,
		callee:      vcl.Proto,
		fnReg:       globalA,
		nArgs:       nArgs,
		nResults:    c - 1,
	}

	// Trace argument sources: scan backward from CALL to find
	// MOVE/LOADINT that set up each arg register R(fnReg+1+i).
	candidate.argTraces = make([]inlineArgTrace, vcl.Proto.NumParams)
	for i := 0; i < vcl.Proto.NumParams && i < nArgs; i++ {
		argReg := globalA + 1 + i
		for scanPC := pc2 - 1; scanPC > pc && scanPC >= pc2-10; scanPC-- {
			si := code[scanPC]
			sop := vm.DecodeOp(si)
			sa := vm.DecodeA(si)
			if sa != argReg {
				continue
			}
			if sop == vm.OP_MOVE {
				srcReg := vm.DecodeB(si)
				candidate.argTraces[i] = inlineArgTrace{
					fromReg: srcReg, traced: true, setupPC: scanPC,
				}
			} else if sop == vm.OP_LOADINT {
				sbx := vm.DecodesBx(si)
				candidate.argTraces[i] = inlineArgTrace{
					fromConst: int64(sbx), isConst: true, traced: true, setupPC: scanPC,
				}
			}
			break
		}
	}

	// Mark traced arg setup PCs as skippable
	for _, trace := range candidate.argTraces {
		if trace.traced {
			cg.inlineArgSkipPCs[trace.setupPC] = true
		}
	}

	// Trace result destination: if the next instruction is
	// MOVE R(dest) R(fnReg), write result directly to R(dest).
	candidate.resultDest = -1
	candidate.resultMovePC = -1
	if pc2+1 < len(code) {
		nextInst := code[pc2+1]
		if vm.DecodeOp(nextInst) == vm.OP_MOVE && vm.DecodeB(nextInst) == globalA {
			candidate.resultDest = vm.DecodeA(nextInst)
			candidate.resultMovePC = pc2 + 1
			cg.inlineArgSkipPCs[pc2+1] = true
		}
	}

	return candidate
}

// analyzeSkipArgSave performs a second pass over inline candidates to detect
// when pinned register saves (X19/X22) can be skipped for non-tail self-calls.
// For each non-tail self-call, it scans forward to see if any instruction between
// it and the next self-call (or RETURN) reads from pinned registers R(0)/R(1).
// If no pinned register is read, the save can be skipped.
func (cg *Codegen) analyzeSkipArgSave(code []uint32) {
	for _, cand := range cg.inlineCandidates {
		if !cand.isSelfCall || cand.isTailCall {
			continue
		}
		canSkip := true
		for scanPC := cand.callPC + 1; scanPC < len(code); scanPC++ {
			// If we reach another self-call, it will overwrite X19/X22. Safe.
			if nextCand, ok := cg.inlineCandidates[scanPC]; ok && nextCand.isSelfCall {
				break
			}
			inst := code[scanPC]
			op := vm.DecodeOp(inst)
			// If we reach RETURN, pinned regs aren't needed. Safe.
			if op == vm.OP_RETURN {
				break
			}
			// Skip GETGLOBAL instructions that are part of inline patterns.
			if cg.inlineSkipPCs[scanPC] {
				continue
			}
			// Skip arg setup instructions.
			if cg.inlineArgSkipPCs[scanPC] {
				continue
			}
			// Check if this instruction reads R(0) or R(1) as source operands.
			if cg.instructionReadsPinned(inst, op) {
				canSkip = false
				break
			}
		}
		cand.skipArgSave = canSkip
	}
}

// isInlineable returns true if a function body is simple enough to inline.
func (cg *Codegen) isInlineable(proto *vm.FuncProto) bool {
	if len(proto.Upvalues) > 0 || proto.IsVarArg || len(proto.Protos) > 0 {
		return false
	}
	if len(proto.Code) > 20 {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD,
			vm.OP_MOVE, vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL,
			vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_JMP,
			vm.OP_RETURN, vm.OP_UNM, vm.OP_NOT:
			continue
		default:
			return false
		}
	}
	return true
}
