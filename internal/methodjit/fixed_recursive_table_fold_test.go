//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFixedRecursiveTableFoldQualifiesCheckTree(t *testing.T) {
	src := `
func checkTree(node) {
	if node.left == nil { return 1 }
	return 1 + checkTree(node.left) + checkTree(node.right)
}
`
	top := compileProto(t, src)
	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		t.Fatal("checkTree proto not found")
	}
	protocol, ok := analyzeFixedRecursiveTableFold(checkTree)
	if !ok {
		dumpProtoBytecode(t, checkTree)
		t.Fatal("checkTree should qualify for fixed recursive table fold protocol")
	}
	if protocol.nilField != "left" || protocol.baseValue != 1 || protocol.combineBias != 1 || len(protocol.children) != 2 {
		t.Fatalf("unexpected derived protocol: %#v", protocol)
	}
	if !shouldPromoteTier2(checkTree, analyzeFuncProfile(checkTree), 2) {
		t.Fatal("checkTree protocol should promote once hot")
	}
}

func TestFixedRecursiveTableFoldQualifiesNonLeftRightWalker(t *testing.T) {
	src := `
func makePair(depth) {
	if depth == 0 {
		return {first: nil, second: nil}
	}
	return {first: makePair(depth - 1), second: makePair(depth - 1)}
}

func countPair(node) {
	if node.first == nil { return 7 }
	return 3 + countPair(node.first) + countPair(node.second)
}
`
	top := compileProto(t, src)
	countPair := findProtoByName(top, "countPair")
	if countPair == nil {
		t.Fatal("countPair proto not found")
	}
	protocol, ok := analyzeFixedRecursiveTableFold(countPair)
	if !ok {
		dumpProtoBytecode(t, countPair)
		t.Fatal("non-left/right fixed recursive walker should qualify")
	}
	if protocol.nilField != "first" || protocol.baseValue != 7 || protocol.combineBias != 3 {
		t.Fatalf("unexpected derived protocol: %#v", protocol)
	}
	if len(protocol.children) != 2 || protocol.children[0].field != "first" || protocol.children[1].field != "second" {
		t.Fatalf("unexpected derived children: %#v", protocol.children)
	}
}

func TestFixedRecursiveTableFoldExecutesNonLeftRightWalker(t *testing.T) {
	src := `
func makePair(depth) {
	if depth == 0 {
		return {first: nil, second: nil}
	}
	return {first: makePair(depth - 1), second: makePair(depth - 1)}
}

func countPair(node) {
	if node.first == nil { return 7 }
	return 3 + countPair(node.first) + countPair(node.second)
}

root := makePair(4)
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
	fn := v.GetGlobal("countPair")
	root := v.GetGlobal("root")
	for i := 0; i < 3; i++ {
		got, err := v.CallValue(fn, []runtime.Value{root})
		if err != nil {
			t.Fatalf("countPair call %d: %v", i, err)
		}
		if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 157 {
			t.Fatalf("countPair(root)=%v, want 157", got)
		}
	}
	countPair := findProtoByName(top, "countPair")
	if countPair == nil || countPair.EnteredTier2 == 0 {
		t.Fatalf("countPair did not enter Tier2 protocol; proto=%v", countPair)
	}
}

func TestFixedRecursiveTableFoldFallsBackWhenSelfGlobalChanges(t *testing.T) {
	src := `
func makePair(depth) {
	if depth == 0 {
		return {first: nil, second: nil}
	}
	return {first: makePair(depth - 1), second: makePair(depth - 1)}
}

func countPair(node) {
	if node.first == nil { return 7 }
	return 3 + countPair(node.first) + countPair(node.second)
}

func replacement(node) {
	return 100
}

root := makePair(2)
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
	oldCountPair := v.GetGlobal("countPair")
	root := v.GetGlobal("root")
	for i := 0; i < 3; i++ {
		if _, err := v.CallValue(oldCountPair, []runtime.Value{root}); err != nil {
			t.Fatalf("warm countPair %d: %v", i, err)
		}
	}
	countPair := findProtoByName(top, "countPair")
	if cf := tm.tier2Compiled[countPair]; cf == nil || cf.FixedRecursiveTableFold == nil {
		t.Fatalf("countPair did not compile to fixed recursive table fold; cf=%#v", cf)
	}

	v.SetGlobal("countPair", v.GetGlobal("replacement"))
	got, err := v.CallValue(oldCountPair, []runtime.Value{root})
	if err != nil {
		t.Fatalf("old countPair after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 203 {
		t.Fatalf("old countPair after rebind = %v, want 203 from dynamic global calls", got)
	}
}

func TestFixedRecursiveTableFoldOverInt48FallsBack(t *testing.T) {
	leaf := runtime.NewTable()
	leaf.RawSetString("mark", runtime.NilValue())

	root := runtime.NewTable()
	root.RawSetString("mark", runtime.IntValue(1))
	root.RawSetString("child", runtime.FreshTableValue(leaf))

	protocol := &fixedRecursiveTableFoldProtocol{
		nilField:    "mark",
		baseValue:   fixedFoldMaxInt48,
		combineBias: 1,
		children: []fixedRecursiveTableFoldChild{
			{field: "child"},
		},
	}
	if _, ok := protocol.fold(runtime.FreshTableValue(root)); ok {
		t.Fatal("fold should fall back before producing a value outside int48 semantics")
	}
}

func dumpProtoBytecode(t *testing.T, proto *vm.FuncProto) {
	t.Helper()
	for pc, inst := range proto.Code {
		t.Logf("[%02d] %s A=%d B=%d C=%d Bx=%d sBx=%d", pc, vm.OpName(vm.DecodeOp(inst)),
			vm.DecodeA(inst), vm.DecodeB(inst), vm.DecodeC(inst), vm.DecodeBx(inst), vm.DecodesBx(inst))
	}
}
