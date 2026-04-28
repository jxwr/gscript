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
)

// SpecializedABIParamRep describes how one fixed parameter is represented at
// a specialized entry point.
type SpecializedABIParamRep uint8

const (
	SpecializedABIParamBoxed SpecializedABIParamRep = iota
	SpecializedABIParamRawInt
)

// SpecializedABIReturnRep describes the result representation at a specialized
// return point.
type SpecializedABIReturnRep uint8

const (
	SpecializedABIReturnNone SpecializedABIReturnRep = iota
	SpecializedABIReturnBoxed
	SpecializedABIReturnRawInt
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
// self-recursive entry. It is derived from SpecializedABI but keeps only the
// facts emission and compiled-function metadata need.
type RawIntSelfABI struct {
	Eligible   bool
	NumParams  int
	ParamSlots []int
	Return     SpecializedABIReturnRep
	RejectWhy  string
}

type specializedSlotRep uint8

const (
	specializedSlotUnknown specializedSlotRep = iota
	specializedSlotRawInt
	specializedSlotSelfCallRawInt
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
		case vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_GETUPVAL, vm.OP_NEWTABLE,
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
