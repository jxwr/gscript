//go:build darwin && arm64

// emit.go generates ARM64 machine code from CFG SSA IR + register allocation.
// This is the final stage of the Method JIT pipeline:
//   BuildGraph -> Passes -> RegAlloc -> Emit -> executable ARM64 code.
//
// Uses the existing ARM64 assembler from internal/jit/assembler*.go.
//
// Strategy: "register-resident" for values with physical register allocation,
// "memory-to-memory" fallback for spilled/unallocated values.
//
// Register-resident values (X20-X23) hold NaN-boxed values in ARM64 callee-saved
// registers. Within a basic block, reads from these registers avoid memory LDR.
// Block-local values also skip memory STR entirely. Cross-block live values use
// write-through (register + memory) so other blocks can read them.
//
// Memory-to-memory fallback: values without a register allocation use the
// original strategy of loading from VM register slots into scratch registers
// (X0-X3) for each instruction.
//
// See emit_reg.go for register resolution helpers and cross-block analysis.
//
// Pinned registers:
//   X19: ExecContext pointer
//   X24: NaN-boxing int tag constant (0xFFFE000000000000)
//   X25: NaN-boxing bool tag constant (0xFFFD000000000000)
//   X26: VM register base pointer
//   X27: constants pointer
//   X20-X23, X28: allocated GPRs (NaN-boxed values cached from VM register file)
//   X0-X3: scratch (caller-saved, used for computation)

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Pinned register aliases (must match trace JIT convention).
const (
	mRegCtx     = jit.X19 // ExecContext pointer
	mRegTagInt  = jit.X24 // NaN-boxing int tag 0xFFFE000000000000
	mRegTagBool = jit.X25 // NaN-boxing bool tag 0xFFFD000000000000
	mRegRegs    = jit.X26 // VM register base pointer
	mRegConsts  = jit.X27 // constants pointer
)

// nb64 converts a uint64 NaN-boxing constant to int64 for LoadImm64.
func nb64(v uint64) int64 { return int64(v) }

// ExecContext is the calling convention struct between Go and JIT code.
// Passed via X0 from callJIT trampoline, saved into X19.
type ExecContext struct {
	Regs         uintptr // pointer to vm.regs[base]
	Constants    uintptr // pointer to proto.Constants[0] (or 0 if none)
	ExitCode     int64   // 0 = normal, 2 = deopt, 3 = call-exit, 4 = global-exit, 5 = table-exit
	ReturnPC     int64   // unused for now
	CallSlot     int64   // VM register slot of the function value (call-exit)
	CallNArgs    int64   // number of arguments for call-exit
	CallNRets    int64   // number of expected results for call-exit
	CallID       int64   // instruction ID for resolving resume address
	GlobalSlot   int64   // VM register slot for global-exit result
	GlobalConst  int64   // constant pool index for global name (global-exit)
	GlobalExitID int64   // instruction ID for resolving global-exit resume address
	// Table-exit fields (ExitCode=5): for OpNewTable, OpGetTable, OpSetTable
	TableOp       int64  // 0=NewTable, 1=GetTable, 2=SetTable, 3=GetField(deopt), 4=SetField(deopt)
	TableSlot     int64  // VM register slot for the table (or result slot for NewTable)
	TableKeySlot  int64  // VM register slot for the key (GetTable/SetTable)
	TableValSlot  int64  // VM register slot for the value (SetTable)
	TableAux      int64  // Aux data: NewTable=arrayHint, GetField/SetField=constIdx
	TableAux2     int64  // Aux2 data: NewTable=hashHint
	TableExitID   int64  // instruction ID for resolving resume address
	// Op-exit fields (ExitCode=6): generic exit for unsupported ops
	OpExitOp   int64 // which Op to execute (cast to Op)
	OpExitSlot int64 // destination slot for result
	OpExitArg1 int64 // operand 1 slot (or constant index)
	OpExitArg2 int64 // operand 2 slot (or constant index)
	OpExitAux  int64 // auxiliary data (e.g., constant pool index for strings)
	OpExitID   int64 // resume point ID (instruction ID)
	// Baseline JIT fields (ExitCode=7): bytecode-level op-exit
	BaselineOp int64 // vm.Opcode of the bytecode being executed
	BaselinePC int64 // bytecodePC of the NEXT instruction (resume point)
	BaselineA  int64 // decoded A field from the instruction
	BaselineB  int64 // decoded B field (or Bx for ABx format)
	BaselineC  int64 // decoded C field
	// Baseline JIT native table access support
	BaselineFieldCache uintptr // pointer to proto.FieldCache[0] (nil if not yet allocated)
	BaselineClosurePtr uintptr // pointer to *vm.Closure (for GETUPVAL/SETUPVAL)
}

