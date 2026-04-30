//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFixedRecursiveTableBuilderQualifiesMakeTree(t *testing.T) {
	src := `
func makeTree(depth) {
	if depth == 0 {
		return {left: nil, right: nil}
	}
	return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}
`
	top := compileProto(t, src)
	makeTree := findProtoByName(top, "makeTree")
	if makeTree == nil {
		t.Fatal("makeTree proto not found")
	}
	if _, ok := analyzeFixedRecursiveTableBuilder(makeTree); !ok {
		dumpProtoBytecode(t, makeTree)
		t.Fatal("makeTree should qualify for fixed recursive table builder protocol")
	}
}

func TestFixedRecursiveTableBuilderExecutesMakeTree(t *testing.T) {
	src := `
func makeTree(depth) {
	if depth == 0 {
		return {left: nil, right: nil}
	}
	return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
	if node.left == nil { return 1 }
	return 1 + checkTree(node.left) + checkTree(node.right)
}

result := checkTree(makeTree(5))
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 63 {
		t.Fatalf("result = %v, want 63", result)
	}
	makeTree := findProtoByName(top, "makeTree")
	if makeTree == nil || makeTree.EnteredTier2 == 0 {
		t.Fatalf("makeTree did not enter fixed recursive table builder Tier 2; proto=%v", makeTree)
	}
	if cf := tm.tier2Compiled[makeTree]; cf == nil || cf.FixedRecursiveTableBuilder == nil {
		t.Fatalf("makeTree compiled function missing builder protocol: %#v", cf)
	}
}

func TestFixedRecursiveTableBuilderFallsBackWhenSelfGlobalChanges(t *testing.T) {
	src := `
func makeTree(depth) {
	if depth == 0 {
		return {left: nil, right: nil}
	}
	return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func replacement(depth) {
	return {left: nil, right: nil}
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	oldMakeTree := v.GetGlobal("makeTree")
	for i := 0; i < 2; i++ {
		if _, err := v.CallValue(oldMakeTree, []runtime.Value{runtime.IntValue(2)}); err != nil {
			t.Fatalf("warm makeTree %d: %v", i, err)
		}
	}
	makeTree := findProtoByName(top, "makeTree")
	if cf := tm.tier2Compiled[makeTree]; cf == nil || cf.FixedRecursiveTableBuilder == nil {
		t.Fatalf("makeTree did not compile to fixed recursive table builder; cf=%#v", cf)
	}

	v.SetGlobal("makeTree", v.GetGlobal("replacement"))
	got, err := v.CallValue(oldMakeTree, []runtime.Value{runtime.IntValue(2)})
	if err != nil {
		t.Fatalf("old makeTree after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsTable() {
		t.Fatalf("old makeTree after rebind = %v, want table", got)
	}
	root := got[0].Table()
	if !root.RawGetString("left").IsTable() || !root.RawGetString("right").IsTable() {
		t.Fatalf("fallback did not observe rebound recursive calls: left=%v right=%v",
			root.RawGetString("left"), root.RawGetString("right"))
	}
}

func TestFixedRecursiveTableBuilderRejectsDepthAboveNativeCap(t *testing.T) {
	src := `
func makeTree(depth) {
	if depth == 0 {
		return {left: nil, right: nil}
	}
	return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	makeTreeValue := v.GetGlobal("makeTree")
	for i := 0; i < 2; i++ {
		if _, err := v.CallValue(makeTreeValue, []runtime.Value{runtime.IntValue(1)}); err != nil {
			t.Fatalf("warm makeTree %d: %v", i, err)
		}
	}
	makeTree := findProtoByName(top, "makeTree")
	cf := tm.tier2Compiled[makeTree]
	if cf == nil || cf.FixedRecursiveTableBuilder == nil {
		t.Fatalf("makeTree did not compile to fixed recursive table builder; cf=%#v", cf)
	}

	regs := []runtime.Value{runtime.IntValue(fixedRecursiveTableBuilderMaxDepth + 1)}
	if _, err := tm.executeFixedRecursiveTableBuilder(cf, regs, 0, makeTree, tm.retBuf[:0]); err == nil {
		t.Fatal("depth above native cap should reject before recursive allocation")
	}
	if !tm.tier2Failed[makeTree] {
		t.Fatal("depth above native cap should disable the fixed builder protocol")
	}
	if tm.tier2Compiled[makeTree] != nil {
		t.Fatal("depth above native cap should evict the fixed builder compiled function")
	}
}
