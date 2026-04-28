//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

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
	SpecializedABIParamRawTablePtr
)

// SpecializedABIReturnRep describes the result representation at a specialized
// return point.
type SpecializedABIReturnRep uint8

const (
	SpecializedABIReturnNone SpecializedABIReturnRep = iota
	SpecializedABIReturnBoxed
	SpecializedABIReturnRawInt
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
	specializedSlotRawTable
	specializedSlotNil
	specializedSlotSelfCallRawInt
	specializedSlotSelfCallRawTable
	specializedSlotSelfFunc
	specializedSlotOtherFunc
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
		case vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_GETUPVAL, vm.OP_NEWTABLE, vm.OP_NEWOBJECT2,
			vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_SETLIST, vm.OP_APPEND, vm.OP_NOT, vm.OP_LEN, vm.OP_CONCAT,
			vm.OP_POW, vm.OP_CLOSURE, vm.OP_FORPREP, vm.OP_FORLOOP,
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
	if proto.NumParams > maxTrackedSlots || proto.MaxStack > maxTrackedSlots {
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
				return typedSelfABIReject("non-int constant load")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_LOADNIL:
			for slot := a; slot <= a+b && slot < len(slots); slot++ {
				setSpecializedSlot(slots, slot, specializedSlotNil)
			}
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
			return typedSelfABIReject("global mutation")
		case vm.OP_NEWTABLE, vm.OP_NEWOBJECT2:
			setSpecializedSlot(slots, a, specializedSlotRawTable)
			usesTableABI = true
		case vm.OP_GETFIELD, vm.OP_GETTABLE:
			if !typedSelfSlotIsTable(getSpecializedSlot(slots, b)) {
				return typedSelfABIReject("non-table field receiver")
			}
			if typedSelfFeedbackResultIsTable(proto, pc) {
				setSpecializedSlot(slots, a, specializedSlotRawTable)
			} else {
				setSpecializedSlot(slots, a, specializedSlotUnknown)
			}
		case vm.OP_SETFIELD, vm.OP_SETTABLE:
			if len(slots) <= b || !typedSelfSlotIsTable(getSpecializedSlot(slots, b)) {
				return typedSelfABIReject("non-table field store receiver")
			}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
			if !typedSelfRKIsInt(slots, proto, b) || !typedSelfRKIsInt(slots, proto, c) {
				return typedSelfABIReject("non-int arithmetic operand")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_UNM:
			if !typedSelfSlotIsInt(getSpecializedSlot(slots, b)) {
				return typedSelfABIReject("non-int unary operand")
			}
			setSpecializedSlot(slots, a, specializedSlotRawInt)
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			if !typedSelfCompareOK(slots, proto, b, c) {
				return typedSelfABIReject("unsupported comparison operand")
			}
		case vm.OP_TEST:
		case vm.OP_TESTSET:
			setSpecializedSlot(slots, a, specializedSlotUnknown)
		case vm.OP_CALL:
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
				case SpecializedABIReturnRawTablePtr:
					setSpecializedSlot(slots, a, specializedSlotSelfCallRawTable)
				default:
					setSpecializedSlot(slots, a, specializedSlotUnknown)
				}
			case 1:
				setSpecializedSlot(slots, a, specializedSlotUnknown)
			default:
				return typedSelfABIReject("multiple call returns")
			}
		case vm.OP_RETURN:
			if b != 2 {
				return typedSelfABIReject("non-single return")
			}
			rep := typedSelfReturnRep(getSpecializedSlot(slots, a), returnRep)
			if rep == SpecializedABIReturnNone || rep == SpecializedABIReturnBoxed {
				return typedSelfABIReject("unsupported return representation")
			}
			if returnRep != SpecializedABIReturnNone && returnRep != rep {
				return typedSelfABIReject("inconsistent return representation")
			}
			if rep == SpecializedABIReturnRawTablePtr {
				usesTableABI = true
			}
			returnRep = rep
			sawReturn = true
		case vm.OP_LOADBOOL, vm.OP_GETUPVAL, vm.OP_NOT, vm.OP_LEN, vm.OP_CONCAT,
			vm.OP_DIV, vm.OP_POW, vm.OP_CLOSURE, vm.OP_FORPREP, vm.OP_FORLOOP,
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
	if !sawSelfCall {
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

	resetOrigins()
	branchTargets := specializedABIBranchTargets(proto.Code)
	for pc, inst := range proto.Code {
		if pc > 0 && branchTargets[pc] {
			resetOrigins()
		}
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		switch op {
		case vm.OP_MOVE:
			if a >= 0 && a < len(origins) {
				origins[a] = -1
				if b >= 0 && b < len(origins) {
					origins[a] = origins[b]
				}
			}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD:
			if b < vm.RKBit {
				if reason := setParam(b, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
			if c < vm.RKBit {
				if reason := setParam(c, SpecializedABIParamRawInt); reason != "" {
					return nil, reason
				}
			}
			if a >= 0 && a < len(origins) {
				origins[a] = -1
			}
		case vm.OP_UNM:
			if reason := setParam(b, SpecializedABIParamRawInt); reason != "" {
				return nil, reason
			}
			if a >= 0 && a < len(origins) {
				origins[a] = -1
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
		case vm.OP_GETFIELD, vm.OP_GETTABLE, vm.OP_SETFIELD, vm.OP_SETTABLE:
			if reason := setParam(b, SpecializedABIParamRawTablePtr); reason != "" {
				return nil, reason
			}
			if a >= 0 && a < len(origins) && (op == vm.OP_GETFIELD || op == vm.OP_GETTABLE) {
				origins[a] = -1
			}
		case vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL,
			vm.OP_GETGLOBAL, vm.OP_GETUPVAL, vm.OP_NEWTABLE, vm.OP_NEWOBJECT2,
			vm.OP_CALL, vm.OP_TESTSET, vm.OP_NOT, vm.OP_LEN, vm.OP_CONCAT,
			vm.OP_CLOSURE, vm.OP_VARARG, vm.OP_SELF, vm.OP_APPEND:
			if a >= 0 && a < len(origins) {
				origins[a] = -1
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
		case SpecializedABIParamRawTablePtr:
			slots[i] = specializedSlotRawTable
		}
	}
}

func typedSelfRKIsInt(slots []specializedSlotRep, proto *vm.FuncProto, idx int) bool {
	if idx >= vm.RKBit {
		return specializedABIConstIsInt(proto, idx-vm.RKBit)
	}
	return typedSelfSlotIsInt(getSpecializedSlot(slots, idx))
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
	return false
}

func typedSelfFeedbackResultIsTable(proto *vm.FuncProto, pc int) bool {
	return proto != nil && proto.Feedback != nil && pc >= 0 && pc < len(proto.Feedback) && proto.Feedback[pc].Result == vm.FBTable
}

func typedSelfSlotIsInt(rep specializedSlotRep) bool {
	return rep == specializedSlotRawInt || rep == specializedSlotSelfCallRawInt
}

func typedSelfSlotIsTable(rep specializedSlotRep) bool {
	return rep == specializedSlotRawTable || rep == specializedSlotSelfCallRawTable
}

func typedSelfSlotMatchesParam(rep specializedSlotRep, param SpecializedABIParamRep) bool {
	switch param {
	case SpecializedABIParamRawInt:
		return typedSelfSlotIsInt(rep)
	case SpecializedABIParamRawTablePtr:
		return typedSelfSlotIsTable(rep) || rep == specializedSlotUnknown
	default:
		return false
	}
}

func typedSelfReturnRep(slot specializedSlotRep, current SpecializedABIReturnRep) SpecializedABIReturnRep {
	switch slot {
	case specializedSlotRawInt:
		return SpecializedABIReturnRawInt
	case specializedSlotRawTable:
		return SpecializedABIReturnRawTablePtr
	case specializedSlotSelfCallRawInt:
		return SpecializedABIReturnRawInt
	case specializedSlotSelfCallRawTable:
		return SpecializedABIReturnRawTablePtr
	case specializedSlotUnknown:
		return current
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
