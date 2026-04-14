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
	HasLoop       bool // contains FORPREP/FORLOOP or backward JMP
	LoopDepth     int  // maximum nesting depth of loops
	BytecodeCount int  // total number of bytecodes
	ArithCount    int  // ADD/SUB/MUL/DIV/MOD/UNM count
	CallCount     int  // OP_CALL count (static, not runtime)
	TableOpCount  int  // GETTABLE/SETTABLE/GETFIELD/SETFIELD count
	NewTableCount int  // OP_NEWTABLE count (allocation pressure signal)
	HasClosure    bool // contains OP_CLOSURE
	HasUpval      bool // contains OP_GETUPVAL or OP_SETUPVAL
	HasVararg     bool // contains OP_VARARG
	HasGlobal     bool // contains OP_GETGLOBAL or OP_SETGLOBAL
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
// NEWTABLE (and other non-native ops) costs ~100–200ns per exit. For
// tiny recursive allocation builders — the canonical case is
// binary_trees.gs's `makeTree` (21 bytecodes, 2 NEWTABLEs, 4 SETFIELDs,
// 2 CALLs) — this overhead is measurable: Tier 1 runs ~25% slower than
// the interpreter on this shape.
//
// Gate: small (≤ 25 bytecodes) + allocates (NewTableCount > 0) + no
// loops + has calls. The "has calls" clause is important — pure
// allocation without calls would usually be called from a loop
// context where the caller gets the JIT benefit anyway.
func shouldStayTier0(profile FuncProfile) bool {
	return profile.BytecodeCount <= 25 &&
		profile.NewTableCount > 0 &&
		!profile.HasLoop &&
		profile.CallCount > 0
}

// shouldPromoteTier2 decides whether a function should be promoted to Tier 2
// based on its static profile and runtime call count.
func shouldPromoteTier2(proto *vm.FuncProto, profile FuncProfile, runtimeCallCount int) bool {
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

	// Functions with calls (no loops): keep at Tier 1.
	// Tier 1's native BLR handles calls efficiently (~10ns per call).
	// Even after inlining, non-loop functions don't benefit enough from
	// Tier 2's type specialization to justify compilation overhead.
	// Functions with loops + calls are handled by the clause above —
	// compileTier2 will try inlining and reject if calls remain via irHasCall.
	if profile.CallCount > 0 && !profile.HasLoop {
		return false
	}

	// Default: stay at Tier 1. Simple functions without loops, calls, or
	// significant arithmetic don't benefit enough from Tier 2 to justify
	// compilation overhead.
	return false
}
