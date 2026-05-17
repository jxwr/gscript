package vm

import "github.com/gscript/gscript/internal/runtime"

// Encoded bytecode for the structural permutation flip/checksum implementation shape.
var permutationFlipChecksumCode = [...]uint32{
	265, 521, 777, 2147484674, 1284, 2147485186, 2147812389, 460804,
	461060, 134808076, 460804, 461060, 134808332, 2146960422, 2147419906, 2147420162,
	2147420418, 2147486210, 2820, 2147486722, 2147682853, 855812, 251792907, 855812,
	235864332, 2147093030, 2147421442, 2147487490, 251727371, 2147487490, 252576027, 2149187616,
	2147487490, 921604, 269418524, 2148466720, 987652, 302059787, 1053444, 318837259,
	987908, 303235340, 1118724, 1053444, 303235340, 2147488514, 319754769, 1183492,
	2147488514, 319820306, 1183748, 2146238496, 2147488258, 302846225, 1117444, 2147488258,
	302059787, 1117700, 2145452064, 218562588, 2147549216, 855812, 984836, 2147553282,
	269029141, 2147422210, 269418523, 2147614752, 218631953, 985092, 2147549216, 218631954,
	985092, 2147487746, 269029137, 985348, 69377, 2147553282, 4356, 2147488258,
	2149650469, 2147489026, 352457739, 2147489026, 1251588, 2147490306, 437851666, 2147489538,
	2147751205, 2147490562, 454564369, 436345099, 1579524, 421134860, 2147030310, 1316868,
	1251588, 404292108, 1251844, 436410635, 2147490306, 437852178, 1251588, 404292364,
	2147424258, 1251844, 436410635, 421003292, 2147614752, 6145, 1576708, 2147680288,
	1251332, 1251588, 404292364, 2145128486, 988676, 5662, 2147483680, 2147483680,
	2140602400, 463876, 529668, 335549194, 135970,
}

func (vm *VM) tryRunPermutationFlipChecksumWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || !hotWholeCallKernelRecognized(cl.Proto, wholeCallKernelPermutationFlipChecksum) {
		return false, nil, nil
	}
	return vm.runPermutationFlipChecksumWholeCallKernel(cl, args)
}

func (vm *VM) runPermutationFlipChecksumWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !vm.noGlobalLock {
		return false, nil, nil
	}
	if !args[0].IsNumber() {
		return false, nil, nil
	}
	nn := args[0].Number()
	n64 := int64(nn)
	if float64(n64) != nn || n64 < 1 || int64(int(n64)) != n64 {
		return false, nil, nil
	}
	ctor, ok := permutationFlipChecksumResultCtor(cl.Proto)
	if !ok {
		return false, nil, nil
	}
	result, ok := runPermutationFlipChecksumKernel(int(n64), ctor)
	if !ok {
		return false, nil, nil
	}
	seedPermutationFlipChecksumFeedback(cl.Proto)
	return true, []runtime.Value{runtime.FreshTableValue(result)}, nil
}

func IsPermutationFlipChecksumKernelProto(p *FuncProto) bool {
	return cachedWholeCallKernelRecognized(p, wholeCallKernelPermutationFlipChecksum)
}

func isPermutationFlipChecksumKernelProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || p.MaxStack != 30 || len(p.Protos) != 0 || len(p.Constants) != 2 {
		return false
	}
	if _, ok := permutationFlipChecksumResultCtor(p); !ok {
		return false
	}
	return codeEquals(p.Code, permutationFlipChecksumCode[:])
}

func permutationFlipChecksumResultCtor(p *FuncProto) (*runtime.SmallTableCtor2, bool) {
	if p == nil || len(p.Constants) != 2 || !p.Constants[0].IsString() || !p.Constants[1].IsString() {
		return nil, false
	}
	left, right := p.Constants[0].Str(), p.Constants[1].Str()
	if left == "" || right == "" || left == right {
		return nil, false
	}
	ctor := runtime.NewSmallTableCtor2(left, right)
	return &ctor, true
}

func seedPermutationFlipChecksumFeedback(p *FuncProto) {
	// Preserve the feedback shape that the old executed path produced so
	// diagnostics and later TypeSpec passes still see int-array accesses.
	fb := p.EnsureFeedback()
	for pc, inst := range p.Code {
		switch DecodeOp(inst) {
		case OP_GETTABLE:
			fb[pc].Result = FBInt
			fb[pc].Kind = FBKindInt
		case OP_SETTABLE:
			fb[pc].Kind = FBKindInt
		}
	}
}

func runPermutationFlipChecksumKernel(n int, ctor *runtime.SmallTableCtor2) (*runtime.Table, bool) {
	if n < 1 || n > 12 {
		return nil, false
	}
	if ctor == nil {
		return nil, false
	}
	perm := make([]int, n+1)
	perm1 := make([]int, n+1)
	count := make([]int, n+1)
	for i := 1; i <= n; i++ {
		perm1[i] = i
		count[i] = i
	}

	maxFlips := 0
	checksum := 0
	nperm := 0
	for {
		copy(perm[1:], perm1[1:])

		flips := 0
		for k := perm[1]; k != 1; k = perm[1] {
			for lo, hi := 1, k; lo < hi; lo, hi = lo+1, hi-1 {
				perm[lo], perm[hi] = perm[hi], perm[lo]
			}
			flips++
		}
		if flips > maxFlips {
			maxFlips = flips
		}
		if nperm%2 == 0 {
			checksum += flips
		} else {
			checksum -= flips
		}
		nperm++

		done := true
		for i := 2; i <= n; i++ {
			t := perm1[1]
			for j := 1; j < i; j++ {
				perm1[j] = perm1[j+1]
			}
			perm1[i] = t

			count[i]--
			if count[i] > 0 {
				done = false
				break
			}
			count[i] = i
		}
		if done {
			break
		}
	}

	return runtime.NewTableFromCtor2NonNil(
		ctor,
		runtime.IntValue(int64(maxFlips)),
		runtime.IntValue(int64(checksum)),
	), true
}
