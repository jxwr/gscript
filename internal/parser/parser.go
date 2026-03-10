package parser

import (
	"fmt"

	"github.com/gscript/gscript/internal/ast"
	"github.com/gscript/gscript/internal/lexer"
)

// Parser implements a recursive descent parser for GScript.
type Parser struct {
	tokens []lexer.Token
	pos    int
}

// New creates a new Parser for the given token slice.
func New(tokens []lexer.Token) *Parser {
	return &Parser{
		tokens: tokens,
		pos:    0,
	}
}

// Parse parses the token stream and returns the top-level Program AST node.
func (p *Parser) Parse() (*ast.Program, error) {
	prog := &ast.Program{}
	for !p.isAtEnd() {
		// skip optional semicolons between statements
		p.skipSemicolons()
		if p.isAtEnd() {
			break
		}
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		prog.Stmts = append(prog.Stmts, stmt)
	}
	return prog, nil
}

// ============================================================
// Token helpers
// ============================================================

func (p *Parser) peek() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peekAt(offset int) lexer.Token {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[idx]
}

func (p *Parser) advance() lexer.Token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) isAtEnd() bool {
	return p.peek().Type == lexer.TOKEN_EOF
}

func (p *Parser) check(t lexer.TokenType) bool {
	return p.peek().Type == t
}

func (p *Parser) match(types ...lexer.TokenType) bool {
	for _, t := range types {
		if p.check(t) {
			return true
		}
	}
	return false
}

func (p *Parser) expect(t lexer.TokenType) (lexer.Token, error) {
	tok := p.peek()
	if tok.Type != t {
		return tok, p.errorf("expected %s, got %s (%q)", t, tok.Type, tok.Value)
	}
	p.advance()
	return tok, nil
}

func (p *Parser) errorf(format string, args ...interface{}) error {
	tok := p.peek()
	prefix := fmt.Sprintf("parse error at %d:%d: ", tok.Line, tok.Column)
	return fmt.Errorf(prefix+format, args...)
}

func (p *Parser) tokenPos(tok lexer.Token) ast.Pos {
	return ast.Pos{Line: tok.Line, Column: tok.Column}
}

func (p *Parser) currentPos() ast.Pos {
	return p.tokenPos(p.peek())
}

func (p *Parser) skipSemicolons() {
	for p.check(lexer.TOKEN_SEMICOLON) {
		p.advance()
	}
}

// ============================================================
// Statement parsing
// ============================================================

func (p *Parser) parseStmt() (ast.Stmt, error) {
	switch p.peek().Type {
	case lexer.TOKEN_FUNC:
		return p.parseFuncDeclOrExprStmt()
	case lexer.TOKEN_IF:
		return p.parseIfStmt()
	case lexer.TOKEN_FOR:
		return p.parseForStmt()
	case lexer.TOKEN_RETURN:
		return p.parseReturnStmt()
	case lexer.TOKEN_BREAK:
		return p.parseBreakStmt()
	case lexer.TOKEN_CONTINUE:
		return p.parseContinueStmt()
	default:
		return p.parseExpressionStmt()
	}
}

func (p *Parser) parseFuncDeclOrExprStmt() (ast.Stmt, error) {
	// func followed by IDENT then LPAREN is a FuncDeclStmt
	// func followed by LPAREN is a FuncLitExpr used as an expression
	if p.peekAt(1).Type == lexer.TOKEN_IDENT && p.peekAt(2).Type == lexer.TOKEN_LPAREN {
		return p.parseFuncDeclStmt()
	}
	// Otherwise it's an expression statement starting with a func literal
	return p.parseExpressionStmt()
}

