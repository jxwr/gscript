// Package methodjit implements a V8 Maglev-style method JIT compiler.
// It compiles entire functions (not traces) to native ARM64 code via
// a CFG-based SSA intermediate representation.
//
// Architecture:
//
//	Bytecode → GraphBuilder → CFG SSA IR → (future: Optimize → RegAlloc → Emit → ARM64)
//
// The IR uses the Braun et al. algorithm for SSA construction:
// single forward pass, lazy phi insertion, no dominance frontier computation.
package methodjit

import (
	"github.com/gscript/gscript/internal/vm"
)

// Function is the complete IR for one compiled function.
type Function struct {
	Entry   *Block        // entry basic block
	Blocks  []*Block      // all blocks in RPO (reverse postorder)
	Proto   *vm.FuncProto // source bytecode
	NumRegs int           // number of VM registers used
	nextID  int           // next value ID

	// Int48Safe is the set of integer arithmetic SSA value IDs whose runtime
	// result is provably within the int48 signed range. Populated by
	// RangeAnalysisPass. The emitter consults this set to skip the
	// SBFX+CMP+B.NE overflow check for provably safe AddInt/SubInt/MulInt/NegInt.
	Int48Safe map[int]bool

	// IntModNonZeroDivisor is the set of ModInt SSA value IDs whose divisor
	// range excludes zero. Populated by RangeAnalysisPass so the emitter can
	// skip the modulo-by-zero deopt guard at those sites.
	IntModNonZeroDivisor map[int]bool

	// IntModNoSignAdjust is the set of ModInt SSA value IDs whose operand signs
	// prove that ARM64 SDIV/MSUB already matches Lua modulo semantics. Populated
	// by RangeAnalysisPass so the emitter can skip the sign-adjust slow path.
	IntModNoSignAdjust map[int]bool

	// IntRanges records the integer range facts computed by RangeAnalysisPass.
	// Unlike Int48Safe, consumers must treat these facts as optimization hints:
	// missing or unknown ranges mean "top", not failure. OverflowBoxing uses
	// this to distinguish bounded linear inductions from overflow-prone
	// arithmetic recurrences such as multiplicative LCGs.
	IntRanges map[int]intRange

	// Globals, if non-nil, maps global function names to their protos.
	// Used by the IR interpreter to resolve residual cross-function calls
	// (e.g., those left after bounded recursive inlining). Populated by
	// the inline pass when its config includes a globals map. Production
	// code paths never consult this field — it exists only as a hook for
	// the IR correctness oracle.
	Globals map[string]*vm.FuncProto

	// Unpromotable, when true, signals that this function cannot be safely
	// compiled at Tier 2 because BuildGraph encountered bytecode patterns
	// it does not model. Set by the graph builder and checked by
	// compileTier2; an unpromotable function stays at Tier 1.
	//
	// Currently set when OP_CALL B==0 (variadic args threaded via top) is
	// seen: the graph builder cannot statically determine the argument
	// count, so emitting an OpCall would drop arguments and corrupt the
	// call. Patterns like `outer(x, inner(...))` and `return f(g(...))`
	// compile to CALL B=0.
	Unpromotable bool

	// CarryPreheaderInvariants, when true, enables the register allocator
	// to pin LICM-hoisted loop-invariant float values (defined in a
	// pre-header block) into FPRs across loop-body blocks. This avoids
	// per-iteration memory reloads for invariant constants. Set to true
	// by compileTier2 after LICM runs. Defaults to false (Go zero value).
	CarryPreheaderInvariants bool

	// Remarks is an optional diagnostic sink for optimization decisions.
	// Production compiles leave it nil; CompileForDiagnostics wires it so
	// passes can explain important changes and misses without stderr prints.
	Remarks *OptimizationRemarks
}

// newValueID allocates a unique ID for a new SSA value.
func (f *Function) newValueID() int {
	id := f.nextID
	f.nextID++
	return id
}

// Block represents a basic block in the control flow graph.
type Block struct {
	ID     int      // unique block ID
	Instrs []*Instr // instructions (last one is always a terminator)
	Preds  []*Block // predecessor blocks
	Succs  []*Block // successor blocks

	// SSA construction state (used by graph builder, not needed after)
	sealed     bool            // all predecessors known
	incomplete []incompletePhi // phis waiting for predecessors
	defs       map[int]*Value  // slot → current SSA value definition in this block
}

// incompletePhi tracks a phi node that needs more args when predecessors are sealed.
type incompletePhi struct {
	slot int
	phi  *Instr
}

// Instr is one SSA instruction within a basic block.
type Instr struct {
	ID    int      // unique instruction ID (= its Value ID)
	Op    Op       // operation
	Type  Type     // result type
	Args  []*Value // SSA value inputs
	Aux   int64    // auxiliary data (constant value, field index, slot number, etc.)
	Aux2  int64    // second auxiliary (e.g., for Branch: true block ID)
	Block *Block   // owning block

	// Source metadata links this IR instruction back to the bytecode that
	// produced it. HasSource is false for synthetic instructions introduced by
	// passes or CFG repair unless the pass explicitly copies source metadata.
	HasSource  bool
	SourcePC   int
	SourceLine int
}

// Value returns the SSA value produced by this instruction.
func (i *Instr) Value() *Value {
	return &Value{ID: i.ID, Def: i}
}

// Value represents an SSA value (the result of an instruction).
type Value struct {
	ID  int    // unique value ID
	Def *Instr // instruction that defines this value (nil for function parameters)
}

// Type represents the type of an SSA value.
type Type uint8

const (
	TypeUnknown Type = iota
	TypeInt
	TypeFloat
	TypeBool
	TypeString
	TypeTable
	TypeNil
	TypeFunction
	TypeAny // unspecialized (dynamic)
)

var typeNames = [...]string{
	TypeUnknown:  "unknown",
	TypeInt:      "int",
	TypeFloat:    "float",
	TypeBool:     "bool",
	TypeString:   "string",
	TypeTable:    "table",
	TypeNil:      "nil",
	TypeFunction: "function",
	TypeAny:      "any",
}

func (t Type) String() string {
	if int(t) < len(typeNames) {
		return typeNames[t]
	}
	return "?"
}
