package jit

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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
		} else if ir.BType == runtime.TypeFloat {
			b.slotType[ir.B] = SSATypeFloat
		}
		if ir.CType == runtime.TypeInt {
			b.slotType[ir.C] = SSATypeInt
		} else if ir.CType == runtime.TypeFloat {
			b.slotType[ir.C] = SSATypeFloat
		}
	}

	// Phase 2: Emit guards for loop entry (type checks for used slots)
	guardedSlots := make(map[int]bool)
	for _, ir := range b.trace.IR {
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV,
			vm.OP_LT, vm.OP_LE:
			if ir.B < 256 && !guardedSlots[ir.B] {
				b.emitGuard(ir.B, ir.BType, ir.PC)
				guardedSlots[ir.B] = true
			}
			if ir.C < 256 && !guardedSlots[ir.C] {
				b.emitGuard(ir.C, ir.CType, ir.PC)
				guardedSlots[ir.C] = true
			}
		case vm.OP_UNM:
			if ir.B < 256 && !guardedSlots[ir.B] {
				b.emitGuard(ir.B, ir.BType, ir.PC)
				guardedSlots[ir.B] = true
			}
		case vm.OP_GETTABLE, vm.OP_SETTABLE:
			// Guard table slot (B for GETTABLE, A for SETTABLE)
			tableSlot := ir.B
			if ir.Op == vm.OP_SETTABLE {
				tableSlot = ir.A
			}
			if tableSlot < 256 && !guardedSlots[tableSlot] {
				b.emitGuard(tableSlot, runtime.TypeTable, ir.PC)
				guardedSlots[tableSlot] = true
			}
			// Guard key slot
			keySlot := ir.C
			if ir.Op == vm.OP_SETTABLE {
				keySlot = ir.B
			}
			if keySlot < 256 && !guardedSlots[keySlot] {
				b.emitGuard(keySlot, ir.CType, ir.PC)
				guardedSlots[keySlot] = true
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
	for i := range b.trace.IR {
		b.convertIR(i, &b.trace.IR[i])
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

	// Emit unbox for known types
	if ssaType == SSATypeInt {
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_INT,
			Type: SSATypeInt,
			Arg1: loadRef,
			Slot: int16(slot),
		})
		b.slotDefs[slot] = unboxRef
	} else if ssaType == SSATypeFloat {
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_FLOAT,
			Type: SSATypeFloat,
			Arg1: loadRef,
			Slot: int16(slot),
		})
		b.slotDefs[slot] = unboxRef
	}
}