func (p *Parser) parseFuncDeclStmt() (ast.Stmt, error) {
	tok := p.advance() // consume 'func'
	pos := p.tokenPos(tok)

	nameTok, err := p.expect(lexer.TOKEN_IDENT)
	if err != nil {
		return nil, err
	}

	params, err := p.parseFuncParams()
	if err != nil {
		return nil, err
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ast.FuncDeclStmt{
		P:      pos,
		Name:   nameTok.Value,
		Params: params,
		Body:   body,
	}, nil
}

func (p *Parser) parseFuncParams() ([]ast.FuncParam, error) {
	if _, err := p.expect(lexer.TOKEN_LPAREN); err != nil {
		return nil, err
	}

	var params []ast.FuncParam
	if !p.check(lexer.TOKEN_RPAREN) {
		for {
			if p.check(lexer.TOKEN_ELLIPSIS) {
				// bare vararg: func f(...)
				p.advance()
				params = append(params, ast.FuncParam{Name: "...", IsVarArg: true})
				break
			}

			nameTok, err := p.expect(lexer.TOKEN_IDENT)
			if err != nil {
				return nil, err
			}

			param := ast.FuncParam{Name: nameTok.Value}
			if p.check(lexer.TOKEN_ELLIPSIS) {
				p.advance()
				param.IsVarArg = true
				params = append(params, param)
				break // vararg must be last
			}

			params = append(params, param)

			if !p.check(lexer.TOKEN_COMMA) {
				break
			}
			p.advance() // consume ','
		}
	}

	if _, err := p.expect(lexer.TOKEN_RPAREN); err != nil {
		return nil, err
	}

	return params, nil
}

func (p *Parser) parseBlock() (*ast.BlockStmt, error) {
	lbrace, err := p.expect(lexer.TOKEN_LBRACE)
	if err != nil {
		return nil, err
	}

	block := &ast.BlockStmt{P: p.tokenPos(lbrace)}

	for !p.check(lexer.TOKEN_RBRACE) && !p.isAtEnd() {
		p.skipSemicolons()
		if p.check(lexer.TOKEN_RBRACE) {
			break
		}
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		block.Stmts = append(block.Stmts, stmt)
	}

	if _, err := p.expect(lexer.TOKEN_RBRACE); err != nil {
		return nil, err
	}

	return block, nil
}

func (p *Parser) parseIfStmt() (ast.Stmt, error) {
	tok := p.advance() // consume 'if'
	pos := p.tokenPos(tok)

	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	ifStmt := &ast.IfStmt{
		P:    pos,
		Cond: cond,
		Body: body,
	}

	// Parse elseif chains
	for p.check(lexer.TOKEN_ELSEIF) {
		eiTok := p.advance()
		eiPos := p.tokenPos(eiTok)

		eiCond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		eiBody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}

		ifStmt.ElseIfs = append(ifStmt.ElseIfs, ast.ElseIfClause{
			P:    eiPos,
			Cond: eiCond,
			Body: eiBody,
		})
	}

	// Parse else
	if p.check(lexer.TOKEN_ELSE) {
		p.advance()
		elseBody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		ifStmt.ElseBody = elseBody
	}

	return ifStmt, nil
}

func (p *Parser) parseForStmt() (ast.Stmt, error) {
	tok := p.advance() // consume 'for'
	pos := p.tokenPos(tok)

	// for { } -- infinite loop
	if p.check(lexer.TOKEN_LBRACE) {
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return &ast.ForStmt{P: pos, Cond: nil, Body: body}, nil
	}

	// Disambiguate: look ahead to decide which for variant.
	//
	// Strategy: try to detect ForRangeStmt and ForNumStmt patterns.
	// ForRangeStmt: for IDENT [, IDENT] := range EXPR { }
	// ForNumStmt:   for INIT ; COND ; POST { }
	// ForStmt:      for COND { }

	if p.isForRange() {
		return p.parseForRangeBody(pos)
	}

	if p.isForNum() {
		return p.parseForNumBody(pos)
	}

	// Simple for (while) loop: for cond { }
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.ForStmt{P: pos, Cond: cond, Body: body}, nil
}

