package jit

// SSA IR opcodes
type SSAOp uint8

const (
	// Guards (side-exit on failure)
	SSA_GUARD_TYPE   SSAOp = iota // guard ref has expected type
	SSA_GUARD_NNIL                // guard ref is not nil
	SSA_GUARD_NOMETA              // guard table has no metatable
	SSA_GUARD_TRUTHY              // guard ref is truthy (AuxInt=0) or falsy (AuxInt=1)

	// Integer arithmetic (unboxed int64)
	SSA_ADD_INT // ref + ref → int
	SSA_SUB_INT // ref - ref → int
	SSA_MUL_INT // ref * ref → int
	SSA_MOD_INT // ref % ref → int
	SSA_NEG_INT // -ref → int

	// Float arithmetic (unboxed float64, SIMD registers)
	SSA_ADD_FLOAT // ref + ref → float
	SSA_SUB_FLOAT // ref - ref → float
	SSA_MUL_FLOAT // ref * ref → float
	SSA_DIV_FLOAT // ref / ref → float
	SSA_NEG_FLOAT // -ref → float
	SSA_FMADD     // Arg1*Arg2 + AuxInt(ref) → float (fused multiply-add)
	SSA_FMSUB     // AuxInt(ref) - Arg1*Arg2 → float (fused multiply-sub)

	// Comparisons (produce bool, used by guards)
	SSA_EQ_INT  // ref == ref
	SSA_LT_INT  // ref < ref
	SSA_LE_INT  // ref <= ref
	SSA_LT_FLOAT // ref < ref (float)
	SSA_LE_FLOAT // ref <= ref (float)
	SSA_GT_FLOAT // ref > ref (float)

	// Memory
	SSA_LOAD_SLOT  // load VM register → boxed value
	SSA_STORE_SLOT // store to VM register
	SSA_UNBOX_INT   // extract int64 from boxed Value
	SSA_BOX_INT     // create boxed Value from int64
	SSA_UNBOX_FLOAT // extract float64 bits from boxed Value
	SSA_BOX_FLOAT   // create boxed Value from float64 bits

	// Table operations
	SSA_LOAD_FIELD  // table.field → value
	SSA_STORE_FIELD // table.field = value
	SSA_LOAD_ARRAY  // table[int] → value
	SSA_STORE_ARRAY // table[int] = value
	SSA_TABLE_LEN   // #table → int
	SSA_LOAD_GLOBAL // load global value from constant pool → register

	// Constants
	SSA_CONST_INT   // immediate int64
	SSA_CONST_FLOAT // immediate float64
	SSA_CONST_NIL
	SSA_CONST_BOOL

	// Control
	SSA_LOOP     // loop header marker
	SSA_PHI      // merge at loop back-edge
	SSA_SNAPSHOT // state capture for side-exit

	// Function calls
	SSA_CALL        // generic call (side-exit)
	SSA_CALL_SELF   // self-recursive call
	SSA_INTRINSIC   // inlined GoFunction (XOR, AND, etc.)

	// Sub-trace calling
	SSA_CALL_INNER_TRACE // call pre-compiled inner loop trace

	// Full nested loop
	SSA_INNER_LOOP // inner loop header marker (label for inner loop back-edge)

	// Misc
	SSA_MOVE     // copy ref
	SSA_NOP      // no operation (placeholder for deleted instructions)
	SSA_SIDE_EXIT // unconditional side-exit
)

// SSA value types
type SSAType uint8

const (
	SSATypeUnknown SSAType = iota
	SSATypeInt
	SSATypeFloat
	SSATypeBool
	SSATypeNil
	SSATypeTable
	SSATypeString
)

// SSARef is a reference to an SSA instruction (index into Insts array).
// Negative values reference constants.
type SSARef int32

const SSARefNone SSARef = -32768

// SSAInst is one SSA instruction.
type SSAInst struct {
	Op     SSAOp
	Type   SSAType  // result type (known at compile time)
	Arg1   SSARef   // first operand
	Arg2   SSARef   // second operand
	Slot   int16    // VM register slot (for LOAD/STORE)
	PC     int      // original bytecode PC (for side-exit)
	AuxInt int64    // auxiliary integer (constants, intrinsic ID)
}

// SSAFunc holds the SSA IR for a compiled trace.
type SSAFunc struct {
	Insts        []SSAInst
	Trace        *Trace          // original trace (for side-exit snapshots)
	AbsorbedMuls map[SSARef]bool // MUL refs absorbed into FMADD/FMSUB (skip in codegen)
}
