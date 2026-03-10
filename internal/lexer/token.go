package lexer

import "fmt"

// TokenType represents the type of a lexical token.
type TokenType int

const (
	// Special
	TOKEN_EOF TokenType = iota
	TOKEN_ILLEGAL

	// Literals
	TOKEN_NUMBER // 123, 1.5, 1e10
	TOKEN_STRING // "hello"
	TOKEN_TRUE   // true
	TOKEN_FALSE  // false
	TOKEN_NIL    // nil

	// Identifier
	TOKEN_IDENT

	// Keywords
	TOKEN_FUNC     // func
	TOKEN_RETURN   // return
	TOKEN_IF       // if
	TOKEN_ELSE     // else
	TOKEN_ELSEIF   // elseif
	TOKEN_FOR      // for
	TOKEN_RANGE    // range
	TOKEN_BREAK    // break
	TOKEN_CONTINUE // continue
	TOKEN_IN       // in
	TOKEN_VAR      // var

	// Assignment operators
	TOKEN_ASSIGN       // =
	TOKEN_DECLARE      // :=
	TOKEN_PLUS_ASSIGN  // +=
	TOKEN_MINUS_ASSIGN // -=
	TOKEN_STAR_ASSIGN  // *=
	TOKEN_SLASH_ASSIGN // /=

	// Comparison operators
	TOKEN_EQ  // ==
	TOKEN_NEQ // !=
	TOKEN_LT  // <
	TOKEN_LE  // <=
	TOKEN_GT  // >
	TOKEN_GE  // >=

	// Math operators
	TOKEN_PLUS    // +
	TOKEN_MINUS   // -
	TOKEN_STAR    // *
	TOKEN_SLASH   // /
	TOKEN_PERCENT // %
	TOKEN_POW     // **

	// Logic operators
	TOKEN_AND // &&
	TOKEN_OR  // ||
	TOKEN_NOT // !

	// String/misc operators
	TOKEN_CONCAT   // ..
	TOKEN_LEN      // #
	TOKEN_ELLIPSIS // ...

	// Increment/Decrement
	TOKEN_INC // ++
	TOKEN_DEC // --

	// Separators
	TOKEN_LPAREN    // (
	TOKEN_RPAREN    // )
	TOKEN_LBRACE    // {
	TOKEN_RBRACE    // }
	TOKEN_LBRACKET  // [
	TOKEN_RBRACKET  // ]
	TOKEN_COMMA     // ,
	TOKEN_SEMICOLON // ;
	TOKEN_DOT       // .
	TOKEN_COLON     // :
)

var tokenNames = map[TokenType]string{
	TOKEN_EOF:     "EOF",
	TOKEN_ILLEGAL: "ILLEGAL",

	TOKEN_NUMBER: "NUMBER",
	TOKEN_STRING: "STRING",
	TOKEN_TRUE:   "TRUE",
	TOKEN_FALSE:  "FALSE",
	TOKEN_NIL:    "NIL",

	TOKEN_IDENT: "IDENT",

	TOKEN_FUNC:     "FUNC",
	TOKEN_RETURN:   "RETURN",
	TOKEN_IF:       "IF",
	TOKEN_ELSE:     "ELSE",
	TOKEN_ELSEIF:   "ELSEIF",
	TOKEN_FOR:      "FOR",
	TOKEN_RANGE:    "RANGE",
	TOKEN_BREAK:    "BREAK",
	TOKEN_CONTINUE: "CONTINUE",
	TOKEN_IN:       "IN",
	TOKEN_VAR:      "VAR",

	TOKEN_ASSIGN:       "ASSIGN",
	TOKEN_DECLARE:      "DECLARE",
	TOKEN_PLUS_ASSIGN:  "PLUS_ASSIGN",
	TOKEN_MINUS_ASSIGN: "MINUS_ASSIGN",
	TOKEN_STAR_ASSIGN:  "STAR_ASSIGN",
	TOKEN_SLASH_ASSIGN: "SLASH_ASSIGN",

	TOKEN_EQ:  "EQ",
	TOKEN_NEQ: "NEQ",
	TOKEN_LT:  "LT",
	TOKEN_LE:  "LE",
	TOKEN_GT:  "GT",
	TOKEN_GE:  "GE",

	TOKEN_PLUS:    "PLUS",
	TOKEN_MINUS:   "MINUS",
	TOKEN_STAR:    "STAR",
	TOKEN_SLASH:   "SLASH",
	TOKEN_PERCENT: "PERCENT",
	TOKEN_POW:     "POW",

	TOKEN_AND: "AND",
	TOKEN_OR:  "OR",
	TOKEN_NOT: "NOT",

	TOKEN_CONCAT:   "CONCAT",
	TOKEN_LEN:      "LEN",
	TOKEN_ELLIPSIS: "ELLIPSIS",

	TOKEN_INC: "INC",
	TOKEN_DEC: "DEC",

	TOKEN_LPAREN:    "LPAREN",
	TOKEN_RPAREN:    "RPAREN",
	TOKEN_LBRACE:    "LBRACE",
	TOKEN_RBRACE:    "RBRACE",
	TOKEN_LBRACKET:  "LBRACKET",
	TOKEN_RBRACKET:  "RBRACKET",
	TOKEN_COMMA:     "COMMA",
	TOKEN_SEMICOLON: "SEMICOLON",
	TOKEN_DOT:       "DOT",
	TOKEN_COLON:     "COLON",
}

// String returns a human-readable name for the token type.
func (t TokenType) String() string {
	if name, ok := tokenNames[t]; ok {
		return name
	}
	return fmt.Sprintf("TOKEN(%d)", int(t))
}

// Token represents a single lexical token.
type Token struct {
	Type   TokenType
	Value  string
	Line   int
	Column int
}

func (t Token) String() string {
	return fmt.Sprintf("{%s %q %d:%d}", t.Type, t.Value, t.Line, t.Column)
}

// keywords maps keyword strings to their token types.
var keywords = map[string]TokenType{
	"func":     TOKEN_FUNC,
	"return":   TOKEN_RETURN,
	"if":       TOKEN_IF,
	"else":     TOKEN_ELSE,
	"elseif":   TOKEN_ELSEIF,
	"for":      TOKEN_FOR,
	"range":    TOKEN_RANGE,
	"break":    TOKEN_BREAK,
	"continue": TOKEN_CONTINUE,
	"in":       TOKEN_IN,
	"var":      TOKEN_VAR,
	"true":     TOKEN_TRUE,
	"false":    TOKEN_FALSE,
	"nil":      TOKEN_NIL,
}

// LookupIdent checks if an identifier is a keyword and returns the
// appropriate token type.
func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return TOKEN_IDENT
}
