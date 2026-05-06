package runtime

import "testing"

func TestRuntimePathStatsTableAndStringCounters(t *testing.T) {
	stats := EnableRuntimePathStats()
	defer DisableRuntimePathStats()

	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(10))
	if got := tbl.RawGetInt(1); !got.IsInt() || got.Int() != 10 {
		t.Fatalf("RawGetInt(1) = %v, want 10", got)
	}
	_ = tbl.RawGetInt(99)
	tbl.RawSetInt(5000, IntValue(20))

	if _, err := stringFormatValue([]Value{StringValue("item_%03d"), IntValue(7)}); err != nil {
		t.Fatalf("fast stringFormatValue: %v", err)
	}
	if _, err := stringFormatValue([]Value{StringValue("%.2e"), FloatValue(1.25)}); err != nil {
		t.Fatalf("fallback stringFormatValue: %v", err)
	}

	snap := stats.Snapshot()
	if snap.TableArray.SetHot == 0 {
		t.Fatalf("TableArray.SetHot = 0, want non-zero")
	}
	if snap.TableArray.SetFallback == 0 {
		t.Fatalf("TableArray.SetFallback = 0, want non-zero")
	}
	if snap.TableArray.GetHot == 0 {
		t.Fatalf("TableArray.GetHot = 0, want non-zero")
	}
	if snap.TableArray.GetFallback == 0 {
		t.Fatalf("TableArray.GetFallback = 0, want non-zero")
	}
	if snap.StringFormat.Fast == 0 {
		t.Fatalf("StringFormat.Fast = 0, want non-zero")
	}
	if snap.StringFormat.Fallback == 0 {
		t.Fatalf("StringFormat.Fallback = 0, want non-zero")
	}
}

func TestRuntimePathStatsJSONSmoke(t *testing.T) {
	stats := EnableRuntimePathStats()
	defer DisableRuntimePathStats()

	RecordRuntimePathNativeCallFast()
	RecordRuntimePathStructuralKernelHit("whole_call_value", "unit_kernel")
	var buf testWriter
	if err := stats.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !buf.seen {
		t.Fatal("WriteJSON wrote no data")
	}
}

func TestRuntimePathStatsStructuralKernelAttribution(t *testing.T) {
	RecordRuntimePathStructuralKernelHit("whole_call_value", "disabled")

	stats := EnableRuntimePathStats()
	defer DisableRuntimePathStats()

	RecordRuntimePathStructuralKernelHit("whole_call_value", "alpha")
	RecordRuntimePathStructuralKernelHit("whole_call_value", "alpha")
	RecordRuntimePathStructuralKernelHit("whole_call_no_result", "beta")
	RecordRuntimePathStructuralKernelHit("", "ignored")
	RecordRuntimePathStructuralKernelHit("whole_call_value", "")

	snap := stats.Snapshot()
	if snap.StructuralKernel.Total != 3 {
		t.Fatalf("StructuralKernel.Total = %d, want 3", snap.StructuralKernel.Total)
	}
	if len(snap.StructuralKernel.PerKernel) != 2 {
		t.Fatalf("PerKernel length = %d, want 2: %+v", len(snap.StructuralKernel.PerKernel), snap.StructuralKernel.PerKernel)
	}
	first := snap.StructuralKernel.PerKernel[0]
	if first.Route != "whole_call_value" || first.Name != "alpha" || first.Count != 2 {
		t.Fatalf("first structural kernel row = %+v, want alpha count 2", first)
	}

	var buf testWriter
	stats.WriteText(&buf)
	if !buf.seen {
		t.Fatal("WriteText wrote no data")
	}
}

type testWriter struct {
	seen bool
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.seen = w.seen || len(p) > 0
	return len(p), nil
}

// TestRuntimePathStatsPerBuiltinAttribution exercises the per-GoFunction
// attribution wired into RecordRuntimePathNativeCall{Fast,Fallback}For. It
// drives one fast-path builtin and one fallback-path builtin and asserts:
//   - per_builtin entries are emitted with the correct names,
//   - per_builtin sums match the top-level fast/fallback totals,
//   - the disabled-stats path attributes nothing (no leaks across runs).
func TestRuntimePathStatsPerBuiltinAttribution(t *testing.T) {
	gfFast := &GoFunction{
		Name: "test.fast_builtin",
		Fast1: func(args []Value) (Value, error) {
			return IntValue(1), nil
		},
	}
	gfSlow := &GoFunction{
		Name: "test.slow_builtin",
		Fn: func(args []Value) ([]Value, error) {
			return []Value{IntValue(2)}, nil
		},
	}

	// Disabled: bumps must be no-ops.
	RecordRuntimePathNativeCallFastFor(gfFast)
	RecordRuntimePathNativeCallFallbackFor(gfSlow)

	stats := EnableRuntimePathStats()
	defer DisableRuntimePathStats()

	const fastN, slowN = 7, 3
	for i := 0; i < fastN; i++ {
		RecordRuntimePathNativeCallFastFor(gfFast)
	}
	for i := 0; i < slowN; i++ {
		RecordRuntimePathNativeCallFallbackFor(gfSlow)
	}
	// Also exercise the nil-gf path to make sure it does not crash.
	RecordRuntimePathNativeCallFastFor(nil)
	RecordRuntimePathNativeCallFallbackFor(nil)

	snap := stats.Snapshot()
	if snap.NativeCall.Fast != fastN+1 {
		t.Fatalf("NativeCall.Fast = %d, want %d", snap.NativeCall.Fast, fastN+1)
	}
	if snap.NativeCall.Fallback != slowN+1 {
		t.Fatalf("NativeCall.Fallback = %d, want %d", snap.NativeCall.Fallback, slowN+1)
	}
	if len(snap.NativeCall.PerBuiltin) == 0 {
		t.Fatal("PerBuiltin is empty")
	}

	var sumFast, sumFallback uint64
	gotFast := map[string]uint64{}
	gotFallback := map[string]uint64{}
	for _, e := range snap.NativeCall.PerBuiltin {
		sumFast += e.Fast
		sumFallback += e.Fallback
		gotFast[e.Name] = e.Fast
		gotFallback[e.Name] = e.Fallback
	}
	// Per-builtin totals must match the top-level minus the nil-gf calls
	// (which intentionally bump only the global counters).
	if sumFast != fastN {
		t.Fatalf("sum(per_builtin.fast) = %d, want %d", sumFast, fastN)
	}
	if sumFallback != slowN {
		t.Fatalf("sum(per_builtin.fallback) = %d, want %d", sumFallback, slowN)
	}
	if gotFast["test.fast_builtin"] != fastN {
		t.Fatalf("test.fast_builtin fast = %d, want %d", gotFast["test.fast_builtin"], fastN)
	}
	if gotFallback["test.slow_builtin"] != slowN {
		t.Fatalf("test.slow_builtin fallback = %d, want %d", gotFallback["test.slow_builtin"], slowN)
	}
	// Sort guarantee: fallback-heavy builtin should come first.
	if snap.NativeCall.PerBuiltin[0].Name != "test.slow_builtin" {
		t.Fatalf("PerBuiltin[0] = %q, want test.slow_builtin (sorted by fallback desc)",
			snap.NativeCall.PerBuiltin[0].Name)
	}
}