// isForRange checks if the for statement is a range-based loop.
// Pattern: IDENT [, IDENT] := range
func (p *Parser) isForRange() bool {
	if p.peek().Type != lexer.TOKEN_IDENT {
		return false
	}
	off := 1
	// Check for optional second variable: IDENT , IDENT
	if p.peekAt(off).Type == lexer.TOKEN_COMMA {
		off++
		if p.peekAt(off).Type != lexer.TOKEN_IDENT {
			return false
		}
		off++
	}
	// Must be followed by := range
	if p.peekAt(off).Type != lexer.TOKEN_DECLARE {
		return false
	}
	off++
	return p.peekAt(off).Type == lexer.TOKEN_RANGE
}

// isForNum checks if the for statement is a C-style for loop.
// We detect this by checking if parsing an initial statement is followed by a semicolon.
// Pattern: We look for IDENT (possibly with comma and more idents) := expr ;
// or a simple init followed by ;
func (p *Parser) isForNum() bool {
	// Scan ahead looking for a semicolon at the "right level" (not inside braces/parens/brackets)
	depth := 0
	for i := 0; ; i++ {
		t := p.peekAt(i)
		switch t.Type {
		case lexer.TOKEN_EOF:
			return false
		case lexer.TOKEN_LBRACE:
			if depth == 0 {
				// Hit opening brace of body before finding semicolon - not ForNum
				return false
			}
			depth++
		case lexer.TOKEN_RBRACE:
			depth--
		case lexer.TOKEN_LPAREN:
			depth++
		case lexer.TOKEN_RPAREN:
			depth--
		case lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RBRACKET:
			depth--
		case lexer.TOKEN_SEMICOLON:
			if depth == 0 {
				return true
			}
		}
	}
}

func (p *Parser) parseForRangeBody(pos ast.Pos) (ast.Stmt, error) {
	keyTok := p.advance() // first IDENT
	key := keyTok.Value
	value := ""

	if p.check(lexer.TOKEN_COMMA) {
		p.advance() // consume ','
		valTok, err := p.expect(lexer.TOKEN_IDENT)
		if err != nil {
			return nil, err
		}
		value = valTok.Value
	}

	if _, err := p.expect(lexer.TOKEN_DECLARE); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.TOKEN_RANGE); err != nil {
		return nil, err
	}

	iter, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ast.ForRangeStmt{
		P:     pos,
		Key:   key,
		Value: value,
		Iter:  iter,
		Body:  body,
	}, nil
}

func (p *Parser) parseForNumBody(pos ast.Pos) (ast.Stmt, error) {
	init, err := p.parseSimpleStmt()
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(lexer.TOKEN_SEMICOLON); err != nil {
		return nil, err
	}

	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(lexer.TOKEN_SEMICOLON); err != nil {
		return nil, err
	}

	post, err := p.parseSimpleStmt()
	if err != nil {
		return nil, err
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ast.ForNumStmt{
		P:    pos,
		Init: init,
		Cond: cond,
		Post: post,
		Body: body,
	}, nil
}

// parseSimpleStmt parses a simple statement (no control flow).
// Used for for-loop init and post clauses, and as the basis for parseExpressionStmt.
func (p *Parser) parseSimpleStmt() (ast.Stmt, error) {
	return p.parseExpressionStmt()
}

func (p *Parser) parseReturnStmt() (ast.Stmt, error) {
	tok := p.advance() // consume 'return'
	pos := p.tokenPos(tok)

	ret := &ast.ReturnStmt{P: pos}

	// Return values are optional. If the next token can start an expression,
	// parse a comma-separated expression list.
	if !p.isAtEnd() && !p.check(lexer.TOKEN_RBRACE) && !p.check(lexer.TOKEN_SEMICOLON) && p.canStartExpr() {
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			ret.Values = append(ret.Values, expr)
			if !p.check(lexer.TOKEN_COMMA) {
				break
			}
			p.advance() // consume ','
		}
	}

	return ret, nil
}

