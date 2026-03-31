//go:build darwin && arm64

// tiering_manager.go implements the TieringManager, a multi-tier JIT engine
// that manages automatic promotion from Tier 1 (baseline) to Tier 2 (optimizing).
//
// The TieringManager implements vm.MethodJITEngine and is a drop-in replacement
// for BaselineJITEngine. It delegates to BaselineJITEngine for Tier 1, and uses
// the existing Tier 2 pipeline (BuildGraph → TypeSpec → ConstProp → DCE →
// RegAlloc → Compile) for Tier 2.
//
// Tiering strategy:
//   - CallCount < 1:               stay interpreted (return nil)
//   - CallCount >= 1 && < Tier2T:  Tier 1 (baseline JIT)
//   - CallCount >= Tier2T:         try Tier 2 (optimizing JIT)
//
// The CallCount is incremented both by the VM on every vm.call() and by
// Tier 1's native BLR call sequence (which increments the callee's
// proto.CallCount before the BLR instruction). This ensures that functions
// called via BLR also accumulate call counts toward Tier 2 promotion.
//
// If Tier 2 compilation fails for a function, it falls back to Tier 1 permanently.
//
// Execution dispatches based on the compiled type:
//   - *BaselineFunc:       executed by BaselineJITEngine
//   - *CompiledFunction:   executed by Tier 2 execute loop (same as MethodJITEngine)

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// tmDefaultTier2Threshold is the default number of calls before a function
// is promoted from Tier 1 to Tier 2. Tier 1's native BLR call sequence
// increments the callee's proto.CallCount (see tier1_call.go), so both
// VM-path calls and BLR calls contribute to this counter. A threshold of 2
// means: first call compiles Tier 1, second call triggers Tier 2 compilation.
const tmDefaultTier2Threshold = 2

// TieringManager manages automatic promotion between Tier 1 and Tier 2.
// It implements vm.MethodJITEngine.
type TieringManager struct {
	tier1           *BaselineJITEngine
	tier2Compiled   map[*vm.FuncProto]*CompiledFunction
	tier2Failed     map[*vm.FuncProto]bool
	callVM          *vm.VM
	tier2Threshold  int // configurable threshold for testing
}

// NewTieringManager creates a new TieringManager with Tier 1 baseline support
// and Tier 2 optimizing support.
func NewTieringManager() *TieringManager {
	t1 := NewBaselineJITEngine()
	// Tell the Tier 1 engine to fall to slow path (callVM.CallValue) for callees
	// that have reached the Tier 2 threshold. The slow path goes through the VM's
	// call() which calls TieringManager.TryCompile(), enabling Tier 2 promotion.
	t1.SetTierUpThreshold(tmDefaultTier2Threshold)
	return &TieringManager{
		tier1:          t1,
		tier2Compiled:  make(map[*vm.FuncProto]*CompiledFunction),
		tier2Failed:    make(map[*vm.FuncProto]bool),
		tier2Threshold: tmDefaultTier2Threshold,
	}
}

// SetTier2Threshold sets the call count threshold for Tier 2 promotion.
// Only affects future compilations.
func (tm *TieringManager) SetTier2Threshold(n int) {
	tm.tier2Threshold = n
	tm.tier1.SetTierUpThreshold(n)
}

// SetCallVM sets the VM used for call-exit and global-exit during JIT execution.
func (tm *TieringManager) SetCallVM(v *vm.VM) {
	tm.callVM = v
	tm.tier1.SetCallVM(v)
}

// TryCompile checks if a function should be compiled and returns the compiled
// code. For cold functions (below Tier 2 threshold), returns Tier 1 compiled
// code. For hot functions (at or above Tier 2 threshold), promotes to Tier 2.
func (tm *TieringManager) TryCompile(proto *vm.FuncProto) interface{} {
	// Already at Tier 2? Return cached.
	if t2, ok := tm.tier2Compiled[proto]; ok {
		return t2
	}

	// Below Tier 1 threshold? Stay interpreted.
	if proto.CallCount < BaselineCompileThreshold {
		return nil
	}

	// Below Tier 2 threshold? Use Tier 1.
	if proto.CallCount < tm.tier2Threshold {
		return tm.tier1.TryCompile(proto)
	}

	// At or above Tier 2 threshold. Tier 2 already failed? Use Tier 1.
	if tm.tier2Failed[proto] {
		return tm.tier1.TryCompile(proto)
	}

	// Ensure Tier 1 is compiled first (needed as deopt fallback).
	t1 := tm.tier1.TryCompile(proto)

	// Ensure feedback is initialized for type specialization.
	// Initialize now if needed — TypeSpecializePass uses SSA-local inference
	// and doesn't require actual feedback data, so we don't need to wait
	// an extra call.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}

	// Attempt Tier 2 compilation.
	t2, err := tm.compileTier2(proto)
	if err != nil {
		tm.tier2Failed[proto] = true
		return t1
	}

	tm.tier2Compiled[proto] = t2

	// Update DirectEntryPtr so Tier 1 BLR callers jump to Tier 2's direct entry.
	if t2.DirectEntryOffset > 0 {
		proto.DirectEntryPtr = uintptr(t2.Code.Ptr()) + uintptr(t2.DirectEntryOffset)
	}

	return t2
}

// Execute runs compiled code. Dispatches to Tier 1 or Tier 2 based on the
// compiled type.
func (tm *TieringManager) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	switch c := compiled.(type) {
	case *BaselineFunc:
		return tm.tier1.Execute(c, regs, base, proto)
	case *CompiledFunction:
		return tm.executeTier2(c, regs, base, proto)
	default:
		return nil, fmt.Errorf("tiering: unknown compiled type %T", compiled)
	}
}

