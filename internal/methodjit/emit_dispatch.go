//go:build darwin && arm64

// emit_dispatch.go contains the instruction emission dispatch (emitInstr),
// phi move resolution, control flow emission (jump, branch, return),
// and loop exit boxing for the Tier 2 Method JIT.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// emitInstr emits ARM64 code for a single SSA instruction.
func (ec *emitContext) emitInstr(instr *Instr, block *Block) {
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
	case OpNot:
		ec.emitNot(instr)

	// --- Comparison ---
	case OpLt, OpLtInt:
		ec.emitIntCmp(instr, jit.CondLT)
	case OpLe, OpLeInt:
		ec.emitIntCmp(instr, jit.CondLE)
	case OpEq, OpEqInt:
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

	// --- Call-exit: execute calls via VM and resume JIT ---
	case OpCall:
		ec.emitCallExit(instr)

	// --- Global-exit: load globals via VM and resume JIT ---
	case OpGetGlobal:
		ec.emitGlobalExit(instr)

	// --- Table operations ---
	case OpNewTable:
		ec.emitNewTableExit(instr)
	case OpGetTable:
		ec.emitGetTableExit(instr)
	case OpSetTable:
		ec.emitSetTableExit(instr)
	case OpGetField:
		ec.emitGetField(instr)
	case OpSetField:
		ec.emitSetField(instr)

	// --- Op-exit: unsupported ops exit to Go, execute there, resume JIT ---
	case OpSelf,
		OpSetGlobal,
		OpGetUpval, OpSetUpval,
		OpSetList, OpAppend,
		OpConcat,
		OpLen,
		OpPow,
		OpClosure, OpClose,
		OpForPrep, OpForLoop,
		OpTForCall, OpTForLoop,
		OpVararg, OpTestSet,
		OpGo, OpMakeChan, OpSend, OpRecv,
		OpGuardType, OpGuardNonNil, OpGuardTruthy:
		ec.emitOpExit(instr)

	default:
		ec.asm.NOP() // truly unknown op placeholder
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

	for _, instr := range to.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if predIdx >= len(instr.Args) {
			continue
		}

		srcArg := instr.Args[predIdx]

		// Destination: use allocation directly (target block context).
		dstPR, dstHasReg := ec.alloc.ValueRegs[instr.ID]
		dstHasGPR := dstHasReg && !dstPR.IsFloat

		// --- Raw-int loop path ---
		// When targeting a loop header with int-typed phi, transfer raw int
		// directly. This avoids box/unbox overhead on every iteration.
		if toIsLoopHeader && dstHasGPR && instr.Type == TypeInt {
			ec.emitPhiMoveRawInt(srcArg, instr, dstPR)
			continue
		}

		// --- Standard NaN-boxed path ---
		// Source: use activeRegs (current block context).
		srcHasReg := ec.hasReg(srcArg.ID)

		// Get NaN-boxed source value.
		// If the source is a raw int in register (from type-specialized ops),
		// we must box it first since phi moves expect NaN-boxed values.
		var srcVal jit.Reg
		if srcHasReg && ec.rawIntRegs[srcArg.ID] {
			// Raw int in register: box into X0 before the phi move.
			reg := ec.physReg(srcArg.ID)
			jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
			srcVal = jit.X0
		} else if srcHasReg {
			srcVal = ec.physReg(srcArg.ID)
		} else {
			ec.loadValue(jit.X0, srcArg.ID)
			srcVal = jit.X0
		}

		// Store to destination register (if allocated).
		if dstHasGPR {
			dstReg := jit.Reg(dstPR.Reg)
			if srcVal != dstReg {
				ec.asm.MOVreg(dstReg, srcVal)
			}
		}

		// Write-through to memory only if the phi value is used in another block.
		// Block-local phis skip the store entirely.
		if ec.crossBlockLive[instr.ID] || !dstHasGPR {
			dstSlot, hasDst := ec.slotMap[instr.ID]
			if hasDst {
				if dstHasGPR {
					ec.asm.STR(jit.Reg(dstPR.Reg), mRegRegs, slotOffset(dstSlot))
				} else if srcVal != jit.X0 {
					ec.asm.MOVreg(jit.X0, srcVal)
					ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
				} else {
					ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
				}
			}
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
		ec.emitLoopExitBoxing()
	}
	ec.emitPhiMoves(block, target)
	ec.asm.B(blockLabel(target))
}

