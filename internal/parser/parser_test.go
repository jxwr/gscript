package parser

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/ast"
	"github.com/gscript/gscript/internal/lexer"
)

// helper: lex and parse the source, fail the test on error
func mustParse(t *testing.T, src string) *ast.Program {
	t.Helper()
	lex := lexer.New(src)
	tokens, err := lex.Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	p := New(tokens)
	prog, err := p.Parse()
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, src)
	}
	return prog
}

// helper: lex and parse, expecting a parse error
func mustFail(t *testing.T, src string) {
	t.Helper()
	lex := lexer.New(src)
	tokens, err := lex.Tokenize()
	if err != nil {
		// lexer error is also acceptable
		return
	}
	p := New(tokens)
	_, err = p.Parse()
	if err == nil {
		t.Fatalf("expected parse error for source:\n%s", src)
	}
}

// ============================================================
// Literal expressions
// ============================================================

func TestNumberLiteral(t *testing.T) {
	prog := mustParse(t, `x := 42`)
	if len(prog.Stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(prog.Stmts))
	}
	decl, ok := prog.Stmts[0].(*ast.DeclareStmt)
	if !ok {
		t.Fatalf("expected DeclareStmt, got %T", prog.Stmts[0])
	}
	if len(decl.Names) != 1 || decl.Names[0] != "x" {
		t.Errorf("expected name 'x', got %v", decl.Names)
	}
	num, ok := decl.Values[0].(*ast.NumberLit)
	if !ok {
		t.Fatalf("expected NumberLit, got %T", decl.Values[0])
	}
	if num.Value != "42" {
		t.Errorf("expected '42', got %q", num.Value)
	}
}

func TestStringLiteral(t *testing.T) {
	prog := mustParse(t, `s := "hello"`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	str, ok := decl.Values[0].(*ast.StringLit)
	if !ok {
		t.Fatalf("expected StringLit, got %T", decl.Values[0])
	}
	if str.Value != "hello" {
		t.Errorf("expected 'hello', got %q", str.Value)
	}
}

func TestBoolLiterals(t *testing.T) {
	prog := mustParse(t, `a := true; b := false`)
	decl1 := prog.Stmts[0].(*ast.DeclareStmt)
	bl1, ok := decl1.Values[0].(*ast.BoolLit)
	if !ok || !bl1.Value {
		t.Errorf("expected true BoolLit")
	}
	decl2 := prog.Stmts[1].(*ast.DeclareStmt)
	bl2, ok := decl2.Values[0].(*ast.BoolLit)
	if !ok || bl2.Value {
		t.Errorf("expected false BoolLit")
	}
}

func TestNilLiteral(t *testing.T) {
	prog := mustParse(t, `x := nil`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	_, ok := decl.Values[0].(*ast.NilLit)
	if !ok {
		t.Fatalf("expected NilLit, got %T", decl.Values[0])
	}
}

// ============================================================
// Declaration and assignment statements
// ============================================================

func TestDeclareStmt(t *testing.T) {
	prog := mustParse(t, `x := 1`)
	decl, ok := prog.Stmts[0].(*ast.DeclareStmt)
	if !ok {
		t.Fatalf("expected DeclareStmt, got %T", prog.Stmts[0])
	}
	if len(decl.Names) != 1 || decl.Names[0] != "x" {
		t.Errorf("expected ['x'], got %v", decl.Names)
	}
}

func TestMultiDeclareStmt(t *testing.T) {
	prog := mustParse(t, `a, b := 1, 2`)
	decl, ok := prog.Stmts[0].(*ast.DeclareStmt)
	if !ok {
		t.Fatalf("expected DeclareStmt, got %T", prog.Stmts[0])
	}
	if len(decl.Names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(decl.Names))
	}
	if decl.Names[0] != "a" || decl.Names[1] != "b" {
		t.Errorf("expected ['a', 'b'], got %v", decl.Names)
	}
	if len(decl.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(decl.Values))
	}
}

func TestAssignStmt(t *testing.T) {
	prog := mustParse(t, `x = 10`)
	assign, ok := prog.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", prog.Stmts[0])
	}
	if len(assign.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(assign.Targets))
	}
	ident, ok := assign.Targets[0].(*ast.IdentExpr)
	if !ok || ident.Name != "x" {
		t.Errorf("expected target 'x'")
	}
}

func TestMultiAssignStmt(t *testing.T) {
	prog := mustParse(t, `a, b = 1, 2`)
	assign, ok := prog.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", prog.Stmts[0])
	}
	if len(assign.Targets) != 2 || len(assign.Values) != 2 {
		t.Errorf("expected 2 targets and 2 values")
	}
}

