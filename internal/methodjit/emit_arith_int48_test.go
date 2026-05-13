//go:build darwin && arm64

package methodjit

import "testing"

func TestEmitContextInt48SafeUsesKnownRanges(t *testing.T) {
	ec := &emitContext{fn: &Function{
		Int48Safe: map[int]bool{1: true},
		IntRanges: map[int]intRange{
			2: pointRange(1),
			3: {min: MinInt48 - 1, max: 0, known: true},
		},
	}}

	if !ec.int48Safe(1) {
		t.Fatal("explicit Int48Safe fact should be honored")
	}
	if !ec.int48Safe(2) {
		t.Fatal("known int range fitting int48 should be safe")
	}
	if ec.int48Safe(3) {
		t.Fatal("known int range outside int48 should not be safe")
	}
	if ec.int48Safe(4) {
		t.Fatal("missing range should not be safe")
	}
}
