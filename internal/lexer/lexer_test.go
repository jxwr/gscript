package lexer

import (
	"testing"
)

// ============================================================
// Helper
// ============================================================

func expectTokens(t *testing.T, input string, expected []Token) {
	t.Helper()
	lex := New(input)
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("Tokenize(%q) returned error: %v", input, err)
	}
	// Strip the trailing EOF for comparison convenience
	if len(tokens) > 0 && tokens[len(tokens)-1].Type == TOKEN_EOF {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) != len(expected) {
		t.Fatalf("Tokenize(%q): expected %d tokens, got %d\ntokens: %v", input, len(expected), len(tokens), tokens)
	}
	for i, exp := range expected {
		got := tokens[i]
		if got.Type != exp.Type {
			t.Errorf("token[%d] type: expected %v, got %v (value=%q)", i, exp.Type, got.Type, got.Value)
		}
		if exp.Value != "" && got.Value != exp.Value {
			t.Errorf("token[%d] value: expected %q, got %q", i, exp.Value, got.Value)
		}
		if exp.Line != 0 && got.Line != exp.Line {
			t.Errorf("token[%d] line: expected %d, got %d", i, exp.Line, got.Line)
		}
		if exp.Column != 0 && got.Column != exp.Column {
			t.Errorf("token[%d] column: expected %d, got %d", i, exp.Column, got.Column)
		}
	}
}

// ============================================================
// Token Type Tests
// ============================================================

func TestSingleCharTokens(t *testing.T) {
	expectTokens(t, "( ) { } [ ] , ; :", []Token{
		{Type: TOKEN_LPAREN, Value: "("},
		{Type: TOKEN_RPAREN, Value: ")"},
		{Type: TOKEN_LBRACE, Value: "{"},
		{Type: TOKEN_RBRACE, Value: "}"},
		{Type: TOKEN_LBRACKET, Value: "["},
		{Type: TOKEN_RBRACKET, Value: "]"},
		{Type: TOKEN_COMMA, Value: ","},
		{Type: TOKEN_SEMICOLON, Value: ";"},
		{Type: TOKEN_COLON, Value: ":"},
	})
}

func TestMathOperators(t *testing.T) {
	expectTokens(t, "+ - * / %", []Token{
		{Type: TOKEN_PLUS, Value: "+"},
		{Type: TOKEN_MINUS, Value: "-"},
		{Type: TOKEN_STAR, Value: "*"},
		{Type: TOKEN_SLASH, Value: "/"},
		{Type: TOKEN_PERCENT, Value: "%"},
	})
}

func TestPowerOperator(t *testing.T) {
	expectTokens(t, "2 ** 3", []Token{
		{Type: TOKEN_NUMBER, Value: "2"},
		{Type: TOKEN_POW, Value: "**"},
		{Type: TOKEN_NUMBER, Value: "3"},
	})
}

func TestComparisonOperators(t *testing.T) {
	expectTokens(t, "< <= > >= == !=", []Token{
		{Type: TOKEN_LT, Value: "<"},
		{Type: TOKEN_LE, Value: "<="},
		{Type: TOKEN_GT, Value: ">"},
		{Type: TOKEN_GE, Value: ">="},
		{Type: TOKEN_EQ, Value: "=="},
		{Type: TOKEN_NEQ, Value: "!="},
	})
}

func TestAssignmentOperators(t *testing.T) {
	expectTokens(t, "= := += -= *= /=", []Token{
		{Type: TOKEN_ASSIGN, Value: "="},
		{Type: TOKEN_DECLARE, Value: ":="},
		{Type: TOKEN_PLUS_ASSIGN, Value: "+="},
		{Type: TOKEN_MINUS_ASSIGN, Value: "-="},
		{Type: TOKEN_STAR_ASSIGN, Value: "*="},
		{Type: TOKEN_SLASH_ASSIGN, Value: "/="},
	})
}

