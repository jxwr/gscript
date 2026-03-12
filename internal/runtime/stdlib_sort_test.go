package runtime

import (
	"testing"
)

// sortInterp creates an interpreter with the sort library registered.
func sortInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	interp := New()
	interp.globals.Define("sort", TableValue(buildSortLib(interp)))
	tokens, err := lexerNew(src)
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parserNew(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	return interp
}

// ==================================================================
// sort.asc tests
// ==================================================================

func TestSortAsc(t *testing.T) {
	interp := sortInterp(t, `
		arr := {5, 3, 1, 4, 2}
		sort.asc(arr)
		a := arr[1]
		b := arr[2]
		c := arr[3]
		d := arr[4]
		e := arr[5]
	`)
	if interp.GetGlobal("a").Int() != 1 { t.Error("expected 1") }
	if interp.GetGlobal("b").Int() != 2 { t.Error("expected 2") }
	if interp.GetGlobal("c").Int() != 3 { t.Error("expected 3") }
	if interp.GetGlobal("d").Int() != 4 { t.Error("expected 4") }
	if interp.GetGlobal("e").Int() != 5 { t.Error("expected 5") }
}

func TestSortAscStrings(t *testing.T) {
	interp := sortInterp(t, `
		arr := {"cherry", "apple", "banana"}
		sort.asc(arr)
		a := arr[1]
		b := arr[2]
		c := arr[3]
	`)
	if interp.GetGlobal("a").Str() != "apple" { t.Error("expected apple") }
	if interp.GetGlobal("b").Str() != "banana" { t.Error("expected banana") }
	if interp.GetGlobal("c").Str() != "cherry" { t.Error("expected cherry") }
}

func TestSortAscReturnsTable(t *testing.T) {
	interp := sortInterp(t, `
		arr := {3, 1, 2}
		result := sort.asc(arr)
		same := result == arr
	`)
	v := interp.GetGlobal("same")
	if !v.IsBool() || !v.Bool() {
		t.Error("sort.asc should return the same table")
	}
}

// ==================================================================
// sort.desc tests
// ==================================================================

func TestSortDesc(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 5, 3, 2, 4}
		sort.desc(arr)
		a := arr[1]
		b := arr[2]
		c := arr[5]
	`)
	if interp.GetGlobal("a").Int() != 5 { t.Error("expected 5") }
	if interp.GetGlobal("b").Int() != 4 { t.Error("expected 4") }
	if interp.GetGlobal("c").Int() != 1 { t.Error("expected 1") }
}

// ==================================================================
// sort.by tests
// ==================================================================

func TestSortBy(t *testing.T) {
	interp := sortInterp(t, `
		arr := {5, 3, 1, 4, 2}
		sort.by(arr, func(a, b) { return a > b })
		first := arr[1]
		last := arr[5]
	`)
	if interp.GetGlobal("first").Int() != 5 { t.Error("expected 5") }
	if interp.GetGlobal("last").Int() != 1 { t.Error("expected 1") }
}

// ==================================================================
// sort.byKey tests
// ==================================================================

func TestSortByKey(t *testing.T) {
	interp := sortInterp(t, `
		arr := {"banana", "fig", "apple", "cherry"}
		sort.byKey(arr, func(s) { return #s })
		first := arr[1]
		last := arr[4]
	`)
	// Sorted by string length: fig(3), apple(5), banana(6), cherry(6)
	if interp.GetGlobal("first").Str() != "fig" { t.Errorf("expected fig, got %s", interp.GetGlobal("first").Str()) }
}

// ==================================================================
// sort.isSorted tests
// ==================================================================

func TestSortIsSortedTrue(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 2, 3, 4, 5}
		result := sort.isSorted(arr)
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("expected true for sorted array")
	}
}

func TestSortIsSortedFalse(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 3, 2, 4, 5}
		result := sort.isSorted(arr)
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || v.Bool() {
		t.Error("expected false for unsorted array")
	}
}

func TestSortIsSortedEmpty(t *testing.T) {
	interp := sortInterp(t, `
		arr := {}
		result := sort.isSorted(arr)
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("expected true for empty array")
	}
}

func TestSortIsSortedSingle(t *testing.T) {
	interp := sortInterp(t, `
		arr := {42}
		result := sort.isSorted(arr)
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("expected true for single element")
	}
}

// ==================================================================
// sort.reverse tests
// ==================================================================

func TestSortReverse(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 2, 3, 4, 5}
		sort.reverse(arr)
		a := arr[1]
		b := arr[5]
	`)
	if interp.GetGlobal("a").Int() != 5 { t.Error("expected 5") }
	if interp.GetGlobal("b").Int() != 1 { t.Error("expected 1") }
}

