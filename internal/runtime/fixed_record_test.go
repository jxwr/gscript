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

func TestFillFixedRecordKnownCtorSupportsInlineArities(t *testing.T) {
	ctor2 := NewSmallTableCtorN([]string{"a", "b"})
	fr := &FixedRecord{}
	v, ok := FillFixedRecordKnownCtor(fr, &ctor2, []Value{IntValue(10), IntValue(20)})
	if !ok {
		t.Fatal("FillFixedRecordKnownCtor failed for 2-field record")
	}
	if got, ok := v.FixedRecordRawGetString("a"); !ok || !got.IsInt() || got.Int() != 10 {
		t.Fatalf("a lookup = %v ok=%v", got, ok)
	}
	if got, ok := v.FixedRecordRawGetString("b"); !ok || !got.IsInt() || got.Int() != 20 {
		t.Fatalf("b lookup = %v ok=%v", got, ok)
	}

	ctor5 := NewSmallTableCtorN([]string{"a", "b", "c", "d", "e"})
	v, ok = FillFixedRecordKnownCtor(fr, &ctor5, []Value{IntValue(1), IntValue(2), IntValue(3), IntValue(4), IntValue(5)})
	if !ok {
		t.Fatal("FillFixedRecordKnownCtor failed for 5-field record")
	}
	if got, ok := v.FixedRecordRawGetString("e"); !ok || !got.IsInt() || got.Int() != 5 {
		t.Fatalf("e lookup after refill = %v ok=%v", got, ok)
	}
	if got, ok := v.FixedRecordRawGetString("missing"); !ok || !got.IsNil() {
		t.Fatalf("missing lookup after refill = %v ok=%v", got, ok)
	}
}

func TestFillFixedRecordKnownCtorRejectsNilAndLargeShape(t *testing.T) {
	ctor := NewSmallTableCtorN([]string{"a", "b"})
	if _, ok := FillFixedRecordKnownCtor(&FixedRecord{}, &ctor, []Value{IntValue(1), NilValue()}); ok {
		t.Fatal("nil-valued refill should fail")
	}

	large := NewSmallTableCtorN([]string{"a", "b", "c", "d", "e", "f"})
	vals := []Value{IntValue(1), IntValue(2), IntValue(3), IntValue(4), IntValue(5), IntValue(6)}
	if _, ok := FillFixedRecordKnownCtor(&FixedRecord{}, &large, vals); ok {
		t.Fatal("large refill should fail")
	}
}