func (b *ssaBuilder) convertIR(idx int, ir *TraceIR) {
	switch ir.Op {
	case vm.OP_ADD:
		b.convertArithTyped(ir, SSA_ADD_INT, SSA_ADD_FLOAT)
	case vm.OP_SUB:
		b.convertArithTyped(ir, SSA_SUB_INT, SSA_SUB_FLOAT)
	case vm.OP_MUL:
		b.convertArithTyped(ir, SSA_MUL_INT, SSA_MUL_FLOAT)
	case vm.OP_DIV:
		b.convertArith(ir, SSA_DIV_FLOAT) // division always float
	case vm.OP_MOD:
		b.convertArith(ir, SSA_MOD_INT)
	case vm.OP_UNM:
		src := b.getSlotRef(ir.B)
		ref := b.emit(SSAInst{Op: SSA_NEG_INT, Type: SSATypeInt, Arg1: src, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_FORLOOP:
		b.convertForLoop(ir)

	case vm.OP_FORPREP:
		// FORPREP: R(A) -= R(A+2)
		init := b.getSlotRef(ir.A)
		step := b.getSlotRef(ir.A + 2)
		ref := b.emit(SSAInst{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: init, Arg2: step, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_MOVE:
		src := b.getSlotRef(ir.B)
		// Emit an actual SSA_MOVE to copy the value from slot B to slot A.
		// A simple alias (slotDefs[A] = src) breaks loop-carried values because
		// the source slot's register is never copied to the destination slot's register,
		// causing stale reads on the next loop iteration.
		ref := b.emit(SSAInst{Op: SSA_MOVE, Type: b.slotType[ir.B], Arg1: src, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = b.slotType[ir.B]

	case vm.OP_LOADINT:
		ref := b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: int64(ir.SBX), Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_LOADK:
		if ir.BX < len(b.trace.Constants) {
			c := b.trace.Constants[ir.BX]
			if c.IsInt() {
				ref := b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: c.Int(), Slot: int16(ir.A), PC: ir.PC})
				b.slotDefs[ir.A] = ref
				b.slotType[ir.A] = SSATypeInt
			} else if c.IsFloat() {
				ref := b.emit(SSAInst{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: int64(math.Float64bits(c.Float())), Slot: int16(ir.A), PC: ir.PC})
				b.slotDefs[ir.A] = ref
				b.slotType[ir.A] = SSATypeFloat
			} else {
				b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
			}
		} else {
			b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
		}

	case vm.OP_LOADBOOL:
		ref := b.emit(SSAInst{Op: SSA_CONST_BOOL, Type: SSATypeBool, AuxInt: int64(ir.B), Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeBool

	case vm.OP_LT:
		b.convertComparison(idx, ir, SSA_LT_INT, SSA_LT_FLOAT)

	case vm.OP_LE:
		b.convertComparison(idx, ir, SSA_LE_INT, SSA_LE_FLOAT)

	case vm.OP_EQ:
		// EQ guard: if (RK(B) == RK(C)) != bool(A) then skip
		arg1 := b.getSlotOrRK(ir.B)
		arg2 := b.getSlotOrRK(ir.C)
		b.emit(SSAInst{Op: SSA_EQ_INT, Type: SSATypeBool, Arg1: arg1, Arg2: arg2, AuxInt: int64(ir.A), PC: ir.PC})

	case vm.OP_TEST:
		// TEST A C: if (Truthy(R(A)) ~= bool(C)) then skip
		// Detect skip/no-skip by checking if next instruction is JMP.
		didSkip := true
		if idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP {
			didSkip = false
		}
		// AuxInt: 0=expect truthy, 1=expect falsy
		auxInt := int64(ir.C)
		if !didSkip {
			auxInt = 1 - auxInt
		}
		src := b.getSlotRef(ir.A)
		b.emit(SSAInst{Op: SSA_GUARD_TRUTHY, Type: SSATypeBool, Arg1: src, Slot: int16(ir.A), AuxInt: auxInt, PC: ir.PC})

	case vm.OP_GETTABLE:
		// GETTABLE A B C: R(A) = R(B)[RK(C)]
		// Emit SSA_LOAD_ARRAY: reads from table (slot B) at integer key (RK(C))
		tableRef := b.getSlotRef(ir.B)
		keyRef := b.getSlotOrRK(ir.C)
		ref := b.emit(SSAInst{
			Op: SSA_LOAD_ARRAY, Type: SSATypeUnknown,
			Arg1: tableRef, Arg2: keyRef,
			Slot: int16(ir.A), PC: ir.PC,
		})
		b.slotDefs[ir.A] = ref
		// Result type is unknown (could be any value from the table)

	case vm.OP_SETTABLE:
		// SETTABLE A B C: R(A)[RK(B)] = RK(C)
		tableRef := b.getSlotRef(ir.A)
		keyRef := b.getSlotOrRK(ir.B)
		valRef := b.getSlotOrRK(ir.C)
		b.emit(SSAInst{
			Op: SSA_STORE_ARRAY, Type: SSATypeUnknown,
			Arg1: tableRef, Arg2: keyRef,
			Slot: int16(ir.A), PC: ir.PC,
			AuxInt: int64(valRef), // store value ref in AuxInt
		})

	case vm.OP_JMP:
		// JMP in trace body: no-op (trace is linear; guards handle branching)

	default:
		// Unsupported op → side-exit marker
		b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
	}
}

func (b *ssaBuilder) convertArith(ir *TraceIR, op SSAOp) {
	arg1 := b.getSlotOrRK(ir.B)
	arg2 := b.getSlotOrRK(ir.C)
	typ := SSATypeInt
	if isFloatOp(op) {
		typ = SSATypeFloat
	}
	ref := b.emit(SSAInst{Op: op, Type: typ, Arg1: arg1, Arg2: arg2, Slot: int16(ir.A), PC: ir.PC})
	b.slotDefs[ir.A] = ref
	b.slotType[ir.A] = typ
}

// convertArithTyped picks the int or float SSA op based on operand types.
func (b *ssaBuilder) convertArithTyped(ir *TraceIR, intOp, floatOp SSAOp) {
	// Determine operand types
	bType := b.slotType[ir.B]
	cType := b.slotType[ir.C]
	if ir.B >= vm.RKBit {
		bType = ssaTypFromRuntime(ir.BType)
	}
	if ir.C >= vm.RKBit {
		cType = ssaTypFromRuntime(ir.CType)
	}

	// If either operand is float, use float op
	if bType == SSATypeFloat || cType == SSATypeFloat {
		b.convertArith(ir, floatOp)
	} else {
		b.convertArith(ir, intOp)
	}
}

func isFloatOp(op SSAOp) bool {
	switch op {
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
		SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	}
	return false
}

func (b *ssaBuilder) convertForLoop(ir *TraceIR) {
	// idx += step
	idx := b.getSlotRef(ir.A)
	step := b.getSlotRef(ir.A + 2)
	newIdx := b.emit(SSAInst{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: idx, Arg2: step, Slot: int16(ir.A), PC: ir.PC})
	b.slotDefs[ir.A] = newIdx

	// R(A+3) = idx (loop variable) — emit explicit MOVE so liveness analysis
	// sees that slot A+3 is written and needs store-back.
	moveRef := b.emit(SSAInst{Op: SSA_MOVE, Type: SSATypeInt, Arg1: newIdx, Slot: int16(ir.A + 3), PC: ir.PC})
	b.slotDefs[ir.A+3] = moveRef
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
		// Constant from pool — not bound to any VM slot
		constIdx := idx - vm.RKBit
		if constIdx < len(b.trace.Constants) {
			c := b.trace.Constants[constIdx]
			if c.IsInt() {
				return b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: c.Int(), Slot: -1})
			}
			if c.IsFloat() {
				return b.emit(SSAInst{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: int64(math.Float64bits(c.Float())), Slot: -1})
			}
		}
		return b.emit(SSAInst{Op: SSA_LOAD_SLOT, Type: SSATypeUnknown, Slot: int16(idx)})
	}
	return b.getSlotRef(idx)
}

// convertComparison handles OP_LT / OP_LE with typed int/float dispatch.
//
// OP_LT A B C: if (RK(B) < RK(C)) != bool(A) then PC++ (skip next JMP)
//
// The guard must reproduce the SAME skip/no-skip behavior as the recording.
// We detect which path was taken by checking the next instruction in the trace:
//   - Next is JMP → recording saw NO skip (comparison didn't trigger)
//   - Next is NOT JMP → recording saw SKIP (comparison triggered, JMP was skipped)
//
// AuxInt encoding: 0 = guard expects comparison TRUE, 1 = guard expects FALSE.
// For skip path: (B<C) != bool(A) was TRUE, so (B<C) == !bool(A)
// For no-skip path: (B<C) != bool(A) was FALSE, so (B<C) == bool(A)
func (b *ssaBuilder) convertComparison(idx int, ir *TraceIR, intOp, floatOp SSAOp) {
	arg1 := b.getSlotOrRK(ir.B)
	arg2 := b.getSlotOrRK(ir.C)

	// Determine types
	bType := b.slotType[ir.B]
	cType := b.slotType[ir.C]
	if ir.B >= vm.RKBit {
		bType = ssaTypFromRuntime(ir.BType)
	}
	if ir.C >= vm.RKBit {
		cType = ssaTypFromRuntime(ir.CType)
	}

	op := intOp
	if bType == SSATypeFloat || cType == SSATypeFloat {
		op = floatOp
	}

	// Detect skip vs no-skip by looking at the next trace instruction
	didSkip := true
	if idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP {
		didSkip = false // JMP follows → comparison didn't skip it
	}

	// Encode guard polarity in AuxInt:
	// For LT: the comparison is (B < C)
	//   didSkip + A=0: skip when B<C → guard: expect B<C → AuxInt=0 (exit on GE)
	//   didSkip + A=1: skip when B>=C → guard: expect B>=C → AuxInt=1 (exit on LT)
	//   !didSkip + A=0: no-skip when B>=C → guard: expect B>=C → AuxInt=1 (exit on LT)
	//   !didSkip + A=1: no-skip when B<C → guard: expect B<C → AuxInt=0 (exit on GE)
	auxInt := int64(ir.A)
	if !didSkip {
		// Invert: no-skip means the comparison result MATCHED bool(A),
		// so the guard expects the opposite condition from the skip case
		auxInt = 1 - auxInt
	}

	b.emit(SSAInst{Op: op, Type: SSATypeBool, Arg1: arg1, Arg2: arg2, AuxInt: auxInt, PC: ir.PC})
}

// SSAIsUseful returns true if the SSA function can actually loop natively.
// A trace is only useful if the loop exit check (LE_INT) is reachable — meaning
// the trace can execute multiple iterations without side-exiting. If a SIDE_EXIT
// appears before the LE_INT, the trace always exits after partial computation
// (never loops), which adds overhead and can corrupt register state.
// Float comparisons (LT_FLOAT, LE_FLOAT, GT_FLOAT) are conditional guards
// (like the escape check in mandelbrot), not loop terminators — they're fine.
func SSAIsUseful(f *SSAFunc) bool {
	loopSeen := false
	hasUsefulOp := false
	for _, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopSeen = true
			continue
		}
		if loopSeen {
			switch inst.Op {
			case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
				SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
				SSA_EQ_INT:
				hasUsefulOp = true
			case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
				SSA_GUARD_TRUTHY:
				// Conditional guards — don't block the loop
				hasUsefulOp = true
			case SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_FIELD, SSA_STORE_FIELD:
				hasUsefulOp = true
			case SSA_LE_INT, SSA_LT_INT:
				// Loop exit check is reachable — trace can actually loop
				return hasUsefulOp
			case SSA_SIDE_EXIT:
				// Unconditional side-exit before loop check — trace never loops
				return false
			}
		}
	}
	return false
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
		case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA, SSA_GUARD_TRUTHY,
			SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY,
			SSA_LOAD_ARRAY, // table loads have side-exits, keep alive
			SSA_LOOP, SSA_SNAPSHOT, SSA_SIDE_EXIT,
			SSA_LE_INT, SSA_LT_INT, SSA_EQ_INT,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_CALL, SSA_CALL_SELF:
			refCount[i]++ // keep alive
		}
	}

	// Mark loop-carried values as live: any value-producing instruction after LOOP
	// that writes to a VM slot (Slot >= 0) is potentially a loop-carried definition
	// and must not be eliminated.
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx >= 0 {
		for i := loopIdx + 1; i < len(f.Insts); i++ {
			inst := &f.Insts[i]
			switch inst.Op {
			case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
				SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
				SSA_CONST_INT, SSA_CONST_FLOAT, SSA_MOVE:
				if inst.Slot >= 0 {
					refCount[i]++ // keep alive: writes to a VM slot
				}
			}
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
