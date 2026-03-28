//go:build darwin && arm64

// emit.go generates ARM64 machine code from CFG SSA IR + register allocation.
// This is the final stage of the Method JIT pipeline:
//   BuildGraph -> Passes -> RegAlloc -> Emit -> executable ARM64 code.
//
// Uses the existing ARM64 assembler from internal/jit/assembler*.go.
//
// MVP Strategy ("memory-to-memory"):
// Every SSA value has a "home slot" in the VM register file (NaN-boxed).
// - LoadSlot values: home = their original VM slot
// - Other values: home = temp slot (starting at fn.NumRegs)
// Before each instruction, operands are loaded from their home slots into
// scratch registers (X0-X3). After computation, the result is stored back
// to its home slot. This is correct across block boundaries because all
// state lives in memory, not in physical registers.
//
// Pinned registers:
//   X19: ExecContext pointer
//   X24: NaN-boxing int tag constant (0xFFFE000000000000)
//   X26: VM register base pointer
//   X27: constants pointer
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
	mRegCtx    = jit.X19 // ExecContext pointer
	mRegTagInt = jit.X24 // NaN-boxing int tag 0xFFFE000000000000
	mRegRegs   = jit.X26 // VM register base pointer
	mRegConsts = jit.X27 // constants pointer
)

// nb64 converts a uint64 NaN-boxing constant to int64 for LoadImm64.
func nb64(v uint64) int64 { return int64(v) }

// ExecContext is the calling convention struct between Go and JIT code.
// Passed via X0 from callJIT trampoline, saved into X19.
type ExecContext struct {
	Regs      uintptr // pointer to vm.regs[base]
	Constants uintptr // pointer to proto.Constants[0] (or 0 if none)
	ExitCode  int64   // 0 = normal return
	ReturnPC  int64   // unused for now
}

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
}

// Execute runs the compiled function with the given arguments.
// Arguments are loaded into VM register slots before calling the native code.
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

	// Read return value from slot 0.
	result := regs[0]
	return []runtime.Value{result}, nil
}

// Compile takes a Function with register allocation and produces executable ARM64 code.
func Compile(fn *Function, alloc *RegAllocation) (*CompiledFunction, error) {
	ec := &emitContext{
		fn:       fn,
		alloc:    alloc,
		asm:      jit.NewAssembler(),
		slotMap:  make(map[int]int),
		nextSlot: fn.NumRegs,
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
	fn       *Function
	alloc    *RegAllocation
	asm      *jit.Assembler
	slotMap  map[int]int // SSA value ID -> home slot index in VM register file
	nextSlot int         // next available temp slot
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
	// Save callee-saved FPRs.
	asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
	asm.FSTP(jit.D10, jit.D11, jit.SP, 112)

	// Set up pinned registers.
	// X0 holds ExecContext pointer (from callJIT trampoline).
	asm.MOVreg(mRegCtx, jit.X0)                      // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)       // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))    // X24 = 0xFFFE000000000000
}

func (ec *emitContext) emitEpilogue() {
	asm := ec.asm

	asm.Label("epilogue")

	// Store exit code 0 (normal return) to ExecContext.
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	// Restore callee-saved FPRs.
	asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
	asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
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

	// --- Slot access ---
	case OpLoadSlot:
		// LoadSlot's home IS the VM slot; no code needed (already there).
	case OpStoreSlot:
		ec.emitStoreSlot(instr)

	// --- Type-generic arithmetic ---
	case OpAdd, OpAddInt:
		ec.emitIntBinOp(instr, intBinAdd)
	case OpSub, OpSubInt:
		ec.emitIntBinOp(instr, intBinSub)
	case OpMul, OpMulInt:
		ec.emitIntBinOp(instr, intBinMul)
	case OpMod, OpModInt:
		ec.emitIntBinOp(instr, intBinMod)

	// --- Comparison ---
	case OpLt, OpLtInt:
		ec.emitIntCmp(instr, jit.CondLT)
	case OpLe, OpLeInt:
		ec.emitIntCmp(instr, jit.CondLE)
	case OpEq, OpEqInt:
		ec.emitIntCmp(instr, jit.CondEQ)

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

	default:
		ec.asm.NOP() // unhandled op placeholder
	}
}

