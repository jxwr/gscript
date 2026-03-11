package runtime

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash/crc32"
)

// buildHashLib creates the "hash" standard library table.
func buildHashLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "hash." + name,
			Fn:   fn,
		}))
	}

	// hash.md5(str) -> lowercase hex string (32 chars)
	set("md5", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'hash.md5'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'hash.md5' (string expected)")
		}
		h := md5.Sum([]byte(args[0].Str()))
		return []Value{StringValue(hex.EncodeToString(h[:]))}, nil
	})

	// hash.sha1(str) -> lowercase hex string (40 chars)
	set("sha1", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'hash.sha1'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'hash.sha1' (string expected)")
		}
		h := sha1.Sum([]byte(args[0].Str()))
		return []Value{StringValue(hex.EncodeToString(h[:]))}, nil
	})

	// hash.sha256(str) -> lowercase hex string (64 chars)
	set("sha256", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'hash.sha256'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'hash.sha256' (string expected)")
		}
		h := sha256.Sum256([]byte(args[0].Str()))
		return []Value{StringValue(hex.EncodeToString(h[:]))}, nil
	})

	// hash.sha512(str) -> lowercase hex string (128 chars)
	set("sha512", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'hash.sha512'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'hash.sha512' (string expected)")
		}
		h := sha512.Sum512([]byte(args[0].Str()))
		return []Value{StringValue(hex.EncodeToString(h[:]))}, nil
	})

	// hash.crc32(str) -> integer (CRC32 checksum)
	set("crc32", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'hash.crc32'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'hash.crc32' (string expected)")
		}
		checksum := crc32.ChecksumIEEE([]byte(args[0].Str()))
		return []Value{IntValue(int64(checksum))}, nil
	})

	// hash.hmacSHA256(key, message) -> lowercase hex string
	set("hmacSHA256", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad arguments to 'hash.hmacSHA256' (key and message expected)")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'hash.hmacSHA256' (string expected)")
		}
		if !args[1].IsString() {
			return nil, fmt.Errorf("bad argument #2 to 'hash.hmacSHA256' (string expected)")
		}
		mac := hmac.New(sha256.New, []byte(args[0].Str()))
		mac.Write([]byte(args[1].Str()))
		result := mac.Sum(nil)
		return []Value{StringValue(hex.EncodeToString(result))}, nil
	})

	return t
}