func TestLogicOperators(t *testing.T) {
	expectTokens(t, "&& || !", []Token{
		{Type: TOKEN_AND, Value: "&&"},
		{Type: TOKEN_OR, Value: "||"},
		{Type: TOKEN_NOT, Value: "!"},
	})
}

func TestIncrementDecrement(t *testing.T) {
	expectTokens(t, "++ --", []Token{
		{Type: TOKEN_INC, Value: "++"},
		{Type: TOKEN_DEC, Value: "--"},
	})
}

func TestDotOperators(t *testing.T) {
	expectTokens(t, ". .. ...", []Token{
		{Type: TOKEN_DOT, Value: "."},
		{Type: TOKEN_CONCAT, Value: ".."},
		{Type: TOKEN_ELLIPSIS, Value: "..."},
	})
}

func TestLenOperator(t *testing.T) {
	expectTokens(t, "#", []Token{
		{Type: TOKEN_LEN, Value: "#"},
	})
}

// ============================================================
// Literal Tests
// ============================================================

func TestIntegerLiterals(t *testing.T) {
	expectTokens(t, "0 42 1234567890", []Token{
		{Type: TOKEN_NUMBER, Value: "0"},
		{Type: TOKEN_NUMBER, Value: "42"},
		{Type: TOKEN_NUMBER, Value: "1234567890"},
	})
}

func TestFloatLiterals(t *testing.T) {
	expectTokens(t, "3.14 0.5 100.0", []Token{
		{Type: TOKEN_NUMBER, Value: "3.14"},
		{Type: TOKEN_NUMBER, Value: "0.5"},
		{Type: TOKEN_NUMBER, Value: "100.0"},
	})
}

func TestScientificNotation(t *testing.T) {
	expectTokens(t, "1e10 1.5e-3 2E+4 3.0e0", []Token{
		{Type: TOKEN_NUMBER, Value: "1e10"},
		{Type: TOKEN_NUMBER, Value: "1.5e-3"},
		{Type: TOKEN_NUMBER, Value: "2E+4"},
		{Type: TOKEN_NUMBER, Value: "3.0e0"},
	})
}

func TestStringLiterals(t *testing.T) {
	expectTokens(t, `"hello" "world"`, []Token{
		{Type: TOKEN_STRING, Value: "hello"},
		{Type: TOKEN_STRING, Value: "world"},
	})
}

func TestStringEscapeSequences(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello\nworld"`, "hello\nworld"},
		{`"tab\there"`, "tab\there"},
		{`"back\\slash"`, "back\\slash"},
		{`"a\"b"`, "a\"b"},
		{`"cr\rhere"`, "cr\rhere"},
	}
	for _, tt := range tests {
		lex := New(tt.input)
		tokens, err := lex.Tokenize()
		if err != nil {
			t.Fatalf("Tokenize(%q) error: %v", tt.input, err)
		}
		if tokens[0].Value != tt.expected {
			t.Errorf("Tokenize(%q): expected value %q, got %q", tt.input, tt.expected, tokens[0].Value)
		}
	}
}

func TestEmptyString(t *testing.T) {
	expectTokens(t, `""`, []Token{
		{Type: TOKEN_STRING, Value: ""},
	})
}

func TestBooleanLiterals(t *testing.T) {
	expectTokens(t, "true false", []Token{
		{Type: TOKEN_TRUE, Value: "true"},
		{Type: TOKEN_FALSE, Value: "false"},
	})
}

func TestNilLiteral(t *testing.T) {
	expectTokens(t, "nil", []Token{
		{Type: TOKEN_NIL, Value: "nil"},
	})
}

// ============================================================
// Identifier and Keyword Tests
// ============================================================

func TestIdentifiers(t *testing.T) {
	expectTokens(t, "foo bar baz _x x1 _hello_world camelCase", []Token{
		{Type: TOKEN_IDENT, Value: "foo"},
		{Type: TOKEN_IDENT, Value: "bar"},
		{Type: TOKEN_IDENT, Value: "baz"},
		{Type: TOKEN_IDENT, Value: "_x"},
		{Type: TOKEN_IDENT, Value: "x1"},
		{Type: TOKEN_IDENT, Value: "_hello_world"},
		{Type: TOKEN_IDENT, Value: "camelCase"},
	})
}

