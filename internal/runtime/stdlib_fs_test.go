package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

// runWithFSPath creates a temp dir and runs GScript source with fs and path libs registered.
// Returns the interpreter and the temp dir path (caller should defer os.RemoveAll(tmpDir)).
func runWithFSPath(t *testing.T, src string) (*Interpreter, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "gscript_fs_test_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("parse error: %v", err)
	}
	interp := New()
	interp.globals.Define("fs", TableValue(buildFSLib()))
	interp.globals.Define("path", TableValue(buildPathLib()))
	// Provide the temp dir as a global for tests to use
	interp.globals.Define("tmpDir", StringValue(tmpDir))
	if err := interp.Exec(prog); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("exec error: %v", err)
	}
	return interp, tmpDir
}

// ==================================================================
// fs.exists, fs.isfile, fs.isdir
// ==================================================================

func TestFS_Exists_File(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/testfile.txt"
		fs.writefile(p, "hello")
		result := fs.exists(p)
	`)
	defer os.RemoveAll(tmpDir)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

func TestFS_Exists_NonExistent(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		result := fs.exists(tmpDir .. "/nonexistent")
	`)
	defer os.RemoveAll(tmpDir)
	v := interp.GetGlobal("result")
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false, got %v", v)
	}
}

func TestFS_IsFile(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/testfile.txt"
		fs.writefile(p, "hello")
		r1 := fs.isfile(p)
		r2 := fs.isfile(tmpDir)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected isfile(file)=true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected isfile(dir)=false")
	}
}

func TestFS_IsDir(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/testfile.txt"
		fs.writefile(p, "hello")
		r1 := fs.isdir(tmpDir)
		r2 := fs.isdir(p)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected isdir(dir)=true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected isdir(file)=false")
	}
}

// ==================================================================
// fs.writefile, fs.readfile
// ==================================================================

func TestFS_WriteFile_ReadFile(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/hello.txt"
		ok := fs.writefile(p, "hello world")
		content := fs.readfile(p)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("writefile should return true")
	}
	if interp.GetGlobal("content").Str() != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", interp.GetGlobal("content").Str())
	}
}

func TestFS_ReadFile_NonExistent(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		content, err := fs.readfile(tmpDir .. "/nope.txt")
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("content").IsNil() {
		t.Errorf("expected nil for non-existent file, got %v", interp.GetGlobal("content"))
	}
	errMsg := interp.GetGlobal("err")
	if !errMsg.IsString() || errMsg.Str() == "" {
		t.Errorf("expected error message string, got %v", errMsg)
	}
}

// ==================================================================
// fs.appendfile
// ==================================================================

func TestFS_AppendFile(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/append.txt"
		fs.writefile(p, "hello")
		fs.appendfile(p, " world")
		content := fs.readfile(p)
	`)
	defer os.RemoveAll(tmpDir)
	if interp.GetGlobal("content").Str() != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", interp.GetGlobal("content").Str())
	}
}

// ==================================================================
// fs.stat
// ==================================================================

func TestFS_Stat(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/statfile.txt"
		fs.writefile(p, "data123")
		info := fs.stat(p)
		name := info.name
		size := info.size
		isdir := info.isdir
		isfile := info.isfile
	`)
	defer os.RemoveAll(tmpDir)
	if interp.GetGlobal("name").Str() != "statfile.txt" {
		t.Errorf("expected name='statfile.txt', got '%s'", interp.GetGlobal("name").Str())
	}
	if interp.GetGlobal("size").Int() != 7 {
		t.Errorf("expected size=7, got %v", interp.GetGlobal("size"))
	}
	if interp.GetGlobal("isdir").Bool() {
		t.Errorf("expected isdir=false")
	}
	if !interp.GetGlobal("isfile").Bool() {
		t.Errorf("expected isfile=true")
	}
}

func TestFS_Stat_NonExistent(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		info, err := fs.stat(tmpDir .. "/nope.txt")
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("info").IsNil() {
		t.Errorf("expected nil for non-existent, got %v", interp.GetGlobal("info"))
	}
	if interp.GetGlobal("err").Str() == "" {
		t.Errorf("expected error message")
	}
}

