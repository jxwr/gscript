//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const ackBenchmarkSource = `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
`

func BenchmarkAckermannVMCallValueSteady(b *testing.B) {
	top := compileTopB(b, ackBenchmarkSource)
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		b.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("ack")
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}

	for i := 0; i < 3; i++ {
		results, err := v.CallValue(fn, args)
		if err != nil {
			b.Fatalf("warm call: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
			b.Fatalf("warm ack result = %v, want int 125", results)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := v.CallValue(fn, args)
		if err != nil {
			b.Fatalf("call: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
			b.Fatalf("ack result = %v, want int 125", results)
		}
	}
}

func BenchmarkAckermannForcedTier2CallValueSteady(b *testing.B) {
	top := compileTopB(b, ackBenchmarkSource)
	ackProto := findProtoByName(top, "ack")
	if ackProto == nil {
		b.Fatal("ack proto not found")
	}

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		b.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("ack")

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(ackProto); err != nil {
		b.Fatalf("CompileTier2(ack): %v", err)
	}

	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	for i := 0; i < 10; i++ {
		results, err := v.CallValue(fn, args)
		if err != nil {
			b.Fatalf("warm call: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
			b.Fatalf("warm ack result = %v, want int 125", results)
		}
	}
	if ackProto.EnteredTier2 == 0 {
		b.Fatal("forced Tier 2 ack was compiled but never entered")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := v.CallValue(fn, args)
		if err != nil {
			b.Fatalf("call: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
			b.Fatalf("ack result = %v, want int 125", results)
		}
	}
}

func BenchmarkAckermannForcedTier2DirectSteady(b *testing.B) {
	b.Skip("direct executeTier2 bypasses VM call-frame lifecycle; use CallValue steady benchmarks")
	top := compileTopB(b, ackBenchmarkSource)
	ackProto := findProtoByName(top, "ack")
	if ackProto == nil {
		b.Fatal("ack proto not found")
	}

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		b.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("ack")
	if _, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(1), runtime.IntValue(1)}); err != nil {
		b.Fatalf("seed VM regs: %v", err)
	}

	tm := NewTieringManager()
	tm.SetCallVM(v)
	if err := tm.CompileTier2(ackProto); err != nil {
		b.Fatalf("CompileTier2(ack): %v", err)
	}
	ackProto.CallCount = tmDefaultTier2Threshold + 1
	cf := tm.tier2Compiled[ackProto]
	if cf == nil {
		b.Fatal("CompileTier2(ack) did not cache compiled function")
	}

	for i := 0; i < 10; i++ {
		regs := v.Regs()
		if len(regs) < ackProto.MaxStack {
			b.Fatalf("VM regs len %d < ack MaxStack %d", len(regs), ackProto.MaxStack)
		}
		clearAckBenchRegs(regs, ackProto.MaxStack)
		regs[0] = runtime.IntValue(3)
		regs[1] = runtime.IntValue(4)
		results, err := tm.executeTier2(cf, regs, 0, ackProto)
		if err != nil {
			b.Fatalf("warm executeTier2: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
			b.Fatalf("warm executeTier2(ack) = %v, want int 125", results)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		regs := v.Regs()
		clearAckBenchRegs(regs, ackProto.MaxStack)
		regs[0] = runtime.IntValue(3)
		regs[1] = runtime.IntValue(4)
		results, err := tm.executeTier2(cf, regs, 0, ackProto)
		if err != nil {
			b.Fatalf("executeTier2: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
			b.Fatalf("executeTier2(ack) = %v, want int 125", results)
		}
	}
}

func clearAckBenchRegs(regs []runtime.Value, n int) {
	if n > len(regs) {
		n = len(regs)
	}
	for i := 0; i < n; i++ {
		regs[i] = runtime.NilValue()
	}
}

func TestAckermannForcedTier2DirectExecuteStatus(t *testing.T) {
	top := compileTop(t, ackBenchmarkSource)
	ackProto := findProtoByName(top, "ack")
	if ackProto == nil {
		t.Fatal("ack proto not found")
	}

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fn := v.GetGlobal("ack")
	if _, err := v.CallValue(fn, []runtime.Value{runtime.IntValue(1), runtime.IntValue(1)}); err != nil {
		t.Fatalf("seed VM regs: %v", err)
	}
	tm := NewTieringManager()
	tm.SetCallVM(v)
	if err := tm.CompileTier2(ackProto); err != nil {
		t.Fatalf("CompileTier2(ack): %v", err)
	}
	ackProto.CallCount = tmDefaultTier2Threshold + 1
	cf := tm.tier2Compiled[ackProto]
	if cf == nil {
		t.Fatal("CompileTier2(ack) did not cache compiled function")
	}

	regs := v.Regs()
	if len(regs) < ackProto.MaxStack {
		t.Fatalf("VM regs len %d < ack MaxStack %d", len(regs), ackProto.MaxStack)
	}
	regs[0] = runtime.IntValue(3)
	regs[1] = runtime.IntValue(4)
	results, err := tm.executeTier2(cf, regs, 0, ackProto)
	if err != nil {
		t.Fatalf("executeTier2(ack) failed before VM fallback: %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
		t.Fatalf("executeTier2(ack) = %v, want int 125", results)
	}
}

func TestAckermannForcedTier2RepeatedCallValueDoesNotCorruptRuntime(t *testing.T) {
	for iter := 0; iter < 5; iter++ {
		top := compileTop(t, ackBenchmarkSource)
		ackProto := findProtoByName(top, "ack")
		if ackProto == nil {
			t.Fatal("ack proto not found")
		}

		v := vm.New(runtime.NewInterpreterGlobals())
		if _, err := v.Execute(top); err != nil {
			v.Close()
			t.Fatalf("iter %d execute top: %v", iter, err)
		}
		fn := v.GetGlobal("ack")

		tm := NewTieringManager()
		v.SetMethodJIT(tm)
		if err := tm.CompileTier2(ackProto); err != nil {
			v.Close()
			t.Fatalf("iter %d CompileTier2(ack): %v", iter, err)
		}

		args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
		for call := 0; call < 3; call++ {
			results, err := v.CallValue(fn, args)
			if err != nil {
				v.Close()
				t.Fatalf("iter %d call %d: %v", iter, call, err)
			}
			if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 125 {
				v.Close()
				t.Fatalf("iter %d call %d ack result = %v, want int 125", iter, call, results)
			}
		}
		v.Close()

		// Rebuild stdlib globals after forced JIT execution. Long-running Go
		// benchmarks crashed here when native code polluted process state.
		globals := runtime.NewInterpreterGlobals()
		if globals == nil {
			t.Fatalf("iter %d NewInterpreterGlobals returned nil", iter)
		}
	}
}
