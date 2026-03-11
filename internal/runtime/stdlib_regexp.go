package runtime

import (
	"fmt"
	"regexp"
)

// makeReObject wraps a compiled *regexp.Regexp into a GScript table with methods.
func makeReObject(re *regexp.Regexp) *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "re." + name,
			Fn:   fn,
		}))
	}

	// re.pattern — the pattern string
	t.RawSet(StringValue("pattern"), StringValue(re.String()))

	// re.match(str) → bool
	set("match", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 're.match'")
		}
		return []Value{BoolValue(re.MatchString(args[0].Str()))}, nil
	})

	// re.find(str) → string or nil
	set("find", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 're.find'")
		}
		m := re.FindString(args[0].Str())
		if m == "" && !re.MatchString(args[0].Str()) {
			return []Value{NilValue()}, nil
		}
		return []Value{StringValue(m)}, nil
	})

	// re.findSubmatch(str) → table or nil
	set("findSubmatch", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 're.findSubmatch'")
		}
		matches := re.FindStringSubmatch(args[0].Str())
		if matches == nil {
			return []Value{NilValue()}, nil
		}
		tbl := NewTable()
		for i, m := range matches {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(m))
		}
		return []Value{TableValue(tbl)}, nil
	})

	// re.findAll(str [, n]) → table
	set("findAll", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 're.findAll'")
		}
		n := -1
		if len(args) >= 2 {
			n = int(toInt(args[1]))
		}
		matches := re.FindAllString(args[0].Str(), n)
		tbl := NewTable()
		for i, m := range matches {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(m))
		}
		return []Value{TableValue(tbl)}, nil
	})

	// re.findAllSubmatch(str [, n]) → table of tables
	set("findAllSubmatch", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 're.findAllSubmatch'")
		}
		n := -1
		if len(args) >= 2 {
			n = int(toInt(args[1]))
		}
		allMatches := re.FindAllStringSubmatch(args[0].Str(), n)
		tbl := NewTable()
		for i, matches := range allMatches {
			sub := NewTable()
			for j, m := range matches {
				sub.RawSet(IntValue(int64(j+1)), StringValue(m))
			}
			tbl.RawSet(IntValue(int64(i+1)), TableValue(sub))
		}
		return []Value{TableValue(tbl)}, nil
	})

	// re.replace(str, repl) → string (replace first)
	set("replace", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 're.replace'")
		}
		str := args[0].Str()
		repl := args[1].Str()
		loc := re.FindStringIndex(str)
		if loc == nil {
			return []Value{StringValue(str)}, nil
		}
		result := str[:loc[0]] + repl + str[loc[1]:]
		return []Value{StringValue(result)}, nil
	})

	// re.replaceAll(str, repl) → string
	set("replaceAll", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 're.replaceAll'")
		}
		return []Value{StringValue(re.ReplaceAllString(args[0].Str(), args[1].Str()))}, nil
	})

	// re.split(str [, n]) → table of strings
	set("split", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 're.split'")
		}
		n := -1
		if len(args) >= 2 {
			n = int(toInt(args[1]))
		}
		parts := re.Split(args[0].Str(), n)
		tbl := NewTable()
		for i, p := range parts {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(p))
		}
		return []Value{TableValue(tbl)}, nil
	})

	// re.numSubexp() → int
	set("numSubexp", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(re.NumSubexp()))}, nil
	})

	return t
}

// buildRegexpLib creates the "regexp" standard library table.
func buildRegexpLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "regexp." + name,
			Fn:   fn,
		}))
	}

	// regexp.compile(pattern) → re object or nil, errMsg
	set("compile", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'regexp.compile'")
		}
		pattern := args[0].Str()
		re, err := regexp.Compile(pattern)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{TableValue(makeReObject(re)), NilValue()}, nil
	})

	// regexp.mustCompile(pattern) → re object (errors if invalid)
	set("mustCompile", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'regexp.mustCompile'")
		}
		pattern := args[0].Str()
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("regexp.mustCompile: %v", err)
		}
		return []Value{TableValue(makeReObject(re))}, nil
	})

	// regexp.match(pattern, str) → bool
	set("match", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'regexp.match'")
		}
		matched, err := regexp.MatchString(args[0].Str(), args[1].Str())
		if err != nil {
			return nil, fmt.Errorf("regexp.match: %v", err)
		}
		return []Value{BoolValue(matched)}, nil
	})

	// regexp.find(pattern, str) → string or nil
	set("find", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'regexp.find'")
		}
		re, err := regexp.Compile(args[0].Str())
		if err != nil {
			return nil, fmt.Errorf("regexp.find: %v", err)
		}
		m := re.FindString(args[1].Str())
		if m == "" && !re.MatchString(args[1].Str()) {
			return []Value{NilValue()}, nil
		}
		return []Value{StringValue(m)}, nil
	})

	// regexp.findAll(pattern, str [, n]) → table
	set("findAll", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'regexp.findAll'")
		}
		re, err := regexp.Compile(args[0].Str())
		if err != nil {
			return nil, fmt.Errorf("regexp.findAll: %v", err)
		}
		n := -1
		if len(args) >= 3 {
			n = int(toInt(args[2]))
		}
		matches := re.FindAllString(args[1].Str(), n)
		tbl := NewTable()
		for i, m := range matches {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(m))
		}
		return []Value{TableValue(tbl)}, nil
	})

	// regexp.replace(pattern, str, repl) → string (replace first)
	set("replace", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'regexp.replace'")
		}
		re, err := regexp.Compile(args[0].Str())
		if err != nil {
			return nil, fmt.Errorf("regexp.replace: %v", err)
		}
		str := args[1].Str()
		repl := args[2].Str()
		loc := re.FindStringIndex(str)
		if loc == nil {
			return []Value{StringValue(str)}, nil
		}
		result := str[:loc[0]] + repl + str[loc[1]:]
		return []Value{StringValue(result)}, nil
	})

	// regexp.replaceAll(pattern, str, repl) → string
	set("replaceAll", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'regexp.replaceAll'")
		}
		re, err := regexp.Compile(args[0].Str())
		if err != nil {
			return nil, fmt.Errorf("regexp.replaceAll: %v", err)
		}
		return []Value{StringValue(re.ReplaceAllString(args[1].Str(), args[2].Str()))}, nil
	})

	// regexp.split(pattern, str [, n]) → table
	set("split", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'regexp.split'")
		}
		re, err := regexp.Compile(args[0].Str())
		if err != nil {
			return nil, fmt.Errorf("regexp.split: %v", err)
		}
		n := -1
		if len(args) >= 3 {
			n = int(toInt(args[2]))
		}
		parts := re.Split(args[1].Str(), n)
		tbl := NewTable()
		for i, p := range parts {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(p))
		}
		return []Value{TableValue(tbl)}, nil
	})

	return t
}
