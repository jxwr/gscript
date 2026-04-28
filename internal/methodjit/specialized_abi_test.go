//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestAnalyzeSpecializedABI_RawIntEligible(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		protoName string
		numParams int
	}{
		{
			name:      "ack",
			protoName: "ack",
			numParams: 2,
			src: `func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}`,
		},
		{
			name:      "fib",
			protoName: "fib",
			numParams: 1,
			src: `func fib(n) {
	if n < 2 { return n }
	return fib(n - 1) + fib(n - 2)
}`,
		},
		{
			name:      "gcd",
			protoName: "gcd",
			numParams: 2,
			src: `func gcd(a, b) {
	if b == 0 { return a }
	return gcd(b, a % b)
}`,
		},
		{
			name:      "factorial",
			protoName: "fact",
			numParams: 2,
			src: `func fact(n, acc) {
	if n <= 1 { return acc }
	return fact(n - 1, acc * n)
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := findProtoByName(compileTop(t, tt.src), tt.protoName)
			if proto == nil {
				t.Fatalf("function %q not found", tt.protoName)
			}

			abi := AnalyzeSpecializedABI(proto)
			if !abi.Eligible {
				t.Fatalf("expected eligible raw-int ABI, rejected: %s", abi.RejectWhy)
			}
			if abi.Kind != SpecializedABIRawInt {
				t.Fatalf("Kind=%d want %d", abi.Kind, SpecializedABIRawInt)
			}
			if abi.Return != SpecializedABIReturnRawInt {
				t.Fatalf("Return=%d want %d", abi.Return, SpecializedABIReturnRawInt)
			}
			if len(abi.Params) != tt.numParams {
				t.Fatalf("len(Params)=%d want %d", len(abi.Params), tt.numParams)
			}
			for i, rep := range abi.Params {
				if rep != SpecializedABIParamRawInt {
					t.Fatalf("Params[%d]=%d want %d", i, rep, SpecializedABIParamRawInt)
				}
			}
		})
	}
}

func TestAnalyzeSpecializedABI_NonEligible(t *testing.T) {
	tests := []struct {
		name      string
		protoName string
		src       string
	}{
		{
			name:      "float return",
			protoName: "f",
			src:       `func f(x) { return x + 1.5 }`,
		},
		{
			name:      "table param use",
			protoName: "first",
			src:       `func first(t) { return t[0] }`,
		},
		{
			name:      "multi return",
			protoName: "pair",
			src:       `func pair(a, b) { return a, b }`,
		},
		{
			name:      "vararg",
			protoName: "sum",
			src:       `func sum(...) { return 0 }`,
		},
		{
			name:      "non-self call",
			protoName: "f",
			src: `func g(x) { return x }
func f(x) { return g(x) }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := findProtoByName(compileTop(t, tt.src), tt.protoName)
			if proto == nil {
				t.Fatalf("function %q not found", tt.protoName)
			}

			abi := AnalyzeSpecializedABI(proto)
			if abi.Eligible {
				t.Fatalf("expected ineligible, got %+v", abi)
			}
			if abi.Kind != SpecializedABINone {
				t.Fatalf("Kind=%d want %d", abi.Kind, SpecializedABINone)
			}
		})
	}
}

func TestQualifiesForNumericCrossRecursiveCandidate(t *testing.T) {
	top := compileTop(t, `func F(n) {
	if n == 0 { return 1 }
	return n - M(F(n - 1))
}
func M(n) {
	if n == 0 { return 0 }
	return n - F(M(n - 1))
}`)
	for _, name := range []string{"F", "M"} {
		proto := findProtoByName(top, name)
		if proto == nil {
			t.Fatalf("function %q not found", name)
		}
		if !qualifiesForNumericCrossRecursiveCandidate(proto) {
			t.Fatalf("%s should qualify as numeric cross-recursive candidate", name)
		}
		if abi := AnalyzeSpecializedABI(proto); abi.Eligible {
			t.Fatalf("%s must not be accepted by self-only raw ABI analysis: %+v", name, abi)
		}
		raw := AnalyzeRawIntSelfABI(proto)
		if !raw.Eligible || raw.NumParams != 1 || raw.Return != SpecializedABIReturnRawInt {
			t.Fatalf("%s should publish a raw numeric recursive ABI, got %+v", name, raw)
		}
	}

	wrapper := findProtoByName(compileTop(t, `func g(n) { return n + 1 }
func f(n) { return g(n) }`), "f")
	if wrapper == nil {
		t.Fatal("function \"f\" not found")
	}
	if qualifiesForNumericCrossRecursiveCandidate(wrapper) {
		t.Fatal("non-recursive wrapper should not qualify")
	}
}

