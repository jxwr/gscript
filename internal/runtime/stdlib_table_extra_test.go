package runtime

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

func TestTableKeys(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		k := table.keys(t)
	`)
	keys := interp.GetGlobal("k").Table()
	if keys.Length() != 3 {
		t.Errorf("expected 3 keys, got %d", keys.Length())
	}
}

func TestTableValues(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		v := table.values(t)
	`)
	vals := interp.GetGlobal("v").Table()
	if vals.Length() != 3 {
		t.Errorf("expected 3 values, got %d", vals.Length())
	}
}

func TestTableContains(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		a := table.contains(t, 20)
		b := table.contains(t, 99)
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for 20")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for 99")
	}
}

func TestTableIndexOf(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		a := table.indexOf(t, 20)
		b := table.indexOf(t, 99)
	`)
	if interp.GetGlobal("a").Int() != 2 {
		t.Errorf("expected 2, got %v", interp.GetGlobal("a"))
	}
	if !interp.GetGlobal("b").IsNil() {
		t.Errorf("expected nil, got %v", interp.GetGlobal("b"))
	}
}

func TestTableCopy(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		c := table.copy(t)
		c[1] = 99
		a := t[1]
		b := c[1]
	`)
	if interp.GetGlobal("a").Int() != 10 {
		t.Errorf("expected original unchanged (10), got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 99 {
		t.Errorf("expected copy changed (99), got %v", interp.GetGlobal("b"))
	}
}

func TestTableMerge(t *testing.T) {
	interp := runProgram(t, `
		t1 := {a: 1, b: 2}
		t2 := {b: 3, c: 4}
		table.merge(t1, t2)
		a := t1.a
		b := t1.b
		c := t1.c
	`)
	if interp.GetGlobal("a").Int() != 1 {
		t.Errorf("expected a=1, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 3 {
		t.Errorf("expected b=3 (overwritten), got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 4 {
		t.Errorf("expected c=4, got %v", interp.GetGlobal("c"))
	}
}

func TestTableCount(t *testing.T) {
	interp := runProgram(t, `
		t := {a: 1, b: 2, c: 3}
		n := table.count(t)
	`)
	if interp.GetGlobal("n").Int() != 3 {
		t.Errorf("expected 3, got %v", interp.GetGlobal("n"))
	}
}

func TestTableCountMixed(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, a: 1}
		n := table.count(t)
	`)
	if interp.GetGlobal("n").Int() != 3 {
		t.Errorf("expected 3, got %v", interp.GetGlobal("n"))
	}
}

func TestTableUnique(t *testing.T) {
	interp := runProgram(t, `
		t := {1, 2, 3, 2, 1, 4}
		u := table.unique(t)
	`)
	u := interp.GetGlobal("u").Table()
	if u.Length() != 4 {
		t.Errorf("expected 4 unique values, got %d", u.Length())
	}
}

func TestTableFlatten(t *testing.T) {
	interp := runProgram(t, `
		t := {1, {2, 3}, {4, {5, 6}}}
		f := table.flatten(t)
	`)
	f := interp.GetGlobal("f").Table()
	if f.Length() != 6 {
		t.Errorf("expected 6 elements, got %d", f.Length())
	}
	if f.RawGet(IntValue(1)).Int() != 1 {
		t.Errorf("expected f[1]=1, got %v", f.RawGet(IntValue(1)))
	}
	if f.RawGet(IntValue(6)).Int() != 6 {
		t.Errorf("expected f[6]=6, got %v", f.RawGet(IntValue(6)))
	}
}

func TestTableFlattenDepth(t *testing.T) {
	interp := runProgram(t, `
		t := {1, {2, {3, 4}}}
		f := table.flatten(t, 1)
	`)
	f := interp.GetGlobal("f").Table()
	// depth=1: flatten once, so {1, 2, {3, 4}}
	if f.Length() != 3 {
		t.Errorf("expected 3 elements at depth 1, got %d", f.Length())
	}
}

func TestTableZip(t *testing.T) {
	interp := runProgram(t, `
		a := {1, 2, 3}
		b := {"a", "b", "c"}
		z := table.zip(a, b)
	`)
	z := interp.GetGlobal("z").Table()
	if z.Length() != 3 {
		t.Errorf("expected 3 pairs, got %d", z.Length())
	}
	pair := z.RawGet(IntValue(1)).Table()
	if pair.RawGet(IntValue(1)).Int() != 1 {
		t.Errorf("expected pair[1]=1, got %v", pair.RawGet(IntValue(1)))
	}
	if pair.RawGet(IntValue(2)).Str() != "a" {
		t.Errorf("expected pair[2]='a', got %v", pair.RawGet(IntValue(2)))
	}
}

func TestTableReverse(t *testing.T) {
	interp := runProgram(t, `
		t := {1, 2, 3, 4, 5}
		table.reverse(t)
	`)
	tbl := interp.GetGlobal("t").Table()
	expected := []int64{5, 4, 3, 2, 1}
	for i, exp := range expected {
		v := tbl.RawGet(IntValue(int64(i + 1)))
		if v.Int() != exp {
			t.Errorf("t[%d] = %v, expected %d", i+1, v, exp)
		}
	}
}

func TestTableSlice(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30, 40, 50}
		s := table.slice(t, 2, 4)
	`)
	s := interp.GetGlobal("s").Table()
	if s.Length() != 3 {
		t.Errorf("expected 3 elements, got %d", s.Length())
	}
	if s.RawGet(IntValue(1)).Int() != 20 {
		t.Errorf("expected s[1]=20, got %v", s.RawGet(IntValue(1)))
	}
	if s.RawGet(IntValue(3)).Int() != 40 {
		t.Errorf("expected s[3]=40, got %v", s.RawGet(IntValue(3)))
	}
}

