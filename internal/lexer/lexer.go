package lexer

import "fmt"

// Lexer tokenizes GScript source code.
type Lexer struct {
	source string
	pos    int
	line   int
	col    int
	tokens []Token
}

// New creates a new Lexer for the given source string.
func New(source string) *Lexer {
	return &Lexer{
		source: source,
		pos:    0,
		line:   1,
		col:    1,
	}
}

// peek returns the current character without advancing, or 0 if at end.
func (l *Lexer) peek() byte {
	if l.pos >= len(l.source) {
		return 0
	}
	return l.source[l.pos]
}

// peekAt returns the character at pos+offset, or 0 if out of bounds.
func (l *Lexer) peekAt(offset int) byte {
	idx := l.pos + offset
	if idx >= len(l.source) {
		return 0
	}
	return l.source[idx]
}

// advance moves forward one character and updates line/col tracking.
func (l *Lexer) advance() byte {
	if l.pos >= len(l.source) {
		return 0
	}
	ch := l.source[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

// skipWhitespace skips spaces, tabs, carriage returns, and newlines.
func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.source) {
		ch := l.source[l.pos]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			l.advance()
		} else {
			break
		}
	}
}

// NextToken returns the next token from the source.
// After all tokens are consumed, it returns TOKEN_EOF.
func (l *Lexer) NextToken() Token {
	tok, err := l.nextTokenInternal()
	if err != nil {
		return Token{Type: TOKEN_ILLEGAL, Value: err.Error(), Line: l.line, Column: l.col}
	}
	return tok
}

// nextTokenInternal returns the next token or an error.
func (l *Lexer) nextTokenInternal() (Token, error) {
	l.skipWhitespace()

	if l.pos >= len(l.source) {
		return Token{Type: TOKEN_EOF, Value: "", Line: l.line, Column: l.col}, nil
	}

	ch := l.peek()
	startLine := l.line
	startCol := l.col

	// Line comment
	if ch == '/' && l.peekAt(1) == '/' {
		l.skipLineComment()
		return l.nextTokenInternal()
	}

	// Block comment
	if ch == '/' && l.peekAt(1) == '*' {
		if err := l.skipBlockComment(); err != nil {
			return Token{}, err
		}
		return l.nextTokenInternal()
	}

	// String literal
	if ch == '"' {
		return l.readString()
	}

	// Number literal
	if isDigit(ch) {
		return l.readNumber()
	}

	// Identifier or keyword
	if isLetter(ch) {
		return l.readIdentifier()
	}

	// Operators and separators
	return l.readOperator(startLine, startCol)
}

// skipLineComment skips from // to end of line.
func (l *Lexer) skipLineComment() {
	// consume the //
	l.advance()
	l.advance()
	for l.pos < len(l.source) && l.peek() != '\n' {
		l.advance()
	}
}

// skipBlockComment skips from /* to */. Returns error if unterminated.
func (l *Lexer) skipBlockComment() error {
	startLine := l.line
	startCol := l.col
	// consume /*
	l.advance()
	l.advance()
	for l.pos < len(l.source) {
		if l.peek() == '*' && l.peekAt(1) == '/' {
			l.advance() // *
			l.advance() // /
			return nil
		}
		l.advance()
	}
	return fmt.Errorf("unterminated block comment starting at %d:%d", startLine, startCol)
}

// readString reads a double-quoted string literal with escape sequences.
func (l *Lexer) readString() (Token, error) {
	startLine := l.line
	startCol := l.col
	l.advance() // consume opening "

	var result []byte
	for l.pos < len(l.source) {
		ch := l.peek()
		if ch == '\n' {
			return Token{}, fmt.Errorf("unterminated string at %d:%d: newline in string literal", startLine, startCol)
		}
		if ch == '"' {
			l.advance() // consume closing "
			return Token{Type: TOKEN_STRING, Value: string(result), Line: startLine, Column: startCol}, nil
		}
		if ch == '\\' {
			l.advance() // consume backslash
			if l.pos >= len(l.source) {
				return Token{}, fmt.Errorf("unterminated string at %d:%d: unexpected end after escape", startLine, startCol)
			}
			esc := l.advance()
			switch esc {
			case 'n':
				result = append(result, '\n')
			case 't':
				result = append(result, '\t')
			case 'r':
				result = append(result, '\r')
			case '\\':
				result = append(result, '\\')
			case '"':
				result = append(result, '"')
			default:
				// Unrecognized escape: keep backslash and character
				result = append(result, '\\', esc)
			}
			continue
		}
		result = append(result, l.advance())
	}
	return Token{}, fmt.Errorf("unterminated string at %d:%d", startLine, startCol)
}

// readNumber reads an integer or floating-point number, including scientific notation.
func (l *Lexer) readNumber() (Token, error) {
	startLine := l.line
	startCol := l.col
	startPos := l.pos

	// Read digits
	for l.pos < len(l.source) && isDigit(l.peek()) {
		l.advance()
	}

	// Check for decimal point — but not if followed by another dot (which is CONCAT "..")
	if l.pos < len(l.source) && l.peek() == '.' && l.peekAt(1) != '.' {
		l.advance() // consume .
		for l.pos < len(l.source) && isDigit(l.peek()) {
			l.advance()
		}
	}

	// Check for exponent
	if l.pos < len(l.source) && (l.peek() == 'e' || l.peek() == 'E') {
		l.advance() // consume e/E
		if l.pos < len(l.source) && (l.peek() == '+' || l.peek() == '-') {
			l.advance() // consume sign
		}
		for l.pos < len(l.source) && isDigit(l.peek()) {
			l.advance()
		}
	}

	value := l.source[startPos:l.pos]
	return Token{Type: TOKEN_NUMBER, Value: value, Line: startLine, Column: startCol}, nil
}