func TestCompoundAssignStmts(t *testing.T) {
	ops := []struct {
		src string
		op  string
	}{
		{`x += 1`, "+="},
		{`x -= 1`, "-="},
		{`x *= 2`, "*="},
		{`x /= 3`, "/="},
	}
	for _, tt := range ops {
		prog := mustParse(t, tt.src)
		cs, ok := prog.Stmts[0].(*ast.CompoundAssignStmt)
		if !ok {
			t.Fatalf("expected CompoundAssignStmt for %q, got %T", tt.src, prog.Stmts[0])
		}
		if cs.Op != tt.op {
			t.Errorf("expected op %q, got %q", tt.op, cs.Op)
		}
	}
}

func TestIncDecStmt(t *testing.T) {
	prog := mustParse(t, "i++\nj--")
	inc, ok := prog.Stmts[0].(*ast.IncDecStmt)
	if !ok {
		t.Fatalf("expected IncDecStmt, got %T", prog.Stmts[0])
	}
	if inc.Op != "++" {
		t.Errorf("expected '++', got %q", inc.Op)
	}
	dec, ok := prog.Stmts[1].(*ast.IncDecStmt)
	if !ok {
		t.Fatalf("expected IncDecStmt, got %T", prog.Stmts[1])
	}
	if dec.Op != "--" {
		t.Errorf("expected '--', got %q", dec.Op)
	}
}

// ============================================================
// Function declarations
// ============================================================

func TestFuncDeclStmt(t *testing.T) {
	prog := mustParse(t, `func add(a, b) { return a + b }`)
	fn, ok := prog.Stmts[0].(*ast.FuncDeclStmt)
	if !ok {
		t.Fatalf("expected FuncDeclStmt, got %T", prog.Stmts[0])
	}
	if fn.Name != "add" {
		t.Errorf("expected name 'add', got %q", fn.Name)
	}
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}
	if fn.Params[0].Name != "a" || fn.Params[1].Name != "b" {
		t.Errorf("expected params [a, b], got %v", fn.Params)
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body statement, got %d", len(fn.Body.Stmts))
	}
}

func TestFuncDeclNoParams(t *testing.T) {
	prog := mustParse(t, `func greet() { }`)
	fn, ok := prog.Stmts[0].(*ast.FuncDeclStmt)
	if !ok {
		t.Fatalf("expected FuncDeclStmt, got %T", prog.Stmts[0])
	}
	if fn.Name != "greet" {
		t.Errorf("expected name 'greet', got %q", fn.Name)
	}
	if len(fn.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(fn.Params))
	}
}

