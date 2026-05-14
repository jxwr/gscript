package methodjit

import "github.com/gscript/gscript/internal/vm"

type closureRecurrenceDeltaKind uint8

const (
	closureRecurrenceDeltaConstInt closureRecurrenceDeltaKind = iota
	closureRecurrenceDeltaUpvalue
)

type closureRecurrenceFact struct {
	Proto      *vm.FuncProto
	ValueUpval int
	DeltaKind  closureRecurrenceDeltaKind
	DeltaInt   int64
	DeltaUpval int
	ReturnReg  int
}

func analyzeClosureRecurrence(proto *vm.FuncProto) (closureRecurrenceFact, bool) {
	if proto == nil || proto.NumParams != 0 || proto.IsVarArg || len(proto.Code) != 6 {
		return closureRecurrenceFact{}, false
	}
	code := proto.Code
	if vm.DecodeOp(code[0]) != vm.OP_GETUPVAL ||
		vm.DecodeOp(code[2]) != vm.OP_ADD ||
		vm.DecodeOp(code[3]) != vm.OP_SETUPVAL ||
		vm.DecodeOp(code[4]) != vm.OP_GETUPVAL ||
		vm.DecodeOp(code[5]) != vm.OP_RETURN {
		return closureRecurrenceFact{}, false
	}
	valueLoadReg := vm.DecodeA(code[0])
	valueUpval := vm.DecodeB(code[0])
	if valueUpval < 0 || valueUpval >= len(proto.Upvalues) {
		return closureRecurrenceFact{}, false
	}
	addDst := vm.DecodeA(code[2])
	addB := vm.DecodeB(code[2])
	addC := vm.DecodeC(code[2])
	if addDst != vm.DecodeA(code[3]) || vm.DecodeB(code[3]) != valueUpval ||
		vm.DecodeB(code[4]) != valueUpval {
		return closureRecurrenceFact{}, false
	}
	retReg := vm.DecodeA(code[4])
	if vm.DecodeA(code[5]) != retReg || vm.DecodeB(code[5]) != 2 {
		return closureRecurrenceFact{}, false
	}
	fact := closureRecurrenceFact{
		Proto:      proto,
		ValueUpval: valueUpval,
		ReturnReg:  retReg,
	}
	switch vm.DecodeOp(code[1]) {
	case vm.OP_LOADINT:
		deltaReg := vm.DecodeA(code[1])
		if !commutativeAddOperands(addB, addC, valueLoadReg, deltaReg) {
			return closureRecurrenceFact{}, false
		}
		fact.DeltaKind = closureRecurrenceDeltaConstInt
		fact.DeltaInt = int64(vm.DecodesBx(code[1]))
		return fact, true
	case vm.OP_GETUPVAL:
		deltaReg := vm.DecodeA(code[1])
		deltaUpval := vm.DecodeB(code[1])
		if deltaUpval < 0 || deltaUpval >= len(proto.Upvalues) || deltaUpval == valueUpval {
			return closureRecurrenceFact{}, false
		}
		if !commutativeAddOperands(addB, addC, valueLoadReg, deltaReg) {
			return closureRecurrenceFact{}, false
		}
		fact.DeltaKind = closureRecurrenceDeltaUpvalue
		fact.DeltaUpval = deltaUpval
		return fact, true
	default:
		return closureRecurrenceFact{}, false
	}
}

func commutativeAddOperands(a, b, wantA, wantB int) bool {
	return (a == wantA && b == wantB) || (a == wantB && b == wantA)
}