func (ec *emitContext) emitBranch(instr *Instr, block *Block) {
	if len(instr.Args) == 0 || len(block.Succs) < 2 {
		return
	}

	trueBlock := block.Succs[0]
	falseBlock := block.Succs[1]

	// Load condition value (NaN-boxed bool) from register or memory.
	condReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)

	// The condition value is a NaN-boxed bool.
	// Test bit 0 directly: if 1 -> true branch, 0 -> false branch.
	// TBNZ is a single instruction replacing LoadImm64+AND+CBNZ (3 instructions).
	trueTrampolineLabel := fmt.Sprintf("B%d_true_from_B%d", trueBlock.ID, block.ID)

	ec.asm.TBNZ(condReg, 0, trueTrampolineLabel)

	// False path (fall-through).
	if ec.isLoopExit(block, falseBlock) {
		ec.emitLoopExitBoxing()
	}
	ec.emitPhiMoves(block, falseBlock)
	ec.asm.B(blockLabel(falseBlock))

	// True path (trampoline).
	ec.asm.Label(trueTrampolineLabel)
	if ec.isLoopExit(block, trueBlock) {
		ec.emitLoopExitBoxing()
	}
	ec.emitPhiMoves(block, trueBlock)
	ec.asm.B(blockLabel(trueBlock))
}

// isLoopExit returns true if the edge from 'from' to 'to' exits a loop
// (from is in a loop, to is not).
func (ec *emitContext) isLoopExit(from *Block, to *Block) bool {
	if ec.loop == nil {
		return false
	}
	return ec.loop.loopBlocks[from.ID] && !ec.loop.loopBlocks[to.ID]
}

// emitLoopExitBoxing boxes loop header phi values that need exit-time
// boxing (in loopExitBoxPhis). These are phis whose write-through was
// deferred to exit time. Uses the loopHeaderRegs to find the register.
func (ec *emitContext) emitLoopExitBoxing() {
	for valID := range ec.loopExitBoxPhis {
		pr, ok := ec.alloc.ValueRegs[valID]
		if !ok || pr.IsFloat {
			continue
		}
		reg := jit.Reg(pr.Reg)
		jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
		ec.storeValue(jit.X0, valID)
	}
}


func (ec *emitContext) emitReturn(instr *Instr, block *Block) {
	if len(instr.Args) > 0 {
		valID := instr.Args[0].ID
		// If the return value is a raw int in register, box it first.
		if ec.hasReg(valID) && ec.rawIntRegs[valID] {
			reg := ec.physReg(valID)
			jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
			ec.asm.STR(jit.X0, mRegRegs, 0)
		} else {
			// NaN-boxed: resolve and store directly.
			retReg := ec.resolveValueNB(valID, jit.X0)
			if retReg != jit.X0 {
				ec.asm.MOVreg(jit.X0, retReg)
			}
			ec.asm.STR(jit.X0, mRegRegs, 0)
		}
	} else {
		// No return value: use nil.
		ec.asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
		ec.asm.STR(jit.X0, mRegRegs, 0)
	}
	// Also write to ctx.BaselineReturnValue for BLR caller compatibility.
	// When called via BLR from Tier 1, the caller reads BaselineReturnValue.
	ec.asm.STR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	// Check CallMode: 0 = normal entry (from Execute/callJIT), 1 = direct entry (from BLR).
	// Both use a full 128B frame, but the direct epilogue returns to the BLR caller
	// while the normal epilogue returns to the callJIT trampoline.
	ec.asm.LDR(jit.X1, mRegCtx, execCtxOffCallMode)
	ec.asm.CBNZ(jit.X1, "t2_direct_epilogue")
	ec.asm.B("epilogue")
}
