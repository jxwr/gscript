//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const fixedIntFibSrc = `
func fib(n) {
	if n < 2 { return n }
	return fib(n - 1) + fib(n - 2)
}
`

func TestFixedRecursiveIntFoldQualifiesFib(t *testing.T) {
	top := compileProto(t, fixedIntFibSrc)
	fib := findProtoByName(top, "fib")
	if fib == nil {
		t.Fatal("fib proto not found")
	}
	protocol, ok := analyzeFixedRecursiveIntFold(fib)
	if !ok {
		dumpProtoBytecode(t, fib)
		t.Fatal("fib should qualify for fixed recursive int fold protocol")
	}
	if protocol.threshold != 2 || protocol.bias != 0 || len(protocol.terms) != 2 {
		t.Fatalf("unexpected protocol: %#v", protocol)
	}
	if protocol.terms[0].decrement != 1 || protocol.terms[0].count != 1 ||
		protocol.terms[1].decrement != 2 || protocol.terms[1].count != 1 {
		t.Fatalf("unexpected recurrence terms: %#v", protocol.terms)
	}
}

func TestFixedRecursiveIntFoldQualifiesNonFibRecurrence(t *testing.T) {
	src := `
func stair(n) {
	if n < 3 { return n }
	return 1 + stair(n - 1) + stair(n - 3)
}
`
	top := compileProto(t, src)
	stair := findProtoByName(top, "stair")
	if stair == nil {
		t.Fatal("stair proto not found")
	}
	protocol, ok := analyzeFixedRecursiveIntFold(stair)
	if !ok {
		dumpProtoBytecode(t, stair)
		t.Fatal("non-fib recurrence should qualify for fixed recursive int fold protocol")
	}
	if protocol.threshold != 3 || protocol.bias != 1 || len(protocol.terms) != 2 {
		t.Fatalf("unexpected protocol: %#v", protocol)
	}
	if protocol.terms[0].decrement != 1 || protocol.terms[0].count != 1 ||
		protocol.terms[1].decrement != 3 || protocol.terms[1].count != 1 {
		t.Fatalf("unexpected recurrence terms: %#v", protocol.terms)
	}
	got, ok := protocol.fold(runtime.IntValue(8))
	if !ok || got != 27 {
		t.Fatalf("stair(8) fold = %d/%v, want 27/true", got, ok)
	}
}

func TestFixedRecursiveIntFoldRejectsAckermann(t *testing.T) {
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
	if qualifiesForFixedRecursiveIntFold(ack) {
		t.Fatal("ackermann must not qualify for fixed recursive int fold")
	}
}

func TestFixedRecursiveIntFoldExecutesFib(t *testing.T) {
	top := compileProto(t, fixedIntFibSrc)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("fib")
	for i := 0; i < 4; i++ {
		got, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(35)})
		if err != nil {
			t.Fatalf("fib call %d: %v", i, err)
		}
		if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 9227465 {
			t.Fatalf("fib(35)=%v, want 9227465", got)
		}
	}
	fib := findProtoByName(top, "fib")
	if fib == nil || fib.EnteredTier2 == 0 {
		t.Fatalf("fib did not enter Tier2 protocol; proto=%v", fib)
	}
	cf := tm.tier2Compiled[fib]
	if cf == nil || cf.FixedRecursiveIntFold == nil {
		t.Fatalf("fib did not compile to fixed recursive int fold; cf=%#v", cf)
	}
}

func TestFixedRecursiveIntFoldFallsBackWhenSelfGlobalChanges(t *testing.T) {
	src := fixedIntFibSrc + `
func replacement(n) {
	return 1000
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
	oldFib := v.GetGlobal("fib")
	for i := 0; i < 4; i++ {
		if _, err := v.CallValue(oldFib, []runtime.Value{runtime.IntValue(10)}); err != nil {
			t.Fatalf("warm old fib %d: %v", i, err)
		}
	}
	fib := findProtoByName(top, "fib")
	if cf := tm.tier2Compiled[fib]; cf == nil || cf.FixedRecursiveIntFold == nil {
		t.Fatalf("fib did not compile to fixed recursive int fold; cf=%#v", cf)
	}

	v.SetGlobal("fib", v.GetGlobal("replacement"))
	got, err := v.CallValue(oldFib, []runtime.Value{runtime.IntValue(5)})
	if err != nil {
		t.Fatalf("old fib after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 2000 {
		t.Fatalf("old fib after rebind = %v, want 2000 from dynamic global calls", got)
	}
}

func TestFixedRecursiveIntFoldOverInt48FallsBack(t *testing.T) {
	top := compileProto(t, fixedIntFibSrc)
	fib := findProtoByName(top, "fib")
	if fib == nil {
		t.Fatal("fib proto not found")
	}
	protocol, ok := analyzeFixedRecursiveIntFold(fib)
	if !ok {
		t.Fatal("fib should qualify")
	}
	if _, ok := protocol.fold(runtime.IntValue(80)); ok {
		t.Fatal("fib(80) should overflow int48 protocol and fall back")
	}
}
