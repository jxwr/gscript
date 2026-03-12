package runtime

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
)

// buildCompressLib creates the "compress" standard library table.
// Provides gzip, zlib, and deflate compression/decompression.
// Inspired by Odin's compress package (gzip, zlib).
func buildCompressLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "compress." + name,
			Fn:   fn,
		}))
	}

	// ---------------------------------------------------------------
	// Gzip
	// ---------------------------------------------------------------

	// compress.gzipEncode(str [, level]) -> compressed string
	// level: 1-9 (1=fastest, 9=best compression), default=6
	set("gzipEncode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'compress.gzipEncode' (string expected)")
		}
		data := []byte(args[0].Str())
		level := gzip.DefaultCompression
		if len(args) >= 2 && args[1].IsNumber() {
			level = int(toInt(args[1]))
			if level < 1 || level > 9 {
				level = gzip.DefaultCompression
			}
		}

		var buf bytes.Buffer
		w, err := gzip.NewWriterLevel(&buf, level)
		if err != nil {
			return nil, fmt.Errorf("compress.gzipEncode: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("compress.gzipEncode: %v", err)
		}
		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("compress.gzipEncode: %v", err)
		}
		return []Value{StringValue(buf.String())}, nil
	})

	// compress.gzipDecode(str) -> decompressed string, or nil, error
	set("gzipDecode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'compress.gzipDecode' (string expected)")
		}
		r, err := gzip.NewReader(bytes.NewReader([]byte(args[0].Str())))
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		defer r.Close()
		decoded, err := io.ReadAll(r)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(decoded))}, nil
	})

	// ---------------------------------------------------------------
	// Zlib
	// ---------------------------------------------------------------

	// compress.zlibEncode(str [, level]) -> compressed string
	set("zlibEncode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'compress.zlibEncode' (string expected)")
		}
		data := []byte(args[0].Str())
		level := zlib.DefaultCompression
		if len(args) >= 2 && args[1].IsNumber() {
			level = int(toInt(args[1]))
			if level < 1 || level > 9 {
				level = zlib.DefaultCompression
			}
		}

		var buf bytes.Buffer
		w, err := zlib.NewWriterLevel(&buf, level)
		if err != nil {
			return nil, fmt.Errorf("compress.zlibEncode: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("compress.zlibEncode: %v", err)
		}
		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("compress.zlibEncode: %v", err)
		}
		return []Value{StringValue(buf.String())}, nil
	})

	// compress.zlibDecode(str) -> decompressed string, or nil, error
	set("zlibDecode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'compress.zlibDecode' (string expected)")
		}
		r, err := zlib.NewReader(bytes.NewReader([]byte(args[0].Str())))
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		defer r.Close()
		decoded, err := io.ReadAll(r)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(decoded))}, nil
	})

	// ---------------------------------------------------------------
	// Deflate (raw, no header)
	// ---------------------------------------------------------------

	// compress.deflateEncode(str [, level]) -> compressed string
	set("deflateEncode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'compress.deflateEncode' (string expected)")
		}
		data := []byte(args[0].Str())
		level := flate.DefaultCompression
		if len(args) >= 2 && args[1].IsNumber() {
			level = int(toInt(args[1]))
			if level < 1 || level > 9 {
				level = flate.DefaultCompression
			}
		}

		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, level)
		if err != nil {
			return nil, fmt.Errorf("compress.deflateEncode: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("compress.deflateEncode: %v", err)
		}
		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("compress.deflateEncode: %v", err)
		}
		return []Value{StringValue(buf.String())}, nil
	})

	// compress.deflateDecode(str) -> decompressed string, or nil, error
	set("deflateDecode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'compress.deflateDecode' (string expected)")
		}
		r := flate.NewReader(bytes.NewReader([]byte(args[0].Str())))
		defer r.Close()
		decoded, err := io.ReadAll(r)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(decoded))}, nil
	})

	return t
}
