package methodjit

// Op represents an SSA operation in the method JIT IR.
type Op uint8

const (
	// Constants
	OpConstInt   Op = iota // Aux = int64 value
	OpConstFloat           // Aux = math.Float64bits(value)
	OpConstBool            // Aux = 0 (false) or 1 (true)
	OpConstNil
	OpConstString // Aux = constant pool index

	// Slot access (VM register file)
	OpLoadSlot  // Aux = slot number; load NaN-boxed value from VM register
	OpStoreSlot // Args[0] = value; Aux = slot number; store to VM register

	// Arithmetic (type-generic: dispatches based on operand types at runtime)
	OpAdd // Args[0] + Args[1]
	OpSub // Args[0] - Args[1]
	OpMul // Args[0] * Args[1]
	OpDiv // Args[0] / Args[1] (always float, Lua semantics)
	OpMod // Args[0] % Args[1]
	OpPow // Args[0] ** Args[1]
	OpUnm // -Args[0]
	OpNot // !Args[0]
	OpLen // #Args[0]

	// Type-specialized arithmetic (inserted by optimization passes)
	OpAddInt   // int + int → int
	OpSubInt   // int - int → int
	OpMulInt   // int * int → int
	OpModInt   // int % int → int
	OpNegInt   // -int → int
	OpAddFloat // float + float → float
	OpSubFloat // float - float → float
	OpMulFloat // float * float → float
	OpDivFloat // float / float → float (also int/int → float)
	OpNegFloat // -float → float

	// Comparison (type-generic)
	OpEq // Args[0] == Args[1]
	OpLt // Args[0] < Args[1]
	OpLe // Args[0] <= Args[1]

	// Type-specialized comparison
	OpEqInt
	OpLtInt
	OpLeInt
	OpLtFloat
	OpLeFloat

	// String
	OpConcat // Args[0] .. Args[1] .. ...

	// Table operations
	OpNewTable  // Aux = array hint, Aux2 = hash hint
	OpGetTable  // Args[0][Args[1]]
	OpSetTable  // Args[0][Args[1]] = Args[2]
	OpGetField  // Args[0].field; Aux = constant pool index for field name
	OpSetField  // Args[0].field = Args[1]; Aux = constant pool index
	OpSetList   // table.setlist(Args[0], Args[1:])
	OpAppend    // table.insert(Args[0], Args[1])

	// Global access
	OpGetGlobal // Aux = constant pool index for name
	OpSetGlobal // Args[0] = value; Aux = constant pool index

	// Upvalue access
	OpGetUpval // Aux = upvalue index
	OpSetUpval // Args[0] = value; Aux = upvalue index

	// Type operations
	OpBoxInt     // raw int64 → NaN-boxed Value
	OpBoxFloat   // raw float64 → NaN-boxed Value
	OpUnboxInt   // NaN-boxed → raw int64
	OpUnboxFloat // NaN-boxed → raw float64

	// Guards (speculative; deopt on failure)
	OpGuardType // Args[0] must have type Aux; deopt if not
	OpGuardNonNil
	OpGuardTruthy

	// Control flow (terminators — must be last instruction in a block)
	OpJump   // unconditional jump to Succs[0]
	OpBranch // conditional: if Args[0] then Succs[0] else Succs[1]
	OpReturn // return Args[0], Args[1], ...

	// Calls
	OpCall    // Args[0] = function, Args[1:] = arguments
	OpSelf    // method call: Args[0] = table, Args[1] = method key

	// For-loop
	OpForPrep // initialize: R(A) -= R(A+2); jump to Succs[0] (loop test block)
	OpForLoop // test+increment: R(A) += R(A+2); branch on R(A) <= R(A+1)

	// Generic for / iterator
	OpTForCall // R(A+3)..R(A+2+C) = R(A)(R(A+1), R(A+2)); Aux = C (num results)
	OpTForLoop // if R(A+1) != nil { R(A) = R(A+1); jump }; Aux = target block

	// Closures
	OpClosure // Aux = proto index in parent's Protos[]
	OpClose   // close upvalues >= slot Aux

	// Varargs
	OpVararg // R(A)..R(A+B-2) = varargs; Aux = B (0 = all)

	// TestSet (short-circuit &&/||)
	OpTestSet // if bool(Args[0]) != bool(Aux) then skip, else result = Args[0]

	// Goroutine & channel
	OpGo       // go Args[0](Args[1:]); spawn goroutine
	OpMakeChan // make(chan, Aux); Aux = buffer size (0 = unbuffered)
	OpSend     // Args[0] <- Args[1]; send value to channel
	OpRecv     // <-Args[0]; receive from channel

	// Phi (only appears at block entry, not in Instrs)
	OpPhi

	// Special
	OpNop // no operation (placeholder)

	OpMax // sentinel
)

