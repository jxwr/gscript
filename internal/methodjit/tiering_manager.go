//go:build darwin && arm64

// tiering_manager.go implements the TieringManager, a multi-tier JIT engine
// that manages automatic promotion from Tier 1 (baseline) to Tier 2 (optimizing).
//
// The TieringManager implements vm.MethodJITEngine and is a drop-in replacement
// for BaselineJITEngine. It delegates to BaselineJITEngine for Tier 1, and uses
// the existing Tier 2 pipeline (BuildGraph → TypeSpec → ConstProp → DCE →
// RegAlloc → Compile) for Tier 2.
//
// Smart tiering strategy (profile-based):
//   - CallCount < 1:                      stay interpreted (return nil)
//   - Pure-compute + loop + arith > 3:    Tier 2 at callCount=1 (immediate)
//   - Dense arithmetic, no calls:         Tier 2 at callCount=1
//   - Loop + calls + arith > 2:           Tier 2 at callCount=2
//   - Loop + table ops:                   Tier 2 at callCount=3
//   - Calls only (no loops):              stay Tier 1 (BLR is faster)
//   - Default:                            stay Tier 1
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
//   - *CompiledFunction:   executed by Tier 2 execute loop

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// inlineMaxCalleeSize is the maximum bytecode count for a callee to be
// considered inlineable during the pre-scan. Callees larger than this
// are not inlined, and functions containing such calls are not promoted
// via the inlining path.
const inlineMaxCalleeSize = 50

// tmDefaultTier2Threshold is the BLR tier-up threshold. Controls when Tier 1's
// BLR call path falls to slow path to give TieringManager.TryCompile a chance
// to promote. With smart tiering, the actual promotion decision is per-function
// based on profile analysis (see shouldPromoteTier2 in func_profile.go).
const tmDefaultTier2Threshold = 2

// osrDefaultIterations is the default number of loop iterations before Tier 1
// triggers an OSR exit. After this many FORLOOP back-edges, the function exits
// with ExitOSR and the TieringManager compiles Tier 2 and re-enters.
const osrDefaultIterations = 1000

// TieringManager manages automatic promotion between Tier 1 and Tier 2.
// It implements vm.MethodJITEngine.
type TieringManager struct {
	tier1           *BaselineJITEngine
	tier2Compiled   map[*vm.FuncProto]*CompiledFunction
	tier2Failed     map[*vm.FuncProto]bool
	callVM          *vm.VM
	tier2Threshold  int // configurable threshold for testing (legacy fallback)
	profileCache    map[*vm.FuncProto]FuncProfile // cached function profiles
}

