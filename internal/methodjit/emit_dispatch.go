//go:build darwin && arm64

// emit_dispatch.go contains the instruction emission dispatch (emitInstr),
// phi move resolution, control flow emission (jump, branch, return),
// and loop exit boxing for the Tier 2 Method JIT.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// gprPhiMove represents a single GPR phi move for dependency-aware ordering.
type gprPhiMove struct {
	srcArg    *Value
	phiInstr  *Instr
	dstPR     PhysReg
	srcGPR    jit.Reg // source GPR (if hasSrcGPR)
	dstGPR    jit.Reg // destination GPR
	hasSrcGPR bool    // source is in a GPR register
	hasDstGPR bool    // destination has a GPR allocation
	isRawInt  bool    // use raw-int transfer (loop header path)

	// Memory slot tracking for write-through conflict detection.
	// A phi move may write through its result to a memory slot, and another
	// phi move may read its source from a memory slot. If the write-through
	// slot equals another move's read slot, sequential emission clobbers
	// the source value.
	readsMemSlot  int // memory slot this move reads from (-1 = none)
	writesMemSlot int // memory slot this move writes through to (-1 = none)
}

// emitInstr emits ARM64 code for a single SSA instruction.
func (ec *emitContext) emitInstr(instr *Instr, block *Block) {
	codeStart := len(ec.asm.Code())
	// Clear per-instruction scratch FPR cache (D0-D3 are clobber-scoped per-instr).
	for k := range ec.scratchFPRCache {
		delete(ec.scratchFPRCache, k)
	}
	if !fieldOpPreservesSvalsCache(instr.Op) {
		ec.invalidateFieldSvalsCache()
	}
	// Reset fused compare state if this is NOT a Branch that should consume
	// the fused comparison. A fused comparison sets fusedActive, and only
	// the immediately-following Branch should see it. If any other instruction
	// runs between them (shouldn't happen per our pre-scan), clear it.
	if instr.Op != OpBranch {
		ec.fusedActive = false
	}
	switch instr.Op {
	// --- Constants ---
	case OpConstInt:
		ec.emitConstInt(instr)
	case OpConstNil:
		ec.emitConstNil(instr)
	case OpConstBool:
		ec.emitConstBool(instr)
	case OpConstFloat:
		ec.emitConstFloat(instr)
	case OpConstString:
		ec.emitOpExit(instr)

	// --- Slot access ---
	case OpLoadSlot:
		ec.emitLoadSlot(instr)
	case OpStoreSlot:
		ec.emitStoreSlot(instr)

	// --- Type-generic arithmetic (float-aware) ---
	case OpAdd:
		ec.emitFloatBinOp(instr, intBinAdd)
	case OpSub:
		ec.emitFloatBinOp(instr, intBinSub)
	case OpMul:
		ec.emitFloatBinOp(instr, intBinMul)
	case OpMod:
		ec.emitFloatBinOp(instr, intBinMod)

	// --- Type-specialized int arithmetic (raw int registers, no unbox/box) ---
	case OpAddInt:
		ec.emitRawIntBinOp(instr, intBinAdd)
	case OpSubInt:
		ec.emitRawIntBinOp(instr, intBinSub)
	case OpMulInt:
		ec.emitRawIntBinOp(instr, intBinMul)
	case OpModInt:
		ec.emitRawIntBinOp(instr, intBinMod)
	case OpDivIntExact:
		ec.emitRawIntExactDiv(instr)

	// --- Type-specialized float arithmetic ---
	case OpAddFloat:
		ec.emitTypedFloatBinOp(instr, intBinAdd)
	case OpSubFloat:
		ec.emitTypedFloatBinOp(instr, intBinSub)
	case OpMulFloat:
		ec.emitTypedFloatBinOp(instr, intBinMul)

	// --- Division (always returns float) ---
	case OpDiv, OpDivFloat:
		ec.emitDiv(instr)

	// --- Unary ---
	case OpUnm:
		ec.emitUnm(instr)
	case OpNegInt:
		ec.emitNegInt(instr)
	case OpNegFloat:
		ec.emitNegFloat(instr)
	case OpSqrt:
		ec.emitSqrtFloat(instr)
	case OpFMA:
		ec.emitFMA(instr)
	case OpMatrixGetF:
		ec.emitMatrixGetF(instr)
	case OpMatrixSetF:
		ec.emitMatrixSetF(instr)
	case OpMatrixFlat:
		ec.emitMatrixFlat(instr)
	case OpMatrixStride:
		ec.emitMatrixStride(instr)
	case OpMatrixLoadFAt:
		ec.emitMatrixLoadFAt(instr)
	case OpMatrixStoreFAt:
		ec.emitMatrixStoreFAt(instr)
	case OpMatrixRowPtr:
		ec.emitMatrixRowPtr(instr)
	case OpMatrixLoadFRow:
		ec.emitMatrixLoadFRow(instr)
	case OpMatrixStoreFRow:
		ec.emitMatrixStoreFRow(instr)
	case OpNot:
		ec.emitNot(instr)

	// --- Comparison ---
	case OpLt:
		ec.emitGenericNumericCmp(instr, jit.CondLT)
	case OpLe:
		ec.emitGenericNumericCmp(instr, jit.CondLE)
	case OpEq:
		ec.emitGenericNumericCmp(instr, jit.CondEQ)
	case OpLtInt:
		ec.emitIntCmp(instr, jit.CondLT)
	case OpLeInt:
		ec.emitIntCmp(instr, jit.CondLE)
	case OpEqInt:
		ec.emitIntCmp(instr, jit.CondEQ)

	// --- Float comparison ---
	case OpLtFloat:
		ec.emitFloatCmp(instr, jit.CondLT)
	case OpLeFloat:
		ec.emitFloatCmp(instr, jit.CondLE)

	// --- Phi ---
	case OpPhi:
		// Phi resolution happens at block transitions (emitPhiMoves).

	// --- Control flow ---
	case OpJump:
		ec.emitJump(instr, block)
	case OpBranch:
		ec.emitBranch(instr, block)
	case OpReturn:
		ec.emitReturn(instr, block)

	case OpNop:
		// nothing

	// --- Call: native BLR with spill/reload, slow path falls to exit-resume ---
	case OpCall:
		ec.emitOpCall(instr)

	// --- Global-exit: load globals via VM and resume JIT ---
	case OpGetGlobal:
		ec.emitGetGlobalNative(instr)
	case OpSetGlobal:
		ec.emitSetGlobalNative(instr)

	// --- Table operations ---
	case OpNewTable:
		ec.emitNewTableExit(instr)
	case OpGetTable:
		ec.emitGetTableNative(instr)
	case OpSetTable:
		ec.emitSetTableNative(instr)
		// Dynamic key writes can add new string keys, changing table shape.
		ec.shapeVerified = make(map[int]uint32)
	case OpTableArrayHeader:
		ec.emitTableArrayHeader(instr)
	case OpTableArrayLen:
		ec.emitTableArrayLen(instr)
	case OpTableArrayData:
		ec.emitTableArrayData(instr)
	case OpTableArrayLoad:
		ec.emitTableArrayLoad(instr)
	case OpGetField:
		ec.emitGetField(instr)
	case OpGetFieldNumToFloat:
		ec.emitGetFieldNumToFloat(instr)
	case OpSetField:
		ec.emitSetField(instr)

	// --- Guards ---
	case OpGuardType:
		ec.emitGuardType(instr)
	case OpNumToFloat:
		ec.emitNumToFloat(instr)
	case OpGuardTruthy:
		ec.emitGuardTruthy(instr)

	// --- SetList: store values to consecutive temp slots, then op-exit ---
	case OpSetList:
		ec.emitSetListExit(instr)

	// --- Op-exit: OpSelf exits to Go and may modify table shapes ---
	case OpSelf:
		ec.emitOpExit(instr)
		ec.shapeVerified = make(map[int]uint32)
		ec.tableVerified = make(map[int]bool)
		ec.kindVerified = make(map[int]uint16)
		ec.keysDirtyWritten = make(map[int]bool)
		ec.dmVerified = make(map[int]bool)

	// --- Op-exit: unsupported ops exit to Go, execute there, resume JIT ---
	case OpConcat:
		ec.emitConcatExit(instr)

	case OpGetUpval, OpSetUpval,
		OpAppend,
		OpPow,
		OpClosure, OpClose,
		OpForPrep, OpForLoop,
		OpTForCall, OpTForLoop,
		OpVararg, OpTestSet,
		OpGo, OpMakeChan, OpSend, OpRecv,
		OpGuardNonNil:
		ec.emitOpExit(instr)
	case OpLen:
		ec.emitLenNative(instr)

	default:
		ec.asm.NOP() // truly unknown op placeholder
	}
	codeEnd := len(ec.asm.Code())
	if codeEnd > codeStart {
		pass := "normal"
		if ec.numericMode {
			pass = "numeric"
		}
		ec.instrCodeRanges = append(ec.instrCodeRanges, InstrCodeRange{
			InstrID:   instr.ID,
			BlockID:   block.ID,
			CodeStart: codeStart,
			CodeEnd:   codeEnd,
			Pass:      pass,
		})
	}
}