var opNames = [...]string{
	OpConstInt:   "ConstInt",
	OpConstFloat: "ConstFloat",
	OpConstBool:  "ConstBool",
	OpConstNil:   "ConstNil",
	OpConstString: "ConstString",
	OpLoadSlot:   "LoadSlot",
	OpStoreSlot:  "StoreSlot",
	OpAdd:        "Add",
	OpSub:        "Sub",
	OpMul:        "Mul",
	OpDiv:        "Div",
	OpMod:        "Mod",
	OpPow:        "Pow",
	OpUnm:        "Unm",
	OpNot:        "Not",
	OpLen:        "Len",
	OpAddInt:     "AddInt",
	OpSubInt:     "SubInt",
	OpMulInt:     "MulInt",
	OpModInt:     "ModInt",
	OpNegInt:     "NegInt",
	OpAddFloat:   "AddFloat",
	OpSubFloat:   "SubFloat",
	OpMulFloat:   "MulFloat",
	OpDivFloat:   "DivFloat",
	OpNegFloat:   "NegFloat",
	OpEq:         "Eq",
	OpLt:         "Lt",
	OpLe:         "Le",
	OpEqInt:      "EqInt",
	OpLtInt:      "LtInt",
	OpLeInt:      "LeInt",
	OpLtFloat:    "LtFloat",
	OpLeFloat:    "LeFloat",
	OpConcat:     "Concat",
	OpNewTable:   "NewTable",
	OpGetTable:   "GetTable",
	OpSetTable:   "SetTable",
	OpGetField:   "GetField",
	OpSetField:   "SetField",
	OpSetList:    "SetList",
	OpAppend:     "Append",
	OpGetGlobal:  "GetGlobal",
	OpSetGlobal:  "SetGlobal",
	OpGetUpval:   "GetUpval",
	OpSetUpval:   "SetUpval",
	OpBoxInt:     "BoxInt",
	OpBoxFloat:   "BoxFloat",
	OpUnboxInt:   "UnboxInt",
	OpUnboxFloat: "UnboxFloat",
	OpGuardType:  "GuardType",
	OpGuardNonNil: "GuardNonNil",
	OpGuardTruthy: "GuardTruthy",
	OpJump:       "Jump",
	OpBranch:     "Branch",
	OpReturn:     "Return",
	OpCall:       "Call",
	OpSelf:       "Self",
	OpForPrep:    "ForPrep",
	OpForLoop:    "ForLoop",
	OpClosure:    "Closure",
	OpClose:      "Close",
	OpTForCall:   "TForCall",
	OpTForLoop:   "TForLoop",
	OpVararg:     "Vararg",
	OpTestSet:    "TestSet",
	OpGo:         "Go",
	OpMakeChan:   "MakeChan",
	OpSend:       "Send",
	OpRecv:       "Recv",
	OpPhi:        "Phi",
	OpNop:        "Nop",
}

func (op Op) String() string {
	if int(op) < len(opNames) && opNames[op] != "" {
		return opNames[op]
	}
	return "???"
}

// IsTerminator returns true if this op must be the last instruction in a block.
func (op Op) IsTerminator() bool {
	switch op {
	case OpJump, OpBranch, OpReturn:
		return true
	}
	return false
}
