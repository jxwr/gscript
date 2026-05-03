//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestCompiledFunctionProtocolKind(t *testing.T) {
	tests := []struct {
		name string
		cf   *CompiledFunction
		want compiledProtocolKind
	}{
		{name: "nil", cf: nil, want: compiledProtocolNone},
		{name: "native", cf: &CompiledFunction{}, want: compiledProtocolNone},
		{name: "fixed int fold", cf: &CompiledFunction{FixedRecursiveIntFold: &fixedRecursiveIntFoldProtocol{}}, want: compiledProtocolFixedRecursiveIntFold},
		{name: "fixed nested int fold", cf: &CompiledFunction{FixedRecursiveNestedIntFold: &fixedRecursiveNestedIntFoldProtocol{}}, want: compiledProtocolFixedRecursiveNestedIntFold},
		{name: "table builder", cf: &CompiledFunction{FixedRecursiveTableBuilder: &fixedRecursiveTableBuilderProtocol{}}, want: compiledProtocolFixedRecursiveTableBuilder},
		{name: "table fold", cf: &CompiledFunction{FixedRecursiveTableFold: &fixedRecursiveTableFoldProtocol{}}, want: compiledProtocolFixedRecursiveTableFold},
		{name: "mutual int scc", cf: &CompiledFunction{MutualRecursiveIntSCC: &mutualRecursiveIntSCCProtocol{}}, want: compiledProtocolMutualRecursiveIntSCC},
	}
	for _, tt := range tests {
		if got := tt.cf.ProtocolKind(); got != tt.want {
			t.Fatalf("%s: ProtocolKind()=%v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTryCompiledProtocolCallExitUsesTier2Protocol(t *testing.T) {
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
		if _, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}); err != nil {
			t.Fatalf("warm ack %d: %v", i, err)
		}
	}
	ack := findProtoByName(top, "ack")
	if cf := tm.tier2Compiled[ack]; cf == nil || cf.ProtocolKind() != compiledProtocolFixedRecursiveNestedIntFold {
		t.Fatalf("ack did not compile to fixed recursive nested int fold: %#v", cf)
	}

	regs := []runtime.Value{fn, runtime.IntValue(3), runtime.IntValue(4)}
	handled, err := tm.tryCompiledProtocolCallExit(fn, regs, 0, 2, 1)
	if err != nil {
		t.Fatalf("protocol call exit fast path: %v", err)
	}
	if !handled {
		t.Fatal("protocol call exit fast path did not handle stable compiled protocol callee")
	}
	got := v.Regs()[0]
	if !got.IsInt() || got.Int() != 125 {
		t.Fatalf("protocol call result=%v, want 125", got)
	}
}

func TestTryCompiledProtocolCallExitRejectsNonIntArgs(t *testing.T) {
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
		if _, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}); err != nil {
			t.Fatalf("warm ack %d: %v", i, err)
		}
	}
	regs := []runtime.Value{fn, runtime.StringValue("3"), runtime.IntValue(4)}
	handled, err := tm.tryCompiledProtocolCallExit(fn, regs, 0, 2, 1)
	if err != nil {
		t.Fatalf("non-int protocol call fast path errored: %v", err)
	}
	if handled {
		t.Fatal("protocol call fast path handled non-int arguments")
	}
}

func TestProtocolConstCallFoldAnnotatesGuardedProtocolCall(t *testing.T) {
	top := compileProto(t, fixedNestedAckSrc+`
func driver() {
	return ack(3, 4)
}
`)
	ack := findProtoByName(top, "ack")
	driver := findProtoByName(top, "driver")
	if ack == nil || driver == nil {
		t.Fatalf("missing protos: ack=%v driver=%v", ack, driver)
	}

	fn := AnnotateProtocolConstCallFolds(BuildGraph(driver), map[string]*vm.FuncProto{"ack": ack})
	if len(fn.ProtocolConstCallFolds) != 1 {
		t.Fatalf("ProtocolConstCallFolds len=%d, want 1", len(fn.ProtocolConstCallFolds))
	}
	for _, fact := range fn.ProtocolConstCallFolds {
		if fact.CalleeProto != ack || fact.Result != 125 {
			t.Fatalf("unexpected fold fact: callee=%v result=%d", fact.CalleeProto, fact.Result)
		}
		if len(fact.GuardConsts) != 1 || len(fact.GuardProtos) != 1 || fact.GuardProtos[0] != ack {
			t.Fatalf("missing callee guard in fold fact: %#v", fact)
		}
		if got := driver.Constants[fact.GuardConsts[0]]; !got.IsString() || got.Str() != "ack" {
			t.Fatalf("callee guard const=%v, want ack string", got)
		}
		if len(fact.IntGuardConsts) != 0 || len(fact.IntGuardValues) != 0 {
			t.Fatalf("const-arg fold should not need int global guards: %#v", fact)
		}
	}
}

func TestProtocolConstCallFoldFallbackAfterCalleeRebind(t *testing.T) {
	top := compileProto(t, fixedNestedAckSrc+`
func replacement(m, n) {
	return 1000
}

func driver() {
	return ack(3, 4)
}
`)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	driver := findProtoByName(top, "driver")
	if driver == nil {
		t.Fatal("driver proto not found")
	}
	if err := tm.CompileTier2(driver); err != nil {
		t.Fatalf("CompileTier2(driver): %v", err)
	}
	fn := v.GetGlobal("driver")
	got, err := v.CallValue(fn, nil)
	if err != nil {
		t.Fatalf("driver before rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 125 {
		t.Fatalf("driver before rebind=%v, want 125", got)
	}

	v.SetGlobal("ack", v.GetGlobal("replacement"))
	got, err = v.CallValue(fn, nil)
	if err != nil {
		t.Fatalf("driver after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 1000 {
		t.Fatalf("driver after rebind=%v, want 1000 from fallback", got)
	}
}

func TestProtocolConstCallFoldEliminatesLoopCallExitForLocalDeclarations(t *testing.T) {
	src := fixedNestedAckSrc + `
N := 4
result := 0
for i := 1; i <= 20; i++ {
	result = ack(3, N)
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
	got := v.GetGlobal("result")
	if !got.IsInt() || got.Int() != 125 {
		t.Fatalf("result=%v, want 125", got)
	}
	for _, site := range tm.ExitStats().Sites {
		if site.ExitCode == ExitCallExit && site.Count >= 20 {
			t.Fatalf("loop protocol call still used call-exit: site=%#v", site)
		}
	}
}