func TestAnalyzeSpecializedABI_NilAndManualProto(t *testing.T) {
	if abi := AnalyzeSpecializedABI(nil); abi.Eligible {
		t.Fatalf("nil proto should be ineligible: %+v", abi)
	}

	proto := &vm.FuncProto{
		Name:      "add1",
		NumParams: 1,
		MaxStack:  3,
		Code: []uint32{
			vm.EncodeABC(vm.OP_ADD, 1, 0, vm.ConstToRK(0)),
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),
		},
		Constants: []runtime.Value{runtime.IntValue(1)},
	}
	if abi := AnalyzeSpecializedABI(proto); !abi.Eligible {
		t.Fatalf("manual int proto should be eligible, rejected: %s", abi.RejectWhy)
	}
}

func TestAnalyzeTypedSelfABI_TableShapes(t *testing.T) {
	top := compileTop(t, `func makeTree(depth) {
	if depth == 0 {
		return {left: nil, right: nil}
	}
	return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}
func checkTree(node) {
	if node.left == nil { return 1 }
	return 1 + checkTree(node.left) + checkTree(node.right)
}`)
	makeTree := findProtoByName(top, "makeTree")
	checkTree := findProtoByName(top, "checkTree")
	if makeTree == nil || checkTree == nil {
		t.Fatalf("missing protos: makeTree=%v checkTree=%v", makeTree != nil, checkTree != nil)
	}
	// checkTree needs the normal feedback fact that the recursive left/right
	// fields are tables. The entry leaf-test field remains any/nil.
	checkTree.EnsureFeedback()
	checkTree.Feedback[8].Result = vm.FBTable
	checkTree.Feedback[12].Result = vm.FBTable

	makeABI := AnalyzeTypedSelfABI(makeTree)
	if !makeABI.Eligible {
		t.Fatalf("makeTree typed ABI rejected: %s", makeABI.RejectWhy)
	}
	if makeABI.NumParams != 1 || len(makeABI.Params) != 1 || makeABI.Params[0] != SpecializedABIParamRawInt {
		t.Fatalf("makeTree params=%+v", makeABI)
	}
	if makeABI.Return != SpecializedABIReturnRawTablePtr {
		t.Fatalf("makeTree return=%d want raw table", makeABI.Return)
	}

	checkABI := AnalyzeTypedSelfABI(checkTree)
	if !checkABI.Eligible {
		t.Fatalf("checkTree typed ABI rejected: %s", checkABI.RejectWhy)
	}
	if checkABI.NumParams != 1 || len(checkABI.Params) != 1 || checkABI.Params[0] != SpecializedABIParamRawTablePtr {
		t.Fatalf("checkTree params=%+v", checkABI)
	}
	if checkABI.Return != SpecializedABIReturnRawInt {
		t.Fatalf("checkTree return=%d want raw int", checkABI.Return)
	}
}

