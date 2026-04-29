package runtime

import "testing"

func TestReuseValueSlice1UsesProvidedBuffer(t *testing.T) {
	var storage [2]Value
	got := ReuseValueSlice1(storage[:0], IntValue(42))
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if &got[0] != &storage[0] {
		t.Fatal("ReuseValueSlice1 did not use caller storage")
	}
	if !got[0].IsInt() || got[0].Int() != 42 {
		t.Fatalf("got %v, want int 42", got[0])
	}
}

func TestReuseValueSlice1FallsBackWithoutBuffer(t *testing.T) {
	got := ReuseValueSlice1(nil, BoolValue(true))
	if len(got) != 1 || !got[0].IsBool() || !got[0].Bool() {
		t.Fatalf("got %v, want true", got)
	}
}
