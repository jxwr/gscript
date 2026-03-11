package runtime

import (
	"strings"
	"testing"
)

func TestCSVParse(t *testing.T) {
	interp := New()
	interp.globals.Define("csv", TableValue(buildCSVLib()))

	execOnInterp(t, interp, `
		data := "a,b,c\n1,2,3\n4,5,6"
		result := csv.parse(data)
	`)

	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table, got %s", v.TypeName())
	}
	tbl := v.Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 rows, got %d", tbl.Length())
	}
	row1 := tbl.RawGet(IntValue(1)).Table()
	if row1.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected row1[1]='a', got '%s'", row1.RawGet(IntValue(1)).Str())
	}
	if row1.RawGet(IntValue(2)).Str() != "b" {
		t.Errorf("expected row1[2]='b', got '%s'", row1.RawGet(IntValue(2)).Str())
	}
}

func TestCSVParseWithHeaders(t *testing.T) {
	interp := New()
	interp.globals.Define("csv", TableValue(buildCSVLib()))

	execOnInterp(t, interp, `
		data := "name,age,city\nAlice,30,NYC\nBob,25,LA"
		result := csv.parseWithHeaders(data)
	`)

	v := interp.GetGlobal("result")
	tbl := v.Table()
	if tbl.Length() != 2 {
		t.Errorf("expected 2 data rows, got %d", tbl.Length())
	}
	row1 := tbl.RawGet(IntValue(1)).Table()
	if row1.RawGet(StringValue("name")).Str() != "Alice" {
		t.Errorf("expected name='Alice', got '%s'", row1.RawGet(StringValue("name")).Str())
	}
	if row1.RawGet(StringValue("age")).Str() != "30" {
		t.Errorf("expected age='30', got '%s'", row1.RawGet(StringValue("age")).Str())
	}
}

func TestCSVParseSep(t *testing.T) {
	interp := New()
	interp.globals.Define("csv", TableValue(buildCSVLib()))

	execOnInterp(t, interp, `
		data := "a;b;c\n1;2;3"
		result := csv.parse(data, {sep: ";"})
	`)

	v := interp.GetGlobal("result")
	tbl := v.Table()
	if tbl.Length() != 2 {
		t.Errorf("expected 2 rows, got %d", tbl.Length())
	}
	row1 := tbl.RawGet(IntValue(1)).Table()
	if row1.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected 'a', got '%s'", row1.RawGet(IntValue(1)).Str())
	}
}

func TestCSVEncode(t *testing.T) {
	interp := New()
	interp.globals.Define("csv", TableValue(buildCSVLib()))

	execOnInterp(t, interp, `
		rows := {
			{"a", "b", "c"},
			{"1", "2", "3"}
		}
		result := csv.encode(rows)
	`)

	v := interp.GetGlobal("result")
	expected := "a,b,c\n1,2,3\n"
	if v.Str() != expected {
		t.Errorf("expected %q, got %q", expected, v.Str())
	}
}

func TestCSVEncodeWithHeaders(t *testing.T) {
	interp := New()
	interp.globals.Define("csv", TableValue(buildCSVLib()))

	execOnInterp(t, interp, `
		rows := {
			{name: "Alice", age: "30"},
			{name: "Bob", age: "25"}
		}
		headers := {"name", "age"}
		result := csv.encodeWithHeaders(rows, headers)
	`)

	v := interp.GetGlobal("result")
	s := v.Str()
	if !strings.HasPrefix(s, "name,age\n") {
		t.Errorf("expected to start with 'name,age\\n', got %q", s)
	}
	if !strings.Contains(s, "Alice,30") {
		t.Errorf("expected 'Alice,30' in output, got %q", s)
	}
}

func TestCSVRoundTrip(t *testing.T) {
	interp := New()
	interp.globals.Define("csv", TableValue(buildCSVLib()))

	execOnInterp(t, interp, `
		original := "name,age\nAlice,30\nBob,25\n"
		parsed := csv.parseWithHeaders(original)
		result := csv.encodeWithHeaders(parsed, {"name", "age"})
	`)

	v := interp.GetGlobal("result")
	if !strings.Contains(v.Str(), "Alice,30") || !strings.Contains(v.Str(), "Bob,25") {
		t.Errorf("round trip failed, got %q", v.Str())
	}
}
