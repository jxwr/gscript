package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// SSA IR opcodes
type SSAOp uint8

const (
	// Guards (side-exit on failure)
	SSA_GUARD_TYPE SSAOp = iota // guard ref has expected type
	SSA_GUARD_NNIL              // guard ref is not nil
	SSA_GUARD_NOMETA            // guard table has no metatable

	// Integer arithmetic (unboxed int64)
	SSA_ADD_INT // ref + ref → int
	SSA_SUB_INT // ref - ref → int
	SSA_MUL_INT // ref * ref → int
	SSA_MOD_INT // ref % ref → int
	SSA_NEG_INT // -ref → int

	// Comparisons (produce bool, used by guards)
	SSA_EQ_INT  // ref == ref
	SSA_LT_INT  // ref < ref
	SSA_LE_INT  // ref <= ref

	// Memory
	SSA_LOAD_SLOT  // load VM register → boxed value
	SSA_STORE_SLOT // store to VM register
	SSA_UNBOX_INT  // extract int64 from boxed Value
	SSA_BOX_INT    // create boxed Value from int64

	// Table operations
	SSA_LOAD_FIELD  // table.field → value
	SSA_STORE_FIELD // table.field = value
	SSA_LOAD_ARRAY  // table[int] → value
	SSA_STORE_ARRAY // table[int] = value
	SSA_TABLE_LEN   // #table → int

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
	SSATypeFunc
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
	Insts []SSAInst
	Trace *Trace // original trace (for side-exit snapshots)
}

// BuildSSA converts a Trace into SSA IR with type inference.
func BuildSSA(trace *Trace) *SSAFunc {
	b := &ssaBuilder{
		trace:    trace,
		slotDefs: make(map[int]SSARef), // current SSA ref for each VM slot
		slotType: make(map[int]SSAType), // known type for each VM slot
	}
	return b.build()
}

type ssaBuilder struct {
	trace    *Trace
	insts    []SSAInst
	slotDefs map[int]SSARef  // VM register → current SSA definition
	slotType map[int]SSAType // VM register → known type
}

func (b *ssaBuilder) emit(inst SSAInst) SSARef {
	ref := SSARef(len(b.insts))
	b.insts = append(b.insts, inst)
	return ref
}

func (b *ssaBuilder) build() *SSAFunc {
	// Phase 1: Scan trace to determine initial slot types from recording
	for _, ir := range b.trace.IR {
		if ir.BType == runtime.TypeInt {
			b.slotType[ir.B] = SSATypeInt
		}
		if ir.CType == runtime.TypeInt {
			b.slotType[ir.C] = SSATypeInt
		}
	}

	// Phase 2: Emit guards for loop entry (type checks for used slots)
	guardedSlots := make(map[int]bool)
	for _, ir := range b.trace.IR {
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
			if ir.B < 256 && !guardedSlots[ir.B] {
				b.emitGuard(ir.B, ir.BType, ir.PC)
				guardedSlots[ir.B] = true
			}
			if ir.C < 256 && !guardedSlots[ir.C] {
				b.emitGuard(ir.C, ir.CType, ir.PC)
				guardedSlots[ir.C] = true
			}
		case vm.OP_FORLOOP:
			// Guard loop control registers
			for _, slot := range []int{ir.A, ir.A + 1, ir.A + 2} {
				if !guardedSlots[slot] {
					b.emitGuard(slot, runtime.TypeInt, ir.PC)
					guardedSlots[slot] = true
				}
			}
		}
	}

	// Phase 3: Emit LOOP marker
	b.emit(SSAInst{Op: SSA_LOOP})

	// Phase 4: Convert each trace instruction to SSA
	for _, ir := range b.trace.IR {
		b.convertIR(&ir)
	}

	return &SSAFunc{Insts: b.insts, Trace: b.trace}
}

func (b *ssaBuilder) emitGuard(slot int, typ runtime.ValueType, pc int) {
	loadRef := b.emit(SSAInst{
		Op:   SSA_LOAD_SLOT,
		Type: SSATypeUnknown,
		Slot: int16(slot),
		PC:   pc,
	})
	b.slotDefs[slot] = loadRef

	var ssaType SSAType
	switch typ {
	case runtime.TypeInt:
		ssaType = SSATypeInt
	case runtime.TypeFloat:
		ssaType = SSATypeFloat
	case runtime.TypeTable:
		ssaType = SSATypeTable
	case runtime.TypeString:
		ssaType = SSATypeString
	default:
		ssaType = SSATypeUnknown
	}

	b.emit(SSAInst{
		Op:     SSA_GUARD_TYPE,
		Type:   ssaType,
		Arg1:   loadRef,
		AuxInt: int64(typ),
		PC:     pc,
	})

	// After guard, the slot has known type
	b.slotType[slot] = ssaType

	// If int, emit unbox
	if ssaType == SSATypeInt {
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_INT,
			Type: SSATypeInt,
			Arg1: loadRef,
			Slot: int16(slot),
		})
		b.slotDefs[slot] = unboxRef
	}
}

