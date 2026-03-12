package runtime

import (
	"testing"
)

// compressInterp creates an interpreter with the compress library registered.
func compressInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "compress", buildCompressLib())
}

// ==================================================================
// compress.gzipEncode / gzipDecode tests
// ==================================================================

func TestCompressGzipRoundtrip(t *testing.T) {
	interp := compressInterp(t, `
		original := "Hello, World! This is a test of gzip compression."
		compressed := compress.gzipEncode(original)
		decompressed := compress.gzipDecode(compressed)
		result := decompressed == original
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("gzip roundtrip should preserve data")
	}
}

func TestCompressGzipCompresses(t *testing.T) {
	interp := compressInterp(t, `
		original := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		compressed := compress.gzipEncode(original)
		origLen := #original
		compLen := #compressed
		smaller := compLen < origLen
	`)
	v := interp.GetGlobal("smaller")
	if !v.IsBool() || !v.Bool() {
		t.Error("gzip should compress repetitive data")
	}
}

func TestCompressGzipEmpty(t *testing.T) {
	interp := compressInterp(t, `
		compressed := compress.gzipEncode("")
		decompressed := compress.gzipDecode(compressed)
		result := decompressed == ""
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("gzip should handle empty strings")
	}
}

func TestCompressGzipWithLevel(t *testing.T) {
	interp := compressInterp(t, `
		data := "Hello, World! This is a test with a specific compression level."
		compressed := compress.gzipEncode(data, 1)
		decompressed := compress.gzipDecode(compressed)
		result := decompressed == data
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("gzip with level should roundtrip correctly")
	}
}

func TestCompressGzipDecodeInvalid(t *testing.T) {
	interp := compressInterp(t, `
		result, err := compress.gzipDecode("not gzip data")
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Error("expected nil for invalid gzip data")
	}
	errV := interp.GetGlobal("err")
	if !errV.IsString() || errV.Str() == "" {
		t.Error("expected error message")
	}
}

// ==================================================================
// compress.zlibEncode / zlibDecode tests
// ==================================================================

func TestCompressZlibRoundtrip(t *testing.T) {
	interp := compressInterp(t, `
		original := "Hello, World! Testing zlib compression."
		compressed := compress.zlibEncode(original)
		decompressed := compress.zlibDecode(compressed)
		result := decompressed == original
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("zlib roundtrip should preserve data")
	}
}

func TestCompressZlibEmpty(t *testing.T) {
	interp := compressInterp(t, `
		compressed := compress.zlibEncode("")
		decompressed := compress.zlibDecode(compressed)
		result := decompressed == ""
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("zlib should handle empty strings")
	}
}

func TestCompressZlibDecodeInvalid(t *testing.T) {
	interp := compressInterp(t, `
		result, err := compress.zlibDecode("not zlib data")
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Error("expected nil for invalid zlib data")
	}
}

func TestCompressZlibCompresses(t *testing.T) {
	interp := compressInterp(t, `
		original := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		compressed := compress.zlibEncode(original)
		smaller := #compressed < #original
	`)
	v := interp.GetGlobal("smaller")
	if !v.IsBool() || !v.Bool() {
		t.Error("zlib should compress repetitive data")
	}
}

// ==================================================================
// compress.deflateEncode / deflateDecode tests
// ==================================================================

func TestCompressDeflateRoundtrip(t *testing.T) {
	interp := compressInterp(t, `
		original := "Hello, World! Testing deflate compression."
		compressed := compress.deflateEncode(original)
		decompressed := compress.deflateDecode(compressed)
		result := decompressed == original
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("deflate roundtrip should preserve data")
	}
}

func TestCompressDeflateEmpty(t *testing.T) {
	interp := compressInterp(t, `
		compressed := compress.deflateEncode("")
		decompressed := compress.deflateDecode(compressed)
		result := decompressed == ""
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("deflate should handle empty strings")
	}
}

func TestCompressDeflateWithLevel(t *testing.T) {
	interp := compressInterp(t, `
		data := "Testing deflate with best compression level 9"
		compressed := compress.deflateEncode(data, 9)
		decompressed := compress.deflateDecode(compressed)
		result := decompressed == data
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("deflate with level should roundtrip correctly")
	}
}
