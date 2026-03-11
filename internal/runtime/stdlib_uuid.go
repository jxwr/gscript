package runtime

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

// buildUUIDLib creates the "uuid" standard library table.
func buildUUIDLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "uuid." + name,
			Fn:   fn,
		}))
	}

	// generateUUIDv4 generates a UUID v4 string
	generateUUIDv4 := func() (string, error) {
		var uuid [16]byte
		_, err := rand.Read(uuid[:])
		if err != nil {
			return "", fmt.Errorf("uuid.v4: failed to generate random bytes: %v", err)
		}
		// Set version 4
		uuid[6] = (uuid[6] & 0x0f) | 0x40
		// Set variant bits (RFC 4122)
		uuid[8] = (uuid[8] & 0x3f) | 0x80

		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]), nil
	}

	// uuid.v4() -- generate random UUID v4 string
	set("v4", func(args []Value) ([]Value, error) {
		s, err := generateUUIDv4()
		if err != nil {
			return nil, err
		}
		return []Value{StringValue(s)}, nil
	})

	// uuid.v4Raw() -- UUID v4 without hyphens (32 hex chars)
	set("v4Raw", func(args []Value) ([]Value, error) {
		var uuid [16]byte
		_, err := rand.Read(uuid[:])
		if err != nil {
			return nil, fmt.Errorf("uuid.v4Raw: failed to generate random bytes: %v", err)
		}
		uuid[6] = (uuid[6] & 0x0f) | 0x40
		uuid[8] = (uuid[8] & 0x3f) | 0x80

		return []Value{StringValue(fmt.Sprintf("%x", uuid[:]))}, nil
	})

	// uuid.isValid(s) -- bool (validates UUID format)
	uuidPattern := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	set("isValid", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'uuid.isValid' (string expected)")
		}
		return []Value{BoolValue(uuidPattern.MatchString(args[0].Str()))}, nil
	})

	// uuid.parse(s) -- parse UUID string -> {version, variant, bytes (hex string)}
	set("parse", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'uuid.parse' (string expected)")
		}
		s := args[0].Str()
		if !uuidPattern.MatchString(s) {
			return []Value{NilValue(), StringValue("invalid UUID format")}, nil
		}

		// Extract version from character at position 14 (0-indexed)
		versionChar := s[14]
		version := int64(0)
		if versionChar >= '0' && versionChar <= '9' {
			version = int64(versionChar - '0')
		} else if versionChar >= 'a' && versionChar <= 'f' {
			version = int64(versionChar - 'a' + 10)
		} else if versionChar >= 'A' && versionChar <= 'F' {
			version = int64(versionChar - 'A' + 10)
		}

		// Extract variant from character at position 19
		variantChar := s[19]
		var variant string
		variantBits := int64(0)
		if variantChar >= '0' && variantChar <= '9' {
			variantBits = int64(variantChar - '0')
		} else if variantChar >= 'a' && variantChar <= 'f' {
			variantBits = int64(variantChar - 'a' + 10)
		} else if variantChar >= 'A' && variantChar <= 'F' {
			variantBits = int64(variantChar - 'A' + 10)
		}

		if variantBits&0x8 == 0 {
			variant = "NCS"
		} else if variantBits&0x4 == 0 {
			variant = "RFC4122"
		} else if variantBits&0x2 == 0 {
			variant = "Microsoft"
		} else {
			variant = "Future"
		}

		// Hex string without hyphens
		hexStr := ""
		for _, c := range s {
			if c != '-' {
				hexStr += string(c)
			}
		}

		result := NewTable()
		result.RawSet(StringValue("version"), IntValue(version))
		result.RawSet(StringValue("variant"), StringValue(variant))
		result.RawSet(StringValue("bytes"), StringValue(hexStr))

		return []Value{TableValue(result)}, nil
	})

	// uuid.nil() -- return the nil UUID
	set("nil", func(args []Value) ([]Value, error) {
		return []Value{StringValue("00000000-0000-0000-0000-000000000000")}, nil
	})

	return t
}
