package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gscript/gscript/internal/ast"
)

// Interpreter is the tree-walking evaluator for GScript programs.
type Interpreter struct {
	globals   *Environment
	output    []string     // captured print output (for testing)
	currentCo *Coroutine   // non-nil when running inside a coroutine
}

// New creates a new Interpreter with built-in globals.
func New() *Interpreter {
	interp := &Interpreter{
		globals: NewEnvironment(nil),
	}
	interp.registerBuiltins()
	return interp
}

// SetGlobal defines or overwrites a global variable.
func (interp *Interpreter) SetGlobal(name string, val Value) {
	interp.globals.Define(name, val)
}

// GetGlobal retrieves a global variable.
func (interp *Interpreter) GetGlobal(name string) Value {
	v, _ := interp.globals.Get(name)
	return v
}

// Output returns captured print output (for testing).
func (interp *Interpreter) Output() []string {
	return interp.output
}

// registerBuiltins installs standard global functions.
func (interp *Interpreter) registerBuiltins() {
	interp.globals.Define("print", FunctionValue(&GoFunction{
		Name: "print",
		Fn: func(args []Value) ([]Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = a.String()
			}
			line := strings.Join(parts, "\t")
			fmt.Println(line)
			interp.output = append(interp.output, line)
			return nil, nil
		},
	}))

	interp.globals.Define("type", FunctionValue(&GoFunction{
		Name: "type",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) == 0 {
				return []Value{StringValue("nil")}, nil
			}
			return []Value{StringValue(args[0].TypeName())}, nil
		},
	}))

	interp.globals.Define("tostring", FunctionValue(&GoFunction{
		Name: "tostring",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) == 0 {
				return []Value{StringValue("nil")}, nil
			}
			return []Value{StringValue(args[0].String())}, nil
		},
	}))

	interp.globals.Define("tonumber", FunctionValue(&GoFunction{
		Name: "tonumber",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) == 0 {
				return []Value{NilValue()}, nil
			}
			v, ok := args[0].ToNumber()
			if !ok {
				return []Value{NilValue()}, nil
			}
			return []Value{v}, nil
		},
	}))

	interp.globals.Define("setmetatable", FunctionValue(&GoFunction{
		Name: "setmetatable",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("bad argument to 'setmetatable' (table expected)")
			}
			if !args[0].IsTable() {
				return nil, fmt.Errorf("bad argument #1 to 'setmetatable' (table expected, got %s)", args[0].TypeName())
			}
			tbl := args[0].Table()
			if args[1].IsNil() {
				tbl.SetMetatable(nil)
			} else if args[1].IsTable() {
				tbl.SetMetatable(args[1].Table())
			} else {
				return nil, fmt.Errorf("bad argument #2 to 'setmetatable' (nil or table expected, got %s)", args[1].TypeName())
			}
			return []Value{args[0]}, nil
		},
	}))

	interp.globals.Define("getmetatable", FunctionValue(&GoFunction{
		Name: "getmetatable",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) == 0 {
				return nil, fmt.Errorf("bad argument to 'getmetatable' (value expected)")
			}
			if !args[0].IsTable() {
				return []Value{NilValue()}, nil
			}
			mt := args[0].Table().GetMetatable()
			if mt == nil {
				return []Value{NilValue()}, nil
			}
			return []Value{TableValue(mt)}, nil
		},
	}))

	interp.globals.Define("rawget", FunctionValue(&GoFunction{
		Name: "rawget",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("bad argument to 'rawget' (table expected)")
			}
			if !args[0].IsTable() {
				return nil, fmt.Errorf("bad argument #1 to 'rawget' (table expected, got %s)", args[0].TypeName())
			}
			return []Value{args[0].Table().RawGet(args[1])}, nil
		},
	}))

	interp.globals.Define("rawset", FunctionValue(&GoFunction{
		Name: "rawset",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 3 {
				return nil, fmt.Errorf("bad argument to 'rawset' (table expected)")
			}
			if !args[0].IsTable() {
				return nil, fmt.Errorf("bad argument #1 to 'rawset' (table expected, got %s)", args[0].TypeName())
			}
			args[0].Table().RawSet(args[1], args[2])
			return []Value{args[0]}, nil
		},
	}))

	interp.globals.Define("rawequal", FunctionValue(&GoFunction{
		Name: "rawequal",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("bad argument to 'rawequal' (two values expected)")
			}
			return []Value{BoolValue(args[0].Equal(args[1]))}, nil
		},
	}))

	interp.globals.Define("len", FunctionValue(&GoFunction{
		Name: "len",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) == 0 {
				return nil, fmt.Errorf("bad argument to 'len' (value expected)")
			}
			a := args[0]
			switch a.Type() {
			case TypeString:
				return []Value{IntValue(int64(len(a.Str())))}, nil
			case TypeTable:
				return []Value{IntValue(int64(a.Table().Length()))}, nil
			default:
				return nil, fmt.Errorf("bad argument to 'len' (table or string expected, got %s)", a.TypeName())
			}
		},
	}))

	// ----------------------------------------------------------------
	// Coroutine library
	// ----------------------------------------------------------------
	interp.registerCoroutineLib()
}

