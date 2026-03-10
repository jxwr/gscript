package runtime

import (
	"fmt"
	"regexp"
	"strings"
)

// buildStringLib creates the "string" standard library table and returns it.
func buildStringLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "string." + name,
			Fn:   fn,
		}))
	}

	// string.len(s) -> int
	set("len", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.len' (string expected)")
		}
		return []Value{IntValue(int64(len(args[0].Str())))}, nil
	})

	// string.sub(s, i [, j]) -> string
	// 1-based indexing, negative indices count from end
	set("sub", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.sub'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.sub' (string expected)")
		}
		s := args[0].Str()
		slen := len(s)

		i := int(toInt(args[1]))
		j := slen
		if len(args) >= 3 {
			j = int(toInt(args[2]))
		}

		// Convert Lua 1-based to Go 0-based
		if i < 0 {
			i = slen + i + 1
		}
		if i < 1 {
			i = 1
		}
		if j < 0 {
			j = slen + j + 1
		}
		if j > slen {
			j = slen
		}
		if i > j {
			return []Value{StringValue("")}, nil
		}
		return []Value{StringValue(s[i-1 : j])}, nil
	})

	// string.upper(s) -> string
	set("upper", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.upper' (string expected)")
		}
		return []Value{StringValue(strings.ToUpper(args[0].Str()))}, nil
	})

	// string.lower(s) -> string
	set("lower", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.lower' (string expected)")
		}
		return []Value{StringValue(strings.ToLower(args[0].Str()))}, nil
	})

	// string.rep(s, n [, sep]) -> string
	set("rep", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.rep'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.rep' (string expected)")
		}
		s := args[0].Str()
		n := int(toInt(args[1]))
		if n <= 0 {
			return []Value{StringValue("")}, nil
		}
		sep := ""
		if len(args) >= 3 && args[2].IsString() {
			sep = args[2].Str()
		}
		if sep == "" {
			return []Value{StringValue(strings.Repeat(s, n))}, nil
		}
		parts := make([]string, n)
		for i := range parts {
			parts[i] = s
		}
		return []Value{StringValue(strings.Join(parts, sep))}, nil
	})

	// string.reverse(s) -> string
	set("reverse", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.reverse' (string expected)")
		}
		s := args[0].Str()
		runes := []rune(s)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return []Value{StringValue(string(runes))}, nil
	})

	// string.byte(s [, i [, j]]) -> int...
	set("byte", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.byte' (string expected)")
		}
		s := args[0].Str()
		i := 1
		j := i
		if len(args) >= 2 {
			i = int(toInt(args[1]))
			j = i
		}
		if len(args) >= 3 {
			j = int(toInt(args[2]))
		}
		if i < 1 {
			i = 1
		}
		if j > len(s) {
			j = len(s)
		}
		var result []Value
		for k := i; k <= j; k++ {
			result = append(result, IntValue(int64(s[k-1])))
		}
		if len(result) == 0 {
			return []Value{NilValue()}, nil
		}
		return result, nil
	})

	// string.char(i...) -> string
	set("char", func(args []Value) ([]Value, error) {
		buf := make([]byte, 0, len(args))
		for _, a := range args {
			n := int(toInt(a))
			if n < 0 || n > 255 {
				return nil, fmt.Errorf("bad argument to 'string.char' (value out of range)")
			}
			buf = append(buf, byte(n))
		}
		return []Value{StringValue(string(buf))}, nil
	})

	// string.find(s, pattern [, init [, plain]]) -> start, end [, captures...]
	set("find", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.find'")
		}
		if !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.find' (string expected)")
		}
		s := args[0].Str()
		pattern := args[1].Str()
		init := 1
		plain := false
		if len(args) >= 3 {
			init = int(toInt(args[2]))
		}
		if len(args) >= 4 {
			plain = args[3].Truthy()
		}

		if init < 0 {
			init = len(s) + init + 1
		}
		if init < 1 {
			init = 1
		}
		if init > len(s)+1 {
			return []Value{NilValue()}, nil
		}
		searchStr := s[init-1:]

		if plain {
			idx := strings.Index(searchStr, pattern)
			if idx < 0 {
				return []Value{NilValue()}, nil
			}
			start := idx + init
			end := start + len(pattern) - 1
			return []Value{IntValue(int64(start)), IntValue(int64(end))}, nil
		}

		// Pattern matching
		goPattern := luaPatternToRegex(pattern)
		re, err := regexp.Compile(goPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %s", err)
		}
		loc := re.FindStringSubmatchIndex(searchStr)
		if loc == nil {
			return []Value{NilValue()}, nil
		}
		start := loc[0] + init
		end := loc[1] + init - 1
		result := []Value{IntValue(int64(start)), IntValue(int64(end))}
		// Add captures if any
		for i := 2; i < len(loc); i += 2 {
			if loc[i] >= 0 {
				result = append(result, StringValue(searchStr[loc[i]:loc[i+1]]))
			} else {
				result = append(result, NilValue())
			}
		}
		return result, nil
	})

	// string.match(s, pattern [, init]) -> captures...
	set("match", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.match'")
		}
		if !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.match' (string expected)")
		}
		s := args[0].Str()
		pattern := args[1].Str()
		init := 1
		if len(args) >= 3 {
			init = int(toInt(args[2]))
		}
		if init < 0 {
			init = len(s) + init + 1
		}
		if init < 1 {
			init = 1
		}
		if init > len(s)+1 {
			return []Value{NilValue()}, nil
		}
		searchStr := s[init-1:]

		goPattern := luaPatternToRegex(pattern)
		re, err := regexp.Compile(goPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %s", err)
		}
		matches := re.FindStringSubmatch(searchStr)
		if matches == nil {
			return []Value{NilValue()}, nil
		}
		if len(matches) == 1 {
			// No captures: return whole match
			return []Value{StringValue(matches[0])}, nil
		}
		// Return captures
		result := make([]Value, 0, len(matches)-1)
		for _, m := range matches[1:] {
			result = append(result, StringValue(m))
		}
		return result, nil
	})

	// string.gmatch(s, pattern) -> iterator
	set("gmatch", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.gmatch'")
		}
		if !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.gmatch' (string expected)")
		}
		s := args[0].Str()
		pattern := args[1].Str()

		goPattern := luaPatternToRegex(pattern)
		re, err := regexp.Compile(goPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %s", err)
		}
		allMatches := re.FindAllStringSubmatch(s, -1)
		idx := 0
		iter := &GoFunction{
			Name: "gmatch_iterator",
			Fn: func(_ []Value) ([]Value, error) {
				if idx >= len(allMatches) {
					return []Value{NilValue()}, nil
				}
				m := allMatches[idx]
				idx++
				if len(m) == 1 {
					return []Value{StringValue(m[0])}, nil
				}
				result := make([]Value, 0, len(m)-1)
				for _, sub := range m[1:] {
					result = append(result, StringValue(sub))
				}
				return result, nil
			},
		}
		return []Value{FunctionValue(iter)}, nil
	})

	// string.gsub(s, pattern, repl [, n]) -> string, count
	set("gsub", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'string.gsub'")
		}
		if !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.gsub' (string expected)")
		}
		s := args[0].Str()
		pattern := args[1].Str()
		repl := args[2]
		maxRepl := -1
		if len(args) >= 4 {
			maxRepl = int(toInt(args[3]))
		}

		goPattern := luaPatternToRegex(pattern)
		re, err := regexp.Compile(goPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %s", err)
		}

		count := 0
		var result string
		if repl.IsString() {
			replStr := repl.Str()
			result = re.ReplaceAllStringFunc(s, func(match string) string {
				if maxRepl >= 0 && count >= maxRepl {
					return match
				}
				count++
				return replStr
			})
		} else {
			// For non-string replacement, just do string replacement
			replStr := repl.String()
			result = re.ReplaceAllStringFunc(s, func(match string) string {
				if maxRepl >= 0 && count >= maxRepl {
					return match
				}
				count++
				return replStr
			})
		}
		return []Value{StringValue(result), IntValue(int64(count))}, nil
	})

	// string.format(fmt, ...) -> string
	set("format", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.format' (string expected)")
		}
		formatStr := args[0].Str()
		argIdx := 1

		var buf strings.Builder
		i := 0
		for i < len(formatStr) {
			if formatStr[i] != '%' {
				buf.WriteByte(formatStr[i])
				i++
				continue
			}
			i++ // skip %
			if i >= len(formatStr) {
				return nil, fmt.Errorf("invalid format string (ends with %%)")
			}

			// %% → literal %
			if formatStr[i] == '%' {
				buf.WriteByte('%')
				i++
				continue
			}

			// Collect flags, width, precision
			start := i - 1 // include the %
			for i < len(formatStr) && strings.ContainsRune("-+ #0", rune(formatStr[i])) {
				i++
			}
			// Width
			for i < len(formatStr) && formatStr[i] >= '0' && formatStr[i] <= '9' {
				i++
			}
			// Precision
			if i < len(formatStr) && formatStr[i] == '.' {
				i++
				for i < len(formatStr) && formatStr[i] >= '0' && formatStr[i] <= '9' {
					i++
				}
			}

			if i >= len(formatStr) {
				return nil, fmt.Errorf("invalid format string")
			}
			spec := formatStr[i]
			i++
			fmtSpec := formatStr[start:i]

			if argIdx >= len(args) {
				return nil, fmt.Errorf("bad argument #%d to 'string.format' (no value)", argIdx+1)
			}
			arg := args[argIdx]
			argIdx++

			switch spec {
			case 'd', 'i', 'u':
				n := toInt(arg)
				goFmt := strings.Replace(fmtSpec, string(spec), "d", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			case 'f', 'e', 'E', 'g', 'G':
				f := toFloat(arg)
				buf.WriteString(fmt.Sprintf(fmtSpec, f))
			case 'x':
				n := toInt(arg)
				goFmt := strings.Replace(fmtSpec, "x", "x", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			case 'X':
				n := toInt(arg)
				goFmt := strings.Replace(fmtSpec, "X", "X", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			case 'o':
				n := toInt(arg)
				goFmt := strings.Replace(fmtSpec, "o", "o", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			case 'c':
				n := toInt(arg)
				buf.WriteRune(rune(n))
			case 's':
				s := arg.String()
				goFmt := strings.Replace(fmtSpec, "s", "s", 1)
				buf.WriteString(fmt.Sprintf(goFmt, s))
			case 'q':
				// Quoted string
				s := arg.String()
				buf.WriteByte('"')
				for _, c := range s {
					switch c {
					case '"':
						buf.WriteString(`\"`)
					case '\\':
						buf.WriteString(`\\`)
					case '\n':
						buf.WriteString(`\n`)
					case '\r':
						buf.WriteString(`\r`)
					case '\000':
						buf.WriteString(`\0`)
					default:
						buf.WriteRune(c)
					}
				}
				buf.WriteByte('"')
			default:
				return nil, fmt.Errorf("invalid format specifier '%%%c'", spec)
			}
		}
		return []Value{StringValue(buf.String())}, nil
	})

	// string.split(s, sep) -> table (non-standard but useful)
	set("split", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.split'")
		}
		if !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.split' (string expected)")
		}
		s := args[0].Str()
		sep := args[1].Str()
		parts := strings.Split(s, sep)
		tbl := NewTable()
		for i, p := range parts {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(p))
		}
		return []Value{TableValue(tbl)}, nil
	})

	return t
}

