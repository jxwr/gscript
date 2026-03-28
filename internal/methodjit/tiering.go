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
//  4. On next call after feedback is ready, compiles via BuildGraph → Validate → RegAlloc → Compile.
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
const CompileThreshold = 100

// MethodJITEngine manages compiled function cache and tiering decisions.
type MethodJITEngine struct {
	compiled map[*vm.FuncProto]*CompiledFunction
	failed   map[*vm.FuncProto]bool // functions that failed compilation
}

// NewMethodJITEngine creates a new Method JIT engine with empty caches.
func NewMethodJITEngine() *MethodJITEngine {
	return &MethodJITEngine{
		compiled: make(map[*vm.FuncProto]*CompiledFunction),
		failed:   make(map[*vm.FuncProto]bool),
	}
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

	// Check that all IR ops are supported by the code generator.
	// Functions with unsupported ops (calls, globals, tables, etc.) stay interpreted.
	if !canCompile(fn) {
		e.failed[proto] = true
		return nil
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		e.failed[proto] = true
		return nil
	}

	e.compiled[proto] = cf
	return cf
}

// Execute runs a compiled function using the VM's register file.
// The compiled parameter must be a *CompiledFunction returned by TryCompile.
// The arguments must already be placed at regs[base..base+numParams-1] by the VM.
// Returns the result values read from regs[base] after execution.
func (e *MethodJITEngine) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	cf := compiled.(*CompiledFunction)
	// Ensure we have enough register space for the compiled function's temp slots.
	needed := base + cf.numRegs
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
	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Call the native code.
	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	jit.CallJIT(uintptr(cf.Code.Ptr()), ctxPtr)

	// Read return value from slot 0 (relative to base).
	result := regs[base]
	return []runtime.Value{result}, nil
}

// CompiledCount returns the number of successfully compiled functions.
func (e *MethodJITEngine) CompiledCount() int {
	return len(e.compiled)
}

// FailedCount returns the number of functions that failed compilation.
func (e *MethodJITEngine) FailedCount() int {
	return len(e.failed)
}

// supportedOps is the set of IR ops that the ARM64 code generator can handle.
// Any function containing ops not in this set will stay interpreted.
var supportedOps = map[Op]bool{
	OpConstInt:   true,
	OpConstFloat: true,
	OpConstBool:  true,
	OpConstNil:   true,
	OpLoadSlot:   true,
	OpStoreSlot:  true,
	OpAdd:        true,
	OpSub:        true,
	OpMul:        true,
	OpMod:        true,
	OpAddInt:     true,
	OpSubInt:     true,
	OpMulInt:     true,
	OpModInt:     true,
	OpLt:         true,
	OpLe:         true,
	OpEq:         true,
	OpLtInt:      true,
	OpLeInt:      true,
	OpEqInt:      true,
	OpPhi:        true,
	OpJump:       true,
	OpBranch:     true,
	OpReturn:     true,
	OpNop:        true,
}

// canCompile checks whether all IR instructions in fn use ops that the
// ARM64 code generator supports. Returns false if any unsupported op is found.
func canCompile(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !supportedOps[instr.Op] {
				return false
			}
		}
	}
	return true
}
