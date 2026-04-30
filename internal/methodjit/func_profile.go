//go:build darwin && arm64

// func_profile.go implements function profile analysis for smart tiering.
//
// Instead of a simple call-count threshold, the tiering manager analyzes
// each function's bytecodes to determine its characteristics (loops, arithmetic
// density, call patterns, table ops). This profile drives the Tier 2 promotion
// decision:
//
//   - Pure-compute functions with loops: promote immediately (threshold=1)
//   - Functions with calls that can be inlined: promote at threshold=2
//   - Functions with table/field ops: promote at threshold=5
//   - Default: keep at Tier 1

package methodjit

import (
	"github.com/gscript/gscript/internal/vm"
)

// FuncProfile captures the static characteristics of a function's bytecodes.
// Computed once per function prototype and cached by the TieringManager.
type FuncProfile struct {
	HasLoop            bool // contains FORPREP/FORLOOP or backward JMP
	LoopDepth          int  // maximum nesting depth of loops
	BytecodeCount      int  // total number of bytecodes
	ArithCount         int  // ADD/SUB/MUL/DIV/MOD/UNM count
	CallCount          int  // OP_CALL count (static, not runtime)
	TableOpCount       int  // GETTABLE/SETTABLE/GETFIELD/SETFIELD count
	NewTableCount      int  // OP_NEWTABLE/OP_NEWOBJECT2 count (allocation pressure signal)
	EmptyNewTableCount int  // OP_NEWTABLE array=0 hash=0 count (native-cacheable leaf allocation)
	HasClosure         bool // contains OP_CLOSURE
	HasUpval           bool // contains OP_GETUPVAL or OP_SETUPVAL
	HasVararg          bool // contains OP_VARARG
	HasGlobal          bool // contains OP_GETGLOBAL or OP_SETGLOBAL
}

// hasTailCall returns true when the bytecode has an OP_CALL immediately
// followed by an OP_RETURN. Kept as a profiling helper for tests and
// diagnostics; production promotion no longer rejects raw-int self-recursive
// candidates solely for this shape because Tier 2 now lowers static self tail
// calls into in-frame loops and reserves native stack for non-tail recursion.
func hasTailCall(proto *vm.FuncProto) bool {
	for i, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		if i+1 >= len(proto.Code) {
			continue
		}
		if vm.DecodeOp(proto.Code[i+1]) == vm.OP_RETURN {
			return true
		}
	}
	return false
}

// staticallyCallsOnlySelf returns true when the bytecode's GETGLOBAL
// targets are all the proto's own name — i.e. this proto calls only
// itself, no other globals. Used to gate Tier 2 promotion for purely-
// self-recursive 1-param numeric protos (fib/ack): these benefit from
// the numeric calling convention's raw-int BL path. Mutual-recursion
// (F/M in Hofstadter) intentionally returns false — cross-proto BLR is
// faster in Tier 1 than the mixed Tier 2 path that R132 first tried
// (mut_recursion regressed +81% when F/M were promoted).
//
// Requires at least one GETGLOBAL-of-self (else non-calling functions
// would trivially qualify). Purely static — no proto compilation
// needed.
func staticallyCallsOnlySelf(proto *vm.FuncProto) bool {
	if proto == nil || proto.Name == "" {
		return false
	}
	sawSelf := false
	for _, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		bx := vm.DecodeBx(inst)
		if bx < 0 || bx >= len(proto.Constants) {
			return false
		}
		kv := proto.Constants[bx]
		if !kv.IsString() {
			return false
		}
		if kv.Str() != proto.Name {
			return false
		}
		sawSelf = true
	}
	return sawSelf
}

// analyzeFuncProfile scans a function's bytecodes once and returns a FuncProfile.
func analyzeFuncProfile(proto *vm.FuncProto) FuncProfile {
	p := FuncProfile{
		BytecodeCount: len(proto.Code),
	}

	// Track loop nesting via FORPREP/FORLOOP pairs.
	// FORPREP jumps forward to FORLOOP; FORLOOP jumps backward to loop body.
	// A backward JMP also indicates a while-style loop.
	currentLoopDepth := 0

	for pc := 0; pc < len(proto.Code); pc++ {
		inst := proto.Code[pc]
		op := vm.DecodeOp(inst)

		switch op {
		// Arithmetic ops
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_UNM:
			p.ArithCount++

		// Call ops
		case vm.OP_CALL:
			p.CallCount++

		// Table/field ops
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD:
			p.TableOpCount++

		case vm.OP_NEWTABLE:
			p.NewTableCount++
			if vm.DecodeB(inst) == 0 && vm.DecodeC(inst) == 0 {
				p.EmptyNewTableCount++
			}
		case vm.OP_NEWOBJECT2:
			p.NewTableCount++
			p.TableOpCount += 2

		// Loop indicators
		case vm.OP_FORPREP:
			p.HasLoop = true
			currentLoopDepth++
			if currentLoopDepth > p.LoopDepth {
				p.LoopDepth = currentLoopDepth
			}
		case vm.OP_FORLOOP:
			if currentLoopDepth > 0 {
				currentLoopDepth--
			}

		case vm.OP_JMP:
			sbx := vm.DecodesBx(inst)
			target := pc + 1 + sbx
			if target <= pc {
				// Backward jump = while-style loop
				p.HasLoop = true
				if p.LoopDepth == 0 {
					p.LoopDepth = 1
				}
			}

		// Closure/upvalue/vararg
		case vm.OP_CLOSURE:
			p.HasClosure = true
		case vm.OP_GETUPVAL, vm.OP_SETUPVAL:
			p.HasUpval = true
		case vm.OP_VARARG:
			p.HasVararg = true

		// Global access
		case vm.OP_GETGLOBAL, vm.OP_SETGLOBAL:
			p.HasGlobal = true
		}
	}

	return p
}

