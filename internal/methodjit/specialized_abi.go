//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"strings"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// SpecializedABIKind names the entry/return convention a function can use.
// The analysis result is consumed by codegen to decide whether to emit and use
// the raw-int self-recursive ABI; non-eligible functions stay on the boxed VM
// ABI.
type SpecializedABIKind uint8

const (
	SpecializedABINone SpecializedABIKind = iota
	SpecializedABIRawInt
	SpecializedABITyped
)

// SpecializedABIParamRep describes how one fixed parameter is represented at
// a specialized entry point.
type SpecializedABIParamRep uint8

const (
	SpecializedABIParamBoxed SpecializedABIParamRep = iota
	SpecializedABIParamRawInt
	SpecializedABIParamRawFloat
	SpecializedABIParamRawTablePtr
)

// SpecializedABIReturnRep describes the result representation at a specialized
// return point.
type SpecializedABIReturnRep uint8

const (
	SpecializedABIReturnNone SpecializedABIReturnRep = iota
	SpecializedABIReturnBoxed
	SpecializedABIReturnRawInt
	SpecializedABIReturnRawFloat
	SpecializedABIReturnRawTablePtr
)

// SpecializedABI is the analysis result for a candidate specialized entry.
type SpecializedABI struct {
	Kind      SpecializedABIKind
	Params    []SpecializedABIParamRep
	Return    SpecializedABIReturnRep
	Eligible  bool
	RejectWhy string
}

func specializedABIParamName(rep SpecializedABIParamRep) string {
	switch rep {
	case SpecializedABIParamRawInt:
		return "raw-int"
	case SpecializedABIParamRawFloat:
		return "raw-float"
	case SpecializedABIParamRawTablePtr:
		return "raw-table"
	case SpecializedABIParamBoxed:
		return "boxed"
	default:
		return "unknown"
	}
}

func typedABISignature(abi TypedSelfABI) uint64 {
	if !abi.Eligible {
		return 0
	}
	sig := uint64(0x54414249) // "TABI"
	sig = sig*131 + uint64(abi.NumParams)
	sig = sig*131 + uint64(abi.Return)
	for _, rep := range abi.Params {
		sig = sig*131 + uint64(rep+1)
	}
	return sig
}

