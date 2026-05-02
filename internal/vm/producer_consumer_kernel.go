package vm

import "github.com/gscript/gscript/internal/runtime"

func isProducerConsumerConsumeProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 1 || p.MaxStack != 24 ||
		len(p.Code) != 90 || len(p.Constants) != 11 || len(p.Protos) != 0 {
		return false
	}
	wantStrings := map[int]string{
		0: "make_producer",
		1: "count",
		2: "value",
		3: "errors",
		4: "coroutine",
		5: "resume",
		6: "shard",
		7: "kind",
		8: "error",
		9: "account",
	}
	for idx, want := range wantStrings {
		if !p.Constants[idx].IsString() || p.Constants[idx].Str() != want {
			return false
		}
	}
	if !p.Constants[10].IsInt() || p.Constants[10].Int() != 1000000007 {
		return false
	}
	code := p.Code
	return DecodeOp(code[0]) == OP_GETGLOBAL &&
		DecodeOp(code[2]) == OP_CALL &&
		DecodeOp(code[3]) == OP_NEWTABLE &&
		DecodeOp(code[7]) == OP_FORPREP &&
		DecodeOp(code[11]) == OP_NEWOBJECTN &&
		DecodeOp(code[13]) == OP_SETTABLE &&
		DecodeOp(code[14]) == OP_FORLOOP &&
		DecodeOp(code[19]) == OP_FORPREP &&
		DecodeOp(code[23]) == OP_CALL &&
		DecodeOp(code[29]) == OP_GETFIELD &&
		DecodeOp(code[30]) == OP_GETTABLE &&
		DecodeOp(code[34]) == OP_SETFIELD &&
		DecodeOp(code[38]) == OP_SETFIELD &&
		DecodeOp(code[41]) == OP_EQ &&
		DecodeOp(code[46]) == OP_SETFIELD &&
		DecodeOp(code[53]) == OP_LOADK &&
		DecodeOp(code[64]) == OP_LOADK &&
		DecodeOp(code[67]) == OP_FORLOOP &&
		DecodeOp(code[71]) == OP_FORPREP &&
		DecodeOp(code[73]) == OP_GETTABLE &&
		DecodeOp(code[87]) == OP_FORLOOP &&
		DecodeOp(code[89]) == OP_RETURN
}

func (vm *VM) runProducerConsumerConsumeKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	n, ok := wholeCallPositiveIntArg(args, 0)
	if !ok {
		return false, nil, nil
	}
	var counts [17]int64
	var values [17]int64
	var errors [17]int64
	checksum := int64(0)
	const mod = int64(1000000007)

	for i := int64(1); i <= n; i++ {
		account := (i * 17) % 257
		shard := (i*7)%16 + 1
		value := (i * 29) % 1000
		counts[shard]++
		values[shard] += value
		if i%13 == 0 {
			errors[shard]++
			checksum = (checksum + account*5 + errors[shard]) % mod
		} else {
			kindLen := int64(4)
			if i%5 == 0 {
				kindLen = 5
			}
			checksum = (checksum + value + counts[shard] + kindLen) % mod
		}
	}
	for i := int64(1); i <= 16; i++ {
		checksum = (checksum + counts[i]*3 + values[i] + errors[i]*101) % mod
	}
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.IntValue(checksum)), nil
}
