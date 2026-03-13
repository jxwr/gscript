package ast

// Pos represents a source position (line and column).
type Pos struct {
	Line   int
	Column int
}

// Node is the interface that all AST nodes implement.
type Node interface {
	nodeType() string
	GetPos() Pos
}

// Stmt is the interface for statement nodes.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is the interface for expression nodes.
type Expr interface {
	Node
	exprNode()
}

// ============================================================
// Program (top-level)
// ============================================================

// Program represents the top-level AST node containing all statements.
type Program struct {
	Stmts []Stmt
}

func (p *Program) nodeType() string { return "Program" }
func (p *Program) GetPos() Pos {
	if len(p.Stmts) > 0 {
		return p.Stmts[0].GetPos()
	}
	return Pos{Line: 1, Column: 1}
}

// ============================================================
// Statements
// ============================================================

// AssignStmt represents assignment: a, b = 1, 2
type AssignStmt struct {
	P       Pos
	Targets []Expr
	Values  []Expr
}

func (s *AssignStmt) nodeType() string { return "AssignStmt" }
func (s *AssignStmt) GetPos() Pos      { return s.P }
func (s *AssignStmt) stmtNode()        {}

// DeclareStmt represents short variable declaration: a, b := 1, 2
type DeclareStmt struct {
	P      Pos
	Names  []string
	Values []Expr
}

func (s *DeclareStmt) nodeType() string { return "DeclareStmt" }
func (s *DeclareStmt) GetPos() Pos      { return s.P }
func (s *DeclareStmt) stmtNode()        {}

// CompoundAssignStmt represents compound assignment: a += b, a -= b, etc.
type CompoundAssignStmt struct {
	P      Pos
	Target Expr
	Op     string // "+=", "-=", "*=", "/="
	Value  Expr
}

func (s *CompoundAssignStmt) nodeType() string { return "CompoundAssignStmt" }
func (s *CompoundAssignStmt) GetPos() Pos      { return s.P }
func (s *CompoundAssignStmt) stmtNode()        {}

// IncDecStmt represents increment/decrement: a++, a--
type IncDecStmt struct {
	P      Pos
	Target Expr
	Op     string // "++" or "--"
}

func (s *IncDecStmt) nodeType() string { return "IncDecStmt" }
func (s *IncDecStmt) GetPos() Pos      { return s.P }
func (s *IncDecStmt) stmtNode()        {}

// CallStmt wraps a CallExpr as a statement.
type CallStmt struct {
	P    Pos
	Call *CallExpr
}

func (s *CallStmt) nodeType() string { return "CallStmt" }
func (s *CallStmt) GetPos() Pos      { return s.P }
func (s *CallStmt) stmtNode()        {}

// GoStmt represents a go statement: go func(){}() or go f(args)
type GoStmt struct {
	P    Pos
	Call Expr // must be *CallExpr or *MethodCallExpr
}

func (s *GoStmt) nodeType() string { return "GoStmt" }
func (s *GoStmt) GetPos() Pos      { return s.P }
func (s *GoStmt) stmtNode()        {}

// SendStmt represents a channel send: ch <- value
type SendStmt struct {
	P       Pos
	Channel Expr
	Value   Expr
}

func (s *SendStmt) nodeType() string { return "SendStmt" }
func (s *SendStmt) GetPos() Pos      { return s.P }
func (s *SendStmt) stmtNode()        {}

// IfStmt represents if/elseif/else chains.
type IfStmt struct {
	P          Pos
	Cond       Expr
	Body       *BlockStmt
	ElseIfs    []ElseIfClause
	ElseBody   *BlockStmt // nil if no else
}

// ElseIfClause represents a single elseif branch.
type ElseIfClause struct {
	P    Pos
	Cond Expr
	Body *BlockStmt
}

func (s *IfStmt) nodeType() string { return "IfStmt" }
func (s *IfStmt) GetPos() Pos      { return s.P }
func (s *IfStmt) stmtNode()        {}

// ForNumStmt represents a C-style for loop: for i := 0; i < n; i++ { }
type ForNumStmt struct {
	P    Pos
	Init Stmt // the init statement (typically DeclareStmt or AssignStmt)
	Cond Expr
	Post Stmt // the post statement (typically IncDecStmt or CompoundAssignStmt)
	Body *BlockStmt
}

