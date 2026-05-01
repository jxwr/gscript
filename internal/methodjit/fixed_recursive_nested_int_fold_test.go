//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const fixedNestedAckSrc = `
func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}
`

func TestFixedRecursiveNestedIntFoldQualifiesAckermann(t *testing.T) {
	top := compileProto(t, fixedNestedAckSrc)
	ack := findProtoByName(top, "ack")
	if ack == nil {
		t.Fatal("ack proto not found")
	}
	protocol, ok := analyzeFixedRecursiveNestedIntFold(ack)
	if !ok {
		dumpProtoBytecode(t, ack)
		t.Fatal("ack should qualify for fixed recursive nested int fold protocol")
	}
	if protocol.baseAdd != 1 || protocol.zeroArg != 1 || protocol.mStep != 1 || protocol.nStep != 1 {
		t.Fatalf("unexpected protocol: %#v", protocol)
	}
	got, ok := protocol.fold(runtime.IntValue(3), runtime.IntValue(4))
	if !ok || got != 125 {
		t.Fatalf("ack(3,4) fold = %d/%v, want 125/true", got, ok)
	}
}

func TestFixedRecursiveNestedIntFoldQualifiesNonAckNameAndZeroArg(t *testing.T) {
	top := compileProto(t, `
func hyper(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return hyper(m - 1, 2) }
	return hyper(m - 1, hyper(m, n - 1))
}
`)
	hyper := findProtoByName(top, "hyper")
	if hyper == nil {
		t.Fatal("hyper proto not found")
	}
	protocol, ok := analyzeFixedRecursiveNestedIntFold(hyper)
	if !ok {
		dumpProtoBytecode(t, hyper)
		t.Fatal("non-ack nested recurrence should qualify for fixed recursive nested int fold protocol")
	}
	got, ok := protocol.fold(runtime.IntValue(3), runtime.IntValue(2))
	if !ok || got != 119 {
		t.Fatalf("hyper(3,2) fold = %d/%v, want 119/true", got, ok)
	}
}

func TestFixedRecursiveNestedIntFoldRejectsNonNestedRecurrence(t *testing.T) {
	top := compileProto(t, fixedIntFibSrc)
	fib := findProtoByName(top, "fib")
	if fib == nil {
		t.Fatal("fib proto not found")
	}
	if qualifiesForFixedRecursiveNestedIntFold(fib) {
		t.Fatal("fib must not qualify for fixed recursive nested int fold")
	}
}

func TestFixedRecursiveNestedIntFoldExecutesAckermann(t *testing.T) {
	top := compileProto(t, fixedNestedAckSrc)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("ack")
	for i := 0; i < 4; i++ {
		got, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)})
		if err != nil {
			t.Fatalf("ack call %d: %v", i, err)
		}
		if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 125 {
			t.Fatalf("ack(3,4)=%v, want 125", got)
		}
	}
	ack := findProtoByName(top, "ack")
	if ack == nil || ack.EnteredTier2 == 0 {
		t.Fatalf("ack did not enter Tier2 protocol; proto=%v", ack)
	}
	cf := tm.tier2Compiled[ack]
	if cf == nil || cf.FixedRecursiveNestedIntFold == nil {
		t.Fatalf("ack did not compile to fixed recursive nested int fold; cf=%#v", cf)
	}
}

func TestFixedRecursiveNestedIntFoldFallsBackWhenSelfGlobalChanges(t *testing.T) {
	src := fixedNestedAckSrc + `
func replacement(m, n) {
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
	oldAck := v.GetGlobal("ack")
	for i := 0; i < 4; i++ {
		if _, err := v.CallValue(oldAck, []runtime.Value{runtime.IntValue(1), runtime.IntValue(0)}); err != nil {
			t.Fatalf("warm old ack %d: %v", i, err)
		}
	}
	ack := findProtoByName(top, "ack")
	if cf := tm.tier2Compiled[ack]; cf == nil || cf.FixedRecursiveNestedIntFold == nil {
		t.Fatalf("ack did not compile to fixed recursive nested int fold; cf=%#v", cf)
	}

	v.SetGlobal("ack", v.GetGlobal("replacement"))
	got, err := v.CallValue(oldAck, []runtime.Value{runtime.IntValue(1), runtime.IntValue(0)})
	if err != nil {
		t.Fatalf("old ack after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 1000 {
		t.Fatalf("old ack after rebind = %v, want 1000 from dynamic global call", got)
	}
}

func TestFixedRecursiveNestedIntFoldOverInt48FallsBack(t *testing.T) {
	protocol := &fixedRecursiveNestedIntFoldProtocol{
		baseAdd: fixedFoldMaxInt48,
		zeroArg: 1,
		mStep:   1,
		nStep:   1,
	}
	if _, ok := protocol.fold(runtime.IntValue(0), runtime.IntValue(1)); ok {
		t.Fatal("nested fold should fall back before producing a value outside int48 semantics")
	}
}
