//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// SSAOp is an SSA instruction opcode.
type SSAOp int

const (
	// Guards (pre-loop type checks)
	SSA_GUARD_TYPE SSAOp = iota // guard slot type matches expected

	// Slot access
	SSA_LOAD_SLOT  // load value from VM slot → SSA value
	SSA_STORE_SLOT // store SSA value back to VM slot (for store-back)

	// Integer arithmetic
	SSA_ADD_INT
	SSA_SUB_INT
	SSA_MUL_INT
	SSA_MOD_INT
	SSA_NEG_INT
	SSA_DIV_INT // integer division (not in Lua, but for completeness)

	// Float arithmetic
	SSA_ADD_FLOAT
	SSA_SUB_FLOAT
	SSA_MUL_FLOAT
	SSA_DIV_FLOAT
	SSA_NEG_FLOAT
	SSA_FMADD // fused multiply-add: a*b+c
	SSA_FMSUB // fused multiply-sub: a*b-c

	// Unboxing/boxing
	SSA_UNBOX_INT   // NaN-boxed → raw int64
	SSA_UNBOX_FLOAT // NaN-boxed → raw float64
	SSA_BOX_INT     // raw int64 → NaN-boxed
	SSA_BOX_FLOAT   // raw float64 → NaN-boxed

	// Comparisons (produce guard, branch on fail)
	SSA_EQ_INT
	SSA_LT_INT
	SSA_LE_INT
	SSA_LT_FLOAT
	SSA_LE_FLOAT
	SSA_GT_FLOAT

	// Constants
	SSA_CONST_INT
	SSA_CONST_FLOAT
	SSA_CONST_NIL
	SSA_CONST_BOOL

	// Table operations
	SSA_LOAD_FIELD  // R(A) = table.field (string key, known index)
	SSA_STORE_FIELD // table.field = R(C) (string key, known index)
	SSA_LOAD_ARRAY  // R(A) = table[key] (integer index)
	SSA_STORE_ARRAY // table[key] = R(C) (integer index)
	SSA_LOAD_GLOBAL // R(A) = globals[name]
	SSA_TABLE_LEN   // R(A) = #table

	// Guards
	SSA_GUARD_TRUTHY // guard that value is truthy
	SSA_GUARD_NNIL   // guard non-nil
	SSA_GUARD_NOMETA // guard no metatable

	// Control flow
	SSA_LOOP      // loop header marker
	SSA_SIDE_EXIT // explicit side-exit
	SSA_NOP       // no-op (dead code)
	SSA_SNAPSHOT  // snapshot marker (for deopt)

	// Calls
	SSA_CALL              // call-exit: VM executes this instruction
	SSA_CALL_INNER_TRACE  // call a sub-trace
	SSA_INNER_LOOP        // inner loop marker
	SSA_INTRINSIC         // inlined intrinsic (math.sqrt, bit32.*)

	// Data movement
	SSA_MOVE // copy value (register-to-register)
	SSA_PHI  // loop-carried value
)

// SSAType describes the type of an SSA value.
type SSAType int

const (
	SSATypeUnknown SSAType = iota
	SSATypeBool
	SSATypeInt
	SSATypeFloat
	SSATypeTable
	SSATypeString
	SSATypeNil
)

// SSARef is a reference to an SSA instruction (index into SSAFunc.Insts).
type SSARef int32

const SSARefNone SSARef = -1

// SSAInst is a single SSA instruction.
type SSAInst struct {
	Op     SSAOp
	Type   SSAType // result type
	Arg1   SSARef  // first operand (SSA value ref)
	Arg2   SSARef  // second operand
	AuxInt int64   // auxiliary: PC, field index, constant, snapshot index, etc.
	Slot   int16   // source/target VM slot (-1 if none)
	PC     int     // bytecode PC for this instruction
}

// SnapEntry maps a VM slot to an SSA value at a specific program point.
type SnapEntry struct {
	Slot int     // VM slot number
	Ref  SSARef  // SSA value currently in this slot
	Type SSAType // value type (for correct boxing during restore)
}

// Snapshot captures the VM state at a guard/call-exit point.
// On side-exit, the executor restores VM memory from this snapshot.
type Snapshot struct {
	PC      int         // bytecode PC for interpreter recovery
	Entries []SnapEntry // slot → SSA value mappings (only modified slots)
}

// SSAFunc is the SSA representation of a compiled trace.
type SSAFunc struct {
	Insts     []SSAInst
	Snapshots []Snapshot
	Trace     *Trace // source trace (recording data)
	LoopIdx   int    // index of SSA_LOOP marker

	// AbsorbedMuls tracks MUL instructions absorbed by FMADD/FMSUB.
	AbsorbedMuls map[SSARef]bool
}

// TraceIR records one bytecode instruction during trace recording.
type TraceIR struct {
	Op         vm.Opcode
	A, B, C    int
	BX         int
	SBX        int
	PC         int
	Proto      *vm.FuncProto
	Depth      int // inlining depth (0 = top level)
	Base       int // register base at recording time
	AType      runtime.ValueType
	BType      runtime.ValueType
	CType      runtime.ValueType
	FieldIndex int    // for GETFIELD/SETFIELD: skeys index; for FORPREP: inner FORLOOP PC (sub-trace)
	ShapeID    uint32 // for GETFIELD/SETFIELD: table shape ID
	IsSelfCall bool   // true if this OP_CALL is self-recursive
	Intrinsic  int    // recognized GoFunction intrinsic ID (0=none)
}

// Trace holds recorded trace data.
type Trace struct {
	IR           []TraceIR
	ID           int
	LoopPC       int
	LoopProto    *vm.FuncProto
	EntryPC      int
	StartBase    int              // base register index of the traced function
	Constants    []runtime.Value  // trace-level constant pool (includes inlined function constants)
	HasSelfCalls bool             // true if trace contains self-recursive CALL
}

// Intrinsic IDs for recognized GoFunction calls.
const (
	IntrinsicNone   = 0
	IntrinsicBxor   = 1
	IntrinsicBand   = 2
	IntrinsicBor    = 3
	IntrinsicBnot   = 4
	IntrinsicLshift = 5
	IntrinsicRshift = 6
	IntrinsicSqrt   = 7
)