func (p *Parser) parseBreakStmt() (ast.Stmt, error) {
	tok := p.advance()
	return &ast.BreakStmt{P: p.tokenPos(tok)}, nil
}

func (p *Parser) parseContinueStmt() (ast.Stmt, error) {
	tok := p.advance()
	return &ast.ContinueStmt{P: p.tokenPos(tok)}, nil
}

// parseExpressionStmt parses an expression statement and determines what kind
// of statement it represents based on what follows the initial expression list.
func (p *Parser) parseExpressionStmt() (ast.Stmt, error) {
	pos := p.currentPos()

	// Parse the first expression
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// Check what follows
	switch p.peek().Type {
	case lexer.TOKEN_DECLARE:
		// a, b := expr, expr
		return p.parseDeclareStmt(pos, expr)
	case lexer.TOKEN_ASSIGN:
		// a = expr OR a, b = expr, expr
		return p.parseAssignStmtFromExpr(pos, expr)
	case lexer.TOKEN_PLUS_ASSIGN, lexer.TOKEN_MINUS_ASSIGN,
		lexer.TOKEN_STAR_ASSIGN, lexer.TOKEN_SLASH_ASSIGN:
		// a += expr
		return p.parseCompoundAssignStmt(pos, expr)
	case lexer.TOKEN_INC, lexer.TOKEN_DEC:
		// a++ or a--
		opTok := p.advance()
		return &ast.IncDecStmt{P: pos, Target: expr, Op: opTok.Value}, nil
	case lexer.TOKEN_COMMA:
		// Could be multi-assignment: a, b = ... or a, b := ...
		return p.parseMultiTargetStmt(pos, expr)
	default:
		// Standalone call expression
		if callExpr, ok := expr.(*ast.CallExpr); ok {
			return &ast.CallStmt{P: pos, Call: callExpr}, nil
		}
		// Method call expression: obj:method(args) used as statement.
		// We wrap it by converting to an equivalent CallExpr.
		if mc, ok := expr.(*ast.MethodCallExpr); ok {
			callExpr := &ast.CallExpr{
				P:    mc.P,
				Func: &ast.FieldExpr{P: mc.P, Table: mc.Object, Field: mc.Method},
				Args: mc.Args,
			}
			return &ast.CallStmt{P: pos, Call: callExpr}, nil
		}
		// If it's just an expression that isn't a call, that's an error
		return nil, fmt.Errorf("parse error at %d:%d: expression is not a statement",
			pos.Line, pos.Column)
	}
}

func (p *Parser) parseDeclareStmt(pos ast.Pos, firstExpr ast.Expr) (ast.Stmt, error) {
	// firstExpr should be an IdentExpr
	names, err := p.exprListToNames([]ast.Expr{firstExpr})
	if err != nil {
		return nil, err
	}

	p.advance() // consume ':='

	values, err := p.parseExprList()
	if err != nil {
		return nil, err
	}

	return &ast.DeclareStmt{P: pos, Names: names, Values: values}, nil
}

func (p *Parser) parseAssignStmtFromExpr(pos ast.Pos, target ast.Expr) (ast.Stmt, error) {
	targets := []ast.Expr{target}
	p.advance() // consume '='

	values, err := p.parseExprList()
	if err != nil {
		return nil, err
	}

	return &ast.AssignStmt{P: pos, Targets: targets, Values: values}, nil
}

func (p *Parser) parseCompoundAssignStmt(pos ast.Pos, target ast.Expr) (ast.Stmt, error) {
	opTok := p.advance() // consume the compound assignment operator

	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	return &ast.CompoundAssignStmt{
		P:      pos,
		Target: target,
		Op:     opTok.Value,
		Value:  value,
	}, nil
}