func specializedABIParamSummary(params []SpecializedABIParamRep) string {
	if len(params) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(params))
	for _, rep := range params {
		parts = append(parts, specializedABIParamName(rep))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func specializedABIReturnName(rep SpecializedABIReturnRep) string {
	switch rep {
	case SpecializedABIReturnNone:
		return "none"
	case SpecializedABIReturnBoxed:
		return "boxed"
	case SpecializedABIReturnRawInt:
		return "raw-int"
	case SpecializedABIReturnRawFloat:
		return "raw-float"
	case SpecializedABIReturnRawTablePtr:
		return "raw-table"
	default:
		return "unknown"
	}
}

// RawIntSelfABI is the compact codegen contract for the private numeric
// recursive entry. It started as the self-recursive entry descriptor and still
// carries that name for compatibility, but it also covers structurally pure
// numeric self+peer recursion accepted by qualifiesForNumericCrossRecursiveCandidate.
type RawIntSelfABI struct {
	Eligible   bool
	NumParams  int
	ParamSlots []int
	Return     SpecializedABIReturnRep
	RejectWhy  string
}

// TypedSelfABI is the private method-JIT ABI for fixed-shape recursive
// kernels whose hot recursive edge can avoid the boxed VM CALL convention.
// Parameters are passed in X0..X3 as raw int64 or *runtime.Table pointers.
// The return value is delivered in X0 with the representation named by Return.
type TypedSelfABI struct {
	Eligible   bool
	NumParams  int
	ParamSlots []int
	Params     []SpecializedABIParamRep
	Return     SpecializedABIReturnRep
	RejectWhy  string
}

type specializedSlotRep uint8

const (
	specializedSlotUnknown specializedSlotRep = iota
	specializedSlotRawInt
	specializedSlotRawFloat
	specializedSlotRawTable
	specializedSlotRawString
	specializedSlotNil
	specializedSlotSelfCallRawInt
	specializedSlotSelfCallRawFloat
	specializedSlotSelfCallRawTable
	specializedSlotSelfFunc
	specializedSlotOtherFunc
	specializedSlotStdMathTable
	specializedSlotMathSqrtFunc
	specializedSlotMathFloorFunc
)

// AnalyzeSpecializedABI recognizes generic raw-int ABI candidates. It is
// intentionally not tied to any one benchmark: a candidate must have fixed
// integer parameters, a single integer return, and bytecode operations whose
// values can be represented as raw int64/int48 along recursive call edges.
func AnalyzeSpecializedABI(proto *vm.FuncProto) SpecializedABI {
	if proto == nil {
		return specializedABIReject("nil proto")
	}
	if proto.IsVarArg {
		return specializedABIReject("vararg function")
	}
	if proto.NumParams < 1 || proto.NumParams > 4 {
		return specializedABIReject("unsupported fixed param count")
	}
	if len(proto.Upvalues) != 0 {
		return specializedABIReject("upvalues")
	}
	if len(proto.Protos) != 0 {
		return specializedABIReject("nested protos")
	}
	if proto.NumParams > maxTrackedSlots || proto.MaxStack > maxTrackedSlots {
		return specializedABIReject("too many slots")
	}

	slots := make([]specializedSlotRep, maxTrackedSlots)
	paramReps := make([]SpecializedABIParamRep, proto.NumParams)
	for i := 0; i < proto.NumParams; i++ {
		slots[i] = specializedSlotRawInt
		paramReps[i] = SpecializedABIParamRawInt
	}

	branchTargets := specializedABIBranchTargets(proto.Code)
	sawReturn := false

	for pc, inst := range proto.Code {
		if pc > 0 && branchTargets[pc] {
			for i := range slots {
				slots[i] = specializedSlotUnknown
			}
			for i := 0; i < proto.NumParams; i++ {
				slots[i] = specializedSlotRawInt
			}
			for slot, rep := range typedSelfForLoopBranchFacts(proto, paramReps, pc) {
				setSpecializedSlot(slots, slot, rep)
			}
		}

		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		switch op {
		case vm.OP_LOADINT:
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_LOADK:
			bx := vm.DecodeBx(inst)
			if !specializedABIConstIsInt(proto, bx) {
				return specializedABIReject("non-int constant load")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_MOVE:
			setSpecializedSlot(slots, a, getSpecializedSlot(slots, b))
		case vm.OP_GETGLOBAL:
			bx := vm.DecodeBx(inst)
			if specializedABIConstString(proto, bx) == proto.Name {
				setSpecializedSlot(slots, a, specializedSlotSelfFunc)
			} else {
				setSpecializedSlot(slots, a, specializedSlotOtherFunc)
			}
		case vm.OP_SETUPVAL, vm.OP_CLOSE, vm.OP_JMP:
			// No local value produced.
		case vm.OP_SETGLOBAL:
			return specializedABIReject("global mutation")
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD:
			if !specializedABIRKIsRawInt(slots, proto, b) || !specializedABIRKIsRawInt(slots, proto, c) {
				return specializedABIReject("non-int arithmetic operand")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_UNM:
			if !specializedABIRepIsRawInt(getSpecializedSlot(slots, b)) {
				return specializedABIReject("non-int unary operand")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if !specializedABIRKIsRawInt(slots, proto, b) || !specializedABIRKIsRawInt(slots, proto, c) {
				return specializedABIReject("non-int comparison operand")
			}
		case vm.OP_TEST:
			// Branch-only; does not affect raw-int data.
		case vm.OP_TESTSET:
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		case vm.OP_CALL:
			if getSpecializedSlot(slots, a) != specializedSlotSelfFunc {
				return specializedABIReject("non-self call")
			}
			if b == 0 || b-1 != proto.NumParams {
				return specializedABIReject("dynamic call arity")
			}
			for arg := a + 1; arg <= a+b-1; arg++ {
				if !specializedABIRepIsRawInt(getSpecializedSlot(slots, arg)) {
					return specializedABIReject("non-int call argument")
				}
			}
			switch c {
			case 0:
				setSpecializedSlot(slots, a, specializedSlotSelfCallRawInt)
			case 2:
				setSpecializedSlot(slots, a, specializedSlotRawInt)
			case 1:
				setSpecializedSlot(slots, a, specializedSlotUnknown)
			default:
				return specializedABIReject("multiple call returns")
			}
		case vm.OP_RETURN:
			switch b {
			case 2:
				if !specializedABIRepIsRawInt(getSpecializedSlot(slots, a)) {
					return specializedABIReject("non-int return")
				}
				sawReturn = true
			case 0:
				if getSpecializedSlot(slots, a) != specializedSlotSelfCallRawInt {
					return specializedABIReject("dynamic return")
				}
				sawReturn = true
			default:
				return specializedABIReject("non-single return")
			}
		case vm.OP_FORPREP:
			if !specializedABIRepIsRawInt(getSpecializedSlot(slots, a)) ||
				!specializedABIRepIsRawInt(getSpecializedSlot(slots, a+1)) ||
				!specializedABIRepIsRawInt(getSpecializedSlot(slots, a+2)) {
				return specializedABIReject("non-int for-loop control")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_FORLOOP:
			typedSelfApplyStableForLoopFacts(proto, paramReps, pc, a, slots)
			setSpecializedSlot(slots, a, specializedSlotRawInt)
			setSpecializedSlot(slots, a+1, specializedSlotRawInt)
			setSpecializedSlot(slots, a+2, specializedSlotRawInt)
			setSpecializedSlot(slots, a+3, specializedSlotRawInt)
		case vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_GETUPVAL, vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN,
			vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_SETLIST, vm.OP_APPEND, vm.OP_NOT, vm.OP_LEN, vm.OP_CONCAT,
			vm.OP_POW, vm.OP_CLOSURE,
			vm.OP_TFORCALL, vm.OP_TFORLOOP, vm.OP_VARARG, vm.OP_SELF,
			vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			return specializedABIReject("unsupported opcode")
		default:
			return specializedABIReject("unknown opcode")
		}
	}

	if !sawReturn {
		return specializedABIReject("no return")
	}
	return SpecializedABI{
		Kind:     SpecializedABIRawInt,
		Params:   paramReps,
		Return:   SpecializedABIReturnRawInt,
		Eligible: true,
	}
}

func AnalyzeRawIntSelfABI(proto *vm.FuncProto) RawIntSelfABI {
	abi := AnalyzeSpecializedABI(proto)
	if !abi.Eligible || abi.Kind != SpecializedABIRawInt || abi.Return != SpecializedABIReturnRawInt {
		if qualifiesForNumericCrossRecursiveCandidate(proto) {
			paramSlots := make([]int, proto.NumParams)
			for i := range paramSlots {
				paramSlots[i] = i
			}
			return RawIntSelfABI{
				Eligible:   true,
				NumParams:  proto.NumParams,
				ParamSlots: paramSlots,
				Return:     SpecializedABIReturnRawInt,
			}
		}
		return RawIntSelfABI{
			Return:    abi.Return,
			RejectWhy: abi.RejectWhy,
		}
	}
	paramSlots := make([]int, proto.NumParams)
	for i := range paramSlots {
		paramSlots[i] = i
	}
	return RawIntSelfABI{
		Eligible:   true,
		NumParams:  proto.NumParams,
		ParamSlots: paramSlots,
		Return:     SpecializedABIReturnRawInt,
	}
}

// AnalyzeTypedSelfABI recognizes fixed-type recursive kernels that can use a
// private typed self-call ABI. It deliberately excludes pure raw-int kernels,
// which are handled by the older numeric ABI. This keeps the first typed-table
// contract narrow: it is for recursive functions with at least one table
// parameter or a table return, such as makeTree(int)->table and
// checkTree(table)->int.
func AnalyzeTypedSelfABI(proto *vm.FuncProto) TypedSelfABI {
	abi := analyzeTypedABI(proto, true)
	if abi.Eligible && abi.Return == SpecializedABIReturnNone {
		return typedSelfABIReject("zero-result typed self ABI is disabled")
	}
	return abi
}

func analyzeTypedABI(proto *vm.FuncProto, requireSelfCall bool) TypedSelfABI {
	return analyzeTypedABIWithArgFacts(proto, requireSelfCall, nil)
}

func analyzeTypedABIWithArgFacts(proto *vm.FuncProto, requireSelfCall bool, argFacts map[int]FixedShapeTableFact) TypedSelfABI {
	return analyzeTypedABIWithFacts(proto, requireSelfCall, argFacts, nil)
}

func analyzeTypedABIWithFacts(proto *vm.FuncProto, requireSelfCall bool, argFacts map[int]FixedShapeTableFact, arrayElementArgFacts map[int]FixedShapeTableFact) TypedSelfABI {
	return analyzeTypedABIWithFactsAndGlobals(proto, requireSelfCall, argFacts, arrayElementArgFacts, nil, nil)
}

func analyzeTypedABIWithFactsAndGlobals(proto *vm.FuncProto, requireSelfCall bool, argFacts map[int]FixedShapeTableFact, arrayElementArgFacts map[int]FixedShapeTableFact, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) TypedSelfABI {
	if proto == nil {
		return typedSelfABIReject("nil proto")
	}
	if proto.IsVarArg {
		return typedSelfABIReject("vararg function")
	}
	if proto.NumParams < 1 || proto.NumParams > 4 {
		return typedSelfABIReject("unsupported fixed param count")
	}
	if len(proto.Upvalues) != 0 {
		return typedSelfABIReject("upvalues")
	}
	if len(proto.Protos) != 0 {
		return typedSelfABIReject("nested protos")
	}
	if proto.NumParams > maxTrackedSlots || specializedABIUsesSlotAtOrAbove(proto, maxTrackedSlots) {
		return typedSelfABIReject("too many slots")
	}
	if raw := AnalyzeRawIntSelfABI(proto); raw.Eligible && raw.Return == SpecializedABIReturnRawInt {
		return typedSelfABIReject("covered by raw-int ABI")
	}

	params, reason := inferTypedSelfABIParams(proto)
	if reason != "" {
		return typedSelfABIReject(reason)
	}

	slots := make([]specializedSlotRep, maxTrackedSlots)
	typedSelfResetSlots(slots, params)
	tableFacts := typedSelfInitialTableFacts(params, argFacts)
	branchTargets := specializedABIBranchTargets(proto.Code)
	returnRep := SpecializedABIReturnNone
	sawReturn := false
	sawSelfCall := false
	usesTableABI := false
	for _, p := range params {
		if p == SpecializedABIParamRawTablePtr {
			usesTableABI = true
			break
		}
	}

	for pc, inst := range proto.Code {
		if pc > 0 && branchTargets[pc] {
			typedSelfResetSlots(slots, params)
			tableFacts = typedSelfInitialTableFacts(params, argFacts)
			typedSelfApplyBranchFacts(slots, typedSelfBranchFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts))
			for slot, fact := range typedSelfForLoopBranchTableFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts) {
				if tableFacts == nil {
					tableFacts = make(map[int]FixedShapeTableFact)
				}
				tableFacts[slot] = fact
			}
		}

		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		switch op {
		case vm.OP_LOADINT:
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_LOADK:
			bx := vm.DecodeBx(inst)
			if specializedABIConstIsInt(proto, bx) {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
			} else if specializedABIConstIsFloat(proto, bx) {
				setSpecializedSlot(slots, a, specializedSlotRawFloat)
			} else if specializedABIConstString(proto, bx) != "" {
				setSpecializedSlot(slots, a, specializedSlotRawString)
			} else {
				setSpecializedSlot(slots, a, specializedSlotUnknown)
			}
		case vm.OP_LOADNIL:
			for slot := a; slot <= a+b && slot < len(slots); slot++ {
				setSpecializedSlot(slots, slot, specializedSlotNil)
			}
		case vm.OP_MOVE:
			setSpecializedSlot(slots, a, getSpecializedSlot(slots, b))
			typedSelfMoveTableFact(tableFacts, a, b)
		case vm.OP_GETGLOBAL:
			if specializedABIConstString(proto, vm.DecodeBx(inst)) == proto.Name {
				setSpecializedSlot(slots, a, specializedSlotSelfFunc)
			} else if rep, ok := typedSelfNumericGlobalRep(proto, vm.DecodeBx(inst), numericGlobals); ok {
				setSpecializedSlot(slots, a, rep)
			} else if fact, ok := typedSelfGlobalArrayElementFact(proto, vm.DecodeBx(inst), globalArrayElementFacts); ok {
				setSpecializedSlot(slots, a, specializedSlotRawTable)
				usesTableABI = true
				if tableFacts == nil {
					tableFacts = make(map[int]FixedShapeTableFact)
				}
				tableFacts[a] = fact
			} else if specializedABIConstString(proto, vm.DecodeBx(inst)) == "math" {
				setSpecializedSlot(slots, a, specializedSlotStdMathTable)
			} else {
				setSpecializedSlot(slots, a, specializedSlotOtherFunc)
			}
		case vm.OP_SETUPVAL, vm.OP_CLOSE, vm.OP_JMP:
		case vm.OP_SETGLOBAL:
			return typedSelfABIReject("global mutation")
		case vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN:
			setSpecializedSlot(slots, a, specializedSlotRawTable)
			delete(tableFacts, a)
			usesTableABI = true
		case vm.OP_GETFIELD:
			if !typedSelfSlotIsTable(getSpecializedSlot(slots, b)) {
				return typedSelfABIReject(fmt.Sprintf("non-table field receiver at pc %d", pc))
			}
			name := typedSelfConstFieldName(proto, c)
			if getSpecializedSlot(slots, b) == specializedSlotStdMathTable && name == "sqrt" {
				setSpecializedSlot(slots, a, specializedSlotMathSqrtFunc)
				delete(tableFacts, a)
			} else if getSpecializedSlot(slots, b) == specializedSlotStdMathTable && name == "floor" {
				setSpecializedSlot(slots, a, specializedSlotMathFloorFunc)
				delete(tableFacts, a)
			} else if fact, ok := tableFacts[b]; ok {
				typ, hasTyp := typedSelfFieldTypeFromFact(fact, name)
				if nested, ok := typedSelfNestedTableFactFromFact(fact, name); ok {
					setSpecializedSlot(slots, a, specializedSlotRawTable)
					if tableFacts == nil {
						tableFacts = make(map[int]FixedShapeTableFact)
					}
					tableFacts[a] = nested
				} else if hasTyp && typ == TypeInt {
					setSpecializedSlot(slots, a, specializedSlotRawInt)
					delete(tableFacts, a)
				} else if hasTyp && typ == TypeFloat {
					setSpecializedSlot(slots, a, specializedSlotRawFloat)
					delete(tableFacts, a)
				} else if hasTyp && typ == TypeString {
					setSpecializedSlot(slots, a, specializedSlotRawString)
					delete(tableFacts, a)
				} else if typedSelfFeedbackResultIsTable(proto, pc) {
					setSpecializedSlot(slots, a, specializedSlotRawTable)
					delete(tableFacts, a)
				} else if typedSelfFeedbackResultIsInt(proto, pc) {
					setSpecializedSlot(slots, a, specializedSlotRawInt)
					delete(tableFacts, a)
				} else if typedSelfFeedbackResultIsFloat(proto, pc) {
					setSpecializedSlot(slots, a, specializedSlotRawFloat)
					delete(tableFacts, a)
				} else {
					setSpecializedSlot(slots, a, specializedSlotUnknown)
					delete(tableFacts, a)
				}
			} else if typ, ok := typedSelfParamFieldTypeWithFacts(proto, b, c, tableFacts); ok && typ == TypeTable {
				setSpecializedSlot(slots, a, specializedSlotRawTable)
				typedSelfSetNestedTableFact(tableFacts, a, tableFacts[b], name)
			} else if ok && typ == TypeInt {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
				delete(tableFacts, a)
			} else if ok && typ == TypeFloat {
				setSpecializedSlot(slots, a, specializedSlotRawFloat)
				delete(tableFacts, a)
			} else if ok && typ == TypeString {
				setSpecializedSlot(slots, a, specializedSlotRawString)
				delete(tableFacts, a)
			} else if typedSelfFeedbackResultIsTable(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawTable)
				delete(tableFacts, a)
			} else if typedSelfFeedbackResultIsInt(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
				delete(tableFacts, a)
			} else if typedSelfFeedbackResultIsFloat(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawFloat)
				delete(tableFacts, a)
			} else {
				setSpecializedSlot(slots, a, specializedSlotUnknown)
				delete(tableFacts, a)
			}
		case vm.OP_GETTABLE:
			if !typedSelfSlotIsTable(getSpecializedSlot(slots, b)) {
				return typedSelfABIReject(fmt.Sprintf("non-table index receiver at pc %d", pc))
			}
			if !typedSelfRKIsInt(slots, proto, c) {
				return typedSelfABIReject(fmt.Sprintf("non-int table index at pc %d", pc))
			}
			if fact, ok := arrayElementArgFacts[b]; ok && typedSelfSlotIsTable(getSpecializedSlot(slots, b)) {
				if fact.ArrayElementType == TypeInt {
					setSpecializedSlot(slots, a, specializedSlotRawInt)
					delete(tableFacts, a)
				} else if fact.ArrayElementType == TypeFloat {
					setSpecializedSlot(slots, a, specializedSlotRawFloat)
					delete(tableFacts, a)
				} else {
					setSpecializedSlot(slots, a, specializedSlotRawTable)
					if tableFacts == nil {
						tableFacts = make(map[int]FixedShapeTableFact)
					}
					tableFacts[a] = fact
				}
			} else if fact, ok := tableFacts[b]; ok {
				if fact.ArrayElementType == TypeInt {
					setSpecializedSlot(slots, a, specializedSlotRawInt)
					delete(tableFacts, a)
				} else if fact.ArrayElementType == TypeFloat {
					setSpecializedSlot(slots, a, specializedSlotRawFloat)
					delete(tableFacts, a)
				} else if fixedShapeTableFactHasUsableTableFact(fact) {
					setSpecializedSlot(slots, a, specializedSlotRawTable)
					if tableFacts == nil {
						tableFacts = make(map[int]FixedShapeTableFact)
					}
					tableFacts[a] = fact
				} else {
					setSpecializedSlot(slots, a, specializedSlotUnknown)
					delete(tableFacts, a)
				}
			} else if typedSelfFeedbackResultIsTable(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawTable)
				delete(tableFacts, a)
			} else if typedSelfFeedbackResultIsInt(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
				delete(tableFacts, a)
			} else if typedSelfFeedbackResultIsFloat(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawFloat)
				delete(tableFacts, a)
			} else {
				setSpecializedSlot(slots, a, specializedSlotUnknown)
				delete(tableFacts, a)
			}
		case vm.OP_SETFIELD:
			if len(slots) <= a || !typedSelfSlotIsTable(getSpecializedSlot(slots, a)) {
				return typedSelfABIReject("non-table field store receiver")
			}
		case vm.OP_SETTABLE:
			if len(slots) <= a || !typedSelfSlotIsTable(getSpecializedSlot(slots, a)) {
				return typedSelfABIReject(fmt.Sprintf("non-table index store receiver at pc %d", pc))
			}
			if !typedSelfRKIsInt(slots, proto, b) {
				return typedSelfABIReject(fmt.Sprintf("non-int table store index at pc %d", pc))
			}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD:
			left, lok := typedSelfRKNumericRep(slots, proto, b)
			right, rok := typedSelfRKNumericRep(slots, proto, c)
			if !lok || !rok {
				return typedSelfABIReject(fmt.Sprintf("non-numeric arithmetic operand at pc %d left=%s right=%s", pc,
					specializedSlotRepName(getSpecializedSlot(slots, b)),
					specializedSlotRepName(getSpecializedSlot(slots, c))))
			}
			if op == vm.OP_DIV ||
				left == specializedSlotRawFloat || left == specializedSlotSelfCallRawFloat ||
				right == specializedSlotRawFloat || right == specializedSlotSelfCallRawFloat {
				setSpecializedSlot(slots, a, specializedSlotRawFloat)
			} else {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
			}
		case vm.OP_UNM:
			rep := getSpecializedSlot(slots, b)
			if !typedSelfSlotIsNumeric(rep) {
				return typedSelfABIReject("non-numeric unary operand")
			}
			setSpecializedSlot(slots, a, typedSelfNumericBaseRep(rep))
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if !typedSelfCompareOK(slots, proto, b, c) {
				return typedSelfABIReject("unsupported comparison operand")
			}
		case vm.OP_TEST:
		case vm.OP_TESTSET:
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		case vm.OP_LEN:
			if typedSelfSlotIsString(getSpecializedSlot(slots, b)) || typedSelfFeedbackResultIsInt(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
			} else {
				return typedSelfABIReject("unsupported length result")
			}
		case vm.OP_CALL:
			if typedSelfSlotIsMathUnaryFunc(getSpecializedSlot(slots, a)) {
				if b != 2 || c != 2 {
					return typedSelfABIReject("dynamic intrinsic call arity")
				}
				argRep := getSpecializedSlot(slots, a+1)
				if !typedSelfSlotIsNumeric(argRep) {
					return typedSelfABIReject("non-numeric intrinsic argument")
				}
				if getSpecializedSlot(slots, a) == specializedSlotMathFloorFunc {
					setSpecializedSlot(slots, a, specializedSlotRawInt)
				} else {
					setSpecializedSlot(slots, a, specializedSlotRawFloat)
				}
				delete(tableFacts, a)
				continue
			}
			if getSpecializedSlot(slots, a) != specializedSlotSelfFunc {
				return typedSelfABIReject("non-self call")
			}
			if b == 0 || b-1 != proto.NumParams {
				return typedSelfABIReject("dynamic call arity")
			}
			for i := 0; i < proto.NumParams; i++ {
				argRep := getSpecializedSlot(slots, a+1+i)
				if !typedSelfSlotMatchesParam(argRep, params[i]) {
					return typedSelfABIReject("call argument does not match typed ABI")
				}
			}
			sawSelfCall = true
			switch c {
			case 2:
				switch returnRep {
				case SpecializedABIReturnRawInt:
					setSpecializedSlot(slots, a, specializedSlotSelfCallRawInt)
				case SpecializedABIReturnRawFloat:
					setSpecializedSlot(slots, a, specializedSlotSelfCallRawFloat)
				case SpecializedABIReturnRawTablePtr:
					setSpecializedSlot(slots, a, specializedSlotSelfCallRawTable)
				default:
					setSpecializedSlot(slots, a, specializedSlotUnknown)
				}
			case 1:
				// CALL C=1 has zero results and preserves R(A). Do not
				// fabricate a raw result in the destination slot.
			default:
				return typedSelfABIReject("multiple call returns")
			}
		case vm.OP_RETURN:
			var rep SpecializedABIReturnRep
			switch b {
			case 1:
				rep = SpecializedABIReturnNone
			case 2:
				rep = typedSelfReturnRep(getSpecializedSlot(slots, a), returnRep)
			default:
				return typedSelfABIReject("non-single return")
			}
			if rep == SpecializedABIReturnNone || rep == SpecializedABIReturnBoxed {
				if rep != SpecializedABIReturnNone {
					return typedSelfABIReject("unsupported return representation")
				}
			}
			if sawReturn && returnRep != rep {
				return typedSelfABIReject("inconsistent return representation")
			}
			if rep == SpecializedABIReturnRawTablePtr {
				usesTableABI = true
			}
			returnRep = rep
			sawReturn = true
		case vm.OP_FORPREP:
			if !typedSelfSlotIsInt(getSpecializedSlot(slots, a)) ||
				!typedSelfSlotIsInt(getSpecializedSlot(slots, a+1)) ||
				!typedSelfSlotIsInt(getSpecializedSlot(slots, a+2)) {
				return typedSelfABIReject("non-int for-loop control")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_FORLOOP:
			if typedSelfForLoopControlProvenIntWithFactsAndGlobals(proto, params, pc, a, numericGlobals, globalArrayElementFacts) {
				typedSelfApplyStableForLoopFactsWithFactsAndGlobals(proto, params, pc, a, slots, numericGlobals, globalArrayElementFacts)
			}
			if !typedSelfSlotIsInt(getSpecializedSlot(slots, a)) ||
				!typedSelfSlotIsInt(getSpecializedSlot(slots, a+1)) ||
				!typedSelfSlotIsInt(getSpecializedSlot(slots, a+2)) {
				return typedSelfABIReject("non-int for-loop control")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
			setSpecializedSlot(slots, a+3, specializedSlotRawInt)
		case vm.OP_LOADBOOL, vm.OP_GETUPVAL, vm.OP_NOT, vm.OP_CONCAT,
			vm.OP_POW, vm.OP_CLOSURE,
			vm.OP_TFORCALL, vm.OP_TFORLOOP, vm.OP_VARARG, vm.OP_SELF,
			vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV, vm.OP_APPEND, vm.OP_SETLIST:
			return typedSelfABIReject("unsupported opcode")
		default:
			return typedSelfABIReject("unknown opcode")
		}
	}

	if !sawReturn {
		return typedSelfABIReject("no return")
	}
	if requireSelfCall && !sawSelfCall {
		return typedSelfABIReject("no self call")
	}
	if !usesTableABI {
		return typedSelfABIReject("no table parameter or return")
	}
	paramSlots := make([]int, proto.NumParams)
	for i := range paramSlots {
		paramSlots[i] = i
	}
	return TypedSelfABI{
		Eligible:   true,
		NumParams:  proto.NumParams,
		ParamSlots: paramSlots,
		Params:     params,
		Return:     returnRep,
	}
}

func AnalyzeTypedPeerABI(proto *vm.FuncProto) TypedSelfABI {
	abi := analyzeTypedABI(proto, false)
	if !abi.Eligible {
		return abi
	}
	if abi.Return == SpecializedABIReturnNone && typedABIHasStaticSelfCall(proto) {
		return typedSelfABIReject("zero-result self-recursive typed peer ABI is disabled")
	}
	for _, rep := range abi.Params {
		if rep == SpecializedABIParamRawTablePtr {
			return abi
		}
	}
	return typedSelfABIReject("no table parameter")
}

func AnalyzeTypedPeerABIWithArgFacts(proto *vm.FuncProto, argFacts map[int]FixedShapeTableFact) TypedSelfABI {
	abi := analyzeTypedABIWithArgFacts(proto, false, argFacts)
	if !abi.Eligible {
		return abi
	}
	if abi.Return == SpecializedABIReturnNone && typedABIHasStaticSelfCall(proto) {
		return typedSelfABIReject("zero-result self-recursive typed peer ABI is disabled")
	}
	for _, rep := range abi.Params {
		if rep == SpecializedABIParamRawTablePtr {
			return abi
		}
	}
	return typedSelfABIReject("no table parameter")
}

func AnalyzeTypedPeerABIWithFacts(proto *vm.FuncProto, argFacts map[int]FixedShapeTableFact, arrayElementArgFacts map[int]FixedShapeTableFact) TypedSelfABI {
	abi := analyzeTypedABIWithFacts(proto, false, argFacts, arrayElementArgFacts)
	if !abi.Eligible {
		return abi
	}
	if abi.Return == SpecializedABIReturnNone && typedABIHasStaticSelfCall(proto) {
		return typedSelfABIReject("zero-result self-recursive typed peer ABI is disabled")
	}
	for _, rep := range abi.Params {
		if rep == SpecializedABIParamRawTablePtr {
			return abi
		}
	}
	return typedSelfABIReject("no table parameter")
}

func AnalyzeTypedPeerABIWithFactsAndGlobals(proto *vm.FuncProto, argFacts map[int]FixedShapeTableFact, arrayElementArgFacts map[int]FixedShapeTableFact, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) TypedSelfABI {
	abi := analyzeTypedABIWithFactsAndGlobals(proto, false, argFacts, arrayElementArgFacts, numericGlobals, globalArrayElementFacts)
	if !abi.Eligible {
		return abi
	}
	if abi.Return == SpecializedABIReturnNone && typedABIHasStaticSelfCall(proto) {
		return typedSelfABIReject("zero-result self-recursive typed peer ABI is disabled")
	}
	for _, rep := range abi.Params {
		if rep == SpecializedABIParamRawTablePtr {
			return abi
		}
	}
	if len(globalArrayElementFacts) > 0 {
		return abi
	}
	return typedSelfABIReject("no table parameter")
}

func typedABIHasStaticSelfCall(proto *vm.FuncProto) bool {
	if proto == nil || proto.Name == "" {
		return false
	}
	slots := make([]bool, maxTrackedSlots)
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		switch op {
		case vm.OP_GETGLOBAL:
			if a >= 0 && a < len(slots) {
				slots[a] = specializedABIConstString(proto, vm.DecodeBx(inst)) == proto.Name
			}
		case vm.OP_MOVE:
			if a >= 0 && a < len(slots) {
				slots[a] = b >= 0 && b < len(slots) && slots[b]
			}
		case vm.OP_CALL:
			if a >= 0 && a < len(slots) && slots[a] {
				return true
			}
			if a >= 0 && a < len(slots) {
				slots[a] = false
			}
		default:
			if typedABIOpWritesA(op) && a >= 0 && a < len(slots) {
				slots[a] = false
			}
		}
	}
	return false
}

