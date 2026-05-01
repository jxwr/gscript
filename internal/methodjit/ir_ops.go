// ir_ops.go defines the Op enum for the Method JIT's CFG SSA IR.
// Every GScript bytecode opcode maps to at least one Op. Type-specialized
// variants (AddInt, AddFloat) are introduced by optimization passes.

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
	OpAddInt      // int + int → int
	OpSubInt      // int - int → int
	OpMulInt      // int * int → int
	OpModInt      // int % int → int
	OpDivIntExact // int / int → int when exact; deopts otherwise
	OpNegInt      // -int → int
	OpAddFloat    // float + float → float
	OpSubFloat    // float - float → float
	OpMulFloat    // float * float → float
	OpDivFloat    // float / float → float (also int/int → float)
	OpNegFloat    // -float → float
	OpSqrt        // sqrt(float) → float (intrinsic: rewrites math.sqrt(x))
	OpFloor       // floor(number) → int (intrinsic: rewrites math.floor(x))
	// R43 Phase 2 DenseMatrix intrinsics (compound; self-contained).
	// OpMatrixGetF: Args = [m, i, j]; loads flat[i*m.dmStride + j] as float.
	// OpMatrixSetF: Args = [m, i, j, v]; stores v at flat[i*m.dmStride + j].
	// Both guard dmStride > 0 at runtime; deopt on miss.
	OpMatrixGetF
	OpMatrixSetF
	// R45 Phase 2c LICM-friendly split:
	//   OpMatrixFlat(m) → int64 raw pointer (dmFlat), verifies DM
	//   OpMatrixStride(m) → int64 (dmStride), verifies DM
	//   OpMatrixLoadFAt(flat, stride, i, j) → float (no guards)
	//   OpMatrixStoreFAt(flat, stride, i, j, v) → void (no guards)
	// Lowering happens after TypeSpecialize so LICM can hoist Flat/
	// Stride out of loops when m is loop-invariant.
	OpMatrixFlat
	OpMatrixStride
	OpMatrixLoadFAt
	OpMatrixStoreFAt
	// R46 Phase 2d row-pointer strength reduction:
	//   OpMatrixRowPtr(flat, stride, i) → int64 = flat + i*stride*8
	//   OpMatrixLoadFRow(rowPtr, j) → float = *(rowPtr + j*8)
	//   OpMatrixStoreFRow(rowPtr, j, v) → void
	// When i is loop-invariant (matmul: i fixed in j-loop and k-loop),
	// LICM hoists OpMatrixRowPtr out so the k body is one LDR per load.
	OpMatrixRowPtr
	OpMatrixLoadFRow
	OpMatrixStoreFRow
	// R47: fused multiply-add. OpFMA(a, b, acc) → acc + a*b.
	// Emitted by FMAFusionPass when OpAddFloat(acc, OpMulFloat(a,b))
	// is detected with single-use Mul. Single-insn ARM64 FMADDd.
	OpFMA
	// Fused multiply-subtract. OpFMSUB(a, b, acc) → acc - a*b.
	// Emitted by FMAFusionPass for SubFloat(acc, single-use MulFloat(a,b)).
	// Single-insn ARM64 FMSUBd.
	OpFMSUB

	// Comparison (type-generic)
	OpEq // Args[0] == Args[1]
	OpLt // Args[0] < Args[1]
	OpLe // Args[0] <= Args[1]

	// Type-specialized comparison
	OpEqInt
	OpLtInt
	OpLeInt
	OpModZeroInt // Args[0] % Aux == 0 for non-zero constant integer Aux
	OpLtFloat
	OpLeFloat

	// String
	OpConcat            // Args[0] .. Args[1] .. ...
	OpStringConstLookup // Args[0] indexes Function.StringConstTables[Aux], Aux2 = table length

	// Table operations
	OpNewTable // Aux = array hint, Aux2 = hash hint
	// OpNewFixedTable constructs a fixed string-field table from Args.
	// Aux = table-constructor index, Aux2 = field count. Today codegen
	// supports the generic two-field constructor shape carried by OP_NEWOBJECT2.
	OpNewFixedTable
	OpGetTable // Args[0][Args[1]]
	OpSetTable // Args[0][Args[1]] = Args[2]
	// Typed table array load split. Lowered from monomorphic-kind
	// OpGetTable so table/kind/header/data facts can be CSE'd and hoisted.
	// Aux carries vm.FBKind*.
	OpTableArrayHeader // Args[0] = table; verifies table/metatable/kind, returns raw *Table
	OpTableArrayLen    // Args[0] = header; loads active array len
	OpTableArrayData   // Args[0] = header; loads active array data pointer
	OpTableArrayLoad   // Args = [data, len, key]; loads element, bounds-checks key
	// Checked typed array store. Args = [table, data, len, key, value].
	// Reuses previously verified typed-array facts, checks key/value before
	// mutation, and precise-deopts on miss so the interpreter replays SETTABLE.
	OpTableArrayStore
	// Fused typed-array swap. Args = [table, data, len, keyA, keyB].
	// Replaces same-block load/load/store/store exchange patterns after the
	// table kind/header/data facts have already been lowered.
	OpTableArraySwap
	// Bulk bool-array fill. Args = [table, start, end] for contiguous fills or
	// [table, start, end, step] for bounded stride fills. Aux = byte value
	// (1=false, 2=true). The stride form uses a guarded bool-array kernel and
	// falls back through RawSetInt when array kind or bounds do not match.
	OpTableBoolArrayFill
	// Bulk bool-array truthy count. Args = [table, start, end]. Returns the
	// number of true bool-array bytes in the inclusive range, with table-exit
	// fallback to RawGetInt+Truthy when guards miss.
	OpTableBoolArrayCount
	// Guarded int-array prefix reversal. Args = [table, hi]. Returns true
	// after reversing keys 1..hi in place on an int array, or false without
	// mutation so control can branch to the original scalar loop fallback.
	OpTableIntArrayReversePrefix
	// Guarded int-array prefix copy. Args = [dst, src, hi]. Returns true
	// after copying keys 1..hi from src to dst, or false without mutation so
	// control can branch to the original scalar loop fallback.
	OpTableIntArrayCopyPrefix
	// Same-block nested row load:
	// Args = [outerData, outerLen, outerKey, innerKey], Aux = inner row FBKind.
	// Loads a table row from a mixed outer array, verifies the row array kind,
	// then loads the inner element without materializing the row table SSA value.
	OpTableArrayNestedLoad
	OpGetField // Args[0].field; Aux = constant pool index for field name
	// OpGetFieldNumToFloat fuses Args[0].field with numeric widening.
	// It preserves NumToFloat semantics: int and float fields become raw
	// float64, while non-numeric fields deopt.
	OpGetFieldNumToFloat
	OpSetField // Args[0].field = Args[1]; Aux = constant pool index
	OpSetList  // table.setlist(Args[0], Args[1:])
	OpAppend   // table.insert(Args[0], Args[1])

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
	OpNumToFloat // NaN-boxed int/float → raw float64; deopt if non-number

	// Guards (speculative; deopt on failure)
	OpGuardType     // Args[0] must have type Aux; deopt if not
	OpGuardIntRange // Args[0] must be int in [Aux, Aux2]; deopt if not
	OpGuardNonNil
	OpGuardTruthy

	// Control flow (terminators — must be last instruction in a block)
	OpJump   // unconditional jump to Succs[0]
	OpBranch // conditional: if Args[0] then Succs[0] else Succs[1]
	OpReturn // return Args[0], Args[1], ...

	// Calls
	OpCall // Args[0] = function, Args[1:] = arguments
	OpSelf // method call: Args[0] = table, Args[1] = method key

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
	OpConstInt:                   "ConstInt",
	OpConstFloat:                 "ConstFloat",
	OpConstBool:                  "ConstBool",
	OpConstNil:                   "ConstNil",
	OpConstString:                "ConstString",
	OpLoadSlot:                   "LoadSlot",
	OpStoreSlot:                  "StoreSlot",
	OpAdd:                        "Add",
	OpSub:                        "Sub",
	OpMul:                        "Mul",
	OpDiv:                        "Div",
	OpMod:                        "Mod",
	OpPow:                        "Pow",
	OpUnm:                        "Unm",
	OpNot:                        "Not",
	OpLen:                        "Len",
	OpAddInt:                     "AddInt",
	OpSubInt:                     "SubInt",
	OpMulInt:                     "MulInt",
	OpModInt:                     "ModInt",
	OpDivIntExact:                "DivIntExact",
	OpNegInt:                     "NegInt",
	OpAddFloat:                   "AddFloat",
	OpSubFloat:                   "SubFloat",
	OpMulFloat:                   "MulFloat",
	OpDivFloat:                   "DivFloat",
	OpNegFloat:                   "NegFloat",
	OpSqrt:                       "Sqrt",
	OpFloor:                      "Floor",
	OpMatrixGetF:                 "MatrixGetF",
	OpMatrixSetF:                 "MatrixSetF",
	OpMatrixFlat:                 "MatrixFlat",
	OpMatrixStride:               "MatrixStride",
	OpMatrixLoadFAt:              "MatrixLoadFAt",
	OpMatrixStoreFAt:             "MatrixStoreFAt",
	OpMatrixRowPtr:               "MatrixRowPtr",
	OpMatrixLoadFRow:             "MatrixLoadFRow",
	OpMatrixStoreFRow:            "MatrixStoreFRow",
	OpFMA:                        "FMA",
	OpFMSUB:                      "FMSUB",
	OpEq:                         "Eq",
	OpLt:                         "Lt",
	OpLe:                         "Le",
	OpEqInt:                      "EqInt",
	OpLtInt:                      "LtInt",
	OpLeInt:                      "LeInt",
	OpModZeroInt:                 "ModZeroInt",
	OpLtFloat:                    "LtFloat",
	OpLeFloat:                    "LeFloat",
	OpConcat:                     "Concat",
	OpStringConstLookup:          "StringConstLookup",
	OpNewTable:                   "NewTable",
	OpNewFixedTable:              "NewFixedTable",
	OpGetTable:                   "GetTable",
	OpSetTable:                   "SetTable",
	OpTableArrayHeader:           "TableArrayHeader",
	OpTableArrayLen:              "TableArrayLen",
	OpTableArrayData:             "TableArrayData",
	OpTableArrayLoad:             "TableArrayLoad",
	OpTableArrayStore:            "TableArrayStore",
	OpTableArraySwap:             "TableArraySwap",
	OpTableBoolArrayFill:         "TableBoolArrayFill",
	OpTableBoolArrayCount:        "TableBoolArrayCount",
	OpTableIntArrayReversePrefix: "TableIntArrayReversePrefix",
	OpTableIntArrayCopyPrefix:    "TableIntArrayCopyPrefix",
	OpTableArrayNestedLoad:       "TableNestedLoad",
	OpGetField:                   "GetField",
	OpGetFieldNumToFloat:         "GetFieldNumToFloat",
	OpSetField:                   "SetField",
	OpSetList:                    "SetList",
	OpAppend:                     "Append",
	OpGetGlobal:                  "GetGlobal",
	OpSetGlobal:                  "SetGlobal",
	OpGetUpval:                   "GetUpval",
	OpSetUpval:                   "SetUpval",
	OpBoxInt:                     "BoxInt",
	OpBoxFloat:                   "BoxFloat",
	OpUnboxInt:                   "UnboxInt",
	OpUnboxFloat:                 "UnboxFloat",
	OpNumToFloat:                 "NumToFloat",
	OpGuardType:                  "GuardType",
	OpGuardIntRange:              "GuardIntRange",
	OpGuardNonNil:                "GuardNonNil",
	OpGuardTruthy:                "GuardTruthy",
	OpJump:                       "Jump",
	OpBranch:                     "Branch",
	OpReturn:                     "Return",
	OpCall:                       "Call",
	OpSelf:                       "Self",
	OpForPrep:                    "ForPrep",
	OpForLoop:                    "ForLoop",
	OpClosure:                    "Closure",
	OpClose:                      "Close",
	OpTForCall:                   "TForCall",
	OpTForLoop:                   "TForLoop",
	OpVararg:                     "Vararg",
	OpTestSet:                    "TestSet",
	OpGo:                         "Go",
	OpMakeChan:                   "MakeChan",
	OpSend:                       "Send",
	OpRecv:                       "Recv",
	OpPhi:                        "Phi",
	OpNop:                        "Nop",
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
