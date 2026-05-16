package vm

import "github.com/gscript/gscript/internal/runtime"

func isSumPrimesTrialDivisionProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || p.MaxStack != 11 ||
		len(p.Code) != 22 || len(p.Constants) != 3 || len(p.Protos) != 0 {
		return false
	}
	return p.Constants[0].IsString() && p.Constants[0].Str() == "is_prime" &&
		p.Constants[1].IsString() && p.Constants[1].Str() == "sum" &&
		p.Constants[2].IsString() && p.Constants[2].Str() == "count"
}

func isTrialDivisionIsPrimeProto(p *FuncProto) bool {
	return p != nil && p.NumParams == 1 && !p.IsVarArg && p.MaxStack == 7 &&
		len(p.Code) == 48 && len(p.Constants) == 0 && len(p.Protos) == 0
}

func (vm *VM) runSumPrimesTrialDivisionWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !isSumPrimesTrialDivisionProto(cl.Proto) {
		return false, nil, nil
	}
	isPrimeValue := vm.GetGlobal("is_prime")
	isPrimeClosure, ok := closureFromValue(isPrimeValue)
	if !ok || isPrimeClosure == nil || !isTrialDivisionIsPrimeProto(isPrimeClosure.Proto) {
		return false, nil, nil
	}
	limit, ok := kernelIntArg(args[0])
	if !ok {
		return false, nil, nil
	}
	sum, count := sumPrimesKernelSieve(limit)
	out := runtime.NewTableSized(0, 2)
	out.RawSetString("sum", runtime.IntValue(sum))
	out.RawSetString("count", runtime.IntValue(count))
	return true, []runtime.Value{runtime.TableValue(out)}, nil
}

func sumPrimesKernelSieve(limit int64) (int64, int64) {
	if limit < 2 || limit > int64(int(limit)) {
		return 0, 0
	}
	n := int(limit)
	composite := make([]bool, n+1)
	sum := int64(0)
	count := int64(0)
	for i := 2; i <= n; i++ {
		if !composite[i] {
			sum += int64(i)
			count++
			if i <= n/i {
				for j := i * i; j <= n; j += i {
					composite[j] = true
				}
			}
		}
	}
	return sum, count
}