func fieldOpPreservesSvalsCache(op Op) bool {
	switch op {
	case OpGetField, OpGetFieldNumToFloat, OpSetField:
		return true
	default:
		return false
	}
}

// Constant, slot, arithmetic, comparison emission in emit_arith.go

// --- Phi ---

// emitPhiMoves emits copies for phi nodes when transitioning from 'from' to 'to'.
// For register-resident values, this is a register-to-register MOV (NaN-boxed).
// For memory-resident values, this is a memory-to-memory copy via scratch.
// Mixed: register-to-memory or memory-to-register via scratch X0.
//
// When the destination is a loop header, phi moves transfer raw int values
// directly (no boxing/unboxing), because the loop body operates in raw-int mode.
//
// NOTE: For phi destinations, we use the register allocation directly (not
// activeRegs) because the phi belongs to the TARGET block, not the current one.
func (ec *emitContext) emitPhiMoves(from *Block, to *Block) {
	predIdx := -1
	for i, p := range to.Preds {
		if p == from {
			predIdx = i
			break
		}
	}
	if predIdx < 0 {
		return
	}

	// Check if the destination is a loop header — use raw-int phi transfer.
	toIsLoopHeader := ec.loop != nil && ec.loop.loopHeaders[to.ID]

	// Pre-pass: emit FPR phi moves with dependency-aware ordering.
	// FPR phi moves are semantically parallel assignments. When two FPR
	// moves conflict (e.g., D4→D5 and D6→D4 where D4 is both a source
	// and a destination), sequential emission clobbers the source value.
	// We detect this and reorder, using D0 as scratch to break cycles.
	ec.emitFPRPhiMovesOrdered(to, predIdx)

	// Emit GPR phi moves with dependency-aware ordering.
	// GPR phi moves are semantically parallel assignments. When two GPR
	// moves conflict (e.g., X20→X21 and X21→X22 where X21 is both a source
	// and a destination), sequential emission clobbers the source value.
	// We detect this and reorder, using X0 as scratch to break cycles.
	ec.emitGPRPhiMovesOrdered(to, predIdx, toIsLoopHeader)
}

