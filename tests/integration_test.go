package tests_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

// runGScript executes a GScript source string and captures its print output.
func runGScript(t *testing.T, src string) string {
	t.Helper()
	interp := runtime.New()
	var buf bytes.Buffer

	// Override print to capture output
	interp.SetGlobal("print", runtime.FunctionValue(&runtime.GoFunction{
		Name: "print",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = a.String()
			}
			fmt.Fprintln(&buf, strings.Join(parts, "\t"))
			return nil, nil
		},
	}))

	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return buf.String()
}

// runGScriptFile reads a .gs file and executes it.
func runGScriptFile(t *testing.T, filename string) string {
	t.Helper()
	src, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read %s: %v", filename, err)
	}
	return runGScript(t, string(src))
}

func TestBasicArithmetic(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "01_basic.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"3", "7", "20", "2.5", "1", "256"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestStrings(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "02_strings.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"hello world", "5", "HELLO", "ell", "ababab"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestControlFlow(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "03_control.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"medium", "55", "128"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestFunctions(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "04_functions.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// add(3,4) = 7
	// divmod(17,5): q = 3.4, r = 2
	// fib(10) = 55
	expected := []string{"7", "3.4", "2", "55"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestTables(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "05_tables.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"5",      // #arr
		"10",     // arr[1]
		"30",     // arr[3]
		"6",      // #arr after insert
		"20",     // arr[1] after remove
		"alice",  // person.name
		"30",     // person["age"]
		"5",      // matrix[2][2]
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestClosures(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "06_closures.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"1", "2", "11", "3", "12"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestMetatable(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "07_metatable.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"Rex says woof",
		"Whiskers says meow",
		"4",  // v3.x
		"6",  // v3.y
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestCoroutine(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "08_coroutine.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"1", "4", "9", "16", "25"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestError(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "09_error.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"false",              // ok (pcall caught error)
		"something went wrong", // err message
		"true",               // ok2 (pcall success)
		"42",                 // val
		"false",              // ok3 (error object)
		"404",                // e.code
		"not found",          // e.msg
		"false",              // ok4 (assert failed)
		"math is broken",     // e2
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestStringOps(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "10_string_ops.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{
		"13",               // string.len
		"hello, world!",    // string.lower
		"HELLO, WORLD!",    // string.upper
		"Hello",            // string.sub(s, 1, 5)
		"8\t12",            // string.find returns two values
		"HeLLo, WorLd!\t3", // string.gsub returns string and count
		"1 + 2 = 3",        // string.format
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestIterator(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "11_iterator.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// ipairs output: 1 a, 2 b, 3 c, 4 d
	// pairs sorted output: x 1, y 2, z 3
	expected := []string{
		"1\ta", "2\tb", "3\tc", "4\td",
		"x\t1", "y\t2", "z\t3",
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

func TestAdvanced(t *testing.T) {
	out := runGScriptFile(t, filepath.Join(".", "12_advanced.gs"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"832040"}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d:\n%s", len(expected), len(lines), out)
	}
	for i, exp := range expected {
		got := strings.TrimSpace(lines[i])
		if got != exp {
			t.Errorf("line %d: expected %q, got %q", i+1, exp, got)
		}
	}
}

// TestExamples runs example programs to make sure they don't error.
func TestExamples(t *testing.T) {
	examples := []string{
		filepath.Join("..", "examples", "fib.gs"),
		filepath.Join("..", "examples", "counter.gs"),
		filepath.Join("..", "examples", "class.gs"),
	}
	for _, ex := range examples {
		t.Run(filepath.Base(ex), func(t *testing.T) {
			src, err := os.ReadFile(ex)
			if err != nil {
				t.Fatalf("failed to read %s: %v", ex, err)
			}
			// Just run it and make sure no errors
			interp := runtime.New()
			tokens, err := lexer.New(string(src)).Tokenize()
			if err != nil {
				t.Fatalf("lexer error: %v", err)
			}
			prog, err := parser.New(tokens).Parse()
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if err := interp.Exec(prog); err != nil {
				t.Fatalf("runtime error: %v", err)
			}
		})
	}
}
