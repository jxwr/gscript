package runtime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// buildCryptoLib creates the "crypto" standard library table.
// Provides AES encryption/decryption (GCM mode) and secure random generation.
// Inspired by Odin's crypto package (aes, chacha20, etc.).
func buildCryptoLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "crypto." + name,
			Fn:   fn,
		}))
	}

	// crypto.randomBytes(n) -> string of n cryptographically secure random bytes
	set("randomBytes", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'crypto.randomBytes' (number expected)")
		}
		n := int(toInt(args[0]))
		if n < 0 {
			return nil, fmt.Errorf("bad argument #1 to 'crypto.randomBytes' (non-negative number expected)")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			return nil, fmt.Errorf("crypto.randomBytes: %v", err)
		}
		return []Value{StringValue(string(buf))}, nil
	})

	// crypto.randomHex(n) -> hex string of n random bytes (2n chars)
	set("randomHex", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'crypto.randomHex' (number expected)")
		}
		n := int(toInt(args[0]))
		if n < 0 {
			return nil, fmt.Errorf("bad argument #1 to 'crypto.randomHex' (non-negative number expected)")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			return nil, fmt.Errorf("crypto.randomHex: %v", err)
		}
		return []Value{StringValue(hex.EncodeToString(buf))}, nil
	})

	// crypto.aesGcmEncrypt(key, plaintext) -> ciphertext (hex string)
	// key must be 16, 24, or 32 bytes (AES-128, AES-192, AES-256)
	// The nonce is prepended to the ciphertext automatically
	set("aesGcmEncrypt", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad arguments to 'crypto.aesGcmEncrypt' (key and plaintext expected)")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'crypto.aesGcmEncrypt' (string key expected)")
		}
		if !args[1].IsString() {
			return nil, fmt.Errorf("bad argument #2 to 'crypto.aesGcmEncrypt' (string plaintext expected)")
		}

		key := []byte(args[0].Str())
		plaintext := []byte(args[1].Str())

		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("crypto.aesGcmEncrypt: %v", err)
		}

		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("crypto.aesGcmEncrypt: %v", err)
		}

		nonce := make([]byte, gcm.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return nil, fmt.Errorf("crypto.aesGcmEncrypt: %v", err)
		}

		ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
		return []Value{StringValue(hex.EncodeToString(ciphertext))}, nil
	})

	// crypto.aesGcmDecrypt(key, ciphertextHex) -> plaintext, or nil, error
	// ciphertextHex is the hex string from aesGcmEncrypt (includes nonce)
	set("aesGcmDecrypt", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad arguments to 'crypto.aesGcmDecrypt' (key and ciphertext expected)")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'crypto.aesGcmDecrypt' (string key expected)")
		}
		if !args[1].IsString() {
			return nil, fmt.Errorf("bad argument #2 to 'crypto.aesGcmDecrypt' (string ciphertext expected)")
		}

		key := []byte(args[0].Str())
		ciphertextHex := args[1].Str()

		ciphertext, err := hex.DecodeString(ciphertextHex)
		if err != nil {
			return []Value{NilValue(), StringValue("invalid hex ciphertext")}, nil
		}

		block, err := aes.NewCipher(key)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}

		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}

		nonceSize := gcm.NonceSize()
		if len(ciphertext) < nonceSize {
			return []Value{NilValue(), StringValue("ciphertext too short")}, nil
		}

		nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
		plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}

		return []Value{StringValue(string(plaintext))}, nil
	})

	// crypto.generateKey(size) -> random key string of specified byte length
	// Common sizes: 16 (AES-128), 24 (AES-192), 32 (AES-256)
	set("generateKey", func(args []Value) ([]Value, error) {
		size := 32 // default AES-256
		if len(args) >= 1 {
			size = int(toInt(args[0]))
		}
		if size != 16 && size != 24 && size != 32 {
			return nil, fmt.Errorf("crypto.generateKey: size must be 16, 24, or 32 (got %d)", size)
		}
		key := make([]byte, size)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("crypto.generateKey: %v", err)
		}
		return []Value{StringValue(string(key))}, nil
	})

	// crypto.equal(a, b) -> bool (constant-time comparison)
	// Prevents timing attacks when comparing secrets
	set("equal", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad arguments to 'crypto.equal' (two strings expected)")
		}
		a := []byte(args[0].Str())
		b := []byte(args[1].Str())
		if len(a) != len(b) {
			return []Value{BoolValue(false)}, nil
		}
		// Constant-time comparison
		result := byte(0)
		for i := range a {
			result |= a[i] ^ b[i]
		}
		return []Value{BoolValue(result == 0)}, nil
	})

	return t
}