func TestKeywords(t *testing.T) {
	expectTokens(t, "func return if else elseif for range break continue in var", []Token{
		{Type: TOKEN_FUNC, Value: "func"},
		{Type: TOKEN_RETURN, Value: "return"},
		{Type: TOKEN_IF, Value: "if"},
		{Type: TOKEN_ELSE, Value: "else"},
		{Type: TOKEN_ELSEIF, Value: "elseif"},
		{Type: TOKEN_FOR, Value: "for"},
		{Type: TOKEN_RANGE, Value: "range"},
		{Type: TOKEN_BREAK, Value: "break"},
		{Type: TOKEN_CONTINUE, Value: "continue"},
		{Type: TOKEN_IN, Value: "in"},
		{Type: TOKEN_VAR, Value: "var"},
	})
}

func TestKeywordVsIdentifier(t *testing.T) {
	// "funcX" should be an identifier, not keyword + ident
	expectTokens(t, "funcX returning iffoo", []Token{
		{Type: TOKEN_IDENT, Value: "funcX"},
		{Type: TOKEN_IDENT, Value: "returning"},
		{Type: TOKEN_IDENT, Value: "iffoo"},
	})
}

// ============================================================
// Comment Tests
// ============================================================

func TestLineComment(t *testing.T) {
	expectTokens(t, "x // this is a comment\ny", []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_IDENT, Value: "y"},
	})
}

func TestBlockComment(t *testing.T) {
	expectTokens(t, "x /* block\ncomment */ y", []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_IDENT, Value: "y"},
	})
}

func TestBlockCommentInline(t *testing.T) {
	expectTokens(t, "a /* inline */ + b", []Token{
		{Type: TOKEN_IDENT, Value: "a"},
		{Type: TOKEN_PLUS, Value: "+"},
		{Type: TOKEN_IDENT, Value: "b"},
	})
}

func TestCommentOnly(t *testing.T) {
	expectTokens(t, "// entire line is a comment", []Token{})
}

// ============================================================
// Whitespace Handling
// ============================================================

func TestNewlinesAreNotSignificant(t *testing.T) {
	input := "x\n+\ny"
	expectTokens(t, input, []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_PLUS, Value: "+"},
		{Type: TOKEN_IDENT, Value: "y"},
	})
}

func TestTabsAndSpaces(t *testing.T) {
	expectTokens(t, "  \t x \t  y  \t", []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_IDENT, Value: "y"},
	})
}

func TestEmptyInput(t *testing.T) {
	expectTokens(t, "", []Token{})
}

func TestWhitespaceOnly(t *testing.T) {
	expectTokens(t, "   \n\t\n   ", []Token{})
}

// ============================================================
// Position Tracking Tests
// ============================================================

func TestPositionTracking(t *testing.T) {
	input := "x := 10"
	lex := New(input)
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("Tokenize error: %v", err)
	}
	// x at line 1, col 1
	if tokens[0].Line != 1 || tokens[0].Column != 1 {
		t.Errorf("token 'x': expected pos (1,1), got (%d,%d)", tokens[0].Line, tokens[0].Column)
	}
	// := at line 1, col 3
	if tokens[1].Line != 1 || tokens[1].Column != 3 {
		t.Errorf("token ':=': expected pos (1,3), got (%d,%d)", tokens[1].Line, tokens[1].Column)
	}
	// 10 at line 1, col 6
	if tokens[2].Line != 1 || tokens[2].Column != 6 {
		t.Errorf("token '10': expected pos (1,6), got (%d,%d)", tokens[2].Line, tokens[2].Column)
	}
}