// emitGPRPhiMovesOrdered emits all GPR-targeted phi moves for the edge
// predIdx→to, ordered to avoid clobbering source values when there are
// register conflicts. Uses X0 as scratch to break cycles.
//
// Handles both raw-int loop header phis and standard NaN-boxed phis.
// The key invariant: all phi moves are semantically parallel, so we must
// not clobber a source register that another move still needs.
func (ec *emitContext) emitGPRPhiMovesOrdered(to *Block, predIdx int, toIsLoopHeader bool) {
	var moves []gprPhiMove

	for _, instr := range to.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if predIdx >= len(instr.Args) {
			continue
		}
		dstPR, dstHasReg := ec.alloc.ValueRegs[instr.ID]
		// Skip FPR phis — already handled by emitFPRPhiMovesOrdered.
		if dstHasReg && dstPR.IsFloat {
			continue
		}
		dstHasGPR := dstHasReg && !dstPR.IsFloat

		srcArg := instr.Args[predIdx]
		isRawInt := toIsLoopHeader && dstHasGPR && instr.Type == TypeInt

		m := gprPhiMove{
			srcArg:        srcArg,
			phiInstr:      instr,
			dstPR:         dstPR,
			hasDstGPR:     dstHasGPR,
			isRawInt:      isRawInt,
			readsMemSlot:  -1,
			writesMemSlot: -1,
		}

		if dstHasGPR {
			m.dstGPR = jit.Reg(dstPR.Reg)
		}

		// Determine source GPR for dependency analysis. Even NaN-boxed phi
		// moves can read a raw-int source GPR before boxing it through X0, so
		// raw and boxed moves both participate in register conflict ordering.
		srcHasReg := ec.hasReg(srcArg.ID)
		if isRawInt {
			if srcHasReg {
				m.srcGPR = ec.physReg(srcArg.ID)
				m.hasSrcGPR = true
			}
		} else {
			if srcHasReg {
				m.srcGPR = ec.physReg(srcArg.ID)
				m.hasSrcGPR = true
			}
			// Raw ints in GPRs are boxed through X0 during emission, but the
			// boxing still reads the original GPR. Treat that GPR as a real
			// source for parallel phi ordering, otherwise an earlier move can
			// clobber it before the boxing happens.
		}

		// Track memory slot reads: source loaded from memory when not in a GPR.
		// For raw-int path: !srcHasReg means load from memory.
		// For NaN-boxed path: !srcHasReg or (srcHasReg && rawInt) both use X0,
		// but only !srcHasReg actually reads from a memory slot.
		if !srcHasReg {
			if srcSlot, ok := ec.slotMap[srcArg.ID]; ok {
				m.readsMemSlot = srcSlot
			}
		}

		// Track memory slot writes: write-through to phi's destination slot.
		// Raw-int: writes if crossBlockLive && !loopExitBoxPhis.
		// NaN-boxed: writes if crossBlockLive || !hasDstGPR.
		if isRawInt {
			if ec.crossBlockLive[instr.ID] && !ec.loopExitBoxPhis[instr.ID] {
				if dstSlot, ok := ec.slotMap[instr.ID]; ok {
					m.writesMemSlot = dstSlot
				}
			}
		} else {
			if (ec.crossBlockLive[instr.ID] && !ec.loopExitStorePhis[instr.ID]) || !dstHasGPR {
				if dstSlot, ok := ec.slotMap[instr.ID]; ok {
					m.writesMemSlot = dstSlot
				}
			}
		}

		moves = append(moves, m)
	}

	if len(moves) <= 1 {
		for i := range moves {
			ec.emitSingleGPRPhiMove(&moves[i])
		}
		return
	}

	// Emit in dependency-aware order considering BOTH register and memory
	// conflicts. A move blocks another if:
	//   (a) Register conflict: move's dst GPR == another's src GPR
	//   (b) Memory conflict: move writes through to a memory slot that
	//       another move reads its source from
	//
	// The isBlocked helper checks both conflict types.
	isBlocked := func(i int, emitted []bool) bool {
		m := &moves[i]
		// Check register conflict: does m's dst GPR block another's source?
		if m.hasDstGPR {
			for j := range moves {
				if j == i || emitted[j] {
					continue
				}
				if moves[j].hasSrcGPR && moves[j].srcGPR == m.dstGPR {
					return true
				}
			}
		}
		// Check memory conflict: does m's write-through slot conflict with
		// another move's read slot?
		if m.writesMemSlot >= 0 {
			for j := range moves {
				if j == i || emitted[j] {
					continue
				}
				if moves[j].readsMemSlot == m.writesMemSlot {
					return true
				}
			}
		}
		return false
	}

	emitted := make([]bool, len(moves))
	totalEmitted := 0

	for totalEmitted < len(moves) {
		progress := false

		// Emit all moves that are not blocked by any other.
		for i := range moves {
			if emitted[i] {
				continue
			}

			if !isBlocked(i, emitted) {
				ec.emitSingleGPRPhiMove(&moves[i])
				emitted[i] = true
				totalEmitted++
				progress = true
			}
		}

		if totalEmitted >= len(moves) {
			break
		}

		if !progress {
			// All remaining moves form register cycles. Break one cycle
			// using X0 as scratch. Pick the first un-emitted move with a
			// register source, save its source GPR to X0, then emit the
			// register blocker carefully to avoid clobbering X0.
			for i := range moves {
				if emitted[i] || !moves[i].hasSrcGPR {
					continue
				}
				m := &moves[i]

				// Save m's source GPR to X0.
				ec.asm.MOVreg(jit.X0, m.srcGPR)

				// Find the register-blocker: the move whose dstGPR == m.srcGPR.
				blockerIdx := -1
				for j := range moves {
					if j == i || emitted[j] {
						continue
					}
					if moves[j].hasDstGPR && moves[j].dstGPR == m.srcGPR {
						blockerIdx = j
						break
					}
				}

				if blockerIdx >= 0 {
					// Emit ONLY the blocker's register transfer (not the full
					// emitSingleGPRPhiMove which uses X0 for write-through
					// and would clobber our saved value).
					b := &moves[blockerIdx]
					ec.emitGPRPhiRegTransferOnly(b)
					// Mark as emitted for register purposes; write-through
					// is deferred until after m is emitted.
				}

				// Emit m from X0 (its original source value).
				ec.emitGPRPhiMoveFromScratch(m)
				emitted[i] = true
				totalEmitted++

				// Now do the blocker's write-through if needed.
				if blockerIdx >= 0 {
					b := &moves[blockerIdx]
					ec.emitGPRPhiWriteThrough(b)
					emitted[blockerIdx] = true
					totalEmitted++
				}
				break
			}
		}
	}
}

