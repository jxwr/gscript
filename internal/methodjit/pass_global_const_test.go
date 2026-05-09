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

func TestGlobalConstSpecializationSkipsEffectfulFunction(t *testing.T) {
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
	if countOpHelper(got, OpGuardGlobalConst) != 0 || countOpHelper(got, OpGetGlobal) == 0 {
		t.Fatalf("effectful function should not be global-const specialized:\n%s", Print(got))
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