func typedABIOpWritesA(op vm.Opcode) bool {
	switch op {
	case vm.OP_SETUPVAL, vm.OP_SETGLOBAL, vm.OP_SETTABLE, vm.OP_SETFIELD,
		vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_JMP, vm.OP_RETURN,
		vm.OP_FORLOOP, vm.OP_TFORLOOP, vm.OP_CLOSE:
		return false
	default:
		return true
	}
}

func protoReturnsOnlyNoResults(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	sawReturn := false
	for _, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_RETURN {
			continue
		}
		sawReturn = true
		if vm.DecodeB(inst) != 1 {
			return false
		}
	}
	return sawReturn
}

func protoHasRecursiveTableSurface(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN, vm.OP_SETLIST, vm.OP_APPEND:
			return true
		}
	}
	return false
}

func qualifiesForNumericCrossRecursiveCandidate(proto *vm.FuncProto) bool {
	if proto == nil || proto.IsVarArg || proto.NumParams < 1 || proto.NumParams > 4 {
		return false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || proto.MaxStack > maxTrackedSlots {
		return false
	}

	slots := make([]specializedSlotRep, maxTrackedSlots)
	for i := 0; i < proto.NumParams; i++ {
		slots[i] = specializedSlotRawInt
	}

	branchTargets := specializedABIBranchTargets(proto.Code)
	sawReturn := false
	sawSelfCall := false
	sawPeerCall := false

	for pc, inst := range proto.Code {
		if pc > 0 && branchTargets[pc] {
			for i := range slots {
				slots[i] = specializedSlotUnknown
			}
			for i := 0; i < proto.NumParams; i++ {
				slots[i] = specializedSlotRawInt
			}
		}

		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		switch op {
		case vm.OP_LOADINT:
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_LOADK:
			if !specializedABIConstIsInt(proto, vm.DecodeBx(inst)) {
				return false
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_MOVE:
			setSpecializedSlot(slots, a, getSpecializedSlot(slots, b))
		case vm.OP_GETGLOBAL:
			if specializedABIConstString(proto, vm.DecodeBx(inst)) == proto.Name {
				setSpecializedSlot(slots, a, specializedSlotSelfFunc)
			} else {
				setSpecializedSlot(slots, a, specializedSlotOtherFunc)
			}
		case vm.OP_SETUPVAL, vm.OP_CLOSE, vm.OP_JMP:
		case vm.OP_SETGLOBAL:
			return false
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD:
			if !specializedABIRKIsRawInt(slots, proto, b) || !specializedABIRKIsRawInt(slots, proto, c) {
				return false
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_UNM:
			if !specializedABIRepIsRawInt(getSpecializedSlot(slots, b)) {
				return false
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if !specializedABIRKIsRawInt(slots, proto, b) || !specializedABIRKIsRawInt(slots, proto, c) {
				return false
			}
		case vm.OP_TEST:
		case vm.OP_TESTSET:
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		case vm.OP_CALL:
			callee := getSpecializedSlot(slots, a)
			if callee != specializedSlotSelfFunc && callee != specializedSlotOtherFunc {
				return false
			}
			if b == 0 || b-1 != proto.NumParams {
				return false
			}
			for arg := a + 1; arg <= a+b-1; arg++ {
				if !specializedABIRepIsRawInt(getSpecializedSlot(slots, arg)) {
					return false
				}
			}
			if callee == specializedSlotSelfFunc {
				sawSelfCall = true
			} else {
				sawPeerCall = true
			}
			switch c {
			case 2:
				setSpecializedSlot(slots, a, specializedSlotRawInt)
			case 1:
				setSpecializedSlot(slots, a, specializedSlotUnknown)
			default:
				return false
			}
		case vm.OP_RETURN:
			if b != 2 || !specializedABIRepIsRawInt(getSpecializedSlot(slots, a)) {
				return false
			}
			sawReturn = true
		default:
			return false
		}
	}

	return sawReturn && sawSelfCall && sawPeerCall
}

func typedSelfABIReject(reason string) TypedSelfABI {
	return TypedSelfABI{
		Return:    SpecializedABIReturnBoxed,
		RejectWhy: reason,
	}
}

func specializedABIUsesSlotAtOrAbove(proto *vm.FuncProto, limit int) bool {
	if proto == nil || limit <= 0 {
		return true
	}
	if proto.MaxStack > 0 && proto.MaxStack <= limit {
		return false
	}
	uses := func(slot int) bool {
		return slot >= limit
	}
	usesRange := func(start, count int) bool {
		return count > 0 && start+count-1 >= limit
	}
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		bReg := b < vm.RKBit
		cReg := c < vm.RKBit
		switch op {
		case vm.OP_LOADNIL:
			if usesRange(a, b+1) {
				return true
			}
		case vm.OP_CALL:
			if uses(a) || (b > 0 && usesRange(a+1, b-1)) || (c > 1 && usesRange(a, c-1)) || (b == 0 || c == 0) {
				return true
			}
		case vm.OP_RETURN:
			if b == 0 || usesRange(a, b-1) {
				return true
			}
		case vm.OP_FORPREP, vm.OP_FORLOOP:
			if usesRange(a, 4) {
				return true
			}
		case vm.OP_MOVE:
			if uses(a) || uses(b) {
				return true
			}
		case vm.OP_GETTABLE:
			if uses(a) || uses(b) || (cReg && uses(c)) {
				return true
			}
		case vm.OP_SETTABLE:
			if uses(a) || (bReg && uses(b)) || (cReg && uses(c)) {
				return true
			}
		case vm.OP_GETFIELD, vm.OP_SELF:
			if uses(a) || uses(b) {
				return true
			}
		case vm.OP_SETFIELD:
			if uses(a) || (cReg && uses(c)) {
				return true
			}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_POW,
			vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if uses(a) || (bReg && uses(b)) || (cReg && uses(c)) {
				return true
			}
		case vm.OP_UNM, vm.OP_NOT, vm.OP_LEN, vm.OP_TESTSET:
			if uses(a) || uses(b) {
				return true
			}
		case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_GETGLOBAL,
			vm.OP_CLOSURE, vm.OP_GETUPVAL, vm.OP_VARARG, vm.OP_NEWTABLE,
			vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN, vm.OP_TFORCALL:
			if uses(a) {
				return true
			}
		case vm.OP_SETGLOBAL, vm.OP_SETUPVAL, vm.OP_TEST, vm.OP_SETLIST,
			vm.OP_APPEND, vm.OP_SEND:
			if uses(a) {
				return true
			}
		}
	}
	return false
}

func inferTypedSelfABIParams(proto *vm.FuncProto) ([]SpecializedABIParamRep, string) {
	params := make([]SpecializedABIParamRep, proto.NumParams)
	origins := make([]int, maxTrackedSlots)
	for i := range origins {
		origins[i] = -1
	}
	resetOrigins := func() {
		for i := range origins {
			origins[i] = -1
		}
		for i := 0; i < proto.NumParams && i < len(origins); i++ {
			origins[i] = i
		}
	}
	setParam := func(slot int, rep SpecializedABIParamRep) string {
		if slot < 0 || slot >= len(origins) || origins[slot] < 0 {
			return ""
		}
		idx := origins[slot]
		if params[idx] != SpecializedABIParamBoxed && params[idx] != rep {
			return "conflicting parameter representations"
		}
		params[idx] = rep
		return ""
	}
	setParamFromNumericPeer := func(slot int, peer specializedSlotRep, op vm.Opcode) string {
		if typedSelfSlotIsFloat(peer) {
			return setParam(slot, SpecializedABIParamRawFloat)
		}
		if op == vm.OP_DIV {
			return setParam(slot, SpecializedABIParamRawInt)
		}
		return setParam(slot, SpecializedABIParamRawInt)
	}
	slotReps := make([]specializedSlotRep, maxTrackedSlots)
	resetSlotReps := func() {
		for i := range slotReps {
			slotReps[i] = specializedSlotUnknown
		}
		for i, rep := range params {
			switch rep {
			case SpecializedABIParamRawInt:
				slotReps[i] = specializedSlotRawInt
			case SpecializedABIParamRawFloat:
				slotReps[i] = specializedSlotRawFloat
			case SpecializedABIParamRawTablePtr:
				slotReps[i] = specializedSlotRawTable
			}
		}
	}

	resetOrigins()
	resetSlotReps()
	branchTargets := specializedABIBranchTargets(proto.Code)
	for pc, inst := range proto.Code {
		if pc > 0 && branchTargets[pc] {
			resetOrigins()
			resetSlotReps()
		}
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		switch op {
		case vm.OP_MOVE:
			if a >= 0 && a < len(origins) {
				origins[a] = -1
				slotReps[a] = specializedSlotUnknown
				if b >= 0 && b < len(origins) {
					origins[a] = origins[b]
					slotReps[a] = slotReps[b]
				}
			}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD:
			leftRep, _ := typedSelfInferRKNumericRep(slotReps, proto, b)
			rightRep, _ := typedSelfInferRKNumericRep(slotReps, proto, c)
			if b < vm.RKBit {
				if reason := setParamFromNumericPeer(b, rightRep, op); reason != "" {
					return nil, reason
				}
			}
			if c < vm.RKBit {
				if reason := setParamFromNumericPeer(c, leftRep, op); reason != "" {
					return nil, reason
				}
			}
			if a >= 0 && a < len(origins) {
				origins[a] = -1
				if op == vm.OP_DIV || typedSelfSlotIsFloat(leftRep) || typedSelfSlotIsFloat(rightRep) {
					slotReps[a] = specializedSlotRawFloat
				} else {
					slotReps[a] = specializedSlotRawInt
				}
			}
		case vm.OP_UNM:
			if reason := setParam(b, SpecializedABIParamRawInt); reason != "" {
				return nil, reason
			}
			if a >= 0 && a < len(origins) {
				origins[a] = -1
				slotReps[a] = specializedSlotRawInt
			}
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if b < vm.RKBit && typedSelfConstOrOriginSuggestsInt(proto, c) {
				if reason := setParam(b, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
			if c < vm.RKBit && typedSelfConstOrOriginSuggestsInt(proto, b) {
				if reason := setParam(c, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
		case vm.OP_GETFIELD, vm.OP_GETTABLE:
			if reason := setParam(b, SpecializedABIParamRawTablePtr); reason != "" {
				return nil, reason
			}
			if op == vm.OP_GETTABLE && c < vm.RKBit {
				if reason := setParam(c, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
			if a >= 0 && a < len(origins) && (op == vm.OP_GETFIELD || op == vm.OP_GETTABLE) {
				origins[a] = -1
				switch {
				case typedSelfFeedbackResultIsFloat(proto, pc):
					slotReps[a] = specializedSlotRawFloat
				case typedSelfFeedbackResultIsInt(proto, pc):
					slotReps[a] = specializedSlotRawInt
				case typedSelfFeedbackResultIsTable(proto, pc):
					slotReps[a] = specializedSlotRawTable
				default:
					slotReps[a] = specializedSlotUnknown
				}
			}
		case vm.OP_SETFIELD:
			if reason := setParam(a, SpecializedABIParamRawTablePtr); reason != "" {
				return nil, reason
			}
		case vm.OP_SETTABLE:
			if reason := setParam(a, SpecializedABIParamRawTablePtr); reason != "" {
				return nil, reason
			}
			if b < vm.RKBit {
				if reason := setParam(b, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
		case vm.OP_FORPREP, vm.OP_FORLOOP:
			for slot := a; slot <= a+2; slot++ {
				if reason := setParam(slot, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
			if a >= 0 && a < len(origins) {
				origins[a] = -1
			}
			if op == vm.OP_FORLOOP && a+3 >= 0 && a+3 < len(origins) {
				origins[a+3] = -1
			}
		case vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL,
			vm.OP_GETGLOBAL, vm.OP_GETUPVAL, vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN,
			vm.OP_CALL, vm.OP_TESTSET, vm.OP_NOT, vm.OP_LEN, vm.OP_CONCAT,
			vm.OP_CLOSURE, vm.OP_VARARG, vm.OP_SELF, vm.OP_APPEND:
			if a >= 0 && a < len(origins) {
				origins[a] = -1
				slotReps[a] = specializedSlotUnknown
			}
		}
	}
	for i, rep := range params {
		if rep == SpecializedABIParamBoxed {
			return nil, "untyped parameter"
		}
		params[i] = rep
	}
	return params, ""
}

func typedSelfConstOrOriginSuggestsInt(proto *vm.FuncProto, idx int) bool {
	if idx >= vm.RKBit {
		return specializedABIConstIsInt(proto, idx-vm.RKBit)
	}
	return false
}

func typedSelfResetSlots(slots []specializedSlotRep, params []SpecializedABIParamRep) {
	for i := range slots {
		slots[i] = specializedSlotUnknown
	}
	for i, rep := range params {
		switch rep {
		case SpecializedABIParamRawInt:
			slots[i] = specializedSlotRawInt
		case SpecializedABIParamRawFloat:
			slots[i] = specializedSlotRawFloat
		case SpecializedABIParamRawTablePtr:
			slots[i] = specializedSlotRawTable
		}
	}
}

func typedSelfInitialTableFacts(params []SpecializedABIParamRep, argFacts map[int]FixedShapeTableFact) map[int]FixedShapeTableFact {
	if len(argFacts) == 0 {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for i, rep := range params {
		if rep != SpecializedABIParamRawTablePtr {
			continue
		}
		if fact, ok := argFacts[i]; ok {
			out[i] = fact
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func typedSelfMoveTableFact(tableFacts map[int]FixedShapeTableFact, dst, src int) {
	if tableFacts == nil {
		return
	}
	if fact, ok := tableFacts[src]; ok {
		tableFacts[dst] = fact
	} else {
		delete(tableFacts, dst)
	}
}

func typedSelfSetNestedTableFact(tableFacts map[int]FixedShapeTableFact, dst int, receiver FixedShapeTableFact, name string) {
	if tableFacts == nil || name == "" {
		return
	}
	if nested, ok := typedSelfNestedTableFactFromFact(receiver, name); ok {
		tableFacts[dst] = nested
		return
	}
	delete(tableFacts, dst)
}

func typedSelfNestedTableFactFromFact(fact FixedShapeTableFact, name string) (FixedShapeTableFact, bool) {
	if name == "" || len(fact.FieldTableFacts) == 0 {
		return FixedShapeTableFact{}, false
	}
	nested, ok := fact.FieldTableFacts[name]
	if !ok || !fixedShapeTableFactHasUsableTableFact(nested) {
		return FixedShapeTableFact{}, false
	}
	return cloneFixedShapeTableFact(nested), true
}

func typedSelfFieldTypeFromFact(fact FixedShapeTableFact, name string) (Type, bool) {
	if name == "" {
		return TypeUnknown, false
	}
	if nested, ok := typedSelfNestedTableFactFromFact(fact, name); ok && fixedShapeTableFactHasUsableTableFact(nested) {
		return TypeTable, true
	}
	if fact.FieldTypes != nil {
		if typ, ok := fact.FieldTypes[name]; ok && typ != TypeUnknown && typ != TypeAny {
			return typ, true
		}
	}
	if fact.FieldRanges != nil {
		if _, ok := fact.FieldRanges[name]; ok {
			return TypeInt, true
		}
	}
	if fact.FieldLenRanges != nil {
		if _, ok := fact.FieldLenRanges[name]; ok {
			return TypeString, true
		}
	}
	if fact.ShapeID != 0 {
		if idx, ok := fact.fieldIndex(name); ok {
			if vt, stable := runtime.ShapeFieldStableType(fact.ShapeID, idx); stable {
				if typ, ok := runtimeValueTypeToIRType(vt); ok {
					return typ, true
				}
			}
		}
	}
	return TypeUnknown, false
}

func runtimeValueTypeToIRType(vt runtime.ValueType) (Type, bool) {
	switch vt {
	case runtime.TypeInt:
		return TypeInt, true
	case runtime.TypeFloat:
		return TypeFloat, true
	case runtime.TypeBool:
		return TypeBool, true
	case runtime.TypeString:
		return TypeString, true
	case runtime.TypeTable:
		return TypeTable, true
	default:
		return TypeUnknown, false
	}
}

func typedSelfConstFieldName(proto *vm.FuncProto, constIdx int) string {
	if proto == nil || constIdx < 0 || constIdx >= len(proto.Constants) {
		return ""
	}
	key := proto.Constants[constIdx]
	if !key.IsString() {
		return ""
	}
	return key.Str()
}

func typedSelfRKIsInt(slots []specializedSlotRep, proto *vm.FuncProto, idx int) bool {
	if idx >= vm.RKBit {
		return specializedABIConstIsInt(proto, idx-vm.RKBit)
	}
	return typedSelfSlotIsInt(getSpecializedSlot(slots, idx))
}

func typedSelfRKNumericRep(slots []specializedSlotRep, proto *vm.FuncProto, idx int) (specializedSlotRep, bool) {
	if idx >= vm.RKBit {
		k := idx - vm.RKBit
		if specializedABIConstIsInt(proto, k) {
			return specializedSlotRawInt, true
		}
		if specializedABIConstIsFloat(proto, k) {
			return specializedSlotRawFloat, true
		}
		return specializedSlotUnknown, false
	}
	rep := getSpecializedSlot(slots, idx)
	if typedSelfSlotIsNumeric(rep) {
		return rep, true
	}
	return specializedSlotUnknown, false
}

func typedSelfRKIsNil(slots []specializedSlotRep, proto *vm.FuncProto, idx int) bool {
	if idx >= vm.RKBit {
		k := idx - vm.RKBit
		return k >= 0 && k < len(proto.Constants) && proto.Constants[k].IsNil()
	}
	return getSpecializedSlot(slots, idx) == specializedSlotNil
}

func typedSelfCompareOK(slots []specializedSlotRep, proto *vm.FuncProto, b, c int) bool {
	if typedSelfRKIsInt(slots, proto, b) && typedSelfRKIsInt(slots, proto, c) {
		return true
	}
	if typedSelfRKIsNil(slots, proto, b) || typedSelfRKIsNil(slots, proto, c) {
		return true
	}
	// Comparisons do not create ABI-carried values. Unknown table contents may
	// still be compared by the normal boxed/generic compare path as long as
	// they are not later treated as raw int/table arguments.
	return true
}

func typedSelfFeedbackResultIsTable(proto *vm.FuncProto, pc int) bool {
	if proto == nil || pc < 0 {
		return false
	}
	if proto.Feedback != nil && pc < len(proto.Feedback) && proto.Feedback[pc].Result == vm.FBTable {
		return true
	}
	if proto.FieldAccessFeedback != nil && pc < len(proto.FieldAccessFeedback) && proto.FieldAccessFeedback[pc].ValueType == vm.FBTable {
		return true
	}
	return proto.TableKeyFeedback != nil && pc < len(proto.TableKeyFeedback) && proto.TableKeyFeedback[pc].ValueType == vm.FBTable
}

func typedSelfFeedbackResultIsInt(proto *vm.FuncProto, pc int) bool {
	if proto == nil || pc < 0 {
		return false
	}
	if proto.Feedback != nil && pc < len(proto.Feedback) && proto.Feedback[pc].Result == vm.FBInt {
		return true
	}
	if proto.FieldAccessFeedback != nil && pc < len(proto.FieldAccessFeedback) && proto.FieldAccessFeedback[pc].ValueType == vm.FBInt {
		return true
	}
	return proto.TableKeyFeedback != nil && pc < len(proto.TableKeyFeedback) && proto.TableKeyFeedback[pc].ValueType == vm.FBInt
}

func typedSelfFeedbackResultIsFloat(proto *vm.FuncProto, pc int) bool {
	if proto == nil || pc < 0 {
		return false
	}
	if proto.Feedback != nil && pc < len(proto.Feedback) && proto.Feedback[pc].Result == vm.FBFloat {
		return true
	}
	if proto.FieldAccessFeedback != nil && pc < len(proto.FieldAccessFeedback) && proto.FieldAccessFeedback[pc].ValueType == vm.FBFloat {
		return true
	}
	return proto.TableKeyFeedback != nil && pc < len(proto.TableKeyFeedback) && proto.TableKeyFeedback[pc].ValueType == vm.FBFloat
}

func typedSelfParamFieldType(proto *vm.FuncProto, paramSlot, constIdx int) (Type, bool) {
	return typedSelfParamFieldTypeWithFacts(proto, paramSlot, constIdx, nil)
}

func typedSelfParamFieldTypeWithFacts(proto *vm.FuncProto, paramSlot, constIdx int, argFacts map[int]FixedShapeTableFact) (Type, bool) {
	if proto == nil || paramSlot < 0 || paramSlot >= maxTrackedSlots ||
		constIdx < 0 || constIdx >= len(proto.Constants) ||
		(len(proto.ArgShapeFeedback) <= paramSlot && len(proto.ArgArrayElementShapeFeedback) <= paramSlot && len(argFacts) == 0) {
		return TypeUnknown, false
	}
	key := proto.Constants[constIdx]
	if !key.IsString() {
		return TypeUnknown, false
	}
	if fact, ok := argFacts[paramSlot]; ok {
		if typ, ok := typedSelfFieldTypeFromFact(fact, key.Str()); ok {
			return typ, true
		}
	}
	if paramSlot >= proto.NumParams {
		return TypeUnknown, false
	}
	if len(proto.ArgShapeFeedback) > paramSlot {
		feedback := proto.ArgShapeFeedback[paramSlot]
		if len(feedback.FieldTypes) > 0 {
			if typ, ok := feedbackToIRType(feedback.FieldTypes[key.Str()]); ok {
				return typ, true
			}
		}
		if len(feedback.FieldRanges) > 0 {
			if _, _, ok := feedback.FieldRanges[key.Str()].StableRange(); ok {
				return TypeInt, true
			}
		}
		if len(feedback.FieldLenRanges) > 0 {
			if _, _, ok := feedback.FieldLenRanges[key.Str()].StableRange(); ok {
				return TypeString, true
			}
		}
	}
	if len(proto.ArgArrayElementShapeFeedback) <= paramSlot {
		return TypeUnknown, false
	}
	feedback := proto.ArgArrayElementShapeFeedback[paramSlot]
	if len(feedback.FieldTypes) == 0 {
		return TypeUnknown, false
	}
	if len(feedback.FieldRanges) > 0 {
		if _, _, ok := feedback.FieldRanges[key.Str()].StableRange(); ok {
			return TypeInt, true
		}
	}
	if len(feedback.FieldLenRanges) > 0 {
		if _, _, ok := feedback.FieldLenRanges[key.Str()].StableRange(); ok {
			return TypeString, true
		}
	}
	typ, ok := feedbackToIRType(feedback.FieldTypes[key.Str()])
	return typ, ok
}

func typedSelfBranchFacts(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int) map[int]specializedSlotRep {
	return typedSelfBranchFactsWithGlobals(proto, params, pc, nil)
}

func typedSelfBranchFactsWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int, numericGlobals map[string]runtime.Value) map[int]specializedSlotRep {
	return typedSelfBranchFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, nil)
}

func typedSelfBranchFactsWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) map[int]specializedSlotRep {
	if proto == nil || pc < 0 {
		return nil
	}
	if typedSelfHasFallthroughPred(proto, pc) {
		return nil
	}
	var facts map[int]specializedSlotRep
	havePred := false
	mergePredFacts := func(pred map[int]specializedSlotRep) {
		if !havePred {
			havePred = true
			if len(pred) == 0 {
				return
			}
			facts = make(map[int]specializedSlotRep, len(pred))
			for slot, rep := range pred {
				facts[slot] = rep
			}
			return
		}
		for slot, rep := range facts {
			if predRep, ok := pred[slot]; !ok || predRep != rep {
				delete(facts, slot)
			}
		}
	}
	for srcPC, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_JMP:
			target := srcPC + 1 + vm.DecodesBx(inst)
			if target == pc {
				if slots, ok := typedSelfLoopFactSlotsAtPCWithFactsAndGlobals(proto, params, srcPC, numericGlobals, globalArrayElementFacts); ok {
					mergePredFacts(typedSelfSlotFacts(slots))
				}
			}
			continue
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
			target := srcPC + 2
			if target == pc {
				if slots, ok := typedSelfLoopFactSlotsAtPCWithFactsAndGlobals(proto, params, srcPC, numericGlobals, globalArrayElementFacts); ok {
					mergePredFacts(typedSelfSlotFacts(slots))
				}
			}
			continue
		case vm.OP_FORLOOP:
		default:
			continue
		}
		bodyTarget := srcPC + 1 + vm.DecodesBx(inst)
		exitTarget := srcPC + 1
		if bodyTarget != pc && exitTarget != pc {
			continue
		}
		a := vm.DecodeA(inst)
		if typedSelfForLoopControlProvenIntWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts) {
			pred := make(map[int]specializedSlotRep)
			addFact := func(slot int, rep specializedSlotRep) {
				if slot >= 0 && slot < maxTrackedSlots {
					pred[slot] = rep
				}
			}
			bodyTarget := srcPC + 1 + vm.DecodesBx(inst)
			if preSlots, ok := typedSelfForLoopPreSlotsWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts); ok {
				for slot, rep := range preSlots {
					if typedSelfLoopBodyWritesSlot(proto, bodyTarget, srcPC, slot) {
						continue
					}
					switch rep {
					case specializedSlotRawInt, specializedSlotRawTable, specializedSlotNil,
						specializedSlotSelfFunc, specializedSlotOtherFunc, specializedSlotStdMathTable,
						specializedSlotMathSqrtFunc, specializedSlotMathFloorFunc:
						addFact(slot, rep)
					}
				}
			}
			preSlots, postSlots, ok := typedSelfForLoopStableSlotsWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts)
			if ok {
				for slot, pre := range preSlots {
					if pre != postSlots[slot] {
						continue
					}
					switch pre {
					case specializedSlotRawInt, specializedSlotRawTable, specializedSlotNil,
						specializedSlotSelfFunc, specializedSlotOtherFunc, specializedSlotStdMathTable,
						specializedSlotMathSqrtFunc, specializedSlotMathFloorFunc:
						addFact(slot, pre)
					}
				}
			}
			addFact(a, specializedSlotRawInt)
			if bodyTarget == pc {
				addFact(a+3, specializedSlotRawInt)
			}
			mergePredFacts(pred)
		}
	}
	if !havePred {
		return nil
	}
	return facts
}

func typedSelfCollectSlotFacts(slots []specializedSlotRep, addFact func(int, specializedSlotRep)) {
	for slot, rep := range slots {
		switch rep {
		case specializedSlotRawInt, specializedSlotRawTable, specializedSlotNil,
			specializedSlotSelfFunc, specializedSlotOtherFunc, specializedSlotStdMathTable,
			specializedSlotMathSqrtFunc, specializedSlotMathFloorFunc:
			addFact(slot, rep)
		}
	}
}

func typedSelfSlotFacts(slots []specializedSlotRep) map[int]specializedSlotRep {
	facts := make(map[int]specializedSlotRep)
	typedSelfCollectSlotFacts(slots, func(slot int, rep specializedSlotRep) {
		facts[slot] = rep
	})
	return facts
}

func typedSelfHasFallthroughPred(proto *vm.FuncProto, pc int) bool {
	if proto == nil || pc <= 0 || pc > len(proto.Code) {
		return false
	}
	switch vm.DecodeOp(proto.Code[pc-1]) {
	case vm.OP_JMP, vm.OP_RETURN, vm.OP_FORPREP, vm.OP_FORLOOP:
		return false
	default:
		return true
	}
}

func typedSelfApplyBranchFacts(slots []specializedSlotRep, facts map[int]specializedSlotRep) {
	for slot, rep := range facts {
		setSpecializedSlot(slots, slot, rep)
	}
}

func typedSelfCallArgSlotMatches(proto *vm.FuncProto, callPC, argIndex int, param SpecializedABIParamRep) bool {
	if proto == nil || callPC < 0 || callPC >= len(proto.Code) {
		return false
	}
	inst := proto.Code[callPC]
	if vm.DecodeOp(inst) != vm.OP_CALL {
		return false
	}
	params, reason := inferTypedSelfABIParams(proto)
	if reason != "" || argIndex < 0 || argIndex >= len(params) {
		return false
	}
	callSlot := vm.DecodeA(inst)
	argSlot := callSlot + 1 + argIndex
	rep, ok := typedSelfSlotRepAtPC(proto, params, callPC, argSlot)
	return ok && typedSelfSlotMatchesParam(rep, param)
}

func typedSelfSlotRepAtPC(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC, slot int) (specializedSlotRep, bool) {
	slots, ok := typedSelfSlotsAtPC(proto, params, targetPC)
	if !ok || slot < 0 || slot >= len(slots) {
		return specializedSlotUnknown, false
	}
	return getSpecializedSlot(slots, slot), true
}

func typedSelfSlotsAtPC(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int) ([]specializedSlotRep, bool) {
	return typedSelfSlotsAtPCWithGlobals(proto, params, targetPC, nil)
}

func typedSelfSlotsAtPCWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int, numericGlobals map[string]runtime.Value) ([]specializedSlotRep, bool) {
	return typedSelfSlotsAtPCWithFactsAndGlobals(proto, params, targetPC, numericGlobals, nil)
}

func typedSelfSlotsAtPCWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) ([]specializedSlotRep, bool) {
	if proto == nil || targetPC < 0 || targetPC > len(proto.Code) {
		return nil, false
	}
	slots := make([]specializedSlotRep, maxTrackedSlots)
	typedSelfResetSlots(slots, params)
	branchTargets := specializedABIBranchTargets(proto.Code)
	for pc := 0; pc < targetPC; pc++ {
		if pc > 0 && branchTargets[pc] {
			typedSelfResetSlots(slots, params)
			typedSelfApplyBranchFacts(slots, typedSelfBranchFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts))
		}
		if !typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto, slots, pc, numericGlobals, globalArrayElementFacts) {
			return nil, false
		}
	}
	return slots, true
}

func typedSelfLoopFactSlotsAtPC(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int) ([]specializedSlotRep, bool) {
	return typedSelfLoopFactSlotsAtPCWithGlobals(proto, params, targetPC, nil)
}

func typedSelfLoopFactSlotsAtPCWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int, numericGlobals map[string]runtime.Value) ([]specializedSlotRep, bool) {
	return typedSelfLoopFactSlotsAtPCWithFactsAndGlobals(proto, params, targetPC, numericGlobals, nil)
}

func typedSelfLoopFactSlotsAtPCWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) ([]specializedSlotRep, bool) {
	if proto == nil || targetPC < 0 || targetPC > len(proto.Code) {
		return nil, false
	}
	slots := make([]specializedSlotRep, maxTrackedSlots)
	typedSelfResetSlots(slots, params)
	branchTargets := specializedABIBranchTargets(proto.Code)
	for pc := 0; pc < targetPC; pc++ {
		if pc > 0 && branchTargets[pc] {
			typedSelfResetSlots(slots, params)
			typedSelfApplyBranchFacts(slots, typedSelfForLoopBranchFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts))
		}
		if !typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto, slots, pc, numericGlobals, globalArrayElementFacts) {
			return nil, false
		}
	}
	return slots, true
}

func typedSelfForLoopBranchFacts(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int) map[int]specializedSlotRep {
	return typedSelfForLoopBranchFactsWithGlobals(proto, params, pc, nil)
}

func typedSelfForLoopBranchFactsWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int, numericGlobals map[string]runtime.Value) map[int]specializedSlotRep {
	return typedSelfForLoopBranchFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, nil)
}

func typedSelfForLoopBranchFactsWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) map[int]specializedSlotRep {
	if proto == nil || pc < 0 {
		return nil
	}
	var facts map[int]specializedSlotRep
	addFact := func(slot int, rep specializedSlotRep) {
		if slot < 0 || slot >= maxTrackedSlots {
			return
		}
		if facts == nil {
			facts = make(map[int]specializedSlotRep)
		}
		facts[slot] = rep
	}
	for srcPC, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_FORLOOP {
			continue
		}
		bodyTarget := srcPC + 1 + vm.DecodesBx(inst)
		exitTarget := srcPC + 1
		if bodyTarget != pc && exitTarget != pc {
			continue
		}
		a := vm.DecodeA(inst)
		if !typedSelfForLoopControlProvenIntWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts) {
			continue
		}
		if preSlots, ok := typedSelfForLoopPreSlotsWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts); ok {
			for slot, rep := range preSlots {
				if typedSelfLoopBodyWritesSlot(proto, bodyTarget, srcPC, slot) {
					continue
				}
				switch rep {
				case specializedSlotRawInt, specializedSlotRawTable, specializedSlotNil,
					specializedSlotSelfFunc, specializedSlotOtherFunc, specializedSlotStdMathTable,
					specializedSlotMathSqrtFunc, specializedSlotMathFloorFunc:
					addFact(slot, rep)
				}
			}
		}
		preSlots, postSlots, ok := typedSelfForLoopStableSlotsWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts)
		if ok {
			for slot, pre := range preSlots {
				if pre == postSlots[slot] {
					switch pre {
					case specializedSlotRawInt, specializedSlotRawTable, specializedSlotNil,
						specializedSlotSelfFunc, specializedSlotOtherFunc, specializedSlotStdMathTable,
						specializedSlotMathSqrtFunc, specializedSlotMathFloorFunc:
						addFact(slot, pre)
					}
				}
			}
		}
		addFact(a, specializedSlotRawInt)
		if bodyTarget == pc {
			addFact(a+3, specializedSlotRawInt)
		}
	}
	return facts
}

func typedSelfForLoopBranchTableFactsWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, pc int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) map[int]FixedShapeTableFact {
	if proto == nil || pc < 0 {
		return nil
	}
	var facts map[int]FixedShapeTableFact
	addFact := func(slot int, fact FixedShapeTableFact) {
		if slot < 0 || slot >= maxTrackedSlots || !fixedShapeTableFactHasUsableTableFact(fact) {
			return
		}
		if facts == nil {
			facts = make(map[int]FixedShapeTableFact)
		}
		facts[slot] = cloneFixedShapeTableFact(fact)
	}
	for srcPC, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_FORLOOP {
			continue
		}
		bodyTarget := srcPC + 1 + vm.DecodesBx(inst)
		exitTarget := srcPC + 1
		if bodyTarget != pc && exitTarget != pc {
			continue
		}
		a := vm.DecodeA(inst)
		if !typedSelfForLoopControlProvenIntWithFactsAndGlobals(proto, params, srcPC, a, numericGlobals, globalArrayElementFacts) {
			continue
		}
		prepPC := typedSelfFindForPrep(proto, srcPC, a)
		if prepPC < 0 {
			continue
		}
		preFacts, ok := typedSelfTableFactsAtPCWithFactsAndGlobals(proto, params, prepPC, numericGlobals, globalArrayElementFacts)
		if !ok {
			continue
		}
		for slot, fact := range preFacts {
			if typedSelfLoopBodyWritesSlot(proto, bodyTarget, srcPC, slot) {
				continue
			}
			addFact(slot, fact)
		}
	}
	return facts
}

func typedSelfTableFactsAtPCWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, targetPC int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) (map[int]FixedShapeTableFact, bool) {
	if proto == nil || targetPC < 0 || targetPC > len(proto.Code) {
		return nil, false
	}
	slots := make([]specializedSlotRep, maxTrackedSlots)
	typedSelfResetSlots(slots, params)
	tableFacts := make(map[int]FixedShapeTableFact)
	branchTargets := specializedABIBranchTargets(proto.Code)
	for pc := 0; pc < targetPC; pc++ {
		if pc > 0 && branchTargets[pc] {
			typedSelfResetSlots(slots, params)
			tableFacts = make(map[int]FixedShapeTableFact)
			typedSelfApplyBranchFacts(slots, typedSelfBranchFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts))
			for slot, fact := range typedSelfForLoopBranchTableFactsWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts) {
				tableFacts[slot] = fact
			}
		}
		if !typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto, slots, pc, numericGlobals, globalArrayElementFacts) {
			return nil, false
		}
		typedSelfAdvanceSimpleTableFacts(proto, slots, tableFacts, pc, globalArrayElementFacts)
	}
	return tableFacts, true
}

func typedSelfAdvanceSimpleTableFacts(proto *vm.FuncProto, slots []specializedSlotRep, tableFacts map[int]FixedShapeTableFact, pc int, globalArrayElementFacts map[string]FixedShapeTableFact) {
	if proto == nil || pc < 0 || pc >= len(proto.Code) || tableFacts == nil {
		return
	}
	inst := proto.Code[pc]
	op := vm.DecodeOp(inst)
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	kill := func(slot int) {
		if slot >= 0 && slot < maxTrackedSlots {
			delete(tableFacts, slot)
		}
	}
	switch op {
	case vm.OP_MOVE:
		if fact, ok := tableFacts[b]; ok {
			tableFacts[a] = cloneFixedShapeTableFact(fact)
		} else {
			kill(a)
		}
	case vm.OP_GETGLOBAL:
		if fact, ok := typedSelfGlobalArrayElementFact(proto, vm.DecodeBx(inst), globalArrayElementFacts); ok {
			tableFacts[a] = fact
		} else {
			kill(a)
		}
	case vm.OP_GETTABLE:
		if typedSelfSlotIsTable(getSpecializedSlot(slots, b)) && typedSelfRKIsInt(slots, proto, c) {
			if fact, ok := tableFacts[b]; ok && fixedShapeTableFactHasUsableTableFact(fact) {
				tableFacts[a] = cloneFixedShapeTableFact(fact)
				return
			}
		}
		kill(a)
	case vm.OP_GETFIELD:
		name := typedSelfConstFieldName(proto, c)
		if fact, ok := tableFacts[b]; ok {
			if nested, ok := typedSelfNestedTableFactFromFact(fact, name); ok {
				tableFacts[a] = nested
				return
			}
		}
		kill(a)
	case vm.OP_LOADNIL:
		for slot := a; slot <= a+b && slot < maxTrackedSlots; slot++ {
			kill(slot)
		}
	case vm.OP_CALL:
		callC := vm.DecodeC(inst)
		if callC == 0 {
			for slot := a; slot < maxTrackedSlots; slot++ {
				kill(slot)
			}
		} else {
			for slot := a; slot < a+callC-1 && slot < maxTrackedSlots; slot++ {
				kill(slot)
			}
		}
	case vm.OP_SETFIELD, vm.OP_SETTABLE, vm.OP_SETGLOBAL, vm.OP_SETUPVAL, vm.OP_JMP, vm.OP_EQ,
		vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_RETURN, vm.OP_CLOSE:
		return
	case vm.OP_FORLOOP:
		kill(a)
		kill(a + 3)
	case vm.OP_FORPREP:
		kill(a)
	case vm.OP_SELF:
		kill(a)
		kill(a + 1)
	case vm.OP_TFORCALL:
		callC := vm.DecodeC(inst)
		for slot := a + 3; slot < a+3+callC && slot < maxTrackedSlots; slot++ {
			kill(slot)
		}
	default:
		kill(a)
	}
}

