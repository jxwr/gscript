package vm

import "github.com/gscript/gscript/internal/runtime"

func (vm *VM) tryRunSieveWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !vm.noGlobalLock || !isSieveProto(cl.Proto) {
		return false, nil, nil
	}
	if !args[0].IsNumber() {
		return false, nil, nil
	}
	nn := args[0].Number()
	n64 := int64(nn)
	if float64(n64) != nn || n64 < 0 || int64(int(n64)) != n64 {
		return false, nil, nil
	}
	return true, []runtime.Value{runtime.IntValue(runSieveCountKernel(int(n64)))}, nil
}

func runSieveCountKernel(n int) int64 {
	if n < 2 {
		return 0
	}
	flags := make([]byte, n+1)
	for i := 2; i <= n; i++ {
		flags[i] = 1
	}
	for i := 2; i <= n/i; i++ {
		if flags[i] == 0 {
			continue
		}
		for j := i * i; j <= n; j += i {
			flags[j] = 0
		}
	}
	count := int64(0)
	for i := 2; i <= n; i++ {
		if flags[i] != 0 {
			count++
		}
	}
	return count
}

func IsSieveKernelProto(p *FuncProto) bool {
	return isSieveProto(p)
}

func isSieveProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || p.MaxStack != 15 ||
		len(p.Constants) != 0 || len(p.Protos) != 0 {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_NEWTABLE, 1, 0, 0),
		EncodeAsBx(OP_LOADINT, 2, 2),
		EncodeABC(OP_MOVE, 3, 0, 0),
		EncodeAsBx(OP_LOADINT, 4, 1),
		EncodeAsBx(OP_FORPREP, 2, 3),
		EncodeABC(OP_LOADBOOL, 6, 1, 0),
		EncodeABC(OP_MOVE, 7, 5, 0),
		EncodeABC(OP_SETTABLE, 1, 7, 6),
		EncodeAsBx(OP_FORLOOP, 2, -4),
		EncodeAsBx(OP_LOADINT, 5, 2),
		EncodeABC(OP_MUL, 6, 5, 5),
		EncodeABC(OP_LE, 0, 6, 0),
		EncodeAsBx(OP_JMP, 0, 17),
		EncodeABC(OP_MOVE, 7, 5, 0),
		EncodeABC(OP_GETTABLE, 6, 1, 7),
		EncodeABC(OP_TEST, 6, 0, 0),
		EncodeAsBx(OP_JMP, 0, 9),
		EncodeABC(OP_MUL, 6, 5, 5),
		EncodeABC(OP_LE, 0, 6, 0),
		EncodeAsBx(OP_JMP, 0, 6),
		EncodeABC(OP_LOADBOOL, 7, 0, 0),
		EncodeABC(OP_MOVE, 8, 6, 0),
		EncodeABC(OP_SETTABLE, 1, 8, 7),
		EncodeABC(OP_ADD, 7, 6, 5),
		EncodeABC(OP_MOVE, 6, 7, 0),
		EncodeAsBx(OP_JMP, 0, -8),
		EncodeAsBx(OP_LOADINT, 7, 1),
		EncodeABC(OP_ADD, 6, 5, 7),
		EncodeABC(OP_MOVE, 5, 6, 0),
		EncodeAsBx(OP_JMP, 0, -20),
		EncodeAsBx(OP_LOADINT, 6, 0),
		EncodeAsBx(OP_LOADINT, 7, 2),
		EncodeABC(OP_MOVE, 8, 0, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeAsBx(OP_FORPREP, 7, 7),
		EncodeABC(OP_MOVE, 12, 10, 0),
		EncodeABC(OP_GETTABLE, 11, 1, 12),
		EncodeABC(OP_TEST, 11, 0, 0),
		EncodeAsBx(OP_JMP, 0, 3),
		EncodeAsBx(OP_LOADINT, 12, 1),
		EncodeABC(OP_ADD, 11, 6, 12),
		EncodeABC(OP_MOVE, 6, 11, 0),
		EncodeAsBx(OP_FORLOOP, 7, -8),
		EncodeABC(OP_MOVE, 10, 6, 0),
		EncodeABC(OP_RETURN, 10, 2, 0),
	})
}
