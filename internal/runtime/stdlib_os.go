package runtime

import (
	"fmt"
	"os"
	"time"
)

// startTime is used by os.clock() to measure CPU time (approximated as wall time).
var startTime = time.Now()

// buildOSLib creates the "os" standard library table.
func buildOSLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "os." + name,
			Fn:   fn,
		}))
	}

	// os.time() -> unix timestamp
	set("time", func(args []Value) ([]Value, error) {
		return []Value{IntValue(time.Now().Unix())}, nil
	})

	// os.clock() -> elapsed CPU time in seconds (approximated as wall time)
	set("clock", func(args []Value) ([]Value, error) {
		elapsed := time.Since(startTime).Seconds()
		return []Value{FloatValue(elapsed)}, nil
	})

	// os.date([format [, time]]) -> formatted date string
	set("date", func(args []Value) ([]Value, error) {
		format := "%c"
		if len(args) >= 1 && args[0].IsString() {
			format = args[0].Str()
		}
		var tm time.Time
		if len(args) >= 2 {
			tm = time.Unix(toInt(args[1]), 0)
		} else {
			tm = time.Now()
		}

		result := luaDateFormat(format, tm)
		return []Value{StringValue(result)}, nil
	})

	// os.exit([code])
	set("exit", func(args []Value) ([]Value, error) {
		code := 0
		if len(args) >= 1 {
			code = int(toInt(args[0]))
		}
		os.Exit(code)
		return nil, nil // unreachable
	})

	// os.getenv(name) -> string or nil
	set("getenv", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'os.getenv' (string expected)")
		}
		val, ok := os.LookupEnv(args[0].Str())
		if !ok {
			return []Value{NilValue()}, nil
		}
		return []Value{StringValue(val)}, nil
	})

	// os.remove(filename) -> true or nil, error
	set("remove", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'os.remove' (string expected)")
		}
		err := os.Remove(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// os.rename(old, new) -> true or nil, error
	set("rename", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'os.rename' (string expected)")
		}
		err := os.Rename(args[0].Str(), args[1].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// os.tmpname() -> string
	set("tmpname", func(args []Value) ([]Value, error) {
		f, err := os.CreateTemp("", "gscript_*")
		if err != nil {
			return nil, err
		}
		name := f.Name()
		f.Close()
		return []Value{StringValue(name)}, nil
	})

	// os.setenv(key, value) -- set environment variable
	set("setenv", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'os.setenv' (string expected)")
		}
		err := os.Setenv(args[0].Str(), args[1].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// os.unsetenv(key) -- unset environment variable
	set("unsetenv", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'os.unsetenv' (string expected)")
		}
		err := os.Unsetenv(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// os.args() -- return table of os.Args (command line arguments)
	set("args", func(args []Value) ([]Value, error) {
		tbl := NewTable()
		for i, arg := range os.Args {
			tbl.RawSet(IntValue(int64(i+1)), StringValue(arg))
		}
		return []Value{TableValue(tbl)}, nil
	})

	// os.hostname() -- get hostname
	set("hostname", func(args []Value) ([]Value, error) {
		name, err := os.Hostname()
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(name)}, nil
	})

	// os.getpid() -- get process ID as int
	set("getpid", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(os.Getpid()))}, nil
	})

	// os.expand(s) -- expand $VAR and ${VAR} in string using os.Expand
	set("expand", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'os.expand' (string expected)")
		}
		result := os.ExpandEnv(args[0].Str())
		return []Value{StringValue(result)}, nil
	})

	return t
}

// luaDateFormat converts a Lua-style date format to a Go time string.
func luaDateFormat(format string, t time.Time) string {
	// Replace Lua format specifiers with Go equivalents
	result := format
	replacements := map[string]string{
		"%Y": "2006",
		"%y": "06",
		"%m": "01",
		"%d": "02",
		"%H": "15",
		"%M": "04",
		"%S": "05",
		"%A": "Monday",
		"%a": "Mon",
		"%B": "January",
		"%b": "Jan",
		"%p": "PM",
		"%c": "Mon Jan  2 15:04:05 2006",
		"%X": "15:04:05",
		"%x": "01/02/06",
		"%%": "%",
	}
	for lua, goFmt := range replacements {
		if goFmt == "%" {
			// Handle %% separately to avoid double replacement
			continue
		}
		for {
			idx := findLuaFormatSpec(result, lua)
			if idx < 0 {
				break
			}
			result = result[:idx] + t.Format(goFmt) + result[idx+len(lua):]
		}
	}
	// Handle %% last
	for {
		idx := findLuaFormatSpec(result, "%%")
		if idx < 0 {
			break
		}
		result = result[:idx] + "%" + result[idx+2:]
	}
	return result
}

// findLuaFormatSpec finds the index of a Lua format specifier in a string.
func findLuaFormatSpec(s, spec string) int {
	for i := 0; i <= len(s)-len(spec); i++ {
		if s[i:i+len(spec)] == spec {
			return i
		}
	}
	return -1
}
