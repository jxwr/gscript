package runtime

import (
	"testing"

	"github.com/gscript/gscript/internal/ast"
	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

// lexerNew is a convenience wrapper for tokenizing source code in tests.
func lexerNew(src string) ([]lexer.Token, error) {
	return lexer.New(src).Tokenize()
}

// parserNew is a convenience wrapper for parsing tokens in tests.
func parserNew(tokens []lexer.Token) (*ast.Program, error) {
	return parser.New(tokens).Parse()
}

// runWithLib parses and executes source code with a custom library pre-registered.
// libName is the global name (e.g., "json"), and lib is the *Table for that library.
func runWithLib(t *testing.T, src string, libName string, lib *Table) *Interpreter {
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
	interp.globals.Define(libName, TableValue(lib))
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	return interp
}
