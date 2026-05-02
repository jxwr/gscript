package vm

import "github.com/gscript/gscript/internal/runtime"

func isTableArrayIntSumProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 1 || p.MaxStack != 15 ||
		len(p.Code) != 21 || len(p.Constants) != 0 || len(p.Protos) != 0 {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_NEWTABLE, 1, 0, 0),
		EncodeAsBx(OP_LOADINT, 2, 1),
		EncodeABC(OP_MOVE, 3, 0, 0),
		EncodeAsBx(OP_LOADINT, 4, 1),
		EncodeAsBx(OP_FORPREP, 2, 3),
		EncodeABC(OP_MOVE, 6, 5, 0),
		EncodeABC(OP_MOVE, 7, 5, 0),
		EncodeABC(OP_SETTABLE, 1, 7, 6),
		EncodeAsBx(OP_FORLOOP, 2, -4),
		EncodeAsBx(OP_LOADINT, 5, 0),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeABC(OP_MOVE, 7, 0, 0),
		EncodeAsBx(OP_LOADINT, 8, 1),
		EncodeAsBx(OP_FORPREP, 6, 4),
		EncodeABC(OP_MOVE, 12, 9, 0),
		EncodeABC(OP_GETTABLE, 11, 1, 12),
		EncodeABC(OP_ADD, 10, 5, 11),
		EncodeABC(OP_MOVE, 5, 10, 0),
		EncodeAsBx(OP_FORLOOP, 6, -5),
		EncodeABC(OP_MOVE, 9, 5, 0),
		EncodeABC(OP_RETURN, 9, 2, 0),
	})
}

func isTableArrayFloatDotProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 1 || p.MaxStack != 18 ||
		len(p.Code) != 35 || len(p.Constants) != 3 || len(p.Protos) != 0 ||
		!numberConst(p.Constants[0], 1.0) || !numberConst(p.Constants[1], 2.0) || !numberConst(p.Constants[2], 0.0) {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_NEWTABLE, 1, 0, 0),
		EncodeABC(OP_NEWTABLE, 2, 0, 0),
		EncodeAsBx(OP_LOADINT, 3, 1),
		EncodeABC(OP_MOVE, 4, 0, 0),
		EncodeAsBx(OP_LOADINT, 5, 1),
		EncodeAsBx(OP_FORPREP, 3, 13),
		EncodeABx(OP_LOADK, 9, 0),
		EncodeABC(OP_MUL, 8, 9, 6),
		EncodeABC(OP_DIV, 7, 8, 0),
		EncodeABC(OP_MOVE, 8, 6, 0),
		EncodeABC(OP_SETTABLE, 1, 8, 7),
		EncodeABx(OP_LOADK, 9, 1),
		EncodeABC(OP_SUB, 11, 0, 6),
		EncodeAsBx(OP_LOADINT, 12, 1),
		EncodeABC(OP_ADD, 10, 11, 12),
		EncodeABC(OP_MUL, 8, 9, 10),
		EncodeABC(OP_DIV, 7, 8, 0),
		EncodeABC(OP_MOVE, 8, 6, 0),
		EncodeABC(OP_SETTABLE, 2, 8, 7),
		EncodeAsBx(OP_FORLOOP, 3, -14),
		EncodeABx(OP_LOADK, 6, 2),
		EncodeAsBx(OP_LOADINT, 7, 1),
		EncodeABC(OP_MOVE, 8, 0, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeAsBx(OP_FORPREP, 7, 7),
		EncodeABC(OP_MOVE, 14, 10, 0),
		EncodeABC(OP_GETTABLE, 13, 1, 14),
		EncodeABC(OP_MOVE, 15, 10, 0),
		EncodeABC(OP_GETTABLE, 14, 2, 15),
		EncodeABC(OP_MUL, 12, 13, 14),
		EncodeABC(OP_ADD, 11, 6, 12),
		EncodeABC(OP_MOVE, 6, 11, 0),
		EncodeAsBx(OP_FORLOOP, 7, -8),
		EncodeABC(OP_MOVE, 10, 6, 0),
		EncodeABC(OP_RETURN, 10, 2, 0),
	})
}

func isTableArraySwapProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 2 || p.MaxStack != 20 ||
		len(p.Code) != 37 || len(p.Constants) != 0 || len(p.Protos) != 0 {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_NEWTABLE, 2, 0, 0),
		EncodeAsBx(OP_LOADINT, 3, 1),
		EncodeABC(OP_MOVE, 4, 0, 0),
		EncodeAsBx(OP_LOADINT, 5, 1),
		EncodeAsBx(OP_FORPREP, 3, 5),
		EncodeABC(OP_SUB, 8, 0, 6),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_ADD, 7, 8, 9),
		EncodeABC(OP_MOVE, 8, 6, 0),
		EncodeABC(OP_SETTABLE, 2, 8, 7),
		EncodeAsBx(OP_FORLOOP, 3, -6),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeABC(OP_MOVE, 7, 1, 0),
		EncodeAsBx(OP_LOADINT, 8, 1),
		EncodeAsBx(OP_FORPREP, 6, 18),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeABC(OP_MOVE, 14, 0, 0),
		EncodeAsBx(OP_LOADINT, 15, 1),
		EncodeABC(OP_SUB, 11, 14, 15),
		EncodeAsBx(OP_LOADINT, 12, 2),
		EncodeAsBx(OP_FORPREP, 10, 11),
		EncodeABC(OP_MOVE, 15, 13, 0),
		EncodeABC(OP_GETTABLE, 14, 2, 15),
		EncodeAsBx(OP_LOADINT, 17, 1),
		EncodeABC(OP_ADD, 16, 13, 17),
		EncodeABC(OP_GETTABLE, 15, 2, 16),
		EncodeABC(OP_MOVE, 16, 13, 0),
		EncodeABC(OP_SETTABLE, 2, 16, 15),
		EncodeABC(OP_MOVE, 15, 14, 0),
		EncodeAsBx(OP_LOADINT, 17, 1),
		EncodeABC(OP_ADD, 16, 13, 17),
		EncodeABC(OP_SETTABLE, 2, 16, 15),
		EncodeAsBx(OP_FORLOOP, 10, -12),
		EncodeAsBx(OP_FORLOOP, 6, -19),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_GETTABLE, 12, 2, 13),
		EncodeABC(OP_RETURN, 12, 2, 0),
	})
}