func TestMultiLinePositionTracking(t *testing.T) {
	input := "x\ny\nz"
	lex := New(input)
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("Tokenize error: %v", err)
	}
	// x at line 1
	if tokens[0].Line != 1 {
		t.Errorf("'x' line: expected 1, got %d", tokens[0].Line)
	}
	// y at line 2
	if tokens[1].Line != 2 {
		t.Errorf("'y' line: expected 2, got %d", tokens[1].Line)
	}
	// z at line 3
	if tokens[2].Line != 3 {
		t.Errorf("'z' line: expected 3, got %d", tokens[2].Line)
	}
}

// ============================================================
// Error Cases
// ============================================================

func TestUnterminatedString(t *testing.T) {
	lex := New(`"hello`)
	_, err := lex.Tokenize()
	if err == nil {
		t.Fatal("expected error for unterminated string")
	}
}

func TestUnterminatedStringNewline(t *testing.T) {
	lex := New("\"hello\n")
	_, err := lex.Tokenize()
	if err == nil {
		t.Fatal("expected error for string with unescaped newline")
	}
}

func TestUnterminatedBlockComment(t *testing.T) {
	lex := New("/* unclosed block comment")
	_, err := lex.Tokenize()
	if err == nil {
		t.Fatal("expected error for unterminated block comment")
	}
}

func TestInvalidCharacter(t *testing.T) {
	lex := New("@")
	_, err := lex.Tokenize()
	if err == nil {
		t.Fatal("expected error for invalid character @")
	}
}

func TestInvalidCharacterTilde(t *testing.T) {
	lex := New("~")
	_, err := lex.Tokenize()
	if err == nil {
		t.Fatal("expected error for invalid character ~")
	}
}

// ============================================================
// Real Code Snippets
// ============================================================

func TestFunctionDeclaration(t *testing.T) {
	input := `func add(a, b) {
    return a + b
}`
	expectTokens(t, input, []Token{
		{Type: TOKEN_FUNC, Value: "func"},
		{Type: TOKEN_IDENT, Value: "add"},
		{Type: TOKEN_LPAREN, Value: "("},
		{Type: TOKEN_IDENT, Value: "a"},
		{Type: TOKEN_COMMA, Value: ","},
		{Type: TOKEN_IDENT, Value: "b"},
		{Type: TOKEN_RPAREN, Value: ")"},
		{Type: TOKEN_LBRACE, Value: "{"},
		{Type: TOKEN_RETURN, Value: "return"},
		{Type: TOKEN_IDENT, Value: "a"},
		{Type: TOKEN_PLUS, Value: "+"},
		{Type: TOKEN_IDENT, Value: "b"},
		{Type: TOKEN_RBRACE, Value: "}"},
	})
}

func TestVariableDeclaration(t *testing.T) {
	input := `x := 42
name := "hello"
flag := true`
	expectTokens(t, input, []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_DECLARE, Value: ":="},
		{Type: TOKEN_NUMBER, Value: "42"},
		{Type: TOKEN_IDENT, Value: "name"},
		{Type: TOKEN_DECLARE, Value: ":="},
		{Type: TOKEN_STRING, Value: "hello"},
		{Type: TOKEN_IDENT, Value: "flag"},
		{Type: TOKEN_DECLARE, Value: ":="},
		{Type: TOKEN_TRUE, Value: "true"},
	})
}

func TestIfElseStatement(t *testing.T) {
	input := `if x > 0 {
    y := x
} elseif x == 0 {
    y := 0
} else {
    y := -x
}`
	expectTokens(t, input, []Token{
		{Type: TOKEN_IF},
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_GT},
		{Type: TOKEN_NUMBER, Value: "0"},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_IDENT, Value: "y"},
		{Type: TOKEN_DECLARE},
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_RBRACE},
		{Type: TOKEN_ELSEIF},
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_EQ},
		{Type: TOKEN_NUMBER, Value: "0"},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_IDENT, Value: "y"},
		{Type: TOKEN_DECLARE},
		{Type: TOKEN_NUMBER, Value: "0"},
		{Type: TOKEN_RBRACE},
		{Type: TOKEN_ELSE},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_IDENT, Value: "y"},
		{Type: TOKEN_DECLARE},
		{Type: TOKEN_MINUS},
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_RBRACE},
	})
}

