//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const mutualIntHofstadterSrc = `
func F(n) {
	if n == 0 { return 1 }
	return n - M(F(n - 1))
}
func M(n) {
	if n == 0 { return 0 }
	return n - F(M(n - 1))
}
`

func TestMutualRecursiveIntSCCQualifiesHofstadter(t *testing.T) {
	top := compileProto(t, mutualIntHofstadterSrc)
	f := findProtoByName(top, "F")
	m := findProtoByName(top, "M")
	if f == nil || m == nil {
		t.Fatalf("missing protos: F=%v M=%v", f != nil, m != nil)
	}
	protocol, ok := analyzeMutualRecursiveIntSCC(f, map[string]*vm.FuncProto{"F": f, "M": m})
	if !ok {
		dumpProtoBytecode(t, f)
		dumpProtoBytecode(t, m)
		t.Fatal("Hofstadter F/M should qualify for mutual recursive int SCC protocol")
	}
	if len(protocol.protos) != 2 || protocol.entryIndex < 0 {
		t.Fatalf("unexpected protocol: %#v", protocol)
	}
}

func TestMutualRecursiveIntSCCRejectsAckermann(t *testing.T) {
	src := `
func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}
`
	top := compileProto(t, src)
	ack := findProtoByName(top, "ack")
	if ack == nil {
		t.Fatal("ack proto not found")
	}
	if protocol, ok := analyzeMutualRecursiveIntSCC(ack, map[string]*vm.FuncProto{"ack": ack}); ok {
		t.Fatalf("ackermann must not qualify for mutual recursive int SCC: %#v", protocol)
	}
}

func TestMutualRecursiveIntSCCModMatchesVMIntSemantics(t *testing.T) {
	cases := []struct {
		a, b int64
		want int64
	}{
		{a: -3, b: 2, want: 1},
		{a: 3, b: -2, want: -1},
		{a: -3, b: -2, want: -1},
	}
	for _, tc := range cases {
		got, ok := mutualRecursiveIntArith(vm.OP_MOD, tc.a, tc.b)
		if !ok || got != tc.want {
			t.Fatalf("%d %% %d = %d/%v, want %d/true", tc.a, tc.b, got, ok, tc.want)
		}
	}
}

func TestMutualRecursiveIntSCCExecutesHofstadter(t *testing.T) {
	top := compileProto(t, mutualIntHofstadterSrc)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("F")
	for i := 0; i < 4; i++ {
		got, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(25)})
		if err != nil {
			t.Fatalf("F call %d: %v", i, err)
		}
		if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 16 {
			t.Fatalf("F(25)=%v, want 16", got)
		}
	}
	f := findProtoByName(top, "F")
	if f == nil || f.EnteredTier2 == 0 {
		t.Fatalf("F did not enter Tier2 protocol; proto=%v", f)
	}
	cf := tm.tier2Compiled[f]
	if cf == nil || cf.MutualRecursiveIntSCC == nil {
		t.Fatalf("F did not compile to mutual recursive int SCC; cf=%#v", cf)
	}
}

func TestMutualRecursiveIntSCCPrecompiledFromStableLoopGlobals(t *testing.T) {
	src := mutualIntHofstadterSrc + `
result := 0
for rep := 1; rep <= 1000; rep++ {
	result = F(25)
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
	f := findProtoByName(top, "F")
	if f == nil {
		t.Fatal("F proto not found")
	}
	cf := tm.tier2Compiled[f]
	if cf == nil || cf.MutualRecursiveIntSCC == nil {
		t.Fatalf("F was not precompiled to mutual recursive int SCC from stable loop globals; cf=%#v", cf)
	}
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "F" && site.ExitCode == ExitCallExit {
			t.Fatalf("F still used call-exit recursion after precompile: site=%#v", site)
		}
	}
}

func TestMutualRecursiveIntSCCFallsBackWhenPeerGlobalChanges(t *testing.T) {
	src := mutualIntHofstadterSrc + `
func replacement(n) {
	return 100
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
	oldF := v.GetGlobal("F")
	for i := 0; i < 4; i++ {
		if _, err := v.CallValue(oldF, []runtime.Value{runtime.IntValue(10)}); err != nil {
			t.Fatalf("warm old F %d: %v", i, err)
		}
	}
	f := findProtoByName(top, "F")
	if cf := tm.tier2Compiled[f]; cf == nil || cf.MutualRecursiveIntSCC == nil {
		t.Fatalf("F did not compile to mutual recursive int SCC; cf=%#v", cf)
	}

	v.SetGlobal("M", v.GetGlobal("replacement"))
	got, err := v.CallValue(oldF, []runtime.Value{runtime.IntValue(2)})
	if err != nil {
		t.Fatalf("old F after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != -98 {
		t.Fatalf("old F after rebind = %v, want -98 from dynamic peer global call", got)
	}
}
