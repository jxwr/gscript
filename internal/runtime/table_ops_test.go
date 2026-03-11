package runtime

import (
	"testing"
)

// ==================================================================
// Table operations edge cases
// ==================================================================

// --- Table creation edge cases ---

func TestTableEmptyCreation(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		result := #t
	`, "result")
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

func TestTableSingleElement(t *testing.T) {
	v := getGlobal(t, `
		t := {42}
		result := t[1]
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestTableMixedKeysAccess(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, name: "test", 30}
		a := t[1]
		b := t[2]
		c := t[3]
		n := t.name
		l := #t
	`)
	if interp.GetGlobal("a").Int() != 10 {
		t.Errorf("expected a=10, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 20 {
		t.Errorf("expected b=20, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 30 {
		t.Errorf("expected c=30, got %v", interp.GetGlobal("c"))
	}
	if interp.GetGlobal("n").Str() != "test" {
		t.Errorf("expected n='test', got %v", interp.GetGlobal("n"))
	}
}

// --- Nested table creation ---

func TestTableNestedCreation(t *testing.T) {
	v := getGlobal(t, `
		t := {a: {b: {c: 42}}}
		result := t.a.b.c
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestTableNestedArrayCreation(t *testing.T) {
	v := getGlobal(t, `
		t := {{1, 2}, {3, 4}, {5, 6}}
		result := t[2][1]
	`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}

func TestTableNestedMixedCreation(t *testing.T) {
	v := getGlobal(t, `
		t := {data: {1, 2, 3}, info: {name: "test"}}
		result := t.data[2] + #t.data
	`, "result")
	// t.data[2] = 2, #t.data = 3, result = 5
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

// --- Table mutation ---

func TestTableAddNewField(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		t.x = 10
		t.y = 20
		t.z = 30
		result := t.x + t.y + t.z
	`, "result")
	if !v.IsInt() || v.Int() != 60 {
		t.Errorf("expected 60, got %v", v)
	}
}

func TestTableAppendNumericKeys(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		for i := 1; i <= 5; i++ {
			t[i] = i * 10
		}
		result := t[3] + #t
	`, "result")
	// t[3] = 30, #t = 5, result = 35
	if !v.IsInt() || v.Int() != 35 {
		t.Errorf("expected 35, got %v", v)
	}
}

func TestTableOverwriteField(t *testing.T) {
	v := getGlobal(t, `
		t := {x: 10}
		t.x = 20
		t.x = 30
		result := t.x
	`, "result")
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected 30, got %v", v)
	}
}

// --- Table as function argument (by reference) ---

func TestTableByReference(t *testing.T) {
	v := getGlobal(t, `
		func addField(tbl, key, val) {
			tbl[key] = val
		}
		t := {}
		addField(t, "x", 42)
		result := t.x
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestTableMutationInFunction(t *testing.T) {
	v := getGlobal(t, `
		func increment(tbl) {
			tbl.n = tbl.n + 1
		}
		t := {n: 0}
		increment(t)
		increment(t)
		increment(t)
		result := t.n
	`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}

// --- Table mutation in closures ---

func TestTableMutationInClosure(t *testing.T) {
	v := getGlobal(t, `
		t := {n: 0}
		inc := func() {
			t.n = t.n + 1
		}
		inc()
		inc()
		result := t.n
	`, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected 2, got %v", v)
	}
}

// --- Table length operator edge cases ---

func TestTableLengthAfterInsert(t *testing.T) {
	v := getGlobal(t, `
		t := {1, 2, 3}
		table.insert(t, 4)
		result := #t
	`, "result")
	if !v.IsInt() || v.Int() != 4 {
		t.Errorf("expected 4, got %v", v)
	}
}

func TestTableLengthAfterRemove(t *testing.T) {
	v := getGlobal(t, `
		t := {1, 2, 3, 4, 5}
		table.remove(t)
		result := #t
	`, "result")
	if !v.IsInt() || v.Int() != 4 {
		t.Errorf("expected 4, got %v", v)
	}
}

// --- Table iteration ---

func TestTableForRangeArray(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20, 30, 40}
		sum := 0
		for k, v := range t {
			sum = sum + v
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 100 {
		t.Errorf("expected 100, got %v", v)
	}
}

func TestTableForRangeDict(t *testing.T) {
	v := getGlobal(t, `
		t := {a: 1, b: 2, c: 3}
		sum := 0
		for k, v := range t {
			sum = sum + v
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected 6, got %v", v)
	}
}

func TestTableIpairsStopsAtHole(t *testing.T) {
	// ipairs iterates consecutive integer keys from 1
	interp := runProgram(t, `
		t := {10, 20, 30}
		count := 0
		for i, v := range ipairs(t) {
			count = count + 1
		}
	`)
	count := interp.GetGlobal("count")
	if count.Int() != 3 {
		t.Errorf("expected 3 iterations, got %v", count)
	}
}

// --- Table as key ---

func TestTableStringKeyVsBracket(t *testing.T) {
	interp := runProgram(t, `
		t := {}
		t.x = 10
		t["x"] = 20
		r1 := t.x
		r2 := t["x"]
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	// t.x and t["x"] should be the same
	if r1.Int() != 20 || r2.Int() != 20 {
		t.Errorf("expected both 20, got r1=%v, r2=%v", r1, r2)
	}
}

func TestTableNumericStringKeyDifference(t *testing.T) {
	interp := runProgram(t, `
		t := {}
		t[1] = "numeric"
		t["1"] = "string"
		r1 := t[1]
		r2 := t["1"]
	`)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if r1.Str() != "numeric" {
		t.Errorf("expected r1='numeric', got %v", r1)
	}
	if r2.Str() != "string" {
		t.Errorf("expected r2='string', got %v", r2)
	}
}

// --- Table nil access ---

func TestTableNilFieldReturnsNil(t *testing.T) {
	v := getGlobal(t, `
		t := {x: 10}
		result := t.y
	`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil for missing field, got %v", v)
	}
}

func TestTableNilIndexReturnsNil(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20}
		result := t[5]
	`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil for missing index, got %v", v)
	}
}

// --- table.concat edge cases ---

func TestTableConcatEmpty(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		result := table.concat(t, ",")
	`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

func TestTableConcatSingleElement(t *testing.T) {
	v := getGlobal(t, `
		t := {"hello"}
		result := table.concat(t, ",")
	`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

// --- table.sort stability test ---

func TestTableSortStrings(t *testing.T) {
	interp := runProgram(t, `
		t := {"banana", "apple", "cherry"}
		table.sort(t)
	`)
	tbl := interp.GetGlobal("t").Table()
	if tbl.RawGet(IntValue(1)).Str() != "apple" {
		t.Errorf("expected t[1]='apple', got %v", tbl.RawGet(IntValue(1)))
	}
	if tbl.RawGet(IntValue(2)).Str() != "banana" {
		t.Errorf("expected t[2]='banana', got %v", tbl.RawGet(IntValue(2)))
	}
	if tbl.RawGet(IntValue(3)).Str() != "cherry" {
		t.Errorf("expected t[3]='cherry', got %v", tbl.RawGet(IntValue(3)))
	}
}

// --- Table used as stack ---

func TestTableAsStack(t *testing.T) {
	v := getGlobal(t, `
		stack := {}
		table.insert(stack, 10)
		table.insert(stack, 20)
		table.insert(stack, 30)
		top := table.remove(stack)
		result := top + #stack
	`, "result")
	// top=30, #stack=2, result=32
	if !v.IsInt() || v.Int() != 32 {
		t.Errorf("expected 32, got %v", v)
	}
}

// --- Table with function values ---

func TestTableWithFunctions(t *testing.T) {
	v := getGlobal(t, `
		ops := {
			add: func(a, b) { return a + b },
			mul: func(a, b) { return a * b }
		}
		result := ops.add(3, 4) + ops.mul(5, 6)
	`, "result")
	if !v.IsInt() || v.Int() != 37 {
		t.Errorf("expected 37, got %v", v)
	}
}

// --- Table chained field assignment ---

func TestTableChainedFieldAssignment(t *testing.T) {
	v := getGlobal(t, `
		t := {inner: {}}
		t.inner.x = 42
		result := t.inner.x
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// --- table.unpack with range ---

func TestTableUnpackSubrange(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30, 40, 50}
		a, b, c := table.unpack(t, 2, 4)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != 20 || b.Int() != 30 || c.Int() != 40 {
		t.Errorf("expected 20,30,40, got %v,%v,%v", a, b, c)
	}
}

// --- Table identity ---

func TestTableIdentityAfterAssignment(t *testing.T) {
	v := getGlobal(t, `
		a := {x: 1}
		b := a
		b.x = 2
		result := a.x
	`, "result")
	// b is same reference as a
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected 2, got %v", v)
	}
}

func TestTableDifferentInstances(t *testing.T) {
	v := getGlobal(t, `
		a := {x: 1}
		b := {x: 1}
		result := a == b
	`, "result")
	if v.Bool() {
		t.Errorf("different tables should not be equal")
	}
}