// ==================================================================
// fs.mkdir, fs.mkdirAll
// ==================================================================

func TestFS_Mkdir(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/newdir"
		ok := fs.mkdir(p)
		exists := fs.isdir(p)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("mkdir should return true")
	}
	if !interp.GetGlobal("exists").Bool() {
		t.Errorf("directory should exist after mkdir")
	}
}

func TestFS_MkdirAll(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/a/b/c"
		ok := fs.mkdirAll(p)
		exists := fs.isdir(p)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("mkdirAll should return true")
	}
	if !interp.GetGlobal("exists").Bool() {
		t.Errorf("nested directory should exist after mkdirAll")
	}
}

// ==================================================================
// fs.remove, fs.removeAll
// ==================================================================

func TestFS_Remove(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/removeme.txt"
		fs.writefile(p, "data")
		ok := fs.remove(p)
		exists := fs.exists(p)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("remove should return true")
	}
	if interp.GetGlobal("exists").Bool() {
		t.Errorf("file should not exist after remove")
	}
}

func TestFS_RemoveAll(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		base := tmpDir .. "/rmdir"
		fs.mkdirAll(base .. "/sub")
		fs.writefile(base .. "/sub/file.txt", "data")
		ok := fs.removeAll(base)
		exists := fs.exists(base)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("removeAll should return true")
	}
	if interp.GetGlobal("exists").Bool() {
		t.Errorf("directory should not exist after removeAll")
	}
}

// ==================================================================
// fs.rename
// ==================================================================

func TestFS_Rename(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		old := tmpDir .. "/old.txt"
		new := tmpDir .. "/new.txt"
		fs.writefile(old, "content")
		ok := fs.rename(old, new)
		oldExists := fs.exists(old)
		newExists := fs.exists(new)
		content := fs.readfile(new)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("rename should return true")
	}
	if interp.GetGlobal("oldExists").Bool() {
		t.Errorf("old path should not exist after rename")
	}
	if !interp.GetGlobal("newExists").Bool() {
		t.Errorf("new path should exist after rename")
	}
	if interp.GetGlobal("content").Str() != "content" {
		t.Errorf("expected 'content', got '%s'", interp.GetGlobal("content").Str())
	}
}

// ==================================================================
// fs.readdir
// ==================================================================

func TestFS_Readdir(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		fs.writefile(tmpDir .. "/a.txt", "aaa")
		fs.writefile(tmpDir .. "/b.txt", "bbb")
		fs.mkdir(tmpDir .. "/subdir")
		entries := fs.readdir(tmpDir)
		count := #entries
	`)
	defer os.RemoveAll(tmpDir)
	count := interp.GetGlobal("count").Int()
	if count != 3 {
		t.Errorf("expected 3 entries, got %d", count)
	}
}

func TestFS_Readdir_EntryFields(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		fs.writefile(tmpDir .. "/file.txt", "hello")
		entries := fs.readdir(tmpDir)
		entry := entries[1]
		name := entry.name
		isdir := entry.isdir
	`)
	defer os.RemoveAll(tmpDir)
	name := interp.GetGlobal("name").Str()
	if name != "file.txt" {
		t.Errorf("expected name='file.txt', got '%s'", name)
	}
	if interp.GetGlobal("isdir").Bool() {
		t.Errorf("expected isdir=false for file")
	}
}

// ==================================================================
// fs.glob
// ==================================================================