func TestForLoop(t *testing.T) {
	input := `for i, v := range items {
    total += v
}`
	expectTokens(t, input, []Token{
		{Type: TOKEN_FOR},
		{Type: TOKEN_IDENT, Value: "i"},
		{Type: TOKEN_COMMA},
		{Type: TOKEN_IDENT, Value: "v"},
		{Type: TOKEN_DECLARE},
		{Type: TOKEN_RANGE},
		{Type: TOKEN_IDENT, Value: "items"},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_IDENT, Value: "total"},
		{Type: TOKEN_PLUS_ASSIGN},
		{Type: TOKEN_IDENT, Value: "v"},
		{Type: TOKEN_RBRACE},
	})
}

func TestTableAccess(t *testing.T) {
	input := `t[0] + t["key"] + t.field`
	expectTokens(t, input, []Token{
		{Type: TOKEN_IDENT, Value: "t"},
		{Type: TOKEN_LBRACKET},
		{Type: TOKEN_NUMBER, Value: "0"},
		{Type: TOKEN_RBRACKET},
		{Type: TOKEN_PLUS},
		{Type: TOKEN_IDENT, Value: "t"},
		{Type: TOKEN_LBRACKET},
		{Type: TOKEN_STRING, Value: "key"},
		{Type: TOKEN_RBRACKET},
		{Type: TOKEN_PLUS},
		{Type: TOKEN_IDENT, Value: "t"},
		{Type: TOKEN_DOT},
		{Type: TOKEN_IDENT, Value: "field"},
	})
}

func TestStringConcat(t *testing.T) {
	input := `"hello" .. " " .. "world"`
	expectTokens(t, input, []Token{
		{Type: TOKEN_STRING, Value: "hello"},
		{Type: TOKEN_CONCAT},
		{Type: TOKEN_STRING, Value: " "},
		{Type: TOKEN_CONCAT},
		{Type: TOKEN_STRING, Value: "world"},
	})
}

func TestLenOperatorOnVariable(t *testing.T) {
	input := `#items`
	expectTokens(t, input, []Token{
		{Type: TOKEN_LEN, Value: "#"},
		{Type: TOKEN_IDENT, Value: "items"},
	})
}

func TestCompoundExpression(t *testing.T) {
	input := `x := 2 ** 3 + 4 * (5 - 1) / 2.0`
	expectTokens(t, input, []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_DECLARE},
		{Type: TOKEN_NUMBER, Value: "2"},
		{Type: TOKEN_POW},
		{Type: TOKEN_NUMBER, Value: "3"},
		{Type: TOKEN_PLUS},
		{Type: TOKEN_NUMBER, Value: "4"},
		{Type: TOKEN_STAR},
		{Type: TOKEN_LPAREN},
		{Type: TOKEN_NUMBER, Value: "5"},
		{Type: TOKEN_MINUS},
		{Type: TOKEN_NUMBER, Value: "1"},
		{Type: TOKEN_RPAREN},
		{Type: TOKEN_SLASH},
		{Type: TOKEN_NUMBER, Value: "2.0"},
	})
}

func TestNotEqual(t *testing.T) {
	// Make sure != is distinguished from just !
	input := `!x != y`
	expectTokens(t, input, []Token{
		{Type: TOKEN_NOT, Value: "!"},
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_NEQ, Value: "!="},
		{Type: TOKEN_IDENT, Value: "y"},
	})
}

func TestEOFToken(t *testing.T) {
	lex := New("x")
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) < 2 {
		t.Fatal("expected at least 2 tokens (IDENT + EOF)")
	}
	last := tokens[len(tokens)-1]
	if last.Type != TOKEN_EOF {
		t.Errorf("last token should be EOF, got %v", last.Type)
	}
}

