package runtime

import "testing"

func TestShapeMutationProfileRecordsOverwriteAndDelete(t *testing.T) {
	base := "shape_mutation_profile_overwrite_delete"
	tbl := NewTable()
	tbl.RawSetString(base+"_a", IntValue(1))
	tbl.RawSetString(base+"_b", IntValue(2))

	shapeID := tbl.shapeID
	if shapeID == 0 {
		t.Fatal("expected shaped table")
	}
	if got := ShapeMutationCount(shapeID); got != 0 {
		t.Fatalf("append-only construction should not mark mutation, got %d", got)
	}

	tbl.RawSetString(base+"_a", IntValue(3))
	if got := ShapeMutationCount(shapeID); got == 0 {
		t.Fatal("expected overwrite to mark shape mutation")
	}

	afterOverwrite := ShapeMutationCount(shapeID)
	tbl.RawSetString(base+"_b", NilValue())
	if got := ShapeMutationCount(shapeID); got <= afterOverwrite {
		t.Fatalf("expected delete to advance mutation count, before=%d after=%d", afterOverwrite, got)
	}
}

func TestShapeMutationProfileTracksFieldEpochsIndependently(t *testing.T) {
	base := "shape_mutation_profile_field_epochs"
	tbl := NewTable()
	tbl.RawSetString(base+"_method", IntValue(1))
	tbl.RawSetString(base+"_state", IntValue(2))

	shapeID := tbl.shapeID
	if shapeID == 0 {
		t.Fatal("expected shaped table")
	}
	methodBefore := ShapeFieldMutationCount(shapeID, 0)
	stateBefore := ShapeFieldMutationCount(shapeID, 1)
	tbl.RawSetString(base+"_state", IntValue(3))
	if got := ShapeFieldMutationCount(shapeID, 0); got != methodBefore {
		t.Fatalf("state overwrite changed method field epoch, before=%d after=%d", methodBefore, got)
	}
	if got := ShapeFieldMutationCount(shapeID, 1); got <= stateBefore {
		t.Fatalf("expected state field epoch to advance, before=%d after=%d", stateBefore, got)
	}

	methodBefore = ShapeFieldMutationCount(shapeID, 0)
	tbl.RawSetString(base+"_method", IntValue(4))
	if got := ShapeFieldMutationCount(shapeID, 0); got <= methodBefore {
		t.Fatalf("expected method field epoch to advance, before=%d after=%d", methodBefore, got)
	}
}

func TestShapeMutationProfileRecordsCachedAndDynamicOverwrite(t *testing.T) {
	base := "shape_mutation_profile_cached_dynamic"
	tbl := NewTable()
	staticCache := FieldCacheEntry{}
	tbl.RawSetStringCached(base+"_a", IntValue(1), &staticCache)
	tbl.RawSetStringCached(base+"_b", IntValue(2), &staticCache)

	shapeID := tbl.shapeID
	if shapeID == 0 {
		t.Fatal("expected shaped table")
	}
	before := ShapeMutationCount(shapeID)
	tbl.RawSetStringCached(base+"_a", IntValue(3), &FieldCacheEntry{ShapeID: shapeID, FieldIdx: 0})
	if got := ShapeMutationCount(shapeID); got <= before {
		t.Fatalf("expected cached overwrite to mark mutation, before=%d after=%d", before, got)
	}

	tbl = NewTable()
	dynKey := base + "_dyn"
	tbl.RawSetString(dynKey, IntValue(1))
	shapeID = tbl.shapeID
	before = ShapeMutationCount(shapeID)
	dynCache := make([]TableStringKeyCacheEntry, 2)
	tbl.RawGetStringDynamicCached(dynKey, dynCache)
	tbl.RawSetStringDynamicCached(dynKey, IntValue(4), dynCache)
	if got := ShapeMutationCount(shapeID); got <= before {
		t.Fatalf("expected dynamic cached overwrite to mark mutation, before=%d after=%d", before, got)
	}
}

func TestShapeMutationProfileRecordsSmallFieldPromotion(t *testing.T) {
	base := "shape_mutation_profile_promotion"
	tbl := NewTable()
	for i := 0; i < SmallFieldCap; i++ {
		tbl.RawSetString(base+string(rune('a'+i)), IntValue(int64(i)))
	}

	shapeID := tbl.shapeID
	if shapeID == 0 {
		t.Fatal("expected shaped table before promotion")
	}
	before := ShapeMutationCount(shapeID)
	tbl.RawSetString(base+"_overflow", IntValue(99))
	if got := ShapeMutationCount(shapeID); got <= before {
		t.Fatalf("expected small-field promotion to mark old shape mutation, before=%d after=%d", before, got)
	}
}