// emitSingleGPRPhiMove emits a single GPR phi move using the standard paths.
// For raw-int loop header phis, delegates to emitPhiMoveRawInt.
// For NaN-boxed phis, performs boxing/loading and register/memory transfer.
func (ec *emitContext) emitSingleGPRPhiMove(m *gprPhiMove) {
	if m.isRawInt {
		ec.emitPhiMoveRawInt(m.srcArg, m.phiInstr, m.dstPR)
		return
	}

	// Standard NaN-boxed path.
	srcHasReg := ec.hasReg(m.srcArg.ID)

	var srcVal jit.Reg
	if srcHasReg && ec.rawIntRegs[m.srcArg.ID] {
		reg := ec.physReg(m.srcArg.ID)
		jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
		srcVal = jit.X0
	} else if srcHasReg {
		srcVal = ec.physReg(m.srcArg.ID)
	} else {
		ec.loadValue(jit.X0, m.srcArg.ID)
		srcVal = jit.X0
	}

	if m.hasDstGPR {
		dstReg := jit.Reg(m.dstPR.Reg)
		if srcVal != dstReg {
			ec.asm.MOVreg(dstReg, srcVal)
		}
	}

	if (ec.crossBlockLive[m.phiInstr.ID] && !ec.loopExitStorePhis[m.phiInstr.ID]) || !m.hasDstGPR {
		dstSlot, hasDst := ec.slotMap[m.phiInstr.ID]
		if hasDst {
			if m.hasDstGPR {
				ec.asm.STR(jit.Reg(m.dstPR.Reg), mRegRegs, slotOffset(dstSlot))
			} else if srcVal != jit.X0 {
				ec.asm.MOVreg(jit.X0, srcVal)
				ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
			} else {
				ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
			}
		}
	}
}