// NewTieringManager creates a new TieringManager with Tier 1 baseline support
// and Tier 2 optimizing support.
func NewTieringManager() *TieringManager {
	t1 := NewBaselineJITEngine()
	// Tell the Tier 1 engine to fall to slow path (callVM.CallValue) for callees
	// that have reached the Tier 2 threshold. The slow path goes through the VM's
	// call() which calls TieringManager.TryCompile(), enabling Tier 2 promotion.
	t1.SetTierUpThreshold(tmDefaultTier2Threshold)
	tm := &TieringManager{
		tier1:          t1,
		tier2Compiled:  make(map[*vm.FuncProto]*CompiledFunction),
		tier2Failed:    make(map[*vm.FuncProto]bool),
		tier2Threshold: tmDefaultTier2Threshold,
		profileCache:   make(map[*vm.FuncProto]FuncProfile),
	}
	// Wire the outer compiler so handleCallFast routes through TieringManager
	t1.SetOuterCompiler(func(proto *vm.FuncProto) interface{} {
		return tm.TryCompile(proto)
	})
	return tm
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

// getProfile returns a cached FuncProfile for the given proto, computing it
// on first access.
func (tm *TieringManager) getProfile(proto *vm.FuncProto) FuncProfile {
	if p, ok := tm.profileCache[proto]; ok {
		return p
	}
	p := analyzeFuncProfile(proto)
	tm.profileCache[proto] = p
	return p
}

// TryCompile checks if a function should be compiled and returns the compiled
// code. Uses smart tiering: analyzes function characteristics (loops, arithmetic
// density, call patterns) to decide promotion thresholds instead of a simple
// call count.
func (tm *TieringManager) TryCompile(proto *vm.FuncProto) interface{} {
	// Already at Tier 2? Return cached.
	if t2, ok := tm.tier2Compiled[proto]; ok {
		return t2
	}

	// Below Tier 1 threshold? Stay interpreted.
	if proto.CallCount < BaselineCompileThreshold {
		return nil
	}

	// Get the function profile (cached after first computation).
	profile := tm.getProfile(proto)

	// Use smart tiering to decide if this function should be promoted to Tier 2.
	if !shouldPromoteTier2(proto, profile, proto.CallCount) {
		// Standard rules say no. Check if inlining could make it worthwhile:
		// functions with calls + arithmetic that are called enough times AND
		// whose calls are all inlineable.
		if profile.CallCount > 0 && profile.ArithCount > 0 && proto.CallCount >= 2 {
			inlineGlobals := tm.buildInlineGlobals()
			if canPromoteWithInlining(proto, inlineGlobals) {
				// Fall through to Tier 2 compilation with inlining.
				goto tryTier2
			}
		}
		// Not ready for Tier 2: use Tier 1, but enable OSR for loop-heavy
		// functions so they can be upgraded mid-execution if they run hot.
		t1 := tm.tier1.TryCompile(proto)
		// OSR disabled for now: mandelbrot's Tier 2 float code is slower than Tier 1.
		// Re-enable once Tier 2 float handling is fully optimized.
		// if profile.HasLoop && !tm.tier2Failed[proto] {
		// 	tm.tier1.SetOSRCounter(proto, osrDefaultIterations)
		// }
		return t1
	}

tryTier2:

	// Tier 2 already failed? Use Tier 1.
	if tm.tier2Failed[proto] {
		return tm.tier1.TryCompile(proto)
	}

	// Ensure Tier 1 is compiled first (needed as deopt fallback).
	t1 := tm.tier1.TryCompile(proto)

	// Ensure feedback is initialized for type specialization.
	// Initialize now if needed -- TypeSpecializePass uses SSA-local inference
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
// compiled type. Handles OSR: if Tier 1 exits with an OSR request, compiles
// Tier 2 and re-enters the function from the start at Tier 2 speed.
func (tm *TieringManager) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	switch c := compiled.(type) {
	case *BaselineFunc:
		results, err := tm.tier1.Execute(c, regs, base, proto)
		if err == errOSRRequested {
			return tm.handleOSR(regs, base, proto)
		}
		return results, err
	case *CompiledFunction:
		return tm.executeTier2(c, regs, base, proto)
	default:
		return nil, fmt.Errorf("tiering: unknown compiled type %T", compiled)
	}
}

// handleOSR compiles the function at Tier 2 and re-enters it from the start.
// The register file already has the function's arguments from the original call.
// This is a simplified OSR: instead of entering at the loop header, we restart
// the entire function at Tier 2. The restart overhead is negligible compared to
// long-running loops (e.g., mandelbrot(1000) with 1M iterations).
func (tm *TieringManager) handleOSR(regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	// Ensure feedback is initialized.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}

	// Try to compile at Tier 2.
	t2, err := tm.compileTier2(proto)
	if err != nil {
		// Tier 2 compilation failed. Disable OSR for this function and
		// re-run at Tier 1 from the start with OSR disabled.
		tm.tier2Failed[proto] = true
		tm.tier1.SetOSRCounter(proto, -1) // disable OSR
		t1 := tm.tier1.TryCompile(proto)
		if t1 == nil {
			return nil, fmt.Errorf("tiering: OSR fallback failed: no Tier 1 code")
		}
		return tm.tier1.Execute(t1, regs, base, proto)
	}

	// Cache the Tier 2 compilation for future calls.
	tm.tier2Compiled[proto] = t2
	if t2.DirectEntryOffset > 0 {
		proto.DirectEntryPtr = uintptr(t2.Code.Ptr()) + uintptr(t2.DirectEntryOffset)
	}

	// Re-enter the function from the start at Tier 2.
	return tm.executeTier2(t2, regs, base, proto)
}

// compileTier2 compiles a function at Tier 2 (optimizing).
// Uses the pipeline: BuildGraph → TypeSpec → [Inline →] ConstProp →
// DCE → RegAlloc → Compile.

// canPromoteToTier2 checks if a function is safe for Tier 2 compilation.
// Most ops are now supported via exit-resume (exit to Go, execute, resume JIT).
// Only ops requiring special VM state (closures, upvalues, vararg) are blocked.
//
// Supported via exit-resume:
//   - CALL: emitCallExit
//   - GETGLOBAL, SETGLOBAL: emitGlobalExit / emitOpExit
//   - GETTABLE, SETTABLE: emitGetTableExit / emitSetTableExit
//   - GETFIELD, SETFIELD: emitGetField / emitSetField (inline cache + exit)
//   - NEWTABLE, SETLIST, APPEND, LEN, CONCAT, SELF: emitOpExit
func canPromoteToTier2(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		// Ops that Tier 2 doesn't handle natively yet.
		// These either need inline cache (Tier 1 is better) or Go allocation.
		case vm.OP_CALL, vm.OP_CLOSURE, vm.OP_GETGLOBAL, vm.OP_SETGLOBAL,
			vm.OP_NEWTABLE, vm.OP_SETLIST, vm.OP_VARARG, vm.OP_SELF,
			vm.OP_CONCAT, vm.OP_GETUPVAL, vm.OP_SETUPVAL,
			vm.OP_GETFIELD, vm.OP_SETFIELD, vm.OP_GETTABLE, vm.OP_SETTABLE,
			vm.OP_APPEND, vm.OP_LEN:
			return false
		}
	}
	return true
}