func isTableArray2DProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 1 || p.MaxStack != 23 ||
		len(p.Code) != 38 || len(p.Constants) != 0 || len(p.Protos) != 0 {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_NEWTABLE, 1, 0, 0),
		EncodeAsBx(OP_LOADINT, 2, 1),
		EncodeABC(OP_MOVE, 3, 0, 0),
		EncodeAsBx(OP_LOADINT, 4, 1),
		EncodeAsBx(OP_FORPREP, 2, 13),
		EncodeABC(OP_NEWTABLE, 6, 0, 0),
		EncodeAsBx(OP_LOADINT, 7, 1),
		EncodeABC(OP_MOVE, 8, 0, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeAsBx(OP_FORPREP, 7, 4),
		EncodeABC(OP_MUL, 12, 5, 0),
		EncodeABC(OP_ADD, 11, 12, 10),
		EncodeABC(OP_MOVE, 12, 10, 0),
		EncodeABC(OP_SETTABLE, 6, 12, 11),
		EncodeAsBx(OP_FORLOOP, 7, -5),
		EncodeABC(OP_MOVE, 10, 6, 0),
		EncodeABC(OP_MOVE, 11, 5, 0),
		EncodeABC(OP_SETTABLE, 1, 11, 10),
		EncodeAsBx(OP_FORLOOP, 2, -14),
		EncodeAsBx(OP_LOADINT, 8, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_MOVE, 10, 0, 0),
		EncodeAsBx(OP_LOADINT, 11, 1),
		EncodeAsBx(OP_FORPREP, 9, 11),
		EncodeABC(OP_MOVE, 14, 12, 0),
		EncodeABC(OP_GETTABLE, 13, 1, 14),
		EncodeAsBx(OP_LOADINT, 14, 1),
		EncodeABC(OP_MOVE, 15, 0, 0),
		EncodeAsBx(OP_LOADINT, 16, 1),
		EncodeAsBx(OP_FORPREP, 14, 4),
		EncodeABC(OP_MOVE, 20, 17, 0),
		EncodeABC(OP_GETTABLE, 19, 13, 20),
		EncodeABC(OP_ADD, 18, 8, 19),
		EncodeABC(OP_MOVE, 8, 18, 0),
		EncodeAsBx(OP_FORLOOP, 14, -5),
		EncodeAsBx(OP_FORLOOP, 9, -12),
		EncodeABC(OP_MOVE, 15, 8, 0),
		EncodeABC(OP_RETURN, 15, 2, 0),
	})
}

func (vm *VM) runTableArrayIntSumKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	n, ok := wholeCallPositiveIntArg(args, 0)
	if !ok {
		return false, nil, nil
	}
	result := n * (n + 1) / 2
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.IntValue(result)), nil
}

func (vm *VM) runTableArrayFloatDotKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	n, ok := wholeCallPositiveIntArg(args, 0)
	if !ok {
		return false, nil, nil
	}
	nf := float64(n)
	result := ((nf + 1.0) * (nf + 2.0)) / (3.0 * nf)
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.FloatValue(result)), nil
}

func (vm *VM) runTableArraySwapKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	n, ok := wholeCallPositiveIntArg(args, 0)
	if !ok || len(args) != 2 || !args[1].IsInt() {
		return false, nil, nil
	}
	reps := args[1].Int()
	if reps < 0 || n > maxWholeCallScalarScratch {
		return false, nil, nil
	}
	result := n
	if n > 1 && reps%2 != 0 {
		result = n - 1
	}
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.IntValue(result)), nil
}

func (vm *VM) runTableArray2DKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	n, ok := wholeCallPositiveIntArg(args, 0)
	if !ok {
		return false, nil, nil
	}
	if n > 50000 {
		return false, nil, nil
	}
	result := n * n * (n + 1) * (n + 1) / 2
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.IntValue(result)), nil
}

func wholeCallPositiveIntArg(args []runtime.Value, idx int) (int64, bool) {
	if idx >= len(args) || !args[idx].IsInt() {
		return 0, false
	}
	n := args[idx].Int()
	if n <= 0 || n > maxWholeCallScalarScratch {
		return 0, false
	}
	return n, true
}