// ExitCode constants.
const (
	ExitNormal     = 0 // normal return
	ExitDeopt      = 2 // deopt: bail to interpreter for the entire function
	ExitCallExit   = 3 // call-exit: pause JIT, execute call via VM, resume JIT
	ExitGlobalExit = 4 // global-exit: pause JIT, load global via VM, resume JIT
	ExitTableExit  = 5 // table-exit: pause JIT, do table op via Go, resume JIT
	ExitOpExit         = 6 // op-exit: pause JIT, Go handles the operation, resume JIT
	ExitBaselineOpExit = 7 // baseline op-exit: bytecode-level exit for Tier 1
)

// TableOp constants (stored in ExecContext.TableOp).
const (
	TableOpNewTable  = 0
	TableOpGetTable  = 1
	TableOpSetTable  = 2
	TableOpGetField  = 3 // deopt fallback for GetField (no field cache)
	TableOpSetField  = 4 // deopt fallback for SetField (no field cache)
)

// ExecContext field offsets (must match struct layout above).
var (
	execCtxOffRegs         = int(unsafe.Offsetof(ExecContext{}.Regs))
	execCtxOffConstants    = int(unsafe.Offsetof(ExecContext{}.Constants))
	execCtxOffExitCode     = int(unsafe.Offsetof(ExecContext{}.ExitCode))
	execCtxOffReturnPC     = int(unsafe.Offsetof(ExecContext{}.ReturnPC))
	execCtxOffCallSlot     = int(unsafe.Offsetof(ExecContext{}.CallSlot))
	execCtxOffCallNArgs    = int(unsafe.Offsetof(ExecContext{}.CallNArgs))
	execCtxOffCallNRets    = int(unsafe.Offsetof(ExecContext{}.CallNRets))
	execCtxOffCallID       = int(unsafe.Offsetof(ExecContext{}.CallID))
	execCtxOffGlobalSlot   = int(unsafe.Offsetof(ExecContext{}.GlobalSlot))
	execCtxOffGlobalConst  = int(unsafe.Offsetof(ExecContext{}.GlobalConst))
	execCtxOffGlobalExitID = int(unsafe.Offsetof(ExecContext{}.GlobalExitID))
	execCtxOffTableOp      = int(unsafe.Offsetof(ExecContext{}.TableOp))
	execCtxOffTableSlot    = int(unsafe.Offsetof(ExecContext{}.TableSlot))
	execCtxOffTableKeySlot = int(unsafe.Offsetof(ExecContext{}.TableKeySlot))
	execCtxOffTableValSlot = int(unsafe.Offsetof(ExecContext{}.TableValSlot))
	execCtxOffTableAux     = int(unsafe.Offsetof(ExecContext{}.TableAux))
	execCtxOffTableAux2    = int(unsafe.Offsetof(ExecContext{}.TableAux2))
	execCtxOffTableExitID  = int(unsafe.Offsetof(ExecContext{}.TableExitID))
	execCtxOffOpExitOp     = int(unsafe.Offsetof(ExecContext{}.OpExitOp))
	execCtxOffOpExitSlot   = int(unsafe.Offsetof(ExecContext{}.OpExitSlot))
	execCtxOffOpExitArg1   = int(unsafe.Offsetof(ExecContext{}.OpExitArg1))
	execCtxOffOpExitArg2   = int(unsafe.Offsetof(ExecContext{}.OpExitArg2))
	execCtxOffOpExitAux    = int(unsafe.Offsetof(ExecContext{}.OpExitAux))
	execCtxOffOpExitID     = int(unsafe.Offsetof(ExecContext{}.OpExitID))
	// Baseline JIT fields
	execCtxOffBaselineOp         = int(unsafe.Offsetof(ExecContext{}.BaselineOp))
	execCtxOffBaselinePC         = int(unsafe.Offsetof(ExecContext{}.BaselinePC))
	execCtxOffBaselineA          = int(unsafe.Offsetof(ExecContext{}.BaselineA))
	execCtxOffBaselineB          = int(unsafe.Offsetof(ExecContext{}.BaselineB))
	execCtxOffBaselineC          = int(unsafe.Offsetof(ExecContext{}.BaselineC))
	execCtxOffBaselineFieldCache = int(unsafe.Offsetof(ExecContext{}.BaselineFieldCache))
	execCtxOffBaselineClosurePtr = int(unsafe.Offsetof(ExecContext{}.BaselineClosurePtr))
)