func (b *ssaBuilder) convertIR(ir *TraceIR) {
	switch ir.Op {
	case vm.OP_ADD:
		b.convertArith(ir, SSA_ADD_INT)
	case vm.OP_SUB:
		b.convertArith(ir, SSA_SUB_INT)
	case vm.OP_MUL:
		b.convertArith(ir, SSA_MUL_INT)
	case vm.OP_MOD:
		b.convertArith(ir, SSA_MOD_INT)
	case vm.OP_UNM:
		src := b.getSlotRef(ir.B)
		ref := b.emit(SSAInst{Op: SSA_NEG_INT, Type: SSATypeInt, Arg1: src, PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_FORLOOP:
		b.convertForLoop(ir)

	case vm.OP_FORPREP:
		// FORPREP: R(A) -= R(A+2)
		init := b.getSlotRef(ir.A)
		step := b.getSlotRef(ir.A + 2)
		ref := b.emit(SSAInst{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: init, Arg2: step, PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_MOVE:
		src := b.getSlotRef(ir.B)
		b.slotDefs[ir.A] = src
		b.slotType[ir.A] = b.slotType[ir.B]

	case vm.OP_LOADINT:
		ref := b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: int64(ir.SBX), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	default:
		// Unsupported op → side-exit marker
		b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
	}
}

func (b *ssaBuilder) convertArith(ir *TraceIR, op SSAOp) {
	arg1 := b.getSlotOrRK(ir.B)
	arg2 := b.getSlotOrRK(ir.C)
	ref := b.emit(SSAInst{Op: op, Type: SSATypeInt, Arg1: arg1, Arg2: arg2, PC: ir.PC})
	b.slotDefs[ir.A] = ref
	b.slotType[ir.A] = SSATypeInt
}

func (b *ssaBuilder) convertForLoop(ir *TraceIR) {
	// idx += step
	idx := b.getSlotRef(ir.A)
	step := b.getSlotRef(ir.A + 2)
	newIdx := b.emit(SSAInst{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: idx, Arg2: step, PC: ir.PC})
	b.slotDefs[ir.A] = newIdx

	// R(A+3) = idx (loop variable)
	b.slotDefs[ir.A+3] = newIdx
	b.slotType[ir.A+3] = SSATypeInt

	// Compare: idx <= limit
	limit := b.getSlotRef(ir.A + 1)
	b.emit(SSAInst{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: newIdx, Arg2: limit, PC: ir.PC})
}

func (b *ssaBuilder) getSlotRef(slot int) SSARef {
	if ref, ok := b.slotDefs[slot]; ok {
		return ref
	}
	// Not yet defined → load from memory
	ref := b.emit(SSAInst{Op: SSA_LOAD_SLOT, Type: b.slotType[slot], Slot: int16(slot)})
	b.slotDefs[slot] = ref
	return ref
}

func (b *ssaBuilder) getSlotOrRK(idx int) SSARef {
	if idx >= vm.RKBit {
		// Constant from pool
		constIdx := idx - vm.RKBit
		if constIdx < len(b.trace.Constants) {
			c := b.trace.Constants[constIdx]
			if c.IsInt() {
				return b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: c.Int()})
			}
		}
		return b.emit(SSAInst{Op: SSA_LOAD_SLOT, Type: SSATypeUnknown, Slot: int16(idx)})
	}
	return b.getSlotRef(idx)
}

// OptimizeSSA runs optimization passes on the SSA IR.
func OptimizeSSA(f *SSAFunc) *SSAFunc {
	// Pass 1: Guard hoisting — guards are already at the top (before LOOP)
	// This is ensured by BuildSSA's structure.

	// Pass 2: Dead code elimination
	f = eliminateDeadCode(f)

	return f
}

// eliminateDeadCode removes SSA instructions whose results are never used.
func eliminateDeadCode(f *SSAFunc) *SSAFunc {
	// Count references to each instruction
	refCount := make([]int, len(f.Insts))
	for _, inst := range f.Insts {
		if inst.Arg1 >= 0 && int(inst.Arg1) < len(refCount) {
			refCount[inst.Arg1]++
		}
		if inst.Arg2 >= 0 && int(inst.Arg2) < len(refCount) {
			refCount[inst.Arg2]++
		}
	}

	// Mark side-effecting instructions as live
	for i, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
			SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY,
			SSA_LOOP, SSA_SNAPSHOT, SSA_SIDE_EXIT,
			SSA_LE_INT, SSA_LT_INT, SSA_EQ_INT,
			SSA_CALL, SSA_CALL_SELF:
			refCount[i]++ // keep alive
		}
	}

	// NOP out dead instructions
	for i := range f.Insts {
		if refCount[i] == 0 && f.Insts[i].Op != SSA_NOP {
			f.Insts[i].Op = SSA_NOP
		}
	}

	return f
}

// ssaTypFromRuntime converts runtime.ValueType to SSAType.
func ssaTypFromRuntime(t runtime.ValueType) SSAType {
	switch t {
	case runtime.TypeInt:
		return SSATypeInt
	case runtime.TypeFloat:
		return SSATypeFloat
	case runtime.TypeBool:
		return SSATypeBool
	case runtime.TypeNil:
		return SSATypeNil
	case runtime.TypeTable:
		return SSATypeTable
	case runtime.TypeString:
		return SSATypeString
	default:
		return SSATypeUnknown
	}
}
