package runtime

import (
	"regexp"
	"testing"
)

func TestUUIDv4(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	execOnInterp(t, interp, `result := uuid.v4()`)

	v := interp.GetGlobal("result")
	if !v.IsString() {
		t.Fatalf("expected string, got %s", v.TypeName())
	}
	s := v.Str()
	// Check format
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(s) {
		t.Errorf("UUID v4 format incorrect: %s", s)
	}
}

func TestUUIDv4Unique(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	execOnInterp(t, interp, `
		a := uuid.v4()
		b := uuid.v4()
	`)

	a := interp.GetGlobal("a").Str()
	b := interp.GetGlobal("b").Str()
	if a == b {
		t.Errorf("two UUIDs should be different: %s == %s", a, b)
	}
}

func TestUUIDv4Raw(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	execOnInterp(t, interp, `result := uuid.v4Raw()`)

	v := interp.GetGlobal("result")
	s := v.Str()
	if len(s) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(s), s)
	}
	hexRe := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !hexRe.MatchString(s) {
		t.Errorf("expected hex string, got %s", s)
	}
}

func TestUUIDIsValid(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	execOnInterp(t, interp, `
		a := uuid.isValid("550e8400-e29b-41d4-a716-446655440000")
		b := uuid.isValid("not-a-uuid")
		c := uuid.isValid("550E8400-E29B-41D4-A716-446655440000")
	`)

	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for valid UUID")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for invalid UUID")
	}
	if !interp.GetGlobal("c").Bool() {
		t.Errorf("expected true for uppercase UUID")
	}
}

func TestUUIDParse(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	execOnInterp(t, interp, `result := uuid.parse("550e8400-e29b-41d4-a716-446655440000")`)

	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table, got %s", v.TypeName())
	}
	tbl := v.Table()
	if tbl.RawGet(StringValue("version")).Int() != 4 {
		t.Errorf("expected version=4, got %v", tbl.RawGet(StringValue("version")))
	}
	variant := tbl.RawGet(StringValue("variant")).Str()
	if variant != "RFC4122" {
		t.Errorf("expected variant='RFC4122', got '%s'", variant)
	}
	hexBytes := tbl.RawGet(StringValue("bytes")).Str()
	if len(hexBytes) != 32 {
		t.Errorf("expected 32 hex chars in bytes, got %d", len(hexBytes))
	}
}

func TestUUIDParseInvalid(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	execOnInterp(t, interp, `result, err := uuid.parse("not-a-uuid")`)

	if !interp.GetGlobal("result").IsNil() {
		t.Errorf("expected nil for invalid UUID")
	}
	if interp.GetGlobal("err").IsNil() {
		t.Errorf("expected error message for invalid UUID")
	}
}

func TestUUIDNil(t *testing.T) {
	interp := New()
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))

	// Use bracket notation since "nil" is a keyword in GScript
	execOnInterp(t, interp, `result := uuid["nil"]()`)

	v := interp.GetGlobal("result")
	if v.Str() != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("expected nil UUID, got '%s'", v.Str())
	}
}