func TestAnalyzeSpecializedABI_RejectsUnsupportedABIShape(t *testing.T) {
	base := func() *vm.FuncProto {
		return &vm.FuncProto{
			Name:      "f",
			NumParams: 1,
			MaxStack:  2,
			Code: []uint32{
				vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(*vm.FuncProto)
	}{
		{
			name: "too many raw params",
			mutate: func(proto *vm.FuncProto) {
				proto.NumParams = 5
				proto.MaxStack = 6
			},
		},
		{
			name: "upvalues",
			mutate: func(proto *vm.FuncProto) {
				proto.Upvalues = []vm.UpvalDesc{{Name: "x"}}
			},
		},
		{
			name: "nested protos",
			mutate: func(proto *vm.FuncProto) {
				proto.Protos = []*vm.FuncProto{{Name: "inner"}}
			},
		},
		{
			name: "dynamic return count",
			mutate: func(proto *vm.FuncProto) {
				proto.Code = []uint32{vm.EncodeABC(vm.OP_RETURN, 0, 0, 0)}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := base()
			tt.mutate(proto)
			if abi := AnalyzeSpecializedABI(proto); abi.Eligible {
				t.Fatalf("expected ineligible, got %+v", abi)
			}
		})
	}
}

func TestAnalyzeSpecializedABI_RawIntNumericSelfRecursiveShapes(t *testing.T) {
	tests := []struct {
		name           string
		src            string
		protoName      string
		wantParams     int
		minSelfCalls   int
		minArithOps    int
		minCompareOps  int
		wantDynamicRet bool
	}{
		{
			name:       "nested one-param recursion",
			protoName:  "r",
			wantParams: 1,
			src: `func r(n) {
	if n < 2 { return n }
	return r(n - 1) + r(n - 2)
}`,
			minSelfCalls:  2,
			minArithOps:   3,
			minCompareOps: 1,
		},
		{
			name:       "tail recursion with accumulator",
			protoName:  "fold",
			wantParams: 2,
			src: `func fold(n, acc) {
	if n <= 1 { return acc }
	return fold(n - 1, acc * n)
}`,
			minSelfCalls:   1,
			minArithOps:    2,
			minCompareOps:  1,
			wantDynamicRet: true,
		},
		{
			name:       "two-param nested dynamic return",
			protoName:  "nest",
			wantParams: 2,
			src: `func nest(a, b) {
	if a == 0 { return b + 1 }
	if b == 0 { return nest(a - 1, 1) }
	return nest(a - 1, nest(a, b - 1))
}`,
			minSelfCalls:   3,
			minArithOps:    4,
			minCompareOps:  2,
			wantDynamicRet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := findProtoByName(compileTop(t, tt.src), tt.protoName)
			if proto == nil {
				t.Fatalf("function %q not found", tt.protoName)
			}

			abi := AnalyzeSpecializedABI(proto)
			assertRawIntSpecializedABI(t, abi, tt.wantParams)

			selfCalls, arithOps, compareOps, dynamicReturns := countRawIntSelfBodyOps(t, proto)
			if selfCalls < tt.minSelfCalls {
				t.Fatalf("self call count=%d want >=%d", selfCalls, tt.minSelfCalls)
			}
			if arithOps < tt.minArithOps {
				t.Fatalf("arithmetic op count=%d want >=%d", arithOps, tt.minArithOps)
			}
			if compareOps < tt.minCompareOps {
				t.Fatalf("compare op count=%d want >=%d", compareOps, tt.minCompareOps)
			}
			if got := dynamicReturns > 0; got != tt.wantDynamicRet {
				t.Fatalf("dynamic return present=%v want %v", got, tt.wantDynamicRet)
			}
		})
	}
}

func TestAnalyzeRawIntSelfABI_Metadata(t *testing.T) {
	top := compileTop(t, `func mix(a, b, c) {
	if a == 0 { return b + c }
	return mix(a - 1, b + 1, c + 2)
}`)
	proto := findProtoByName(top, "mix")
	if proto == nil {
		t.Fatal("function \"mix\" not found")
	}

	abi := AnalyzeRawIntSelfABI(proto)
	if !abi.Eligible {
		t.Fatalf("expected raw-int self ABI metadata, rejected: %s", abi.RejectWhy)
	}
	if abi.NumParams != 3 {
		t.Fatalf("NumParams=%d, want 3", abi.NumParams)
	}
	if abi.Return != SpecializedABIReturnRawInt {
		t.Fatalf("Return=%d, want raw int", abi.Return)
	}
	for i, slot := range abi.ParamSlots {
		if slot != i {
			t.Fatalf("ParamSlots[%d]=%d, want %d", i, slot, i)
		}
	}

	nonSelf := findProtoByName(compileTop(t, `func g(n) { return n + 1 }
func f(n) { return g(n) }`), "f")
	if nonSelf == nil {
		t.Fatal("function \"f\" not found")
	}
	if got := AnalyzeRawIntSelfABI(nonSelf); got.Eligible {
		t.Fatalf("non-self call should not get raw-int self ABI metadata: %+v", got)
	}
}

func BenchmarkSpecializedABIRawIntEligibilitySmoke(b *testing.B) {
	cases := []struct {
		name      string
		protoName string
		src       string
	}{
		{
			name:      "gcd",
			protoName: "gcd",
			src: `func gcd(a, b) {
	if b == 0 { return a }
	return gcd(b, a % b)
}`,
		},
		{
			name:      "fact",
			protoName: "fact",
			src: `func fact(n, acc) {
	if n <= 1 { return acc }
	return fact(n - 1, acc * n)
}`,
		},
		{
			name:      "fib",
			protoName: "fib",
			src: `func fib(n) {
	if n < 2 { return n }
	return fib(n - 1) + fib(n - 2)
}`,
		},
		{
			name:      "nested2",
			protoName: "nested2",
			src: `func nested2(a, b) {
	if a == 0 { return b + 1 }
	if b == 0 { return nested2(a - 1, 1) }
	return nested2(a - 1, nested2(a, b - 1))
}`,
		},
	}

	protos := make([]*vm.FuncProto, 0, len(cases))
	for _, tc := range cases {
		proto := findProtoByName(compileTopB(b, tc.src), tc.protoName)
		if proto == nil {
			b.Fatalf("%s: function %q not found", tc.name, tc.protoName)
		}
		protos = append(protos, proto)
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j, proto := range protos {
			abi := AnalyzeSpecializedABI(proto)
			if !abi.Eligible {
				b.Fatalf("%s: rejected raw-int ABI: %s", cases[j].name, abi.RejectWhy)
			}
		}
	}
}

func assertRawIntSpecializedABI(t *testing.T, abi SpecializedABI, wantParams int) {
	t.Helper()
	if !abi.Eligible {
		t.Fatalf("expected eligible raw-int ABI, rejected: %s", abi.RejectWhy)
	}
	if abi.Kind != SpecializedABIRawInt {
		t.Fatalf("Kind=%d want %d", abi.Kind, SpecializedABIRawInt)
	}
	if abi.Return != SpecializedABIReturnRawInt {
		t.Fatalf("Return=%d want %d", abi.Return, SpecializedABIReturnRawInt)
	}
	if len(abi.Params) != wantParams {
		t.Fatalf("len(Params)=%d want %d", len(abi.Params), wantParams)
	}
	for i, rep := range abi.Params {
		if rep != SpecializedABIParamRawInt {
			t.Fatalf("Params[%d]=%d want %d", i, rep, SpecializedABIParamRawInt)
		}
	}
}

func countRawIntSelfBodyOps(t *testing.T, proto *vm.FuncProto) (selfCalls, arithOps, compareOps, dynamicReturns int) {
	t.Helper()
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD, vm.OP_UNM:
			arithOps++
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			compareOps++
		case vm.OP_CALL:
			if callTargetsSelf(proto, inst) {
				selfCalls++
			}
		case vm.OP_RETURN:
			if vm.DecodeB(inst) == 0 {
				dynamicReturns++
			}
		}
	}
	return selfCalls, arithOps, compareOps, dynamicReturns
}

func callTargetsSelf(proto *vm.FuncProto, callInst uint32) bool {
	a := vm.DecodeA(callInst)
	for _, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL || vm.DecodeA(inst) != a {
			continue
		}
		if specializedABIConstString(proto, vm.DecodeBx(inst)) == proto.Name {
			return true
		}
	}
	return false
}
