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
	if _, err := stringFormatValue([]Value{StringValue("%.2f"), FloatValue(1.25)}); err != nil {
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
	var buf testWriter
	if err := stats.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !buf.seen {
		t.Fatal("WriteJSON wrote no data")
	}
}

type testWriter struct {
	seen bool
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.seen = w.seen || len(p) > 0
	return len(p), nil
}
