//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"
)

func TestEntryHardeningCompileGuardAndNumericLabel(t *testing.T) {
	alloc := &RegAllocation{ValueRegs: map[int]PhysReg{}}

	if _, err := Compile(nil, alloc); err == nil || !strings.Contains(err.Error(), "nil function") {
		t.Fatalf("Compile(nil) error = %v, want nil function", err)
	}

	if _, err := Compile(&Function{}, alloc); err == nil || !strings.Contains(err.Error(), "nil entry") {
		t.Fatalf("Compile(nil entry) error = %v, want nil entry", err)
	}

	entry := &Block{ID: 7}
	other := &Block{ID: 0}
	fn := &Function{Entry: entry, Blocks: []*Block{other}}
	if _, err := Compile(fn, alloc); err == nil || !strings.Contains(err.Error(), "missing from block list") {
		t.Fatalf("Compile(missing entry) error = %v, want missing entry", err)
	}

	ec := &emitContext{fn: &Function{Entry: entry}}
	if label, ok := ec.entryBlockLabelOK(); !ok || label != "B7" {
		t.Fatalf("normal entry label = %q, %v; want B7, true", label, ok)
	}
	ec.numericMode = true
	if label, ok := ec.entryBlockLabelOK(); !ok || label != "num_B7" {
		t.Fatalf("numeric entry label = %q, %v; want num_B7, true", label, ok)
	}
	if label, ok := (&emitContext{}).entryBlockLabelOK(); ok || label != "" {
		t.Fatalf("nil entry label = %q, %v; want empty, false", label, ok)
	}
}