func typedSelfForLoopStableSlots(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int) ([]specializedSlotRep, []specializedSlotRep, bool) {
	return typedSelfForLoopStableSlotsWithGlobals(proto, params, forLoopPC, a, nil)
}

func typedSelfForLoopStableSlotsWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, numericGlobals map[string]runtime.Value) ([]specializedSlotRep, []specializedSlotRep, bool) {
	return typedSelfForLoopStableSlotsWithFactsAndGlobals(proto, params, forLoopPC, a, numericGlobals, nil)
}

func typedSelfForLoopStableSlotsWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) ([]specializedSlotRep, []specializedSlotRep, bool) {
	if proto == nil || forLoopPC <= 0 {
		return nil, nil, false
	}
	prepPC := typedSelfFindForPrep(proto, forLoopPC, a)
	if prepPC < 0 {
		return nil, nil, false
	}
	slots := make([]specializedSlotRep, maxTrackedSlots)
	typedSelfResetSlots(slots, params)
	for pc := 0; pc <= prepPC; pc++ {
		if !typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto, slots, pc, numericGlobals, globalArrayElementFacts) {
			return nil, nil, false
		}
	}
	preSlots := append([]specializedSlotRep(nil), slots...)
	for pc := prepPC + 1; pc < forLoopPC; pc++ {
		if !typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto, slots, pc, numericGlobals, globalArrayElementFacts) {
			return nil, nil, false
		}
	}
	postSlots := append([]specializedSlotRep(nil), slots...)
	return preSlots, postSlots, true
}

func typedSelfForLoopPreSlotsWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, numericGlobals map[string]runtime.Value) ([]specializedSlotRep, bool) {
	return typedSelfForLoopPreSlotsWithFactsAndGlobals(proto, params, forLoopPC, a, numericGlobals, nil)
}

func typedSelfForLoopPreSlotsWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) ([]specializedSlotRep, bool) {
	if proto == nil || forLoopPC <= 0 {
		return nil, false
	}
	prepPC := typedSelfFindForPrep(proto, forLoopPC, a)
	if prepPC < 0 {
		return nil, false
	}
	return typedSelfSlotsAtPCWithFactsAndGlobals(proto, params, prepPC, numericGlobals, globalArrayElementFacts)
}

func typedSelfFindForPrep(proto *vm.FuncProto, forLoopPC, a int) int {
	if proto == nil {
		return -1
	}
	for pc := forLoopPC - 1; pc >= 0; pc-- {
		if vm.DecodeOp(proto.Code[pc]) == vm.OP_FORPREP && vm.DecodeA(proto.Code[pc]) == a {
			return pc
		}
	}
	return -1
}

func typedSelfLoopBodyWritesSlot(proto *vm.FuncProto, bodyStart, forLoopPC, slot int) bool {
	if proto == nil || slot < 0 || bodyStart < 0 || forLoopPC < bodyStart || forLoopPC > len(proto.Code) {
		return true
	}
	for pc := bodyStart; pc < forLoopPC; pc++ {
		if typedSelfInstrWritesSlot(proto.Code[pc], slot) {
			return true
		}
	}
	return false
}

func typedSelfInstrWritesSlot(inst uint32, slot int) bool {
	op := vm.DecodeOp(inst)
	a := vm.DecodeA(inst)
	switch op {
	case vm.OP_SETUPVAL, vm.OP_SETGLOBAL, vm.OP_SETFIELD, vm.OP_SETTABLE, vm.OP_SETLIST,
		vm.OP_JMP, vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_RETURN,
		vm.OP_CLOSE, vm.OP_TFORLOOP:
		return false
	case vm.OP_LOADNIL:
		b := vm.DecodeB(inst)
		return slot >= a && slot <= a+b
	case vm.OP_CALL:
		c := vm.DecodeC(inst)
		if c == 0 {
			return slot >= a
		}
		return slot >= a && slot < a+c-1
	case vm.OP_FORPREP:
		return slot == a
	case vm.OP_FORLOOP:
		return slot == a || slot == a+3
	case vm.OP_SELF:
		return slot == a || slot == a+1
	case vm.OP_TFORCALL:
		c := vm.DecodeC(inst)
		return slot >= a+3 && slot < a+3+c
	default:
		return slot == a
	}
}

func typedSelfApplyStableForLoopFacts(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, slots []specializedSlotRep) {
	typedSelfApplyStableForLoopFactsWithGlobals(proto, params, forLoopPC, a, slots, nil)
}

func typedSelfApplyStableForLoopFactsWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, slots []specializedSlotRep, numericGlobals map[string]runtime.Value) {
	typedSelfApplyStableForLoopFactsWithFactsAndGlobals(proto, params, forLoopPC, a, slots, numericGlobals, nil)
}

func typedSelfApplyStableForLoopFactsWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, slots []specializedSlotRep, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) {
	preSlots, postSlots, ok := typedSelfForLoopStableSlotsWithFactsAndGlobals(proto, params, forLoopPC, a, numericGlobals, globalArrayElementFacts)
	if ok {
		for slot, pre := range preSlots {
			if pre != postSlots[slot] {
				continue
			}
			switch pre {
			case specializedSlotRawInt, specializedSlotRawTable, specializedSlotNil,
				specializedSlotSelfFunc, specializedSlotOtherFunc, specializedSlotStdMathTable,
				specializedSlotMathSqrtFunc, specializedSlotMathFloorFunc:
				setSpecializedSlot(slots, slot, pre)
			}
		}
	}
	setSpecializedSlot(slots, a, specializedSlotRawInt)
	setSpecializedSlot(slots, a+1, specializedSlotRawInt)
	setSpecializedSlot(slots, a+2, specializedSlotRawInt)
}

func typedSelfForLoopControlProvenInt(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int) bool {
	return typedSelfForLoopControlProvenIntWithGlobals(proto, params, forLoopPC, a, nil)
}

func typedSelfForLoopControlProvenIntWithGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, numericGlobals map[string]runtime.Value) bool {
	return typedSelfForLoopControlProvenIntWithFactsAndGlobals(proto, params, forLoopPC, a, numericGlobals, nil)
}

func typedSelfForLoopControlProvenIntWithFactsAndGlobals(proto *vm.FuncProto, params []SpecializedABIParamRep, forLoopPC, a int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) bool {
	if proto == nil || forLoopPC <= 0 {
		return false
	}
	for pc := forLoopPC - 1; pc >= 0; pc-- {
		inst := proto.Code[pc]
		if vm.DecodeOp(inst) != vm.OP_FORPREP || vm.DecodeA(inst) != a {
			continue
		}
		slots, ok := typedSelfSlotsAtPCWithFactsAndGlobals(proto, params, pc, numericGlobals, globalArrayElementFacts)
		if !ok {
			return false
		}
		return typedSelfSlotIsInt(getSpecializedSlot(slots, a)) &&
			typedSelfSlotIsInt(getSpecializedSlot(slots, a+1)) &&
			typedSelfSlotIsInt(getSpecializedSlot(slots, a+2))
	}
	return false
}

func typedSelfAdvanceSimpleSlotFact(proto *vm.FuncProto, slots []specializedSlotRep, pc int) bool {
	return typedSelfAdvanceSimpleSlotFactWithGlobals(proto, slots, pc, nil)
}

func typedSelfAdvanceSimpleSlotFactWithGlobals(proto *vm.FuncProto, slots []specializedSlotRep, pc int, numericGlobals map[string]runtime.Value) bool {
	return typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto, slots, pc, numericGlobals, nil)
}