func TestNextTokenIncremental(t *testing.T) {
	lex := New("a + b")
	tok := lex.NextToken()
	if tok.Type != TOKEN_IDENT || tok.Value != "a" {
		t.Errorf("expected IDENT 'a', got %v %q", tok.Type, tok.Value)
	}
	tok = lex.NextToken()
	if tok.Type != TOKEN_PLUS {
		t.Errorf("expected PLUS, got %v", tok.Type)
	}
	tok = lex.NextToken()
	if tok.Type != TOKEN_IDENT || tok.Value != "b" {
		t.Errorf("expected IDENT 'b', got %v %q", tok.Type, tok.Value)
	}
	tok = lex.NextToken()
	if tok.Type != TOKEN_EOF {
		t.Errorf("expected EOF, got %v", tok.Type)
	}
	// Subsequent calls should keep returning EOF
	tok = lex.NextToken()
	if tok.Type != TOKEN_EOF {
		t.Errorf("expected EOF again, got %v", tok.Type)
	}
}

func TestVariadicFunction(t *testing.T) {
	input := `func sum(args...) {}`
	expectTokens(t, input, []Token{
		{Type: TOKEN_FUNC},
		{Type: TOKEN_IDENT, Value: "sum"},
		{Type: TOKEN_LPAREN},
		{Type: TOKEN_IDENT, Value: "args"},
		{Type: TOKEN_ELLIPSIS},
		{Type: TOKEN_RPAREN},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_RBRACE},
	})
}

func TestIncrementDecrementInContext(t *testing.T) {
	input := `i++
j--`
	expectTokens(t, input, []Token{
		{Type: TOKEN_IDENT, Value: "i"},
		{Type: TOKEN_INC},
		{Type: TOKEN_IDENT, Value: "j"},
		{Type: TOKEN_DEC},
	})
}

func TestComplexProgram(t *testing.T) {
	input := `// Fibonacci function
func fib(n) {
    if n <= 1 {
        return n
    }
    return fib(n - 1) + fib(n - 2)
}

/* Main entry */
result := fib(10)
`
	expectTokens(t, input, []Token{
		// func fib(n) {
		{Type: TOKEN_FUNC},
		{Type: TOKEN_IDENT, Value: "fib"},
		{Type: TOKEN_LPAREN},
		{Type: TOKEN_IDENT, Value: "n"},
		{Type: TOKEN_RPAREN},
		{Type: TOKEN_LBRACE},
		// if n <= 1 {
		{Type: TOKEN_IF},
		{Type: TOKEN_IDENT, Value: "n"},
		{Type: TOKEN_LE},
		{Type: TOKEN_NUMBER, Value: "1"},
		{Type: TOKEN_LBRACE},
		// return n
		{Type: TOKEN_RETURN},
		{Type: TOKEN_IDENT, Value: "n"},
		// }
		{Type: TOKEN_RBRACE},
		// return fib(n - 1) + fib(n - 2)
		{Type: TOKEN_RETURN},
		{Type: TOKEN_IDENT, Value: "fib"},
		{Type: TOKEN_LPAREN},
		{Type: TOKEN_IDENT, Value: "n"},
		{Type: TOKEN_MINUS},
		{Type: TOKEN_NUMBER, Value: "1"},
		{Type: TOKEN_RPAREN},
		{Type: TOKEN_PLUS},
		{Type: TOKEN_IDENT, Value: "fib"},
		{Type: TOKEN_LPAREN},
		{Type: TOKEN_IDENT, Value: "n"},
		{Type: TOKEN_MINUS},
		{Type: TOKEN_NUMBER, Value: "2"},
		{Type: TOKEN_RPAREN},
		// }
		{Type: TOKEN_RBRACE},
		// result := fib(10)
		{Type: TOKEN_IDENT, Value: "result"},
		{Type: TOKEN_DECLARE},
		{Type: TOKEN_IDENT, Value: "fib"},
		{Type: TOKEN_LPAREN},
		{Type: TOKEN_NUMBER, Value: "10"},
		{Type: TOKEN_RPAREN},
	})
}

