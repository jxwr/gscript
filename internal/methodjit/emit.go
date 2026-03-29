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
	Regs      uintptr // pointer to vm.regs[base]
	Constants uintptr // pointer to proto.Constants[0] (or 0 if none)
	ExitCode  int64   // 0 = normal return, 2 = deopt (bail to interpreter)
	ReturnPC  int64   // unused for now
}

// ExitCode constants.
const (
	ExitNormal = 0 // normal return
	ExitDeopt  = 2 // deopt: bail to interpreter for the entire function
)

// ExecContext field offsets.
const (
	execCtxOffRegs      = 0
	execCtxOffConstants = 8
	execCtxOffExitCode  = 16
	execCtxOffReturnPC  = 24
)

// CompiledFunction holds the generated native code for a function.
type CompiledFunction struct {
	Code      *jit.CodeBlock // executable memory
	Proto     *vm.FuncProto  // source function
	NumSpills int            // stack space needed for spill slots
	numRegs   int            // total number of VM register slots (including temp slots)

	// DeoptFunc is called when the JIT bails out (ExitCode=ExitDeopt).
	// It runs the function via the VM interpreter. Set by the caller
	// (e.g., test harness or tiering engine) to provide VM fallback.
	// If nil, Execute returns an error on deopt.
	DeoptFunc func(args []runtime.Value) ([]runtime.Value, error)
}