// registerCoroutineLib installs the "coroutine" global table with
// create, resume, yield, status, wrap, and isyieldable.
func (interp *Interpreter) registerCoroutineLib() {
	coLib := NewTable()

	coLib.RawSet(StringValue("create"), FunctionValue(&GoFunction{
		Name: "coroutine.create",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 || !args[0].IsFunction() {
				return nil, fmt.Errorf("coroutine.create expects a function")
			}
			co := NewCoroutine(args[0])
			return []Value{CoroutineValue(co)}, nil
		},
	}))

	coLib.RawSet(StringValue("resume"), FunctionValue(&GoFunction{
		Name: "coroutine.resume",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.resume expects a coroutine")
			}
			co := args[0].Coroutine()
			resumeArgs := args[1:]
			return interp.resumeCoroutine(co, resumeArgs)
		},
	}))

	coLib.RawSet(StringValue("yield"), FunctionValue(&GoFunction{
		Name: "coroutine.yield",
		Fn: func(args []Value) ([]Value, error) {
			return interp.yieldFromCoroutine(args)
		},
	}))

	coLib.RawSet(StringValue("status"), FunctionValue(&GoFunction{
		Name: "coroutine.status",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.status expects a coroutine")
			}
			return []Value{StringValue(args[0].Coroutine().Status())}, nil
		},
	}))

	coLib.RawSet(StringValue("wrap"), FunctionValue(&GoFunction{
		Name: "coroutine.wrap",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 || !args[0].IsFunction() {
				return nil, fmt.Errorf("coroutine.wrap expects a function")
			}
			co := NewCoroutine(args[0])
			wrapper := &GoFunction{
				Name: "wrapped_coroutine",
				Fn: func(wargs []Value) ([]Value, error) {
					results, err := interp.resumeCoroutine(co, wargs)
					if err != nil {
						return nil, err
					}
					// results[0] is bool success flag
					if len(results) > 0 && !results[0].Bool() {
						if len(results) > 1 {
							return nil, fmt.Errorf("%s", results[1].String())
						}
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					// Strip the success flag, return remaining values
					return results[1:], nil
				},
			}
			return []Value{FunctionValue(wrapper)}, nil
		},
	}))

	coLib.RawSet(StringValue("isyieldable"), FunctionValue(&GoFunction{
		Name: "coroutine.isyieldable",
		Fn: func(args []Value) ([]Value, error) {
			return []Value{BoolValue(getCurrentCoroutine() != nil)}, nil
		},
	}))

	interp.globals.Define("coroutine", TableValue(coLib))
}

// resumeCoroutine resumes a suspended coroutine with the given arguments.
// Returns (true, values...) on success/yield, or (false, error_message) on failure.
func (interp *Interpreter) resumeCoroutine(co *Coroutine, args []Value) ([]Value, error) {
	if co.status == CoroutineDead {
		return []Value{BoolValue(false), StringValue("cannot resume dead coroutine")}, nil
	}
	if co.status == CoroutineRunning {
		return []Value{BoolValue(false), StringValue("cannot resume running coroutine")}, nil
	}

	// Save and restore the previous coroutine context so that nested
	// resume calls (coroutine resuming another coroutine) work correctly.
	prevCo := interp.currentCo

	co.status = CoroutineRunning

	if !co.started {
		co.started = true
		// Launch the goroutine. It creates its own Interpreter that shares
		// globals but has its own currentCo, avoiding data races.
		go func() {
			// Register this coroutine in the goroutine-local map so that
			// coroutine.yield can find it from within GoFunction closures.
			setCurrentCoroutine(co)
			defer setCurrentCoroutine(nil)

			coInterp := &Interpreter{
				globals:   interp.globals,
				currentCo: co,
			}
			// Wait for initial args from the first resume.
			initArgs := <-co.resumeCh
			// Call the coroutine body function.
			results, err := coInterp.callFunction(co.fn, initArgs)
			if results == nil {
				results = []Value{}
			}
			co.yieldCh <- yieldResult{values: results, err: err, done: true}
		}()
	}

	// Send args to the coroutine (initial args on first resume, or
	// values returned from yield on subsequent resumes).
	co.resumeCh <- args

	// Wait for the coroutine to yield or finish.
	result := <-co.yieldCh

	if result.done || result.err != nil {
		co.status = CoroutineDead
	} else {
		co.status = CoroutineSuspended
	}

	interp.currentCo = prevCo

	if result.err != nil {
		return []Value{BoolValue(false), StringValue(result.err.Error())}, nil
	}

	// Prepend true to indicate success.
	return append([]Value{BoolValue(true)}, result.values...), nil
}

// yieldFromCoroutine yields values from the currently running coroutine back
// to the caller of resume. It blocks until the coroutine is resumed again,
// and returns the values passed to the next resume call.
// It uses goroutine-local storage to find the correct coroutine, so it works
// correctly even though the GoFunction closure captures the main interpreter.
func (interp *Interpreter) yieldFromCoroutine(values []Value) ([]Value, error) {
	co := getCurrentCoroutine()
	if co == nil {
		return nil, fmt.Errorf("cannot yield from outside a coroutine")
	}
	// Send yielded values to the resume caller.
	co.yieldCh <- yieldResult{values: values}
	// Block until resumed.
	resumeVals := <-co.resumeCh
	return resumeVals, nil
}

// ====================================================================
// Metamethod helpers
// ====================================================================

const maxMetaDepth = 50

// getMetamethod returns the metamethod for the given event (__add, etc.)
// Returns the metamethod Value and true if found.
func (interp *Interpreter) getMetamethod(val Value, event string) (Value, bool) {
	var mt *Table
	if val.IsTable() {
		mt = val.Table().GetMetatable()
	}
	if mt == nil {
		return NilValue(), false
	}
	mm := mt.RawGet(StringValue(event))
	if mm.IsNil() {
		return NilValue(), false
	}
	return mm, true
}

// tableGet retrieves a value from a table, with __index metamethod support.
func (interp *Interpreter) tableGet(t Value, key Value) (Value, error) {
	return interp.tableGetDepth(t, key, 0)
}

