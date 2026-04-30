//go:build darwin && arm64

package methodjit

import "testing"

func TestValueReprMirrorsLegacyMaps(t *testing.T) {
	ec := &emitContext{
		valueReprs:      make(map[int]valueRepr),
		rawIntRegs:      make(map[int]bool),
		rawTablePtrRegs: make(map[int]bool),
		activeFPRegs:    make(map[int]bool),
	}

	if got := ec.valueReprOf(1); got != valueReprBoxed {
		t.Fatalf("default repr=%s, want boxed", got)
	}

	ec.setValueRepr(1, valueReprRawInt)
	if got := ec.valueReprOf(1); got != valueReprRawInt {
		t.Fatalf("raw int repr=%s", got)
	}
	if !ec.rawIntRegs[1] || ec.rawTablePtrRegs[1] {
		t.Fatalf("raw int mirrors wrong: rawInt=%v rawTable=%v", ec.rawIntRegs[1], ec.rawTablePtrRegs[1])
	}

	ec.setValueRepr(1, valueReprRawTablePtr)
	if got := ec.valueReprOf(1); got != valueReprRawTablePtr {
		t.Fatalf("raw table repr=%s", got)
	}
	if ec.rawIntRegs[1] || !ec.rawTablePtrRegs[1] {
		t.Fatalf("raw table mirrors wrong: rawInt=%v rawTable=%v", ec.rawIntRegs[1], ec.rawTablePtrRegs[1])
	}

	ec.setValueRepr(1, valueReprBoxed)
	if got := ec.valueReprOf(1); got != valueReprBoxed {
		t.Fatalf("boxed repr=%s", got)
	}
	if ec.rawIntRegs[1] || ec.rawTablePtrRegs[1] {
		t.Fatalf("boxed should clear legacy mirrors")
	}
}

func TestValueReprLegacyDirectWritesRemainAuthoritative(t *testing.T) {
	ec := &emitContext{
		valueReprs:      make(map[int]valueRepr),
		rawIntRegs:      make(map[int]bool),
		rawTablePtrRegs: make(map[int]bool),
		activeFPRegs:    make(map[int]bool),
	}

	ec.setValueRepr(2, valueReprRawInt)
	delete(ec.rawIntRegs, 2)
	if got := ec.valueReprOf(2); got != valueReprBoxed {
		t.Fatalf("legacy raw-int delete repr=%s, want boxed", got)
	}

	ec.rawTablePtrRegs[3] = true
	if got := ec.valueReprOf(3); got != valueReprRawTablePtr {
		t.Fatalf("legacy raw-table write repr=%s", got)
	}

	ec.activeFPRegs[4] = true
	if got := ec.valueReprOf(4); got != valueReprRawFloat {
		t.Fatalf("active FPR repr=%s", got)
	}
}

func TestExitResumeCheckLiveSlotsTracksRawTablePtr(t *testing.T) {
	ec := &emitContext{
		alloc: &RegAllocation{ValueRegs: map[int]PhysReg{
			7: {Reg: 20},
		}},
		slotMap:         map[int]int{7: 3},
		activeRegs:      map[int]bool{7: true},
		activeFPRegs:    make(map[int]bool),
		valueReprs:      make(map[int]valueRepr),
		rawIntRegs:      make(map[int]bool),
		rawTablePtrRegs: make(map[int]bool),
		exitResumeCheck: newExitResumeCheckMetadata(),
	}
	ec.setValueRepr(7, valueReprRawTablePtr)

	live := ec.exitResumeCheckLiveSlots(ec.activeRegs, nil)
	if len(live) != 1 {
		t.Fatalf("live slots=%d, want 1", len(live))
	}
	if live[0].Repr != valueReprRawTablePtr || !live[0].RawTablePtr {
		t.Fatalf("live repr=%s rawTable=%v", live[0].Repr, live[0].RawTablePtr)
	}
	if live[0].RawInt || live[0].RawFloat {
		t.Fatalf("raw table ptr slot should not be tagged as int/float: %+v", live[0])
	}
}