// Execute runs the compiled function with the given arguments.
// Arguments are loaded into VM register slots before calling the native code.
// If the JIT bails out (ExitCode=ExitDeopt), falls back via DeoptFunc.
// Returns the function's return values.
func (cf *CompiledFunction) Execute(args []runtime.Value) ([]runtime.Value, error) {
	// Allocate VM registers (NaN-boxed values).
	nregs := cf.numRegs
	if nregs < len(args)+1 {
		nregs = len(args) + 1
	}
	if nregs < 16 {
		nregs = 16 // minimum to avoid out-of-bounds
	}
	regs := make([]runtime.Value, nregs)

	// Load arguments into slots 0, 1, 2, ...
	for i, arg := range args {
		regs[i] = arg
	}
	// Fill remaining with nil.
	for i := len(args); i < nregs; i++ {
		regs[i] = runtime.NilValue()
	}

	// Set up ExecContext.
	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	// Call the JIT code.
	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	jit.CallJIT(uintptr(cf.Code.Ptr()), ctxPtr)

	// Check exit code.
	if ctx.ExitCode == ExitDeopt {
		// JIT bailed out: fall back to VM interpreter for the entire function.
		if cf.DeoptFunc != nil {
			return cf.DeoptFunc(args)
		}
		return nil, fmt.Errorf("methodjit: deopt with no DeoptFunc set")
	}

	// Read return value from slot 0.
	result := regs[0]
	return []runtime.Value{result}, nil
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

	ec := &emitContext{
		fn:             fn,
		alloc:          alloc,
		asm:            jit.NewAssembler(),
		slotMap:        make(map[int]int),
		nextSlot:       fn.NumRegs,
		activeRegs:     make(map[int]bool),
		rawIntRegs:     make(map[int]bool),
		crossBlockLive: computeCrossBlockLive(fn),
		useFPR:         hasFPR,
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

	return &CompiledFunction{
		Code:      cb,
		Proto:     fn.Proto,
		NumSpills: alloc.NumSpillSlots,
		numRegs:   ec.nextSlot,
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

	// Reset active register set for this block. Only values defined
	// in this block (or phis resolved at entry) have valid register contents.
	ec.activeRegs = make(map[int]bool)
	ec.rawIntRegs = make(map[int]bool)

	// Phi values are active at block entry (their registers were loaded
	// by emitPhiMoves from the predecessor). Check alloc directly, not
	// hasReg (which requires activeRegs to already be set -- chicken/egg).
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
			ec.activeRegs[instr.ID] = true
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
		ec.emitDeopt(instr) // strings in constants need pointer handling

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

	// --- Deopt: all complex ops bail to interpreter ---
	case OpCall, OpSelf,
		OpGetGlobal, OpSetGlobal,
		OpGetUpval, OpSetUpval,
		OpNewTable, OpGetTable, OpSetTable, OpGetField, OpSetField, OpSetList, OpAppend,
		OpConcat,
		OpLen,
		OpPow,
		OpClosure, OpClose,
		OpForPrep, OpForLoop,
		OpTForCall, OpTForLoop,
		OpVararg, OpTestSet,
		OpGo, OpMakeChan, OpSend, OpRecv,
		OpGuardType, OpGuardNonNil, OpGuardTruthy:
		ec.emitDeopt(instr)

	default:
		ec.asm.NOP() // truly unknown op placeholder
	}
}

// --- Constant emission ---
// Each stores the NaN-boxed constant to the value's home slot via X0 scratch.

func (ec *emitContext) emitConstInt(instr *Instr) {
	// Load raw int value, NaN-box it, store to register (activating it) or memory.
	ec.asm.LoadImm64(jit.X0, instr.Aux)
	jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeResultNB(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstNil(instr *Instr) {
	jit.EmitBoxNil(ec.asm, jit.X0)
	ec.storeResultNB(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstBool(instr *Instr) {
	if instr.Aux != 0 {
		// true = NB_TagBool|1. Compute from pinned X25 (1 ADD instruction).
		ec.asm.ADDimm(jit.X0, mRegTagBool, 1)
	} else {
		// false = NB_TagBool|0. Use pinned X25 directly (1 MOV instruction).
		ec.asm.MOVreg(jit.X0, mRegTagBool)
	}
	ec.storeResultNB(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstFloat(instr *Instr) {
	ec.asm.LoadImm64(jit.X0, instr.Aux)
	// Float constants stored as raw IEEE 754 bits (NaN-boxed representation).
	ec.storeResultNB(jit.X0, instr.ID)
}

// --- Slot access ---

func (ec *emitContext) emitLoadSlot(instr *Instr) {
	// Check if this value has a register allocation (don't use hasReg which
	// checks activeRegs -- this is where we ACTIVATE the register).
	pr, ok := ec.alloc.ValueRegs[instr.ID]
	if ok && !pr.IsFloat {
		// Register-resident: load from VM slot into allocated register.
		ec.emitLoadSlotToReg(instr)
		return
	}
	// Memory-to-memory: LoadSlot's home IS the VM slot; no code needed.
}

func (ec *emitContext) emitStoreSlot(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	// Get the NaN-boxed value from register or memory, store to target VM slot.
	reg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	slot := int(instr.Aux)
	ec.asm.STR(reg, mRegRegs, slotOffset(slot))
}

// --- Integer binary operations (NaN-boxed) ---

type intBinOp int

const (
	intBinAdd intBinOp = iota
	intBinSub
	intBinMul
	intBinMod
)

func (ec *emitContext) emitIntBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}

	// Resolve both operands: NaN-boxed from register or memory, then unbox.
	lhsSrc := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	jit.EmitUnboxInt(ec.asm, jit.X0, lhsSrc) // X0 = raw int lhs

	rhsSrc := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	jit.EmitUnboxInt(ec.asm, jit.X1, rhsSrc) // X1 = raw int rhs

	// Perform the operation into X0.
	switch op {
	case intBinAdd:
		ec.asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case intBinSub:
		ec.asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case intBinMul:
		ec.asm.MUL(jit.X0, jit.X0, jit.X1)
	case intBinMod:
		ec.asm.SDIV(jit.X2, jit.X0, jit.X1)
		ec.asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	}

	// Rebox result and store to register or memory.
	jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeResultNB(jit.X0, instr.ID)
}

// --- Raw int binary operation (type-specialized, no unbox/box) ---
// When TypeSpec has proven both operands are int, we keep raw int64 values
// in registers. This saves 4 instructions per operation (2 unbox + 1 box + 1 MOV).
func (ec *emitContext) emitRawIntBinOp(instr *Instr, op intBinOp) {
	if len(instr.Args) < 2 {
		return
	}
	lhs := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
	rhs := ec.resolveRawInt(instr.Args[1].ID, jit.X1)

	// Compute directly with raw ints — destination can be the allocated register.
	dst := jit.X0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
		dst = jit.Reg(pr.Reg)
	}

	switch op {
	case intBinAdd:
		ec.asm.ADDreg(dst, lhs, rhs)
	case intBinSub:
		ec.asm.SUBreg(dst, lhs, rhs)
	case intBinMul:
		ec.asm.MUL(dst, lhs, rhs)
	case intBinMod:
		ec.asm.SDIV(jit.X2, lhs, rhs)
		ec.asm.MSUB(dst, jit.X2, rhs, lhs)
	}

	// Mark as raw int in register (no box needed until block boundary/return).
	ec.storeRawInt(dst, instr.ID)
}

// --- Raw int unary negate (type-specialized, no unbox/box) ---
// When TypeSpec has proven the operand is int, we keep raw int64 values
// in registers. This saves ~12 instructions of the generic Unm path.
func (ec *emitContext) emitNegInt(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	src := ec.resolveRawInt(instr.Args[0].ID, jit.X0)

	// Compute directly with raw int — destination can be the allocated register.
	dst := jit.X0
	if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
		dst = jit.Reg(pr.Reg)
	}

	ec.asm.NEG(dst, src)

	// Mark as raw int in register (no box needed until block boundary/return).
	ec.storeRawInt(dst, instr.ID)
}

// --- Integer comparison (NaN-boxed) ---

func (ec *emitContext) emitIntCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}

	// Use raw int path if available (from type-specialized ops).
	lhs := ec.resolveRawInt(instr.Args[0].ID, jit.X0)
	rhs := ec.resolveRawInt(instr.Args[1].ID, jit.X1)

	// Compare.
	ec.asm.CMPreg(lhs, rhs)

	// Set result: 1 if condition true, 0 if false.
	ec.asm.CSET(jit.X0, cond)

	// Box as bool: NB_TagBool | (0 or 1). X25 = pinned NB_TagBool.
	ec.asm.ORRreg(jit.X0, jit.X0, mRegTagBool)

	// Store NaN-boxed bool result to register or memory.
	ec.storeResultNB(jit.X0, instr.ID)
}

// --- Phi ---

// emitPhiMoves emits copies for phi nodes when transitioning from 'from' to 'to'.
// For register-resident values, this is a register-to-register MOV (NaN-boxed).
// For memory-resident values, this is a memory-to-memory copy via scratch.
// Mixed: register-to-memory or memory-to-register via scratch X0.
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

	for _, instr := range to.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if predIdx >= len(instr.Args) {
			continue
		}

		srcArg := instr.Args[predIdx]

		// Source: use activeRegs (current block context).
		srcHasReg := ec.hasReg(srcArg.ID)
		// Destination: use allocation directly (target block context).
		dstPR, dstHasReg := ec.alloc.ValueRegs[instr.ID]
		dstHasGPR := dstHasReg && !dstPR.IsFloat

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

// --- Control flow ---

func (ec *emitContext) emitJump(instr *Instr, block *Block) {
	if len(block.Succs) == 0 {
		return
	}
	target := block.Succs[0]
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
	ec.emitPhiMoves(block, falseBlock)
	ec.asm.B(blockLabel(falseBlock))

	// True path (trampoline).
	ec.asm.Label(trueTrampolineLabel)
	ec.emitPhiMoves(block, trueBlock)
	ec.asm.B(blockLabel(trueBlock))
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