// emitGPRPhiMoveFromScratch emits a GPR phi move where the source value
// has already been saved to X0 (cycle-breaking scratch). This handles
// both raw-int and NaN-boxed paths.
func (ec *emitContext) emitGPRPhiMoveFromScratch(m *gprPhiMove) {
	if m.isRawInt {
		dstReg := jit.Reg(m.dstPR.Reg)
		// Source was in a GPR, now saved to X0.
		if ec.rawIntRegs[m.srcArg.ID] {
			// Was raw int: X0 has raw int bits, transfer directly.
			if dstReg != jit.X0 {
				ec.asm.MOVreg(dstReg, jit.X0)
			}
		} else {
			// Was NaN-boxed in GPR: X0 has NaN-boxed value, unbox.
			jit.EmitUnboxInt(ec.asm, dstReg, jit.X0)
		}
		// Write-through if needed.
		if ec.crossBlockLive[m.phiInstr.ID] && !ec.loopExitBoxPhis[m.phiInstr.ID] {
			dstSlot, ok := ec.slotMap[m.phiInstr.ID]
			if ok {
				jit.EmitBoxIntFast(ec.asm, jit.X0, dstReg, mRegTagInt)
				ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
			}
		}
		return
	}

	// NaN-boxed path: source value is in X0. For raw-int sources saved while
	// breaking a cycle, X0 holds raw bits and must be boxed before transfer.
	if ec.rawIntRegs[m.srcArg.ID] {
		jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	}
	if m.hasDstGPR {
		dstReg := jit.Reg(m.dstPR.Reg)
		ec.asm.MOVreg(dstReg, jit.X0)
	}

	if (ec.crossBlockLive[m.phiInstr.ID] && !ec.loopExitStorePhis[m.phiInstr.ID]) || !m.hasDstGPR {
		dstSlot, hasDst := ec.slotMap[m.phiInstr.ID]
		if hasDst {
			if m.hasDstGPR {
				ec.asm.STR(jit.Reg(m.dstPR.Reg), mRegRegs, slotOffset(dstSlot))
			} else {
				ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
			}
		}
	}
}

// emitGPRPhiRegTransferOnly emits ONLY the register-to-register part of a
// GPR phi move, WITHOUT the memory write-through. Used during cycle-breaking
// to avoid clobbering X0 (which holds a saved source value for another move).
func (ec *emitContext) emitGPRPhiRegTransferOnly(m *gprPhiMove) {
	if !m.hasDstGPR {
		return
	}
	dstReg := jit.Reg(m.dstPR.Reg)

	if m.isRawInt {
		srcHasReg := ec.hasReg(m.srcArg.ID)
		if srcHasReg && ec.rawIntRegs[m.srcArg.ID] {
			srcReg := ec.physReg(m.srcArg.ID)
			if srcReg != dstReg {
				ec.asm.MOVreg(dstReg, srcReg)
			}
		} else if srcHasReg {
			srcReg := ec.physReg(m.srcArg.ID)
			jit.EmitUnboxInt(ec.asm, dstReg, srcReg)
		}
		// Memory source case: cannot happen in a register cycle (hasSrcGPR
		// is required to be in a cycle), so skip.
		return
	}

	// NaN-boxed path: register transfer only.
	srcHasReg := ec.hasReg(m.srcArg.ID)
	if srcHasReg && ec.rawIntRegs[m.srcArg.ID] {
		// Raw int in register: box to dstReg (use dstReg as temp, skip X0).
		reg := ec.physReg(m.srcArg.ID)
		jit.EmitBoxIntFast(ec.asm, dstReg, reg, mRegTagInt)
	} else if srcHasReg {
		srcReg := ec.physReg(m.srcArg.ID)
		if srcReg != dstReg {
			ec.asm.MOVreg(dstReg, srcReg)
		}
	}
}