func (s *ForNumStmt) nodeType() string { return "ForNumStmt" }
func (s *ForNumStmt) GetPos() Pos      { return s.P }
func (s *ForNumStmt) stmtNode()        {}

// ForRangeStmt represents a range-based for loop: for k, v := range expr { }
type ForRangeStmt struct {
	P      Pos
	Key    string // first variable name
	Value  string // second variable name (may be empty)
	Iter   Expr   // the expression being iterated
	Body   *BlockStmt
}

func (s *ForRangeStmt) nodeType() string { return "ForRangeStmt" }
func (s *ForRangeStmt) GetPos() Pos      { return s.P }
func (s *ForRangeStmt) stmtNode()        {}

// ForStmt represents a while-style loop: for cond { }
type ForStmt struct {
	P    Pos
	Cond Expr // nil means infinite loop (for { })
	Body *BlockStmt
}

func (s *ForStmt) nodeType() string { return "ForStmt" }
func (s *ForStmt) GetPos() Pos      { return s.P }
func (s *ForStmt) stmtNode()        {}

// ReturnStmt represents a return statement: return expr, expr, ...
type ReturnStmt struct {
	P      Pos
	Values []Expr
}

func (s *ReturnStmt) nodeType() string { return "ReturnStmt" }
func (s *ReturnStmt) GetPos() Pos      { return s.P }
func (s *ReturnStmt) stmtNode()        {}

// BreakStmt represents a break statement.
type BreakStmt struct {
	P Pos
}

func (s *BreakStmt) nodeType() string { return "BreakStmt" }
func (s *BreakStmt) GetPos() Pos      { return s.P }
func (s *BreakStmt) stmtNode()        {}

// ContinueStmt represents a continue statement.
type ContinueStmt struct {
	P Pos
}

func (s *ContinueStmt) nodeType() string { return "ContinueStmt" }
func (s *ContinueStmt) GetPos() Pos      { return s.P }
func (s *ContinueStmt) stmtNode()        {}

// FuncDeclStmt represents a top-level named function declaration: func name(params) { body }
type FuncDeclStmt struct {
	P      Pos
	Name   string
	Params []FuncParam
	Body   *BlockStmt
}

func (s *FuncDeclStmt) nodeType() string { return "FuncDeclStmt" }
func (s *FuncDeclStmt) GetPos() Pos      { return s.P }
func (s *FuncDeclStmt) stmtNode()        {}

// BlockStmt represents a block of statements enclosed in braces.
type BlockStmt struct {
	P     Pos
	Stmts []Stmt
}

func (s *BlockStmt) nodeType() string { return "BlockStmt" }
func (s *BlockStmt) GetPos() Pos      { return s.P }
func (s *BlockStmt) stmtNode()        {}

// FuncParam represents a function parameter.
type FuncParam struct {
	Name     string
	IsVarArg bool // only the last param can be vararg (...)
}

// ============================================================
// Expressions
// ============================================================

// NumberLit represents a numeric literal: 42, 3.14
type NumberLit struct {
	P     Pos
	Value string // raw text representation
}

func (e *NumberLit) nodeType() string { return "NumberLit" }
func (e *NumberLit) GetPos() Pos      { return e.P }
func (e *NumberLit) exprNode()        {}

// StringLit represents a string literal: "hello"
type StringLit struct {
	P     Pos
	Value string // the string value (with escapes resolved)
}

func (e *StringLit) nodeType() string { return "StringLit" }
func (e *StringLit) GetPos() Pos      { return e.P }
func (e *StringLit) exprNode()        {}

// BoolLit represents a boolean literal: true, false
type BoolLit struct {
	P     Pos
	Value bool
}

func (e *BoolLit) nodeType() string { return "BoolLit" }
func (e *BoolLit) GetPos() Pos      { return e.P }
func (e *BoolLit) exprNode()        {}

// NilLit represents the nil literal.
type NilLit struct {
	P Pos
}

func (e *NilLit) nodeType() string { return "NilLit" }
func (e *NilLit) GetPos() Pos      { return e.P }
func (e *NilLit) exprNode()        {}

// VarArgExpr represents the vararg expression: ...
type VarArgExpr struct {
	P Pos
}

