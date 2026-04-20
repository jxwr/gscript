//go:build darwin && arm64

package methodjit

import (
	"testing"
)

// TestR158_Identify_Simple verifies the MVP predicate on
// `new_vec3(x, y, z) { return {x:x, y:y, z:z} }`. The returned
// table IS used by OpReturn, so it MUST escape (not be identified
// as virtual). This is a correctness gate: we do NOT optimize away
// an allocation that's actually returned.
func TestR158_Identify_ReturnedTable_Escapes(t *testing.T) {
	src := `
func new_vec3(x, y, z) {
    return {x: x, y: y, z: z}
}
result := new_vec3(1, 2, 3)
`
	top := compileProto(t, src)
	inner := findProtoByName(top, "new_vec3")
	if inner == nil {
		t.Fatal("new_vec3 proto missing")
	}
	inner.EnsureFeedback()
	fn := BuildGraph(inner)
	virtuals := identifyVirtualAllocs(fn)
	if len(virtuals) != 0 {
		t.Fatalf("expected 0 virtual allocs (table escapes via Return); got %d", len(virtuals))
	}
}

// TestR158_Identify_ConsumedLocally verifies the MVP predicate on
// a NewTable whose fields are read back and then discarded. For
// example: `p := {x:1, y:2}; return p.x + p.y`. Here p escapes
// through uses of p.x/p.y's ADDED result via Return, but the TABLE
// itself only reaches OpGetField. This SHOULD be identified as
// virtual.
func TestR158_Identify_ConsumedLocally(t *testing.T) {
	src := `
func consume() {
    p := {x: 1, y: 2}
    return p.x + p.y
}
result := consume()
`
	top := compileProto(t, src)
	inner := findProtoByName(top, "consume")
	if inner == nil {
		t.Fatal("consume proto missing")
	}
	inner.EnsureFeedback()
	fn := BuildGraph(inner)
	virtuals := identifyVirtualAllocs(fn)
	if len(virtuals) != 1 {
		// Dump IR to diagnose
		t.Logf("IR:\n%s", Print(fn))
		t.Fatalf("expected 1 virtual alloc (table used only via GetField); got %d", len(virtuals))
	}
	for id, info := range virtuals {
		t.Logf("virtual alloc %d in block %d, %d field uses",
			id, info.blockID, len(info.fieldUses))
	}
}

// TestR158_Identify_StoredAsValue_Escapes verifies that a table
// stored as a field-value of ANOTHER table (i.e. `inner` in
// `outer.v = inner`) is NOT identified as virtual, because `inner`
// is used as Args[1] of OpSetField (the VALUE slot, not Args[0]
// self slot). The outer table IS still virtual under the MVP
// predicate: its fields are only accessed via static-key GetField/
// SetField. (Whether we can profitably rewrite a virtual that
// holds SSA references to a non-virtual table is a R159 concern.)
func TestR158_Identify_StoredAsValue_Escapes(t *testing.T) {
	src := `
func store_in_another() {
    outer := {}
    inner := {x: 1}
    outer.v = inner
    return outer.v.x
}
result := store_in_another()
`
	top := compileProto(t, src)
	inner := findProtoByName(top, "store_in_another")
	if inner == nil {
		t.Fatal("store_in_another proto missing")
	}
	inner.EnsureFeedback()
	fn := BuildGraph(inner)
	virtuals := identifyVirtualAllocs(fn)
	// Check that NO allocation whose SSA ID corresponds to the
	// INNER table appears in virtuals. The inner NewTable has
	// SetField v1.field[0] = 1 (self, OK) AND is stored as value
	// in SetField v0.field[1] = v1 (ESCAPES).
	// The outer table v0 has only GetField/SetField on itself,
	// so IT is virtual under MVP.
	var sawInnerAsVirtual, sawOuterAsVirtual bool
	for id, info := range virtuals {
		// Walk the IR to classify which is which.
		defBlock := fn.Blocks[info.blockID]
		for _, ins := range defBlock.Instrs {
			if ins.ID != id {
				continue
			}
			// Both allocations are OpNewTable; distinguish by
			// whether any SetField stores a value INTO this one
			// where the stored value is another alloc. If there's
			// no such SetField(self=id, value=anotherAlloc), it's
			// the "inner" one (inner has SetField(self=inner, v=1);
			// outer has SetField(self=outer, v=inner)).
			innerMark := true
			for _, ins2 := range defBlock.Instrs {
				if ins2.Op == OpSetField && ins2.Args[0].ID == id &&
					len(ins2.Args) >= 2 && ins2.Args[1].ID > 0 {
					// Check whether Args[1] is itself a NewTable.
					for _, ins3 := range defBlock.Instrs {
						if ins3.ID == ins2.Args[1].ID && ins3.Op == OpNewTable {
							innerMark = false
						}
					}
				}
			}
			if innerMark {
				sawInnerAsVirtual = true
			} else {
				sawOuterAsVirtual = true
			}
			break
		}
	}
	if sawInnerAsVirtual {
		t.Logf("IR:\n%s", Print(fn))
		t.Errorf("inner table was flagged virtual but it escapes via SetField Args[1]")
	}
	t.Logf("outer table was flagged virtual: %v (acceptable under MVP)", sawOuterAsVirtual)
}
