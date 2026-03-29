//go:build darwin && arm64

// tiering.go manages automatic promotion from interpreter to Method JIT.
// When a function's call count exceeds the compilation threshold,
// the Method JIT compiles it and installs the native code for future calls.
// This is the bridge between the VM interpreter and the Method JIT.
//
// Flow:
//  1. VM calls TryCompile on every function call (fast path: map lookup).
//  2. If call count < threshold, returns nil (stay interpreted).
//  3. At threshold, ensures feedback is initialized and waits one more call.
//  4. On next call after feedback is ready, compiles via BuildGraph -> Validate -> RegAlloc -> Compile.
//  5. Caches the CompiledFunction; subsequent calls return it immediately.
//  6. Execute runs the native code using the VM's register file directly.

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// CompileThreshold is the number of calls before a function is compiled.
// Keep at 100 until Method JIT correctness is verified on all benchmarks.
// Lowering to 2 caused sieve to produce wrong results and 470x slowdown.
const CompileThreshold = 100

// MethodJITEngine manages compiled function cache and tiering decisions.
type MethodJITEngine struct {
	compiled map[*vm.FuncProto]*CompiledFunction
	failed   map[*vm.FuncProto]bool // functions that failed compilation
	callVM   *vm.VM                 // VM for call-exit and global-exit
}

// NewMethodJITEngine creates a new Method JIT engine with empty caches.
func NewMethodJITEngine() *MethodJITEngine {
	return &MethodJITEngine{
		compiled: make(map[*vm.FuncProto]*CompiledFunction),
		failed:   make(map[*vm.FuncProto]bool),
	}
}

// SetCallVM sets the VM used for call-exit and global-exit during JIT execution.
// This should be called after the engine is created, typically by the VM
// when SetMethodJIT is called.
func (e *MethodJITEngine) SetCallVM(v *vm.VM) {
	e.callVM = v
}

// IsCompiled returns true if the function has been compiled.
func (e *MethodJITEngine) IsCompiled(proto *vm.FuncProto) bool {
	_, ok := e.compiled[proto]
	return ok
}

// TryCompile checks if a function should be compiled and compiles it.
// Returns the compiled function (as interface{}) if available, nil if not ready or failed.
// The caller is responsible for incrementing proto.CallCount before calling this.
func (e *MethodJITEngine) TryCompile(proto *vm.FuncProto) interface{} {
	// Already compiled? Fast path.
	if cf, ok := e.compiled[proto]; ok {
		return cf
	}

	// Already failed? Don't retry.
	if e.failed[proto] {
		return nil
	}

	// Not hot enough?
	if proto.CallCount < CompileThreshold {
		return nil
	}

	// Ensure feedback is collecting. If we just initialized it,
	// wait for the next call so feedback has at least one iteration of data.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
		return nil
	}

	// Build IR and validate.
	fn := BuildGraph(proto)
	errs := Validate(fn)
	if len(errs) > 0 {
		e.failed[proto] = true
		return nil
	}

	// Run optimization passes: type specialization, constant propagation, DCE.
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		e.failed[proto] = true
		return nil
	}

	// The JIT may use more register slots than the bytecode compiler's MaxStack
	// (SSA temps, spill slots, etc.). Update MaxStack so the VM reserves enough
	// space for recursive calls — otherwise the callee's frame overlaps the
	// JIT's live registers and corrupts them.
	if cf.numRegs > proto.MaxStack {
		proto.MaxStack = cf.numRegs
	}

	e.compiled[proto] = cf
	return cf
}

// Execute runs a compiled function using the VM's register file.
// The compiled parameter must be a *CompiledFunction returned by TryCompile.
// The arguments must already be placed at regs[base..base+numParams-1] by the VM.
// Returns the result values read from regs[base] after execution.
// If the JIT bails out (ExitCode=ExitDeopt), returns an error so the VM
// falls through to the interpreter.
func (e *MethodJITEngine) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	cf := compiled.(*CompiledFunction)
	// Ensure we have enough register space for the compiled function's temp slots.
	needed := base + cf.numRegs
	if needed > len(regs) {
		return nil, fmt.Errorf("methodjit: register file too small: need %d, have %d", needed, len(regs))
	}

	// Ensure register file is large enough for the JIT's temp slots.
	// The JIT may use slots beyond proto.MaxStack for SSA temp values.
	if needed < base+cf.numRegs+proto.MaxStack {
		needed = base + cf.numRegs + proto.MaxStack
	}
	if needed > len(regs) {
		return nil, fmt.Errorf("methodjit: register file too small: need %d, have %d", needed, len(regs))
	}

	// Initialize unused registers to nil to avoid stale data.
	for i := base + proto.NumParams; i < base+cf.numRegs; i++ {
		if i < len(regs) {
			regs[i] = runtime.NilValue()
		}
	}

	// Set up ExecContext pointing into the VM's register file at the callee's base.
	// Heap-allocate to ensure the address is stable across Go stack growth.
	// During recursive call-exit handling, deep recursion can trigger goroutine
	// stack growth. Stack-resident ExecContext would be moved but uintptr(ctxPtr)
	// would NOT be updated, causing the JIT to read/write stale memory.
	ctx := new(ExecContext)
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))

	// resyncRegs re-reads the VM's register file after any exit handler.
	// The VM's register file may have been replaced (grown) during call-exit,
	// or individual exits may write results via e.callVM.Regs() which is the
	// latest slice. We must always keep ctx.Regs in sync with the current array.
	resyncRegs := func() {
		if e.callVM == nil {
			return
		}
		regs = e.callVM.Regs()
		ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	}

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			// Normal return: read result from slot 0 (relative to base).
			result := regs[base]
			return []runtime.Value{result}, nil

		case ExitDeopt:
			// JIT bailed out: return error so the VM falls through to interpreter.
			return nil, fmt.Errorf("methodjit: deopt")

		case ExitCallExit:
			// Call-exit: execute the function call via the VM, then resume JIT.
			if err := e.executeCallExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("methodjit: call-exit error: %w", err)
			}
			// The callee may have grown the VM's register file; resync.
			resyncRegs()
			callID := int(ctx.CallID)
			resumeOff, ok := cf.ResumeAddrs[callID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume addr for call %d", callID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitGlobalExit:
			// Global-exit: load a global variable via the VM, then resume JIT.
			if err := e.executeGlobalExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("methodjit: global-exit error: %w", err)
			}
			resyncRegs()
			globalID := int(ctx.GlobalExitID)
			resumeOff, ok := cf.ResumeAddrs[globalID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume addr for global %d", globalID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitTableExit:
			// Table-exit: perform table operation via Go, then resume JIT.
			if err := e.executeTableExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("methodjit: table-exit error: %w", err)
			}
			resyncRegs()
			tableID := int(ctx.TableExitID)
			resumeOff, ok := cf.ResumeAddrs[tableID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume addr for table %d", tableID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitOpExit:
			// Op-exit: execute unsupported operation via Go, then resume JIT.
			if err := e.executeOpExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("methodjit: op-exit: %w", err)
			}
			resyncRegs()
			opID := int(ctx.OpExitID)
			resumeOff, ok := cf.ResumeAddrs[opID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume addr for op %d", opID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("methodjit: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// CompiledCount returns the number of successfully compiled functions.
func (e *MethodJITEngine) CompiledCount() int {
	return len(e.compiled)
}

// FailedCount returns the number of functions that failed compilation.
func (e *MethodJITEngine) FailedCount() int {
	return len(e.failed)
}

