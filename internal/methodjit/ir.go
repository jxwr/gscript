// Package methodjit implements a V8 Maglev-style method JIT compiler.
// It compiles entire functions (not traces) to native ARM64 code via
// a CFG-based SSA intermediate representation.
//
// Architecture:
//   Bytecode → GraphBuilder → CFG SSA IR → (future: Optimize → RegAlloc → Emit → ARM64)
//
// The IR uses the Braun et al. algorithm for SSA construction:
// single forward pass, lazy phi insertion, no dominance frontier computation.
package methodjit

import (
	"github.com/gscript/gscript/internal/vm"
)

// Function is the complete IR for one compiled function.
type Function struct {
	Entry   *Block          // entry basic block
	Blocks  []*Block        // all blocks in RPO (reverse postorder)
	Proto   *vm.FuncProto   // source bytecode
	NumRegs int             // number of VM registers used
	nextID  int             // next value ID
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
	sealed     bool             // all predecessors known
	incomplete []incompletePhi  // phis waiting for predecessors
	defs       map[int]*Value   // slot → current SSA value definition in this block
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