func (p *Parser) parseMultiTargetStmt(pos ast.Pos, firstExpr ast.Expr) (ast.Stmt, error) {
	// We have firstExpr , ...
	// Collect all targets
	targets := []ast.Expr{firstExpr}
	for p.check(lexer.TOKEN_COMMA) {
		p.advance() // consume ','
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		targets = append(targets, expr)
	}

	switch p.peek().Type {
	case lexer.TOKEN_DECLARE:
		names, err := p.exprListToNames(targets)
		if err != nil {
			return nil, err
		}
		p.advance() // consume ':='
		values, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		return &ast.DeclareStmt{P: pos, Names: names, Values: values}, nil

	case lexer.TOKEN_ASSIGN:
		p.advance() // consume '='
		values, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		return &ast.AssignStmt{P: pos, Targets: targets, Values: values}, nil

	default:
		return nil, p.errorf("expected ':=' or '=' after expression list")
	}
}

func (p *Parser) exprListToNames(exprs []ast.Expr) ([]string, error) {
	names := make([]string, len(exprs))
	for i, e := range exprs {
		ident, ok := e.(*ast.IdentExpr)
		if !ok {
			return nil, fmt.Errorf("parse error at %d:%d: expected identifier in declaration",
				e.GetPos().Line, e.GetPos().Column)
		}
		names[i] = ident.Name
	}
	return names, nil
}

func (p *Parser) parseExprList() ([]ast.Expr, error) {
	var exprs []ast.Expr
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	exprs = append(exprs, expr)

	for p.check(lexer.TOKEN_COMMA) {
		p.advance()
		expr, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}

	return exprs, nil
}

// canStartExpr returns true if the current token can begin an expression.
func (p *Parser) canStartExpr() bool {
	switch p.peek().Type {
	case lexer.TOKEN_IDENT, lexer.TOKEN_NUMBER, lexer.TOKEN_STRING,
		lexer.TOKEN_TRUE, lexer.TOKEN_FALSE, lexer.TOKEN_NIL,
		lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACE, lexer.TOKEN_FUNC,
		lexer.TOKEN_MINUS, lexer.TOKEN_NOT, lexer.TOKEN_LEN,
		lexer.TOKEN_ELLIPSIS:
		return true
	}
	return false
}

// ============================================================
// Expression parsing (precedence climbing)
// ============================================================

// Operator precedence levels (lowest to highest):
// 1. || (or)
// 2. && (and)
// 3. == != < <= > >=  (comparison)
// 4. .. (concat) - RIGHT associative
// 5. + -  (additive)
// 6. * / %  (multiplicative)
// 7. ** (pow) - RIGHT associative
// 8. Unary: - ! #
// 9. Postfix: . [] ()

func (p *Parser) parseExpr() (ast.Expr, error) {
	return p.parseOr()
}

func (p *Parser) parseOr() (ast.Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.check(lexer.TOKEN_OR) {
		opTok := p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  left,
			Op:    opTok.Value,
			Right: right,
		}
	}
	return left, nil
}

func (p *Parser) parseAnd() (ast.Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}

	for p.check(lexer.TOKEN_AND) {
		opTok := p.advance()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  left,
			Op:    opTok.Value,
			Right: right,
		}
	}
	return left, nil
}

func (p *Parser) parseComparison() (ast.Expr, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}

	for p.match(lexer.TOKEN_EQ, lexer.TOKEN_NEQ, lexer.TOKEN_LT, lexer.TOKEN_LE, lexer.TOKEN_GT, lexer.TOKEN_GE) {
		opTok := p.advance()
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  left,
			Op:    opTok.Value,
			Right: right,
		}
	}
	return left, nil
}

// parseConcat handles the .. operator (RIGHT associative).
func (p *Parser) parseConcat() (ast.Expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}

	if p.check(lexer.TOKEN_CONCAT) {
		opTok := p.advance()
		// Right associative: recurse into parseConcat
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  left,
			Op:    opTok.Value,
			Right: right,
		}, nil
	}

	return left, nil
}