// shouldStayTier0 decides whether a function is better off interpreted
// than baseline-compiled. The baseline JIT's exit-resume path for
// non-native allocation ops costs ~100–200ns per exit. Tiny recursive
// allocation builders can be slower in Tier 1 when every allocation
// leaves native code.
//
// Empty NEWTABLE sites are excluded from this gate only when every
// allocation in the function is an empty NEWTABLE. Mixed constructors such
// as binary_trees.makeTree still contain residual NEWOBJECT2 exits; putting
// them in Tier 1 clears direct-entry recursion and is slower than Tier 0
// until fixed-shape object construction is native too.
func shouldStayTier0ForProto(proto *vm.FuncProto, profile FuncProfile) bool {
	return profile.BytecodeCount <= 25 &&
		profile.NewTableCount > profile.EmptyNewTableCount &&
		!profile.HasLoop &&
		profile.CallCount > 0
}

func shouldStayTier0(profile FuncProfile) bool {
	return shouldStayTier0ForProto(nil, profile)
}

// shouldStayTier0RecursiveTableWalker catches the non-numeric sibling of the
// raw-int recursion fast path: a tiny self-recursive function that walks table
// fields. Tier 1 pays native-call/frame overhead on every recursive edge, while
// Tier 2 does not yet have a typed table-pointer recursive ABI for this shape.
func shouldStayTier0RecursiveTableWalker(proto *vm.FuncProto, profile FuncProfile) bool {
	if proto == nil || profile.BytecodeCount > 25 || profile.HasLoop || profile.CallCount == 0 {
		return false
	}
	if profile.TableOpCount == 0 || profile.NewTableCount != 0 {
		return false
	}
	if !staticallyCallsOnlySelf(proto) {
		return false
	}
	if ok, _ := qualifyForNumeric(proto); ok {
		return false
	}
	if qualifiesForFixedRecursiveTableFold(proto) {
		return false
	}
	return true
}

// shouldStayTier0CoroutineRuntime keeps coroutine stdlib users on the VM path.
// The interpreter has VM-native fast paths for coroutine.resume/yield/wrap,
// while Tier 1 treats those calls as generic GoFunction exits and adds overhead
// without opening a Tier 2 path.
func shouldStayTier0CoroutineRuntime(proto *vm.FuncProto, profile FuncProfile) bool {
	if proto == nil || profile.CallCount == 0 {
		return false
	}
	for _, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		bx := vm.DecodeBx(inst)
		if bx < 0 || bx >= len(proto.Constants) {
			continue
		}
		c := proto.Constants[bx]
		if c.IsString() && c.Str() == "coroutine" {
			return true
		}
	}
	return false
}

