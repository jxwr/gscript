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
	var headerRegs map[int]map[int]loopRegEntry
	var headerFPRegs map[int]map[int]loopFPRegEntry
	var safeHdrRegs map[int]map[int]loopRegEntry
	var safeHdrFPRegs map[int]map[int]loopFPRegEntry
	var phiOnlyArgs loopPhiArgSet
	exitBoxPhis := make(map[int]bool)
	if li.hasLoops() {
		headerRegs = li.computeHeaderExitRegs(fn, alloc)
		headerFPRegs = li.computeHeaderExitFPRegs(fn, alloc)
		// Compute safe header regs: only registers NOT clobbered by any
		// non-header block in the loop body. These are used for both
		// block activation and loopPhiOnlyArgs checking.
		safeHdrRegs = computeSafeHeaderRegs(fn, li, alloc, headerRegs)
		safeHdrFPRegs = computeSafeHeaderFPRegs(fn, li, alloc, headerFPRegs)
		phiOnlyArgs = computeLoopPhiArgs(fn, li, alloc, safeHdrRegs)

		// Identify loop header phis that need exit-time boxing:
		// cross-block live AND register survives through the ENTIRE loop body
		// (not just the header). If any non-header block in the loop has an
		// instruction allocated to the same GPR, the phi's register will be
		// clobbered, so we must write-through on every iteration.
		for headerID, phiIDs := range li.loopPhis {
			hdrRegs := headerRegs[headerID]
			bodyBlocks := li.headerBlocks[headerID]
			for _, phiID := range phiIDs {
				if !crossBlockLive[phiID] {
					continue
				}
				pr, ok := alloc.ValueRegs[phiID]
				if !ok || pr.IsFloat {
					continue
				}
				// Check if this phi's register still holds this phi at
				// end of its own header.
				entry, inRegs := hdrRegs[pr.Reg]
				if !inRegs || entry.ValueID != phiID || !entry.IsRawInt {
					continue
				}
				// Check that no non-header block in the loop body clobbers
				// this register. If clobbered, the phi value can't survive
				// in-register and must be written to memory.
				//
				// A "clobber" is any instruction whose allocated register
				// equals this phi's register. Nested loop header phis
				// count: their phi moves write the register at inner-loop
				// entry, overwriting the outer header's phi value.
				clobbered := false
				for _, block := range fn.Blocks {
					if block.ID == headerID || !bodyBlocks[block.ID] {
						continue
					}
					for _, instr := range block.Instrs {
						if instr.Op.IsTerminator() {
							continue
						}
						instrPR, ok := alloc.ValueRegs[instr.ID]
						if !ok || instrPR.IsFloat || instrPR.Reg != pr.Reg {
							continue
						}
						clobbered = true
						break
					}
					if clobbered {
						break
					}
				}
				if !clobbered {
					exitBoxPhis[phiID] = true
				}
			}
		}
	}

	// Build constant int/bool maps for immediate optimization, and IR type map for
	// resolveRawFloat to detect int-typed values that need SCVTF conversion.
	constInts := make(map[int]int64)
	constBools := make(map[int]int64)
	irTypes := make(map[int]Type)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpConstInt {
				constInts[instr.ID] = instr.Aux
			}
			if instr.Op == OpConstBool {
				constBools[instr.ID] = instr.Aux
			}
			irTypes[instr.ID] = instr.Type
		}
	}

	// Identify single-use comparisons that can be fused with their
	// immediately-following Branch. Fused pairs emit CMP/FCMP + B.cc
	// instead of CMP + CSET + ORR + TBNZ (saves 3 instructions).
	useCounts := computeUseCounts(fn)
	fusedCmps := make(map[int]bool)
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if !isFusableComparison(instr.Op) || useCounts[instr.ID] != 1 {
				continue
			}
			if i+1 < len(block.Instrs) {
				next := block.Instrs[i+1]
				if next.Op == OpBranch && len(next.Args) > 0 && next.Args[0].ID == instr.ID {
					fusedCmps[instr.ID] = true
				}
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
		activeFPRegs:   make(map[int]bool),
		shapeVerified:  make(map[int]uint32),
		crossBlockLive: crossBlockLive,
		useFPR:         hasFPR,
		loop:             li,
		loopHeaderRegs:   headerRegs,
		loopHeaderFPRegs: headerFPRegs,
		safeHeaderRegs:   safeHdrRegs,
		safeHeaderFPRegs: safeHdrFPRegs,
		loopPhiOnlyArgs:  phiOnlyArgs,
		loopExitBoxPhis:  exitBoxPhis,
		constInts:        constInts,
		constBools:       constBools,
		irTypes:          irTypes,
		scratchFPRCache:  make(map[int]jit.FReg),
		fusedCmps:        fusedCmps,
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

	// Allocate per-GetGlobal value cache if any GetGlobal instructions exist.
	var globalCache []uint64
	if ec.nextGlobalCacheIndex > 0 {
		globalCache = make([]uint64, ec.nextGlobalCacheIndex)
	}

	return &CompiledFunction{
		Code:              cb,
		Proto:             fn.Proto,
		NumSpills:         alloc.NumSpillSlots,
		numRegs:           ec.nextSlot,
		ResumeAddrs:       resumeAddrs,
		DirectEntryOffset: directEntryOff,
		GlobalCache:       globalCache,
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

	// shapeVerified tracks table SSA value IDs whose shape has been verified
	// in the current block. Maps table value ID -> verified shapeID.
	// Reset at block boundaries and after calls.
	shapeVerified map[int]uint32

	// activeFPRegs tracks which value IDs have their FPR allocation active
	// in the current block. Mirrors activeRegs for FPR-allocated values.
	// Reset at the start of each block.
	activeFPRegs map[int]bool

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

	// loopHeaderRegs is the per-header register state at the end of each loop
	// header. Maps headerBlockID -> (register number -> entry). Non-header
	// loop blocks look up their innermost header to activate the right registers.
	loopHeaderRegs map[int]map[int]loopRegEntry

	// loopHeaderFPRegs is the per-header FPR register state at the end of
	// each loop header. Maps headerBlockID -> (FPR number -> entry).
	loopHeaderFPRegs map[int]map[int]loopFPRegEntry

	// safeHeaderRegs are the subset of loopHeaderRegs whose registers are
	// NOT clobbered by any non-header block in the loop body. Only these
	// values can safely be activated in non-header blocks.
	safeHeaderRegs   map[int]map[int]loopRegEntry
	safeHeaderFPRegs map[int]map[int]loopFPRegEntry

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

	// constBools maps value ID -> 0 (false) or 1 (true) for ConstBool instructions.
	// Used by emitSetTableNative to bypass runtime tag checks for constant bools.
	constBools map[int]int64

	// irTypes maps value ID -> IR Type from the defining instruction.
	// Used by resolveRawFloat to detect NaN-boxed ints that need SCVTF
	// conversion instead of FMOVtoFP.
	irTypes map[int]Type

	// nextGlobalCacheIndex is the next available cache slot index for
	// OpGetGlobal native cache. Each GetGlobal instruction gets a unique
	// index (0, 1, 2, ...) assigned at emission time.
	nextGlobalCacheIndex int

	// scratchFPRCache maps value ID -> scratch FPR (D0-D3) currently holding
	// that value's raw float. Scoped to a SINGLE instruction's operand resolution
	// — cleared at the start of every emitInstr call. Enables dedup of same-value
	// operands within one instruction (e.g., v*v loads v only once).
	scratchFPRCache map[int]jit.FReg

	// fusedCmps is the set of comparison instruction IDs that will be fused
	// with their immediately-following Branch. These comparisons emit only
	// CMP/FCMP (no CSET+ORR bool materialization).
	fusedCmps map[int]bool

	// fusedCond holds the condition code from the last fused comparison.
	// Set by emitIntCmp/emitFloatCmp when the comparison is in fusedCmps.
	fusedCond jit.Cond

	// fusedActive is true when the preceding comparison was fused and
	// emitBranch should use fusedCond + B.cc instead of TBNZ.
	fusedActive bool
}

// isFusableComparison returns true for comparison ops that can be fused
// with an immediately-following Branch (emit CMP/FCMP + B.cc).
func isFusableComparison(op Op) bool {
	switch op {
	case OpLtInt, OpLeInt, OpEqInt, OpLtFloat, OpLeFloat:
		return true
	}
	return false
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
	ec.activeFPRegs = make(map[int]bool)
	ec.shapeVerified = make(map[int]uint32)

	if isLoopBlock && !isHeader && ec.safeHeaderRegs != nil {
		// Non-header loop block: activate SAFE registers from the innermost
		// enclosing loop header. Only registers that are NOT clobbered by
		// any non-header block in the loop body are activated. This prevents
		// stale register assumptions in nested or complex loop bodies.
		if innerHeader, ok := ec.loop.blockInnerHeader[block.ID]; ok {
			if hdrRegs, ok := ec.safeHeaderRegs[innerHeader]; ok {
				for _, entry := range hdrRegs {
					ec.activeRegs[entry.ValueID] = true
					if entry.IsRawInt {
						ec.rawIntRegs[entry.ValueID] = true
					}
				}
			}
		}
	}
	if isLoopBlock && !isHeader && ec.safeHeaderFPRegs != nil {
		// Non-header loop block: activate SAFE FPR registers from innermost header.
		if innerHeader, ok := ec.loop.blockInnerHeader[block.ID]; ok {
			if hdrFPRegs, ok := ec.safeHeaderFPRegs[innerHeader]; ok {
				for _, entry := range hdrFPRegs {
					ec.activeFPRegs[entry.ValueID] = true
				}
			}
		}
	}

	// Phi values are active at block entry (their registers were loaded
	// by emitPhiMoves from the predecessor). When a phi's register
	// conflicts with a loopHeaderRegs value, invalidate the header value.
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok {
			if pr.IsFloat {
				// FPR phi: activated by emitPhiMoves which delivers raw float.
				ec.invalidateFPReg(pr.Reg, instr.ID)
				ec.activeFPRegs[instr.ID] = true
			} else {
				// Invalidate any header reg value that shares this register.
				ec.invalidateReg(pr.Reg, instr.ID)
				ec.activeRegs[instr.ID] = true
				// Loop header phis: mark int-typed phis as raw int.
				// emitPhiMoves delivers raw ints to loop header phis from
				// both the initial entry (unboxing) and back-edge (raw transfer).
				if isHeader && instr.Type == TypeInt {
					ec.rawIntRegs[instr.ID] = true
				}
			}
		}
	}

	for _, instr := range block.Instrs {
		ec.emitInstr(instr, block)
	}
}
