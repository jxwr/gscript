package runtime

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unsafe"
)

const NativeKindStdStringFormat uint8 = 2

var stdStringFormatIdentity byte

// StdStringFormatIdentityPtr returns the process-wide identity token attached
// to stdlib string.format GoFunctions. JIT guards compare this token instead
// of trusting mutable function names or the presence of FastArg2.
func StdStringFormatIdentityPtr() unsafe.Pointer {
	return unsafe.Pointer(&stdStringFormatIdentity)
}

// buildStringLib creates the "string" standard library table and returns it.
func buildStringLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "string." + name,
			Fn:   fn,
		}))
	}
	setFast1 := func(name string, fn func([]Value) ([]Value, error), fast func([]Value) (Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name:  "string." + name,
			Fn:    fn,
			Fast1: fast,
		}))
	}

	// string.len(s) -> int
	set("len", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.len' (string expected)")
		}
		return []Value{IntValue(int64(StringLen(args[0])))}, nil
	})

	// string.sub(s, i [, j]) -> string
	// 1-based indexing, negative indices count from end
	setFast1("sub", func(args []Value) ([]Value, error) {
		v, err := stringSubValue(args)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil
	}, stringSubValue)

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
	setFast1("format", func(args []Value) ([]Value, error) {
		v, err := stringFormatValue(args)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil
	}, stringFormatValue)
	if v := t.RawGetString("format"); v.IsFunction() {
		gf := v.GoFunction()
		gf.FastArg2 = stringFormat2Value
		gf.FastArg3 = stringFormat3Value
		gf.NativeKind = NativeKindStdStringFormat
		gf.NativeData = StdStringFormatIdentityPtr()
	}

	// string.split(s, sep) -> table. sep="" splits by byte
	setFast1("split", func(args []Value) ([]Value, error) {
		v, err := stringSplitValue(args)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil
	}, stringSplitValue)

	// string.trim(s [, cutset]) -- trim leading/trailing whitespace (or chars in cutset)
	set("trim", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.trim' (string expected)")
		}
		s := args[0].Str()
		if len(args) >= 2 && args[1].IsString() {
			return []Value{StringValue(strings.Trim(s, args[1].Str()))}, nil
		}
		return []Value{StringValue(strings.TrimSpace(s))}, nil
	})

	// string.trimLeft(s [, cutset])
	set("trimLeft", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.trimLeft' (string expected)")
		}
		s := args[0].Str()
		if len(args) >= 2 && args[1].IsString() {
			return []Value{StringValue(strings.TrimLeft(s, args[1].Str()))}, nil
		}
		return []Value{StringValue(strings.TrimLeftFunc(s, unicode.IsSpace))}, nil
	})

	// string.trimRight(s [, cutset])
	set("trimRight", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.trimRight' (string expected)")
		}
		s := args[0].Str()
		if len(args) >= 2 && args[1].IsString() {
			return []Value{StringValue(strings.TrimRight(s, args[1].Str()))}, nil
		}
		return []Value{StringValue(strings.TrimRightFunc(s, unicode.IsSpace))}, nil
	})

	// string.hasPrefix(s, prefix) -> bool
	set("hasPrefix", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.hasPrefix' (string expected)")
		}
		return []Value{BoolValue(strings.HasPrefix(args[0].Str(), args[1].Str()))}, nil
	})

	// string.hasSuffix(s, suffix) -> bool
	set("hasSuffix", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.hasSuffix' (string expected)")
		}
		return []Value{BoolValue(strings.HasSuffix(args[0].Str(), args[1].Str()))}, nil
	})

	// string.contains(s, substr) -> bool
	set("contains", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.contains' (string expected)")
		}
		return []Value{BoolValue(strings.Contains(args[0].Str(), args[1].Str()))}, nil
	})

	// string.count(s, substr) -> int
	set("count", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.count' (string expected)")
		}
		return []Value{IntValue(int64(strings.Count(args[0].Str(), args[1].Str())))}, nil
	})

	// string.replaceAll(s, old, new) -- plain string replace all
	set("replaceAll", func(args []Value) ([]Value, error) {
		if len(args) < 3 || !args[0].IsString() || !args[1].IsString() || !args[2].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.replaceAll' (string expected)")
		}
		return []Value{StringValue(strings.ReplaceAll(args[0].Str(), args[1].Str(), args[2].Str()))}, nil
	})

	// string.join(t, sep) -- join table of strings with separator
	set("join", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.join'")
		}
		tbl := args[0].Table()
		sep := args[1].Str()
		length := tbl.Length()
		parts := make([]string, length)
		for i := 0; i < length; i++ {
			parts[i] = tbl.RawGet(IntValue(int64(i + 1))).String()
		}
		return []Value{StringValue(strings.Join(parts, sep))}, nil
	})

	// string.title(s) -- capitalize first letter of each word
	set("title", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.title' (string expected)")
		}
		s := args[0].Str()
		// Capitalize first letter of each word
		prev := ' '
		result := make([]rune, 0, len(s))
		for _, r := range s {
			if unicode.IsSpace(rune(prev)) {
				result = append(result, unicode.ToUpper(r))
			} else {
				result = append(result, r)
			}
			prev = r
		}
		return []Value{StringValue(string(result))}, nil
	})

	// string.padLeft(s, n [, char]) -- pad with char (default space) on left to width n
	set("padLeft", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.padLeft'")
		}
		s := args[0].Str()
		n := int(toInt(args[1]))
		pad := " "
		if len(args) >= 3 && args[2].IsString() {
			pad = args[2].Str()
		}
		if pad == "" {
			pad = " "
		}
		for len(s) < n {
			s = pad + s
		}
		// Trim to exact width if pad added too much
		if len(s) > n {
			s = s[len(s)-n:]
		}
		return []Value{StringValue(s)}, nil
	})

	// string.padRight(s, n [, char]) -- pad on right
	set("padRight", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument to 'string.padRight'")
		}
		s := args[0].Str()
		n := int(toInt(args[1]))
		pad := " "
		if len(args) >= 3 && args[2].IsString() {
			pad = args[2].Str()
		}
		if pad == "" {
			pad = " "
		}
		for len(s) < n {
			s = s + pad
		}
		// Trim to exact width
		if len(s) > n {
			s = s[:n]
		}
		return []Value{StringValue(s)}, nil
	})

	// string.repeat(s, n) -- alias for string.rep
	set("repeat", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'string.repeat'")
		}
		if !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.repeat' (string expected)")
		}
		s := args[0].Str()
		n := int(toInt(args[1]))
		if n <= 0 {
			return []Value{StringValue("")}, nil
		}
		return []Value{StringValue(strings.Repeat(s, n))}, nil
	})

	// string.isNumeric(s) -> bool
	set("isNumeric", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'string.isNumeric' (string expected)")
		}
		s := strings.TrimSpace(args[0].Str())
		_, err := strconv.ParseFloat(s, 64)
		return []Value{BoolValue(err == nil && s != "")}, nil
	})

	return t
}