// emitGPRPhiWriteThrough emits ONLY the memory write-through part of a GPR
// phi move. The register must already hold the correct value. Used after
// cycle-breaking to complete the move without clobbering X0 during the
// critical cycle resolution window.
func (ec *emitContext) emitGPRPhiWriteThrough(m *gprPhiMove) {
	if m.isRawInt {
		if ec.crossBlockLive[m.phiInstr.ID] && !ec.loopExitBoxPhis[m.phiInstr.ID] {
			dstSlot, ok := ec.slotMap[m.phiInstr.ID]
			if ok && m.hasDstGPR {
				dstReg := jit.Reg(m.dstPR.Reg)
				jit.EmitBoxIntFast(ec.asm, jit.X0, dstReg, mRegTagInt)
				ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
			}
		}
		return
	}

	// NaN-boxed path.
	if (ec.crossBlockLive[m.phiInstr.ID] && !ec.loopExitStorePhis[m.phiInstr.ID]) || !m.hasDstGPR {
		dstSlot, hasDst := ec.slotMap[m.phiInstr.ID]
		if hasDst && m.hasDstGPR {
			ec.asm.STR(jit.Reg(m.dstPR.Reg), mRegRegs, slotOffset(dstSlot))
		}
	}
}

// emitFPRPhiMovesOrdered emits all FPR-targeted phi moves for the edge
// predIdx→to, ordered to avoid clobbering source values when there are
// register conflicts. Uses D0 as scratch to break cycles.
//
// Example conflict: phis v_zi (D6→D4) and v_zr (D4→D5). If emitted in
// order, the first move writes D4 which clobbers the source for the second.
// We detect this and emit the second (D4→D5) first, then the first (D6→D4).
func (ec *emitContext) emitFPRPhiMovesOrdered(to *Block, predIdx int) {
	type fprPhiMove struct {
		srcArg    *Value
		phiInstr  *Instr
		dstPR     PhysReg
		srcFPR    jit.FReg
		dstFPR    jit.FReg
		hasSrcFPR bool
	}
	var moves []fprPhiMove

	for _, instr := range to.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if predIdx >= len(instr.Args) {
			continue
		}
		dstPR, dstHasReg := ec.alloc.ValueRegs[instr.ID]
		if !dstHasReg || !dstPR.IsFloat {
			continue
		}
		srcArg := instr.Args[predIdx]
		m := fprPhiMove{
			srcArg:   srcArg,
			phiInstr: instr,
			dstPR:    dstPR,
			dstFPR:   jit.FReg(dstPR.Reg),
		}
		if ec.hasFPReg(srcArg.ID) {
			m.srcFPR = ec.physFPReg(srcArg.ID)
			m.hasSrcFPR = true
		}
		moves = append(moves, m)
	}

	if len(moves) <= 1 {
		for i := range moves {
			ec.emitPhiMoveRawFloat(moves[i].srcArg, moves[i].phiInstr, moves[i].dstPR)
		}
		return
	}

	// Emit in dependency-aware order. A move is safe if its destination FPR
	// is NOT a source FPR of another un-emitted move.
	emitted := make([]bool, len(moves))
	totalEmitted := 0

	for totalEmitted < len(moves) {
		progress := false

		for i := range moves {
			if emitted[i] {
				continue
			}
			m := &moves[i]
			if !m.hasSrcFPR || m.srcFPR == m.dstFPR {
				ec.emitPhiMoveRawFloat(m.srcArg, m.phiInstr, m.dstPR)
				emitted[i] = true
				totalEmitted++
				progress = true
				continue
			}
			blocked := false
			for j := range moves {
				if j == i || emitted[j] || !moves[j].hasSrcFPR {
					continue
				}
				if moves[j].srcFPR == m.dstFPR {
					blocked = true
					break
				}
			}
			if !blocked {
				ec.emitPhiMoveRawFloat(m.srcArg, m.phiInstr, m.dstPR)
				emitted[i] = true
				totalEmitted++
				progress = true
			}
		}

		if totalEmitted >= len(moves) {
			break
		}

		if !progress {
			// Cycle: break with D0 scratch.
			for i := range moves {
				if emitted[i] || !moves[i].hasSrcFPR {
					continue
				}
				m := &moves[i]
				ec.asm.FMOVd(jit.D0, m.srcFPR)
				for j := range moves {
					if j == i || emitted[j] {
						continue
					}
					if moves[j].dstFPR == m.srcFPR {
						ec.emitPhiMoveRawFloat(moves[j].srcArg, moves[j].phiInstr, moves[j].dstPR)
						emitted[j] = true
						totalEmitted++
						break
					}
				}
				ec.asm.FMOVd(m.dstFPR, jit.D0)
				if ec.crossBlockLive[m.phiInstr.ID] && !ec.loopExitBoxFPPhis[m.phiInstr.ID] {
					if dstSlot, ok := ec.slotMap[m.phiInstr.ID]; ok {
						ec.asm.FMOVtoGP(jit.X0, m.dstFPR)
						ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
					}
				}
				emitted[i] = true
				totalEmitted++
				break
			}
		}
	}
}

