//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestKnownIntAnalysis_Skeleton locks in the Task 1 contract: the stub
// returns (nil, false) for any input, so no code path downstream of
// computeKnownIntSlots can accidentally rely on non-nil info in Task 1.
//
// Task 2 replaces this test with real coverage (ack, fib, mixed-type, and
// a float-constant ineligibility case).
func TestKnownIntAnalysis_Skeleton(t *testing.T) {
	proto := &vm.FuncProto{
		NumParams: 2,
		MaxStack:  8,
		Code:      []uint32{},
	}
	info, ok := computeKnownIntSlots(proto)
	if ok {
		t.Fatalf("Task 1 stub must return ok=false, got ok=true (info=%+v)", info)
	}
	if info != nil {
		t.Fatalf("Task 1 stub must return nil info, got %+v", info)
	}
}

// TestKnownIntInfo_NilSafe locks in that the accessors are nil-safe, so
// tier1_compile.go can call knownIntAt without a guard.
func TestKnownIntInfo_NilSafe(t *testing.T) {
	var k *knownIntInfo
	if got := k.knownIntAt(0); got != 0 {
		t.Fatalf("nil knownIntInfo.knownIntAt should return 0, got %d", got)
	}
	if got := k.knownIntAt(-1); got != 0 {
		t.Fatalf("nil knownIntInfo.knownIntAt(-1) should return 0, got %d", got)
	}
	if k.isKnownIntOperand(0, 0, nil) {
		t.Fatalf("nil knownIntInfo.isKnownIntOperand should return false")
	}
}