func TestSortReverseOdd(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 2, 3}
		sort.reverse(arr)
		a := arr[1]
		b := arr[2]
		c := arr[3]
	`)
	if interp.GetGlobal("a").Int() != 3 { t.Error("expected 3") }
	if interp.GetGlobal("b").Int() != 2 { t.Error("expected 2") }
	if interp.GetGlobal("c").Int() != 1 { t.Error("expected 1") }
}

// ==================================================================
// sort.bsearch tests
// ==================================================================

func TestSortBsearchFound(t *testing.T) {
	interp := sortInterp(t, `
		arr := {10, 20, 30, 40, 50}
		result := sort.bsearch(arr, 30)
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected index 3, got %v", v)
	}
}

func TestSortBsearchNotFound(t *testing.T) {
	interp := sortInterp(t, `
		arr := {10, 20, 30, 40, 50}
		result := sort.bsearch(arr, 25)
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("expected nil for not found, got %v", v)
	}
}

func TestSortBsearchFirst(t *testing.T) {
	interp := sortInterp(t, `
		arr := {10, 20, 30}
		result := sort.bsearch(arr, 10)
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected index 1, got %v", v)
	}
}

func TestSortBsearchLast(t *testing.T) {
	interp := sortInterp(t, `
		arr := {10, 20, 30}
		result := sort.bsearch(arr, 30)
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected index 3, got %v", v)
	}
}

// ==================================================================
// sort.unique tests
// ==================================================================

func TestSortUnique(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 1, 2, 2, 3, 3, 3, 4}
		result := sort.unique(arr)
		count := #result
		a := result[1]
		d := result[4]
	`)
	if interp.GetGlobal("count").Int() != 4 { t.Errorf("expected 4 unique, got %d", interp.GetGlobal("count").Int()) }
	if interp.GetGlobal("a").Int() != 1 { t.Error("expected 1") }
	if interp.GetGlobal("d").Int() != 4 { t.Error("expected 4") }
}

func TestSortUniqueEmpty(t *testing.T) {
	interp := sortInterp(t, `
		arr := {}
		result := sort.unique(arr)
		count := #result
	`)
	if interp.GetGlobal("count").Int() != 0 { t.Error("expected 0") }
}

func TestSortUniqueNoDuplicates(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 2, 3}
		result := sort.unique(arr)
		count := #result
	`)
	if interp.GetGlobal("count").Int() != 3 { t.Error("expected 3") }
}

// ==================================================================
// sort.partition tests
// ==================================================================

func TestSortPartition(t *testing.T) {
	interp := sortInterp(t, `
		arr := {1, 2, 3, 4, 5, 6}
		evens, odds := sort.partition(arr, func(v) { return v % 2 == 0 })
		evenCount := #evens
		oddCount := #odds
	`)
	if interp.GetGlobal("evenCount").Int() != 3 { t.Errorf("expected 3 evens, got %d", interp.GetGlobal("evenCount").Int()) }
	if interp.GetGlobal("oddCount").Int() != 3 { t.Errorf("expected 3 odds, got %d", interp.GetGlobal("oddCount").Int()) }
}

// ==================================================================
// sort.min / sort.max tests
// ==================================================================

func TestSortMin(t *testing.T) {
	interp := sortInterp(t, `
		arr := {5, 3, 1, 4, 2}
		result := sort.min(arr)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 1 { t.Errorf("expected 1, got %d", v.Int()) }
}

func TestSortMax(t *testing.T) {
	interp := sortInterp(t, `
		arr := {5, 3, 1, 4, 2}
		result := sort.max(arr)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 5 { t.Errorf("expected 5, got %d", v.Int()) }
}

func TestSortMinByKey(t *testing.T) {
	interp := sortInterp(t, `
		arr := {"banana", "fig", "apple"}
		result := sort.min(arr, func(s) { return #s })
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "fig" { t.Errorf("expected fig, got %s", v.Str()) }
}

func TestSortMaxByKey(t *testing.T) {
	interp := sortInterp(t, `
		arr := {"banana", "fig", "apple"}
		result := sort.max(arr, func(s) { return #s })
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "banana" { t.Errorf("expected banana, got %s", v.Str()) }
}

func TestSortMinEmpty(t *testing.T) {
	interp := sortInterp(t, `
		arr := {}
		result := sort.min(arr)
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() { t.Error("expected nil for empty") }
}

func TestSortMaxEmpty(t *testing.T) {
	interp := sortInterp(t, `
		arr := {}
		result := sort.max(arr)
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() { t.Error("expected nil for empty") }
}
