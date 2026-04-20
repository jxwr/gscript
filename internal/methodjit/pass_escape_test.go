//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
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

// TestR159_Rewrite_ConsumedLocally — verifies the rewrite on the
// same source as R158 TestR158_Identify_ConsumedLocally. After
// the pass, the NewTable becomes OpNop, the SetFields become
// OpNop, and every GetField is replaced by a direct SSA reference
// to the stored value.
func TestR159_Rewrite_ConsumedLocally(t *testing.T) {
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

	// Count ops pre-pass.
	preNewTable, preGetField, preSetField := 0, 0, 0
	for _, block := range fn.Blocks {
		for _, ins := range block.Instrs {
			switch ins.Op {
			case OpNewTable:
				preNewTable++
			case OpGetField:
				preGetField++
			case OpSetField:
				preSetField++
			}
		}
	}
	t.Logf("pre-pass: NewTable=%d GetField=%d SetField=%d",
		preNewTable, preGetField, preSetField)
	if preNewTable == 0 || preGetField == 0 || preSetField == 0 {
		t.Fatalf("expected non-zero table ops pre-pass; got "+
			"NewTable=%d GetField=%d SetField=%d",
			preNewTable, preGetField, preSetField)
	}

	fn2, err := EscapeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("EscapeAnalysisPass: %v", err)
	}

	// Count ops post-pass (skip OpNop).
	postNewTable, postGetField, postSetField := 0, 0, 0
	for _, block := range fn2.Blocks {
		for _, ins := range block.Instrs {
			switch ins.Op {
			case OpNewTable:
				postNewTable++
			case OpGetField:
				postGetField++
			case OpSetField:
				postSetField++
			}
		}
	}
	t.Logf("post-pass: NewTable=%d GetField=%d SetField=%d",
		postNewTable, postGetField, postSetField)
	t.Logf("post-pass IR:\n%s", Print(fn2))

	if postNewTable != 0 {
		t.Errorf("expected 0 NewTable post-pass, got %d", postNewTable)
	}
	if postGetField != 0 {
		t.Errorf("expected 0 GetField post-pass, got %d", postGetField)
	}
	if postSetField != 0 {
		t.Errorf("expected 0 SetField post-pass, got %d", postSetField)
	}
}

// TestR161_VirtualPhi_ObjectCreation — the canonical object_creation
// pattern: a loop-carried accumulator table, re-created each iter
// via inlined vec3_add → new_vec3. Both the initial table (B0) and
// the in-loop new table (B1-end) flow through a loop-header Phi.
// After R161's virtual-Phi rewrite, all 3 NewTable ops per
// iteration should be eliminated.
func TestR161_VirtualPhi_ObjectCreation(t *testing.T) {
	src := `
func new_vec3(x, y, z) {
    return {x: x, y: y, z: z}
}
func vec3_add(a, b) {
    return new_vec3(a.x + b.x, a.y + b.y, a.z + b.z)
}
func vec3_length_sq(v) {
    return v.x * v.x + v.y * v.y + v.z * v.z
}
func create_and_sum(n) {
    total := new_vec3(0.0, 0.0, 0.0)
    for i := 1; i <= n; i++ {
        v := new_vec3(1.0 * i, 2.0 * i, 3.0 * i)
        total = vec3_add(total, v)
    }
    return vec3_length_sq(total)
}
result := create_and_sum(10)
`
	compareTier2Result(t, src, "result")
}

// TestR161_VirtualPhi_PostPipelineIR — confirms that after the
// full Tier 2 pipeline, create_and_sum has ZERO NewTable, ZERO
// GetField, ZERO SetField in any block. Regression gate for EA.
func TestR161_VirtualPhi_PostPipelineIR(t *testing.T) {
	src := `
func new_vec3(x, y, z) {
    return {x: x, y: y, z: z}
}
func vec3_add(a, b) {
    return new_vec3(a.x + b.x, a.y + b.y, a.z + b.z)
}
func vec3_length_sq(v) {
    return v.x * v.x + v.y * v.y + v.z * v.z
}
func create_and_sum(n) {
    total := new_vec3(0.0, 0.0, 0.0)
    for i := 1; i <= n; i++ {
        v := new_vec3(1.0 * i, 2.0 * i, 3.0 * i)
        total = vec3_add(total, v)
    }
    return vec3_length_sq(total)
}
result := create_and_sum(10)
`
	top := compileProto(t, src)
	globals := map[string]*vm.FuncProto{}
	var collect func(*vm.FuncProto)
	collect = func(p *vm.FuncProto) {
		if p.Name != "" {
			globals[p.Name] = p
		}
		for _, sub := range p.Protos {
			collect(sub)
		}
	}
	collect(top)

	proto := findProtoByName(top, "create_and_sum")
	if proto == nil {
		t.Fatal("create_and_sum missing")
	}
	proto.EnsureFeedback()
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals: globals,
		InlineMaxSize: 500,
	})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	nt, gf, sf := 0, 0, 0
	for _, block := range fn.Blocks {
		for _, ins := range block.Instrs {
			switch ins.Op {
			case OpNewTable:
				nt++
			case OpGetField:
				gf++
			case OpSetField:
				sf++
			}
		}
	}
	if nt != 0 || gf != 0 || sf != 0 {
		t.Logf("IR:\n%s", Print(fn))
		t.Errorf("expected all table ops eliminated; got NewTable=%d GetField=%d SetField=%d", nt, gf, sf)
	}
}

// TestR159_Rewrite_ReturnedEscape — verifies the rewrite SKIPS
// allocations that escape. Running the pass on new_vec3's proto
// should leave the NewTable + SetFields intact.
func TestR159_Rewrite_ReturnedEscape(t *testing.T) {
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

	pre := 0
	for _, block := range fn.Blocks {
		for _, ins := range block.Instrs {
			if ins.Op == OpNewTable {
				pre++
			}
		}
	}

	fn2, err := EscapeAnalysisPass(fn)
	if err != nil {
		t.Fatalf("EscapeAnalysisPass: %v", err)
	}

	post := 0
	for _, block := range fn2.Blocks {
		for _, ins := range block.Instrs {
			if ins.Op == OpNewTable {
				post++
			}
		}
	}
	if post != pre {
		t.Errorf("expected escaping NewTable to be preserved: "+
			"pre=%d post=%d", pre, post)
	}
}
