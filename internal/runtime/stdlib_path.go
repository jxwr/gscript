package runtime

import (
	"fmt"
	"os"
	"path/filepath"
)

// buildPathLib creates the "path" standard library table.
func buildPathLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "path." + name,
			Fn:   fn,
		}))
	}

	// Constants
	t.RawSet(StringValue("separator"), StringValue(string(os.PathSeparator)))
	t.RawSet(StringValue("listSeparator"), StringValue(string(os.PathListSeparator)))

	// path.join(...) -> string
	set("join", func(args []Value) ([]Value, error) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, a.Str())
		}
		return []Value{StringValue(filepath.Join(parts...))}, nil
	})

	// path.dir(p) -> string
	set("dir", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.dir' (string expected)")
		}
		return []Value{StringValue(filepath.Dir(args[0].Str()))}, nil
	})

	// path.base(p) -> string
	set("base", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.base' (string expected)")
		}
		return []Value{StringValue(filepath.Base(args[0].Str()))}, nil
	})

	// path.ext(p) -> string
	set("ext", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.ext' (string expected)")
		}
		return []Value{StringValue(filepath.Ext(args[0].Str()))}, nil
	})

	// path.abs(p) -> string or nil, errMsg
	set("abs", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.abs' (string expected)")
		}
		abs, err := filepath.Abs(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(abs)}, nil
	})

	// path.isAbs(p) -> bool
	set("isAbs", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.isAbs' (string expected)")
		}
		return []Value{BoolValue(filepath.IsAbs(args[0].Str()))}, nil
	})

	// path.clean(p) -> string
	set("clean", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.clean' (string expected)")
		}
		return []Value{StringValue(filepath.Clean(args[0].Str()))}, nil
	})

	// path.split(p) -> dir, file
	set("split", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'path.split' (string expected)")
		}
		dir, file := filepath.Split(args[0].Str())
		return []Value{StringValue(dir), StringValue(file)}, nil
	})

	// path.match(pattern, name) -> bool, errMsg
	set("match", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'path.match' (pattern and name expected)")
		}
		matched, err := filepath.Match(args[0].Str(), args[1].Str())
		if err != nil {
			return []Value{BoolValue(false), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(matched)}, nil
	})

	// path.rel(basepath, targpath) -> string or nil, errMsg
	set("rel", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'path.rel' (basepath and targpath expected)")
		}
		rel, err := filepath.Rel(args[0].Str(), args[1].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(rel)}, nil
	})

	return t
}
