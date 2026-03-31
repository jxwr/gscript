//go:build darwin && arm64

// emit_compile.go contains the Tier 2 compile pipeline for the Method JIT.
// It takes a Function with register allocation and produces executable ARM64 code.
// Includes the emitContext struct, slot assignment, prologue/epilogue generation,
// and block emission.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Suppress unused import warnings.
var _ runtime.Value
var _ *vm.FuncProto

// Compile takes a Function with register allocation and produces executable ARM64 code.
func Compile(fn *Function, alloc *RegAllocation) (*CompiledFunction, error) {
	// Check if any FPR allocations exist (to skip FPR save/restore).
	hasFPR := false
	for _, pr := range alloc.ValueRegs {
		if pr.IsFloat {
			hasFPR = true
			break
		}
	}

	li := computeLoopInfo(fn)
	crossBlockLive := computeCrossBlockLive(fn)
	var headerRegs map[int]loopRegEntry
	var phiOnlyArgs loopPhiArgSet
	exitBoxPhis := make(map[int]bool)
	if li.hasLoops() {
		headerRegs = li.computeHeaderExitRegs(fn, alloc)
		phiOnlyArgs = computeLoopPhiArgs(fn, li)

		// Identify loop header phis that need exit-time boxing:
		// cross-block live (used outside loop) AND register survives to end of header.
		for _, phiIDs := range li.loopPhis {
			for _, phiID := range phiIDs {
				if !crossBlockLive[phiID] {
					continue
				}
				pr, ok := alloc.ValueRegs[phiID]
				if !ok || pr.IsFloat {
					continue
				}
				// Check if this phi's register still holds this phi at end of header.
				entry, inRegs := headerRegs[pr.Reg]
				if inRegs && entry.ValueID == phiID && entry.IsRawInt {
					exitBoxPhis[phiID] = true
				}
			}
		}
	}

	// Build constant int map for immediate optimization.
	constInts := make(map[int]int64)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpConstInt {
				constInts[instr.ID] = instr.Aux
			}
		}
	}

	ec := &emitContext{
		fn:             fn,
		alloc:          alloc,
		asm:            jit.NewAssembler(),
		slotMap:        make(map[int]int),
		nextSlot:       fn.NumRegs,
		activeRegs:     make(map[int]bool),
		rawIntRegs:     make(map[int]bool),
		crossBlockLive: crossBlockLive,
		useFPR:         hasFPR,
		loop:           li,
		loopHeaderRegs:  headerRegs,
		loopPhiOnlyArgs: phiOnlyArgs,
		loopExitBoxPhis: exitBoxPhis,
		constInts:       constInts,
	}

	// Assign home slots for all SSA values.
	ec.assignSlots()

	// Emit prologue.
	ec.emitPrologue()

	// Emit each basic block.
	for _, block := range fn.Blocks {
		ec.emitBlock(block)
	}

	// Emit epilogue.
	ec.emitEpilogue()

	// Emit deferred resume entry points (after epilogue so they're separate
	// function entry points with their own prologue).
	ec.emitDeferredResumes()

	// Finalize: resolve labels.
	code, err := ec.asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("methodjit: finalize error: %w", err)
	}

	// Allocate executable memory and write code.
	cb, err := jit.AllocExec(len(code) + 1024) // extra space for safety
	if err != nil {
		return nil, fmt.Errorf("methodjit: alloc exec error: %w", err)
	}
	if err := cb.WriteCode(code); err != nil {
		cb.Free()
		return nil, fmt.Errorf("methodjit: write code error: %w", err)
	}

	// Resolve resume addresses for call-exit points.
	resumeAddrs := make(map[int]int)
	for _, callID := range ec.callExitIDs {
		label := callExitResumeLabel(callID)
		off := ec.asm.LabelOffset(label)
		if off >= 0 {
			resumeAddrs[callID] = off
		}
	}

	// Resolve direct entry offset for BLR callers.
	directEntryOff := ec.asm.LabelOffset("t2_direct_entry")

	return &CompiledFunction{
		Code:              cb,
		Proto:             fn.Proto,
		NumSpills:         alloc.NumSpillSlots,
		numRegs:           ec.nextSlot,
		ResumeAddrs:       resumeAddrs,
		DirectEntryOffset: directEntryOff,
	}, nil
}