func TestFuncDeclVarArgs(t *testing.T) {
	prog := mustParse(t, `func sum(args...) { }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	if len(fn.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(fn.Params))
	}
	if !fn.Params[0].IsVarArg || fn.Params[0].Name != "args" {
		t.Errorf("expected vararg param 'args', got %+v", fn.Params[0])
	}
}

func TestFuncDeclVarArgsWithNormal(t *testing.T) {
	prog := mustParse(t, `func f(a, b, rest...) { }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	if len(fn.Params) != 3 {
		t.Fatalf("expected 3 params, got %d", len(fn.Params))
	}
	if fn.Params[2].IsVarArg != true || fn.Params[2].Name != "rest" {
		t.Errorf("expected third param to be vararg 'rest'")
	}
	if fn.Params[0].IsVarArg || fn.Params[1].IsVarArg {
		t.Errorf("first two params should not be vararg")
	}
}

func TestFuncDeclBareEllipsis(t *testing.T) {
	prog := mustParse(t, `func f(...) { }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	if len(fn.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(fn.Params))
	}
	if !fn.Params[0].IsVarArg || fn.Params[0].Name != "..." {
		t.Errorf("expected bare vararg param, got %+v", fn.Params[0])
	}
}

// ============================================================
// Function literal expressions
// ============================================================

func TestFuncLitExpr(t *testing.T) {
	prog := mustParse(t, `f := func(x) { return x * 2 }`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	fn, ok := decl.Values[0].(*ast.FuncLitExpr)
	if !ok {
		t.Fatalf("expected FuncLitExpr, got %T", decl.Values[0])
	}
	if len(fn.Params) != 1 || fn.Params[0].Name != "x" {
		t.Errorf("expected param 'x'")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Errorf("expected 1 body statement")
	}
}

// ============================================================
// If/elseif/else
// ============================================================

func TestIfStmt(t *testing.T) {
	prog := mustParse(t, `if x > 0 { y := 1 }`)
	ifStmt, ok := prog.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected IfStmt, got %T", prog.Stmts[0])
	}
	if ifStmt.Cond == nil {
		t.Error("expected condition")
	}
	if len(ifStmt.Body.Stmts) != 1 {
		t.Errorf("expected 1 body statement")
	}
	if len(ifStmt.ElseIfs) != 0 {
		t.Errorf("expected 0 elseif clauses")
	}
	if ifStmt.ElseBody != nil {
		t.Errorf("expected no else body")
	}
}

func TestIfElseStmt(t *testing.T) {
	prog := mustParse(t, `if x > 0 { y := 1 } else { y := -1 }`)
	ifStmt := prog.Stmts[0].(*ast.IfStmt)
	if ifStmt.ElseBody == nil {
		t.Error("expected else body")
	}
}

func TestIfElseIfElseStmt(t *testing.T) {
	src := `if x > 0 {
		y := 1
	} elseif x == 0 {
		y := 0
	} elseif x > -10 {
		y := -1
	} else {
		y := -2
	}`
	prog := mustParse(t, src)
	ifStmt := prog.Stmts[0].(*ast.IfStmt)
	if len(ifStmt.ElseIfs) != 2 {
		t.Fatalf("expected 2 elseif clauses, got %d", len(ifStmt.ElseIfs))
	}
	if ifStmt.ElseBody == nil {
		t.Error("expected else body")
	}
}

// ============================================================
// For loop variants
// ============================================================

func TestForInfiniteLoop(t *testing.T) {
	prog := mustParse(t, `for { break }`)
	forStmt, ok := prog.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", prog.Stmts[0])
	}
	if forStmt.Cond != nil {
		t.Error("expected nil condition for infinite loop")
	}
}

func TestForWhileLoop(t *testing.T) {
	prog := mustParse(t, `for x > 0 { x -= 1 }`)
	forStmt, ok := prog.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", prog.Stmts[0])
	}
	if forStmt.Cond == nil {
		t.Error("expected condition for while loop")
	}
}

func TestForNumLoop(t *testing.T) {
	prog := mustParse(t, `for i := 0; i < 10; i++ { x += i }`)
	forNum, ok := prog.Stmts[0].(*ast.ForNumStmt)
	if !ok {
		t.Fatalf("expected ForNumStmt, got %T", prog.Stmts[0])
	}
	// Check init is DeclareStmt
	init, ok := forNum.Init.(*ast.DeclareStmt)
	if !ok {
		t.Fatalf("expected init to be DeclareStmt, got %T", forNum.Init)
	}
	if init.Names[0] != "i" {
		t.Errorf("expected init var 'i', got %q", init.Names[0])
	}
	// Check cond
	if forNum.Cond == nil {
		t.Error("expected condition")
	}
	// Check post is IncDecStmt
	post, ok := forNum.Post.(*ast.IncDecStmt)
	if !ok {
		t.Fatalf("expected post to be IncDecStmt, got %T", forNum.Post)
	}
	if post.Op != "++" {
		t.Errorf("expected '++', got %q", post.Op)
	}
}

func TestForNumWithAssignInit(t *testing.T) {
	prog := mustParse(t, `for i = 0; i < 10; i += 1 { }`)
	forNum, ok := prog.Stmts[0].(*ast.ForNumStmt)
	if !ok {
		t.Fatalf("expected ForNumStmt, got %T", prog.Stmts[0])
	}
	_, ok = forNum.Init.(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected init to be AssignStmt, got %T", forNum.Init)
	}
	_, ok = forNum.Post.(*ast.CompoundAssignStmt)
	if !ok {
		t.Fatalf("expected post to be CompoundAssignStmt, got %T", forNum.Post)
	}
}

func TestForRangeLoop(t *testing.T) {
	prog := mustParse(t, `for k, v := range items { }`)
	forRange, ok := prog.Stmts[0].(*ast.ForRangeStmt)
	if !ok {
		t.Fatalf("expected ForRangeStmt, got %T", prog.Stmts[0])
	}
	if forRange.Key != "k" {
		t.Errorf("expected key 'k', got %q", forRange.Key)
	}
	if forRange.Value != "v" {
		t.Errorf("expected value 'v', got %q", forRange.Value)
	}
	if forRange.Iter == nil {
		t.Error("expected iterator expression")
	}
}

func TestForRangeSingleVar(t *testing.T) {
	prog := mustParse(t, `for i := range items { }`)
	forRange, ok := prog.Stmts[0].(*ast.ForRangeStmt)
	if !ok {
		t.Fatalf("expected ForRangeStmt, got %T", prog.Stmts[0])
	}
	if forRange.Key != "i" {
		t.Errorf("expected key 'i', got %q", forRange.Key)
	}
	if forRange.Value != "" {
		t.Errorf("expected empty value, got %q", forRange.Value)
	}
}

// ============================================================
// Return, break, continue
// ============================================================

func TestReturnStmt(t *testing.T) {
	prog := mustParse(t, `func f() { return 42 }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected ReturnStmt, got %T", fn.Body.Stmts[0])
	}
	if len(ret.Values) != 1 {
		t.Errorf("expected 1 return value, got %d", len(ret.Values))
	}
}

func TestReturnMultipleValues(t *testing.T) {
	prog := mustParse(t, `func f() { return 1, 2, 3 }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if len(ret.Values) != 3 {
		t.Errorf("expected 3 return values, got %d", len(ret.Values))
	}
}

func TestReturnNoValue(t *testing.T) {
	prog := mustParse(t, `func f() { return }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if len(ret.Values) != 0 {
		t.Errorf("expected 0 return values, got %d", len(ret.Values))
	}
}

func TestBreakStmt(t *testing.T) {
	prog := mustParse(t, `for { break }`)
	forStmt := prog.Stmts[0].(*ast.ForStmt)
	_, ok := forStmt.Body.Stmts[0].(*ast.BreakStmt)
	if !ok {
		t.Fatalf("expected BreakStmt, got %T", forStmt.Body.Stmts[0])
	}
}

func TestContinueStmt(t *testing.T) {
	prog := mustParse(t, `for { continue }`)
	forStmt := prog.Stmts[0].(*ast.ForStmt)
	_, ok := forStmt.Body.Stmts[0].(*ast.ContinueStmt)
	if !ok {
		t.Fatalf("expected ContinueStmt, got %T", forStmt.Body.Stmts[0])
	}
}

// ============================================================
// Call statements
// ============================================================

func TestCallStmt(t *testing.T) {
	prog := mustParse(t, `print("hello")`)
	cs, ok := prog.Stmts[0].(*ast.CallStmt)
	if !ok {
		t.Fatalf("expected CallStmt, got %T", prog.Stmts[0])
	}
	if cs.Call == nil {
		t.Error("expected non-nil Call")
	}
	fn, ok := cs.Call.Func.(*ast.IdentExpr)
	if !ok || fn.Name != "print" {
		t.Errorf("expected function 'print'")
	}
	if len(cs.Call.Args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(cs.Call.Args))
	}
}

func TestCallStmtNoArgs(t *testing.T) {
	prog := mustParse(t, `foo()`)
	cs, ok := prog.Stmts[0].(*ast.CallStmt)
	if !ok {
		t.Fatalf("expected CallStmt, got %T", prog.Stmts[0])
	}
	if len(cs.Call.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(cs.Call.Args))
	}
}