func TestTableToArray(t *testing.T) {
	interp := runProgram(t, `
		t := {a: 1, b: 2}
		arr := table.toArray(t)
	`)
	arr := interp.GetGlobal("arr").Table()
	if arr.Length() != 2 {
		t.Errorf("expected 2 elements, got %d", arr.Length())
	}
}

// runOnInterp parses and executes src on a pre-created interpreter.
func runOnInterp(t *testing.T, interp *Interpreter, src string) {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
}

func TestTableFilter(t *testing.T) {
	interp := New()
	tblLib := interp.GetGlobal("table").Table()
	buildTableHigherOrderWithInterp(interp, tblLib)

	runOnInterp(t, interp, `
		t := {1, 2, 3, 4, 5, 6}
		result := table.filter(t, func(v) { return v > 3 })
	`)

	result := interp.GetGlobal("result").Table()
	if result.Length() != 3 {
		t.Errorf("expected 3 filtered values, got %d", result.Length())
	}
	if result.RawGet(IntValue(1)).Int() != 4 {
		t.Errorf("expected result[1]=4, got %v", result.RawGet(IntValue(1)))
	}
}

func TestTableMap(t *testing.T) {
	interp := New()
	tblLib := interp.GetGlobal("table").Table()
	buildTableHigherOrderWithInterp(interp, tblLib)

	runOnInterp(t, interp, `
		t := {1, 2, 3}
		result := table.map(t, func(v) { return v * 2 })
	`)

	result := interp.GetGlobal("result").Table()
	if result.Length() != 3 {
		t.Errorf("expected 3 mapped values, got %d", result.Length())
	}
	if result.RawGet(IntValue(1)).Int() != 2 {
		t.Errorf("expected result[1]=2, got %v", result.RawGet(IntValue(1)))
	}
	if result.RawGet(IntValue(2)).Int() != 4 {
		t.Errorf("expected result[2]=4, got %v", result.RawGet(IntValue(2)))
	}
	if result.RawGet(IntValue(3)).Int() != 6 {
		t.Errorf("expected result[3]=6, got %v", result.RawGet(IntValue(3)))
	}
}

func TestTableReduce(t *testing.T) {
	interp := New()
	tblLib := interp.GetGlobal("table").Table()
	buildTableHigherOrderWithInterp(interp, tblLib)

	runOnInterp(t, interp, `
		t := {1, 2, 3, 4, 5}
		result := table.reduce(t, func(acc, v) { return acc + v }, 0)
	`)

	result := interp.GetGlobal("result")
	if result.Int() != 15 {
		t.Errorf("expected 15, got %v", result)
	}
}