// readIdentifier reads an identifier or keyword.
func (l *Lexer) readIdentifier() (Token, error) {
	startLine := l.line
	startCol := l.col
	startPos := l.pos

	for l.pos < len(l.source) && isLetterOrDigit(l.peek()) {
		l.advance()
	}

	value := l.source[startPos:l.pos]
	tokType := LookupIdent(value)
	return Token{Type: tokType, Value: value, Line: startLine, Column: startCol}, nil
}

// readOperator reads operators and separator tokens.
func (l *Lexer) readOperator(startLine, startCol int) (Token, error) {
	ch := l.peek()

	makeToken := func(typ TokenType, val string) Token {
		return Token{Type: typ, Value: val, Line: startLine, Column: startCol}
	}

	switch ch {
	case '(':
		l.advance()
		return makeToken(TOKEN_LPAREN, "("), nil
	case ')':
		l.advance()
		return makeToken(TOKEN_RPAREN, ")"), nil
	case '{':
		l.advance()
		return makeToken(TOKEN_LBRACE, "{"), nil
	case '}':
		l.advance()
		return makeToken(TOKEN_RBRACE, "}"), nil
	case '[':
		l.advance()
		return makeToken(TOKEN_LBRACKET, "["), nil
	case ']':
		l.advance()
		return makeToken(TOKEN_RBRACKET, "]"), nil
	case ',':
		l.advance()
		return makeToken(TOKEN_COMMA, ","), nil
	case ';':
		l.advance()
		return makeToken(TOKEN_SEMICOLON, ";"), nil
	case '#':
		l.advance()
		return makeToken(TOKEN_LEN, "#"), nil
	case '%':
		l.advance()
		return makeToken(TOKEN_PERCENT, "%"), nil

	case ':':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_DECLARE, ":="), nil
		}
		return makeToken(TOKEN_COLON, ":"), nil

	case '=':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_EQ, "=="), nil
		}
		return makeToken(TOKEN_ASSIGN, "="), nil

	case '!':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_NEQ, "!="), nil
		}
		return makeToken(TOKEN_NOT, "!"), nil

	case '<':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_LE, "<="), nil
		}
		if l.peek() == '-' {
			l.advance()
			return makeToken(TOKEN_ARROW, "<-"), nil
		}
		return makeToken(TOKEN_LT, "<"), nil

	case '>':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_GE, ">="), nil
		}
		return makeToken(TOKEN_GT, ">"), nil

	case '+':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_PLUS_ASSIGN, "+="), nil
		}
		if l.peek() == '+' {
			l.advance()
			return makeToken(TOKEN_INC, "++"), nil
		}
		return makeToken(TOKEN_PLUS, "+"), nil

	case '-':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_MINUS_ASSIGN, "-="), nil
		}
		if l.peek() == '-' {
			l.advance()
			return makeToken(TOKEN_DEC, "--"), nil
		}
		return makeToken(TOKEN_MINUS, "-"), nil

	case '*':
		l.advance()
		if l.peek() == '*' {
			l.advance()
			return makeToken(TOKEN_POW, "**"), nil
		}
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_STAR_ASSIGN, "*="), nil
		}
		return makeToken(TOKEN_STAR, "*"), nil

	case '/':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return makeToken(TOKEN_SLASH_ASSIGN, "/="), nil
		}
		return makeToken(TOKEN_SLASH, "/"), nil

	case '&':
		l.advance()
		if l.peek() == '&' {
			l.advance()
			return makeToken(TOKEN_AND, "&&"), nil
		}
		return Token{}, fmt.Errorf("unexpected character '&' at %d:%d (did you mean '&&'?)", startLine, startCol)

	case '|':
		l.advance()
		if l.peek() == '|' {
			l.advance()
			return makeToken(TOKEN_OR, "||"), nil
		}
		return Token{}, fmt.Errorf("unexpected character '|' at %d:%d (did you mean '||'?)", startLine, startCol)

	case '.':
		l.advance()
		if l.peek() == '.' {
			l.advance()
			if l.peek() == '.' {
				l.advance()
				return makeToken(TOKEN_ELLIPSIS, "..."), nil
			}
			return makeToken(TOKEN_CONCAT, ".."), nil
		}
		return makeToken(TOKEN_DOT, "."), nil

	default:
		l.advance()
		return Token{}, fmt.Errorf("unexpected character %q at %d:%d", string(ch), startLine, startCol)
	}
}

// Tokenize returns all tokens from the source, including a trailing EOF.
// Returns an error if any lexical error is encountered.
func (l *Lexer) Tokenize() ([]Token, error) {
	var tokens []Token
	for {
		tok, err := l.nextTokenInternal()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Type == TOKEN_EOF {
			break
		}
	}
	return tokens, nil
}

// isDigit returns true for ASCII digits.
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// isLetter returns true for ASCII letters and underscore.
func isLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// isLetterOrDigit returns true for ASCII letters, digits, and underscore.
func isLetterOrDigit(ch byte) bool {
	return isLetter(ch) || isDigit(ch)
}
