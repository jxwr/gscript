package runtime

import (
	"testing"
)

// hashInterp creates an interpreter with the hash library manually registered.
func hashInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "hash", buildHashLib())
}

// ==================================================================
// hash.md5 tests
// ==================================================================

func TestHashMD5(t *testing.T) {
	interp := hashInterp(t, `result := hash.md5("hello")`)
	v := interp.GetGlobal("result")
	// MD5 of "hello" = 5d41402abc4b2a76b9719d911017c592
	expected := "5d41402abc4b2a76b9719d911017c592"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestHashMD5Empty(t *testing.T) {
	interp := hashInterp(t, `result := hash.md5("")`)
	v := interp.GetGlobal("result")
	// MD5 of "" = d41d8cd98f00b204e9800998ecf8427e
	expected := "d41d8cd98f00b204e9800998ecf8427e"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestHashMD5Length(t *testing.T) {
	interp := hashInterp(t, `result := hash.md5("test")`)
	v := interp.GetGlobal("result")
	if len(v.Str()) != 32 {
		t.Errorf("expected 32 chars, got %d", len(v.Str()))
	}
}

// ==================================================================
// hash.sha1 tests
// ==================================================================

func TestHashSHA1(t *testing.T) {
	interp := hashInterp(t, `result := hash.sha1("hello")`)
	v := interp.GetGlobal("result")
	// SHA1 of "hello" = aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d
	expected := "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestHashSHA1Length(t *testing.T) {
	interp := hashInterp(t, `result := hash.sha1("test")`)
	v := interp.GetGlobal("result")
	if len(v.Str()) != 40 {
		t.Errorf("expected 40 chars, got %d", len(v.Str()))
	}
}

// ==================================================================
// hash.sha256 tests
// ==================================================================

func TestHashSHA256(t *testing.T) {
	interp := hashInterp(t, `result := hash.sha256("hello")`)
	v := interp.GetGlobal("result")
	// SHA256 of "hello" = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestHashSHA256Length(t *testing.T) {
	interp := hashInterp(t, `result := hash.sha256("test")`)
	v := interp.GetGlobal("result")
	if len(v.Str()) != 64 {
		t.Errorf("expected 64 chars, got %d", len(v.Str()))
	}
}

// ==================================================================
// hash.sha512 tests
// ==================================================================

func TestHashSHA512(t *testing.T) {
	interp := hashInterp(t, `result := hash.sha512("hello")`)
	v := interp.GetGlobal("result")
	// SHA512 of "hello" = 9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043
	expected := "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestHashSHA512Length(t *testing.T) {
	interp := hashInterp(t, `result := hash.sha512("test")`)
	v := interp.GetGlobal("result")
	if len(v.Str()) != 128 {
		t.Errorf("expected 128 chars, got %d", len(v.Str()))
	}
}

// ==================================================================
// hash.crc32 tests
// ==================================================================

func TestHashCRC32(t *testing.T) {
	interp := hashInterp(t, `result := hash.crc32("hello")`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer, got %s", v.TypeName())
	}
	// CRC32 of "hello" = 907060870
	if v.Int() != 907060870 {
		t.Errorf("expected 907060870, got %v", v.Int())
	}
}

func TestHashCRC32Empty(t *testing.T) {
	interp := hashInterp(t, `result := hash.crc32("")`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer, got %s", v.TypeName())
	}
	// CRC32 of "" = 0
	if v.Int() != 0 {
		t.Errorf("expected 0, got %v", v.Int())
	}
}

// ==================================================================
// hash.hmacSHA256 tests
// ==================================================================

func TestHashHMACSHA256(t *testing.T) {
	interp := hashInterp(t, `result := hash.hmacSHA256("key", "hello")`)
	v := interp.GetGlobal("result")
	if !v.IsString() {
		t.Errorf("expected string, got %s", v.TypeName())
	}
	if len(v.Str()) != 64 {
		t.Errorf("expected 64 chars (SHA256 hex), got %d", len(v.Str()))
	}
	// HMAC-SHA256("key", "hello") = 9307b3b915efb5171ff14d8cb55fbcc798c6c0ef1456d66ded1a6aa723a58b7b
	expected := "9307b3b915efb5171ff14d8cb55fbcc798c6c0ef1456d66ded1a6aa723a58b7b"
	if v.Str() != expected {
		t.Errorf("expected %s, got %s", expected, v.Str())
	}
}

func TestHashHMACSHA256DifferentKeys(t *testing.T) {
	interp := hashInterp(t, `
		a := hash.hmacSHA256("key1", "message")
		b := hash.hmacSHA256("key2", "message")
		result := a != b
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("different keys should produce different HMACs")
	}
}