// emitPhiMoveRawFloat transfers a raw float value to a phi's FPR allocation.
// The source may be a raw float in FPR, NaN-boxed in GPR, or NaN-boxed in memory.
// In all cases, the destination phi FPR receives the raw float64 bits.
func (ec *emitContext) emitPhiMoveRawFloat(srcArg *Value, phiInstr *Instr, dstPR PhysReg) {
	dstFPR := jit.FReg(dstPR.Reg)

	if ec.hasFPReg(srcArg.ID) {
		// Source is raw float in FPR: transfer directly.
		srcFPR := ec.physFPReg(srcArg.ID)
		if srcFPR != dstFPR {
			ec.asm.FMOVd(dstFPR, srcFPR)
		}
	} else if ec.hasReg(srcArg.ID) && ec.rawIntRegs[srcArg.ID] {
		// Source is a raw int in a GPR, but the destination phi is float.
		// Convert numerically; moving raw/boxed bits into an FPR would create
		// a bogus double and can make mixed int/float loop phis non-terminating.
		srcReg := ec.physReg(srcArg.ID)
		ec.asm.SCVTF(dstFPR, srcReg)
	} else if ec.irTypes[srcArg.ID] == TypeInt {
		// Source is known int but not raw-register active, so materialize the
		// boxed value, unbox it, then convert to float64.
		gpr := ec.resolveValueNB(srcArg.ID, jit.X0)
		jit.EmitUnboxInt(ec.asm, jit.X0, gpr)
		ec.asm.SCVTF(dstFPR, jit.X0)
	} else {
		// Source is NaN-boxed in GPR or memory: resolve and move to FPR.
		gpr := ec.resolveValueNB(srcArg.ID, jit.X0)
		ec.asm.FMOVtoFP(dstFPR, gpr)
	}

	// Write-through to memory (NaN-boxed) if the phi is used cross-block.
	if ec.crossBlockLive[phiInstr.ID] && !ec.loopExitBoxFPPhis[phiInstr.ID] {
		dstSlot, ok := ec.slotMap[phiInstr.ID]
		if ok {
			ec.asm.FMOVtoGP(jit.X0, dstFPR)
			ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
		}
	}
}

// emitPhiMoveRawInt transfers a raw int value for a loop header phi.
// The source may be raw int (from loop back-edge), NaN-boxed in register
// (from initial entry), or NaN-boxed in memory. In all cases, the
// destination phi register receives a raw int64.
//
// Memory write-through is still performed (boxing the raw int first) so that
// other loop blocks can load the value from memory if needed.
func (ec *emitContext) emitPhiMoveRawInt(srcArg *Value, phiInstr *Instr, dstPR PhysReg) {
	dstReg := jit.Reg(dstPR.Reg)
	srcHasReg := ec.hasReg(srcArg.ID)

	if srcHasReg && ec.rawIntRegs[srcArg.ID] {
		// Source is raw int in register: transfer directly.
		srcReg := ec.physReg(srcArg.ID)
		if srcReg != dstReg {
			ec.asm.MOVreg(dstReg, srcReg)
		}
	} else if srcHasReg {
		// Source is NaN-boxed in register: unbox into destination.
		srcReg := ec.physReg(srcArg.ID)
		jit.EmitUnboxInt(ec.asm, dstReg, srcReg)
	} else {
		// Source is in memory (NaN-boxed): load and unbox.
		ec.loadValue(jit.X0, srcArg.ID)
		jit.EmitUnboxInt(ec.asm, dstReg, jit.X0)
	}

	// Write-through to memory (boxed) if the phi is used in other blocks.
	// Skip if this phi will be boxed at loop exit (deferred write-through).
	if ec.crossBlockLive[phiInstr.ID] && !ec.loopExitBoxPhis[phiInstr.ID] {
		dstSlot, ok := ec.slotMap[phiInstr.ID]
		if ok {
			jit.EmitBoxIntFast(ec.asm, jit.X0, dstReg, mRegTagInt)
			ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
		}
	}
}

// --- Control flow ---

func (ec *emitContext) emitJump(instr *Instr, block *Block) {
	if len(block.Succs) == 0 {
		return
	}
	target := block.Succs[0]
	if ec.isLoopExit(block, target) {
		ec.emitLoopExitBoxing(ec.exitingHeaderID(block, target))
	}
	ec.emitPhiMoves(block, target)
	ec.asm.B(ec.blockLabelFor(target))
}

