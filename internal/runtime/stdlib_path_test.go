package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

// runWithPath creates an interpreter with path lib and executes source.
func runWithPath(t *testing.T, src string) *Interpreter {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	interp := New()
	interp.globals.Define("path", TableValue(buildPathLib()))
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	return interp
}

// ==================================================================
// path.join
// ==================================================================

func TestPath_Join(t *testing.T) {
	interp := runWithPath(t, `
		result := path.join("a", "b", "c")
	`)
	expected := filepath.Join("a", "b", "c")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

func TestPath_Join_WithSlashes(t *testing.T) {
	interp := runWithPath(t, `
		result := path.join("/usr", "local", "bin")
	`)
	expected := filepath.Join("/usr", "local", "bin")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// path.dir
// ==================================================================

func TestPath_Dir(t *testing.T) {
	interp := runWithPath(t, `
		result := path.dir("/usr/local/bin/go")
	`)
	expected := filepath.Dir("/usr/local/bin/go")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// path.base
// ==================================================================

func TestPath_Base(t *testing.T) {
	interp := runWithPath(t, `
		result := path.base("/usr/local/bin/go")
	`)
	expected := filepath.Base("/usr/local/bin/go")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// path.ext
// ==================================================================

func TestPath_Ext(t *testing.T) {
	interp := runWithPath(t, `
		r1 := path.ext("file.go")
		r2 := path.ext("archive.tar.gz")
		r3 := path.ext("noext")
	`)
	if interp.GetGlobal("r1").Str() != ".go" {
		t.Errorf("expected '.go', got '%s'", interp.GetGlobal("r1").Str())
	}
	if interp.GetGlobal("r2").Str() != ".gz" {
		t.Errorf("expected '.gz', got '%s'", interp.GetGlobal("r2").Str())
	}
	if interp.GetGlobal("r3").Str() != "" {
		t.Errorf("expected '', got '%s'", interp.GetGlobal("r3").Str())
	}
}

// ==================================================================
// path.abs
// ==================================================================

func TestPath_Abs(t *testing.T) {
	interp := runWithPath(t, `
		result := path.abs(".")
	`)
	expected, _ := filepath.Abs(".")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// path.isAbs
// ==================================================================

func TestPath_IsAbs(t *testing.T) {
	interp := runWithPath(t, `
		r1 := path.isAbs("/usr/bin")
		r2 := path.isAbs("relative/path")
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected /usr/bin to be absolute")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected relative/path to not be absolute")
	}
}

// ==================================================================
// path.clean
// ==================================================================

func TestPath_Clean(t *testing.T) {
	interp := runWithPath(t, `
		result := path.clean("/usr//local/../local/bin/./go")
	`)
	expected := filepath.Clean("/usr//local/../local/bin/./go")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// path.split
// ==================================================================

func TestPath_Split(t *testing.T) {
	interp := runWithPath(t, `
		dir, file := path.split("/usr/local/bin/go")
	`)
	expectedDir, expectedFile := filepath.Split("/usr/local/bin/go")
	if interp.GetGlobal("dir").Str() != expectedDir {
		t.Errorf("expected dir='%s', got '%s'", expectedDir, interp.GetGlobal("dir").Str())
	}
	if interp.GetGlobal("file").Str() != expectedFile {
		t.Errorf("expected file='%s', got '%s'", expectedFile, interp.GetGlobal("file").Str())
	}
}

// ==================================================================
// path.match
// ==================================================================

func TestPath_Match(t *testing.T) {
	interp := runWithPath(t, `
		r1 := path.match("*.go", "main.go")
		r2 := path.match("*.go", "main.txt")
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected match('*.go', 'main.go') = true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected match('*.go', 'main.txt') = false")
	}
}

// ==================================================================
// path.rel
// ==================================================================

func TestPath_Rel(t *testing.T) {
	interp := runWithPath(t, `
		result := path.rel("/usr/local", "/usr/local/bin/go")
	`)
	expected, _ := filepath.Rel("/usr/local", "/usr/local/bin/go")
	if interp.GetGlobal("result").Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// path.separator, path.listSeparator
// ==================================================================

func TestPath_Separator(t *testing.T) {
	interp := runWithPath(t, `
		sep := path.separator
		lsep := path.listSeparator
	`)
	if interp.GetGlobal("sep").Str() != string(os.PathSeparator) {
		t.Errorf("expected separator='%s', got '%s'", string(os.PathSeparator), interp.GetGlobal("sep").Str())
	}
	if interp.GetGlobal("lsep").Str() != string(os.PathListSeparator) {
		t.Errorf("expected listSeparator='%s', got '%s'", string(os.PathListSeparator), interp.GetGlobal("lsep").Str())
	}
}
