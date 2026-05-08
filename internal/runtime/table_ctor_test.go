package runtime

import "testing"

func TestNewTableFromCtorNNonNilUsesFixedShape(t *testing.T) {
	ctor := NewSmallTableCtorN([]string{"a", "b", "c"})
	vals := []Value{IntValue(11), StringValue("bee"), BoolValue(true)}

	tbl := NewTableFromCtorNNonNil(&ctor, vals)
	if tbl == nil {
		t.Fatal("nil table")
	}
	if got := tbl.ShapeID(); got == 0 || got != ctor.Shape.ID {
		t.Fatalf("shapeID=%d want ctor shape %d", got, ctor.Shape.ID)
	}
	if got := tbl.RawGetString("a"); !got.IsInt() || got.Int() != 11 {
		t.Fatalf("a=%v, want 11", got)
	}
	if got := tbl.RawGetString("b"); !got.IsString() || got.Str() != "bee" {
		t.Fatalf("b=%v, want bee", got)
	}
	if got := tbl.RawGetString("c"); !got.IsBool() || !got.Bool() {
		t.Fatalf("c=%v, want true", got)
	}
}

func TestNewTableFromCtorNNonNilFallsBackForInvalidCtor(t *testing.T) {
	vals := []Value{IntValue(1), IntValue(2)}
	tbl := NewTableFromCtorNNonNil(nil, vals)
	if tbl == nil {
		t.Fatal("nil table")
	}
	if tbl.ShapeID() != 0 {
		t.Fatalf("nil ctor shapeID=%d, want 0", tbl.ShapeID())
	}
}