func (p *Parser) parseAdditive() (ast.Expr, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}

	for p.match(lexer.TOKEN_PLUS, lexer.TOKEN_MINUS) {
		opTok := p.advance()
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  left,
			Op:    opTok.Value,
			Right: right,
		}
	}
	return left, nil
}

func (p *Parser) parseMultiplicative() (ast.Expr, error) {
	left, err := p.parsePower()
	if err != nil {
		return nil, err
	}

	for p.match(lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT) {
		opTok := p.advance()
		right, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  left,
			Op:    opTok.Value,
			Right: right,
		}
	}
	return left, nil
}

// parsePower handles the ** operator (RIGHT associative).
func (p *Parser) parsePower() (ast.Expr, error) {
	base, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	if p.check(lexer.TOKEN_POW) {
		opTok := p.advance()
		// Right associative: recurse
		exp, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		return &ast.BinaryExpr{
			P:     p.tokenPos(opTok),
			Left:  base,
			Op:    opTok.Value,
			Right: exp,
		}, nil
	}

	return base, nil
}

func (p *Parser) parseUnary() (ast.Expr, error) {
	if p.match(lexer.TOKEN_MINUS, lexer.TOKEN_NOT, lexer.TOKEN_LEN) {
		opTok := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{
			P:       p.tokenPos(opTok),
			Op:      opTok.Value,
			Operand: operand,
		}, nil
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (ast.Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for {
		switch p.peek().Type {
		case lexer.TOKEN_DOT:
			p.advance() // consume '.'
			fieldTok, err := p.expect(lexer.TOKEN_IDENT)
			if err != nil {
				return nil, err
			}
			expr = &ast.FieldExpr{
				P:     expr.GetPos(),
				Table: expr,
				Field: fieldTok.Value,
			}

		case lexer.TOKEN_LBRACKET:
			p.advance() // consume '['
			index, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.TOKEN_RBRACKET); err != nil {
				return nil, err
			}
			expr = &ast.IndexExpr{
				P:     expr.GetPos(),
				Table: expr,
				Index: index,
			}

		case lexer.TOKEN_LPAREN:
			expr, err = p.parseCallExpr(expr)
			if err != nil {
				return nil, err
			}

		case lexer.TOKEN_COLON:
			// Method call: obj:method(args)
			p.advance() // consume ':'
			methodTok, err := p.expect(lexer.TOKEN_IDENT)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.TOKEN_LPAREN); err != nil {
				return nil, err
			}
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.TOKEN_RPAREN); err != nil {
				return nil, err
			}
			expr = &ast.MethodCallExpr{
				P:      expr.GetPos(),
				Object: expr,
				Method: methodTok.Value,
				Args:   args,
			}

		default:
			return expr, nil
		}
	}
}

func (p *Parser) parseCallExpr(fn ast.Expr) (*ast.CallExpr, error) {
	p.advance() // consume '('
	args, err := p.parseArgList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.TOKEN_RPAREN); err != nil {
		return nil, err
	}
	return &ast.CallExpr{
		P:    fn.GetPos(),
		Func: fn,
		Args: args,
	}, nil
}

func (p *Parser) parseArgList() ([]ast.Expr, error) {
	var args []ast.Expr
	if p.check(lexer.TOKEN_RPAREN) {
		return args, nil
	}
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
		if !p.check(lexer.TOKEN_COMMA) {
			break
		}
		p.advance() // consume ','
	}
	return args, nil
}