func (interp *Interpreter) tableGetDepth(t Value, key Value, depth int) (Value, error) {
	if depth > maxMetaDepth {
		return NilValue(), fmt.Errorf("'__index' chain too long; possible loop")
	}
	if !t.IsTable() {
		return NilValue(), fmt.Errorf("attempt to index a %s value", t.TypeName())
	}

	tbl := t.Table()
	val := tbl.RawGet(key)

	if !val.IsNil() {
		return val, nil
	}

	// Try __index
	mt := tbl.GetMetatable()
	if mt == nil {
		return NilValue(), nil
	}

	index := mt.RawGet(StringValue("__index"))
	if index.IsNil() {
		return NilValue(), nil
	}

	if index.IsTable() {
		return interp.tableGetDepth(TableValue(index.Table()), key, depth+1)
	}

	if index.IsFunction() {
		results, err := interp.callFunction(index, []Value{t, key})
		if err != nil {
			return NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return NilValue(), nil
	}

	return NilValue(), nil
}

// tableSet assigns a value to a table, with __newindex metamethod support.
func (interp *Interpreter) tableSet(t Value, key, val Value) error {
	return interp.tableSetDepth(t, key, val, 0)
}

func (interp *Interpreter) tableSetDepth(t Value, key, val Value, depth int) error {
	if depth > maxMetaDepth {
		return fmt.Errorf("'__newindex' chain too long; possible loop")
	}
	if !t.IsTable() {
		return fmt.Errorf("attempt to index a %s value", t.TypeName())
	}

	tbl := t.Table()

	// Check if key already exists (rawget) - if so, just set it directly
	existing := tbl.RawGet(key)
	if !existing.IsNil() {
		tbl.RawSet(key, val)
		return nil
	}

	// Check __newindex
	mt := tbl.GetMetatable()
	if mt != nil {
		newindex := mt.RawGet(StringValue("__newindex"))
		if newindex.IsFunction() {
			_, err := interp.callFunction(newindex, []Value{t, key, val})
			return err
		}
		if newindex.IsTable() {
			return interp.tableSetDepth(TableValue(newindex.Table()), key, val, depth+1)
		}
	}

	tbl.RawSet(key, val)
	return nil
}

// opToMetamethod maps arithmetic operators to metamethod names.
func opToMetamethod(op string) string {
	switch op {
	case "+":
		return "__add"
	case "-":
		return "__sub"
	case "*":
		return "__mul"
	case "/":
		return "__div"
	case "%":
		return "__mod"
	case "**":
		return "__pow"
	default:
		return ""
	}
}

// ====================================================================
// Exec -- top-level entry
// ====================================================================

// Exec executes a program (top-level statement list).
func (interp *Interpreter) Exec(prog *ast.Program) error {
	for _, stmt := range prog.Stmts {
		_, isRet, _, _, err := interp.execStmt(stmt, interp.globals)
		if err != nil {
			return err
		}
		if isRet {
			return nil // top-level return stops execution
		}
	}
	return nil
}

// ====================================================================
// Statement execution
// ====================================================================

// execBlock executes a block of statements in a new child scope.
// Returns (returnValues, isReturn, isBreak, isContinue, error).
func (interp *Interpreter) execBlock(block *ast.BlockStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	child := NewEnvironment(env)
	return interp.execBlockInEnv(block, child)
}

// execBlockInEnv executes a block in the given environment (without creating a new scope).
func (interp *Interpreter) execBlockInEnv(block *ast.BlockStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	for _, stmt := range block.Stmts {
		retVals, isRet, isBrk, isCont, err := interp.execStmt(stmt, env)
		if err != nil {
			return nil, false, false, false, err
		}
		if isRet || isBrk || isCont {
			return retVals, isRet, isBrk, isCont, nil
		}
	}
	return nil, false, false, false, nil
}

// execStmt dispatches a single statement.
func (interp *Interpreter) execStmt(stmt ast.Stmt, env *Environment) ([]Value, bool, bool, bool, error) {
	switch s := stmt.(type) {
	case *ast.DeclareStmt:
		return interp.execDeclare(s, env)
	case *ast.AssignStmt:
		return interp.execAssign(s, env)
	case *ast.CompoundAssignStmt:
		return interp.execCompoundAssign(s, env)
	case *ast.IncDecStmt:
		return interp.execIncDec(s, env)
	case *ast.CallStmt:
		_, err := interp.evalExpr(s.Call, env)
		return nil, false, false, false, err
	case *ast.IfStmt:
		return interp.execIf(s, env)
	case *ast.ForStmt:
		return interp.execFor(s, env)
	case *ast.ForNumStmt:
		return interp.execForNum(s, env)
	case *ast.ForRangeStmt:
		return interp.execForRange(s, env)
	case *ast.ReturnStmt:
		return interp.execReturn(s, env)
	case *ast.BreakStmt:
		return nil, false, true, false, nil
	case *ast.ContinueStmt:
		return nil, false, false, true, nil
	case *ast.FuncDeclStmt:
		return interp.execFuncDecl(s, env)
	case *ast.BlockStmt:
		return interp.execBlock(s, env)
	default:
		return nil, false, false, false, fmt.Errorf("unknown statement type: %T", stmt)
	}
}

// ------------------------------------------------------------------
// DeclareStmt: a, b := expr1, expr2
// ------------------------------------------------------------------
func (interp *Interpreter) execDeclare(s *ast.DeclareStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	vals, err := interp.evalExprList(s.Values, env)
	if err != nil {
		return nil, false, false, false, err
	}
	for i, name := range s.Names {
		v := NilValue()
		if i < len(vals) {
			v = vals[i]
		}
		env.Define(name, v)
	}
	return nil, false, false, false, nil
}

// ------------------------------------------------------------------
// AssignStmt: a, b = expr1, expr2
// ------------------------------------------------------------------
func (interp *Interpreter) execAssign(s *ast.AssignStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	vals, err := interp.evalExprList(s.Values, env)
	if err != nil {
		return nil, false, false, false, err
	}
	for i, target := range s.Targets {
		v := NilValue()
		if i < len(vals) {
			v = vals[i]
		}
		if err := interp.assignTarget(target, v, env); err != nil {
			return nil, false, false, false, err
		}
	}
	return nil, false, false, false, nil
}

// assignTarget assigns a value to an lvalue expression.
func (interp *Interpreter) assignTarget(target ast.Expr, val Value, env *Environment) error {
	switch t := target.(type) {
	case *ast.IdentExpr:
		if !env.Set(t.Name, val) {
			// If variable doesn't exist anywhere, create it in the current env
			// (like a global implicit declaration)
			env.Define(t.Name, val)
		}
		return nil
	case *ast.IndexExpr:
		tbl, err := interp.evalExprSingle(t.Table, env)
		if err != nil {
			return err
		}
		key, err := interp.evalExprSingle(t.Index, env)
		if err != nil {
			return err
		}
		return interp.tableSet(tbl, key, val)
	case *ast.FieldExpr:
		tbl, err := interp.evalExprSingle(t.Table, env)
		if err != nil {
			return err
		}
		return interp.tableSet(tbl, StringValue(t.Field), val)
	default:
		return fmt.Errorf("invalid assignment target: %T", target)
	}
}

// ------------------------------------------------------------------
// CompoundAssignStmt: a += b
// ------------------------------------------------------------------
func (interp *Interpreter) execCompoundAssign(s *ast.CompoundAssignStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	lhs, err := interp.evalExprSingle(s.Target, env)
	if err != nil {
		return nil, false, false, false, err
	}
	rhs, err := interp.evalExprSingle(s.Value, env)
	if err != nil {
		return nil, false, false, false, err
	}

	var op string
	switch s.Op {
	case "+=":
		op = "+"
	case "-=":
		op = "-"
	case "*=":
		op = "*"
	case "/=":
		op = "/"
	default:
		return nil, false, false, false, fmt.Errorf("unknown compound operator: %s", s.Op)
	}

	result, err := interp.arith(op, lhs, rhs)
	if err != nil {
		return nil, false, false, false, err
	}

	if err := interp.assignTarget(s.Target, result, env); err != nil {
		return nil, false, false, false, err
	}
	return nil, false, false, false, nil
}

// ------------------------------------------------------------------
// IncDecStmt: a++ / a--
// ------------------------------------------------------------------
func (interp *Interpreter) execIncDec(s *ast.IncDecStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	lhs, err := interp.evalExprSingle(s.Target, env)
	if err != nil {
		return nil, false, false, false, err
	}

	var result Value
	one := IntValue(1)
	if s.Op == "++" {
		result, err = interp.arith("+", lhs, one)
	} else {
		result, err = interp.arith("-", lhs, one)
	}
	if err != nil {
		return nil, false, false, false, err
	}

	if err := interp.assignTarget(s.Target, result, env); err != nil {
		return nil, false, false, false, err
	}
	return nil, false, false, false, nil
}

// ------------------------------------------------------------------
// IfStmt
// ------------------------------------------------------------------
func (interp *Interpreter) execIf(s *ast.IfStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	cond, err := interp.evalExprSingle(s.Cond, env)
	if err != nil {
		return nil, false, false, false, err
	}
	if cond.Truthy() {
		return interp.execBlock(s.Body, env)
	}
	for _, ei := range s.ElseIfs {
		cond, err = interp.evalExprSingle(ei.Cond, env)
		if err != nil {
			return nil, false, false, false, err
		}
		if cond.Truthy() {
			return interp.execBlock(ei.Body, env)
		}
	}
	if s.ElseBody != nil {
		return interp.execBlock(s.ElseBody, env)
	}
	return nil, false, false, false, nil
}

// ------------------------------------------------------------------
// ForStmt (while-style): for cond { }
// ------------------------------------------------------------------
func (interp *Interpreter) execFor(s *ast.ForStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	for {
		if s.Cond != nil {
			cond, err := interp.evalExprSingle(s.Cond, env)
			if err != nil {
				return nil, false, false, false, err
			}
			if !cond.Truthy() {
				break
			}
		}
		retVals, isRet, isBrk, _, err := interp.execBlock(s.Body, env)
		if err != nil {
			return nil, false, false, false, err
		}
		if isRet {
			return retVals, true, false, false, nil
		}
		if isBrk {
			break
		}
		// isContinue just goes to next iteration
	}
	return nil, false, false, false, nil
}

// ------------------------------------------------------------------
// ForNumStmt (C-style): for init; cond; post { }
// ------------------------------------------------------------------
func (interp *Interpreter) execForNum(s *ast.ForNumStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	// The init, cond, and post all share a new scope
	loopEnv := NewEnvironment(env)
	// Execute init
	_, _, _, _, err := interp.execStmt(s.Init, loopEnv)
	if err != nil {
		return nil, false, false, false, err
	}
	for {
		// Evaluate condition
		cond, err := interp.evalExprSingle(s.Cond, loopEnv)
		if err != nil {
			return nil, false, false, false, err
		}
		if !cond.Truthy() {
			break
		}
		// Execute body in a child of loopEnv
		retVals, isRet, isBrk, _, err := interp.execBlock(s.Body, loopEnv)
		if err != nil {
			return nil, false, false, false, err
		}
		if isRet {
			return retVals, true, false, false, nil
		}
		if isBrk {
			break
		}
		// Execute post
		_, _, _, _, err = interp.execStmt(s.Post, loopEnv)
		if err != nil {
			return nil, false, false, false, err
		}
	}
	return nil, false, false, false, nil
}

// ------------------------------------------------------------------
// ForRangeStmt: for k, v := range expr { }
// ------------------------------------------------------------------
func (interp *Interpreter) execForRange(s *ast.ForRangeStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	iterVal, err := interp.evalExprSingle(s.Iter, env)
	if err != nil {
		return nil, false, false, false, err
	}

	if iterVal.IsTable() {
		tbl := iterVal.Table()
		var key Value = NilValue()
		for {
			nextKey, nextVal, ok := tbl.Next(key)
			if !ok {
				break
			}
			key = nextKey

			// Create new scope for each iteration
			iterEnv := NewEnvironment(env)
			iterEnv.Define(s.Key, nextKey)
			if s.Value != "" {
				iterEnv.Define(s.Value, nextVal)
			}

			retVals, isRet, isBrk, _, err := interp.execBlockInEnv(s.Body, iterEnv)
			if err != nil {
				return nil, false, false, false, err
			}
			if isRet {
				return retVals, true, false, false, nil
			}
			if isBrk {
				break
			}
		}
		return nil, false, false, false, nil
	}

	if iterVal.IsFunction() {
		// Iterator function: call repeatedly until nil
		for {
			results, err := interp.callFunction(iterVal, nil)
			if err != nil {
				return nil, false, false, false, err
			}
			if len(results) == 0 || results[0].IsNil() {
				break
			}
			iterEnv := NewEnvironment(env)
			iterEnv.Define(s.Key, results[0])
			if s.Value != "" {
				v := NilValue()
				if len(results) > 1 {
					v = results[1]
				}
				iterEnv.Define(s.Value, v)
			}
			retVals, isRet, isBrk, _, err := interp.execBlockInEnv(s.Body, iterEnv)
			if err != nil {
				return nil, false, false, false, err
			}
			if isRet {
				return retVals, true, false, false, nil
			}
			if isBrk {
				break
			}
		}
		return nil, false, false, false, nil
	}

	return nil, false, false, false, fmt.Errorf("cannot range over %s", iterVal.TypeName())
}

// ------------------------------------------------------------------
// ReturnStmt
// ------------------------------------------------------------------
func (interp *Interpreter) execReturn(s *ast.ReturnStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	vals, err := interp.evalExprList(s.Values, env)
	if err != nil {
		return nil, false, false, false, err
	}
	return vals, true, false, false, nil
}

// ------------------------------------------------------------------
// FuncDeclStmt
// ------------------------------------------------------------------
func (interp *Interpreter) execFuncDecl(s *ast.FuncDeclStmt, env *Environment) ([]Value, bool, bool, bool, error) {
	proto := &FuncProto{
		Name: s.Name,
		Body: s.Body,
	}
	paramNames := make([]string, 0, len(s.Params))
	for _, p := range s.Params {
		paramNames = append(paramNames, p.Name)
		proto.Params = append(proto.Params, p.Name)
		if p.IsVarArg {
			proto.HasVarArg = true
		}
	}

	// Define the function name first so it can self-reference (recursion)
	env.Define(s.Name, NilValue())

	// Capture free variables from the enclosing environment
	freeVarNames := FreeVars(s.Body, paramNames)
	upvalues := make(map[string]*Upvalue)
	for _, fv := range freeVarNames {
		if uv, ok := env.GetUpvalue(fv); ok {
			upvalues[fv] = uv
		}
	}

	closure := &Closure{
		Proto:    proto,
		Upvalues: upvalues,
		Env:      env,
	}
	env.Set(s.Name, FunctionValue(closure))
	return nil, false, false, false, nil
}

// ====================================================================
// Expression evaluation
// ====================================================================

// evalExpr evaluates an expression and returns a slice of Values.
// Most expressions return a single-element slice; CallExpr may return multiple.
func (interp *Interpreter) evalExpr(expr ast.Expr, env *Environment) ([]Value, error) {
	switch e := expr.(type) {
	case *ast.NumberLit:
		v, err := parseNumber(e.Value)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil

	case *ast.StringLit:
		return []Value{StringValue(e.Value)}, nil

	case *ast.BoolLit:
		return []Value{BoolValue(e.Value)}, nil

	case *ast.NilLit:
		return []Value{NilValue()}, nil

	case *ast.VarArgExpr:
		v, ok := env.Get("...")
		if !ok {
			return nil, nil
		}
		// varargs are stored as a table in "..."
		if v.IsTable() {
			tbl := v.Table()
			n := tbl.Length()
			result := make([]Value, n)
			for i := 1; i <= n; i++ {
				result[i-1] = tbl.RawGet(IntValue(int64(i)))
			}
			return result, nil
		}
		return []Value{v}, nil

	case *ast.IdentExpr:
		v, ok := env.Get(e.Name)
		if !ok {
			return nil, fmt.Errorf("undefined variable: %s", e.Name)
		}
		return []Value{v}, nil

	case *ast.BinaryExpr:
		v, err := interp.evalBinary(e, env)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil

	case *ast.UnaryExpr:
		v, err := interp.evalUnary(e, env)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil

	case *ast.IndexExpr:
		tbl, err := interp.evalExprSingle(e.Table, env)
		if err != nil {
			return nil, err
		}
		key, err := interp.evalExprSingle(e.Index, env)
		if err != nil {
			return nil, err
		}
		val, err := interp.tableGet(tbl, key)
		if err != nil {
			return nil, err
		}
		return []Value{val}, nil

	case *ast.FieldExpr:
		tbl, err := interp.evalExprSingle(e.Table, env)
		if err != nil {
			return nil, err
		}
		val, err := interp.tableGet(tbl, StringValue(e.Field))
		if err != nil {
			return nil, err
		}
		return []Value{val}, nil

	case *ast.CallExpr:
		return interp.evalCall(e, env)

	case *ast.MethodCallExpr:
		return interp.evalMethodCall(e, env)

	case *ast.FuncLitExpr:
		v := interp.makeClosure(e.Params, e.Body, "", env)
		return []Value{v}, nil

	case *ast.TableLitExpr:
		v, err := interp.evalTableLit(e, env)
		if err != nil {
			return nil, err
		}
		return []Value{v}, nil

	default:
		return nil, fmt.Errorf("unknown expression type: %T", expr)
	}
}

// evalExprSingle evaluates an expression and returns a single Value.
// For VarArgExpr, returns the varargs table itself (not the first expanded element),
// so that #... and ...[i] work correctly.
func (interp *Interpreter) evalExprSingle(expr ast.Expr, env *Environment) (Value, error) {
	// Special case: VarArgExpr in single-value context returns the table.
	if _, ok := expr.(*ast.VarArgExpr); ok {
		v, ok := env.Get("...")
		if !ok {
			return NilValue(), nil
		}
		return v, nil
	}
	vals, err := interp.evalExpr(expr, env)
	if err != nil {
		return NilValue(), err
	}
	if len(vals) == 0 {
		return NilValue(), nil
	}
	return vals[0], nil
}

// evalExprList evaluates a list of expressions, expanding the last one for
// multiple return values.
func (interp *Interpreter) evalExprList(exprs []ast.Expr, env *Environment) ([]Value, error) {
	if len(exprs) == 0 {
		return nil, nil
	}
	var result []Value
	for i, expr := range exprs {
		vals, err := interp.evalExpr(expr, env)
		if err != nil {
			return nil, err
		}
		if i == len(exprs)-1 {
			// Last expression: expand all return values
			result = append(result, vals...)
		} else {
			// Not last: take only first value
			if len(vals) > 0 {
				result = append(result, vals[0])
			} else {
				result = append(result, NilValue())
			}
		}
	}
	return result, nil
}

// ------------------------------------------------------------------
// Binary expressions
// ------------------------------------------------------------------
func (interp *Interpreter) evalBinary(e *ast.BinaryExpr, env *Environment) (Value, error) {
	// Short-circuit operators
	if e.Op == "&&" {
		left, err := interp.evalExprSingle(e.Left, env)
		if err != nil {
			return NilValue(), err
		}
		if !left.Truthy() {
			return left, nil
		}
		return interp.evalExprSingle(e.Right, env)
	}
	if e.Op == "||" {
		left, err := interp.evalExprSingle(e.Left, env)
		if err != nil {
			return NilValue(), err
		}
		if left.Truthy() {
			return left, nil
		}
		return interp.evalExprSingle(e.Right, env)
	}

	left, err := interp.evalExprSingle(e.Left, env)
	if err != nil {
		return NilValue(), err
	}
	right, err := interp.evalExprSingle(e.Right, env)
	if err != nil {
		return NilValue(), err
	}

	switch e.Op {
	case "+", "-", "*", "/", "%", "**":
		return interp.arith(e.Op, left, right)
	case "..":
		return interp.concat(left, right)
	case "==":
		eq, err := interp.valEqual(left, right)
		if err != nil {
			return NilValue(), err
		}
		return BoolValue(eq), nil
	case "!=":
		eq, err := interp.valEqual(left, right)
		if err != nil {
			return NilValue(), err
		}
		return BoolValue(!eq), nil
	case "<":
		res, err := interp.valLessThan(left, right)
		if err != nil {
			return NilValue(), err
		}
		return BoolValue(res), nil
	case "<=":
		res, err := interp.valLessEqual(left, right)
		if err != nil {
			return NilValue(), err
		}
		return BoolValue(res), nil
	case ">":
		// a > b is equivalent to b < a
		res, err := interp.valLessThan(right, left)
		if err != nil {
			return NilValue(), err
		}
		return BoolValue(res), nil
	case ">=":
		// a >= b is equivalent to b <= a
		res, err := interp.valLessEqual(right, left)
		if err != nil {
			return NilValue(), err
		}
		return BoolValue(res), nil
	default:
		return NilValue(), fmt.Errorf("unknown binary operator: %s", e.Op)
	}
}

// arith performs arithmetic on two values, with metamethod fallback.
func (interp *Interpreter) arith(op string, left, right Value) (Value, error) {
	// Try to coerce strings to numbers
	l, lok := left.ToNumber()
	r, rok := right.ToNumber()
	if !lok || !rok {
		// Try metamethod before giving up
		mmName := opToMetamethod(op)
		if mmName != "" {
			if mm, ok := interp.getMetamethod(left, mmName); ok {
				results, err := interp.callFunction(mm, []Value{left, right})
				if err != nil {
					return NilValue(), err
				}
				if len(results) > 0 {
					return results[0], nil
				}
				return NilValue(), nil
			}
			if mm, ok := interp.getMetamethod(right, mmName); ok {
				results, err := interp.callFunction(mm, []Value{left, right})
				if err != nil {
					return NilValue(), err
				}
				if len(results) > 0 {
					return results[0], nil
				}
				return NilValue(), nil
			}
		}
		return NilValue(), fmt.Errorf("attempt to perform arithmetic on a %s value", map[bool]string{true: right.TypeName(), false: left.TypeName()}[lok])
	}
	left, right = l, r

	// If both are ints and the op keeps integer domain, use int arithmetic
	if left.IsInt() && right.IsInt() {
		a, b := left.Int(), right.Int()
		switch op {
		case "+":
			return IntValue(a + b), nil
		case "-":
			return IntValue(a - b), nil
		case "*":
			return IntValue(a * b), nil
		case "/":
			// Integer division produces float (like Lua 5.3 / operator)
			if b == 0 {
				return NilValue(), fmt.Errorf("attempt to divide by zero")
			}
			// If evenly divisible, keep int
			if a%b == 0 {
				return IntValue(a / b), nil
			}
			return FloatValue(float64(a) / float64(b)), nil
		case "%":
			if b == 0 {
				return NilValue(), fmt.Errorf("attempt to perform modulo by zero")
			}
			return IntValue(a % b), nil
		case "**":
			if b >= 0 && b < 64 {
				return IntValue(intPow(a, b)), nil
			}
			return FloatValue(math.Pow(float64(a), float64(b))), nil
		}
	}

	// Float arithmetic
	a, b := left.Number(), right.Number()
	switch op {
	case "+":
		return FloatValue(a + b), nil
	case "-":
		return FloatValue(a - b), nil
	case "*":
		return FloatValue(a * b), nil
	case "/":
		if b == 0 {
			return NilValue(), fmt.Errorf("attempt to divide by zero")
		}
		return FloatValue(a / b), nil
	case "%":
		if b == 0 {
			return NilValue(), fmt.Errorf("attempt to perform modulo by zero")
		}
		return FloatValue(math.Mod(a, b)), nil
	case "**":
		return FloatValue(math.Pow(a, b)), nil
	default:
		return NilValue(), fmt.Errorf("unknown arithmetic operator: %s", op)
	}
}

// intPow computes a^b for non-negative b using exponentiation by squaring.
func intPow(a, b int64) int64 {
	result := int64(1)
	for b > 0 {
		if b&1 == 1 {
			result *= a
		}
		a *= a
		b >>= 1
	}
	return result
}

// concat performs string concatenation, with __concat metamethod fallback.
func (interp *Interpreter) concat(left, right Value) (Value, error) {
	ls := valueToStr(left)
	rs := valueToStr(right)
	canL := canConcatType(left)
	canR := canConcatType(right)
	if canL && canR {
		return StringValue(ls + rs), nil
	}
	// Try __concat metamethod
	if mm, ok := interp.getMetamethod(left, "__concat"); ok {
		results, err := interp.callFunction(mm, []Value{left, right})
		if err != nil {
			return NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return NilValue(), nil
	}
	if mm, ok := interp.getMetamethod(right, "__concat"); ok {
		results, err := interp.callFunction(mm, []Value{left, right})
		if err != nil {
			return NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return NilValue(), nil
	}
	if !canL {
		return NilValue(), fmt.Errorf("attempt to concatenate a %s value", left.TypeName())
	}
	return NilValue(), fmt.Errorf("attempt to concatenate a %s value", right.TypeName())
}

// valEqual compares two values for equality, with __eq metamethod support.
func (interp *Interpreter) valEqual(a, b Value) (bool, error) {
	// For primitive types, use raw equality
	if a.IsTable() && b.IsTable() {
		if a.Table() == b.Table() {
			return true, nil
		}
		// Try __eq from a's metatable, then b's
		if mm, ok := interp.getMetamethod(a, "__eq"); ok {
			results, err := interp.callFunction(mm, []Value{a, b})
			if err != nil {
				return false, err
			}
			if len(results) > 0 {
				return results[0].Truthy(), nil
			}
			return false, nil
		}
		if mm, ok := interp.getMetamethod(b, "__eq"); ok {
			results, err := interp.callFunction(mm, []Value{a, b})
			if err != nil {
				return false, err
			}
			if len(results) > 0 {
				return results[0].Truthy(), nil
			}
			return false, nil
		}
		return false, nil
	}
	return a.Equal(b), nil
}

// valLessThan compares two values with < operator, with __lt metamethod support.
func (interp *Interpreter) valLessThan(a, b Value) (bool, error) {
	// Try normal comparison first
	ok, valid := a.lessThan(b)
	if valid {
		return ok, nil
	}
	// Try __lt metamethod
	if mm, found := interp.getMetamethod(a, "__lt"); found {
		results, err := interp.callFunction(mm, []Value{a, b})
		if err != nil {
			return false, err
		}
		if len(results) > 0 {
			return results[0].Truthy(), nil
		}
		return false, nil
	}
	if mm, found := interp.getMetamethod(b, "__lt"); found {
		results, err := interp.callFunction(mm, []Value{a, b})
		if err != nil {
			return false, err
		}
		if len(results) > 0 {
			return results[0].Truthy(), nil
		}
		return false, nil
	}
	return false, fmt.Errorf("attempt to compare %s with %s", a.TypeName(), b.TypeName())
}

// valLessEqual compares two values with <= operator, with __le metamethod support.
func (interp *Interpreter) valLessEqual(a, b Value) (bool, error) {
	// Try normal comparison first
	less, valid := a.lessThan(b)
	if valid {
		return less || a.Equal(b), nil
	}
	// Try __le metamethod
	if mm, found := interp.getMetamethod(a, "__le"); found {
		results, err := interp.callFunction(mm, []Value{a, b})
		if err != nil {
			return false, err
		}
		if len(results) > 0 {
			return results[0].Truthy(), nil
		}
		return false, nil
	}
	if mm, found := interp.getMetamethod(b, "__le"); found {
		results, err := interp.callFunction(mm, []Value{a, b})
		if err != nil {
			return false, err
		}
		if len(results) > 0 {
			return results[0].Truthy(), nil
		}
		return false, nil
	}
	return false, fmt.Errorf("attempt to compare %s with %s", a.TypeName(), b.TypeName())
}

func canConcatType(v Value) bool {
	return v.IsString() || v.IsNumber()
}

func valueToStr(v Value) string {
	switch v.Type() {
	case TypeString:
		return v.Str()
	case TypeInt:
		return strconv.FormatInt(v.Int(), 10)
	case TypeFloat:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	default:
		return ""
	}
}

// ------------------------------------------------------------------
// Unary expressions
// ------------------------------------------------------------------
func (interp *Interpreter) evalUnary(e *ast.UnaryExpr, env *Environment) (Value, error) {
	operand, err := interp.evalExprSingle(e.Operand, env)
	if err != nil {
		return NilValue(), err
	}
	switch e.Op {
	case "-":
		n, ok := operand.ToNumber()
		if !ok {
			// Try __unm metamethod
			if mm, found := interp.getMetamethod(operand, "__unm"); found {
				results, err := interp.callFunction(mm, []Value{operand})
				if err != nil {
					return NilValue(), err
				}
				if len(results) > 0 {
					return results[0], nil
				}
				return NilValue(), nil
			}
			return NilValue(), fmt.Errorf("attempt to perform arithmetic on a %s value", operand.TypeName())
		}
		if n.IsInt() {
			return IntValue(-n.Int()), nil
		}
		return FloatValue(-n.Float()), nil
	case "!":
		return BoolValue(!operand.Truthy()), nil
	case "#":
		// Check __len metamethod first for tables
		if operand.IsTable() {
			if mm, ok := interp.getMetamethod(operand, "__len"); ok {
				results, err := interp.callFunction(mm, []Value{operand})
				if err != nil {
					return NilValue(), err
				}
				if len(results) > 0 {
					return results[0], nil
				}
				return NilValue(), nil
			}
		}
		switch operand.Type() {
		case TypeString:
			return IntValue(int64(len(operand.Str()))), nil
		case TypeTable:
			return IntValue(int64(operand.Table().Length())), nil
		default:
			return NilValue(), fmt.Errorf("attempt to get length of a %s value", operand.TypeName())
		}
	default:
		return NilValue(), fmt.Errorf("unknown unary operator: %s", e.Op)
	}
}

// ------------------------------------------------------------------
// Call expressions
// ------------------------------------------------------------------
func (interp *Interpreter) evalCall(e *ast.CallExpr, env *Environment) ([]Value, error) {
	fn, err := interp.evalExprSingle(e.Func, env)
	if err != nil {
		return nil, err
	}

	// Build args with last-arg expansion
	args, err := interp.evalExprList(e.Args, env)
	if err != nil {
		return nil, err
	}

	return interp.callFunction(fn, args)
}

func (interp *Interpreter) evalMethodCall(e *ast.MethodCallExpr, env *Environment) ([]Value, error) {
	obj, err := interp.evalExprSingle(e.Object, env)
	if err != nil {
		return nil, err
	}
	if !obj.IsTable() {
		return nil, fmt.Errorf("attempt to call method on a %s value", obj.TypeName())
	}
	method, err := interp.tableGet(obj, StringValue(e.Method))
	if err != nil {
		return nil, err
	}
	if !method.IsFunction() {
		return nil, fmt.Errorf("attempt to call a %s value", method.TypeName())
	}

	args, err := interp.evalExprList(e.Args, env)
	if err != nil {
		return nil, err
	}
	// Prepend self as first argument
	args = append([]Value{obj}, args...)

	return interp.callFunction(method, args)
}

// callFunction invokes a function value with the given arguments.
// If fn is a table with a __call metamethod, invokes that instead.
func (interp *Interpreter) callFunction(fn Value, args []Value) ([]Value, error) {
	if !fn.IsFunction() {
		// Try __call metamethod for tables
		if fn.IsTable() {
			if mm, ok := interp.getMetamethod(fn, "__call"); ok {
				// Prepend the table itself as first arg (Lua convention)
				newArgs := make([]Value, 0, len(args)+1)
				newArgs = append(newArgs, fn)
				newArgs = append(newArgs, args...)
				return interp.callFunction(mm, newArgs)
			}
		}
		return nil, fmt.Errorf("attempt to call a %s value", fn.TypeName())
	}

	if gf := fn.GoFunction(); gf != nil {
		return gf.Fn(args)
	}

	cl := fn.Closure()
	if cl == nil {
		return nil, fmt.Errorf("attempt to call a nil function")
	}

	// Create a new environment for the function body.
	// Parent is the globals so that built-in functions are accessible.
	// Captured upvalues are injected directly -- they provide lexical scoping.
	callEnv := NewEnvironment(interp.globals)

	// Inject captured upvalues: these share the same *Upvalue pointer as the
	// enclosing scope, so mutations are visible to all closures that share them.
	for name, uv := range cl.Upvalues {
		callEnv.DefineUpvalue(name, uv)
	}

	proto := cl.Proto
	// Bind parameters (as new local variables -- these shadow any captured upvalues)
	nParams := len(proto.Params)
	if proto.HasVarArg {
		nParams-- // last param is the vararg collector name
	}

	for i := 0; i < nParams; i++ {
		v := NilValue()
		if i < len(args) {
			v = args[i]
		}
		callEnv.Define(proto.Params[i], v)
	}

	if proto.HasVarArg {
		// Collect remaining args into a table stored as "..."
		varargs := NewTable()
		start := nParams
		for i := start; i < len(args); i++ {
			varargs.RawSet(IntValue(int64(i-start+1)), args[i])
		}
		callEnv.Define("...", TableValue(varargs))
	}

	retVals, isRet, _, _, err := interp.execBlockInEnv(proto.Body, callEnv)
	if err != nil {
		return nil, err
	}
	if isRet {
		return retVals, nil
	}
	return nil, nil
}

// ------------------------------------------------------------------
// Function literal
// ------------------------------------------------------------------
func (interp *Interpreter) makeClosure(params []ast.FuncParam, body *ast.BlockStmt, name string, env *Environment) Value {
	proto := &FuncProto{
		Name: name,
		Body: body,
	}
	paramNames := make([]string, 0, len(params))
	for _, p := range params {
		paramNames = append(paramNames, p.Name)
		proto.Params = append(proto.Params, p.Name)
		if p.IsVarArg {
			proto.HasVarArg = true
		}
	}

	// Capture free variables from the enclosing environment
	freeVarNames := FreeVars(body, paramNames)
	upvalues := make(map[string]*Upvalue)
	for _, fv := range freeVarNames {
		if uv, ok := env.GetUpvalue(fv); ok {
			upvalues[fv] = uv
		}
		// If not found in any scope, it's a global or builtin -- don't capture.
		// It will be resolved via interp.globals at call time.
	}

	closure := &Closure{
		Proto:    proto,
		Upvalues: upvalues,
		Env:      env,
	}
	return FunctionValue(closure)
}

// ------------------------------------------------------------------
// Table literal
// ------------------------------------------------------------------
func (interp *Interpreter) evalTableLit(e *ast.TableLitExpr, env *Environment) (Value, error) {
	tbl := NewTable()
	arrayIdx := int64(1) // 1-indexed auto-incrementing key for positional fields

	for i, field := range e.Fields {
		if field.Key == nil {
			// Array-style (positional)
			var val Value
			var err error
			if i == len(e.Fields)-1 {
				// Last field: expand multiple returns
				vals, err2 := interp.evalExpr(field.Value, env)
				if err2 != nil {
					return NilValue(), err2
				}
				for _, v := range vals {
					tbl.RawSet(IntValue(arrayIdx), v)
					arrayIdx++
				}
				continue
			}
			val, err = interp.evalExprSingle(field.Value, env)
			if err != nil {
				return NilValue(), err
			}
			tbl.RawSet(IntValue(arrayIdx), val)
			arrayIdx++
		} else {
			// Keyed field
			key, err := interp.evalExprSingle(field.Key, env)
			if err != nil {
				return NilValue(), err
			}
			val, err := interp.evalExprSingle(field.Value, env)
			if err != nil {
				return NilValue(), err
			}
			tbl.RawSet(key, val)
		}
	}

	return TableValue(tbl), nil
}

// ------------------------------------------------------------------
// Number parsing
// ------------------------------------------------------------------
func parseNumber(s string) (Value, error) {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return IntValue(i), nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return FloatValue(f), nil
	}
	return NilValue(), fmt.Errorf("invalid number: %s", s)
}