// canPromoteWithInlining checks if a function whose only blocker is OP_CALL
// (with OP_GETGLOBAL for the callee) can be promoted by inlining all calls.
// Returns true if ALL calls in the function are to known, small, non-recursive
// global functions. The inline pass will then eliminate those calls.
func canPromoteWithInlining(proto *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if len(globals) == 0 {
		return false
	}
	hasCall := false
	for i, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CALL:
			hasCall = true
			callA := vm.DecodeA(inst)
			if !findInlineableGetGlobal(proto, i, callA, globals) {
				return false
			}
		case vm.OP_GETGLOBAL:
			// GETGLOBAL is needed for CALL resolution — allowed
			continue
		case vm.OP_CLOSURE, vm.OP_SETGLOBAL,
			vm.OP_NEWTABLE, vm.OP_SETLIST, vm.OP_VARARG, vm.OP_SELF,
			vm.OP_CONCAT, vm.OP_GETUPVAL, vm.OP_SETUPVAL,
			vm.OP_GETFIELD, vm.OP_SETFIELD, vm.OP_GETTABLE, vm.OP_SETTABLE,
			vm.OP_APPEND, vm.OP_LEN:
			// Other blocking ops remain after inlining → can't promote
			return false
		}
	}
	return hasCall
}

// findInlineableGetGlobal scans backwards from callPC to find the GETGLOBAL
// that loads the function into register targetReg. Returns true if the callee
// is in globals, small enough, and non-recursive.
func findInlineableGetGlobal(proto *vm.FuncProto, callPC, targetReg int, globals map[string]*vm.FuncProto) bool {
	for j := callPC - 1; j >= 0; j-- {
		prev := proto.Code[j]
		prevOp := vm.DecodeOp(prev)
		if prevOp == vm.OP_GETGLOBAL && vm.DecodeA(prev) == targetReg {
			bx := vm.DecodeBx(prev)
			if bx < 0 || bx >= len(proto.Constants) {
				return false
			}
			name := proto.Constants[bx].Str()
			callee, ok := globals[name]
			if !ok {
				return false
			}
			// Check size budget.
			if len(callee.Code) > inlineMaxCalleeSize {
				return false
			}
			// Check not recursive.
			if isRecursive(callee) {
				return false
			}
			// Check callee name != caller name (mutual recursion).
			if callee.Name == proto.Name {
				return false
			}
			// Check callee has no loops (while-loops produce buggy
			// code when inlined into the caller's IR).
			calleeProfile := analyzeFuncProfile(callee)
			if calleeProfile.HasLoop {
				return false
			}
			return true
		}
		// If another instruction writes to targetReg before we find GETGLOBAL,
		// the function reference is not from a GETGLOBAL. Bail out.
		if prevOp != vm.OP_GETGLOBAL && vm.DecodeA(prev) == targetReg {
			return false
		}
	}
	return false
}

// buildInlineGlobals extracts global function protos from the VM's globals.
// This is used by the inline pass to resolve callee functions at compile time.
func (tm *TieringManager) buildInlineGlobals() map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if tm.callVM == nil {
		return globals
	}
	for _, val := range tm.callVM.Globals() {
		if !val.IsFunction() {
			continue
		}
		ptr := val.Ptr()
		if ptr == nil {
			continue
		}
		if cl, ok := ptr.(*vm.Closure); ok && cl != nil && cl.Proto != nil {
			globals[cl.Proto.Name] = cl.Proto
		}
	}
	return globals
}

func (tm *TieringManager) compileTier2(proto *vm.FuncProto) (cf *CompiledFunction, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			cf = nil
			retErr = fmt.Errorf("tier2: panic during compilation: %v", r)
		}
	}()

	// Check if function can be promoted to Tier 2.
	inlineGlobals := tm.buildInlineGlobals()
	needsInlining := false
	if !canPromoteToTier2(proto) {
		// Standard check failed — try inlining-aware check.
		if canPromoteWithInlining(proto, inlineGlobals) {
			needsInlining = true
		} else {
			return nil, fmt.Errorf("tier2: function has unsupported ops, staying at tier 1")
		}
	}

	// Build SSA IR.
	fn := BuildGraph(proto)

	// Validate.
	if errs := Validate(fn); len(errs) > 0 {
		return nil, fmt.Errorf("tier2: validation failed: %v", errs[0])
	}

	// Run optimization passes.
	fn, _ = TypeSpecializePass(fn)

	// Inline small monomorphic callees if the function needs inlining to qualify.
	if needsInlining && len(inlineGlobals) > 0 {
		config := InlineConfig{Globals: inlineGlobals, MaxSize: inlineMaxCalleeSize}
		fn, _ = InlinePassWith(config)(fn)
		// Re-run TypeSpec after inlining (new optimization opportunities from
		// cross-function type propagation).
		fn, _ = TypeSpecializePass(fn)
	}

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
// This is the Tier 2 execute loop, handling exit codes and resuming JIT code.
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