func (p *Parser) parsePrimary() (ast.Expr, error) {
	tok := p.peek()

	switch tok.Type {
	case lexer.TOKEN_NUMBER:
		p.advance()
		return &ast.NumberLit{P: p.tokenPos(tok), Value: tok.Value}, nil

	case lexer.TOKEN_STRING:
		p.advance()
		return &ast.StringLit{P: p.tokenPos(tok), Value: tok.Value}, nil

	case lexer.TOKEN_TRUE:
		p.advance()
		return &ast.BoolLit{P: p.tokenPos(tok), Value: true}, nil

	case lexer.TOKEN_FALSE:
		p.advance()
		return &ast.BoolLit{P: p.tokenPos(tok), Value: false}, nil

	case lexer.TOKEN_NIL:
		p.advance()
		return &ast.NilLit{P: p.tokenPos(tok)}, nil

	case lexer.TOKEN_ELLIPSIS:
		p.advance()
		return &ast.VarArgExpr{P: p.tokenPos(tok)}, nil

	case lexer.TOKEN_IDENT:
		p.advance()
		return &ast.IdentExpr{P: p.tokenPos(tok), Name: tok.Value}, nil

	case lexer.TOKEN_LPAREN:
		p.advance() // consume '('
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return expr, nil

	case lexer.TOKEN_FUNC:
		return p.parseFuncLitExpr()

	case lexer.TOKEN_LBRACE:
		return p.parseTableLitExpr()

	default:
		return nil, p.errorf("unexpected token %s (%q)", tok.Type, tok.Value)
	}
}

func (p *Parser) parseFuncLitExpr() (ast.Expr, error) {
	tok := p.advance() // consume 'func'
	pos := p.tokenPos(tok)

	params, err := p.parseFuncParams()
	if err != nil {
		return nil, err
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ast.FuncLitExpr{
		P:      pos,
		Params: params,
		Body:   body,
	}, nil
}

func (p *Parser) parseTableLitExpr() (ast.Expr, error) {
	tok := p.advance() // consume '{'
	pos := p.tokenPos(tok)

	table := &ast.TableLitExpr{P: pos}

	for !p.check(lexer.TOKEN_RBRACE) && !p.isAtEnd() {
		field, err := p.parseTableField()
		if err != nil {
			return nil, err
		}
		table.Fields = append(table.Fields, field)

		// Allow optional comma or semicolon separator
		if p.check(lexer.TOKEN_COMMA) || p.check(lexer.TOKEN_SEMICOLON) {
			p.advance()
		}
	}

	if _, err := p.expect(lexer.TOKEN_RBRACE); err != nil {
		return nil, err
	}

	return table, nil
}

func (p *Parser) parseTableField() (ast.TableField, error) {
	// Three forms:
	// 1. [expr]: value  -- computed key
	// 2. ident: value   -- string key shorthand
	// 3. value           -- array-style (positional)

	// Form 1: [expr]: value
	if p.check(lexer.TOKEN_LBRACKET) {
		p.advance() // consume '['
		key, err := p.parseExpr()
		if err != nil {
			return ast.TableField{}, err
		}
		if _, err := p.expect(lexer.TOKEN_RBRACKET); err != nil {
			return ast.TableField{}, err
		}
		if _, err := p.expect(lexer.TOKEN_COLON); err != nil {
			return ast.TableField{}, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return ast.TableField{}, err
		}
		return ast.TableField{Key: key, Value: val}, nil
	}

	// Form 2: ident: value  (look ahead for colon)
	if p.check(lexer.TOKEN_IDENT) && p.peekAt(1).Type == lexer.TOKEN_COLON {
		keyTok := p.advance() // consume ident
		p.advance()           // consume ':'
		val, err := p.parseExpr()
		if err != nil {
			return ast.TableField{}, err
		}
		// The key is a string literal with the identifier's name
		key := &ast.StringLit{P: p.tokenPos(keyTok), Value: keyTok.Value}
		return ast.TableField{Key: key, Value: val}, nil
	}

	// Form 3: bare value (array-style)
	val, err := p.parseExpr()
	if err != nil {
		return ast.TableField{}, err
	}
	return ast.TableField{Key: nil, Value: val}, nil
}