// shouldPromoteTier2 decides whether a function should be promoted to Tier 2
// based on its static profile and runtime call count.
func shouldPromoteTier2(proto *vm.FuncProto, profile FuncProfile, runtimeCallCount int) bool {
	if shouldStayTier1ForBoxedRawIntKernel(proto, profile) {
		return false
	}
	// R72: top-level <main> protos with a driver loop (loop body calls
	// other functions) benefit from Tier 2 because it enables the inline
	// pass to pull callees into main, eliminating Tier 1 → Tier 2 BLR
	// transitions on every loop iter. _main_ is only invoked once by
	// the VM (runtimeCallCount == 1), so the normal `>= 2` threshold
	// would never fire — special-case it.
	if proto.Name == "<main>" && profile.HasLoop && profile.CallCount > 0 {
		return true
	}

	if profile.HasLoop && profile.LoopDepth >= 2 && protoHasMatrixIntrinsicConstants(proto) {
		return true
	}

	// Pure-compute functions with loops (no CALL/GETGLOBAL): promote at threshold=2.
	// Threshold=1 caused regressions on float-heavy functions (mandelbrot)
	// where Tier 2's code was slower than Tier 1. Threshold=2 ensures the
	// function is called at least twice, giving Tier 1 a chance on first call.
	// Uses canPromoteToTier2NoCalls (conservative) to identify functions that
	// don't need the inline pass.
	if profile.HasLoop && profile.ArithCount >= 1 && canPromoteToTier2NoCalls(proto) {
		return runtimeCallCount >= 2
	}

	// Functions with loops + calls: can benefit from Tier 2 exit-resume.
	// Promote at threshold=2 to let feedback stabilize.
	if profile.HasLoop && profile.ArithCount >= 1 {
		return runtimeCallCount >= 2
	}

	// Functions with table/field ops but also loops: promote at threshold=3.
	if profile.HasLoop && profile.TableOpCount > 0 {
		return runtimeCallCount >= 3
	}

	// Recursive-SELF protos that qualify for AnalyzeSpecializedABI's
	// raw-int contract benefit from Tier 2 even without a loop. The
	// numeric body passes raw int64 args via X0-X(N-1), reads parameter
	// loads from those ABI registers, and returns raw int in X0.
	//
	// Gated on HasSelfCalls so non-recursive 1-param wrappers (e.g.
	// `func wrapper(n) { return sum(n) }`) are NOT accidentally
	// promoted: the inline pass would still eliminate their call,
	// then irHasCall is fine, but the round up-front cost isn't worth
	// it for a single static call site.
	//
	if profile.CallCount > 0 && !profile.HasLoop {
		if qualifiesForFixedRecursiveTableBuilder(proto) {
			return runtimeCallCount >= 1
		}
		// proto.HasSelfCalls is only set during compileTier2Pipeline, so
		// at promotion time it's still false — detect recursion by
		// bytecode scan instead.
		if staticallyCallsOnlySelf(proto) {
			// Gate open for all qualifying self-recursive numeric protos.
			// Static self tail calls are lowered to in-frame loops, and
			// executeTier2 reserves stack budget for bounded non-tail native
			// recursion before entering JIT code.
			if ok, _ := qualifyForNumeric(proto); ok {
				return runtimeCallCount >= 2
			}
		}
		// Cross-recursive numeric protos still use the boxed peer-call ABI, but
		// Tier 2 publishes a separate direct entry pointer now. That keeps Tier
		// 2 call ICs stable even when DirectEntryPtr is cleared for baseline
		// callers after a runtime deopt, avoiding the old ExitCallExit storm.
		if qualifiesForNumericCrossRecursiveCandidate(proto) {
			return runtimeCallCount >= 2
		}
		if qualifiesForFixedRecursiveTableFold(proto) {
			return runtimeCallCount >= 2
		}
		// Typed table self-recursive protos can be explicitly compiled to Tier 2,
		// but the private entry still shares the boxed exit contract. Keep
		// automatic promotion closed until the typed success path also has a thin
		// exit-safe frame.
		// Other non-loop call functions stay at Tier 1 for now. Tier 1's
		// native BLR handles calls efficiently; without a raw-int contract,
		// Tier 2 usually does not recover enough call overhead to justify
		// compilation for a tiny non-loop function.
		return false
	}

	// Default: stay at Tier 1. Simple functions without loops, calls, or
	// significant arithmetic don't benefit enough from Tier 2 to justify
	// compilation overhead.
	return false
}

func protoHasMatrixIntrinsicConstants(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	hasMatrix := false
	hasGetSet := false
	for _, c := range proto.Constants {
		if !c.IsString() {
			continue
		}
		switch c.Str() {
		case "matrix":
			hasMatrix = true
		case "getf", "setf":
			hasGetSet = true
		}
	}
	return hasMatrix && hasGetSet
}

// shouldStayTier1ForBoxedRawIntKernel keeps small non-recursive raw-int while
// kernels on the Tier 1 BLR path. Tier 2 can compile these bodies well, but a
// boxed cross-function call pays the full Tier 2 direct-entry frame on every
// invocation. In hot loop-call patterns (math_intensive.gcd_bench), that call
// ABI cost dominates the tiny callee body; Tier 1's baseline direct entry is
// faster until a cross-proto raw-int call ABI exists.
//
// Self-recursive numeric protos are excluded: they use Tier 2's specialized
// raw-int self ABI and are a known win.
func shouldStayTier1ForBoxedRawIntKernel(proto *vm.FuncProto, profile FuncProfile) bool {
	if proto == nil || !profile.HasLoop || profile.CallCount != 0 {
		return false
	}
	if staticallyCallsOnlySelf(proto) {
		return false
	}
	ok, _ := qualifyForNumeric(proto)
	return ok
}
