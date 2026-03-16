package jit

import "github.com/gscript/gscript/internal/vm"

// OptimizeTrace performs optimization passes on a trace before compilation.
// Returns a new (possibly modified) trace.
func OptimizeTrace(trace *Trace) *Trace {
	ir := trace.IR

	// Pass 1: Remove redundant type guards
	// If a register's type was already checked (and not modified since),
	// subsequent type checks on the same register can be skipped.
	ir = removeRedundantGuards(ir)

	// Pass 2: Mark pure-integer operations to skip type byte writes
	// If a register is only used for integer arithmetic (ADD, SUB, MUL, MOD),
	// the type byte is always TypeInt and doesn't need re-writing.
	ir = markKnownIntRegs(ir)

	// Return optimized trace
	opt := *trace
	opt.IR = ir
	return &opt
}

// removeRedundantGuards removes type guard checks when the type is already known.
// For example, in a loop that does `sum = sum + x`, the ADD guard checks
// sum's type every iteration. After the first iteration, sum is known to be TypeInt.
func removeRedundantGuards(ir []TraceIR) []TraceIR {
	// Track which registers have a known type (set by type-producing ops)
	knownInt := make(map[int]bool)

	for i := range ir {
		switch ir[i].Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_UNM:
			// Result is always int → mark destination as known-int
			knownInt[ir[i].A] = true

		case vm.OP_LOADINT:
			knownInt[ir[i].A] = true

		case vm.OP_FORLOOP:
			// FORLOOP produces int in R(A) and R(A+3)
			knownInt[ir[i].A] = true
			knownInt[ir[i].A+3] = true

		case vm.OP_GETFIELD, vm.OP_GETTABLE, vm.OP_GETGLOBAL, vm.OP_MOVE:
			// Unknown type → invalidate
			delete(knownInt, ir[i].A)

		case vm.OP_SETTABLE, vm.OP_SETFIELD:
			// Doesn't modify the destination register type

		case vm.OP_CALL:
			// Call can modify any register — invalidate all
			// (conservative; could be smarter for known callees)
			knownInt = make(map[int]bool)
		}

		// Mark the IR with type info for the compiler to use
		if ir[i].Op == vm.OP_ADD || ir[i].Op == vm.OP_SUB || ir[i].Op == vm.OP_MUL {
			b := ir[i].B
			c := ir[i].C
			if b < 256 && knownInt[b] {
				ir[i].BType = 0xFF // sentinel: type already known
			}
			if c < 256 && knownInt[c] {
				ir[i].CType = 0xFF // sentinel: type already known
			}
		}
	}

	return ir
}

// markKnownIntRegs identifies registers that are always TypeInt throughout the trace.
// The compiler can skip writing the type byte for these registers.
func markKnownIntRegs(ir []TraceIR) []TraceIR {
	// This is a simple analysis — just tracking which registers are produced
	// by int-only operations. A more sophisticated version would do
	// dataflow analysis across the entire trace.
	return ir // TODO: implement when the write-back path uses this info
}