func (ec *emitContext) emitBranch(instr *Instr, block *Block) {
	if len(instr.Args) == 0 || len(block.Succs) < 2 {
		return
	}

	trueBlock := block.Succs[0]
	falseBlock := block.Succs[1]
	prefix := ""
	if ec.numericMode {
		prefix = "num_"
	}
	trueTrampolineLabel := fmt.Sprintf("%sB%d_true_from_B%d", prefix, trueBlock.ID, block.ID)

	// Fused compare+branch: the preceding CMP/FCMP already set NZCV flags.
	// Emit B.cc directly instead of materializing a bool and testing bit 0.
	if ec.fusedActive {
		cond := ec.fusedCond
		ec.fusedActive = false
		ec.asm.BCond(cond, trueTrampolineLabel)
	} else {
		// Standard path: load NaN-boxed bool, test bit 0.
		condReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		ec.asm.TBNZ(condReg, 0, trueTrampolineLabel)
	}

	// False path (fall-through).
	if ec.isLoopExit(block, falseBlock) {
		ec.emitLoopExitBoxing(ec.exitingHeaderID(block, falseBlock))
	}
	ec.emitPhiMoves(block, falseBlock)
	ec.asm.B(ec.blockLabelFor(falseBlock))

	// True path (trampoline).
	ec.asm.Label(trueTrampolineLabel)
	if ec.isLoopExit(block, trueBlock) {
		ec.emitLoopExitBoxing(ec.exitingHeaderID(block, trueBlock))
	}
	ec.emitPhiMoves(block, trueBlock)
	ec.asm.B(ec.blockLabelFor(trueBlock))
}

// isLoopExit returns true if the edge from 'from' to 'to' exits a loop
// (from is in a loop, to is not).
//
// For nested loops: returns true if 'to' is outside 'from's innermost
// enclosing loop, even if 'to' is still inside an outer loop. This lets
// emitLoopExitBoxing run on inner-loop exits so that any loop-header phi
// values whose write-through was deferred (loopExitBoxPhis) are boxed to
// memory before leaving their loop's body.
func (ec *emitContext) isLoopExit(from *Block, to *Block) bool {
	if ec.loop == nil {
		return false
	}
	if !ec.loop.loopBlocks[from.ID] {
		return false
	}
	// 'from' is in some loop. Find from's innermost enclosing loop and
	// check whether 'to' is still inside it.
	var fromHeader int
	if ec.loop.loopHeaders[from.ID] {
		fromHeader = from.ID
	} else if h, ok := ec.loop.blockInnerHeader[from.ID]; ok {
		fromHeader = h
	} else {
		// Shouldn't happen: from is in loopBlocks but no header found.
		return !ec.loop.loopBlocks[to.ID]
	}
	bodyBlocks := ec.loop.headerBlocks[fromHeader]
	// 'to' exits from's loop if it is NOT in the header's body set
	// (including the header itself).
	return !bodyBlocks[to.ID]
}

// exitingHeaderID returns the header ID of the innermost loop being exited
// by the edge from→to. Returns -1 if the edge is not a loop exit (caller
// should have pre-checked via isLoopExit).
func (ec *emitContext) exitingHeaderID(from *Block, to *Block) int {
	if ec.loop == nil || !ec.loop.loopBlocks[from.ID] {
		return -1
	}
	if ec.loop.loopHeaders[from.ID] {
		return from.ID
	}
	if h, ok := ec.loop.blockInnerHeader[from.ID]; ok {
		return h
	}
	return -1
}

// emitLoopExitBoxing boxes loop header phi values that need exit-time
// boxing (in loopExitBoxPhis). These are phis whose write-through was
// deferred to exit time. Uses the loopHeaderRegs to find the register.
//
// When exitingHeaderID >= 0, only phis belonging to that specific loop
// header are boxed — this matters for nested loops, where boxing ALL
// deferred phis at an inner-loop exit would corrupt slots belonging to
// outer-loop phis (those registers may currently hold unrelated values).
// Pass -1 to box everything (whole-function exits).
func (ec *emitContext) emitLoopExitBoxing(exitingHeaderID int) {
	var phiSet map[int]bool
	if exitingHeaderID >= 0 && ec.loop != nil {
		phiSet = make(map[int]bool)
		for _, phiID := range ec.loop.loopPhis[exitingHeaderID] {
			phiSet[phiID] = true
		}
	}
	for valID := range ec.loopExitBoxPhis {
		if phiSet != nil && !phiSet[valID] {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valID]
		if !ok || pr.IsFloat {
			continue
		}
		reg := jit.Reg(pr.Reg)
		jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
		ec.storeValue(jit.X0, valID)
	}
	for valID := range ec.loopExitBoxFPPhis {
		if phiSet != nil && !phiSet[valID] {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valID]
		if !ok || !pr.IsFloat {
			continue
		}
		fpr := jit.FReg(pr.Reg)
		ec.asm.FMOVtoGP(jit.X0, fpr)
		ec.storeValue(jit.X0, valID)
	}
	for valID := range ec.loopExitStorePhis {
		if phiSet != nil && !phiSet[valID] {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valID]
		if !ok || pr.IsFloat {
			continue
		}
		ec.storeValue(jit.Reg(pr.Reg), valID)
	}
}

// emitReturn — moved to emit_return.go (rule 13 file-size split).
