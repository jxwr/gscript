package runtime

import (
	"encoding/base64"
	"fmt"
)

// buildBase64Lib creates the "base64" standard library table.
func buildBase64Lib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "base64." + name,
			Fn:   fn,
		}))
	}

	// base64.encode(str) -> standard base64 encoded string
	set("encode", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'base64.encode'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'base64.encode' (string expected)")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(args[0].Str()))
		return []Value{StringValue(encoded)}, nil
	})

	// base64.decode(str) -> decoded string, or nil, "error message"
	set("decode", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'base64.decode'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'base64.decode' (string expected)")
		}
		decoded, err := base64.StdEncoding.DecodeString(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(decoded))}, nil
	})

	// base64.urlEncode(str) -> URL-safe base64 encoded string (no padding)
	set("urlEncode", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'base64.urlEncode'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'base64.urlEncode' (string expected)")
		}
		encoded := base64.RawURLEncoding.EncodeToString([]byte(args[0].Str()))
		return []Value{StringValue(encoded)}, nil
	})

	// base64.urlDecode(str) -> decoded string, or nil, "error message"
	set("urlDecode", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'base64.urlDecode'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'base64.urlDecode' (string expected)")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(decoded))}, nil
	})

	return t
}
