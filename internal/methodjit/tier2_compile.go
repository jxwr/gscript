//go:build darwin && arm64

// tier2_compile.go is the entry point for the Tier 2 optimizing compiler's
// memory-to-memory ARM64 emission layer. It takes a CFG SSA IR (after
// optimization passes like TypeSpec, ConstProp, DCE) and emits ARM64 code
// where every SSA value gets a memory slot in the VM register file.
//
// NO register allocation: every instruction loads operands from memory into
// scratch registers (X0-X3), computes, and stores the result back to memory.
// This is simpler than the Tier 2 regalloc path but faster than Tier 1
// because it benefits from type specialization eliminating runtime type checks.
//
// Architecture:
//   Bytecode -> BuildGraph -> TypeSpec -> ConstProp -> DCE -> Tier2Compile -> ARM64
//
// Slot assignment:
//   - LoadSlot values reuse their original VM register slot
//   - All other values get temp slots starting at fn.NumRegs
//   - Constants are materialized inline
//
// Register convention (same as Tier 1 and regalloc-Tier 2):
//   X19: ExecContext pointer (pinned)
//   X24: NaN-boxing int tag 0xFFFE000000000000 (pinned)
//   X25: NaN-boxing bool tag 0xFFFD000000000000 (pinned)
//   X26: VM register base pointer (pinned)
//   X27: Constants pointer (pinned)
//   X0-X7: scratch registers for computation
//   D0-D3: scratch float registers

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Tier2CompiledFunc holds the generated native code for a Tier 2 compiled function.
type Tier2CompiledFunc struct {
	Code     *jit.CodeBlock
	Proto    *vm.FuncProto
	NumSlots int         // total slots (VM regs + temps)
	Resumes  map[int]int // exit ID -> code offset for resume

	// CallVM is used for call-exit: executing calls via the VM interpreter.
	CallVM *vm.VM

	// DeoptFunc is called when the JIT bails out.
	DeoptFunc func(args []runtime.Value) ([]runtime.Value, error)
}

// tier2Context holds transient state during Tier 2 code generation.
type tier2Context struct {
	fn       *Function
	asm      *jit.Assembler
	slotMap  map[int]int // SSA value ID -> home slot index in VM register file
	nextSlot int         // next available temp slot

	// exitIDs tracks instruction IDs that need resume entry points.
	exitIDs []int

	// deferredResumes tracks resume labels to emit after the epilogue.
	deferredResumes []tier2Resume
}

// tier2Resume records a deferred resume entry point.
type tier2Resume struct {
	instrID       int
	continueLabel string
}

// Tier2Compile takes a Function (after optimization passes) and produces
// executable ARM64 code using memory-to-memory emission. Every SSA value
// lives in a slot in the VM register file.
func Tier2Compile(fn *Function) (*Tier2CompiledFunc, error) {
	tc := &tier2Context{
		fn:       fn,
		asm:      jit.NewAssembler(),
		slotMap:  make(map[int]int),
		nextSlot: fn.NumRegs,
	}

	// Assign home slots for all SSA values.
	tc.assignSlots()

	// Emit prologue.
	tc.emitPrologue()

	// Emit each basic block in RPO.
	for _, block := range fn.Blocks {
		tc.emitBlock(block)
	}

	// Emit epilogue.
	tc.emitEpilogue()

	// Emit deferred resume entry points.
	tc.emitDeferredResumes()

	// Finalize: resolve labels.
	code, err := tc.asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("tier2: finalize error: %w", err)
	}

	// Allocate executable memory and write code.
	cb, err := jit.AllocExec(len(code) + 512)
	if err != nil {
		return nil, fmt.Errorf("tier2: alloc exec error: %w", err)
	}
	if err := cb.WriteCode(code); err != nil {
		cb.Free()
		return nil, fmt.Errorf("tier2: write code error: %w", err)
	}

	// Resolve resume addresses.
	resumes := make(map[int]int)
	for _, id := range tc.exitIDs {
		label := fmt.Sprintf("t2_resume_%d", id)
		off := tc.asm.LabelOffset(label)
		if off >= 0 {
			resumes[id] = off
		}
	}

	return &Tier2CompiledFunc{
		Code:     cb,
		Proto:    fn.Proto,
		NumSlots: tc.nextSlot,
		Resumes:  resumes,
	}, nil
}

// assignSlots assigns a home slot for every SSA value.
// LoadSlot values keep their original VM slot; all others get temp slots.
func (tc *tier2Context) assignSlots() {
	for _, block := range tc.fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			if instr.Op == OpLoadSlot {
				tc.slotMap[instr.ID] = int(instr.Aux)
			} else {
				tc.slotMap[instr.ID] = tc.nextSlot
				tc.nextSlot++
			}
		}
	}
}

// tier2FrameSize is the stack frame size for callee-saved registers.
// Same as Tier 1: save FP/LR + callee-saved GPRs (X19-X28) = 12 regs = 96 bytes.
const tier2FrameSize = 96