func stringSubValue(args []Value) (Value, error) {
	if len(args) < 2 {
		return NilValue(), fmt.Errorf("bad argument to 'string.sub'")
	}
	if !args[0].IsString() {
		return NilValue(), fmt.Errorf("bad argument #1 to 'string.sub' (string expected)")
	}
	s := args[0].Str()
	slen := len(s)

	i := int(toInt(args[1]))
	j := slen
	if len(args) >= 3 {
		j = int(toInt(args[2]))
	}

	// Convert Lua 1-based indexes to Go byte offsets.
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
		return StringValue(""), nil
	}
	return StringValue(s[i-1 : j]), nil
}

func stringSplitValue(args []Value) (Value, error) {
	if len(args) < 2 {
		return NilValue(), fmt.Errorf("bad argument to 'string.split'")
	}
	if !args[0].IsString() || !args[1].IsString() {
		return NilValue(), fmt.Errorf("bad argument to 'string.split' (string expected)")
	}
	s := args[0].Str()
	sep := args[1].Str()
	if sep == "" {
		tbl := NewSequentialArrayTable(len(s))
		for i := 0; i < len(s); i++ {
			tbl.array[i+1] = StringValue(string(s[i]))
		}
		return TableValue(tbl), nil
	}

	n := strings.Count(s, sep) + 1
	tbl := NewSequentialArrayTable(n)
	start := 0
	idx := 1
	for {
		next := strings.Index(s[start:], sep)
		if next < 0 {
			tbl.array[idx] = StringValue(s[start:])
			break
		}
		end := start + next
		tbl.array[idx] = StringValue(s[start:end])
		idx++
		start = end + len(sep)
	}
	return TableValue(tbl), nil
}

