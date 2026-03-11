package runtime

import (
	"strings"
	"testing"
)

// jsonInterp creates an interpreter with the json library manually registered.
func jsonInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "json", buildJSONLib())
}

// ==================================================================
// JSON encode tests
// ==================================================================

func TestJSONEncodeNil(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode(nil)`)
	v := interp.GetGlobal("result")
	if !v.IsString() || v.Str() != "null" {
		t.Errorf("expected \"null\", got %v", v)
	}
}

func TestJSONEncodeBool(t *testing.T) {
	interp := jsonInterp(t, `
		a := json.encode(true)
		b := json.encode(false)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if a.Str() != "true" {
		t.Errorf("expected \"true\", got %v", a)
	}
	if b.Str() != "false" {
		t.Errorf("expected \"false\", got %v", b)
	}
}

func TestJSONEncodeInt(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode(42)`)
	v := interp.GetGlobal("result")
	if v.Str() != "42" {
		t.Errorf("expected \"42\", got %v", v)
	}
}

func TestJSONEncodeFloat(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode(3.14)`)
	v := interp.GetGlobal("result")
	if v.Str() != "3.14" {
		t.Errorf("expected \"3.14\", got %v", v)
	}
}

func TestJSONEncodeString(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode("hello world")`)
	v := interp.GetGlobal("result")
	if v.Str() != `"hello world"` {
		t.Errorf("expected '\"hello world\"', got %v", v)
	}
}

func TestJSONEncodeArray(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode({1, 2, 3})`)
	v := interp.GetGlobal("result")
	if v.Str() != "[1,2,3]" {
		t.Errorf("expected \"[1,2,3]\", got %v", v)
	}
}

func TestJSONEncodeObject(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode({name: "test", age: 30})`)
	v := interp.GetGlobal("result")
	s := v.Str()
	// JSON object key order is not guaranteed, check both
	if !strings.Contains(s, `"name":"test"`) || !strings.Contains(s, `"age":30`) {
		t.Errorf("expected JSON object with name and age, got %v", s)
	}
}

func TestJSONEncodeNested(t *testing.T) {
	interp := jsonInterp(t, `result := json.encode({items: {1, 2, 3}, meta: {count: 3}})`)
	v := interp.GetGlobal("result")
	s := v.Str()
	if !strings.Contains(s, `"items":[1,2,3]`) {
		t.Errorf("expected items array in JSON, got %v", s)
	}
	if !strings.Contains(s, `"count":3`) {
		t.Errorf("expected count in meta, got %v", s)
	}
}

func TestJSONEncodeMixedTable(t *testing.T) {
	// Mixed table with both int and string keys should encode as object
	interp := jsonInterp(t, `
		t := {10, 20, name: "test"}
		result := json.encode(t)
	`)
	v := interp.GetGlobal("result")
	s := v.Str()
	// Should be an object with "1":10, "2":20, "name":"test"
	if !strings.Contains(s, `"name":"test"`) {
		t.Errorf("expected name field in JSON object, got %v", s)
	}
}

// ==================================================================
// JSON decode tests
// ==================================================================

func TestJSONDecodeNull(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("null")`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestJSONDecodeBool(t *testing.T) {
	interp := jsonInterp(t, `
		a := json.decode("true")
		b := json.decode("false")
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if !a.IsBool() || !a.Bool() {
		t.Errorf("expected true, got %v", a)
	}
	if !b.IsBool() || b.Bool() {
		t.Errorf("expected false, got %v", b)
	}
}

func TestJSONDecodeInt(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("42")`)
	v := interp.GetGlobal("result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v (type %s)", v, v.TypeName())
	}
}

func TestJSONDecodeFloat(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("3.14")`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() || v.Float() != 3.14 {
		t.Errorf("expected float 3.14, got %v", v)
	}
}

func TestJSONDecodeString(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("\"hello\"")`)
	v := interp.GetGlobal("result")
	if !v.IsString() || v.Str() != "hello" {
		t.Errorf("expected string 'hello', got %v", v)
	}
}

