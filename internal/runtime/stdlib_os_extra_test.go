package runtime

import (
	"os"
	"testing"
)

func TestOsSetenvUnsetenv(t *testing.T) {
	interp := runProgram(t, `
		os.setenv("GSCRIPT_TEST_VAR", "hello")
		a := os.getenv("GSCRIPT_TEST_VAR")
		os.unsetenv("GSCRIPT_TEST_VAR")
		b := os.getenv("GSCRIPT_TEST_VAR")
	`)
	if interp.GetGlobal("a").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("a").Str())
	}
	if !interp.GetGlobal("b").IsNil() {
		t.Errorf("expected nil after unsetenv, got %v", interp.GetGlobal("b"))
	}
}

func TestOsArgs(t *testing.T) {
	interp := runProgram(t, `
		a := os.args()
	`)
	v := interp.GetGlobal("a")
	if !v.IsTable() {
		t.Errorf("expected table, got %s", v.TypeName())
	}
	// os.Args should have at least one entry (the test binary)
	if v.Table().Length() < 1 {
		t.Errorf("expected at least 1 arg, got %d", v.Table().Length())
	}
}

func TestOsHostname(t *testing.T) {
	interp := runProgram(t, `
		h := os.hostname()
	`)
	v := interp.GetGlobal("h")
	expected, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname() failed: %v", err)
	}
	if v.Str() != expected {
		t.Errorf("expected '%s', got '%s'", expected, v.Str())
	}
}

func TestOsGetpid(t *testing.T) {
	interp := runProgram(t, `
		pid := os.getpid()
	`)
	v := interp.GetGlobal("pid")
	if !v.IsInt() || v.Int() <= 0 {
		t.Errorf("expected positive integer PID, got %v", v)
	}
	if v.Int() != int64(os.Getpid()) {
		t.Errorf("expected %d, got %d", os.Getpid(), v.Int())
	}
}

func TestOsExpand(t *testing.T) {
	os.Setenv("GSCRIPT_EXPAND_TEST", "world")
	defer os.Unsetenv("GSCRIPT_EXPAND_TEST")

	interp := runProgram(t, `
		result := os.expand("hello $GSCRIPT_EXPAND_TEST")
	`)
	if interp.GetGlobal("result").Str() != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", interp.GetGlobal("result").Str())
	}
}
