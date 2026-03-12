package runtime

import (
	"testing"
)

// encodingInterp creates an interpreter with the encoding library registered.
func encodingInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "encoding", buildEncodingLib())
}

// ==================================================================
// encoding.hexEncode / hexDecode tests
// ==================================================================

func TestEncodingHexEncode(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.hexEncode("hello")`)
	v := interp.GetGlobal("result")
	if v.Str() != "68656c6c6f" {
		t.Errorf("expected 68656c6c6f, got %s", v.Str())
	}
}

func TestEncodingHexEncodeEmpty(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.hexEncode("")`)
	v := interp.GetGlobal("result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %s", v.Str())
	}
}

func TestEncodingHexDecode(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.hexDecode("68656c6c6f")`)
	v := interp.GetGlobal("result")
	if v.Str() != "hello" {
		t.Errorf("expected hello, got %s", v.Str())
	}
}

func TestEncodingHexDecodeInvalid(t *testing.T) {
	interp := encodingInterp(t, `
		result, err := encoding.hexDecode("xyz")
	`)
	v := interp.GetGlobal("result")
	errV := interp.GetGlobal("err")
	if !v.IsNil() {
		t.Errorf("expected nil for invalid hex, got %v", v)
	}
	if !errV.IsString() || errV.Str() == "" {
		t.Error("expected error message")
	}
}

func TestEncodingHexRoundtrip(t *testing.T) {
	interp := encodingInterp(t, `
		original := "Hello, World! 123"
		encoded := encoding.hexEncode(original)
		decoded := encoding.hexDecode(encoded)
		result := decoded == original
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("hex roundtrip should preserve data")
	}
}

// ==================================================================
// encoding.base32Encode / base32Decode tests
// ==================================================================

func TestEncodingBase32Encode(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.base32Encode("hello")`)
	v := interp.GetGlobal("result")
	if v.Str() != "NBSWY3DP" {
		t.Errorf("expected NBSWY3DP, got %s", v.Str())
	}
}

func TestEncodingBase32Decode(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.base32Decode("NBSWY3DP")`)
	v := interp.GetGlobal("result")
	if v.Str() != "hello" {
		t.Errorf("expected hello, got %s", v.Str())
	}
}

func TestEncodingBase32DecodeInvalid(t *testing.T) {
	interp := encodingInterp(t, `
		result, err := encoding.base32Decode("!!!invalid!!!")
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("expected nil for invalid base32, got %v", v)
	}
}

func TestEncodingBase32Roundtrip(t *testing.T) {
	interp := encodingInterp(t, `
		original := "Test Data 123!"
		encoded := encoding.base32Encode(original)
		decoded := encoding.base32Decode(encoded)
		result := decoded == original
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("base32 roundtrip should preserve data")
	}
}

// ==================================================================
// encoding.base32HexEncode / base32HexDecode tests
// ==================================================================

func TestEncodingBase32HexRoundtrip(t *testing.T) {
	interp := encodingInterp(t, `
		original := "hello world"
		encoded := encoding.base32HexEncode(original)
		decoded := encoding.base32HexDecode(encoded)
		result := decoded == original
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("base32hex roundtrip should preserve data")
	}
}

// ==================================================================
// encoding.iniEncode / iniDecode tests
// ==================================================================

func TestEncodingIniDecode(t *testing.T) {
	interp := encodingInterp(t, `
		ini_str := "[database]\nhost=localhost\nport=5432\n\n[app]\nname=myapp\n"
		result := encoding.iniDecode(ini_str)
		db := result["database"]
		host := db["host"]
		port := db["port"]
		app := result["app"]
		name := app["name"]
	`)
	host := interp.GetGlobal("host")
	port := interp.GetGlobal("port")
	name := interp.GetGlobal("name")
	if host.Str() != "localhost" {
		t.Errorf("expected localhost, got %s", host.Str())
	}
	if port.Str() != "5432" {
		t.Errorf("expected 5432, got %s", port.Str())
	}
	if name.Str() != "myapp" {
		t.Errorf("expected myapp, got %s", name.Str())
	}
}

func TestEncodingIniDecodeGlobalKeys(t *testing.T) {
	interp := encodingInterp(t, `
		ini_str := "key=value\n\n[section]\nfoo=bar\n"
		result := encoding.iniDecode(ini_str)
		gval := result["key"]
		sec := result["section"]
		foo := sec["foo"]
	`)
	gval := interp.GetGlobal("gval")
	foo := interp.GetGlobal("foo")
	if gval.Str() != "value" {
		t.Errorf("expected value, got %s", gval.Str())
	}
	if foo.Str() != "bar" {
		t.Errorf("expected bar, got %s", foo.Str())
	}
}

func TestEncodingIniDecodeComments(t *testing.T) {
	interp := encodingInterp(t, `
		ini_str := "; comment\n# also comment\nkey=value\n"
		result := encoding.iniDecode(ini_str)
		val := result["key"]
	`)
	val := interp.GetGlobal("val")
	if val.Str() != "value" {
		t.Errorf("expected value, got %s", val.Str())
	}
}

func TestEncodingIniDecodeEmpty(t *testing.T) {
	interp := encodingInterp(t, `
		result := encoding.iniDecode("")
	`)
	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Error("expected table")
	}
}

// ==================================================================
// encoding.xmlEscape / xmlUnescape tests
// ==================================================================

func TestEncodingXmlEscape(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.xmlEscape("<tag attr=\"val\">text & more</tag>")`)
	v := interp.GetGlobal("result")
	expected := "&lt;tag attr=&#34;val&#34;&gt;text &amp; more&lt;/tag&gt;"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestEncodingXmlEscapePlain(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.xmlEscape("hello world")`)
	v := interp.GetGlobal("result")
	if v.Str() != "hello world" {
		t.Errorf("expected hello world, got %s", v.Str())
	}
}

func TestEncodingXmlUnescape(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.xmlUnescape("&lt;tag&gt;text &amp; more&lt;/tag&gt;")`)
	v := interp.GetGlobal("result")
	expected := "<tag>text & more</tag>"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestEncodingXmlUnescapeQuotes(t *testing.T) {
	interp := encodingInterp(t, `result := encoding.xmlUnescape("&quot;hello&quot; &apos;world&apos;")`)
	v := interp.GetGlobal("result")
	expected := "\"hello\" 'world'"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}
