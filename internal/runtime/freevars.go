package runtime

import "github.com/gscript/gscript/internal/ast"

// FreeVars returns the set of variable names that are referenced in the given
// AST node but not defined within it. 'params' contains locally bound parameter
// names that should be excluded from the result set.
func FreeVars(node ast.Node, params []string) []string {
	c := &freeVarCollector{
		defined: make(map[string]bool),
		used:    make(map[string]bool),
	}
	for _, p := range params {
		c.defined[p] = true
	}
	c.walk(node)

	var result []string
	for name := range c.used {
		if !c.defined[name] {
			result = append(result, name)
		}
	}
	return result
}

type freeVarCollector struct {
	defined map[string]bool // names defined in this scope
	used    map[string]bool // names referenced in this scope
}

func (c *freeVarCollector) walk(node ast.Node) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.BlockStmt:
		c.walkBlock(n)
	case *ast.Program:
		for _, s := range n.Stmts {
			c.walkStmt(s)
		}
	default:
		// Try as stmt or expr
		if s, ok := node.(ast.Stmt); ok {
			c.walkStmt(s)
		}
		if e, ok := node.(ast.Expr); ok {
			c.walkExpr(e)
		}
	}
}

func (c *freeVarCollector) walkBlock(block *ast.BlockStmt) {
	if block == nil {
		return
	}
	for _, s := range block.Stmts {
		c.walkStmt(s)
	}
}

func (c *freeVarCollector) walkStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.DeclareStmt:
		// First walk values (they may reference variables before declaration)
		for _, v := range s.Values {
			c.walkExpr(v)
		}
		for _, name := range s.Names {
			c.defined[name] = true
		}
	case *ast.AssignStmt:
		for _, t := range s.Targets {
			c.walkExpr(t)
		}
		for _, v := range s.Values {
			c.walkExpr(v)
		}
	case *ast.CompoundAssignStmt:
		c.walkExpr(s.Target)
		c.walkExpr(s.Value)
	case *ast.IncDecStmt:
		c.walkExpr(s.Target)
	case *ast.CallStmt:
		c.walkExpr(s.Call)
	case *ast.IfStmt:
		c.walkExpr(s.Cond)
		c.walkBlock(s.Body)
		for _, ei := range s.ElseIfs {
			c.walkExpr(ei.Cond)
			c.walkBlock(ei.Body)
		}
		if s.ElseBody != nil {
			c.walkBlock(s.ElseBody)
		}
	case *ast.ForStmt:
		if s.Cond != nil {
			c.walkExpr(s.Cond)
		}
		c.walkBlock(s.Body)
	case *ast.ForNumStmt:
		// Init may define new variables
		c.walkStmt(s.Init)
		c.walkExpr(s.Cond)
		c.walkStmt(s.Post)
		c.walkBlock(s.Body)
	case *ast.ForRangeStmt:
		c.walkExpr(s.Iter)
		// Key and Value are locally defined
		c.defined[s.Key] = true
		if s.Value != "" {
			c.defined[s.Value] = true
		}
		c.walkBlock(s.Body)
	case *ast.ReturnStmt:
		for _, v := range s.Values {
			c.walkExpr(v)
		}
	case *ast.FuncDeclStmt:
		// The function name is defined in this scope
		c.defined[s.Name] = true
		// The function body is a nested scope; collect free vars of the body
		// but treat params as locally bound in that inner scope.
		inner := &freeVarCollector{
			defined: make(map[string]bool),
			used:    make(map[string]bool),
		}
		for _, p := range s.Params {
			inner.defined[p.Name] = true
		}
		inner.walkBlock(s.Body)
		// Any free vars of the inner function become "used" in this scope
		for name := range inner.used {
			if !inner.defined[name] {
				c.used[name] = true
			}
		}
	case *ast.BlockStmt:
		c.walkBlock(s)
	case *ast.BreakStmt:
		// nothing
	case *ast.ContinueStmt:
		// nothing
	}
}

func (c *freeVarCollector) walkExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.IdentExpr:
		c.used[e.Name] = true
	case *ast.BinaryExpr:
		c.walkExpr(e.Left)
		c.walkExpr(e.Right)
	case *ast.UnaryExpr:
		c.walkExpr(e.Operand)
	case *ast.IndexExpr:
		c.walkExpr(e.Table)
		c.walkExpr(e.Index)
	case *ast.FieldExpr:
		c.walkExpr(e.Table)
	case *ast.CallExpr:
		c.walkExpr(e.Func)
		for _, a := range e.Args {
			c.walkExpr(a)
		}
	case *ast.MethodCallExpr:
		c.walkExpr(e.Object)
		for _, a := range e.Args {
			c.walkExpr(a)
		}
	case *ast.FuncLitExpr:
		// Nested function literal: collect its free vars
		inner := &freeVarCollector{
			defined: make(map[string]bool),
			used:    make(map[string]bool),
		}
		for _, p := range e.Params {
			inner.defined[p.Name] = true
		}
		inner.walkBlock(e.Body)
		// Free vars of the inner func become "used" in this scope
		for name := range inner.used {
			if !inner.defined[name] {
				c.used[name] = true
			}
		}
	case *ast.TableLitExpr:
		for _, f := range e.Fields {
			if f.Key != nil {
				c.walkExpr(f.Key)
			}
			c.walkExpr(f.Value)
		}
	case *ast.VarArgExpr:
		// varargs are accessed via "..."
	case *ast.NumberLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit:
		// no variables
	}
}
