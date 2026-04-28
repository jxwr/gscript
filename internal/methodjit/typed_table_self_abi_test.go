//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTypedTableSelfABI_CheckTreeUsesNativeRecursiveCalls(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

root := makeTree(5)
`
	const want = 63
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		t.Fatal("checkTree proto not found")
	}
	fn := v.GetGlobal("checkTree")
	root := v.GetGlobal("root")
	if fn.IsNil() || root.IsNil() {
		t.Fatalf("missing globals: checkTree=%v root=%v", fn, root)
	}

	warm, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("warm checkTree: %v", err)
	}
	if len(warm) != 1 || !warm[0].IsInt() || warm[0].Int() != want {
		t.Fatalf("warm checkTree(root)=%v, want int %d", warm, want)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(checkTree); err != nil {
		t.Fatalf("CompileTier2(checkTree): %v", err)
	}
	cf := tm.tier2Compiled[checkTree]
	if cf == nil {
		t.Fatal("missing Tier 2 compiled checkTree")
	}
	if !cf.TypedSelfABI.Eligible {
		t.Fatalf("compiled checkTree typed ABI rejected: %s", cf.TypedSelfABI.RejectWhy)
	}
	if cf.TypedEntryOffset == 0 {
		t.Fatal("checkTree compiled without typed self entry")
	}

	checkTree.EnteredTier2 = 0
	got, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("Tier2 checkTree: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != want {
		t.Fatalf("Tier2 checkTree(root)=%v, want int %d", got, want)
	}
	if checkTree.EnteredTier2 == 0 {
		t.Fatal("checkTree did not enter Tier 2")
	}
	if exits := tm.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
		t.Fatalf("typed self recursion used boxed call exit %d times", exits)
	}
	if exits := tm.ExitStats().ByExitCode["ExitDeopt"]; exits != 0 {
		t.Fatalf("typed self recursion deoptimized %d times", exits)
	}
}
