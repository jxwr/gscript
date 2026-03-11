package runtime

import (
	"testing"
)

func TestURLParse(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `result := url.parse("https://user:pass@example.com:8080/path?q=1&r=2#frag")`)

	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table, got %s", v.TypeName())
	}
	tbl := v.Table()
	if tbl.RawGet(StringValue("scheme")).Str() != "https" {
		t.Errorf("expected scheme='https', got '%s'", tbl.RawGet(StringValue("scheme")).Str())
	}
	if tbl.RawGet(StringValue("host")).Str() != "example.com" {
		t.Errorf("expected host='example.com', got '%s'", tbl.RawGet(StringValue("host")).Str())
	}
	if tbl.RawGet(StringValue("port")).Str() != "8080" {
		t.Errorf("expected port='8080', got '%s'", tbl.RawGet(StringValue("port")).Str())
	}
	if tbl.RawGet(StringValue("path")).Str() != "/path" {
		t.Errorf("expected path='/path', got '%s'", tbl.RawGet(StringValue("path")).Str())
	}
	if tbl.RawGet(StringValue("fragment")).Str() != "frag" {
		t.Errorf("expected fragment='frag', got '%s'", tbl.RawGet(StringValue("fragment")).Str())
	}
	query := tbl.RawGet(StringValue("query")).Table()
	if query.RawGet(StringValue("q")).Str() != "1" {
		t.Errorf("expected query.q='1', got '%s'", query.RawGet(StringValue("q")).Str())
	}
}

func TestURLEncodeDecode(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `
		encoded := url.encode("hello world & more")
		decoded := url.decode(encoded)
	`)

	encoded := interp.GetGlobal("encoded").Str()
	decoded := interp.GetGlobal("decoded").Str()
	if decoded != "hello world & more" {
		t.Errorf("round trip failed: encoded=%s, decoded=%s", encoded, decoded)
	}
}

func TestURLQueryEncodeDecode(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `
		encoded := url.queryEncode({a: "1", b: "hello world"})
		decoded := url.queryDecode(encoded)
	`)

	decoded := interp.GetGlobal("decoded").Table()
	if decoded.RawGet(StringValue("a")).Str() != "1" {
		t.Errorf("expected a='1', got '%s'", decoded.RawGet(StringValue("a")).Str())
	}
	if decoded.RawGet(StringValue("b")).Str() != "hello world" {
		t.Errorf("expected b='hello world', got '%s'", decoded.RawGet(StringValue("b")).Str())
	}
}

func TestURLJoin(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `result := url.join("https://example.com/base/", "../other")`)

	v := interp.GetGlobal("result")
	if v.Str() != "https://example.com/other" {
		t.Errorf("expected 'https://example.com/other', got '%s'", v.Str())
	}
}

func TestURLIsValid(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `
		a := url.isValid("https://example.com")
		b := url.isValid("not a url")
	`)

	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for valid URL")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for invalid URL")
	}
}

func TestURLGetHost(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `result := url.getHost("https://example.com:8080/path")`)

	if interp.GetGlobal("result").Str() != "example.com" {
		t.Errorf("expected 'example.com', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestURLGetPath(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `result := url.getPath("https://example.com/foo/bar")`)

	if interp.GetGlobal("result").Str() != "/foo/bar" {
		t.Errorf("expected '/foo/bar', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestURLBuild(t *testing.T) {
	interp := New()
	interp.globals.Define("url", TableValue(buildURLLib()))

	execOnInterp(t, interp, `
		result := url.build({
			scheme: "https",
			host: "example.com",
			port: "8080",
			path: "/api/v1",
			query: {key: "value"}
		})
	`)

	v := interp.GetGlobal("result").Str()
	if v != "https://example.com:8080/api/v1?key=value" {
		t.Errorf("expected 'https://example.com:8080/api/v1?key=value', got '%s'", v)
	}
}
