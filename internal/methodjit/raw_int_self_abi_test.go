//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Raw-int self-recursive ABI design matrix:
//   - eligible self recursion returns one raw int through the specialized path;
//   - nested self calls, especially ack(m-1, ack(m, n-1)), preserve the inner
//     result through the outer call argument protocol;
//   - exits from a raw-int callee fall back through the boxed resume path and
//     restore the caller's live values;
//   - deep recursive pressure trips the depth/fallback policy instead of
//     corrupting native frames;
//   - non-eligible protos stay on the boxed Tier 2 ABI.
//
// These are the red/green contract for the raw ABI entry guards, liveness,
// exit-resume, fallback materialization, and return convention.

func TestRawIntSelfABI_EligibleExecutionMatrix(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		fnName      string
		args        []runtime.Value
		wantParams  int
		compile     []string
		wantEntered []string
	}{
		{
			name:       "tail accumulator return",
			fnName:     "fact",
			wantParams: 2,
			src: `func fact(n, acc) {
	if n <= 1 { return acc }
	return fact(n - 1, acc * n)
}`,
			args:        []runtime.Value{runtime.IntValue(7), runtime.IntValue(1)},
			compile:     []string{"fact"},
			wantEntered: []string{"fact"},
		},
		{
			name:       "nested ack self calls",
			fnName:     "ack",
			wantParams: 2,
			src: `func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}`,
			args:        []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)},
			compile:     []string{"ack"},
			wantEntered: []string{"ack"},
		},
		{
			name:       "non-tail raw return plus live locals",
			fnName:     "fib",
			wantParams: 1,
			src: `func fib(n) {
	if n < 2 { return n }
	left := fib(n - 1)
	right := fib(n - 2)
	return left + right
}`,
			args:        []runtime.Value{runtime.IntValue(10)},
			compile:     []string{"fib"},
			wantEntered: []string{"fib"},
		},
		{
			name:       "native caller keeps live values around raw callee",
			fnName:     "caller",
			wantParams: 1,
			src: `func fib(n) {
	if n < 2 { return n }
	return fib(n - 1) + fib(n - 2)
}
func caller(n) {
	a := n + 11
	b := n * 100
	c := fib(n)
	d := a * 1000
	return d + b + c
}`,
			args:        []runtime.Value{runtime.IntValue(9)},
			compile:     []string{"fib", "caller"},
			wantEntered: []string{"fib", "caller"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := compileTop(t, tt.src)
			candidate := findProtoByName(top, tt.fnName)
			if candidate == nil {
				t.Fatalf("function %q not found", tt.fnName)
			}

			if tt.fnName == "caller" {
				fib := findProtoByName(top, "fib")
				if fib == nil {
					t.Fatal("function \"fib\" not found")
				}
				assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(fib), tt.wantParams)
				if abi := AnalyzeSpecializedABI(candidate); abi.Eligible {
					t.Fatalf("caller must not use raw self ABI, got %+v", abi)
				}
			} else {
				assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(candidate), tt.wantParams)
			}

			vmResults := runVMByName(t, tt.src, tt.fnName, tt.args)
			jitResults, entered := runForcedTier2ByName(t, top, tt.fnName, tt.compile, tt.args)
			assertRawIntSelfResultsEqual(t, tt.fnName, jitResults, vmResults)
			for _, name := range tt.wantEntered {
				if entered[name] == 0 {
					t.Fatalf("%s did not enter Tier 2", name)
				}
			}
		})
	}
}

func TestRawIntSelfABI_ExitResumeFallbackKeepsCallerLiveValues(t *testing.T) {
	src := `func grow(n, x) {
	if n == 0 { return x }
	return grow(n - 1, x + 100000000000000)
}
func caller(n, seed) {
	a := n + 17
	b := seed - 3
	c := n * 1000
	r := grow(n, seed)
	return r + a + b + c
}`
	top := compileTop(t, src)
	grow := findProtoByName(top, "grow")
	if grow == nil {
		t.Fatal("function \"grow\" not found")
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(grow), 2)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("function \"caller\" not found")
	}
	if abi := AnalyzeSpecializedABI(caller); abi.Eligible {
		t.Fatalf("caller must stay boxed, got %+v", abi)
	}

	args := []runtime.Value{runtime.IntValue(2), runtime.IntValue(90000000000000)}
	vmResults := runVMByName(t, src, "caller", args)
	jitResults, entered := runForcedTier2ByName(t, top, "caller", []string{"grow", "caller"}, args)
	assertRawIntSelfResultsEqual(t, "caller", jitResults, vmResults)
	if entered["caller"] == 0 || entered["grow"] == 0 {
		t.Fatalf("expected caller and grow to enter Tier 2, entered=%v", entered)
	}
}

