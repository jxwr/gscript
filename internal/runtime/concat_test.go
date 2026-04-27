package runtime

import "testing"

func TestConcatValues(t *testing.T) {
	got := ConcatValues([]Value{
		StringValue("item_"),
		IntValue(42),
		StringValue("_"),
		FloatValue(3.5),
	})
	if got.Str() != "item_42_3.5" {
		t.Fatalf("ConcatValues() = %q", got.Str())
	}
}

func TestConcatValuesEmptyAndSingle(t *testing.T) {
	if got := ConcatValues(nil); got.Str() != "" {
		t.Fatalf("ConcatValues(nil) = %q", got.Str())
	}
	if got := ConcatValues([]Value{IntValue(7)}); got.Str() != "7" {
		t.Fatalf("ConcatValues(single int) = %q", got.Str())
	}
}