func TestFS_Glob(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		fs.writefile(tmpDir .. "/x.txt", "data")
		fs.writefile(tmpDir .. "/y.txt", "data")
		fs.writefile(tmpDir .. "/z.log", "data")
		matches := fs.glob(tmpDir .. "/*.txt")
		count := #matches
	`)
	defer os.RemoveAll(tmpDir)
	count := interp.GetGlobal("count").Int()
	if count != 2 {
		t.Errorf("expected 2 txt matches, got %d", count)
	}
}

// ==================================================================
// fs.copy
// ==================================================================

func TestFS_Copy(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		src := tmpDir .. "/src.txt"
		dst := tmpDir .. "/dst.txt"
		fs.writefile(src, "copy me")
		ok := fs.copy(src, dst)
		content := fs.readfile(dst)
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("copy should return true")
	}
	if interp.GetGlobal("content").Str() != "copy me" {
		t.Errorf("expected 'copy me', got '%s'", interp.GetGlobal("content").Str())
	}
}

// ==================================================================
// fs.tempdir, fs.tempfile
// ==================================================================

func TestFS_Tempdir(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		result := fs.tempdir()
	`)
	defer os.RemoveAll(tmpDir)
	v := interp.GetGlobal("result")
	if !v.IsString() || v.Str() == "" {
		t.Errorf("expected non-empty string, got %v", v)
	}
	if v.Str() != os.TempDir() {
		t.Errorf("expected '%s', got '%s'", os.TempDir(), v.Str())
	}
}

func TestFS_Tempfile(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		p, err := fs.tempfile(tmpDir, "test_")
		exists := false
		if p != nil {
			exists = fs.exists(p)
		}
	`)
	defer os.RemoveAll(tmpDir)
	p := interp.GetGlobal("p")
	if p.IsNil() {
		t.Fatalf("tempfile returned nil, err=%v", interp.GetGlobal("err"))
	}
	if !interp.GetGlobal("exists").Bool() {
		t.Errorf("temp file should exist at %s", p.Str())
	}
	// Clean up temp file
	os.Remove(p.Str())
}

// ==================================================================
// fs.cwd, fs.chdir
// ==================================================================

func TestFS_Cwd(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		result := fs.cwd()
	`)
	defer os.RemoveAll(tmpDir)
	v := interp.GetGlobal("result")
	if !v.IsString() || v.Str() == "" {
		t.Errorf("cwd should return non-empty string, got %v", v)
	}
}

func TestFS_Chdir(t *testing.T) {
	// Save and restore cwd
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	interp, tmpDir := runWithFSPath(t, `
		ok := fs.chdir(tmpDir)
		result := fs.cwd()
	`)
	defer os.RemoveAll(tmpDir)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("chdir should return true")
	}
	// On macOS, /tmp is a symlink to /private/tmp, so we need to resolve both
	expected, _ := filepath.EvalSymlinks(tmpDir)
	got, _ := filepath.EvalSymlinks(interp.GetGlobal("result").Str())
	if got != expected {
		t.Errorf("expected cwd='%s', got '%s'", expected, got)
	}
}

// ==================================================================
// fs error cases
// ==================================================================

func TestFS_Remove_NonExistent(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		ok, err := fs.remove(tmpDir .. "/nonexistent")
	`)
	defer os.RemoveAll(tmpDir)
	if interp.GetGlobal("ok").Truthy() {
		t.Errorf("remove of non-existent should not return truthy value")
	}
	if interp.GetGlobal("err").Str() == "" {
		t.Errorf("expected error message")
	}
}

func TestFS_Mkdir_AlreadyExists(t *testing.T) {
	interp, tmpDir := runWithFSPath(t, `
		fs.mkdir(tmpDir .. "/existsdir")
		ok, err := fs.mkdir(tmpDir .. "/existsdir")
	`)
	defer os.RemoveAll(tmpDir)
	if interp.GetGlobal("ok").Truthy() {
		t.Errorf("mkdir on existing dir should fail")
	}
	if interp.GetGlobal("err").Str() == "" {
		t.Errorf("expected error message")
	}
}

// ==================================================================
// fs.stat mode field
// ==================================================================

func TestFS_Stat_Mode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode format test not applicable on Windows")
	}
	interp, tmpDir := runWithFSPath(t, `
		p := tmpDir .. "/modefile.txt"
		fs.writefile(p, "data")
		info := fs.stat(p)
		mode := info.mode
	`)
	defer os.RemoveAll(tmpDir)
	mode := interp.GetGlobal("mode").Str()
	// Mode should be an octal string like "0644" or "0664"
	if !strings.HasPrefix(mode, "0") {
		t.Errorf("expected mode to start with '0', got '%s'", mode)
	}
}