func TestRawIntSelfABI_DepthPressureFallback(t *testing.T) {
	src := `func sumdown(n) {
	if n == 0 { return 0 }
	return n + sumdown(n - 1)
}`
	top := compileTop(t, src)
	sumdown := findProtoByName(top, "sumdown")
	if sumdown == nil {
		t.Fatal("function \"sumdown\" not found")
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(sumdown), 1)

	args := []runtime.Value{runtime.IntValue(200)}
	vmResults := runVMByName(t, src, "sumdown", args)
	jitResults, entered := runForcedTier2ByName(t, top, "sumdown", []string{"sumdown"}, args)
	assertRawIntSelfResultsEqual(t, "sumdown", jitResults, vmResults)
	if entered["sumdown"] == 0 {
		t.Fatal("sumdown did not enter Tier 2")
	}
}

func TestRawIntSelfABI_NonEligibleStaysBoxed(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		fnName  string
		args    []runtime.Value
		compile []string
	}{
		{
			name:   "non self call",
			fnName: "f",
			src: `func helper(x) { return x + 1 }
func f(x) { return helper(x) + 10 }`,
			args:    []runtime.Value{runtime.IntValue(31)},
			compile: []string{"helper", "f"},
		},
		{
			name:   "float return",
			fnName: "f",
			src: `func f(x) {
	if x == 0 { return 1.5 }
	return f(x - 1)
}`,
			args:    []runtime.Value{runtime.IntValue(3)},
			compile: []string{"f"},
		},
		{
			name:   "upvalue capture",
			fnName: "inner",
			src: `func outer(base) {
	func inner(n) {
		if n == 0 { return base }
		return inner(n - 1)
	}
	return inner
}
inner := outer(44)`,
			args:    []runtime.Value{runtime.IntValue(3)},
			compile: []string{"inner"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := compileTop(t, tt.src)
			proto := findProtoByName(top, tt.fnName)
			if proto == nil {
				t.Fatalf("function %q not found", tt.fnName)
			}
			if abi := AnalyzeSpecializedABI(proto); abi.Eligible {
				t.Fatalf("expected %s to be raw-int ineligible, got %+v", tt.fnName, abi)
			}

			vmResults := runVMByName(t, tt.src, tt.fnName, tt.args)
			jitResults, entered := runForcedTier2ByName(t, top, tt.fnName, tt.compile, tt.args)
			assertRawIntSelfResultsEqual(t, tt.fnName, jitResults, vmResults)
			if entered[tt.fnName] == 0 {
				t.Fatalf("%s did not enter generic Tier 2", tt.fnName)
			}
		})
	}
}

func runForcedTier2ByName(t *testing.T, top *vm.FuncProto, fnName string, compileNames []string, args []runtime.Value) ([]runtime.Value, map[string]byte) {
	t.Helper()

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	for _, name := range compileNames {
		proto := findProtoByName(top, name)
		if proto == nil {
			t.Fatalf("compile target %q not found", name)
		}
		if err := tm.CompileTier2(proto); err != nil {
			t.Fatalf("CompileTier2(%s): %v", name, err)
		}
	}

	fn := v.GetGlobal(fnName)
	if fn.IsNil() {
		t.Fatalf("function %q not found in globals", fnName)
	}
	results, err := v.CallValue(fn, args)
	if err != nil {
		t.Fatalf("CallValue(%s): %v", fnName, err)
	}

	entered := make(map[string]byte, len(compileNames))
	for _, name := range compileNames {
		proto := findProtoByName(top, name)
		entered[name] = proto.EnteredTier2
	}
	return results, entered
}

func assertRawIntSelfResultsEqual(t *testing.T, label string, got, want []runtime.Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s result count=%d want %d; got=%v want=%v", label, len(got), len(want), got, want)
	}
	for i := range got {
		assertValuesEqual(t, label, got[i], want[i])
	}
}