func TestDotAfterNumber(t *testing.T) {
	// "obj.method" should be IDENT DOT IDENT, not confused with floats
	expectTokens(t, "obj.method", []Token{
		{Type: TOKEN_IDENT, Value: "obj"},
		{Type: TOKEN_DOT},
		{Type: TOKEN_IDENT, Value: "method"},
	})
}

func TestNumberFollowedByDotDot(t *testing.T) {
	// "1..2" is common in some languages for ranges; here: NUMBER(1) CONCAT NUMBER(2)
	// But our language has CONCAT as "..", so this is: NUMBER("1") CONCAT NUMBER("2")
	// The lexer should recognize "1" then ".." then "2"
	expectTokens(t, "1..2", []Token{
		{Type: TOKEN_NUMBER, Value: "1"},
		{Type: TOKEN_CONCAT, Value: ".."},
		{Type: TOKEN_NUMBER, Value: "2"},
	})
}

func TestAndOrInExpressions(t *testing.T) {
	input := `x > 0 && y < 10 || z == 5`
	expectTokens(t, input, []Token{
		{Type: TOKEN_IDENT, Value: "x"},
		{Type: TOKEN_GT},
		{Type: TOKEN_NUMBER, Value: "0"},
		{Type: TOKEN_AND},
		{Type: TOKEN_IDENT, Value: "y"},
		{Type: TOKEN_LT},
		{Type: TOKEN_NUMBER, Value: "10"},
		{Type: TOKEN_OR},
		{Type: TOKEN_IDENT, Value: "z"},
		{Type: TOKEN_EQ},
		{Type: TOKEN_NUMBER, Value: "5"},
	})
}

func TestColonAlone(t *testing.T) {
	// A bare colon should be TOKEN_COLON, not part of :=
	expectTokens(t, "a : b", []Token{
		{Type: TOKEN_IDENT, Value: "a"},
		{Type: TOKEN_COLON, Value: ":"},
		{Type: TOKEN_IDENT, Value: "b"},
	})
}

func TestSemicolonSeparator(t *testing.T) {
	expectTokens(t, "a; b; c", []Token{
		{Type: TOKEN_IDENT, Value: "a"},
		{Type: TOKEN_SEMICOLON},
		{Type: TOKEN_IDENT, Value: "b"},
		{Type: TOKEN_SEMICOLON},
		{Type: TOKEN_IDENT, Value: "c"},
	})
}

func TestBreakContinue(t *testing.T) {
	input := `for {
    if done {
        break
    }
    continue
}`
	expectTokens(t, input, []Token{
		{Type: TOKEN_FOR},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_IF},
		{Type: TOKEN_IDENT, Value: "done"},
		{Type: TOKEN_LBRACE},
		{Type: TOKEN_BREAK},
		{Type: TOKEN_RBRACE},
		{Type: TOKEN_CONTINUE},
		{Type: TOKEN_RBRACE},
	})
}

func TestBlockCommentAcrossLines(t *testing.T) {
	input := `a /* this
spans
multiple
lines */ b`
	lex := New(input)
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// strip EOF
	tokens = tokens[:len(tokens)-1]
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0].Value != "a" {
		t.Errorf("expected 'a', got %q", tokens[0].Value)
	}
	if tokens[1].Value != "b" {
		t.Errorf("expected 'b', got %q", tokens[1].Value)
	}
	// 'b' should be on line 4 (block comment spans 3 newlines)
	if tokens[1].Line != 4 {
		t.Errorf("'b' should be on line 4, got %d", tokens[1].Line)
	}
}

func TestInvalidEscapePassthrough(t *testing.T) {
	// For an unrecognized escape like \z, just include the backslash and char as-is
	lex := New(`"\z"`)
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens[0].Value != "\\z" {
		t.Errorf("expected '\\z', got %q", tokens[0].Value)
	}
}

func TestTokenString(t *testing.T) {
	// Token types should have a readable string representation
	tok := Token{Type: TOKEN_PLUS, Value: "+", Line: 1, Column: 1}
	s := tok.Type.String()
	if s == "" {
		t.Error("Token type String() should not be empty")
	}
}