func stringFormatValue(args []Value) (Value, error) {
	if len(args) < 1 || !args[0].IsString() {
		return NilValue(), fmt.Errorf("bad argument #1 to 'string.format' (string expected)")
	}
	formatStr := args[0].Str()
	if prog, ok, err := cachedSimpleFormat(formatStr); err != nil {
		return NilValue(), err
	} else if ok {
		RecordRuntimePathStringFormatFast()
		return prog.formatValue(args)
	}
	RecordRuntimePathStringFormatFallback()
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
			return NilValue(), fmt.Errorf("invalid format string (ends with %%)")
		}

		if formatStr[i] == '%' {
			buf.WriteByte('%')
			i++
			continue
		}

		start := i - 1 // include the %
		for i < len(formatStr) && isFormatFlag(formatStr[i]) {
			i++
		}
		for i < len(formatStr) && formatStr[i] >= '0' && formatStr[i] <= '9' {
			i++
		}
		if i < len(formatStr) && formatStr[i] == '.' {
			i++
			for i < len(formatStr) && formatStr[i] >= '0' && formatStr[i] <= '9' {
				i++
			}
		}

		if i >= len(formatStr) {
			return NilValue(), fmt.Errorf("invalid format string")
		}
		spec := formatStr[i]
		i++
		fmtSpec := formatStr[start:i]

		if argIdx >= len(args) {
			return NilValue(), fmt.Errorf("bad argument #%d to 'string.format' (no value)", argIdx+1)
		}
		arg := args[argIdx]
		argIdx++

		switch spec {
		case 'd', 'i', 'u':
			n := toInt(arg)
			if !writeFastIntegerFormat(&buf, fmtSpec, spec, n) {
				goFmt := strings.Replace(fmtSpec, string(spec), "d", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			}
		case 'f', 'e', 'E', 'g', 'G':
			buf.WriteString(fmt.Sprintf(fmtSpec, toFloat(arg)))
		case 'x':
			n := toInt(arg)
			if !writeFastIntegerFormat(&buf, fmtSpec, spec, n) {
				goFmt := strings.Replace(fmtSpec, "x", "x", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			}
		case 'X':
			n := toInt(arg)
			if !writeFastIntegerFormat(&buf, fmtSpec, spec, n) {
				goFmt := strings.Replace(fmtSpec, "X", "X", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			}
		case 'o':
			n := toInt(arg)
			if !writeFastIntegerFormat(&buf, fmtSpec, spec, n) {
				goFmt := strings.Replace(fmtSpec, "o", "o", 1)
				buf.WriteString(fmt.Sprintf(goFmt, n))
			}
		case 'c':
			buf.WriteRune(rune(toInt(arg)))
		case 's':
			s := arg.String()
			if fmtSpec == "%s" {
				buf.WriteString(s)
			} else {
				goFmt := strings.Replace(fmtSpec, "s", "s", 1)
				buf.WriteString(fmt.Sprintf(goFmt, s))
			}
		case 'q':
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
			return NilValue(), fmt.Errorf("invalid format specifier '%%%c'", spec)
		}
	}
	return StringValue(buf.String()), nil
}

// StringFormatValue applies the stdlib string.format implementation to a
// pre-built argument slice. It is used by JIT op-exit paths after guarding the
// callee identity.
func StringFormatValue(args []Value) (Value, error) {
	return stringFormatValue(args)
}

func stringFormat2Value(format, arg Value) (Value, error) {
	if !format.IsString() {
		return NilValue(), fmt.Errorf("bad argument #1 to 'string.format' (string expected)")
	}
	formatStr := format.Str()
	if prog, ok, err := cachedSimpleFormat(formatStr); err != nil {
		return NilValue(), err
	} else if ok && prog.singleInt {
		RecordRuntimePathStringFormatFast()
		n := toInt(arg)
		if v, ok := prog.cachedResult(n); ok {
			return v, nil
		}
		s := prog.formatSingleInt(n)
		v := StringValue(s)
		prog.storeCachedResult(n, v)
		return v, nil
	}
	args := [2]Value{format, arg}
	return stringFormatValue(args[:])
}

func stringFormat3Value(format, arg0, arg1 Value) (Value, error) {
	if !format.IsString() {
		return NilValue(), fmt.Errorf("bad argument #1 to 'string.format' (string expected)")
	}
	formatStr := format.Str()
	if prog, ok, err := cachedSimpleFormat(formatStr); err != nil {
		return NilValue(), err
	} else if ok && prog.minArgs == 3 {
		RecordRuntimePathStringFormatFast()
		s, err := prog.formatTwoArgs(arg0, arg1)
		if err != nil {
			return NilValue(), err
		}
		return StringValue(s), nil
	}
	RecordRuntimePathStringFormatFallback()
	args := [3]Value{format, arg0, arg1}
	return stringFormatValue(args[:])
}

// IsStdStringFormatFunction reports whether v is the stdlib string.format
// GoFunction installed by buildStringLib. This is intentionally an identity
// style guard for JIT fast paths: scripts cannot create GoFunction values.
func IsStdStringFormatFunction(v Value) bool {
	gf := v.GoFunction()
	return gf != nil &&
		gf.NativeKind == NativeKindStdStringFormat &&
		gf.NativeData == StdStringFormatIdentityPtr() &&
		gf.FastArg2 != nil
}

// StringFormatSingleInt formats a cached simple one-integer pattern. It is the
// semantic helper for JIT specializations of string.format(pattern, int).
func StringFormatSingleInt(pattern string, n int64) (Value, bool, error) {
	prog, ok, err := cachedSimpleFormat(pattern)
	if err != nil || !ok || !prog.singleInt {
		return NilValue(), false, err
	}
	if v, ok := prog.cachedResult(n); ok {
		return v, true, nil
	}
	v := StringValue(prog.formatSingleInt(n))
	prog.storeCachedResult(n, v)
	return v, true, nil
}

func writeFastIntegerFormat(buf *strings.Builder, fmtSpec string, spec byte, n int64) bool {
	if len(fmtSpec) < 2 || fmtSpec[0] != '%' || fmtSpec[len(fmtSpec)-1] != spec {
		return false
	}
	pos := 1
	pad := byte(' ')
	if pos < len(fmtSpec)-1 && fmtSpec[pos] == '0' {
		pad = '0'
		pos++
	}
	width := 0
	for pos < len(fmtSpec)-1 && fmtSpec[pos] >= '0' && fmtSpec[pos] <= '9' {
		width = width*10 + int(fmtSpec[pos]-'0')
		pos++
	}
	if pos != len(fmtSpec)-1 {
		return false
	}

	var scratch [64]byte
	digits := scratch[:0]
	switch spec {
	case 'd', 'i', 'u':
		digits = strconv.AppendInt(digits, n, 10)
	case 'x':
		digits = strconv.AppendInt(digits, n, 16)
	case 'X':
		digits = strconv.AppendInt(digits, n, 16)
		for i, b := range digits {
			if b >= 'a' && b <= 'f' {
				digits[i] = b - ('a' - 'A')
			}
		}
	case 'o':
		digits = strconv.AppendInt(digits, n, 8)
	default:
		return false
	}

	if width <= len(digits) {
		buf.Write(digits)
		return true
	}
	padCount := width - len(digits)
	if pad == '0' && len(digits) > 0 && digits[0] == '-' {
		buf.WriteByte('-')
		for i := 0; i < padCount; i++ {
			buf.WriteByte('0')
		}
		buf.Write(digits[1:])
		return true
	}
	for i := 0; i < padCount; i++ {
		buf.WriteByte(pad)
	}
	buf.Write(digits)
	return true
}

type simpleFormatPart struct {
	lit   string
	spec  string
	verb  byte
	pad   byte
	width int
}

type simpleFormatProgram struct {
	formatStr string
	parts     []simpleFormatPart
	minArgs   int
	litBytes  int
	singleInt bool

	resultMu       sync.Mutex
	resultCache    map[int64]Value
	resultOrder    []int64
	resultEvict    int
	fastResultTags [64]atomic.Uint64
	fastResultVals [64]atomic.Uint64
}

const simpleFormatCacheLimit = 64
const simpleFormatResultCacheLimit = 8192
const simpleFormatFastCacheSize = 32
const simpleFormatResultTagSalt = 0x9e3779b97f4a7c15

var simpleFormatCache = struct {
	sync.Mutex
	entries map[string]*simpleFormatProgram
	order   []string
}{
	entries: make(map[string]*simpleFormatProgram),
}

var simpleFormatFastCache [simpleFormatFastCacheSize]atomic.Pointer[simpleFormatProgram]

func cachedSimpleFormat(formatStr string) (*simpleFormatProgram, bool, error) {
	if prog := lookupSimpleFormatFast(formatStr); prog != nil {
		return prog, true, nil
	}

	simpleFormatCache.Lock()
	if prog, ok := simpleFormatCache.entries[formatStr]; ok {
		simpleFormatCache.Unlock()
		storeSimpleFormatFast(formatStr, prog)
		return prog, true, nil
	}
	simpleFormatCache.Unlock()

	prog, ok, err := compileSimpleFormat(formatStr)
	if err != nil {
		return nil, false, err
	}
	if ok {
		simpleFormatCache.Lock()
		if cached, exists := simpleFormatCache.entries[formatStr]; exists {
			simpleFormatCache.Unlock()
			storeSimpleFormatFast(formatStr, cached)
			return cached, true, nil
		}
		if len(simpleFormatCache.entries) >= simpleFormatCacheLimit && len(simpleFormatCache.order) > 0 {
			delete(simpleFormatCache.entries, simpleFormatCache.order[0])
			copy(simpleFormatCache.order, simpleFormatCache.order[1:])
			simpleFormatCache.order = simpleFormatCache.order[:len(simpleFormatCache.order)-1]
		}
		simpleFormatCache.entries[formatStr] = prog
		simpleFormatCache.order = append(simpleFormatCache.order, formatStr)
		simpleFormatCache.Unlock()
		storeSimpleFormatFast(formatStr, prog)
		return prog, true, nil
	}
	return nil, false, nil
}

func lookupSimpleFormatFast(formatStr string) *simpleFormatProgram {
	slot := simpleFormatFastSlot(formatStr)
	prog := simpleFormatFastCache[slot].Load()
	if prog != nil && prog.formatStr == formatStr {
		return prog
	}
	return nil
}

func storeSimpleFormatFast(formatStr string, prog *simpleFormatProgram) {
	if prog == nil {
		return
	}
	simpleFormatFastCache[simpleFormatFastSlot(formatStr)].Store(prog)
}

func simpleFormatFastSlot(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h & (simpleFormatFastCacheSize - 1)
}

func compileSimpleFormat(formatStr string) (*simpleFormatProgram, bool, error) {
	parts := make([]simpleFormatPart, 0, 4)
	litStart := 0
	argCount := 0
	litBytes := 0
	for i := 0; i < len(formatStr); {
		if formatStr[i] != '%' {
			i++
			continue
		}
		if i+1 >= len(formatStr) {
			return nil, false, fmt.Errorf("invalid format string (ends with %%)")
		}
		if formatStr[i+1] == '%' {
			return nil, false, nil
		}
		if i > litStart {
			lit := formatStr[litStart:i]
			parts = append(parts, simpleFormatPart{lit: lit})
			litBytes += len(lit)
		}

		start := i
		i++
		for i < len(formatStr) && isFormatFlag(formatStr[i]) {
			if formatStr[i] != '0' {
				return nil, false, nil
			}
			i++
		}
		for i < len(formatStr) && formatStr[i] >= '0' && formatStr[i] <= '9' {
			i++
		}
		if i < len(formatStr) && formatStr[i] == '.' {
			return nil, false, nil
		}
		if i >= len(formatStr) {
			return nil, false, fmt.Errorf("invalid format string")
		}
		verb := formatStr[i]
		i++
		switch verb {
		case 'd', 'i', 'u', 'x', 'X', 'o':
			part, ok := compileSimpleIntegerFormatPart(formatStr[start:i], verb)
			if !ok {
				return nil, false, nil
			}
			parts = append(parts, part)
		case 's':
			if i-start != 2 {
				return nil, false, nil
			}
			parts = append(parts, simpleFormatPart{spec: "%s", verb: verb})
		default:
			return nil, false, nil
		}
		argCount++
		litStart = i
	}
	if argCount == 0 {
		return nil, false, nil
	}
	if litStart < len(formatStr) {
		lit := formatStr[litStart:]
		parts = append(parts, simpleFormatPart{lit: lit})
		litBytes += len(lit)
	}
	return &simpleFormatProgram{
		formatStr: formatStr,
		parts:     parts,
		minArgs:   argCount + 1,
		litBytes:  litBytes,
		singleInt: argCount == 1 && simpleFormatHasSingleIntegerArg(parts),
	}, true, nil
}

func compileSimpleIntegerFormatPart(fmtSpec string, verb byte) (simpleFormatPart, bool) {
	if len(fmtSpec) < 2 || fmtSpec[0] != '%' || fmtSpec[len(fmtSpec)-1] != verb {
		return simpleFormatPart{}, false
	}
	pos := 1
	pad := byte(' ')
	if pos < len(fmtSpec)-1 && fmtSpec[pos] == '0' {
		pad = '0'
		pos++
	}
	width := 0
	for pos < len(fmtSpec)-1 && fmtSpec[pos] >= '0' && fmtSpec[pos] <= '9' {
		width = width*10 + int(fmtSpec[pos]-'0')
		pos++
	}
	if pos != len(fmtSpec)-1 {
		return simpleFormatPart{}, false
	}
	return simpleFormatPart{spec: fmtSpec, verb: verb, pad: pad, width: width}, true
}

func simpleFormatHasSingleIntegerArg(parts []simpleFormatPart) bool {
	seen := false
	for _, part := range parts {
		if part.verb == 0 {
			continue
		}
		switch part.verb {
		case 'd', 'i', 'u', 'x', 'X', 'o':
			if seen {
				return false
			}
			seen = true
		default:
			return false
		}
	}
	return seen
}

func (p *simpleFormatProgram) formatValue(args []Value) (Value, error) {
	if p.singleInt {
		if len(args) < p.minArgs {
			return NilValue(), fmt.Errorf("bad argument #%d to 'string.format' (no value)", len(args)+1)
		}
		n := toInt(args[1])
		if v, ok := p.cachedResult(n); ok {
			return v, nil
		}
		s, err := p.format(args)
		if err != nil {
			return NilValue(), err
		}
		v := StringValue(s)
		p.storeCachedResult(n, v)
		return v, nil
	}
	s, err := p.format(args)
	if err != nil {
		return NilValue(), err
	}
	return StringValue(s), nil
}

func (p *simpleFormatProgram) cachedResult(n int64) (Value, bool) {
	if v, ok := p.cachedResultFast(n); ok {
		return v, true
	}
	p.resultMu.Lock()
	defer p.resultMu.Unlock()
	if p.resultCache == nil {
		return NilValue(), false
	}
	v, ok := p.resultCache[n]
	if ok {
		p.storeCachedResultFast(n, v)
	}
	return v, ok
}

func (p *simpleFormatProgram) storeCachedResult(n int64, v Value) {
	p.resultMu.Lock()
	defer p.resultMu.Unlock()
	if p.resultCache == nil {
		p.resultCache = make(map[int64]Value, 64)
	}
	if _, exists := p.resultCache[n]; exists {
		p.resultCache[n] = v
		return
	}
	if len(p.resultCache) >= simpleFormatResultCacheLimit && len(p.resultOrder) > 0 {
		delete(p.resultCache, p.resultOrder[p.resultEvict])
		p.resultOrder[p.resultEvict] = n
		p.resultEvict++
		if p.resultEvict == len(p.resultOrder) {
			p.resultEvict = 0
		}
	} else {
		p.resultOrder = append(p.resultOrder, n)
	}
	p.resultCache[n] = v
	p.storeCachedResultFast(n, v)
}

func (p *simpleFormatProgram) cachedResultFast(n int64) (Value, bool) {
	slot := simpleFormatResultSlot(n)
	want := simpleFormatResultTag(n)
	if p.fastResultTags[slot].Load() != want {
		return NilValue(), false
	}
	bits := p.fastResultVals[slot].Load()
	if bits == 0 || p.fastResultTags[slot].Load() != want {
		return NilValue(), false
	}
	return Value(bits), true
}

func (p *simpleFormatProgram) storeCachedResultFast(n int64, v Value) {
	slot := simpleFormatResultSlot(n)
	tag := simpleFormatResultTag(n)
	p.fastResultTags[slot].Store(0)
	p.fastResultVals[slot].Store(uint64(v))
	p.fastResultTags[slot].Store(tag)
}

func simpleFormatResultSlot(n int64) uint64 {
	x := uint64(n) ^ simpleFormatResultTagSalt
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	return x & 63
}

func simpleFormatResultTag(n int64) uint64 {
	tag := uint64(n) ^ simpleFormatResultTagSalt
	if tag == 0 {
		return 1
	}
	return tag
}

func (p *simpleFormatProgram) format(args []Value) (string, error) {
	if len(args) < p.minArgs {
		return "", fmt.Errorf("bad argument #%d to 'string.format' (no value)", len(args)+1)
	}
	var buf strings.Builder
	buf.Grow(p.litBytes + 16*(p.minArgs-1))
	argIdx := 1
	for _, part := range p.parts {
		if part.verb == 0 {
			buf.WriteString(part.lit)
			continue
		}
		arg := args[argIdx]
		argIdx++
		switch part.verb {
		case 'd', 'i', 'u', 'x', 'X', 'o':
			writeCompiledIntegerFormat(&buf, part, toInt(arg))
		case 's':
			buf.WriteString(arg.String())
		}
	}
	return buf.String(), nil
}

func (p *simpleFormatProgram) formatTwoArgs(arg0, arg1 Value) (string, error) {
	if p.minArgs > 3 {
		return "", fmt.Errorf("bad argument #3 to 'string.format' (no value)")
	}
	var buf strings.Builder
	buf.Grow(p.litBytes + 32)
	argIdx := 0
	for _, part := range p.parts {
		if part.verb == 0 {
			buf.WriteString(part.lit)
			continue
		}
		var arg Value
		switch argIdx {
		case 0:
			arg = arg0
		case 1:
			arg = arg1
		default:
			return "", fmt.Errorf("bad argument #%d to 'string.format' (no value)", argIdx+2)
		}
		argIdx++
		switch part.verb {
		case 'd', 'i', 'u', 'x', 'X', 'o':
			writeCompiledIntegerFormat(&buf, part, toInt(arg))
		case 's':
			buf.WriteString(arg.String())
		}
	}
	return buf.String(), nil
}

func (p *simpleFormatProgram) formatSingleInt(n int64) string {
	var buf strings.Builder
	buf.Grow(p.litBytes + 16)
	for _, part := range p.parts {
		if part.verb == 0 {
			buf.WriteString(part.lit)
			continue
		}
		writeCompiledIntegerFormat(&buf, part, n)
	}
	return buf.String()
}

func writeCompiledIntegerFormat(buf *strings.Builder, part simpleFormatPart, n int64) {
	var scratch [64]byte
	digits := scratch[:0]
	switch part.verb {
	case 'd', 'i', 'u':
		digits = strconv.AppendInt(digits, n, 10)
	case 'x':
		digits = strconv.AppendInt(digits, n, 16)
	case 'X':
		digits = strconv.AppendInt(digits, n, 16)
		for i, b := range digits {
			if b >= 'a' && b <= 'f' {
				digits[i] = b - ('a' - 'A')
			}
		}
	case 'o':
		digits = strconv.AppendInt(digits, n, 8)
	default:
		return
	}

	if part.width <= len(digits) {
		buf.Write(digits)
		return
	}
	padCount := part.width - len(digits)
	if part.pad == '0' && len(digits) > 0 && digits[0] == '-' {
		buf.WriteByte('-')
		for i := 0; i < padCount; i++ {
			buf.WriteByte('0')
		}
		buf.Write(digits[1:])
		return
	}
	for i := 0; i < padCount; i++ {
		buf.WriteByte(part.pad)
	}
	buf.Write(digits)
}

func scanSimpleFormatCacheRoots(visitor func(unsafe.Pointer), seen map[uintptr]struct{}) {
	simpleFormatCache.Lock()
	programs := make([]*simpleFormatProgram, 0, len(simpleFormatCache.entries))
	for _, prog := range simpleFormatCache.entries {
		programs = append(programs, prog)
	}
	simpleFormatCache.Unlock()

	for _, prog := range programs {
		prog.resultMu.Lock()
		for _, v := range prog.resultCache {
			ScanValueRoots(v, visitor, seen)
		}
		prog.resultMu.Unlock()
		for i := range prog.fastResultVals {
			if bits := prog.fastResultVals[i].Load(); bits != 0 {
				ScanValueRoots(Value(bits), visitor, seen)
			}
		}
	}
}

func isFormatFlag(b byte) bool {
	switch b {
	case '-', '+', ' ', '#', '0':
		return true
	default:
		return false
	}
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
