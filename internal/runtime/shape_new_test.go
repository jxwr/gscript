package runtime

import (
	"testing"
)

func TestShape_ShapeSharing(t *testing.T) {
	// Multiple tables with same field order should share the same Shape
	t1 := NewTable()
	t1.RawSetString("x", IntValue(10))
	t1.RawSetString("y", IntValue(20))

	t2 := NewTable()
	t2.RawSetString("x", IntValue(30))
	t2.RawSetString("y", IntValue(40))

	// Both should have the same Shape (same field order)
	if t1.shape == nil {
		t.Fatal("t1 should have a shape")
	}
	if t2.shape == nil {
		t.Fatal("t2 should have a shape")
	}
	if t1.shape.ID != t2.shape.ID {
		t.Errorf("shapes should have same ID: %d vs %d", t1.shape.ID, t2.shape.ID)
	}
	for i, k := range t1.shape.FieldKeys {
		if k != t2.shape.FieldKeys[i] {
			t.Errorf("shapes should have same field keys at %d: %s vs %s", i, k, t2.shape.FieldKeys[i])
		}
	}
}

func TestShape_Transition(t *testing.T) {
	// Starting with empty table
	t1 := NewTable()
	t1.RawSetString("a", IntValue(1))

	initialShape := t1.shape
	if initialShape == nil {
		t.Fatal("should have shape after first field")
	}
	if len(initialShape.FieldKeys) != 1 || initialShape.FieldKeys[0] != "a" {
		t.Errorf("should have field 'a': %v", initialShape.FieldKeys)
	}

	// Adding second field should transition to new shape
	t1.RawSetString("b", IntValue(2))
	newShape := t1.shape
	if newShape == nil {
		t.Fatal("should have shape after second field")
	}
	if len(newShape.FieldKeys) != 2 || newShape.FieldKeys[0] != "a" || newShape.FieldKeys[1] != "b" {
		t.Errorf("should have fields 'a', 'b': %v", newShape.FieldKeys)
	}

	// Shapes should be different
	if initialShape.ID == newShape.ID {
		t.Error("shapes should have different IDs")
	}

	// Transition should be cached
	// Calling Transition again with same property should return same result
	transition1 := initialShape.Transition("b")
	transition2 := initialShape.Transition("b")
	if transition1.ID != transition2.ID {
		t.Error("transition should be cached")
	}
}

func TestShape_FieldIndex(t *testing.T) {
	// Shape.FieldMap should provide O(1) field lookup
	t1 := NewTable()
	t1.RawSetString("first", IntValue(1))
	t1.RawSetString("second", IntValue(2))
	t1.RawSetString("third", IntValue(3))

	shape := t1.shape
	if shape == nil {
		t.Fatal("should have shape")
	}

	// FieldIndex should return correct indices
	tests := []struct {
		name     string
		expected int
	}{
		{"first", 0},
		{"second", 1},
		{"third", 2},
		{"missing", -1},
	}

	for _, tt := range tests {
		got := shape.GetFieldIndex(tt.name)
		if got != tt.expected {
			t.Errorf("GetFieldIndex(%q) = %d, want %d", tt.name, got, tt.expected)
		}
	}
}

func TestShape_Deletion(t *testing.T) {
	// Deleting a field should transition to shape without that field
	t1 := NewTable()
	t1.RawSetString("a", IntValue(1))
	t1.RawSetString("b", IntValue(2))
	t1.RawSetString("c", IntValue(3))

	shapeWithC := t1.shape
	if len(shapeWithC.FieldKeys) != 3 {
		t.Fatalf("should have all three fields: %v", shapeWithC.FieldKeys)
	}

	// Delete "b"
	t1.RawSetString("b", NilValue())

	shapeWithoutC := t1.shape
	if len(shapeWithoutC.FieldKeys) != 2 {
		t.Fatalf("should have only 'a' and 'c': %v", shapeWithoutC.FieldKeys)
	}
	if shapeWithC.ID == shapeWithoutC.ID {
		t.Error("shape ID should change after deletion")
	}
}

func TestShape_LookupByID(t *testing.T) {
	// Create a shape and verify it can be looked up by ID
	t1 := NewTable()
	t1.RawSetString("x", IntValue(10))
	t1.RawSetString("y", IntValue(20))

	originalShape := t1.shape
	if originalShape == nil {
		t.Fatal("should have shape")
	}

	// Lookup by ID should return same shape
	lookupShape := LookupShapeByID(originalShape.ID)
	if lookupShape != originalShape {
		t.Error("lookup should return same shape instance")
	}

	// Invalid ID should return nil
	invalidShape := LookupShapeByID(99999)
	if invalidShape != nil {
		t.Error("invalid ID should return nil")
	}
}

func TestShape_EmptyTable(t *testing.T) {
	// Empty table should have no shape
	t1 := NewTable()
	if t1.shape != nil {
		t.Errorf("empty table should have nil shape, got %v", t1.shape)
	}
	if t1.ShapeID() != 0 {
		t.Errorf("empty table ShapeID should be 0, got %d", t1.ShapeID())
	}
}

func TestShape_ShapeIDBackwardCompat(t *testing.T) {
	// ShapeID() should return shape.ID when shape exists
	t1 := NewTable()
	t1.RawSetString("x", IntValue(10))
	t1.RawSetString("y", IntValue(20))

	if t1.shape == nil {
		t.Fatal("should have shape")
	}
	if t1.shape.ID != t1.ShapeID() {
		t.Errorf("ShapeID should match shape ID: %d vs %d", t1.shape.ID, t1.ShapeID())
	}

	// Legacy shapeID field should also be updated for compatibility
	if t1.shape.ID != t1.shapeID {
		t.Errorf("legacy shapeID should match shape ID: %d vs %d", t1.shape.ID, t1.shapeID)
	}
}
