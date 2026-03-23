//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

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