// emitPrologue emits the ARM64 function prologue.
func (tc *tier2Context) emitPrologue() {
	asm := tc.asm

	// Allocate stack frame.
	asm.SUBimm(jit.SP, jit.SP, tier2FrameSize)
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

	// Set up pinned registers.
	// X0 holds ExecContext pointer (from callJIT trampoline).
	asm.MOVreg(mRegCtx, jit.X0)                      // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)       // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))    // X24 = 0xFFFE000000000000
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))  // X25 = 0xFFFD000000000000
}

// emitEpilogue emits the ARM64 function epilogue.
func (tc *tier2Context) emitEpilogue() {
	asm := tc.asm

	// Normal return label.
	asm.Label("t2_epilogue")
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	// Shared restore and return.
	asm.Label("t2_exit")

	// Restore callee-saved GPRs.
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	// Restore FP, LR.
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	// Deallocate stack frame.
	asm.ADDimm(jit.SP, jit.SP, tier2FrameSize)
	// Return.
	asm.RET()
}

// emitDeferredResumes emits resume entry points after the epilogue.
// Each resume is a separate function entry point with its own prologue.
func (tc *tier2Context) emitDeferredResumes() {
	for _, r := range tc.deferredResumes {
		label := fmt.Sprintf("t2_resume_%d", r.instrID)
		tc.asm.Label(label)
		tc.emitResumePrologue()
		tc.asm.B(r.continueLabel)
	}
}

// emitResumePrologue emits a resume prologue for re-entering JIT code after an exit.
func (tc *tier2Context) emitResumePrologue() {
	asm := tc.asm

	// Same as full prologue: save callee-saved, set up pinned regs.
	asm.SUBimm(jit.SP, jit.SP, tier2FrameSize)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)

	// Re-establish pinned registers from context.
	asm.MOVreg(mRegCtx, jit.X0)
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))
}

// emitBlock emits ARM64 code for a single basic block.
func (tc *tier2Context) emitBlock(block *Block) {
	tc.asm.Label(fmt.Sprintf("t2_B%d", block.ID))

	for _, instr := range block.Instrs {
		tc.emitInstr(instr, block)
	}
}

// t2SlotOffset returns the byte offset for a slot.
func t2SlotOffset(slot int) int {
	return slot * jit.ValueSize
}

// t2LoadValue loads a NaN-boxed value from its home slot into the given register.
func (tc *tier2Context) t2LoadValue(dst jit.Reg, valueID int) {
	slot, ok := tc.slotMap[valueID]
	if !ok {
		return
	}
	tc.asm.LDR(dst, mRegRegs, t2SlotOffset(slot))
}

// t2StoreValue stores a NaN-boxed value from a register to its home slot.
func (tc *tier2Context) t2StoreValue(src jit.Reg, valueID int) {
	slot, ok := tc.slotMap[valueID]
	if !ok {
		return
	}
	tc.asm.STR(src, mRegRegs, t2SlotOffset(slot))
}

// Execute runs the Tier 2 compiled function with the given arguments.
func (cf *Tier2CompiledFunc) Execute(args []runtime.Value) ([]runtime.Value, error) {
	nregs := cf.NumSlots
	if nregs < len(args)+1 {
		nregs = len(args) + 1
	}
	if nregs < 16 {
		nregs = 16
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
	ctx := new(ExecContext)
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			return []runtime.Value{regs[0]}, nil

		case ExitDeopt:
			if cf.DeoptFunc != nil {
				return cf.DeoptFunc(args)
			}
			return nil, fmt.Errorf("tier2: deopt with no DeoptFunc set")

		case ExitCallExit:
			if err := cf.t2ExecuteCallExit(ctx, regs); err != nil {
				return nil, fmt.Errorf("tier2: call-exit error: %w", err)
			}
			callID := int(ctx.CallID)
			resumeOff, ok := cf.Resumes[callID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume addr for call %d", callID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitGlobalExit:
			if err := cf.t2ExecuteGlobalExit(ctx, regs); err != nil {
				return nil, fmt.Errorf("tier2: global-exit error: %w", err)
			}
			globalID := int(ctx.GlobalExitID)
			resumeOff, ok := cf.Resumes[globalID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume addr for global %d", globalID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitTableExit:
			if err := cf.t2ExecuteTableExit(ctx, regs); err != nil {
				return nil, fmt.Errorf("tier2: table-exit error: %w", err)
			}
			tableID := int(ctx.TableExitID)
			resumeOff, ok := cf.Resumes[tableID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume addr for table %d", tableID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitOpExit:
			if err := cf.t2ExecuteOpExit(ctx, regs); err != nil {
				return nil, fmt.Errorf("tier2: op-exit error: %w", err)
			}
			opID := int(ctx.OpExitID)
			resumeOff, ok := cf.Resumes[opID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume addr for op %d", opID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("tier2: unknown exit code %d", ctx.ExitCode)
		}
	}
}