// emitContext holds transient state during code generation.
type emitContext struct {
	fn           *Function
	alloc        *RegAllocation
	asm          *jit.Assembler
	slotMap      map[int]int // SSA value ID -> home slot index in VM register file
	nextSlot     int         // next available temp slot
	labelCounter int         // counter for generating unique labels

	// activeRegs tracks which value IDs have their register allocation active
	// in the current block. Values from other blocks must be loaded from memory.
	// Reset at the start of each block.
	activeRegs map[int]bool

	// crossBlockLive is the set of value IDs that are used in blocks other than
	// where they're defined. These values need write-through to memory.
	// Values only used within their defining block skip the memory write.
	crossBlockLive map[int]bool

	// rawIntRegs tracks which value IDs have RAW int64 (not NaN-boxed) content
	// in their allocated register. Set by emitRawIntBinOp, read by resolveRawInt.
	// Reset at block boundaries (raw state doesn't carry across blocks).
	rawIntRegs map[int]bool

	// useFPR is true if any values are allocated to FPR registers.
	// When false, FPR save/restore in prologue/epilogue is skipped.
	useFPR bool

	// callExitIDs tracks the instruction IDs of call-exit points.
	// After finalization, these are used to resolve resume label addresses.
	callExitIDs []int

	// deferredResumes tracks resume entry points to emit after the epilogue.
	deferredResumes []deferredResume

	// loop tracks loop structure for raw-int loop optimization.
	// When non-nil and a block is inside a loop, emitPhiMoves to loop
	// headers transfers raw ints, and loop header phis are marked rawInt.
	loop *loopInfo

	// loopHeaderRegs is the register state at the end of the loop header.
	// Non-header loop blocks use this to activate registers that survive
	// from the header. Computed once during Compile.
	loopHeaderRegs map[int]loopRegEntry

	// loopPhiOnlyArgs is the set of value IDs that are ONLY used as phi args
	// to loop header phis. storeRawInt skips write-through for these values
	// since emitPhiMoveRawInt reads from the register directly.
	loopPhiOnlyArgs loopPhiArgSet

	// loopExitBoxPhis is the set of phi value IDs that need boxing at loop
	// exit. These are loop header phis that are cross-block live (used
	// outside the loop) but whose write-through is deferred to exit time.
	loopExitBoxPhis map[int]bool

	// currentBlockID is the ID of the block currently being emitted.
	currentBlockID int

	// constInts maps value ID -> int64 for ConstInt instructions.
	// Used by emitRawIntBinOp to emit ADDimm/SUBimm for small constants.
	constInts map[int]int64
}

// assignSlots assigns a home slot for every SSA value.
// LoadSlot values keep their original VM slot; all others get temp slots.
func (ec *emitContext) assignSlots() {
	for _, block := range ec.fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			if instr.Op == OpLoadSlot {
				ec.slotMap[instr.ID] = int(instr.Aux)
			} else {
				ec.slotMap[instr.ID] = ec.nextSlot
				ec.nextSlot++
			}
		}
	}
}

// slotOffset returns the byte offset for a slot in the VM register file.
func slotOffset(slot int) int {
	return slot * jit.ValueSize
}

// loadValue loads a NaN-boxed value from its home slot into the given scratch register.
func (ec *emitContext) loadValue(dst jit.Reg, valueID int) {
	slot, ok := ec.slotMap[valueID]
	if !ok {
		return
	}
	ec.asm.LDR(dst, mRegRegs, slotOffset(slot))
}

// storeValue stores a NaN-boxed value from a scratch register to its home slot.
func (ec *emitContext) storeValue(src jit.Reg, valueID int) {
	slot, ok := ec.slotMap[valueID]
	if !ok {
		return
	}
	ec.asm.STR(src, mRegRegs, slotOffset(slot))
}