// CompiledFunction holds the generated native code for a function.
type CompiledFunction struct {
	Code      *jit.CodeBlock // executable memory
	Proto     *vm.FuncProto  // source function
	NumSpills int            // stack space needed for spill slots
	numRegs   int            // total number of VM register slots (including temp slots)

	// ResumeAddrs maps call instruction ID to the native code offset (bytes)
	// of the resume label. Used to re-enter JIT code after a call-exit.
	ResumeAddrs map[int]int

	// DeoptFunc is called when the JIT bails out (ExitCode=ExitDeopt).
	// It runs the function via the VM interpreter. Set by the caller
	// (e.g., test harness or tiering engine) to provide VM fallback.
	// If nil, Execute returns an error on deopt.
	DeoptFunc func(args []runtime.Value) ([]runtime.Value, error)

	// CallVM is used for call-exit: executing calls via the VM interpreter.
	// Set by the caller. If nil, calls fall back to DeoptFunc.
	CallVM *vm.VM
}

// Execute runs the compiled function with the given arguments.
// Arguments are loaded into VM register slots before calling the native code.
// If the JIT bails out (ExitCode=ExitDeopt), falls back via DeoptFunc.
// If the JIT hits a call-exit (ExitCode=ExitCallExit), executes the call
// via the VM and re-enters the JIT at the resume point.
// Returns the function's return values.

// Execute, executeCallExit, executeGlobalExit, executeTableExit
// are in emit_execute.go.


// executeCallExit handles a call-exit by executing the call via the VM.
// The JIT has stored all register-resident values to memory before exiting,
// so the VM register file (regs) is fully up-to-date.
func (cf *CompiledFunction) executeCallExit(ctx *ExecContext, regs []runtime.Value) error {
	callSlot := int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	nRets := int(ctx.CallNRets)

	// Get the function value from the register file.
	if callSlot >= len(regs) {
		return fmt.Errorf("call slot %d out of range (regs len %d)", callSlot, len(regs))
	}
	fnVal := regs[callSlot]

	// Collect arguments from regs[callSlot+1 .. callSlot+nArgs].
	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := callSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	// Execute the call.
	var results []runtime.Value
	var err error

	if cf.CallVM != nil {
		results, err = cf.CallVM.CallValue(fnVal, callArgs)
	} else if cf.DeoptFunc != nil {
		// Fallback: no CallVM, try to use the function value directly.
		return fmt.Errorf("no CallVM set for call-exit")
	} else {
		return fmt.Errorf("no CallVM or DeoptFunc set for call-exit")
	}
	if err != nil {
		return err
	}

	// Place results back into the register file at regs[callSlot..callSlot+nRets-1].
	// This follows Lua calling convention: results overwrite the function slot.
	nr := nRets
	if nr <= 0 {
		nr = 1 // at minimum, 1 result
	}
	for i := 0; i < nr; i++ {
		idx := callSlot + i
		if idx < len(regs) {
			if i < len(results) {
				regs[idx] = results[i]
			} else {
				regs[idx] = runtime.NilValue()
			}
		}
	}

	return nil
}

// executeGlobalExit handles a global-exit by loading a global variable via the VM.
// The global name is looked up from the constants pool and resolved via the VM.
func (cf *CompiledFunction) executeGlobalExit(ctx *ExecContext, regs []runtime.Value) error {
	globalSlot := int(ctx.GlobalSlot)
	constIdx := int(ctx.GlobalConst)

	if cf.CallVM == nil {
		return fmt.Errorf("no CallVM set for global-exit")
	}

	// Look up the global name from the constants pool.
	if cf.Proto == nil || constIdx >= len(cf.Proto.Constants) {
		return fmt.Errorf("global constant index %d out of range", constIdx)
	}
	globalName := cf.Proto.Constants[constIdx].Str()

	// Resolve the global value.
	val := cf.CallVM.GetGlobal(globalName)

	// Store the global value to the register file.
	if globalSlot < len(regs) {
		regs[globalSlot] = val
	}

	return nil
}

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

	return &CompiledFunction{
		Code:        cb,
		Proto:       fn.Proto,
		NumSpills:   alloc.NumSpillSlots,
		numRegs:     ec.nextSlot,
		ResumeAddrs: resumeAddrs,
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
			ec.asm.STR(retReg, mRegRegs, 0)
		}
	}
	// Jump to epilogue.
	ec.asm.B("epilogue")
}