// --- Constant emission ---
// Each stores the NaN-boxed constant to the value's home slot via X0 scratch.

func (ec *emitContext) emitConstInt(instr *Instr) {
	// Load raw int value into X0, NaN-box it, store to home slot.
	ec.asm.LoadImm64(jit.X0, instr.Aux)
	jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeValue(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstNil(instr *Instr) {
	jit.EmitBoxNil(ec.asm, jit.X0)
	ec.storeValue(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstBool(instr *Instr) {
	if instr.Aux != 0 {
		ec.asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	} else {
		ec.asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool))
	}
	ec.storeValue(jit.X0, instr.ID)
}

func (ec *emitContext) emitConstFloat(instr *Instr) {
	ec.asm.LoadImm64(jit.X0, instr.Aux)
	ec.storeValue(jit.X0, instr.ID)
}

// --- Slot access ---

func (ec *emitContext) emitStoreSlot(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	// Load source value from its home slot, store to target VM slot.
	ec.loadValue(jit.X0, instr.Args[0].ID)
	slot := int(instr.Aux)
	ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
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

	// Load both operands from their home slots.
	ec.loadValue(jit.X0, instr.Args[0].ID) // X0 = NaN-boxed lhs
	ec.loadValue(jit.X1, instr.Args[1].ID) // X1 = NaN-boxed rhs

	// Unbox both to raw int.
	jit.EmitUnboxInt(ec.asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(ec.asm, jit.X1, jit.X1)

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

	// Rebox result and store to home slot.
	jit.EmitBoxIntFast(ec.asm, jit.X0, jit.X0, mRegTagInt)
	ec.storeValue(jit.X0, instr.ID)
}

// --- Integer comparison (NaN-boxed) ---

func (ec *emitContext) emitIntCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}

	// Load both operands.
	ec.loadValue(jit.X0, instr.Args[0].ID)
	ec.loadValue(jit.X1, instr.Args[1].ID)

	// Unbox both.
	jit.EmitUnboxInt(ec.asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(ec.asm, jit.X1, jit.X1)

	// Compare.
	ec.asm.CMPreg(jit.X0, jit.X1)

	// Set result: 1 if condition true, 0 if false.
	ec.asm.CSET(jit.X0, cond)

	// Box as bool: NB_TagBool | (0 or 1).
	ec.asm.LoadImm64(jit.X1, nb64(jit.NB_TagBool))
	ec.asm.ORRreg(jit.X0, jit.X0, jit.X1)

	// Store result to home slot.
	ec.storeValue(jit.X0, instr.ID)
}

// --- Phi ---

// emitPhiMoves emits memory-to-memory copies for phi nodes when
// transitioning from 'from' to 'to'. For each phi in 'to', copies
// the source value's home slot to the phi's home slot.
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
		srcSlot, hasSrc := ec.slotMap[srcArg.ID]
		dstSlot, hasDst := ec.slotMap[instr.ID]
		if !hasSrc || !hasDst || srcSlot == dstSlot {
			continue
		}

		// Copy: load from source slot, store to phi's slot.
		ec.asm.LDR(jit.X0, mRegRegs, slotOffset(srcSlot))
		ec.asm.STR(jit.X0, mRegRegs, slotOffset(dstSlot))
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

	// Load condition value from its home slot.
	ec.loadValue(jit.X0, instr.Args[0].ID)

	// The condition value is a NaN-boxed bool.
	// Extract the payload bit 0. If 1 -> true branch, 0 -> false branch.
	ec.asm.LoadImm64(jit.X1, 1)
	ec.asm.ANDreg(jit.X0, jit.X0, jit.X1)

	trueTrampolineLabel := fmt.Sprintf("B%d_true_from_B%d", trueBlock.ID, block.ID)

	ec.asm.CBNZ(jit.X0, trueTrampolineLabel)

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
		// Load return value from its home slot, store to slot 0 of VM register file.
		ec.loadValue(jit.X0, instr.Args[0].ID)
		ec.asm.STR(jit.X0, mRegRegs, 0) // regs[0] = return value
	}
	// Jump to epilogue.
	ec.asm.B("epilogue")
}
