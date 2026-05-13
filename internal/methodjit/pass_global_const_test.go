package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestGlobalConstSpecializationRewritesNumericGetGlobal(t *testing.T) {
	proto := compileFunction(t, `
func f() {
    return N - 1
}`)
	fn := BuildGraph(proto)
	constIdx := findConstStringIndex(t, proto, "N")
	got, err := globalConstSpecializationPass(fn, map[int]runtime.Value{
		constIdx: runtime.IntValue(5),
	})
	if err != nil {
		t.Fatalf("globalConstSpecializationPass: %v", err)
	}
	if countOpHelper(got, OpGuardGlobalConst) != 1 {
		t.Fatalf("expected one GuardGlobalConst:\n%s", Print(got))
	}
	if countOpHelper(got, OpGetGlobal) != 0 {
		t.Fatalf("expected GetGlobal to be rewritten:\n%s", Print(got))
	}
	if countOpHelper(got, OpConstInt) == 0 {
		t.Fatalf("expected rewritten ConstInt:\n%s", Print(got))
	}
	if errs := Validate(got); len(errs) > 0 {
		t.Fatalf("invalid IR after global const specialization: %v\n%s", errs[0], Print(got))
	}
}

func TestGlobalConstSpecializationRewritesEffectfulFunctionWithGuard(t *testing.T) {
	proto := compileFunction(t, `
func f() {
    g()
    return N
}`)
	fn := BuildGraph(proto)
	constIdx := findConstStringIndex(t, proto, "N")
	got, err := globalConstSpecializationPass(fn, map[int]runtime.Value{
		constIdx: runtime.IntValue(5),
	})
	if err != nil {
		t.Fatalf("globalConstSpecializationPass: %v", err)
	}
	if countOpHelper(got, OpGuardGlobalConst) != 1 || countOpHelper(got, OpGetGlobal) != 1 {
		t.Fatalf("effectful function should keep call global and guard numeric global:\n%s", Print(got))
	}
	if countOpHelper(got, OpConstInt) == 0 {
		t.Fatalf("expected rewritten ConstInt:\n%s", Print(got))
	}
}

func TestBuildProtoNumericStableGlobals(t *testing.T) {
	proto := compileTop(t, `
N := 1000
dt := 0.01
func step() {}
step()
`)
	values := buildProtoNumericStableGlobals(proto)
	nIdx := findConstStringIndex(t, proto, "N")
	dtIdx := findConstStringIndex(t, proto, "dt")
	if got := values[nIdx]; !got.IsInt() || got.Int() != 1000 {
		t.Fatalf("N value=%v, want int 1000", got)
	}
	if got := values[dtIdx]; !got.IsFloat() || got.Float() != 0.01 {
		t.Fatalf("dt value=%v, want float 0.01", got)
	}
}

func findConstStringIndex(t *testing.T, proto *vm.FuncProto, name string) int {
	t.Helper()
	for i, v := range proto.Constants {
		if v.IsString() && v.Str() == name {
			return i
		}
	}
	t.Fatalf("missing const string %q", name)
	return -1
}