// toInt converts a Value to int64. Handles ints, floats, and string-to-number coercion.
func toInt(v Value) int64 {
	switch v.Type() {
	case TypeInt:
		return v.Int()
	case TypeFloat:
		return int64(v.Float())
	case TypeString:
		n, ok := v.ToNumber()
		if ok {
			return toInt(n)
		}
		return 0
	default:
		return 0
	}
}

// toFloat converts a Value to float64.
func toFloat(v Value) float64 {
	switch v.Type() {
	case TypeInt:
		return float64(v.Int())
	case TypeFloat:
		return v.Float()
	case TypeString:
		n, ok := v.ToNumber()
		if ok {
			return toFloat(n)
		}
		return 0
	default:
		return 0
	}
}

// luaPatternToRegex converts a Lua-style pattern string to a Go regex string.
func luaPatternToRegex(pattern string) string {
	var buf strings.Builder
	i := 0
	n := len(pattern)

	// Handle anchors
	if n > 0 && pattern[0] == '^' {
		buf.WriteByte('^')
		i++
	}

	// Track whether the previous item was a matchable item (can have a quantifier)
	prevMatchable := false

	for i < n {
		c := pattern[i]
		switch c {
		case '%':
			i++
			if i >= n {
				buf.WriteByte('%')
				prevMatchable = false
				continue
			}
			next := pattern[i]
			switch next {
			case 'd':
				buf.WriteString("[0-9]")
			case 'D':
				buf.WriteString("[^0-9]")
			case 'a':
				buf.WriteString("[a-zA-Z]")
			case 'A':
				buf.WriteString("[^a-zA-Z]")
			case 'l':
				buf.WriteString("[a-z]")
			case 'L':
				buf.WriteString("[^a-z]")
			case 'u':
				buf.WriteString("[A-Z]")
			case 'U':
				buf.WriteString("[^A-Z]")
			case 's':
				buf.WriteString("[\\t\\n\\r\\f\\v ]")
			case 'S':
				buf.WriteString("[^\\t\\n\\r\\f\\v ]")
			case 'w':
				buf.WriteString("[a-zA-Z0-9]")
			case 'W':
				buf.WriteString("[^a-zA-Z0-9]")
			case 'p':
				buf.WriteString("[!-/:-@\\[-`{-~]")
			case 'P':
				buf.WriteString("[^!-/:-@\\[-`{-~]")
			case 'c':
				buf.WriteString("[\\x00-\\x1f\\x7f]")
			case 'C':
				buf.WriteString("[^\\x00-\\x1f\\x7f]")
			default:
				// Escape the literal character
				buf.WriteString(regexp.QuoteMeta(string(next)))
			}
			i++
			prevMatchable = true
		case '[':
			// Character class - pass through but translate %x inside
			buf.WriteByte('[')
			i++
			if i < n && pattern[i] == '^' {
				buf.WriteByte('^')
				i++
			}
			for i < n && pattern[i] != ']' {
				if pattern[i] == '%' && i+1 < n {
					i++
					ch := pattern[i]
					switch ch {
					case 'd':
						buf.WriteString("0-9")
					case 'a':
						buf.WriteString("a-zA-Z")
					case 'l':
						buf.WriteString("a-z")
					case 'u':
						buf.WriteString("A-Z")
					case 's':
						buf.WriteString("\\t\\n\\r\\f\\v ")
					case 'w':
						buf.WriteString("a-zA-Z0-9")
					case 'p':
						buf.WriteString("!-/:-@\\[-`{-~")
					default:
						buf.WriteString(regexp.QuoteMeta(string(ch)))
					}
					i++
				} else {
					buf.WriteByte(pattern[i])
					i++
				}
			}
			if i < n {
				buf.WriteByte(']')
				i++
			}
			prevMatchable = true
		case '(':
			buf.WriteByte('(')
			i++
			prevMatchable = false
		case ')':
			buf.WriteByte(')')
			i++
			prevMatchable = false // In Lua, groups cannot be quantified
		case '.':
			// In Lua patterns, . matches any char (similar to regex)
			buf.WriteByte('.')
			i++
			prevMatchable = true
		case '*', '+', '?':
			buf.WriteByte(c)
			i++
			prevMatchable = false
		case '-':
			// In Lua, '-' is a non-greedy repetition modifier (like *? in regex)
			// but only when it follows a matchable item. Otherwise it's a literal '-'.
			if prevMatchable {
				buf.WriteString("*?")
				prevMatchable = false
			} else {
				buf.WriteString("\\-")
				prevMatchable = true
			}
			i++
		case '$':
			if i == n-1 {
				buf.WriteByte('$')
				prevMatchable = false
			} else {
				buf.WriteString(regexp.QuoteMeta("$"))
				prevMatchable = true
			}
			i++
		default:
			// Check if the char needs escaping for Go regex
			if isRegexMeta(c) {
				buf.WriteByte('\\')
			}
			buf.WriteByte(c)
			i++
			prevMatchable = true
		}
	}

	return buf.String()
}

// isRegexMeta returns true if the byte is a Go regex metacharacter that
// needs escaping in a literal context.
func isRegexMeta(c byte) bool {
	switch c {
	case '\\', '{', '}', '|':
		return true
	}
	return false
}