// blockLabel returns the assembler label name for a basic block.
func blockLabel(b *Block) string {
	return fmt.Sprintf("B%d", b.ID)
}

// frameSize is the stack frame size for callee-saved registers.
const frameSize = 128

func (ec *emitContext) emitPrologue() {
	asm := ec.asm

	// Allocate stack frame.
	asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
	// Save FP, LR.
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	// Set FP = SP.
	asm.ADDimm(jit.X29, jit.SP, 0)
	// Save callee-saved GPRs.
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)
	// Save callee-saved FPRs only if float values are register-allocated.
	if ec.useFPR {
		asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
		asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
	}

	// Set up pinned registers.
	// X0 holds ExecContext pointer (from callJIT trampoline).
	asm.MOVreg(mRegCtx, jit.X0)                      // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)       // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))     // X24 = 0xFFFE000000000000
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))  // X25 = 0xFFFD000000000000
}

func (ec *emitContext) emitEpilogue() {
	asm := ec.asm

	asm.Label("epilogue")

	// Store exit code 0 (normal return) to ExecContext.
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	// Shared register restore and return (used by both normal and deopt paths).
	asm.Label("deopt_epilogue")

	// Restore callee-saved FPRs only if they were saved.
	if ec.useFPR {
		asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
		asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
	}
	// Restore callee-saved GPRs.
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	// Restore FP, LR.
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	// Deallocate stack frame.
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	// Return.
	asm.RET()

	// --- Direct entry point for BLR callers (Tier 1 native call) ---
	// Uses the FULL frame (same as normal entry) because Tier 2 may use
	// callee-saved GPRs (X20-X23) for register allocation. The Tier 1
	// caller expects callee-saved registers to be preserved across BLR.
	// Caller has set: X0=ctx, ctx.Regs=callee regs base,
	// ctx.Constants=callee constants, CallMode=1.
	asm.Label("t2_direct_entry")
	asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)
	if ec.useFPR {
		asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
		asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
	}
	asm.MOVreg(mRegCtx, jit.X0)                       // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)        // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)  // X27 = ctx.Constants
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))     // X24
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))   // X25
	asm.B("B0") // Jump to first SSA block.

	// --- Direct epilogue for BLR callers ---
	// Return path when CallMode == 1 in emitReturn. Uses the same frame
	// restore as normal epilogue since the direct entry uses a full frame.
	asm.Label("t2_direct_epilogue")
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.useFPR {
		asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
		asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
	}
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	asm.RET()
}

// emitBlock emits ARM64 code for one basic block.
func (ec *emitContext) emitBlock(block *Block) {
	ec.asm.Label(blockLabel(block))
	ec.currentBlockID = block.ID

	isLoopBlock := ec.loop != nil && ec.loop.loopBlocks[block.ID]
	isHeader := ec.loop != nil && ec.loop.loopHeaders[block.ID]

	// Reset active register set for this block.
	ec.activeRegs = make(map[int]bool)
	ec.rawIntRegs = make(map[int]bool)

	if isLoopBlock && !isHeader && ec.loopHeaderRegs != nil {
		// Non-header loop block: activate registers that survive from the
		// loop header. This allows the loop body to directly use values
		// computed in the header without loading from memory.
		for _, entry := range ec.loopHeaderRegs {
			ec.activeRegs[entry.ValueID] = true
			if entry.IsRawInt {
				ec.rawIntRegs[entry.ValueID] = true
			}
		}
	}

	// Phi values are active at block entry (their registers were loaded
	// by emitPhiMoves from the predecessor).
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
			ec.activeRegs[instr.ID] = true
			// Loop header phis: mark int-typed phis as raw int.
			// emitPhiMoves delivers raw ints to loop header phis from
			// both the initial entry (unboxing) and back-edge (raw transfer).
			if isHeader && instr.Type == TypeInt {
				ec.rawIntRegs[instr.ID] = true
			}
		}
	}

	for _, instr := range block.Instrs {
		ec.emitInstr(instr, block)
	}
}
