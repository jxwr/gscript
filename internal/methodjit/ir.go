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
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// LoopTableArrayFact describes a table-array access admitted by loop-region
// versioning. The preheader has already verified table/metatable/kind and
// loaded stable len/data facts; an existing loop header branch proves the
// access key is below that len on every continuing iteration.
type LoopTableArrayFact struct {
	HeaderBlockID    int
	PreheaderBlockID int
	TableID          int
	TableHeaderID    int
	LenID            int
	DataID           int
	KeyID            int
	Kind             int64
	AccessOp         Op
}

// ProtocolConstCallFoldFact records a callsite whose callee is a guarded
// whole-call protocol and whose integer arguments are compile-time constants or
// guarded stable globals. Codegen guards dependencies before materializing the
// folded Result; guard miss falls back to the normal call-exit path.
type ProtocolConstCallFoldFact struct {
	CalleeProto    *vm.FuncProto
	Result         int64
	GuardConsts    []int
	GuardProtos    []*vm.FuncProto
	IntGuardConsts []int
	IntGuardValues []int64
}

type WholeCallNoResultBatchCall struct {
	FuncConst int
	ArgConsts []int
}

type WholeCallNoResultBatchFact struct {
	LoopBase int
	ExitPC   int
	Calls    []WholeCallNoResultBatchCall
}

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

	// IntNonNegative is the set of integer SSA value IDs whose runtime result is
	// provably >= 0. Populated by RangeAnalysisPass for consumers that only need
	// a sign fact and must not reuse Int48Safe's overflow-specific meaning.
	IntNonNegative map[int]bool

	// TableArrayUpperBoundSafe is the set of table-array access instruction IDs
	// whose key < len check is already guaranteed by an enclosing loop-region
	// fact. The emitter still performs key type and non-negative checks unless
	// separate facts prove those safe.
	TableArrayUpperBoundSafe map[int]bool

	// LoopTableArrayFacts records the table/len/data/key contract behind each
	// TableArrayUpperBoundSafe access. It is diagnostic and a staging point for
	// broader loop-region versioning; codegen treats missing entries as a lack
	// of optimization, not as an error.
	LoopTableArrayFacts map[int]LoopTableArrayFact

	// TableArrayDataPtrs records typed table-array data pointer SSA values. The
	// key is an OpTableArrayData value ID; consumers can resolve it as a raw
	// backing-array pointer only while the matching header guard remains valid.
	TableArrayDataPtrs map[int]TableArrayDataPtrFact

	// Globals, if non-nil, maps global function names to their protos.
	// Used by the IR interpreter to resolve residual cross-function calls
	// (e.g., those left after bounded recursive inlining). Populated by
	// the inline pass when its config includes a globals map. Production
	// code paths never consult this field — it exists only as a hook for
	// the IR correctness oracle.
	Globals map[string]*vm.FuncProto

	// CallABIs records stable callsite ABI facts keyed by OpCall instruction
	// ID. A descriptor is required before codegen may use a specialized
	// cross-proto raw-int call path; OpCall.Type alone is not authoritative.
	CallABIs map[int]CallABIDescriptor

	// ProtocolConstCallFolds records guarded whole-call protocol constants
	// keyed by OpCall instruction ID.
	ProtocolConstCallFolds map[int]ProtocolConstCallFoldFact

	// WholeCallNoResultKernels records stable structural no-result whole-call
	// kernels keyed by OpCall instruction ID. Codegen routes them through a
	// precise op-exit rather than the generic CallExit path.
	WholeCallNoResultKernels map[int]bool

	// WholeCallNoResultBatches records loop-tail no-result whole-call kernel
	// sites that can safely batch future complete loop iterations after the
	// current iteration's final kernel call has run.
	WholeCallNoResultBatches map[int]WholeCallNoResultBatchFact

	// StringConstTables records small immutable lookup tables used by narrow
	// string-format lowerings. CompiledFunction keeps these slices alive after
	// codegen embeds their backing-array addresses.
	StringConstTables [][]runtime.Value

	// StringFormatPatterns records immutable pattern metadata shared by
	// string.format lowerings. Patterns are accepted by syntax shape or guarded
	// constant identity, not by benchmark-specific literal value.
	StringFormatPatterns []string

	// FixedShapeTables records SSA table values whose field layout is known
	// without consulting the runtime field cache. The initial producer is a
	// static table constructor or a call to a function whose every return path
	// creates the same fixed-shape table. Consumers may use this as a guarded
	// shape fact; it is not an aliasing proof and must not remove runtime shape
	// checks by itself.
	FixedShapeTables map[int]FixedShapeTableFact

	// FixedShapeArgFacts records guarded fixed-shape facts keyed by parameter
	// index. These facts come from callsites, not from the callee body, so
	// consumers may use them only through runtime guards such as field-cache
	// shape checks.
	FixedShapeArgFacts map[int]FixedShapeTableFact

	// FixedTableConstructors records OpNewTable values that came from a
	// bytecode-level fixed string-field table constructor. The graph builder
	// keeps the constructor expanded as NewTable+SetField so scalar replacement
	// can still see ordinary field stores; late lowering may combine surviving
	// constructors into OpNewFixedTable for native codegen.
	FixedTableConstructors map[int]FixedTableConstructorFact

	// FixedShapeEntryGuards records parameter shape guards that codegen must
	// execute before entering the optimized body. Once these guards have run,
	// matching FixedShapeArgFacts are safe as callee-local shape facts.
	FixedShapeEntryGuards map[int]FixedShapeTableFact

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
	// to pin selected loop-invariant values across loop-body blocks. Today
	// this covers LICM-hoisted float values in FPRs and typed-array len/data
	// facts in GPRs. Set to true by compileTier2 after LICM runs. Defaults
	// to false (Go zero value).
	CarryPreheaderInvariants bool

	// Remarks is an optional diagnostic sink for optimization decisions.
	// Production compiles leave it nil; CompileForDiagnostics wires it so
	// passes can explain important changes and misses without stderr prints.
	Remarks *OptimizationRemarks
}

// CallABIDescriptor is the stable callsite ABI contract for one OpCall.
// It is intentionally exact: the callee proto, argument/result counts, and
// raw-int parameter/result representations must all match before codegen can
// use a specialized call path.
type CallABIDescriptor struct {
	Callee       *vm.FuncProto
	NumArgs      int
	NumRets      int
	RawIntParams []bool
	RawIntReturn bool
}

// CallABIAnnotationConfig supplies global function facts to the call ABI
// annotation pass. The pass also derives conservative stable globals from the
// current proto when possible.
type CallABIAnnotationConfig struct {
	Globals map[string]*vm.FuncProto
}

// TableArrayDataPtrFact describes the guard-backed ABI contract for a typed
// table-array backing pointer. HeaderID is the OpTableArrayHeader guard that
// proved TableID is a table with the requested array kind and no metatable.
// LenID is optional but present for the normal lowerer shape.
type TableArrayDataPtrFact struct {
	TableID  int
	HeaderID int
	LenID    int
	Kind     int64
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
