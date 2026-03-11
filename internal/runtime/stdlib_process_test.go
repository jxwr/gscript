package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

func execOnInterp(t *testing.T, interp *Interpreter, src string) {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
}

func TestProcessWhich(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))
	execOnInterp(t, interp, `result := process.which("ls")`)

	v := interp.GetGlobal("result")
	if v.IsNil() {
		t.Errorf("expected 'ls' to be found in PATH")
	}
	if !strings.Contains(v.Str(), "ls") {
		t.Errorf("expected path to contain 'ls', got '%s'", v.Str())
	}
}

func TestProcessWhichNotFound(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))
	execOnInterp(t, interp, `result := process.which("__nonexistent_binary_12345__")`)

	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("expected nil for nonexistent binary, got %v", v)
	}
}

func TestProcessRun(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))
	execOnInterp(t, interp, `result := process.run("echo hello")`)

	v := interp.GetGlobal("result")
	if !v.IsTable() {
		t.Fatalf("expected table result, got %s", v.TypeName())
	}
	tbl := v.Table()
	if !tbl.RawGet(StringValue("ok")).Bool() {
		t.Errorf("expected ok=true")
	}
	stdout := tbl.RawGet(StringValue("stdout")).Str()
	if strings.TrimSpace(stdout) != "hello" {
		t.Errorf("expected stdout='hello', got '%s'", stdout)
	}
	if tbl.RawGet(StringValue("code")).Int() != 0 {
		t.Errorf("expected code=0, got %v", tbl.RawGet(StringValue("code")))
	}
}

func TestProcessShell(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))
	execOnInterp(t, interp, `result := process.shell("echo hello && echo world")`)

	v := interp.GetGlobal("result")
	tbl := v.Table()
	if !tbl.RawGet(StringValue("ok")).Bool() {
		t.Errorf("expected ok=true")
	}
	stdout := tbl.RawGet(StringValue("stdout")).Str()
	if !strings.Contains(stdout, "hello") || !strings.Contains(stdout, "world") {
		t.Errorf("expected stdout to contain 'hello' and 'world', got '%s'", stdout)
	}
}

func TestProcessPid(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))
	execOnInterp(t, interp, `pid := process.pid()`)

	v := interp.GetGlobal("pid")
	if v.Int() != int64(os.Getpid()) {
		t.Errorf("expected %d, got %d", os.Getpid(), v.Int())
	}
}

func TestProcessEnv(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))

	os.Setenv("GSCRIPT_TEST_PROC_ENV", "test_value")
	defer os.Unsetenv("GSCRIPT_TEST_PROC_ENV")

	execOnInterp(t, interp, `env := process.env()`)

	v := interp.GetGlobal("env")
	if !v.IsTable() {
		t.Fatalf("expected table, got %s", v.TypeName())
	}
	val := v.Table().RawGet(StringValue("GSCRIPT_TEST_PROC_ENV"))
	if val.Str() != "test_value" {
		t.Errorf("expected 'test_value', got '%s'", val.Str())
	}
}

func TestProcessExec(t *testing.T) {
	interp := New()
	interp.globals.Define("process", TableValue(buildProcessLib()))
	execOnInterp(t, interp, `result := process.exec("echo", "hello")`)

	v := interp.GetGlobal("result")
	if strings.TrimSpace(v.Str()) != "hello" {
		t.Errorf("expected 'hello', got '%s'", v.Str())
	}
}
