package jit

import (
	"fmt"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestDeoptMetadata_New(t *testing.T) {
	dm := NewDeoptMetadata()

	if dm == nil {
		t.Fatal("NewDeoptMetadata returned nil")
	}

	if dm.Guards == nil {
		t.Error("Guards map should be initialized")
	}

	if dm.Bailouts == nil {
		t.Error("Bailouts map should be initialized")
	}

	if dm.NextBailoutID != 0 {
		t.Errorf("NextBailoutID should start at 0, got %d", dm.NextBailoutID)
	}
}

func TestDeoptMetadata_AddGuard(t *testing.T) {
	dm := NewDeoptMetadata()

	// Test Int32 guard
	loc := CodeLoc{BytecodePC: 42}
	bailoutID1 := dm.AddGuard(GuardTypeInt32, 5, nil, loc)

	if bailoutID1 != 0 {
		t.Errorf("First bailout ID should be 0, got %d", bailoutID1)
	}

	if dm.NextBailoutID != 1 {
		t.Errorf("NextBailoutID should be 1 after first guard, got %d", dm.NextBailoutID)
	}

	guard, ok := dm.Guards[0]
	if !ok {
		t.Fatal("Guard not found in registry")
	}

	if guard.Type != GuardTypeInt32 {
		t.Errorf("Guard type should be Int32, got %v", guard.Type)
	}

	if guard.Value != 5 {
		t.Errorf("Guard value should be 5, got %d", guard.Value)
	}

	if guard.BailoutID != 0 {
		t.Errorf("Guard bailout ID should be 0, got %d", guard.BailoutID)
	}

	// Test Float64 guard
	bailoutID2 := dm.AddGuard(GuardTypeFloat64, 10, nil, loc)

	if bailoutID2 != 1 {
		t.Errorf("Second bailout ID should be 1, got %d", bailoutID2)
	}

	if dm.NextBailoutID != 2 {
		t.Errorf("NextBailoutID should be 2 after second guard, got %d", dm.NextBailoutID)
	}

	guard, ok = dm.Guards[1]
	if !ok {
		t.Fatal("Second guard not found in registry")
	}

	if guard.Type != GuardTypeFloat64 {
		t.Errorf("Guard type should be Float64, got %v", guard.Type)
	}

	// Test TableShape guard with expected value
	bailoutID3 := dm.AddGuard(GuardTypeTableShape, 15, uint32(12345), loc)

	if bailoutID3 != 2 {
		t.Errorf("Third bailout ID should be 2, got %d", bailoutID3)
	}

	guard, ok = dm.Guards[2]
	if !ok {
		t.Fatal("Third guard not found in registry")
	}

	if guard.Expected != uint32(12345) {
		t.Errorf("Guard expected value should be 12345, got %v", guard.Expected)
	}
}

func TestDeoptMetadata_GetBailout(t *testing.T) {
	dm := NewDeoptMetadata()

	loc := CodeLoc{BytecodePC: 100}
	bailoutID := dm.AddGuard(GuardTypeInt32, 0, nil, loc)

	bailout := dm.GetBailout(bailoutID)
	if bailout == nil {
		t.Fatal("GetBailout returned nil")
	}

	if bailout.BailoutID != bailoutID {
		t.Errorf("BailoutID mismatch: %d vs %d", bailout.BailoutID, bailoutID)
	}

	if bailout.BytecodePC != 100 {
		t.Errorf("BytecodePC mismatch: %d vs %d", bailout.BytecodePC, 100)
	}

	// Test unknown bailout
	unknown := dm.GetBailout(999)
	if unknown != nil {
		t.Error("GetBailout should return nil for unknown ID")
	}
}

func TestLiveValueInfo_String(t *testing.T) {
	lv := LiveValueInfo{
		SSARef:        5,
		InterpreterSlot: 10,
		ValueType:      runtime.TypeInt,
		NeedsBox:      false,
	}

	str := lv.String()
	if str != fmt.Sprintf("ssa=5, slot=10, type=%v", runtime.TypeInt) {
		t.Errorf("Unexpected String(): %s", str)
	}

	lv2 := LiveValueInfo{
		SSARef:        6,
		InterpreterSlot: 11,
		ValueType:      runtime.TypeFloat,
		NeedsBox:      true,
	}

	str2 := lv2.String()
	if str2 != fmt.Sprintf("ssa=6, slot=11, type=%v, needsBox", runtime.TypeFloat) {
		t.Errorf("Unexpected String(): %s", str2)
	}
}

func TestRegMapping_String(t *testing.T) {
	rm := RegMapping{
		TraceReg:   3,
		InterpSlot: 7,
	}

	str := rm.String()
	if str != "reg3->slot7" {
		t.Errorf("Unexpected String(): %s", str)
	}
}

func TestGuardType_String(t *testing.T) {
	tests := []struct {
		gt     GuardType
		expect  string
	}{
		{GuardTypeInt32, "Int32"},
		{GuardTypeFloat64, "Float64"},
		{GuardTypeNotNil, "NotNil"},
		{GuardTypeBounds, "Bounds"},
		{GuardTypeTableShape, "TableShape"},
		{GuardTypeString, "String"},
		{GuardTypeArrayKind, "ArrayKind"},
	}

	for _, tt := range tests {
		got := tt.gt.String()
		if got != tt.expect {
			t.Errorf("GuardType.String() = %s, want %s", got, tt.expect)
		}
	}
}

func TestBailoutInfo_String(t *testing.T) {
	bi := &BailoutInfo{
		BailoutID:   5,
		BytecodePC: 100,
		LiveValues:   []LiveValueInfo{},
		SnapshotIdx:  -1,
		FrameMapping: []RegMapping{},
	}

	str := bi.String()
	if len(str) < 10 {
		t.Errorf("BailoutInfo.String() too short: %s", str)
	}

	if str[8:12] != "id=5" {
		t.Errorf("BailoutInfo.String() doesn't contain id=5: %s", str)
	}
}

func TestDeoptStats_RecordGuardFailure(t *testing.T) {
	// Reset stats
	globalDeoptStats = &DeoptStats{
		GuardFails: make(map[GuardType]int),
	}

	RecordGuardFailure(GuardTypeInt32)
	RecordGuardFailure(GuardTypeInt32)
	RecordGuardFailure(GuardTypeFloat64)

	if globalDeoptStats.TotalDeopts != 3 {
		t.Errorf("Total deopts should be 3, got %d", globalDeoptStats.TotalDeopts)
	}

	if globalDeoptStats.GuardFails[GuardTypeInt32] != 2 {
		t.Errorf("Int32 guard fails should be 2, got %d", globalDeoptStats.GuardFails[GuardTypeInt32])
	}

	if globalDeoptStats.GuardFails[GuardTypeFloat64] != 1 {
		t.Errorf("Float64 guard fails should be 1, got %d", globalDeoptStats.GuardFails[GuardTypeFloat64])
	}
}

func TestGuardTypeNone_String(t *testing.T) {
	gt := GuardTypeNone
	got := gt.String()
	if got != "Unknown" {
		t.Errorf("GuardTypeNone.String() = %s, want Unknown", got)
	}
}

func TestCodeLoc_String(t *testing.T) {
	// Test without FuncProto
	loc := CodeLoc{
		BytecodePC: 75,
	}

	str := loc.String()
	expected := "pc=75"
	if str != expected {
		t.Errorf("CodeLoc.String() = %s, want %s", str, expected)
	}
}