// compileTier2 compiles a function at Tier 2 (optimizing).
// Uses the same pipeline as MethodJITEngine: BuildGraph → TypeSpec → ConstProp →
// DCE → RegAlloc → Compile.
// canPromoteToTier2 checks if a function is safe for Tier 2 compilation.
// Currently, only pure-compute functions (no function calls, no table creation)
// are promoted. Functions with calls stay at Tier 1 which handles them natively.
func canPromoteToTier2(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CALL, vm.OP_CLOSURE, vm.OP_GETGLOBAL, vm.OP_SETGLOBAL,
			vm.OP_NEWTABLE, vm.OP_SETLIST, vm.OP_VARARG, vm.OP_SELF,
			vm.OP_CONCAT, vm.OP_GETUPVAL, vm.OP_SETUPVAL:
			return false
		}
	}
	return true
}

func (tm *TieringManager) compileTier2(proto *vm.FuncProto) (*CompiledFunction, error) {
	// Only promote pure-compute functions (no calls, no globals, no tables).
	if !canPromoteToTier2(proto) {
		return nil, fmt.Errorf("tier2: function has call/global/table ops, staying at tier 1")
	}

	// Build SSA IR.
	fn := BuildGraph(proto)

	// Validate.
	if errs := Validate(fn); len(errs) > 0 {
		return nil, fmt.Errorf("tier2: validation failed: %v", errs[0])
	}

	// Run optimization passes.
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)

	// Register allocation.
	alloc := AllocateRegisters(fn)

	// Compile to ARM64.
	cf, err := Compile(fn, alloc)
	if err != nil {
		return nil, fmt.Errorf("tier2: compile failed: %w", err)
	}

	// Update MaxStack if the JIT needs more slots than the bytecode compiler
	// originally allocated. This ensures the VM reserves enough register space
	// for recursive calls.
	if cf.numRegs > proto.MaxStack {
		proto.MaxStack = cf.numRegs
	}

	return cf, nil
}

// executeTier2 runs a Tier 2 compiled function using the VM's register file.
// This is essentially the same execute loop as MethodJITEngine.Execute, reusing
// the same exit handlers.
func (tm *TieringManager) executeTier2(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	// Ensure register space.
	needed := base + cf.numRegs
	if needed > len(regs) {
		return nil, fmt.Errorf("tier2: register file too small: need %d, have %d", needed, len(regs))
	}

	// Initialize unused registers to nil.
	for i := base + proto.NumParams; i < base+cf.numRegs; i++ {
		if i < len(regs) {
			regs[i] = runtime.NilValue()
		}
	}

	// Set up ExecContext.
	ctx := new(ExecContext)
	escapeToHeap(ctx)
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))

	// resyncRegs re-reads the VM's register file after exits.
	resyncRegs := func() {
		if tm.callVM == nil {
			return
		}
		regs = tm.callVM.Regs()
		ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	}

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			// Tier 2 return: result in regs[base] (slot 0 relative to base).
			result := regs[base]
			return []runtime.Value{result}, nil

		case ExitDeopt:
			// Bail to interpreter. Return error so the VM falls through.
			return nil, fmt.Errorf("tier2: deopt")

		case ExitCallExit:
			if err := tm.executeCallExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: call-exit: %w", err)
			}
			resyncRegs()
			callID := int(ctx.CallID)
			resumeOff, ok := cf.ResumeAddrs[callID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for call %d", callID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitGlobalExit:
			if err := tm.executeGlobalExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: global-exit: %w", err)
			}
			resyncRegs()
			globalID := int(ctx.GlobalExitID)
			resumeOff, ok := cf.ResumeAddrs[globalID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for global %d", globalID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitTableExit:
			if err := tm.executeTableExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: table-exit: %w", err)
			}
			resyncRegs()
			tableID := int(ctx.TableExitID)
			resumeOff, ok := cf.ResumeAddrs[tableID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for table %d", tableID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitOpExit:
			if err := tm.executeOpExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: op-exit: %w", err)
			}
			resyncRegs()
			opID := int(ctx.OpExitID)
			resumeOff, ok := cf.ResumeAddrs[opID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for op %d", opID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("tier2: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// CompileTier2 explicitly compiles a function at Tier 2. This bypasses the
// call count threshold and is useful for testing or when the caller knows
// the function is hot. Returns error if Tier 2 compilation fails.
func (tm *TieringManager) CompileTier2(proto *vm.FuncProto) error {
	if _, ok := tm.tier2Compiled[proto]; ok {
		return nil // already compiled
	}
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}
	t2, err := tm.compileTier2(proto)
	if err != nil {
		tm.tier2Failed[proto] = true
		return err
	}
	tm.tier2Compiled[proto] = t2

	// Update DirectEntryPtr so Tier 1 BLR callers jump to Tier 2's direct entry.
	if t2.DirectEntryOffset > 0 {
		proto.DirectEntryPtr = uintptr(t2.Code.Ptr()) + uintptr(t2.DirectEntryOffset)
	}

	return nil
}

// Tier2Count returns the number of functions compiled at Tier 2.
func (tm *TieringManager) Tier2Count() int {
	return len(tm.tier2Compiled)
}

// Tier1Count returns the number of functions compiled at Tier 1.
func (tm *TieringManager) Tier1Count() int {
	return tm.tier1.CompiledCount()
}
