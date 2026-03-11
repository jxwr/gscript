package runtime

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// buildFSLib creates the "fs" standard library table.
func buildFSLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "fs." + name,
			Fn:   fn,
		}))
	}

	// fs.exists(path) -> bool
	set("exists", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.exists' (string expected)")
		}
		p := args[0].Str()
		_, err := os.Stat(p)
		return []Value{BoolValue(err == nil)}, nil
	})

	// fs.isfile(path) -> bool
	set("isfile", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.isfile' (string expected)")
		}
		info, err := os.Stat(args[0].Str())
		if err != nil {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(!info.IsDir())}, nil
	})

	// fs.isdir(path) -> bool
	set("isdir", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.isdir' (string expected)")
		}
		info, err := os.Stat(args[0].Str())
		if err != nil {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(info.IsDir())}, nil
	})

	// fs.stat(path) -> table or nil, errMsg
	set("stat", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.stat' (string expected)")
		}
		info, err := os.Stat(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		tbl := NewTable()
		tbl.RawSet(StringValue("name"), StringValue(info.Name()))
		tbl.RawSet(StringValue("size"), IntValue(info.Size()))
		tbl.RawSet(StringValue("mtime"), FloatValue(float64(info.ModTime().Unix())))
		tbl.RawSet(StringValue("isdir"), BoolValue(info.IsDir()))
		tbl.RawSet(StringValue("isfile"), BoolValue(!info.IsDir()))
		tbl.RawSet(StringValue("mode"), StringValue(fmt.Sprintf("0%o", info.Mode().Perm())))
		return []Value{TableValue(tbl)}, nil
	})

	// fs.readfile(path) -> string or nil, errMsg
	set("readfile", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.readfile' (string expected)")
		}
		data, err := os.ReadFile(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(data))}, nil
	})

	// fs.writefile(path, content) -> true or nil, errMsg
	set("writefile", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'fs.writefile' (path and content expected)")
		}
		err := os.WriteFile(args[0].Str(), []byte(args[1].Str()), 0644)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.appendfile(path, content) -> true or nil, errMsg
	set("appendfile", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'fs.appendfile' (path and content expected)")
		}
		f, err := os.OpenFile(args[0].Str(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		defer f.Close()
		_, err = f.WriteString(args[1].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.remove(path) -> true or nil, errMsg
	set("remove", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.remove' (string expected)")
		}
		err := os.Remove(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.removeAll(path) -> true or nil, errMsg
	set("removeAll", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.removeAll' (string expected)")
		}
		err := os.RemoveAll(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.rename(oldpath, newpath) -> true or nil, errMsg
	set("rename", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'fs.rename' (oldpath and newpath expected)")
		}
		err := os.Rename(args[0].Str(), args[1].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.mkdir(path) -> true or nil, errMsg
	set("mkdir", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.mkdir' (string expected)")
		}
		err := os.Mkdir(args[0].Str(), 0755)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.mkdirAll(path) -> true or nil, errMsg
	set("mkdirAll", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.mkdirAll' (string expected)")
		}
		err := os.MkdirAll(args[0].Str(), 0755)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.readdir(path) -> table (array of {name, isdir, size}) or nil, errMsg
	set("readdir", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.readdir' (string expected)")
		}
		entries, err := os.ReadDir(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		result := NewTable()
		for i, entry := range entries {
			entryTbl := NewTable()
			entryTbl.RawSet(StringValue("name"), StringValue(entry.Name()))
			entryTbl.RawSet(StringValue("isdir"), BoolValue(entry.IsDir()))
			info, infoErr := entry.Info()
			if infoErr == nil {
				entryTbl.RawSet(StringValue("size"), IntValue(info.Size()))
			} else {
				entryTbl.RawSet(StringValue("size"), IntValue(0))
			}
			result.RawSet(IntValue(int64(i+1)), TableValue(entryTbl))
		}
		return []Value{TableValue(result)}, nil
	})

	// fs.glob(pattern) -> table (array of matching paths) or nil, errMsg
	set("glob", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.glob' (string expected)")
		}
		matches, err := filepath.Glob(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		result := NewTable()
		for i, m := range matches {
			result.RawSet(IntValue(int64(i+1)), StringValue(m))
		}
		return []Value{TableValue(result)}, nil
	})

	// fs.copy(src, dst) -> true or nil, errMsg
	set("copy", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'fs.copy' (src and dst expected)")
		}
		srcPath := args[0].Str()
		dstPath := args[1].Str()

		srcFile, err := os.Open(srcPath)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		defer srcFile.Close()

		dstFile, err := os.Create(dstPath)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	// fs.tempdir() -> string
	set("tempdir", func(args []Value) ([]Value, error) {
		return []Value{StringValue(os.TempDir())}, nil
	})

	// fs.tempfile([dir [, prefix]]) -> string (path) or nil, errMsg
	set("tempfile", func(args []Value) ([]Value, error) {
		dir := ""
		prefix := ""
		if len(args) >= 1 && !args[0].IsNil() {
			dir = args[0].Str()
		}
		if len(args) >= 2 && !args[1].IsNil() {
			prefix = args[1].Str()
		}
		f, err := os.CreateTemp(dir, prefix)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		name := f.Name()
		f.Close()
		return []Value{StringValue(name)}, nil
	})

	// fs.cwd() -> string or nil, errMsg
	set("cwd", func(args []Value) ([]Value, error) {
		dir, err := os.Getwd()
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(dir)}, nil
	})

	// fs.chdir(path) -> true or nil, errMsg
	set("chdir", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'fs.chdir' (string expected)")
		}
		err := os.Chdir(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{BoolValue(true)}, nil
	})

	return t
}