func TestJSONDecodeArray(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("[1, 2, 3]")`)
	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table, got %v", v.TypeName())
	}
	tbl := v.Table()
	if tbl.Length() != 3 {
		t.Errorf("expected length 3, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Int() != 1 {
		t.Errorf("expected [1]=1, got %v", tbl.RawGet(IntValue(1)))
	}
	if tbl.RawGet(IntValue(2)).Int() != 2 {
		t.Errorf("expected [2]=2, got %v", tbl.RawGet(IntValue(2)))
	}
	if tbl.RawGet(IntValue(3)).Int() != 3 {
		t.Errorf("expected [3]=3, got %v", tbl.RawGet(IntValue(3)))
	}
}

func TestJSONDecodeObject(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("{\"name\":\"test\",\"age\":30}")`)
	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table, got %v", v.TypeName())
	}
	tbl := v.Table()
	name := tbl.RawGet(StringValue("name"))
	age := tbl.RawGet(StringValue("age"))
	if name.Str() != "test" {
		t.Errorf("expected name='test', got %v", name)
	}
	if age.Int() != 30 {
		t.Errorf("expected age=30, got %v", age)
	}
}

func TestJSONDecodeNestedObject(t *testing.T) {
	interp := jsonInterp(t, `result := json.decode("{\"inner\":{\"value\":42}}")`)
	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table, got %v", v.TypeName())
	}
	inner := v.Table().RawGet(StringValue("inner"))
	if !inner.IsTable() {
		t.Fatalf("expected inner table, got %v", inner.TypeName())
	}
	val := inner.Table().RawGet(StringValue("value"))
	if val.Int() != 42 {
		t.Errorf("expected value=42, got %v", val)
	}
}

func TestJSONDecodeError(t *testing.T) {
	interp := jsonInterp(t, `result, err := json.decode("invalid json{{{")`)
	result := interp.GetGlobal("result")
	errMsg := interp.GetGlobal("err")
	if !result.IsNil() {
		t.Errorf("expected nil result on parse error, got %v", result)
	}
	if !errMsg.IsString() || errMsg.Str() == "" {
		t.Errorf("expected error message string, got %v", errMsg)
	}
}

// ==================================================================
// JSON pretty tests
// ==================================================================

func TestJSONPrettyDefault(t *testing.T) {
	interp := jsonInterp(t, `result := json.pretty({a: 1})`)
	v := interp.GetGlobal("result")
	s := v.Str()
	// Should contain newlines and indentation
	if !strings.Contains(s, "\n") {
		t.Errorf("expected pretty-printed JSON with newlines, got %v", s)
	}
	if !strings.Contains(s, "  ") {
		t.Errorf("expected 2-space indentation, got %v", s)
	}
}

func TestJSONPrettyCustomIndent(t *testing.T) {
	interp := jsonInterp(t, `result := json.pretty({a: 1}, "    ")`)
	v := interp.GetGlobal("result")
	s := v.Str()
	if !strings.Contains(s, "    ") {
		t.Errorf("expected 4-space indentation, got %v", s)
	}
}

// ==================================================================
// JSON roundtrip test
// ==================================================================

func TestJSONRoundtrip(t *testing.T) {
	interp := jsonInterp(t, `
		original := {name: "test", scores: {90, 85, 95}, active: true}
		encoded := json.encode(original)
		decoded := json.decode(encoded)
		name := decoded.name
		active := decoded.active
		score1 := decoded.scores[1]
	`)
	if interp.GetGlobal("name").Str() != "test" {
		t.Errorf("expected name='test', got %v", interp.GetGlobal("name"))
	}
	if !interp.GetGlobal("active").Bool() {
		t.Errorf("expected active=true, got %v", interp.GetGlobal("active"))
	}
	if interp.GetGlobal("score1").Int() != 90 {
		t.Errorf("expected score1=90, got %v", interp.GetGlobal("score1"))
	}
}