func typedSelfAdvanceSimpleSlotFactWithGlobalFacts(proto *vm.FuncProto, slots []specializedSlotRep, pc int, numericGlobals map[string]runtime.Value, globalArrayElementFacts map[string]FixedShapeTableFact) bool {
	inst := proto.Code[pc]
	op := vm.DecodeOp(inst)
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	switch op {
	case vm.OP_LOADINT:
		setSpecializedSlot(slots, a, specializedSlotRawInt)
	case vm.OP_LOADK:
		if specializedABIConstIsInt(proto, vm.DecodeBx(inst)) {
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		} else {
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		}
	case vm.OP_MOVE:
		setSpecializedSlot(slots, a, getSpecializedSlot(slots, b))
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
		if typedSelfRKIsInt(slots, proto, b) && typedSelfRKIsInt(slots, proto, c) {
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		} else {
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		}
	case vm.OP_GETTABLE, vm.OP_GETFIELD:
		if op == vm.OP_GETFIELD && getSpecializedSlot(slots, b) == specializedSlotStdMathTable && typedSelfConstFieldName(proto, c) == "sqrt" {
			setSpecializedSlot(slots, a, specializedSlotMathSqrtFunc)
		} else if op == vm.OP_GETFIELD && getSpecializedSlot(slots, b) == specializedSlotStdMathTable && typedSelfConstFieldName(proto, c) == "floor" {
			setSpecializedSlot(slots, a, specializedSlotMathFloorFunc)
		} else if op == vm.OP_GETTABLE && typedSelfSlotIsTable(getSpecializedSlot(slots, b)) && typedSelfRKIsInt(slots, proto, c) {
			setSpecializedSlot(slots, a, specializedSlotRawTable)
		} else if typedSelfFeedbackResultIsInt(proto, pc) {
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		} else if typedSelfFeedbackResultIsTable(proto, pc) {
			setSpecializedSlot(slots, a, specializedSlotRawTable)
		} else {
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		}
	case vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN:
		setSpecializedSlot(slots, a, specializedSlotRawTable)
	case vm.OP_LOADNIL:
		for slot := a; slot <= a+b && slot < len(slots); slot++ {
			setSpecializedSlot(slots, slot, specializedSlotNil)
		}
	case vm.OP_GETGLOBAL:
		if specializedABIConstString(proto, vm.DecodeBx(inst)) == proto.Name {
			setSpecializedSlot(slots, a, specializedSlotSelfFunc)
		} else if rep, ok := typedSelfNumericGlobalRep(proto, vm.DecodeBx(inst), numericGlobals); ok {
			setSpecializedSlot(slots, a, rep)
		} else if _, ok := typedSelfGlobalArrayElementFact(proto, vm.DecodeBx(inst), globalArrayElementFacts); ok {
			setSpecializedSlot(slots, a, specializedSlotRawTable)
		} else if specializedABIConstString(proto, vm.DecodeBx(inst)) == "math" {
			setSpecializedSlot(slots, a, specializedSlotStdMathTable)
		} else {
			setSpecializedSlot(slots, a, specializedSlotOtherFunc)
		}
	case vm.OP_CALL:
		if typedSelfSlotIsMathUnaryFunc(getSpecializedSlot(slots, a)) && b == 2 && c == 2 &&
			typedSelfSlotIsNumeric(getSpecializedSlot(slots, a+1)) {
			if getSpecializedSlot(slots, a) == specializedSlotMathFloorFunc {
				setSpecializedSlot(slots, a, specializedSlotRawInt)
			} else {
				setSpecializedSlot(slots, a, specializedSlotRawFloat)
			}
		} else {
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		}
	case vm.OP_FORPREP:
		if typedSelfSlotIsInt(getSpecializedSlot(slots, a)) &&
			typedSelfSlotIsInt(getSpecializedSlot(slots, a+1)) &&
			typedSelfSlotIsInt(getSpecializedSlot(slots, a+2)) {
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		} else {
			return false
		}
	case vm.OP_LE, vm.OP_LT, vm.OP_EQ, vm.OP_JMP, vm.OP_SETUPVAL, vm.OP_CLOSE,
		vm.OP_SETTABLE, vm.OP_SETFIELD:
	default:
		setSpecializedSlot(slots, a, specializedSlotUnknown)
	}
	return true
}

func typedSelfNumericGlobalRep(proto *vm.FuncProto, constIdx int, numericGlobals map[string]runtime.Value) (specializedSlotRep, bool) {
	if proto == nil || len(numericGlobals) == 0 || constIdx < 0 || constIdx >= len(proto.Constants) {
		return specializedSlotUnknown, false
	}
	c := proto.Constants[constIdx]
	if !c.IsString() {
		return specializedSlotUnknown, false
	}
	v, ok := numericGlobals[c.Str()]
	if !ok {
		return specializedSlotUnknown, false
	}
	if v.IsInt() {
		return specializedSlotRawInt, true
	}
	if v.IsFloat() {
		return specializedSlotRawFloat, true
	}
	return specializedSlotUnknown, false
}

func typedSelfGlobalArrayElementFact(proto *vm.FuncProto, constIdx int, globalFacts map[string]FixedShapeTableFact) (FixedShapeTableFact, bool) {
	if proto == nil || len(globalFacts) == 0 || constIdx < 0 || constIdx >= len(proto.Constants) {
		return FixedShapeTableFact{}, false
	}
	c := proto.Constants[constIdx]
	if !c.IsString() {
		return FixedShapeTableFact{}, false
	}
	fact, ok := globalFacts[c.Str()]
	if !ok || !fixedShapeTableFactHasUsableTableFact(fact) {
		return FixedShapeTableFact{}, false
	}
	return cloneFixedShapeTableFact(fact), true
}

func typedSelfSlotIsInt(rep specializedSlotRep) bool {
	return rep == specializedSlotRawInt || rep == specializedSlotSelfCallRawInt
}

func specializedSlotRepName(rep specializedSlotRep) string {
	switch rep {
	case specializedSlotUnknown:
		return "unknown"
	case specializedSlotRawInt:
		return "raw-int"
	case specializedSlotRawFloat:
		return "raw-float"
	case specializedSlotRawTable:
		return "raw-table"
	case specializedSlotRawString:
		return "raw-string"
	case specializedSlotNil:
		return "nil"
	case specializedSlotSelfCallRawInt:
		return "self-call-raw-int"
	case specializedSlotSelfCallRawFloat:
		return "self-call-raw-float"
	case specializedSlotSelfCallRawTable:
		return "self-call-raw-table"
	case specializedSlotSelfFunc:
		return "self-func"
	case specializedSlotOtherFunc:
		return "other-func"
	case specializedSlotStdMathTable:
		return "std-math-table"
	case specializedSlotMathSqrtFunc:
		return "math.sqrt"
	case specializedSlotMathFloorFunc:
		return "math.floor"
	default:
		return "invalid"
	}
}

func typedSelfSlotIsFloat(rep specializedSlotRep) bool {
	return rep == specializedSlotRawFloat || rep == specializedSlotSelfCallRawFloat
}

func typedSelfSlotIsNumeric(rep specializedSlotRep) bool {
	return typedSelfSlotIsInt(rep) || typedSelfSlotIsFloat(rep)
}

func typedSelfNumericBaseRep(rep specializedSlotRep) specializedSlotRep {
	if typedSelfSlotIsFloat(rep) {
		return specializedSlotRawFloat
	}
	if typedSelfSlotIsInt(rep) {
		return specializedSlotRawInt
	}
	return specializedSlotUnknown
}

func typedSelfSlotIsTable(rep specializedSlotRep) bool {
	return rep == specializedSlotRawTable || rep == specializedSlotSelfCallRawTable || rep == specializedSlotStdMathTable
}

func typedSelfSlotIsMathUnaryFunc(rep specializedSlotRep) bool {
	return rep == specializedSlotMathSqrtFunc || rep == specializedSlotMathFloorFunc
}

func typedSelfSlotIsString(rep specializedSlotRep) bool {
	return rep == specializedSlotRawString
}

func typedSelfSlotMatchesParam(rep specializedSlotRep, param SpecializedABIParamRep) bool {
	switch param {
	case SpecializedABIParamRawInt:
		return typedSelfSlotIsInt(rep)
	case SpecializedABIParamRawFloat:
		return typedSelfSlotIsFloat(rep)
	case SpecializedABIParamRawTablePtr:
		return typedSelfSlotIsTable(rep)
	default:
		return false
	}
}

func typedSelfInferRKNumericRep(slots []specializedSlotRep, proto *vm.FuncProto, idx int) (specializedSlotRep, bool) {
	if idx >= vm.RKBit {
		k := idx - vm.RKBit
		if specializedABIConstIsFloat(proto, k) {
			return specializedSlotRawFloat, true
		}
		if specializedABIConstIsInt(proto, k) {
			return specializedSlotRawInt, true
		}
		return specializedSlotUnknown, false
	}
	if idx >= 0 && idx < len(slots) && typedSelfSlotIsNumeric(slots[idx]) {
		return slots[idx], true
	}
	return specializedSlotUnknown, false
}

func typedSelfReturnRep(slot specializedSlotRep, current SpecializedABIReturnRep) SpecializedABIReturnRep {
	switch slot {
	case specializedSlotRawInt:
		return SpecializedABIReturnRawInt
	case specializedSlotRawFloat:
		return SpecializedABIReturnRawFloat
	case specializedSlotRawTable:
		return SpecializedABIReturnRawTablePtr
	case specializedSlotSelfCallRawInt:
		return SpecializedABIReturnRawInt
	case specializedSlotSelfCallRawFloat:
		return SpecializedABIReturnRawFloat
	case specializedSlotSelfCallRawTable:
		return SpecializedABIReturnRawTablePtr
	case specializedSlotUnknown:
		if current != SpecializedABIReturnNone {
			return current
		}
		return SpecializedABIReturnBoxed
	default:
		return SpecializedABIReturnBoxed
	}
}

func specializedABIReject(reason string) SpecializedABI {
	return SpecializedABI{
		Kind:      SpecializedABINone,
		Return:    SpecializedABIReturnBoxed,
		RejectWhy: reason,
	}
}

func specializedABIBranchTargets(code []uint32) map[int]bool {
	targets := make(map[int]bool)
	for pc, inst := range code {
		switch vm.DecodeOp(inst) {
		case vm.OP_JMP:
			tgt := pc + 1 + vm.DecodesBx(inst)
			if tgt >= 0 && tgt < len(code) {
				targets[tgt] = true
			}
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
			tgt := pc + 2
			if tgt >= 0 && tgt < len(code) {
				targets[tgt] = true
			}
		case vm.OP_FORPREP, vm.OP_FORLOOP:
			tgt := pc + 1 + vm.DecodesBx(inst)
			if tgt >= 0 && tgt < len(code) {
				targets[tgt] = true
			}
		}
	}
	return targets
}

func specializedABIRKIsRawInt(slots []specializedSlotRep, proto *vm.FuncProto, idx int) bool {
	if idx >= vm.RKBit {
		return specializedABIConstIsInt(proto, idx-vm.RKBit)
	}
	return specializedABIRepIsRawInt(getSpecializedSlot(slots, idx))
}

func specializedABIRepIsRawInt(rep specializedSlotRep) bool {
	return rep == specializedSlotRawInt || rep == specializedSlotSelfCallRawInt
}

func specializedABIConstIsInt(proto *vm.FuncProto, idx int) bool {
	return idx >= 0 && idx < len(proto.Constants) && proto.Constants[idx].IsInt()
}

func specializedABIConstIsFloat(proto *vm.FuncProto, idx int) bool {
	return idx >= 0 && idx < len(proto.Constants) && proto.Constants[idx].IsFloat()
}

func specializedABIConstString(proto *vm.FuncProto, idx int) string {
	if idx < 0 || idx >= len(proto.Constants) {
		return ""
	}
	return proto.Constants[idx].Str()
}

func getSpecializedSlot(slots []specializedSlotRep, idx int) specializedSlotRep {
	if idx < 0 || idx >= len(slots) {
		return specializedSlotUnknown
	}
	return slots[idx]
}

func setSpecializedSlot(slots []specializedSlotRep, idx int, rep specializedSlotRep) {
	if idx >= 0 && idx < len(slots) {
		slots[idx] = rep
	}
}