func TestCallStmtMultiArgs(t *testing.T) {
	prog := mustParse(t, `foo(1, 2, 3)`)
	cs := prog.Stmts[0].(*ast.CallStmt)
	if len(cs.Call.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(cs.Call.Args))
	}
}

// ============================================================
// Expressions: binary operators and precedence
// ============================================================

func TestBinaryAdd(t *testing.T) {
	prog := mustParse(t, `x := 1 + 2`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	bin, ok := decl.Values[0].(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", decl.Values[0])
	}
	if bin.Op != "+" {
		t.Errorf("expected '+', got %q", bin.Op)
	}
}

func TestPrecedenceMulOverAdd(t *testing.T) {
	// 1 + 2 * 3 should be 1 + (2 * 3)
	prog := mustParse(t, `x := 1 + 2 * 3`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	add := decl.Values[0].(*ast.BinaryExpr)
	if add.Op != "+" {
		t.Fatalf("expected top-level '+', got %q", add.Op)
	}
	mul, ok := add.Right.(*ast.BinaryExpr)
	if !ok || mul.Op != "*" {
		t.Error("expected right operand to be '*'")
	}
}

func TestPrecedenceParens(t *testing.T) {
	// (1 + 2) * 3 should have + at the left
	prog := mustParse(t, `x := (1 + 2) * 3`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	mul := decl.Values[0].(*ast.BinaryExpr)
	if mul.Op != "*" {
		t.Fatalf("expected top-level '*', got %q", mul.Op)
	}
	add, ok := mul.Left.(*ast.BinaryExpr)
	if !ok || add.Op != "+" {
		t.Error("expected left operand to be '+'")
	}
}

func TestPrecedencePow(t *testing.T) {
	// 2 ** 3 ** 2 should be 2 ** (3 ** 2) (right associative)
	prog := mustParse(t, `x := 2 ** 3 ** 2`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	pow := decl.Values[0].(*ast.BinaryExpr)
	if pow.Op != "**" {
		t.Fatalf("expected top-level '**', got %q", pow.Op)
	}
	leftNum, ok := pow.Left.(*ast.NumberLit)
	if !ok || leftNum.Value != "2" {
		t.Error("expected left to be 2")
	}
	rightPow, ok := pow.Right.(*ast.BinaryExpr)
	if !ok || rightPow.Op != "**" {
		t.Error("expected right to be another '**'")
	}
}

func TestPrecedenceConcat(t *testing.T) {
	// "a" .. "b" .. "c" should be "a" .. ("b" .. "c") (right associative)
	prog := mustParse(t, `x := "a" .. "b" .. "c"`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	concat := decl.Values[0].(*ast.BinaryExpr)
	if concat.Op != ".." {
		t.Fatalf("expected top-level '..', got %q", concat.Op)
	}
	leftStr, ok := concat.Left.(*ast.StringLit)
	if !ok || leftStr.Value != "a" {
		t.Error("expected left to be 'a'")
	}
	rightConcat, ok := concat.Right.(*ast.BinaryExpr)
	if !ok || rightConcat.Op != ".." {
		t.Error("expected right to be another '..'")
	}
}

func TestPrecedenceComparison(t *testing.T) {
	// a + 1 == b * 2
	prog := mustParse(t, `x := a + 1 == b * 2`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	eq := decl.Values[0].(*ast.BinaryExpr)
	if eq.Op != "==" {
		t.Fatalf("expected top-level '==', got %q", eq.Op)
	}
	leftAdd, ok := eq.Left.(*ast.BinaryExpr)
	if !ok || leftAdd.Op != "+" {
		t.Error("expected left to be '+'")
	}
	rightMul, ok := eq.Right.(*ast.BinaryExpr)
	if !ok || rightMul.Op != "*" {
		t.Error("expected right to be '*'")
	}
}

func TestPrecedenceAndOr(t *testing.T) {
	// a || b && c should be a || (b && c)
	prog := mustParse(t, `x := a || b && c`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	or := decl.Values[0].(*ast.BinaryExpr)
	if or.Op != "||" {
		t.Fatalf("expected top-level '||', got %q", or.Op)
	}
	and, ok := or.Right.(*ast.BinaryExpr)
	if !ok || and.Op != "&&" {
		t.Error("expected right to be '&&'")
	}
}

func TestAllComparisonOps(t *testing.T) {
	ops := []struct {
		src string
		op  string
	}{
		{`x := a == b`, "=="},
		{`x := a != b`, "!="},
		{`x := a < b`, "<"},
		{`x := a <= b`, "<="},
		{`x := a > b`, ">"},
		{`x := a >= b`, ">="},
	}
	for _, tt := range ops {
		prog := mustParse(t, tt.src)
		decl := prog.Stmts[0].(*ast.DeclareStmt)
		bin, ok := decl.Values[0].(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("expected BinaryExpr for %q", tt.src)
		}
		if bin.Op != tt.op {
			t.Errorf("expected op %q, got %q for %q", tt.op, bin.Op, tt.src)
		}
	}
}

func TestModOperator(t *testing.T) {
	prog := mustParse(t, `x := 10 % 3`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	bin := decl.Values[0].(*ast.BinaryExpr)
	if bin.Op != "%" {
		t.Errorf("expected '%%', got %q", bin.Op)
	}
}

// ============================================================
// Unary expressions
// ============================================================

func TestUnaryMinus(t *testing.T) {
	prog := mustParse(t, `x := -5`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	unary, ok := decl.Values[0].(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", decl.Values[0])
	}
	if unary.Op != "-" {
		t.Errorf("expected '-', got %q", unary.Op)
	}
}

func TestUnaryNot(t *testing.T) {
	prog := mustParse(t, `x := !true`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	unary, ok := decl.Values[0].(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", decl.Values[0])
	}
	if unary.Op != "!" {
		t.Errorf("expected '!', got %q", unary.Op)
	}
}

func TestUnaryLen(t *testing.T) {
	prog := mustParse(t, `x := #items`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	unary, ok := decl.Values[0].(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", decl.Values[0])
	}
	if unary.Op != "#" {
		t.Errorf("expected '#', got %q", unary.Op)
	}
}

func TestDoubleNegation(t *testing.T) {
	// --5 is lexed as TOKEN_DEC(--) + TOKEN_NUMBER(5), which is not a valid expression.
	// The correct way to write double negation is -(-5).
	mustFail(t, `x := --5`)
}

func TestNestedUnary(t *testing.T) {
	prog := mustParse(t, `x := -(-5)`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	outer, ok := decl.Values[0].(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", decl.Values[0])
	}
	inner, ok := outer.Operand.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected nested UnaryExpr, got %T", outer.Operand)
	}
	if inner.Op != "-" {
		t.Errorf("expected inner '-', got %q", inner.Op)
	}
}

// ============================================================
// Postfix expressions
// ============================================================

func TestFieldExpr(t *testing.T) {
	prog := mustParse(t, `x := t.field`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	field, ok := decl.Values[0].(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected FieldExpr, got %T", decl.Values[0])
	}
	if field.Field != "field" {
		t.Errorf("expected field 'field', got %q", field.Field)
	}
}

func TestIndexExpr(t *testing.T) {
	prog := mustParse(t, `x := t[0]`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	idx, ok := decl.Values[0].(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected IndexExpr, got %T", decl.Values[0])
	}
	num, ok := idx.Index.(*ast.NumberLit)
	if !ok || num.Value != "0" {
		t.Errorf("expected index 0")
	}
}

func TestChainedFieldAccess(t *testing.T) {
	prog := mustParse(t, `x := a.b.c`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	outer, ok := decl.Values[0].(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected FieldExpr, got %T", decl.Values[0])
	}
	if outer.Field != "c" {
		t.Errorf("expected field 'c', got %q", outer.Field)
	}
	inner, ok := outer.Table.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected inner FieldExpr, got %T", outer.Table)
	}
	if inner.Field != "b" {
		t.Errorf("expected field 'b', got %q", inner.Field)
	}
}

func TestCallExpr(t *testing.T) {
	prog := mustParse(t, `x := f(1, 2)`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	call, ok := decl.Values[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", decl.Values[0])
	}
	if len(call.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(call.Args))
	}
}

func TestChainedCallExpr(t *testing.T) {
	prog := mustParse(t, `x := f(1)(2)`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	outerCall, ok := decl.Values[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", decl.Values[0])
	}
	innerCall, ok := outerCall.Func.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected inner CallExpr, got %T", outerCall.Func)
	}
	_ = innerCall
}

func TestMethodCallExpr(t *testing.T) {
	prog := mustParse(t, `x := obj:method(1, 2)`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	mc, ok := decl.Values[0].(*ast.MethodCallExpr)
	if !ok {
		t.Fatalf("expected MethodCallExpr, got %T", decl.Values[0])
	}
	if mc.Method != "method" {
		t.Errorf("expected method 'method', got %q", mc.Method)
	}
	if len(mc.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(mc.Args))
	}
}

func TestFieldThenCall(t *testing.T) {
	// t.method(args) is FieldExpr + CallExpr
	prog := mustParse(t, `x := t.method(1)`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	call, ok := decl.Values[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", decl.Values[0])
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected FieldExpr as callee, got %T", call.Func)
	}
	if field.Field != "method" {
		t.Errorf("expected field 'method', got %q", field.Field)
	}
}

// ============================================================
// Table literals
// ============================================================

func TestEmptyTable(t *testing.T) {
	prog := mustParse(t, `t := {}`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl, ok := decl.Values[0].(*ast.TableLitExpr)
	if !ok {
		t.Fatalf("expected TableLitExpr, got %T", decl.Values[0])
	}
	if len(tbl.Fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(tbl.Fields))
	}
}

func TestTableWithKeyValuePairs(t *testing.T) {
	prog := mustParse(t, `t := {name: "alice", age: 30}`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl := decl.Values[0].(*ast.TableLitExpr)
	if len(tbl.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(tbl.Fields))
	}
	// First field
	key1, ok := tbl.Fields[0].Key.(*ast.StringLit)
	if !ok || key1.Value != "name" {
		t.Errorf("expected key 'name'")
	}
	val1, ok := tbl.Fields[0].Value.(*ast.StringLit)
	if !ok || val1.Value != "alice" {
		t.Errorf("expected value 'alice'")
	}
}

func TestTableArrayStyle(t *testing.T) {
	prog := mustParse(t, `t := {1, 2, 3}`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl := decl.Values[0].(*ast.TableLitExpr)
	if len(tbl.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(tbl.Fields))
	}
	for i, f := range tbl.Fields {
		if f.Key != nil {
			t.Errorf("field %d should have nil key (array-style)", i)
		}
	}
}

func TestTableComputedKey(t *testing.T) {
	prog := mustParse(t, `t := {[1+1]: "two"}`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl := decl.Values[0].(*ast.TableLitExpr)
	if len(tbl.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(tbl.Fields))
	}
	// Key should be a BinaryExpr (1+1)
	binKey, ok := tbl.Fields[0].Key.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected computed key BinaryExpr, got %T", tbl.Fields[0].Key)
	}
	if binKey.Op != "+" {
		t.Errorf("expected '+', got %q", binKey.Op)
	}
}

func TestTableMixed(t *testing.T) {
	prog := mustParse(t, `t := {name: "alice", "bare_value", [2]: "two"}`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl := decl.Values[0].(*ast.TableLitExpr)
	if len(tbl.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(tbl.Fields))
	}
	// First: named key
	if tbl.Fields[0].Key == nil {
		t.Error("field 0 should have a key")
	}
	// Second: array-style
	if tbl.Fields[1].Key != nil {
		t.Error("field 1 should be array-style (nil key)")
	}
	// Third: computed key
	if tbl.Fields[2].Key == nil {
		t.Error("field 2 should have a key")
	}
}

func TestTableTrailingComma(t *testing.T) {
	// Trailing comma should be allowed
	prog := mustParse(t, `t := {1, 2, 3,}`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl := decl.Values[0].(*ast.TableLitExpr)
	if len(tbl.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(tbl.Fields))
	}
}

// ============================================================
// VarArgExpr
// ============================================================

func TestVarArgExpr(t *testing.T) {
	prog := mustParse(t, `func f(...) { x := ... }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	decl := fn.Body.Stmts[0].(*ast.DeclareStmt)
	_, ok := decl.Values[0].(*ast.VarArgExpr)
	if !ok {
		t.Fatalf("expected VarArgExpr, got %T", decl.Values[0])
	}
}

// ============================================================
// Nested structures
// ============================================================

func TestNestedIfInFor(t *testing.T) {
	src := `for i := 0; i < 10; i++ {
		if i % 2 == 0 {
			print(i)
		}
	}`
	prog := mustParse(t, src)
	forNum, ok := prog.Stmts[0].(*ast.ForNumStmt)
	if !ok {
		t.Fatalf("expected ForNumStmt")
	}
	if len(forNum.Body.Stmts) != 1 {
		t.Fatalf("expected 1 body statement")
	}
	ifStmt, ok := forNum.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected IfStmt in for body, got %T", forNum.Body.Stmts[0])
	}
	if len(ifStmt.Body.Stmts) != 1 {
		t.Fatalf("expected 1 statement in if body")
	}
}

func TestNestedForInFunc(t *testing.T) {
	src := `func f(n) {
		total := 0
		for i := 0; i < n; i++ {
			total += i
		}
		return total
	}`
	prog := mustParse(t, src)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected 3 statements in func body, got %d", len(fn.Body.Stmts))
	}
}

// ============================================================
// Error cases
// ============================================================

func TestErrorUnexpectedToken(t *testing.T) {
	mustFail(t, `x := `)
}

func TestErrorMissingCloseBrace(t *testing.T) {
	mustFail(t, `func f() {`)
}

func TestErrorMissingCloseParen(t *testing.T) {
	mustFail(t, `f(1, 2`)
}

func TestErrorMissingCloseBracket(t *testing.T) {
	mustFail(t, `t[0`)
}

func TestErrorBadStatement(t *testing.T) {
	mustFail(t, `42`)
}

func TestErrorBadForSyntax(t *testing.T) {
	// missing semicolons in for loop
	mustFail(t, `for i := 0 i < 10 i++ { }`)
}

func TestErrorContainsPosition(t *testing.T) {
	lex := lexer.New(`x := `)
	tokens, _ := lex.Tokenize()
	p := New(tokens)
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "parse error") {
		t.Errorf("error should contain 'parse error': %q", errStr)
	}
}

// ============================================================
// Assign to complex targets
// ============================================================

func TestAssignToField(t *testing.T) {
	prog := mustParse(t, `t.x = 10`)
	assign, ok := prog.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", prog.Stmts[0])
	}
	_, ok = assign.Targets[0].(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected FieldExpr target, got %T", assign.Targets[0])
	}
}

func TestAssignToIndex(t *testing.T) {
	prog := mustParse(t, `t[0] = "hello"`)
	assign, ok := prog.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", prog.Stmts[0])
	}
	_, ok = assign.Targets[0].(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected IndexExpr target, got %T", assign.Targets[0])
	}
}

func TestCompoundAssignToField(t *testing.T) {
	prog := mustParse(t, `t.x += 1`)
	cs, ok := prog.Stmts[0].(*ast.CompoundAssignStmt)
	if !ok {
		t.Fatalf("expected CompoundAssignStmt, got %T", prog.Stmts[0])
	}
	_, ok = cs.Target.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected FieldExpr target, got %T", cs.Target)
	}
}

// ============================================================
// Full programs
// ============================================================

func TestFibonacciProgram(t *testing.T) {
	src := `func fib(n) {
		if n <= 1 {
			return n
		}
		return fib(n - 1) + fib(n - 2)
	}
	result := fib(10)`
	prog := mustParse(t, src)
	if len(prog.Stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(prog.Stmts))
	}
	_, ok := prog.Stmts[0].(*ast.FuncDeclStmt)
	if !ok {
		t.Fatalf("expected FuncDeclStmt, got %T", prog.Stmts[0])
	}
	_, ok = prog.Stmts[1].(*ast.DeclareStmt)
	if !ok {
		t.Fatalf("expected DeclareStmt, got %T", prog.Stmts[1])
	}
}

func TestCounterClosureProgram(t *testing.T) {
	src := `func makeCounter() {
		count := 0
		return func() {
			count += 1
			return count
		}
	}
	counter := makeCounter()
	counter()
	counter()
	x := counter()`
	prog := mustParse(t, src)
	if len(prog.Stmts) != 5 {
		t.Fatalf("expected 5 statements, got %d", len(prog.Stmts))
	}
}

func TestTableManipulationProgram(t *testing.T) {
	src := `t := {name: "alice", age: 30}
	t.email = "alice@example.com"
	t["phone"] = "555-1234"
	n := #t`
	prog := mustParse(t, src)
	if len(prog.Stmts) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(prog.Stmts))
	}
}

func TestForRangeProgram(t *testing.T) {
	src := `items := {10, 20, 30}
	total := 0
	for k, v := range items {
		total += v
	}
	print(total)`
	prog := mustParse(t, src)
	if len(prog.Stmts) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(prog.Stmts))
	}
	_, ok := prog.Stmts[2].(*ast.ForRangeStmt)
	if !ok {
		t.Fatalf("expected ForRangeStmt, got %T", prog.Stmts[2])
	}
}

func TestComplexExpressionProgram(t *testing.T) {
	src := `x := 2 ** 3 + 4 * (5 - 1) / 2
	y := "hello" .. " " .. "world"
	z := #items > 0 && x != nil || !done`
	prog := mustParse(t, src)
	if len(prog.Stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(prog.Stmts))
	}
}

// ============================================================
// Semicolons as separators
// ============================================================

func TestSemicolonSeparation(t *testing.T) {
	prog := mustParse(t, `a := 1; b := 2; c := 3`)
	if len(prog.Stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(prog.Stmts))
	}
}

// ============================================================
// Position tracking
// ============================================================

func TestPositionTracking(t *testing.T) {
	prog := mustParse(t, `x := 42`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	if decl.P.Line != 1 || decl.P.Column != 1 {
		t.Errorf("expected pos (1,1), got (%d,%d)", decl.P.Line, decl.P.Column)
	}
}

// ============================================================
// Edge cases
// ============================================================

func TestEmptyProgram(t *testing.T) {
	prog := mustParse(t, ``)
	if len(prog.Stmts) != 0 {
		t.Errorf("expected 0 statements, got %d", len(prog.Stmts))
	}
}

func TestEmptyFuncBody(t *testing.T) {
	prog := mustParse(t, `func f() {}`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	if len(fn.Body.Stmts) != 0 {
		t.Errorf("expected 0 body statements, got %d", len(fn.Body.Stmts))
	}
}

func TestMultipleReturnBeforeCloseBrace(t *testing.T) {
	prog := mustParse(t, `func f() { return }`)
	fn := prog.Stmts[0].(*ast.FuncDeclStmt)
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if len(ret.Values) != 0 {
		t.Errorf("expected 0 return values")
	}
}

func TestCallOnFuncLit(t *testing.T) {
	// Immediately invoked function literal
	prog := mustParse(t, `x := func(a) { return a }(42)`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	call, ok := decl.Values[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", decl.Values[0])
	}
	_, ok = call.Func.(*ast.FuncLitExpr)
	if !ok {
		t.Fatalf("expected FuncLitExpr as callee, got %T", call.Func)
	}
}

func TestNestedTableLiterals(t *testing.T) {
	src := `t := {inner: {a: 1, b: 2}, items: {10, 20}}`
	prog := mustParse(t, src)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	tbl := decl.Values[0].(*ast.TableLitExpr)
	if len(tbl.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(tbl.Fields))
	}
	// inner should be a TableLitExpr
	innerTbl, ok := tbl.Fields[0].Value.(*ast.TableLitExpr)
	if !ok {
		t.Fatalf("expected inner TableLitExpr, got %T", tbl.Fields[0].Value)
	}
	if len(innerTbl.Fields) != 2 {
		t.Errorf("expected 2 fields in inner table, got %d", len(innerTbl.Fields))
	}
}

func TestIndexWithExpr(t *testing.T) {
	prog := mustParse(t, `x := t[i + 1]`)
	decl := prog.Stmts[0].(*ast.DeclareStmt)
	idx := decl.Values[0].(*ast.IndexExpr)
	bin, ok := idx.Index.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr index, got %T", idx.Index)
	}
	if bin.Op != "+" {
		t.Errorf("expected '+', got %q", bin.Op)
	}
}

func TestFieldAccessAssign(t *testing.T) {
	// Chained field access on left side of assignment
	prog := mustParse(t, `a.b.c = 5`)
	assign := prog.Stmts[0].(*ast.AssignStmt)
	field, ok := assign.Targets[0].(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected FieldExpr target, got %T", assign.Targets[0])
	}
	if field.Field != "c" {
		t.Errorf("expected field 'c', got %q", field.Field)
	}
}

func TestMethodCallStmt(t *testing.T) {
	// Method call used as a statement
	prog := mustParse(t, `obj:method(1, 2)`)
	cs, ok := prog.Stmts[0].(*ast.CallStmt)
	if !ok {
		// It might be parsed differently since the parser sees obj:method(1,2) as a MethodCallExpr,
		// not a CallExpr. Let's check if the parser handles this.
		t.Fatalf("expected CallStmt, got %T", prog.Stmts[0])
	}
	_ = cs
}
