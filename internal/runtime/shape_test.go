package runtime

import "testing"

func TestGetShapeIDEmpty(t *testing.T) {
	// Empty skeys returns sentinel 0
	id := GetShapeID(nil)
	if id != 0 {
		t.Errorf("GetShapeID(nil) = %d, want 0", id)
	}
	id = GetShapeID([]string{})
	if id != 0 {
		t.Errorf("GetShapeID([]) = %d, want 0", id)
	}
}

func TestGetShapeIDDeterministic(t *testing.T) {
	keys := []string{"x", "y", "z"}
	id1 := GetShapeID(keys)
	id2 := GetShapeID(keys)
	if id1 != id2 {
		t.Errorf("GetShapeID not deterministic: %d vs %d", id1, id2)
	}
	if id1 == 0 {
		t.Errorf("GetShapeID returned sentinel 0 for non-empty keys")
	}
}

func TestGetShapeIDDistinct(t *testing.T) {
	id1 := GetShapeID([]string{"x", "y"})
	id2 := GetShapeID([]string{"a", "b"})
	if id1 == id2 {
		t.Errorf("different key sets should have different shapeIDs: both %d", id1)
	}
}

func TestGetShapeIDOrderMatters(t *testing.T) {
	id1 := GetShapeID([]string{"x", "y"})
	id2 := GetShapeID([]string{"y", "x"})
	if id1 == id2 {
		t.Errorf("different key order should have different shapeIDs: both %d", id1)
	}
}

func TestGetShapeIDSharedAcrossTables(t *testing.T) {
	// Two tables with same fields in same order should share shapeID
	t1 := NewTable()
	t1.RawSetString("x", FloatValue(1.0))
	t1.RawSetString("y", FloatValue(2.0))
	t1.RawSetString("z", FloatValue(3.0))

	t2 := NewTable()
	t2.RawSetString("x", FloatValue(10.0))
	t2.RawSetString("y", FloatValue(20.0))
	t2.RawSetString("z", FloatValue(30.0))

	if t1.ShapeID() == 0 {
		t.Error("t1 shapeID should not be 0")
	}
	if t1.ShapeID() != t2.ShapeID() {
		t.Errorf("tables with same fields should share shapeID: %d vs %d", t1.ShapeID(), t2.ShapeID())
	}
}

func TestShapeIDUpdatedOnAdd(t *testing.T) {
	tbl := NewTable()
	if tbl.ShapeID() != 0 {
		t.Errorf("empty table should have shapeID 0, got %d", tbl.ShapeID())
	}

	tbl.RawSetString("x", FloatValue(1.0))
	id1 := tbl.ShapeID()
	if id1 == 0 {
		t.Error("shapeID should not be 0 after adding a field")
	}

	tbl.RawSetString("y", FloatValue(2.0))
	id2 := tbl.ShapeID()
	if id2 == id1 {
		t.Error("shapeID should change after adding a field")
	}
}

func TestShapeIDUpdatedOnDelete(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	id1 := tbl.ShapeID()

	tbl.RawSetString("y", NilValue()) // delete y
	id2 := tbl.ShapeID()
	if id2 == id1 {
		t.Error("shapeID should change after deleting a field")
	}
}

func TestShapeIDZeroAfterSmapPromotion(t *testing.T) {
	tbl := NewTable()
	for i := 0; i <= smallFieldCap; i++ {
		tbl.RawSetString(string(rune('a'+i)), IntValue(int64(i)))
	}
	if tbl.ShapeID() != 0 {
		t.Errorf("shapeID should be 0 after smap promotion, got %d", tbl.ShapeID())
	}
}

func TestShapeIDCacheHitAcrossTables(t *testing.T) {
	// Simulate nbody pattern: same cache used across tables with same shape
	bodies := make([]*Table, 5)
	for i := range bodies {
		b := NewTable()
		b.RawSetString("x", FloatValue(float64(i)))
		b.RawSetString("y", FloatValue(float64(i) * 2))
		b.RawSetString("mass", FloatValue(float64(i+1) * 100))
		bodies[i] = b
	}

	var cache FieldCacheEntry

	// First access populates cache
	v := bodies[0].RawGetStringCached("x", &cache)
	if v.Float() != 0.0 {
		t.Errorf("expected 0.0, got %v", v.Float())
	}

	// Subsequent accesses on different tables with same shape should hit cache
	for i, body := range bodies {
		v = body.RawGetStringCached("x", &cache)
		if v.Float() != float64(i) {
			t.Errorf("body[%d].x = %v, want %v", i, v.Float(), float64(i))
		}
	}
}
