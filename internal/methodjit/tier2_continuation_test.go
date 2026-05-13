//go:build darwin && arm64

package methodjit

import "testing"

func TestBuildTier2ContinuationsIndexesByStableSourceKey(t *testing.T) {
	sites := map[int]ExitSiteMeta{
		10: {PC: 7, Op: "Call"},
		11: {PC: 8, Op: "GetField"},
		12: {PC: 9, Op: "Add"},
		13: {PC: 10, Op: "FieldCallFloor"},
	}
	resumes := []deferredResume{
		{instrID: 10},
		{instrID: 11, numericPass: true},
		{instrID: 12},
		{instrID: 13},
	}
	offsets := map[string]int{
		callExitResumeLabelForPass(10, false): 100,
		callExitResumeLabelForPass(11, true):  200,
		callExitResumeLabelForPass(12, false): 300,
		callExitResumeLabelForPass(13, false): 400,
	}
	conts := buildTier2Continuations(sites, resumes, func(label string) int {
		if off, ok := offsets[label]; ok {
			return off
		}
		return -1
	})

	cf := &CompiledFunction{Continuations: conts}
	if off, ok := cf.continuationOffset(Tier2ContinuationKey{PC: 7, Kind: Tier2ContinuationCall}); !ok || off != 100 {
		t.Fatalf("call continuation = (%d,%v), want (100,true)", off, ok)
	}
	if off, ok := cf.continuationOffset(Tier2ContinuationKey{PC: 8, Kind: Tier2ContinuationTable, NumericPass: true}); !ok || off != 200 {
		t.Fatalf("numeric table continuation = (%d,%v), want (200,true)", off, ok)
	}
	if off, ok := cf.continuationOffset(Tier2ContinuationKey{PC: 9, Kind: Tier2ContinuationOp}); !ok || off != 300 {
		t.Fatalf("op continuation = (%d,%v), want (300,true)", off, ok)
	}
	if off, ok := cf.continuationOffset(Tier2ContinuationKey{PC: 10, Kind: Tier2ContinuationCall}); !ok || off != 400 {
		t.Fatalf("field call floor continuation = (%d,%v), want (400,true)", off, ok)
	}
}

func TestBuildTier2ContinuationsMarksDuplicateStableKeysAmbiguous(t *testing.T) {
	sites := map[int]ExitSiteMeta{
		10: {PC: 7, Op: "GetField"},
		11: {PC: 7, Op: "SetField"},
	}
	resumes := []deferredResume{{instrID: 10}, {instrID: 11}}
	offsets := map[string]int{
		callExitResumeLabelForPass(10, false): 100,
		callExitResumeLabelForPass(11, false): 120,
	}
	conts := buildTier2Continuations(sites, resumes, func(label string) int {
		if off, ok := offsets[label]; ok {
			return off
		}
		return -1
	})
	key := Tier2ContinuationKey{PC: 7, Kind: Tier2ContinuationTable}
	if cont := conts[key]; !cont.Ambiguous {
		t.Fatalf("duplicate source key should be ambiguous: %+v", cont)
	}
	cf := &CompiledFunction{Continuations: conts}
	if off, ok := cf.continuationOffset(key); ok {
		t.Fatalf("ambiguous continuation should not resolve, got (%d,true)", off)
	}
}
