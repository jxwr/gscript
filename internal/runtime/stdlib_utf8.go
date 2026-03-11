package runtime

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// buildUTF8Lib creates the "utf8" standard library table.
func buildUTF8Lib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "utf8." + name,
			Fn:   fn,
		}))
	}

	// Constants
	t.RawSet(StringValue("charpattern"), StringValue("[\x00-\x7F\xC2-\xFD][\x80-\xBF]*"))

	// utf8.len(s) → int, or nil, errMsg if invalid
	set("len", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.len'")
		}
		s := args[0].Str()
		if !utf8.ValidString(s) {
			return []Value{NilValue(), StringValue("invalid UTF-8 string")}, nil
		}
		return []Value{IntValue(int64(utf8.RuneCountInString(s)))}, nil
	})

	// utf8.char(...) → string
	set("char", func(args []Value) ([]Value, error) {
		var buf strings.Builder
		for _, arg := range args {
			cp := rune(toInt(arg))
			buf.WriteRune(cp)
		}
		return []Value{StringValue(buf.String())}, nil
	})

	// utf8.codepoint(s, i [, j]) → codepoints... (1-based codepoint indices)
	set("codepoint", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.codepoint'")
		}
		s := args[0].Str()
		runes := []rune(s)

		i := int64(1)
		if len(args) >= 2 {
			i = toInt(args[1])
		}
		j := i
		if len(args) >= 3 {
			j = toInt(args[2])
		}

		// Validate bounds
		if i < 1 {
			i = 1
		}
		if j > int64(len(runes)) {
			j = int64(len(runes))
		}

		var results []Value
		for idx := i; idx <= j; idx++ {
			results = append(results, IntValue(int64(runes[idx-1])))
		}
		if len(results) == 0 {
			return []Value{NilValue()}, nil
		}
		return results, nil
	})

	// utf8.codes(s) → table of {pos, code} tables
	set("codes", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.codes'")
		}
		s := args[0].Str()
		tbl := NewTable()
		idx := int64(1)
		bytePos := 0
		for bytePos < len(s) {
			r, size := utf8.DecodeRuneInString(s[bytePos:])
			entry := NewTable()
			entry.RawSet(StringValue("pos"), IntValue(int64(bytePos+1))) // 1-based
			entry.RawSet(StringValue("code"), IntValue(int64(r)))
			tbl.RawSet(IntValue(idx), TableValue(entry))
			idx++
			bytePos += size
		}
		return []Value{TableValue(tbl)}, nil
	})

	// utf8.offset(s, n [, i]) → int (byte position of nth codepoint, 1-based)
	set("offset", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'utf8.offset'")
		}
		s := args[0].Str()
		n := toInt(args[1])
		startByte := int64(0)
		if len(args) >= 3 {
			startByte = toInt(args[2]) - 1 // convert from 1-based to 0-based
		}

		bytePos := int(startByte)
		cpCount := int64(0)
		for bytePos < len(s) {
			cpCount++
			if cpCount == n {
				return []Value{IntValue(int64(bytePos + 1))}, nil // 1-based
			}
			_, size := utf8.DecodeRuneInString(s[bytePos:])
			bytePos += size
		}
		// If n equals len+1, return position past end
		if cpCount+1 == n {
			return []Value{IntValue(int64(bytePos + 1))}, nil
		}
		return []Value{NilValue()}, nil
	})

	// utf8.valid(s) → bool
	set("valid", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.valid'")
		}
		return []Value{BoolValue(utf8.ValidString(args[0].Str()))}, nil
	})

	// utf8.reverse(s) → string (reverse by codepoint)
	set("reverse", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.reverse'")
		}
		runes := []rune(args[0].Str())
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return []Value{StringValue(string(runes))}, nil
	})

	// utf8.sub(s, i [, j]) → string (substring by codepoint indices, 1-based)
	set("sub", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'utf8.sub'")
		}
		runes := []rune(args[0].Str())
		i := int(toInt(args[1]))
		j := len(runes)
		if len(args) >= 3 {
			j = int(toInt(args[2]))
		}

		// Convert to 0-based
		if i < 0 {
			i = len(runes) + i + 1
		}
		if j < 0 {
			j = len(runes) + j + 1
		}

		// Clamp
		if i < 1 {
			i = 1
		}
		if j > len(runes) {
			j = len(runes)
		}
		if i > j+1 {
			return []Value{StringValue("")}, nil
		}

		return []Value{StringValue(string(runes[i-1 : j]))}, nil
	})

	// utf8.upper(s) → string
	set("upper", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.upper'")
		}
		runes := []rune(args[0].Str())
		for i, r := range runes {
			runes[i] = unicode.ToUpper(r)
		}
		return []Value{StringValue(string(runes))}, nil
	})

	// utf8.lower(s) → string
	set("lower", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.lower'")
		}
		runes := []rune(args[0].Str())
		for i, r := range runes {
			runes[i] = unicode.ToLower(r)
		}
		return []Value{StringValue(string(runes))}, nil
	})

	// utf8.charclass(cp) → string ("L", "N", "P", "S", "O")
	set("charclass", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'utf8.charclass'")
		}
		cp := rune(toInt(args[0]))
		var class string
		switch {
		case unicode.IsLetter(cp):
			class = "L"
		case unicode.IsDigit(cp):
			class = "N"
		case unicode.IsSpace(cp):
			class = "S"
		case unicode.IsPunct(cp) || unicode.IsSymbol(cp):
			class = "P"
		default:
			class = "O"
		}
		return []Value{StringValue(class)}, nil
	})

	return t
}