func (e *VarArgExpr) nodeType() string { return "VarArgExpr" }
func (e *VarArgExpr) GetPos() Pos      { return e.P }
func (e *VarArgExpr) exprNode()        {}

// IdentExpr represents a variable name.
type IdentExpr struct {
	P    Pos
	Name string
}

func (e *IdentExpr) nodeType() string { return "IdentExpr" }
func (e *IdentExpr) GetPos() Pos      { return e.P }
func (e *IdentExpr) exprNode()        {}

// BinaryExpr represents a binary expression: a OP b
type BinaryExpr struct {
	P     Pos
	Left  Expr
	Op    string // "+", "-", "*", "/", "%", "**", "..", "==", "!=", "<", "<=", ">", ">=", "&&", "||"
	Right Expr
}

func (e *BinaryExpr) nodeType() string { return "BinaryExpr" }
func (e *BinaryExpr) GetPos() Pos      { return e.P }
func (e *BinaryExpr) exprNode()        {}

// UnaryExpr represents a unary expression: -a, !a, #a
type UnaryExpr struct {
	P       Pos
	Op      string // "-", "!", "#"
	Operand Expr
}

func (e *UnaryExpr) nodeType() string { return "UnaryExpr" }
func (e *UnaryExpr) GetPos() Pos      { return e.P }
func (e *UnaryExpr) exprNode()        {}

// IndexExpr represents an index access: t[k]
type IndexExpr struct {
	P     Pos
	Table Expr
	Index Expr
}

func (e *IndexExpr) nodeType() string { return "IndexExpr" }
func (e *IndexExpr) GetPos() Pos      { return e.P }
func (e *IndexExpr) exprNode()        {}

// FieldExpr represents a field access: t.k (sugar for t["k"])
type FieldExpr struct {
	P     Pos
	Table Expr
	Field string
}

func (e *FieldExpr) nodeType() string { return "FieldExpr" }
func (e *FieldExpr) GetPos() Pos      { return e.P }
func (e *FieldExpr) exprNode()        {}

// CallExpr represents a function call: f(args)
type CallExpr struct {
	P    Pos
	Func Expr   // the function expression being called
	Args []Expr // arguments
}

func (e *CallExpr) nodeType() string { return "CallExpr" }
func (e *CallExpr) GetPos() Pos      { return e.P }
func (e *CallExpr) exprNode()        {}

// MethodCallExpr represents a Lua-style method call: t:method(args)
type MethodCallExpr struct {
	P      Pos
	Object Expr
	Method string
	Args   []Expr
}

func (e *MethodCallExpr) nodeType() string { return "MethodCallExpr" }
func (e *MethodCallExpr) GetPos() Pos      { return e.P }
func (e *MethodCallExpr) exprNode()        {}

// FuncLitExpr represents a function literal: func(params) { body }
type FuncLitExpr struct {
	P      Pos
	Params []FuncParam
	Body   *BlockStmt
}

func (e *FuncLitExpr) nodeType() string { return "FuncLitExpr" }
func (e *FuncLitExpr) GetPos() Pos      { return e.P }
func (e *FuncLitExpr) exprNode()        {}

// TableLitExpr represents a table literal: {k: v, k2: v2, expr, ...}
type TableLitExpr struct {
	P      Pos
	Fields []TableField
}

func (e *TableLitExpr) nodeType() string { return "TableLitExpr" }
func (e *TableLitExpr) GetPos() Pos      { return e.P }
func (e *TableLitExpr) exprNode()        {}

// TableField represents a single field in a table literal.
type TableField struct {
	Key   Expr // nil means array-style (positional value only)
	Value Expr
}

// RecvExpr represents a channel receive expression: <-ch
type RecvExpr struct {
	P       Pos
	Channel Expr
}

func (e *RecvExpr) nodeType() string { return "RecvExpr" }
func (e *RecvExpr) GetPos() Pos      { return e.P }
func (e *RecvExpr) exprNode()        {}

// MakeChanExpr represents a channel creation: make(chan) or make(chan, size)
type MakeChanExpr struct {
	P    Pos
	Size Expr // nil for unbuffered, or an expression for buffer size
}

func (e *MakeChanExpr) nodeType() string { return "MakeChanExpr" }
func (e *MakeChanExpr) GetPos() Pos      { return e.P }
func (e *MakeChanExpr) exprNode()        {}
