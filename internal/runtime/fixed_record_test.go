package runtime

import "testing"

func TestFixedRecordReadsAndMaterializes(t *testing.T) {
	ctor := NewSmallTableCtorN([]string{"id", "account", "shard", "kind", "value"})
	kind := StringValue("view")
	v, ok := NewFixedRecordValue5(&ctor,
		IntValue(1),
		IntValue(17),
		IntValue(8),
		kind,
		IntValue(29),
	)
	if !ok {
		t.Fatal("NewFixedRecordValue5 failed")
	}
	if !v.IsTable() {
		t.Fatal("fixed record should satisfy table type checks")
	}
	if got, ok := v.FixedRecordRawGetString("kind"); !ok || got != kind {
		t.Fatalf("kind lookup = %v ok=%v", got, ok)
	}
	if got, ok := v.FixedRecordRawGetString("missing"); !ok || !got.IsNil() {
		t.Fatalf("missing lookup = %v ok=%v", got, ok)
	}

	tbl := v.Table()
	if tbl == nil {
		t.Fatal("materialized table is nil")
	}
	if got := tbl.RawGetString("value"); !got.IsInt() || got.Int() != 29 {
		t.Fatalf("materialized value = %v", got)
	}
	if again := v.Table(); again != tbl {
		t.Fatal("materialization should preserve table identity")
	}
}

func TestFixedRecordFallsBackOnNilOrLargeShape(t *testing.T) {
	ctor := NewSmallTableCtorN([]string{"a", "b", "c", "d", "e"})
	if _, ok := NewFixedRecordValue5(&ctor, IntValue(1), IntValue(2), NilValue(), IntValue(4), IntValue(5)); ok {
		t.Fatal("nil-valued record should not use fixed record")
	}

	large := NewSmallTableCtorN([]string{"a", "b", "c", "d", "e", "f"})
	vals := []Value{IntValue(1), IntValue(2), IntValue(3), IntValue(4), IntValue(5), IntValue(6)}
	if _, ok := NewFixedRecordValue(&large, vals); ok {
		t.Fatal("large fixed record should fall back to table")
	}
}
