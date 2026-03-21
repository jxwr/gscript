//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

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

					// Trace self-call argument sources to detect when pinned
					// registers already hold the correct arg values.
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

					// For tail self-calls with all args traced, mark arg setup PCs
					// as skippable. The emitSelfTailCall will emit direct register writes.
					if isTail {
						allArgTraced := true
						for i := 0; i < numParams; i++ {
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
						for i := 0; i < numParams; i++ {
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
					fnVal, ok := cg.globals[name]
					if !ok || !fnVal.IsFunction() {
						break
					}
					vcl, _ := fnVal.Ptr().(*vm.Closure)
					if vcl == nil {
						break
					}
					if !cg.isInlineable(vcl.Proto) {
						break
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

					cg.inlineCandidates[pc2] = candidate
					cg.inlineSkipPCs[pc] = true
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
	// For each non-tail self-call, scan forward to see if any instruction between
	// it and the next self-call (or RETURN) reads from pinned registers R(0)/R(1).
	// If no pinned register is read, we can skip saving X19/X22.
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

// hasCrossCallExits checks if the function has non-self, non-inline CALL instructions
// that would become call-exits. These are incompatible with the self-call ARM64 stack
// mechanism: call-exit jumps to epilogue which assumes a clean stack, but self-calls
// push frames on the ARM64 stack that would be orphaned.
func (cg *Codegen) hasCrossCallExits() bool {
	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op != vm.OP_CALL {
			continue
		}
		// Skip self-call and inline candidates (they don't go through call-exit).
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		// This CALL instruction will become a call-exit.
		return true
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
		if op != vm.OP_CALL {
			continue
		}
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		if _, ok := cg.crossCalls[pc]; ok {
			continue
		}
		return true
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

// emitInlineCall emits inline native code for a function call, replacing
// GETGLOBAL + CALL with the callee's body using register remapping.
func (cg *Codegen) emitInlineCall(pc int, candidate *inlineCandidate) error {
	callee := candidate.callee
	fnReg := candidate.fnReg

	// Build register mapping: callee register → caller register
	// Callee params map to the caller's argument registers
	// Callee locals map to scratch space after the args
	regMap := make(map[int]int)
	for i := 0; i < callee.NumParams; i++ {
		regMap[i] = fnReg + 1 + i
	}
	scratchBase := fnReg + 1 + candidate.nArgs
	for i := callee.NumParams; i < callee.MaxStack; i++ {
		regMap[i] = scratchBase + (i - callee.NumParams)
	}

	// Use pre-computed argument traces from the analysis phase.
	// Traced MOVE args update regMap to point to the actual source register.
	// Traced LOADINT args are recorded as known constants.
	constArgs := make(map[int]int64)
	for i, trace := range candidate.argTraces {
		if !trace.traced {
			continue
		}
		if trace.isConst {
			constArgs[i] = trace.fromConst
		} else {
			// MOVE traced: callee param i → actual source register in caller
			regMap[i] = trace.fromReg
		}
	}

	// Find the return register and map it to the result register.
	// If the result destination was traced (CALL result MOVE'd elsewhere),
	// write directly to the final destination.
	resultReg := fnReg
	if candidate.resultDest >= 0 {
		resultReg = candidate.resultDest
	}
	for _, calleeInst := range callee.Code {
		if vm.DecodeOp(calleeInst) == vm.OP_RETURN {
			retA := vm.DecodeA(calleeInst)
			if candidate.nResults > 0 {
				regMap[retA] = resultReg
			}
			break
		}
	}

	exitLabel := fmt.Sprintf("inline_exit_%d", pc)

	// Emit callee instructions with register remapping
	for _, calleeInst := range callee.Code {
		calleeOp := vm.DecodeOp(calleeInst)
		if calleeOp == vm.OP_RETURN {
			// Handle result placement
			retA := vm.DecodeA(calleeInst)
			retB := vm.DecodeB(calleeInst)
			mappedRetA := regMap[retA]
			if retB == 1 {
				// Return nothing — set result to nil
				if candidate.nResults > 0 {
					cg.storeNilValue(resultReg)
				}
			} else if mappedRetA != resultReg && candidate.nResults > 0 {
				// Copy result to resultReg (respecting pinned registers)
				srcArm, srcPinned := cg.pinnedRegs[mappedRetA]
				dstArm, dstPinned := cg.pinnedRegs[resultReg]
				if srcPinned && dstPinned {
					if srcArm != dstArm {
						cg.asm.MOVreg(dstArm, srcArm)
					}
				} else if srcPinned {
					cg.storeIntValue(resultReg, srcArm)
				} else if dstPinned {
					cg.asm.LDR(dstArm, regRegs, regValOffset(mappedRetA))
					EmitUnboxInt(cg.asm, dstArm, dstArm)
				} else {
					cg.copyValue(resultReg, mappedRetA)
				}
			}
			break
		}

		switch calleeOp {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL:
			a := vm.DecodeA(calleeInst)
			b := vm.DecodeB(calleeInst)
			c := vm.DecodeC(calleeInst)
			mappedA := regMap[a]

			// Remap B and C (register references only, not RK constants)
			mappedB := b
			if !vm.IsRK(b) {
				mappedB = regMap[b]
			}
			mappedC := c
			if !vm.IsRK(c) {
				mappedC = regMap[c]
			}

			// Determine if each operand is a known constant.
			// Sources: (1) RK constant from callee pool, (2) traced LOADINT arg
			bIsConst := vm.IsRK(b)
			cIsConst := vm.IsRK(c)
			bConstVal := int64(0)
			cConstVal := int64(0)
			if bIsConst {
				constIdx := vm.RKToConstIdx(b)
				if constIdx < len(callee.Constants) {
					bConstVal = callee.Constants[constIdx].Int()
				}
			} else if cv, ok := constArgs[b]; ok {
				bIsConst = true
				bConstVal = cv
			}
			if cIsConst {
				constIdx := vm.RKToConstIdx(c)
				if constIdx < len(callee.Constants) {
					cConstVal = callee.Constants[constIdx].Int()
				}
			} else if cv, ok := constArgs[c]; ok {
				cIsConst = true
				cConstVal = cv
			}

			// Check pinned status and known-int for each operand
			aArm, aPinned := cg.pinnedRegs[mappedA]
			var bArm, cArm Reg
			var bPinned, cPinned bool
			bKnownInt := bIsConst
			cKnownInt := cIsConst
			if !bIsConst {
				bArm, bPinned = cg.pinnedRegs[mappedB]
				bKnownInt = bPinned || (cg.knownInt != nil && pc < len(cg.knownInt) && regSetHas(cg.knownInt[pc], mappedB))
			}
			if !cIsConst {
				cArm, cPinned = cg.pinnedRegs[mappedC]
				cKnownInt = cPinned || (cg.knownInt != nil && pc < len(cg.knownInt) && regSetHas(cg.knownInt[pc], mappedC))
			}

			// Type guards only for non-known-int, non-pinned, non-constant operands
			if !bKnownInt {
				cg.loadRegTyp(X0, mappedB)
				cg.emitCmpTag(X0, NB_TagIntShr48)
				cg.asm.BCond(CondNE, exitLabel)
			}
			if !cKnownInt {
				cg.loadRegTyp(X0, mappedC)
				cg.emitCmpTag(X0, NB_TagIntShr48)
				cg.asm.BCond(CondNE, exitLabel)
			}

			// Fast path: destination pinned and operands are pinned or constant
			if aPinned && (bPinned || bIsConst) && (cPinned || cIsConst) {
				switch {
				case !bIsConst && !cIsConst:
					// Both in ARM registers
					switch calleeOp {
					case vm.OP_ADD:
						cg.asm.ADDreg(aArm, bArm, cArm)
					case vm.OP_SUB:
						cg.asm.SUBreg(aArm, bArm, cArm)
					case vm.OP_MUL:
						cg.asm.MUL(aArm, bArm, cArm)
					}
				case bIsConst && !cIsConst:
					if calleeOp == vm.OP_ADD && bConstVal >= 0 && bConstVal <= 4095 {
						cg.asm.ADDimm(aArm, cArm, uint16(bConstVal))
					} else {
						cg.asm.LoadImm64(X0, bConstVal)
						switch calleeOp {
						case vm.OP_ADD:
							cg.asm.ADDreg(aArm, X0, cArm)
						case vm.OP_SUB:
							cg.asm.SUBreg(aArm, X0, cArm)
						case vm.OP_MUL:
							cg.asm.MUL(aArm, X0, cArm)
						}
					}
				case !bIsConst && cIsConst:
					if (calleeOp == vm.OP_ADD || calleeOp == vm.OP_SUB) && cConstVal >= 0 && cConstVal <= 4095 {
						switch calleeOp {
						case vm.OP_ADD:
							cg.asm.ADDimm(aArm, bArm, uint16(cConstVal))
						case vm.OP_SUB:
							cg.asm.SUBimm(aArm, bArm, uint16(cConstVal))
						}
					} else {
						cg.asm.LoadImm64(X1, cConstVal)
						switch calleeOp {
						case vm.OP_ADD:
							cg.asm.ADDreg(aArm, bArm, X1)
						case vm.OP_SUB:
							cg.asm.SUBreg(aArm, bArm, X1)
						case vm.OP_MUL:
							cg.asm.MUL(aArm, bArm, X1)
						}
					}
				default:
					// Both constants — fold at compile time
					var result int64
					switch calleeOp {
					case vm.OP_ADD:
						result = bConstVal + cConstVal
					case vm.OP_SUB:
						result = bConstVal - cConstVal
					case vm.OP_MUL:
						result = bConstVal * cConstVal
					}
					cg.asm.LoadImm64(aArm, result)
				}
			} else {
				// Fallback: load operands into scratch registers, compute, store result
				if bIsConst {
					cg.asm.LoadImm64(X0, bConstVal)
				} else if bPinned {
					cg.asm.MOVreg(X0, bArm)
				} else {
					cg.loadRegIval(X0, mappedB)
				}

				if cIsConst {
					cg.asm.LoadImm64(X1, cConstVal)
				} else if cPinned {
					cg.asm.MOVreg(X1, cArm)
				} else {
					cg.loadRegIval(X1, mappedC)
				}

				switch calleeOp {
				case vm.OP_ADD:
					cg.asm.ADDreg(X0, X0, X1)
				case vm.OP_SUB:
					cg.asm.SUBreg(X0, X0, X1)
				case vm.OP_MUL:
					cg.asm.MUL(X0, X0, X1)
				}

				if aPinned {
					cg.asm.MOVreg(aArm, X0)
				} else {
					cg.storeIntValue(mappedA, X0)
				}
			}

		case vm.OP_MOVE:
			a := regMap[vm.DecodeA(calleeInst)]
			b := regMap[vm.DecodeB(calleeInst)]
			srcArm, srcPinned := cg.pinnedRegs[b]
			dstArm, dstPinned := cg.pinnedRegs[a]
			if srcPinned && dstPinned {
				if srcArm != dstArm {
					cg.asm.MOVreg(dstArm, srcArm)
				}
			} else if srcPinned {
				cg.storeIntValue(a, srcArm)
			} else if dstPinned {
				cg.asm.LDR(dstArm, regRegs, regValOffset(b))
				EmitUnboxInt(cg.asm, dstArm, dstArm)
			} else {
				cg.copyValue(a, b)
			}

		case vm.OP_LOADINT:
			a := regMap[vm.DecodeA(calleeInst)]
			sbx := vm.DecodesBx(calleeInst)
			if armReg, pinned := cg.pinnedRegs[a]; pinned {
				cg.asm.LoadImm64(armReg, int64(sbx))
			} else {
				cg.asm.LoadImm64(X0, int64(sbx))
				cg.storeIntValue(a, X0)
			}

		case vm.OP_LOADNIL:
			a := vm.DecodeA(calleeInst)
			b := vm.DecodeB(calleeInst)
			for i := a; i <= a+b; i++ {
				cg.storeNilValue(regMap[i])
			}

		default:
			// Unsupported opcode in inline — fall back to side exit
			cg.spillPinnedRegs()
			cg.asm.LoadImm64(X1, int64(candidate.getglobalPC))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
			return nil
		}
	}

	// afterLabel: hot path falls through here (no need for a skip branch).
	// The exit label is deferred to the cold section.
	cg.deferCold(exitLabel, func() {
		cg.spillPinnedRegs()
		cg.asm.LoadImm64(X1, int64(candidate.getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
	})

	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Call-exit analysis and resume dispatch
// ──────────────────────────────────────────────────────────────────────────────

// analyzeCallExitPCs collects bytecode PCs that will use call-exit (ExitCode=2).
// These are unsupported opcodes (GETGLOBAL, SETGLOBAL, CALL) that the executor
// can handle and then re-enter JIT at the next instruction.
//
// Optimization: A call-exit is only worthwhile if the successor instruction
// (pc+1) can do useful JIT work — i.e., it is supported, an inline candidate,
// or itself a call-exit. If the successor would immediately cause a permanent
// side-exit, we demote the current instruction to permanent side-exit too,
// avoiding a wasted exit/re-enter cycle.
// Processing in reverse order handles cascading: if pc+2 is unsupported, pc+1
// gets demoted, and then pc gets demoted as well.

// emitSelfCallReturn handles RETURN for functions with self-recursive calls.
// If X25 > 0 (we're inside a self-call), return via RET with result in X0.
// If X25 == 0 (outermost call), write to JITContext and go to epilogue.
func (cg *Codegen) emitSelfCallReturn(pc, aReg, b int) error {
	a := cg.asm

	if b == 0 {
		// Variable return count (RETURN A B=0).
		// In self-call functions, treat as a single-value return at all depths.
		// The JIT doesn't maintain vm.top, so we can't side-exit to the interpreter
		// for variable returns — it would compute a wrong return count.
		// Self-call functions only return single values through the JIT path anyway.
		cg.loadRegIval(X0, aReg)
		outerVarLabel := fmt.Sprintf("outermost_varret_%d", pc)
		a.CBZ(regSelfDepth, outerVarLabel)
		a.RET() // nested self-call: return single value in X0

		// Outermost: return single value via JITContext.
		// Write type tag for the return register so the executor reads a valid Value.
		a.Label(outerVarLabel)
		a.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
		a.LoadImm64(X1, int64(aReg))
		a.STR(X1, regCtx, ctxOffRetBase)
		a.LoadImm64(X1, 1) // 1 return value
		a.STR(X1, regCtx, ctxOffRetCount)
		a.LoadImm64(X0, 0) // ExitCode = 0 (normal return)
		a.B("epilogue")
		return nil
	}

	nret := 0
	if b > 1 {
		nret = b - 1
		// Load first return value into X0.
		cg.loadRegIval(X0, aReg)
	} else {
		// Return nothing — X0 = 0.
		a.LoadImm64(X0, 0)
	}

	// Check depth: if > 0, this is a nested self-call return.
	outerLabel := fmt.Sprintf("outermost_ret_%d", pc)
	a.CBZ(regSelfDepth, outerLabel)
	// Self-call return: X0 = result, just RET back to BL caller.
	a.RET()

	// Outermost return: write type tag for the return register so the executor
	// reads a valid Value from the register array.
	a.Label(outerLabel)
	if nret > 0 {
		a.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
	}
	a.LoadImm64(X1, int64(aReg))
	a.STR(X1, regCtx, ctxOffRetBase)
	a.LoadImm64(X1, int64(nret))
	a.STR(X1, regCtx, ctxOffRetCount)
	a.LoadImm64(X0, 0)
	a.B("epilogue")
	return nil
}

// Maximum self-recursion depth before falling back to interpreter.
const maxSelfRecursionDepth = 200

// emitSelfCall emits native ARM64 code for a self-recursive function call.
// For 1-parameter functions: saves LR (X30) + X19 in a 16-byte frame.
// For 2+-parameter functions: saves LR (X30) + X19 + X22 in a 32-byte frame.
// regRegs (X26) is restored by subtraction after the call.
// The depth counter (X25) is managed via increment/decrement.
//
// Tail call optimization: if isTailCall is true, the CALL is immediately
// followed by RETURN. Instead of BL + save/restore + result handling, we
// load the new arguments into the pinned registers and B (jump) directly
// to self_call_entry. This reuses the caller's stack frame and depth level.
func (cg *Codegen) emitSelfCall(pc int, candidate *inlineCandidate) error {
	if candidate.isTailCall {
		return cg.emitSelfTailCall(pc, candidate)
	}
	return cg.emitSelfCallFull(pc, candidate)
}

// emitSelfTailCall emits a tail-call-optimized self-recursive call.
// Instead of the full BL/save/restore sequence (~15 instructions), this emits
// direct register writes + unconditional branch to self_call_entry.
// When arg traces are available, arguments are written directly to pinned
// registers without intermediate memory stores/loads.
func (cg *Codegen) emitSelfTailCall(pc int, candidate *inlineCandidate) error {
	a := cg.asm
	fnReg := candidate.fnReg
	numParams := cg.proto.NumParams

	// Check if all args are traced (needed for direct register passing).
	allTraced := len(candidate.argTraces) >= numParams
	if allTraced {
		for i := 0; i < numParams; i++ {
			if !candidate.argTraces[i].traced {
				allTraced = false
				break
			}
		}
	}

	if !allTraced {
		// Fallback: load from memory (NaN-boxed → unbox int).
		a.LDR(regSelfArg, regRegs, regValOffset(fnReg+1))
		EmitUnboxInt(a, regSelfArg, regSelfArg)
		if numParams > 1 {
			a.LDR(regSelfArg2, regRegs, regValOffset(fnReg+2))
			EmitUnboxInt(a, regSelfArg2, regSelfArg2)
		}
		a.B("self_call_entry")
		return nil
	}

	// Direct register passing: write args directly to X19/X22 and jump.
	// The arg setup instructions (SUB/LOADINT/MOVE) were skipped, so we
	// emit the equivalent operations targeting pinned registers directly.
	cg.emitDirectArgs(candidate, numParams)

	a.B("self_call_entry")
	return nil
}

// emitDirectArgs computes call arguments directly into pinned registers (X19/X22)
// using traced argument sources, avoiding the store→window advance→load roundtrip.
// Used by both emitSelfTailCall and emitSelfCallFull (directArgs mode).

// emitDirectArgs computes call arguments directly into pinned registers (X19/X22)
// using traced argument sources, avoiding the store→window advance→load roundtrip.
// Used by both emitSelfTailCall and emitSelfCallFull (directArgs mode).
func (cg *Codegen) emitDirectArgs(candidate *inlineCandidate, numParams int) {
	a := cg.asm
	pinnedDst := [2]Reg{regSelfArg, regSelfArg2}

	// Check for dependency: does writing arg0 clobber a register that arg1 reads?
	emitOrder := [2]int{0, 1}
	if numParams == 2 {
		t0 := candidate.argTraces[0]
		t1 := candidate.argTraces[1]
		// arg0 writes to X19. Check if arg1 reads from X19.
		if t0.arithOp != "" || !t0.isConst {
			readsSrc := -1
			if t1.arithOp != "" {
				readsSrc = t1.arithSrc
			} else if !t1.isConst {
				readsSrc = t1.fromReg
			}
			if readsSrc >= 0 {
				if srcArm, ok := cg.pinnedRegs[readsSrc]; ok && srcArm == pinnedDst[0] {
					emitOrder = [2]int{1, 0} // emit arg1 first
				}
			}
		}
	}

	for idx := 0; idx < numParams; idx++ {
		i := emitOrder[idx]
		t := candidate.argTraces[i]
		dst := pinnedDst[i]

		if t.isConst && t.arithOp == "" {
			// Constant: MOVimm directly to pinned register.
			a.LoadImm64(dst, t.fromConst)
		} else if t.arithOp != "" {
			// SUB/ADD with const: emit directly to pinned register.
			srcArm, srcPinned := cg.pinnedRegs[t.arithSrc]
			src := X0
			if srcPinned {
				src = srcArm
			} else {
				a.LDR(X0, regRegs, regValOffset(t.arithSrc))
				EmitUnboxInt(a, X0, X0)
			}
			switch t.arithOp {
			case "SUB":
				a.SUBimm(dst, src, uint16(t.fromConst))
			case "ADD":
				a.ADDimm(dst, src, uint16(t.fromConst))
			}
		} else {
			// MOVE: copy from source register.
			srcArm, srcPinned := cg.pinnedRegs[t.fromReg]
			if srcPinned {
				if srcArm != dst {
					a.MOVreg(dst, srcArm)
				}
			} else {
				a.LDR(dst, regRegs, regValOffset(t.fromReg))
				EmitUnboxInt(a, dst, dst)
			}
		}
	}
}

// emitDirectArgLoad replaces a single LDR (load arg from register window) with
// a single equivalent instruction computed from the traced arg source.
// Emits exactly 1 instruction to maintain code alignment.
func (cg *Codegen) emitDirectArgLoad(t inlineArgTrace, dst Reg) {
	a := cg.asm
	if t.arithOp != "" {
		// SUB/ADD with const: compute directly into pinned register.
		srcArm, srcPinned := cg.pinnedRegs[t.arithSrc]
		src := X0
		if srcPinned {
			src = srcArm
		} else {
			// Source not pinned — must load from memory. Load NaN-boxed + unbox.
			a.LDR(dst, regRegs, regValOffset(t.arithSrc))
			EmitUnboxInt(a, dst, dst)
			return
		}
		switch t.arithOp {
		case "SUB":
			a.SUBimm(dst, src, uint16(t.fromConst))
		case "ADD":
			a.ADDimm(dst, src, uint16(t.fromConst))
		}
	} else if t.isConst {
		// Constant: load immediate. May be >1 instruction for large values.
		a.LoadImm64(dst, t.fromConst)
	} else {
		// MOVE: copy from source register.
		srcArm, srcPinned := cg.pinnedRegs[t.fromReg]
		if srcPinned && srcArm == dst {
			// Same register — emit NOP to maintain instruction count.
			a.NOP()
		} else if srcPinned {
			a.MOVreg(dst, srcArm)
		} else {
			a.LDR(dst, regRegs, regValOffset(t.fromReg))
			EmitUnboxInt(a, dst, dst)
		}
	}
}

// emitSelfCallFull emits the full non-tail self-recursive call sequence.
func (cg *Codegen) emitSelfCallFull(pc int, candidate *inlineCandidate) error {
	a := cg.asm
	fnReg := candidate.fnReg
	hasArg2 := cg.proto.NumParams > 1
	skipSave := candidate.skipArgSave

	overflowLabel := fmt.Sprintf("self_overflow_%d", pc)


	// Increment depth counter (before stack push, so overflow unwind is simpler).
	a.ADDimm(regSelfDepth, regSelfDepth, 1)

	// Check depth limit — side exit if too deep.
	a.CMPimm(regSelfDepth, maxSelfRecursionDepth)
	a.BCond(CondGE, overflowLabel)

	// Save callee-saved registers on the ARM64 stack.
	// SP must remain 16-byte aligned.
	// Must save BEFORE directArgs computation to preserve original X19/X22.
	if skipSave {
		// Lightweight frame: only save LR (X30). X19/X22 are not needed after
		// this call returns (next instruction is a tail self-call or RETURN).
		a.STRpre(X30, SP, -16) // SP -= 16; [SP] = X30
	} else if hasArg2 {
		// 32-byte frame: [SP] = {X30, X19}, [SP+16] = {X22, padding}
		a.STPpre(X30, regSelfArg, SP, -32) // SP -= 32; [SP] = {X30, X19}
		a.STR(regSelfArg2, SP, 16)         // [SP+16] = X22
	} else {
		// 16-byte frame: [SP] = {X30, X19}
		a.STPpre(X30, regSelfArg, SP, -16) // SP -= 16; [SP] = {X30, X19}
	}

	// Advance regRegs to callee's register window.
	// Callee's R(0) = Caller's R(fnReg+1).
	offset := (fnReg + 1) * ValueSize
	if offset <= 4095 {
		a.ADDimm(regRegs, regRegs, uint16(offset))
	} else {
		a.LoadImm64(X0, int64(offset))
		a.ADDreg(regRegs, regRegs, X0)
	}

	if candidate.directArgs {
		// directArgs mode: compute args directly into pinned registers from traced
		// sources instead of loading from the register window. Each LDR is replaced
		// by the equivalent computation (SUBimm/ADDimm/MOVreg/NOP) to maintain
		// the same instruction count for code alignment.
		cg.emitDirectArgLoad(candidate.argTraces[0], regSelfArg)
		if hasArg2 {
			cg.emitDirectArgLoad(candidate.argTraces[1], regSelfArg2)
		}
	} else {
		// Load callee's pinned parameters from the new register window.
		// The callee at self_call_entry skips these loads since args are already loaded.
		a.LDR(regSelfArg, regRegs, regValOffset(0))
		EmitUnboxInt(a, regSelfArg, regSelfArg)
		if hasArg2 {
			a.LDR(regSelfArg2, regRegs, regValOffset(1))
			EmitUnboxInt(a, regSelfArg2, regSelfArg2)
		}
	}

	// BL to self_call_entry (re-enters the function body).
	a.BL("self_call_entry")

	// After return: X0 = result (ival).
	// Restore callee-saved registers from stack.
	if skipSave {
		// Lightweight frame: only restore LR.
		a.LDRpost(X30, SP, 16)                // X30 = [SP]; SP += 16
	} else if hasArg2 {
		a.LDR(regSelfArg2, SP, 16)            // X22 = [SP+16]
		a.LDPpost(X30, regSelfArg, SP, 32)    // {X30, X19} = [SP]; SP += 32
	} else {
		a.LDPpost(X30, regSelfArg, SP, 16)    // {X30, X19} = [SP]; SP += 16
	}

	// Restore regRegs by subtracting the offset (avoids saving/restoring x26).
	if offset <= 4095 {
		a.SUBimm(regRegs, regRegs, uint16(offset))
	} else {
		// Use X1 as scratch since X0 holds the result.
		a.LoadImm64(X1, int64(offset))
		a.SUBreg(regRegs, regRegs, X1)
	}

	a.SUBimm(regSelfDepth, regSelfDepth, 1) // depth--

	// Store result to R(fnReg) in caller's register window as NaN-boxed IntValue.
	// X0 holds the raw int result from the callee.
	EmitBoxIntFast(a, X10, X0, regTagInt)
	a.STR(X10, regRegs, regValOffset(fnReg))

	// For variable-return self-calls (C=0), update ctx.Top so subsequent
	// B=0 CALL instructions know the arg range. Top = fnReg + 1.
	// Skip if the next consumer is a tail self-call (loads by position, not ctx.Top).
	if candidate.nResults < 0 && !candidate.skipTopUpdate {
		a.LoadImm64(X1, int64(fnReg+1))
		a.STR(X1, regCtx, ctxOffTop)
	}

	// Overflow handler deferred to cold section.
	getglobalPC := candidate.getglobalPC
	cg.deferCold(overflowLabel, func() {
		cg.asm.MOVreg(SP, X29)                       // unwind all self-call stack frames
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)      // restore original regRegs from context
		cg.asm.MOVimm16(regSelfDepth, 0)             // reset depth
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
	})

	return nil
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
