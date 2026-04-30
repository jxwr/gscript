package runtime

import "testing"

func TestLazyRecursiveTableRawGetAndIdentity(t *testing.T) {
	ctor := NewSmallTableCtor2("left", "right")
	root := NewLazyRecursiveTable(&ctor, 2)

	left1 := root.RawGetString("left")
	left2 := root.RawGetString("left")
	if !left1.IsTable() || !left2.IsTable() || left1.Table() != left2.Table() {
		t.Fatalf("lazy left identity mismatch: %v %v", left1, left2)
	}
	if got := left1.Table().RawGetString("left"); !got.IsTable() {
		t.Fatalf("lazy child left = %v, want table", got)
	}
	if got := left1.Table().RawGetString("right"); !got.IsTable() {
		t.Fatalf("lazy child right = %v, want table", got)
	}
	leaf := left1.Table().RawGetString("left").Table()
	if got := leaf.RawGetString("left"); !got.IsNil() {
		t.Fatalf("lazy leaf left = %v, want nil", got)
	}
}

func TestLazyRecursiveTableMaterializesBeforeMutationAndIteration(t *testing.T) {
	ctor := NewSmallTableCtor2("left", "right")
	root := NewLazyRecursiveTable(&ctor, 1)

	root.RawSetString("extra", IntValue(42))
	if _, _, _, ok := root.LazyRecursiveTableInfo(); ok {
		t.Fatal("mutation should materialize lazy table")
	}
	if got := root.RawGetString("left"); !got.IsTable() {
		t.Fatalf("materialized left = %v, want table", got)
	}
	if got := root.RawGetString("right"); !got.IsTable() {
		t.Fatalf("materialized right = %v, want table", got)
	}
	if got := root.RawGetString("extra"); !got.IsInt() || got.Int() != 42 {
		t.Fatalf("extra = %v, want 42", got)
	}

	iter := NewLazyRecursiveTable(&ctor, 1)
	key, val, ok := iter.Next(NilValue())
	if !ok || !key.IsString() || !val.IsTable() {
		t.Fatalf("first lazy Next = (%v, %v, %v), want string/table/true", key, val, ok)
	}
	if _, _, _, stillLazy := iter.LazyRecursiveTableInfo(); stillLazy {
		t.Fatal("iteration should materialize lazy table")
	}
}
