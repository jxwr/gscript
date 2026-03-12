package runtime

import (
	"testing"
)

// cryptoInterp creates an interpreter with the crypto library registered.
func cryptoInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "crypto", buildCryptoLib())
}

// ==================================================================
// crypto.randomBytes tests
// ==================================================================

func TestCryptoRandomBytes(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.randomBytes(16)
		length := #result
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 16 {
		t.Errorf("expected 16 bytes, got %d", lenV.Int())
	}
}

func TestCryptoRandomBytesZero(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.randomBytes(0)
		length := #result
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 0 {
		t.Errorf("expected 0 bytes, got %d", lenV.Int())
	}
}

func TestCryptoRandomBytesUnique(t *testing.T) {
	interp := cryptoInterp(t, `
		a := crypto.randomBytes(32)
		b := crypto.randomBytes(32)
		result := a != b
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("two random byte sequences should be different")
	}
}

// ==================================================================
// crypto.randomHex tests
// ==================================================================

func TestCryptoRandomHex(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.randomHex(16)
		length := #result
	`)
	lenV := interp.GetGlobal("length")
	// 16 bytes = 32 hex chars
	if lenV.Int() != 32 {
		t.Errorf("expected 32 hex chars, got %d", lenV.Int())
	}
}

func TestCryptoRandomHexUnique(t *testing.T) {
	interp := cryptoInterp(t, `
		a := crypto.randomHex(16)
		b := crypto.randomHex(16)
		result := a != b
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("two random hex strings should be different")
	}
}

// ==================================================================
// crypto.aesGcmEncrypt / aesGcmDecrypt tests
// ==================================================================

func TestCryptoAesGcmRoundtrip(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(32)
		plaintext := "Hello, World! Secret message."
		encrypted := crypto.aesGcmEncrypt(key, plaintext)
		decrypted := crypto.aesGcmDecrypt(key, encrypted)
		result := decrypted == plaintext
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("AES-GCM roundtrip should preserve plaintext")
	}
}

func TestCryptoAesGcmEncryptProducesHex(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(16)
		encrypted := crypto.aesGcmEncrypt(key, "test")
	`)
	v := interp.GetGlobal("encrypted")
	if !v.IsString() {
		t.Errorf("expected string, got %s", v.TypeName())
	}
	// Should be a valid hex string (all chars in 0-9a-f)
	for _, c := range v.Str() {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("expected hex chars, found %c", c)
			break
		}
	}
}

func TestCryptoAesGcmDifferentEncryptions(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(32)
		enc1 := crypto.aesGcmEncrypt(key, "same message")
		enc2 := crypto.aesGcmEncrypt(key, "same message")
		result := enc1 != enc2
	`)
	v := interp.GetGlobal("result")
	// Different nonces should produce different ciphertexts
	if !v.IsBool() || !v.Bool() {
		t.Error("different encryptions should produce different ciphertexts (different nonces)")
	}
}

func TestCryptoAesGcmWrongKey(t *testing.T) {
	interp := cryptoInterp(t, `
		key1 := crypto.generateKey(32)
		key2 := crypto.generateKey(32)
		encrypted := crypto.aesGcmEncrypt(key1, "secret")
		result, err := crypto.aesGcmDecrypt(key2, encrypted)
	`)
	v := interp.GetGlobal("result")
	errV := interp.GetGlobal("err")
	if !v.IsNil() {
		t.Error("decryption with wrong key should return nil")
	}
	if !errV.IsString() || errV.Str() == "" {
		t.Error("expected error message for wrong key")
	}
}

func TestCryptoAesGcmEmptyPlaintext(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(16)
		encrypted := crypto.aesGcmEncrypt(key, "")
		decrypted := crypto.aesGcmDecrypt(key, encrypted)
		result := decrypted == ""
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("AES-GCM should handle empty plaintext")
	}
}

func TestCryptoAesGcm128(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(16)
		encrypted := crypto.aesGcmEncrypt(key, "AES-128 test")
		decrypted := crypto.aesGcmDecrypt(key, encrypted)
		result := decrypted == "AES-128 test"
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("AES-128 GCM should work")
	}
}

func TestCryptoAesGcm192(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(24)
		encrypted := crypto.aesGcmEncrypt(key, "AES-192 test")
		decrypted := crypto.aesGcmDecrypt(key, encrypted)
		result := decrypted == "AES-192 test"
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("AES-192 GCM should work")
	}
}

// ==================================================================
// crypto.generateKey tests
// ==================================================================

func TestCryptoGenerateKey32(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(32)
		length := #key
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 32 {
		t.Errorf("expected 32 bytes, got %d", lenV.Int())
	}
}

func TestCryptoGenerateKey16(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey(16)
		length := #key
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 16 {
		t.Errorf("expected 16 bytes, got %d", lenV.Int())
	}
}

func TestCryptoGenerateKeyDefault(t *testing.T) {
	interp := cryptoInterp(t, `
		key := crypto.generateKey()
		length := #key
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 32 {
		t.Errorf("default key should be 32 bytes, got %d", lenV.Int())
	}
}

// ==================================================================
// crypto.equal tests
// ==================================================================

func TestCryptoEqualSame(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.equal("hello", "hello")
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("equal strings should return true")
	}
}

func TestCryptoEqualDifferent(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.equal("hello", "world")
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || v.Bool() {
		t.Error("different strings should return false")
	}
}

func TestCryptoEqualDifferentLength(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.equal("short", "much longer string")
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || v.Bool() {
		t.Error("different length strings should return false")
	}
}

func TestCryptoEqualEmpty(t *testing.T) {
	interp := cryptoInterp(t, `
		result := crypto.equal("", "")
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("two empty strings should be equal")
	}
}
