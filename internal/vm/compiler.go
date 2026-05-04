package vm

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gscript/gscript/internal/ast"
	"github.com/gscript/gscript/internal/runtime"
)

// --------------------------------------------------------------------
// Compiler: AST -> bytecode FuncProto
// --------------------------------------------------------------------

// Compile compiles a top-level program into a FuncProto.
func Compile(prog *ast.Program) (*FuncProto, error) {
	c := newCompiler(nil, "<main>", 0, false)
	for _, stmt := range prog.Stmts {
		if err := c.compileStmt(stmt); err != nil {
			return nil, err
		}
	}
	c.emitReturn(0, 1)
	return c.finish(), nil
}

// --------------------------------------------------------------------
// Internal types
// --------------------------------------------------------------------

type localVar struct {
	name     string
	reg      int
	depth    int
	captured bool
}

type loopInfo struct {
	breakJumps    []int
	continueJumps []int
	scopeDepth    int
}

type upvalInfo struct {
	name    string
	inStack bool
	index   int
}

type compiler struct {
	parent   *compiler
	proto    *FuncProto
	locals   []localVar
	upvals   []upvalInfo
	nextReg  int
	maxReg   int
	depth    int
	loops    []loopInfo
	isVarArg bool
}

func newCompiler(parent *compiler, name string, line int, isVarArg bool) *compiler {
	return &compiler{
		parent: parent,
		proto: &FuncProto{
			Name:        name,
			LineDefined: line,
		},
		isVarArg: isVarArg,
	}
}

// isMainTopLevel returns true when we are at the top scope of the main chunk.
// Declarations at this level create globals rather than locals.
func (c *compiler) isMainTopLevel() bool {
	return c.parent == nil && c.depth == 0
}

// --------------------------------------------------------------------
// Register allocator
// --------------------------------------------------------------------

func (c *compiler) allocReg() int {
	r := c.nextReg
	c.nextReg++
	if c.nextReg > c.maxReg {
		c.maxReg = c.nextReg
	}
	return r
}

func (c *compiler) allocRegs(n int) int {
	base := c.nextReg
	c.nextReg += n
	if c.nextReg > c.maxReg {
		c.maxReg = c.nextReg
	}
	return base
}

func (c *compiler) freeReg()       { c.nextReg-- }
func (c *compiler) freeRegs(n int) { c.nextReg -= n }

// --------------------------------------------------------------------
// Scoping
// --------------------------------------------------------------------

func (c *compiler) enterScope() { c.depth++ }

func (c *compiler) leaveScope() {
	hasCaptured := false
	firstCapturedReg := c.nextReg
	count := 0
	for i := len(c.locals) - 1; i >= 0; i-- {
		if c.locals[i].depth < c.depth {
			break
		}
		if c.locals[i].captured {
			hasCaptured = true
			if c.locals[i].reg < firstCapturedReg {
				firstCapturedReg = c.locals[i].reg
			}
		}
		count++
	}
	if hasCaptured {
		c.emit(EncodeABC(OP_CLOSE, firstCapturedReg, 0, 0), 0)
	}
	c.locals = c.locals[:len(c.locals)-count]
	if count > 0 {
		c.freeRegs(count)
	}
	c.depth--
}

// --------------------------------------------------------------------
// Local variable management
// --------------------------------------------------------------------

func (c *compiler) addLocal(name string) int {
	reg := c.allocReg()
	c.locals = append(c.locals, localVar{name: name, reg: reg, depth: c.depth})
	return reg
}

func (c *compiler) resolveLocal(name string) int {
	for i := len(c.locals) - 1; i >= 0; i-- {
		if c.locals[i].name == name {
			return c.locals[i].reg
		}
	}
	return -1
}

func (c *compiler) resolveUpvalue(name string) int {
	if c.parent == nil {
		return -1
	}
	for i := len(c.parent.locals) - 1; i >= 0; i-- {
		if c.parent.locals[i].name == name {
			c.parent.locals[i].captured = true
			return c.addUpvalue(name, true, c.parent.locals[i].reg)
		}
	}
	parentUpIdx := c.parent.resolveUpvalue(name)
	if parentUpIdx >= 0 {
		return c.addUpvalue(name, false, parentUpIdx)
	}
	return -1
}

func (c *compiler) addUpvalue(name string, inStack bool, index int) int {
	for i, uv := range c.upvals {
		if uv.inStack == inStack && uv.index == index {
			return i
		}
	}
	idx := len(c.upvals)
	c.upvals = append(c.upvals, upvalInfo{name: name, inStack: inStack, index: index})
	return idx
}

// --------------------------------------------------------------------
// Constant pool
// --------------------------------------------------------------------

func (c *compiler) addConst(v runtime.Value) int {
	for i, k := range c.proto.Constants {
		if k.Equal(v) && k.Type() == v.Type() {
			return i
		}
	}
	idx := len(c.proto.Constants)
	c.proto.Constants = append(c.proto.Constants, v)
	return idx
}

func (c *compiler) stringConst(s string) int { return c.addConst(runtime.StringValue(s)) }
func (c *compiler) intConst(i int64) int     { return c.addConst(runtime.IntValue(i)) }
func (c *compiler) floatConst(f float64) int { return c.addConst(runtime.FloatValue(f)) }

// --------------------------------------------------------------------
// Code emission
// --------------------------------------------------------------------

func (c *compiler) emit(inst uint32, line int) int {
	pos := len(c.proto.Code)
	c.proto.Code = append(c.proto.Code, inst)
	c.proto.LineInfo = append(c.proto.LineInfo, line)
	return pos
}

func (c *compiler) emitABC(op Opcode, a, b, cc int, line int) int {
	return c.emit(EncodeABC(op, a, b, cc), line)
}
func (c *compiler) emitABx(op Opcode, a, bx int, line int) int {
	return c.emit(EncodeABx(op, a, bx), line)
}
func (c *compiler) emitAsBx(op Opcode, a, sbx int, line int) int {
	return c.emit(EncodeAsBx(op, a, sbx), line)
}
func (c *compiler) emitJump(line int) int {
	return c.emit(EncodesBx(OP_JMP, 0), line)
}
func (c *compiler) emitReturn(a, b int) int {
	return c.emitABC(OP_RETURN, a, b, 0, 0)
}

func (c *compiler) patchJump(jmpPos int) {
	target := len(c.proto.Code)
	offset := target - jmpPos - 1
	c.proto.Code[jmpPos] = EncodesBx(OP_JMP, offset)
}

func (c *compiler) patchJumpTo(jmpPos int, target int) {
	offset := target - jmpPos - 1
	c.proto.Code[jmpPos] = EncodesBx(OP_JMP, offset)
}

func (c *compiler) currentPC() int { return len(c.proto.Code) }

// --------------------------------------------------------------------
// Loop management
// --------------------------------------------------------------------

func (c *compiler) pushLoop() {
	c.loops = append(c.loops, loopInfo{scopeDepth: c.depth})
}

func (c *compiler) popLoop() loopInfo {
	info := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	return info
}

func (c *compiler) currentLoop() *loopInfo {
	if len(c.loops) == 0 {
		return nil
	}
	return &c.loops[len(c.loops)-1]
}

func (c *compiler) patchBreaks(info loopInfo) {
	for _, pos := range info.breakJumps {
		c.patchJump(pos)
	}
}

func (c *compiler) patchContinues(info loopInfo, target int) {
	for _, pos := range info.continueJumps {
		c.patchJumpTo(pos, target)
	}
}

func (c *compiler) emitCloseForJump() {
	loop := c.currentLoop()
	if loop == nil {
		return
	}
	for i := len(c.locals) - 1; i >= 0; i-- {
		if c.locals[i].depth <= loop.scopeDepth {
			break
		}
		if c.locals[i].captured {
			c.emit(EncodeABC(OP_CLOSE, c.locals[i].reg, 0, 0), 0)
			return
		}
	}
}

// --------------------------------------------------------------------
// Finish: build the final FuncProto
// --------------------------------------------------------------------

func (c *compiler) finish() *FuncProto {
	c.proto.MaxStack = c.maxReg + 2
	c.proto.NumParams = 0
	c.proto.IsVarArg = c.isVarArg
	c.proto.Upvalues = make([]UpvalDesc, len(c.upvals))
	for i, uv := range c.upvals {
		c.proto.Upvalues[i] = UpvalDesc{Name: uv.name, InStack: uv.inStack, Index: uv.index}
	}
	return c.proto
}

// --------------------------------------------------------------------
// Helper: compile an expression into an allocated temp register
// If the expression is a local variable reference, returns its register
// without allocating (isTemp=false).
// --------------------------------------------------------------------

func (c *compiler) compileExprReg(expr ast.Expr) (reg int, isTemp bool, err error) {
	if ident, ok := expr.(*ast.IdentExpr); ok {
		r := c.resolveLocal(ident.Name)
		if r >= 0 {
			return r, false, nil
		}
	}
	r := c.allocReg()
	if e := c.compileExprTo(expr, r); e != nil {
		return 0, false, e
	}
	return r, true, nil
}

// --------------------------------------------------------------------
// Statement compilation
// --------------------------------------------------------------------

func (c *compiler) compileStmt(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.DeclareStmt:
		return c.compileDeclareStmt(s)
	case *ast.AssignStmt:
		return c.compileAssignStmt(s)
	case *ast.CompoundAssignStmt:
		return c.compileCompoundAssignStmt(s)
	case *ast.IncDecStmt:
		return c.compileIncDecStmt(s)
	case *ast.CallStmt:
		return c.compileCallStmt(s)
	case *ast.IfStmt:
		return c.compileIfStmt(s)
	case *ast.ForNumStmt:
		return c.compileForNumStmt(s)
	case *ast.ForStmt:
		return c.compileForStmt(s)
	case *ast.ForRangeStmt:
		return c.compileForRangeStmt(s)
	case *ast.ReturnStmt:
		return c.compileReturnStmt(s)
	case *ast.BreakStmt:
		return c.compileBreakStmt(s)
	case *ast.ContinueStmt:
		return c.compileContinueStmt(s)
	case *ast.FuncDeclStmt:
		return c.compileFuncDeclStmt(s)
	case *ast.BlockStmt:
		return c.compileBlockStmt(s)
	case *ast.GoStmt:
		return c.compileGoStmt(s)
	case *ast.SendStmt:
		return c.compileSendStmt(s)
	default:
		return fmt.Errorf("line %d: unsupported statement type %T", stmt.GetPos().Line, stmt)
	}
}

// ---- DeclareStmt ----

func (c *compiler) compileDeclareStmt(s *ast.DeclareStmt) error {
	if c.isMainTopLevel() {
		return c.compileDeclareGlobals(s)
	}

	nNames := len(s.Names)
	nValues := len(s.Values)

	if nValues == 1 && nNames > 1 {
		if call, ok := s.Values[0].(*ast.CallExpr); ok {
			return c.compileDeclareMultiCall(s, call)
		}
		if call, ok := s.Values[0].(*ast.MethodCallExpr); ok {
			return c.compileDeclareMultiMethodCall(s, call)
		}
	}

	tempBase := c.nextReg
	for i := 0; i < nNames; i++ {
		if i < nValues {
			if err := c.compileExprTo(s.Values[i], c.allocReg()); err != nil {
				return err
			}
		} else {
			r := c.allocReg()
			c.emitABC(OP_LOADNIL, r, 0, 0, s.P.Line)
		}
	}
	c.freeRegs(nNames)

	for i, name := range s.Names {
		reg := c.addLocal(name)
		if reg != tempBase+i {
			c.emitABC(OP_MOVE, reg, tempBase+i, 0, s.P.Line)
		}
	}
	return nil
}

func (c *compiler) compileDeclareGlobals(s *ast.DeclareStmt) error {
	nNames := len(s.Names)
	nValues := len(s.Values)

	if nValues == 1 && nNames > 1 {
		if call, ok := s.Values[0].(*ast.CallExpr); ok {
			tempBase := c.nextReg
			if err := c.compileCallExprMulti(call, tempBase, nNames); err != nil {
				return err
			}
			c.nextReg = tempBase + nNames
			if c.nextReg > c.maxReg {
				c.maxReg = c.nextReg
			}
			for i, name := range s.Names {
				nameK := c.stringConst(name)
				c.emitABx(OP_SETGLOBAL, tempBase+i, nameK, s.P.Line)
			}
			c.nextReg = tempBase
			return nil
		}
		if call, ok := s.Values[0].(*ast.MethodCallExpr); ok {
			tempBase := c.nextReg
			if err := c.compileMethodCallExprMulti(call, tempBase, nNames); err != nil {
				return err
			}
			c.nextReg = tempBase + nNames
			if c.nextReg > c.maxReg {
				c.maxReg = c.nextReg
			}
			for i, name := range s.Names {
				nameK := c.stringConst(name)
				c.emitABx(OP_SETGLOBAL, tempBase+i, nameK, s.P.Line)
			}
			c.nextReg = tempBase
			return nil
		}
	}

	for i := 0; i < nNames; i++ {
		reg := c.allocReg()
		if i < nValues {
			if err := c.compileExprTo(s.Values[i], reg); err != nil {
				return err
			}
		} else {
			c.emitABC(OP_LOADNIL, reg, 0, 0, s.P.Line)
		}
		nameK := c.stringConst(s.Names[i])
		c.emitABx(OP_SETGLOBAL, reg, nameK, s.P.Line)
		c.freeReg()
	}
	return nil
}

func (c *compiler) compileDeclareMultiCall(s *ast.DeclareStmt, call *ast.CallExpr) error {
	nNames := len(s.Names)
	base := c.nextReg
	if err := c.compileCallExprMulti(call, base, nNames); err != nil {
		return err
	}
	// Results are in registers base..base+nNames-1.
	// Reset nextReg so addLocal allocates those exact registers.
	c.nextReg = base
	for _, name := range s.Names {
		c.addLocal(name)
	}
	return nil
}

func (c *compiler) compileDeclareMultiMethodCall(s *ast.DeclareStmt, call *ast.MethodCallExpr) error {
	nNames := len(s.Names)
	base := c.nextReg
	if err := c.compileMethodCallExprMulti(call, base, nNames); err != nil {
		return err
	}
	// Results are in registers base..base+nNames-1.
	c.nextReg = base
	for _, name := range s.Names {
		c.addLocal(name)
	}
	return nil
}

// ---- AssignStmt ----

func (c *compiler) compileAssignStmt(s *ast.AssignStmt) error {
	nTargets := len(s.Targets)
	nValues := len(s.Values)

	if nValues == 1 && nTargets > 1 {
		if call, ok := s.Values[0].(*ast.CallExpr); ok {
			return c.compileAssignMultiCall(s, call)
		}
		if call, ok := s.Values[0].(*ast.MethodCallExpr); ok {
			return c.compileAssignMultiMethodCall(s, call)
		}
	}

	// Evaluate all values into temp registers. Keep them allocated
	// during the assignment phase so they don't get overwritten.
	tempBase := c.nextReg
	for i := 0; i < nTargets; i++ {
		if i < nValues {
			if err := c.compileExprTo(s.Values[i], c.allocReg()); err != nil {
				return err
			}
		} else {
			r := c.allocReg()
			c.emitABC(OP_LOADNIL, r, 0, 0, s.P.Line)
		}
	}
	// Do NOT free value registers yet — assignments may allocate temp
	// registers for table/field targets, which would overlap.

	for i, target := range s.Targets {
		if err := c.compileAssignTarget(target, tempBase+i, s.P.Line); err != nil {
			return err
		}
	}
	// Now free all value registers
	c.nextReg = tempBase
	return nil
}

func (c *compiler) compileAssignMultiCall(s *ast.AssignStmt, call *ast.CallExpr) error {
	nTargets := len(s.Targets)
	tempBase := c.nextReg
	if err := c.compileCallExprMulti(call, tempBase, nTargets); err != nil {
		return err
	}
	c.nextReg = tempBase + nTargets
	if c.nextReg > c.maxReg {
		c.maxReg = c.nextReg
	}
	c.freeRegs(nTargets)
	for i, target := range s.Targets {
		if err := c.compileAssignTarget(target, tempBase+i, s.P.Line); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) compileAssignMultiMethodCall(s *ast.AssignStmt, call *ast.MethodCallExpr) error {
	nTargets := len(s.Targets)
	tempBase := c.nextReg
	if err := c.compileMethodCallExprMulti(call, tempBase, nTargets); err != nil {
		return err
	}
	c.nextReg = tempBase + nTargets
	if c.nextReg > c.maxReg {
		c.maxReg = c.nextReg
	}
	c.freeRegs(nTargets)
	for i, target := range s.Targets {
		if err := c.compileAssignTarget(target, tempBase+i, s.P.Line); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) compileAssignTarget(target ast.Expr, valueReg int, line int) error {
	switch t := target.(type) {
	case *ast.IdentExpr:
		reg := c.resolveLocal(t.Name)
		if reg >= 0 {
			if reg != valueReg {
				c.emitABC(OP_MOVE, reg, valueReg, 0, line)
			}
			return nil
		}
		upIdx := c.resolveUpvalue(t.Name)
		if upIdx >= 0 {
			c.emitABC(OP_SETUPVAL, valueReg, upIdx, 0, line)
			return nil
		}
		nameK := c.stringConst(t.Name)
		c.emitABx(OP_SETGLOBAL, valueReg, nameK, line)
		return nil

	case *ast.IndexExpr:
		tableReg, tableIsTemp, err := c.compileExprReg(t.Table)
		if err != nil {
			return err
		}
		keyReg := c.allocReg()
		if err := c.compileExprTo(t.Index, keyReg); err != nil {
			return err
		}
		c.emitABC(OP_SETTABLE, tableReg, keyReg, valueReg, line)
		c.freeReg() // keyReg
		if tableIsTemp {
			c.freeReg()
		}
		return nil

	case *ast.FieldExpr:
		tableReg, tableIsTemp, err := c.compileExprReg(t.Table)
		if err != nil {
			return err
		}
		fieldK := c.stringConst(t.Field)
		c.emitABC(OP_SETFIELD, tableReg, fieldK, valueReg, line)
		if tableIsTemp {
			c.freeReg()
		}
		return nil

	default:
		return fmt.Errorf("line %d: invalid assignment target %T", line, target)
	}
}

// ---- CompoundAssignStmt ----

func (c *compiler) compileCompoundAssignStmt(s *ast.CompoundAssignStmt) error {
	line := s.P.Line

	var opcode Opcode
	switch s.Op {
	case "+=":
		opcode = OP_ADD
	case "-=":
		opcode = OP_SUB
	case "*=":
		opcode = OP_MUL
	case "/=":
		opcode = OP_DIV
	case "%=":
		opcode = OP_MOD
	case "**=":
		opcode = OP_POW
	case "..=":
		opcode = OP_CONCAT
	default:
		return fmt.Errorf("line %d: unsupported compound assignment operator %s", line, s.Op)
	}

	switch t := s.Target.(type) {
	case *ast.IdentExpr:
		reg := c.resolveLocal(t.Name)
		if reg >= 0 {
			return c.emitCompoundOp(opcode, reg, s.Value, line,
				func(resultReg int) { /* result already in reg */ })
		}
		upIdx := c.resolveUpvalue(t.Name)
		if upIdx >= 0 {
			tmp := c.allocReg()
			c.emitABC(OP_GETUPVAL, tmp, upIdx, 0, line)
			err := c.emitCompoundOp(opcode, tmp, s.Value, line, func(int) {})
			if err != nil {
				return err
			}
			c.emitABC(OP_SETUPVAL, tmp, upIdx, 0, line)
			c.freeReg()
			return nil
		}
		nameK := c.stringConst(t.Name)
		tmp := c.allocReg()
		c.emitABx(OP_GETGLOBAL, tmp, nameK, line)
		err := c.emitCompoundOp(opcode, tmp, s.Value, line, func(int) {})
		if err != nil {
			return err
		}
		c.emitABx(OP_SETGLOBAL, tmp, nameK, line)
		c.freeReg()
		return nil

	case *ast.IndexExpr:
		tableReg, tableIsTemp, err := c.compileExprReg(t.Table)
		if err != nil {
			return err
		}
		keyReg := c.allocReg()
		if err := c.compileExprTo(t.Index, keyReg); err != nil {
			return err
		}
		oldReg := c.allocReg()
		c.emitABC(OP_GETTABLE, oldReg, tableReg, keyReg, line)
		err = c.emitCompoundOp(opcode, oldReg, s.Value, line, func(int) {})
		if err != nil {
			return err
		}
		c.emitABC(OP_SETTABLE, tableReg, keyReg, oldReg, line)
		c.freeRegs(2) // oldReg, keyReg
		if tableIsTemp {
			c.freeReg()
		}
		return nil

	case *ast.FieldExpr:
		tableReg, tableIsTemp, err := c.compileExprReg(t.Table)
		if err != nil {
			return err
		}
		fieldK := c.stringConst(t.Field)
		oldReg := c.allocReg()
		c.emitABC(OP_GETFIELD, oldReg, tableReg, fieldK, line)
		err = c.emitCompoundOp(opcode, oldReg, s.Value, line, func(int) {})
		if err != nil {
			return err
		}
		c.emitABC(OP_SETFIELD, tableReg, fieldK, oldReg, line)
		c.freeReg() // oldReg
		if tableIsTemp {
			c.freeReg()
		}
		return nil

	default:
		return fmt.Errorf("line %d: invalid compound assignment target %T", line, s.Target)
	}
}

// emitCompoundOp performs: targetReg = targetReg OP value
func (c *compiler) emitCompoundOp(opcode Opcode, targetReg int, value ast.Expr, line int, _ func(int)) error {
	if opcode == OP_CONCAT {
		tmpB := c.allocReg()
		c.emitABC(OP_MOVE, tmpB, targetReg, 0, line)
		tmpC := c.allocReg()
		if err := c.compileExprTo(value, tmpC); err != nil {
			return err
		}
		c.emitABC(OP_CONCAT, targetReg, tmpB, tmpC, line)
		c.freeRegs(2)
	} else {
		valReg := c.allocReg()
		if err := c.compileExprTo(value, valReg); err != nil {
			return err
		}
		c.emitABC(opcode, targetReg, targetReg, valReg, line)
		c.freeReg()
	}
	return nil
}

// ---- IncDecStmt ----

func (c *compiler) compileIncDecStmt(s *ast.IncDecStmt) error {
	line := s.P.Line
	var opcode Opcode
	if s.Op == "++" {
		opcode = OP_ADD
	} else {
		opcode = OP_SUB
	}

	// Load constant 1 into a temp register
	oneReg := c.allocReg()
	c.emitAsBx(OP_LOADINT, oneReg, 1, line)

	switch t := s.Target.(type) {
	case *ast.IdentExpr:
		reg := c.resolveLocal(t.Name)
		if reg >= 0 {
			c.emitABC(opcode, reg, reg, oneReg, line)
			c.freeReg() // oneReg
			return nil
		}
		upIdx := c.resolveUpvalue(t.Name)
		if upIdx >= 0 {
			tmp := c.allocReg()
			c.emitABC(OP_GETUPVAL, tmp, upIdx, 0, line)
			c.emitABC(opcode, tmp, tmp, oneReg, line)
			c.emitABC(OP_SETUPVAL, tmp, upIdx, 0, line)
			c.freeReg() // tmp
			c.freeReg() // oneReg
			return nil
		}
		nameK := c.stringConst(t.Name)
		tmp := c.allocReg()
		c.emitABx(OP_GETGLOBAL, tmp, nameK, line)
		c.emitABC(opcode, tmp, tmp, oneReg, line)
		c.emitABx(OP_SETGLOBAL, tmp, nameK, line)
		c.freeReg() // tmp
		c.freeReg() // oneReg
		return nil

	case *ast.IndexExpr:
		tableReg := c.allocReg()
		if err := c.compileExprTo(t.Table, tableReg); err != nil {
			return err
		}
		keyReg := c.allocReg()
		if err := c.compileExprTo(t.Index, keyReg); err != nil {
			return err
		}
		oldReg := c.allocReg()
		c.emitABC(OP_GETTABLE, oldReg, tableReg, keyReg, line)
		c.emitABC(opcode, oldReg, oldReg, oneReg, line)
		c.emitABC(OP_SETTABLE, tableReg, keyReg, oldReg, line)
		c.freeRegs(3) // oldReg, keyReg, tableReg
		c.freeReg()   // oneReg
		return nil

	case *ast.FieldExpr:
		tableReg := c.allocReg()
		if err := c.compileExprTo(t.Table, tableReg); err != nil {
			return err
		}
		fieldK := c.stringConst(t.Field)
		oldReg := c.allocReg()
		c.emitABC(OP_GETFIELD, oldReg, tableReg, fieldK, line)
		c.emitABC(opcode, oldReg, oldReg, oneReg, line)
		c.emitABC(OP_SETFIELD, tableReg, fieldK, oldReg, line)
		c.freeRegs(2) // oldReg, tableReg
		c.freeReg()   // oneReg
		return nil

	default:
		c.freeReg() // oneReg
		return fmt.Errorf("line %d: invalid inc/dec target %T", line, s.Target)
	}
}

// ---- CallStmt ----

func (c *compiler) compileCallStmt(s *ast.CallStmt) error {
	return c.compileCallExprDiscard(s.Call, s.P.Line)
}

func (c *compiler) compileCallExprDiscard(call *ast.CallExpr, line int) error {
	if op, ok := c.staticCoroutineBuiltinOp(call); ok {
		return c.compileCoroutineBuiltinCall(call, c.nextReg, -1, line, op)
	}
	base := c.nextReg
	funcReg := c.allocReg()
	if err := c.compileExprTo(call.Func, funcReg); err != nil {
		return err
	}
	nArgs := len(call.Args)
	lastArgIsMulti := false
	for i, arg := range call.Args {
		argReg := c.allocReg()
		if i == nArgs-1 {
			switch a := arg.(type) {
			case *ast.CallExpr:
				lastArgIsMulti = true
				if err := c.compileCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.MethodCallExpr:
				lastArgIsMulti = true
				if err := c.compileMethodCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.VarArgExpr:
				lastArgIsMulti = true
				c.emitABC(OP_VARARG, argReg, 0, 0, line)
				continue
			}
		}
		if err := c.compileExprTo(arg, argReg); err != nil {
			return err
		}
	}
	b := nArgs + 1
	if lastArgIsMulti {
		b = 0
	}
	c.emitABC(OP_CALL, funcReg, b, 1, line) // C=1: discard results
	c.nextReg = base
	return nil
}

// ---- GoStmt ----

func (c *compiler) compileGoStmt(s *ast.GoStmt) error {
	line := s.P.Line
	switch call := s.Call.(type) {
	case *ast.CallExpr:
		return c.compileGoCallExpr(call, line)
	case *ast.MethodCallExpr:
		return c.compileGoMethodCallExpr(call, line)
	default:
		return fmt.Errorf("line %d: go statement requires a function call", line)
	}
}

func (c *compiler) compileGoCallExpr(call *ast.CallExpr, line int) error {
	base := c.nextReg
	funcReg := c.allocReg()
	if err := c.compileExprTo(call.Func, funcReg); err != nil {
		return err
	}
	nArgs := len(call.Args)
	lastArgIsMulti := false
	for i, arg := range call.Args {
		argReg := c.allocReg()
		if i == nArgs-1 {
			switch a := arg.(type) {
			case *ast.CallExpr:
				lastArgIsMulti = true
				if err := c.compileCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.MethodCallExpr:
				lastArgIsMulti = true
				if err := c.compileMethodCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.VarArgExpr:
				lastArgIsMulti = true
				c.emitABC(OP_VARARG, argReg, 0, 0, line)
				continue
			}
		}
		if err := c.compileExprTo(arg, argReg); err != nil {
			return err
		}
	}
	b := nArgs + 1
	if lastArgIsMulti {
		b = 0
	}
	c.emitABC(OP_GO, funcReg, b, 0, line)
	c.nextReg = base
	return nil
}

func (c *compiler) compileGoMethodCallExpr(call *ast.MethodCallExpr, line int) error {
	base := c.nextReg
	selfReg := c.allocReg()
	c.allocReg() // reserve selfReg+1 for receiver

	objReg := c.allocReg()
	if err := c.compileExprTo(call.Object, objReg); err != nil {
		return err
	}
	c.emitMethodLookup(selfReg, objReg, c.stringConst(call.Method), line)

	nArgs := len(call.Args)
	for _, arg := range call.Args {
		argReg := c.allocReg()
		if err := c.compileExprTo(arg, argReg); err != nil {
			return err
		}
	}
	b := nArgs + 2 // +1 for self, +1 for encoding
	c.emitABC(OP_GO, selfReg, b, 0, line)
	c.nextReg = base
	return nil
}

// ---- Channel operations ----

func (c *compiler) compileSendStmt(s *ast.SendStmt) error {
	line := s.P.Line
	base := c.nextReg

	// Special case: standalone <-ch (recv as statement, discard result)
	if recvExpr, ok := s.Channel.(*ast.RecvExpr); ok && s.Value == nil {
		chReg := c.allocReg()
		if err := c.compileExprTo(recvExpr.Channel, chReg); err != nil {
			return err
		}
		// Recv into a temp register (discarded)
		c.emitABC(OP_RECV, chReg, chReg, 0, line)
		c.nextReg = base
		return nil
	}

	chReg := c.allocReg()
	if err := c.compileExprTo(s.Channel, chReg); err != nil {
		return err
	}
	valReg := c.allocReg()
	if err := c.compileExprTo(s.Value, valReg); err != nil {
		return err
	}
	c.emitABC(OP_SEND, chReg, valReg, 0, line)
	c.nextReg = base
	return nil
}

func (c *compiler) compileRecvExpr(e *ast.RecvExpr, dest int) error {
	line := e.P.Line
	base := c.nextReg
	chReg := c.allocReg()
	if err := c.compileExprTo(e.Channel, chReg); err != nil {
		return err
	}
	c.emitABC(OP_RECV, dest, chReg, 0, line)
	c.nextReg = base
	return nil
}

func (c *compiler) compileMakeChanExpr(e *ast.MakeChanExpr, dest int) error {
	line := e.P.Line
	if e.Size != nil {
		base := c.nextReg
		sizeReg := c.allocReg()
		if err := c.compileExprTo(e.Size, sizeReg); err != nil {
			return err
		}
		c.emitABC(OP_MAKECHAN, dest, sizeReg, 1, line) // C=1 means size is in R(B)
		c.nextReg = base
	} else {
		c.emitABC(OP_MAKECHAN, dest, 0, 0, line) // C=0 means unbuffered
	}
	return nil
}

// ---- IfStmt ----

func (c *compiler) compileIfStmt(s *ast.IfStmt) error {
	line := s.P.Line
	var endJumps []int

	elseJump, err := c.compileCondJump(s.Cond, line)
	if err != nil {
		return err
	}

	c.enterScope()
	for _, st := range s.Body.Stmts {
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	c.leaveScope()

	if len(s.ElseIfs) > 0 || s.ElseBody != nil {
		endJumps = append(endJumps, c.emitJump(line))
	}
	c.patchJump(elseJump)

	for _, elif := range s.ElseIfs {
		nextJump, err := c.compileCondJump(elif.Cond, elif.P.Line)
		if err != nil {
			return err
		}

		c.enterScope()
		for _, st := range elif.Body.Stmts {
			if err := c.compileStmt(st); err != nil {
				return err
			}
		}
		c.leaveScope()

		endJumps = append(endJumps, c.emitJump(elif.P.Line))
		c.patchJump(nextJump)
	}

	if s.ElseBody != nil {
		c.enterScope()
		for _, st := range s.ElseBody.Stmts {
			if err := c.compileStmt(st); err != nil {
				return err
			}
		}
		c.leaveScope()
	}

	for _, j := range endJumps {
		c.patchJump(j)
	}
	return nil
}

// ---- ForNumStmt ----

func (c *compiler) compileForNumStmt(s *ast.ForNumStmt) error {
	line := s.P.Line

	if forInfo, ok := c.detectSimpleNumericFor(s); ok {
		return c.compileOptimizedForNum(s, forInfo, line)
	}

	// Generic C-style for loop
	c.enterScope()

	if s.Init != nil {
		if err := c.compileStmt(s.Init); err != nil {
			return err
		}
	}

	c.pushLoop()
	loopTop := c.currentPC()

	if s.Cond != nil {
		breakJump, err := c.compileCondJump(s.Cond, line)
		if err != nil {
			return err
		}
		c.currentLoop().breakJumps = append(c.currentLoop().breakJumps, breakJump)
	}

	c.enterScope()
	for _, st := range s.Body.Stmts {
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	c.leaveScope()

	continueTarget := c.currentPC()

	if s.Post != nil {
		if err := c.compileStmt(s.Post); err != nil {
			return err
		}
	}

	loopBack := c.emitJump(line)
	c.patchJumpTo(loopBack, loopTop)

	info := c.popLoop()
	c.patchBreaks(info)
	c.patchContinues(info, continueTarget)

	c.leaveScope()
	return nil
}

type simpleForInfo struct {
	varName string
}

func (c *compiler) detectSimpleNumericFor(s *ast.ForNumStmt) (simpleForInfo, bool) {
	decl, ok := s.Init.(*ast.DeclareStmt)
	if !ok || len(decl.Names) != 1 || len(decl.Values) != 1 {
		return simpleForInfo{}, false
	}
	varName := decl.Names[0]

	bin, ok := s.Cond.(*ast.BinaryExpr)
	if !ok {
		return simpleForInfo{}, false
	}
	ident, ok := bin.Left.(*ast.IdentExpr)
	if !ok || ident.Name != varName {
		return simpleForInfo{}, false
	}
	if bin.Op != "<" && bin.Op != "<=" {
		return simpleForInfo{}, false
	}

	switch post := s.Post.(type) {
	case *ast.IncDecStmt:
		if post.Op != "++" {
			return simpleForInfo{}, false
		}
		ident2, ok := post.Target.(*ast.IdentExpr)
		if !ok || ident2.Name != varName {
			return simpleForInfo{}, false
		}
	case *ast.CompoundAssignStmt:
		if post.Op != "+=" {
			return simpleForInfo{}, false
		}
		ident2, ok := post.Target.(*ast.IdentExpr)
		if !ok || ident2.Name != varName {
			return simpleForInfo{}, false
		}
	case *ast.AssignStmt:
		// Handle `i = i + step` pattern as equivalent to `i += step`
		if len(post.Targets) != 1 || len(post.Values) != 1 {
			return simpleForInfo{}, false
		}
		ident2, ok := post.Targets[0].(*ast.IdentExpr)
		if !ok || ident2.Name != varName {
			return simpleForInfo{}, false
		}
		binExpr, ok := post.Values[0].(*ast.BinaryExpr)
		if !ok || binExpr.Op != "+" {
			return simpleForInfo{}, false
		}
		leftIdent, ok := binExpr.Left.(*ast.IdentExpr)
		if !ok || leftIdent.Name != varName {
			return simpleForInfo{}, false
		}
	default:
		return simpleForInfo{}, false
	}

	return simpleForInfo{varName: varName}, true
}

func (c *compiler) compileOptimizedForNum(s *ast.ForNumStmt, info simpleForInfo, line int) error {
	c.enterScope()

	// Reserve 4 consecutive registers: R(A)=index, R(A+1)=limit, R(A+2)=step, R(A+3)=loop var
	baseReg := c.allocRegs(4)

	decl := s.Init.(*ast.DeclareStmt)
	bin := s.Cond.(*ast.BinaryExpr)

	// Compile init value into R(A)
	if err := c.compileExprTo(decl.Values[0], baseReg); err != nil {
		return err
	}

	// Compile limit into R(A+1)
	if bin.Op == "<" {
		// Subtract 1 from the limit to turn `<` into `<=`
		limitTmp := c.allocReg()
		if err := c.compileExprTo(bin.Right, limitTmp); err != nil {
			return err
		}
		oneReg := c.allocReg()
		c.emitAsBx(OP_LOADINT, oneReg, 1, line)
		c.emitABC(OP_SUB, baseReg+1, limitTmp, oneReg, line)
		c.freeRegs(2) // oneReg, limitTmp
	} else {
		if err := c.compileExprTo(bin.Right, baseReg+1); err != nil {
			return err
		}
	}

	// Compile step into R(A+2)
	switch post := s.Post.(type) {
	case *ast.IncDecStmt:
		c.emitAsBx(OP_LOADINT, baseReg+2, 1, line)
	case *ast.CompoundAssignStmt:
		if err := c.compileExprTo(post.Value, baseReg+2); err != nil {
			return err
		}
	case *ast.AssignStmt:
		// i = i + step → extract step from BinaryExpr.Right
		binExpr := post.Values[0].(*ast.BinaryExpr)
		if err := c.compileExprTo(binExpr.Right, baseReg+2); err != nil {
			return err
		}
	}

	// Declare the loop variable as a local mapping to R(A+3)
	c.locals = append(c.locals, localVar{name: info.varName, reg: baseReg + 3, depth: c.depth})

	c.pushLoop()

	// FORPREP A sBx: R(A) -= R(A+2); PC += sBx
	forprepPos := c.emitAsBx(OP_FORPREP, baseReg, 0, line)

	loopBodyStart := c.currentPC()
	c.enterScope()
	for _, st := range s.Body.Stmts {
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	c.leaveScope()

	continueTarget := c.currentPC()

	forloopOffset := loopBodyStart - c.currentPC() - 1
	c.emitAsBx(OP_FORLOOP, baseReg, forloopOffset, line)

	forprepOffset := continueTarget - forprepPos - 1
	c.proto.Code[forprepPos] = EncodeAsBx(OP_FORPREP, baseReg, forprepOffset)

	info2 := c.popLoop()
	c.patchBreaks(info2)
	c.patchContinues(info2, continueTarget)

	c.leaveScope()
	return nil
}

// ---- ForStmt ----

func (c *compiler) compileForStmt(s *ast.ForStmt) error {
	line := s.P.Line
	c.pushLoop()
	loopTop := c.currentPC()

	if s.Cond != nil {
		breakJump, err := c.compileCondJump(s.Cond, line)
		if err != nil {
			return err
		}
		c.currentLoop().breakJumps = append(c.currentLoop().breakJumps, breakJump)
	}

	c.enterScope()
	for _, st := range s.Body.Stmts {
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	c.leaveScope()

	continueTarget := c.currentPC()
	loopBack := c.emitJump(line)
	c.patchJumpTo(loopBack, loopTop)

	info := c.popLoop()
	c.patchBreaks(info)
	c.patchContinues(info, continueTarget)
	return nil
}

// ---- ForRangeStmt ----

func (c *compiler) compileForRangeStmt(s *ast.ForRangeStmt) error {
	line := s.P.Line
	c.enterScope()

	iterBase := c.allocRegs(3)

	switch iter := s.Iter.(type) {
	case *ast.CallExpr:
		if err := c.compileCallExprMulti(iter, iterBase, 3); err != nil {
			return err
		}
	case *ast.MethodCallExpr:
		if err := c.compileMethodCallExprMulti(iter, iterBase, 3); err != nil {
			return err
		}
	default:
		if err := c.compileExprTo(s.Iter, iterBase); err != nil {
			return err
		}
		c.emitABC(OP_LOADNIL, iterBase+1, 1, 0, line)
	}

	nVars := 1
	if s.Value != "" {
		nVars = 2
	}

	c.locals = append(c.locals, localVar{name: s.Key, reg: iterBase + 3, depth: c.depth})
	for c.nextReg < iterBase+3+nVars {
		c.allocReg()
	}
	if s.Value != "" {
		c.locals = append(c.locals, localVar{name: s.Value, reg: iterBase + 4, depth: c.depth})
	}

	c.pushLoop()

	loopTop := c.currentPC()
	c.emitABC(OP_TFORCALL, iterBase, 0, nVars, line)
	tforloopPos := c.emitAsBx(OP_TFORLOOP, iterBase+2, 0, line)
	exitJump := c.emitJump(line)
	bodyStart := c.currentPC()

	c.enterScope()
	for _, st := range s.Body.Stmts {
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	c.leaveScope()

	continueTarget := c.currentPC()
	loopBack := c.emitJump(line)
	c.patchJumpTo(loopBack, loopTop)

	tforloopOffset := bodyStart - tforloopPos - 1
	c.proto.Code[tforloopPos] = EncodeAsBx(OP_TFORLOOP, iterBase+2, tforloopOffset)

	c.patchJump(exitJump)

	info := c.popLoop()
	c.patchBreaks(info)
	c.patchContinues(info, continueTarget)

	c.leaveScope()
	return nil
}

// ---- ReturnStmt ----

func (c *compiler) compileReturnStmt(s *ast.ReturnStmt) error {
	line := s.P.Line
	nValues := len(s.Values)

	if nValues == 0 {
		c.emitReturn(0, 1)
		return nil
	}

	lastIsMulti := false
	switch s.Values[nValues-1].(type) {
	case *ast.CallExpr, *ast.MethodCallExpr, *ast.VarArgExpr:
		lastIsMulti = true
	}

	base := c.nextReg
	for i := 0; i < nValues; i++ {
		reg := c.allocReg()
		if i == nValues-1 && lastIsMulti {
			switch v := s.Values[i].(type) {
			case *ast.CallExpr:
				if err := c.compileCallExprMulti(v, reg, -1); err != nil {
					return err
				}
			case *ast.MethodCallExpr:
				if err := c.compileMethodCallExprMulti(v, reg, -1); err != nil {
					return err
				}
			case *ast.VarArgExpr:
				c.emitABC(OP_VARARG, reg, 0, 0, line)
			}
		} else {
			if err := c.compileExprTo(s.Values[i], reg); err != nil {
				return err
			}
		}
	}

	if lastIsMulti {
		c.emitReturn(base, 0)
	} else {
		c.emitReturn(base, nValues+1)
	}
	c.nextReg = base
	return nil
}

// ---- BreakStmt / ContinueStmt ----

func (c *compiler) compileBreakStmt(s *ast.BreakStmt) error {
	loop := c.currentLoop()
	if loop == nil {
		return fmt.Errorf("line %d: break outside loop", s.P.Line)
	}
	c.emitCloseForJump()
	jmp := c.emitJump(s.P.Line)
	loop.breakJumps = append(loop.breakJumps, jmp)
	return nil
}

func (c *compiler) compileContinueStmt(s *ast.ContinueStmt) error {
	loop := c.currentLoop()
	if loop == nil {
		return fmt.Errorf("line %d: continue outside loop", s.P.Line)
	}
	c.emitCloseForJump()
	jmp := c.emitJump(s.P.Line)
	loop.continueJumps = append(loop.continueJumps, jmp)
	return nil
}

// ---- FuncDeclStmt ----

func (c *compiler) compileFuncDeclStmt(s *ast.FuncDeclStmt) error {
	line := s.P.Line
	protoIdx, err := c.compileFunction(s.Name, s.Params, s.Body, line)
	if err != nil {
		return err
	}

	reg := c.resolveLocal(s.Name)
	if reg >= 0 {
		c.emitABx(OP_CLOSURE, reg, protoIdx, line)
		return nil
	}
	upIdx := c.resolveUpvalue(s.Name)
	if upIdx >= 0 {
		tmpReg := c.allocReg()
		c.emitABx(OP_CLOSURE, tmpReg, protoIdx, line)
		c.emitABC(OP_SETUPVAL, tmpReg, upIdx, 0, line)
		c.freeReg()
		return nil
	}
	// Global
	tmpReg := c.allocReg()
	c.emitABx(OP_CLOSURE, tmpReg, protoIdx, line)
	nameK := c.stringConst(s.Name)
	c.emitABx(OP_SETGLOBAL, tmpReg, nameK, line)
	c.freeReg()
	return nil
}

// ---- BlockStmt ----

func (c *compiler) compileBlockStmt(s *ast.BlockStmt) error {
	c.enterScope()
	for _, st := range s.Stmts {
		if err := c.compileStmt(st); err != nil {
			return err
		}
	}
	c.leaveScope()
	return nil
}

// --------------------------------------------------------------------
// Expression compilation
// --------------------------------------------------------------------

func (c *compiler) compileExprTo(expr ast.Expr, dest int) error {
	switch e := expr.(type) {
	case *ast.NumberLit:
		return c.compileNumberLit(e, dest)
	case *ast.StringLit:
		k := c.stringConst(e.Value)
		c.emitABx(OP_LOADK, dest, k, e.P.Line)
		return nil
	case *ast.BoolLit:
		b := 0
		if e.Value {
			b = 1
		}
		c.emitABC(OP_LOADBOOL, dest, b, 0, e.P.Line)
		return nil
	case *ast.NilLit:
		c.emitABC(OP_LOADNIL, dest, 0, 0, e.P.Line)
		return nil
	case *ast.IdentExpr:
		return c.compileIdentExpr(e, dest)
	case *ast.BinaryExpr:
		return c.compileBinaryExpr(e, dest)
	case *ast.UnaryExpr:
		return c.compileUnaryExpr(e, dest)
	case *ast.CallExpr:
		return c.compileCallExprMulti(e, dest, 1)
	case *ast.MethodCallExpr:
		return c.compileMethodCallExprMulti(e, dest, 1)
	case *ast.IndexExpr:
		return c.compileIndexExpr(e, dest)
	case *ast.FieldExpr:
		return c.compileFieldExpr(e, dest)
	case *ast.TableLitExpr:
		return c.compileTableLitExpr(e, dest)
	case *ast.FuncLitExpr:
		return c.compileFuncLitExpr(e, dest)
	case *ast.VarArgExpr:
		c.emitABC(OP_VARARG, dest, 2, 0, e.P.Line)
		return nil
	case *ast.RecvExpr:
		return c.compileRecvExpr(e, dest)
	case *ast.MakeChanExpr:
		return c.compileMakeChanExpr(e, dest)
	default:
		return fmt.Errorf("line %d: unsupported expression type %T", expr.GetPos().Line, expr)
	}
}

func (c *compiler) compileNumberLit(e *ast.NumberLit, dest int) error {
	line := e.P.Line
	if i, err := strconv.ParseInt(e.Value, 0, 64); err == nil {
		if i >= -32767 && i <= 32767 {
			c.emitAsBx(OP_LOADINT, dest, int(i), line)
		} else {
			k := c.intConst(i)
			c.emitABx(OP_LOADK, dest, k, line)
		}
		return nil
	}
	if f, err := strconv.ParseFloat(e.Value, 64); err == nil {
		k := c.floatConst(f)
		c.emitABx(OP_LOADK, dest, k, line)
		return nil
	}
	return fmt.Errorf("line %d: invalid number literal %q", line, e.Value)
}

func (c *compiler) compileIdentExpr(e *ast.IdentExpr, dest int) error {
	line := e.P.Line
	reg := c.resolveLocal(e.Name)
	if reg >= 0 {
		if reg != dest {
			c.emitABC(OP_MOVE, dest, reg, 0, line)
		}
		return nil
	}
	upIdx := c.resolveUpvalue(e.Name)
	if upIdx >= 0 {
		c.emitABC(OP_GETUPVAL, dest, upIdx, 0, line)
		return nil
	}
	nameK := c.stringConst(e.Name)
	c.emitABx(OP_GETGLOBAL, dest, nameK, line)
	return nil
}

func (c *compiler) compileBinaryExpr(e *ast.BinaryExpr, dest int) error {
	line := e.P.Line

	if e.Op == "&&" {
		return c.compileAnd(e, dest)
	}
	if e.Op == "||" {
		return c.compileOr(e, dest)
	}

	switch e.Op {
	case "==":
		return c.compileComparison(e, dest, OP_EQ, 0, false)
	case "!=":
		return c.compileComparison(e, dest, OP_EQ, 1, false)
	case "<":
		return c.compileComparison(e, dest, OP_LT, 0, false)
	case "<=":
		return c.compileComparison(e, dest, OP_LE, 0, false)
	case ">":
		return c.compileComparison(e, dest, OP_LT, 0, true)
	case ">=":
		return c.compileComparison(e, dest, OP_LE, 0, true)
	}

	if e.Op == ".." {
		return c.compileConcat(e, dest)
	}

	var opcode Opcode
	switch e.Op {
	case "+":
		opcode = OP_ADD
	case "-":
		opcode = OP_SUB
	case "*":
		opcode = OP_MUL
	case "/":
		opcode = OP_DIV
	case "%":
		opcode = OP_MOD
	case "**":
		opcode = OP_POW
	default:
		return fmt.Errorf("line %d: unsupported binary operator %q", line, e.Op)
	}

	leftReg, leftIsTemp, err := c.compileExprReg(e.Left)
	if err != nil {
		return err
	}
	rightReg, rightIsTemp, err := c.compileExprReg(e.Right)
	if err != nil {
		return err
	}

	c.emitABC(opcode, dest, leftReg, rightReg, line)

	if rightIsTemp {
		c.freeReg()
	}
	if leftIsTemp {
		c.freeReg()
	}
	return nil
}

func (c *compiler) compileAnd(e *ast.BinaryExpr, dest int) error {
	line := e.P.Line
	if err := c.compileExprTo(e.Left, dest); err != nil {
		return err
	}
	c.emitABC(OP_TESTSET, dest, dest, 0, line)
	skipJump := c.emitJump(line)
	if err := c.compileExprTo(e.Right, dest); err != nil {
		return err
	}
	c.patchJump(skipJump)
	return nil
}

func (c *compiler) compileOr(e *ast.BinaryExpr, dest int) error {
	line := e.P.Line
	if err := c.compileExprTo(e.Left, dest); err != nil {
		return err
	}
	c.emitABC(OP_TESTSET, dest, dest, 1, line)
	skipJump := c.emitJump(line)
	if err := c.compileExprTo(e.Right, dest); err != nil {
		return err
	}
	c.patchJump(skipJump)
	return nil
}

// compileCondJump compiles an expression as a branch condition.
// It emits instructions such that execution falls through when the condition is truthy,
// and takes the returned jump when the condition is falsy.
// For comparison expressions, this avoids materializing a boolean value.
func (c *compiler) compileCondJump(expr ast.Expr, line int) (int, error) {
	if binExpr, ok := expr.(*ast.BinaryExpr); ok {
		switch binExpr.Op {
		case "<":
			return c.compileCondCmp(binExpr, OP_LT, 0, false)
		case "<=":
			return c.compileCondCmp(binExpr, OP_LE, 0, false)
		case ">":
			return c.compileCondCmp(binExpr, OP_LT, 0, true)
		case ">=":
			return c.compileCondCmp(binExpr, OP_LE, 0, true)
		case "==":
			return c.compileCondCmp(binExpr, OP_EQ, 0, false)
		case "!=":
			return c.compileCondCmp(binExpr, OP_EQ, 1, false)
		}
	}
	if unExpr, ok := expr.(*ast.UnaryExpr); ok && unExpr.Op == "!" {
		return c.compileCondJumpInv(unExpr.Operand, line)
	}
	// Fallback: compile to register, then TEST
	condReg := c.allocReg()
	if err := c.compileExprTo(expr, condReg); err != nil {
		return 0, err
	}
	c.freeReg()
	c.emitABC(OP_TEST, condReg, 0, 0, line)
	return c.emitJump(line), nil
}

// compileCondJumpInv is like compileCondJump but with inverted sense:
// falls through when the condition is falsy, jumps when truthy.
func (c *compiler) compileCondJumpInv(expr ast.Expr, line int) (int, error) {
	if binExpr, ok := expr.(*ast.BinaryExpr); ok {
		switch binExpr.Op {
		case "<":
			return c.compileCondCmp(binExpr, OP_LT, 1, false)
		case "<=":
			return c.compileCondCmp(binExpr, OP_LE, 1, false)
		case ">":
			return c.compileCondCmp(binExpr, OP_LT, 1, true)
		case ">=":
			return c.compileCondCmp(binExpr, OP_LE, 1, true)
		case "==":
			return c.compileCondCmp(binExpr, OP_EQ, 1, false)
		case "!=":
			return c.compileCondCmp(binExpr, OP_EQ, 0, false)
		}
	}
	if unExpr, ok := expr.(*ast.UnaryExpr); ok && unExpr.Op == "!" {
		return c.compileCondJump(unExpr.Operand, line)
	}
	condReg := c.allocReg()
	if err := c.compileExprTo(expr, condReg); err != nil {
		return 0, err
	}
	c.freeReg()
	c.emitABC(OP_TEST, condReg, 0, 1, line) // C=1: skip if NOT truthy
	return c.emitJump(line), nil
}

// compileCondCmp emits a comparison opcode + JMP for use as a branch condition.
// Falls through when the comparison matches, jumps when it doesn't.
func (c *compiler) compileCondCmp(e *ast.BinaryExpr, op Opcode, a int, swap bool) (int, error) {
	line := e.P.Line
	var leftExpr, rightExpr ast.Expr
	if swap {
		leftExpr = e.Right
		rightExpr = e.Left
	} else {
		leftExpr = e.Left
		rightExpr = e.Right
	}
	leftReg, leftIsTemp, err := c.compileExprReg(leftExpr)
	if err != nil {
		return 0, err
	}
	rightReg, rightIsTemp, err := c.compileExprReg(rightExpr)
	if err != nil {
		return 0, err
	}
	c.emitABC(op, a, leftReg, rightReg, line)
	jmp := c.emitJump(line)
	if rightIsTemp {
		c.freeReg()
	}
	if leftIsTemp {
		c.freeReg()
	}
	return jmp, nil
}

// smallIntLit returns (value, true) if expr is an integer literal in [0, 255].
// Currently unused but retained for future immediate-operand opcodes.
func smallIntLit(expr ast.Expr) (int, bool) {
	num, ok := expr.(*ast.NumberLit)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(num.Value, 0, 64)
	if err != nil || i < 0 || i > 255 {
		return 0, false
	}
	return int(i), true
}

func (c *compiler) compileComparison(e *ast.BinaryExpr, dest int, op Opcode, a int, swap bool) error {
	line := e.P.Line
	var leftExpr, rightExpr ast.Expr
	if swap {
		leftExpr = e.Right
		rightExpr = e.Left
	} else {
		leftExpr = e.Left
		rightExpr = e.Right
	}

	leftReg, leftIsTemp, err := c.compileExprReg(leftExpr)
	if err != nil {
		return err
	}
	rightReg, rightIsTemp, err := c.compileExprReg(rightExpr)
	if err != nil {
		return err
	}

	// OP_CMP A B C: if (R(B) op R(C)) != bool(A) then PC++
	// Pattern:
	//   [0] CMP A B C          ; if condition matches A, skip next
	//   [1] JMP to [3]         ; jump to false LOADBOOL
	//   [2] LOADBOOL dest 1 1  ; true, skip next
	//   [3] LOADBOOL dest 0 0  ; false
	c.emitABC(op, a, leftReg, rightReg, line)
	jmpToFalse := c.emitJump(line)
	c.emitABC(OP_LOADBOOL, dest, 1, 1, line) // true, skip next
	falsePos := c.currentPC()
	c.emitABC(OP_LOADBOOL, dest, 0, 0, line) // false
	c.patchJumpTo(jmpToFalse, falsePos)

	if rightIsTemp {
		c.freeReg()
	}
	if leftIsTemp {
		c.freeReg()
	}
	return nil
}

func (c *compiler) compileConcat(e *ast.BinaryExpr, dest int) error {
	line := e.P.Line
	parts := c.flattenConcat(e)
	base := c.nextReg
	for _, part := range parts {
		reg := c.allocReg()
		if err := c.compileExprTo(part, reg); err != nil {
			return err
		}
	}
	c.emitABC(OP_CONCAT, dest, base, base+len(parts)-1, line)
	c.freeRegs(len(parts))
	return nil
}

func (c *compiler) flattenConcat(expr ast.Expr) []ast.Expr {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op != ".." {
		return []ast.Expr{expr}
	}
	return append(c.flattenConcat(bin.Left), c.flattenConcat(bin.Right)...)
}

func (c *compiler) compileUnaryExpr(e *ast.UnaryExpr, dest int) error {
	line := e.P.Line
	var op Opcode
	switch e.Op {
	case "-":
		op = OP_UNM
	case "!":
		op = OP_NOT
	case "#":
		op = OP_LEN
	default:
		return fmt.Errorf("line %d: unsupported unary operator %q", line, e.Op)
	}
	operandReg := c.allocReg()
	if err := c.compileExprTo(e.Operand, operandReg); err != nil {
		return err
	}
	c.emitABC(op, dest, operandReg, 0, line)
	c.freeReg()
	return nil
}

// ---- Call expressions ----

func (c *compiler) compileCallExprMulti(call *ast.CallExpr, dest int, nResults int) error {
	line := call.P.Line
	if op, ok := c.staticCoroutineBuiltinOp(call); ok {
		return c.compileCoroutineBuiltinCall(call, dest, nResults, line, op)
	}
	savedReg := c.nextReg

	c.nextReg = dest
	funcReg := c.allocReg()
	if err := c.compileExprTo(call.Func, funcReg); err != nil {
		return err
	}

	// R134+R136: nested Call/MethodCall args compile to a fresh scratch
	// reg then MOVE to argReg — decouples inner-call result slot from
	// outer-call arg slot so Tier 2's slotMap doesn't conflate them
	// (fixes ack hang at np>=2). VarArg keeps Lua multi-return (no JIT).
	nArgs := len(call.Args)
	lastArgIsMulti := false
	for i, arg := range call.Args {
		argReg := c.allocReg()
		if _, ok := arg.(*ast.VarArgExpr); ok && i == nArgs-1 {
			lastArgIsMulti = true
			c.emitABC(OP_VARARG, argReg, 0, 0, line)
			continue
		}
		switch arg.(type) {
		case *ast.CallExpr, *ast.MethodCallExpr:
			saved := c.nextReg
			scratch := c.allocReg()
			if err := c.compileExprTo(arg, scratch); err != nil {
				return err
			}
			c.emitABC(OP_MOVE, argReg, scratch, 0, line)
			c.nextReg = saved
		default:
			if err := c.compileExprTo(arg, argReg); err != nil {
				return err
			}
		}
	}

	b := nArgs + 1
	if lastArgIsMulti {
		b = 0
	}
	cc := nResults + 1
	if nResults == -1 {
		cc = 0
	}

	c.emitABC(OP_CALL, funcReg, b, cc, line)

	c.nextReg = savedReg
	if nResults > 0 {
		needed := dest + nResults
		if needed > c.nextReg {
			c.nextReg = needed
			if c.nextReg > c.maxReg {
				c.maxReg = c.nextReg
			}
		}
	}
	return nil
}

func (c *compiler) staticCoroutineBuiltinOp(call *ast.CallExpr) (Opcode, bool) {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return 0, false
	}
	ident, ok := field.Table.(*ast.IdentExpr)
	if !ok || ident.Name != "coroutine" {
		return 0, false
	}
	switch field.Field {
	case "yield":
		return OP_YIELD, true
	case "resume":
		return OP_RESUME, true
	default:
		return 0, false
	}
}

func (c *compiler) compileCoroutineBuiltinCall(call *ast.CallExpr, dest int, nResults int, line int, op Opcode) error {
	savedReg := c.nextReg
	c.nextReg = dest
	funcReg := c.allocReg()

	nArgs := len(call.Args)
	lastArgIsMulti := false
	for i, arg := range call.Args {
		argReg := c.allocReg()
		if i == nArgs-1 {
			switch a := arg.(type) {
			case *ast.CallExpr:
				lastArgIsMulti = true
				if err := c.compileCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.MethodCallExpr:
				lastArgIsMulti = true
				if err := c.compileMethodCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.VarArgExpr:
				lastArgIsMulti = true
				c.emitABC(OP_VARARG, argReg, 0, 0, line)
				continue
			}
		}
		if err := c.compileExprTo(arg, argReg); err != nil {
			return err
		}
	}

	b := nArgs + 1
	if lastArgIsMulti {
		b = 0
	}
	cc := nResults + 1
	if nResults == -1 {
		cc = 0
	}
	c.emitABC(op, funcReg, b, cc, line)
	c.nextReg = savedReg
	return nil
}

func (c *compiler) compileMethodCallExprMulti(call *ast.MethodCallExpr, dest int, nResults int) error {
	line := call.P.Line
	savedReg := c.nextReg

	c.nextReg = dest
	selfReg := c.allocReg() // dest = funcReg
	_ = c.allocReg()        // dest+1 = self (filled by OP_SELF)

	objReg := c.allocReg()
	if err := c.compileExprTo(call.Object, objReg); err != nil {
		return err
	}

	c.emitMethodLookup(selfReg, objReg, c.stringConst(call.Method), line)
	c.freeReg() // objReg

	nArgs := len(call.Args)
	lastArgIsMulti := false
	for i, arg := range call.Args {
		argReg := c.allocReg()
		if i == nArgs-1 {
			switch a := arg.(type) {
			case *ast.CallExpr:
				lastArgIsMulti = true
				if err := c.compileCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.MethodCallExpr:
				lastArgIsMulti = true
				if err := c.compileMethodCallExprMulti(a, argReg, -1); err != nil {
					return err
				}
				continue
			case *ast.VarArgExpr:
				lastArgIsMulti = true
				c.emitABC(OP_VARARG, argReg, 0, 0, line)
				continue
			}
		}
		if err := c.compileExprTo(arg, argReg); err != nil {
			return err
		}
	}

	b := nArgs + 2
	if lastArgIsMulti {
		b = 0
	}
	cc := nResults + 1
	if nResults == -1 {
		cc = 0
	}

	c.emitABC(OP_CALL, selfReg, b, cc, line)

	c.nextReg = savedReg
	if nResults > 0 {
		needed := dest + nResults
		if needed > c.nextReg {
			c.nextReg = needed
			if c.nextReg > c.maxReg {
				c.maxReg = c.nextReg
			}
		}
	}
	return nil
}

func (c *compiler) emitMethodLookup(selfReg, objReg, methodK, line int) {
	// OP_SELF's C operand is only 8 bits in ABC encoding, so RK constants
	// cannot be represented there. For encodable method constants, use
	// MOVE+GETFIELD so method calls share the field inline-cache path.
	if methodK <= 0xFF {
		c.emitABC(OP_MOVE, selfReg+1, objReg, 0, line)
		c.emitABC(OP_GETFIELD, selfReg, objReg, methodK, line)
		return
	}

	methodReg := c.allocReg()
	c.emitABx(OP_LOADK, methodReg, methodK, line)
	c.emitABC(OP_SELF, selfReg, objReg, methodReg, line)
	c.freeReg() // methodReg
}

// ---- Index / Field expressions ----

func (c *compiler) compileIndexExpr(e *ast.IndexExpr, dest int) error {
	line := e.P.Line
	// Use compileExprReg to avoid allocating a temp for table if it's a local.
	// This prevents table-type temps from being reused by float/int computations,
	// which causes trace JIT guard failures from type-conflicting slot reuse.
	tableReg, tableIsTemp, err := c.compileExprReg(e.Table)
	if err != nil {
		return err
	}
	keyReg := c.allocReg()
	if err := c.compileExprTo(e.Index, keyReg); err != nil {
		return err
	}
	c.emitABC(OP_GETTABLE, dest, tableReg, keyReg, line)
	c.freeReg() // keyReg
	if tableIsTemp {
		c.freeReg() // tableReg (only if we allocated it)
	}
	return nil
}

func (c *compiler) compileFieldExpr(e *ast.FieldExpr, dest int) error {
	line := e.P.Line
	// Use compileExprReg: if table is a local variable, use its register directly
	// instead of allocating a temp. This avoids type-conflicting slot reuse in
	// the trace JIT (table temp later reused for float/int computation).
	tableReg, tableIsTemp, err := c.compileExprReg(e.Table)
	if err != nil {
		return err
	}
	// GETFIELD A B C: R(A) = R(B)[Constants[C]]
	fieldK := c.stringConst(e.Field)
	c.emitABC(OP_GETFIELD, dest, tableReg, fieldK, line)
	if tableIsTemp {
		c.freeReg() // tableReg
	}
	return nil
}

// ---- Table construction ----

type staticStringField struct {
	key      string
	keyConst int
	value    ast.Expr
}

func (c *compiler) compileTableLitExpr(e *ast.TableLitExpr, dest int) error {
	line := e.P.Line
	if ok, err := c.compileTwoFieldTableLitExpr(e, dest, line); ok || err != nil {
		return err
	}
	if ok, err := c.compileSmallFixedTableLitExpr(e, dest, line); ok || err != nil {
		return err
	}

	arrayCount := 0
	hashCount := 0
	for _, f := range e.Fields {
		if f.Key == nil {
			arrayCount++
		} else if !isNilLiteral(f.Value) {
			hashCount++
		}
	}

	c.emitABC(OP_NEWTABLE, dest, arrayCount, hashCount, line)

	arrayIdx := 0
	pendingArrayBase := -1
	pendingArrayCount := 0

	flushArrayBatch := func() {
		if pendingArrayCount > 0 {
			batchNum := (arrayIdx-pendingArrayCount)/50 + 1
			c.emitABC(OP_SETLIST, dest, pendingArrayCount, batchNum, line)
			pendingArrayCount = 0
			pendingArrayBase = -1
		}
	}

	for i, f := range e.Fields {
		if f.Key == nil {
			// Array-style field
			if pendingArrayBase == -1 {
				pendingArrayBase = c.nextReg
			}
			valueReg := c.allocReg()
			isLastField := (i == len(e.Fields)-1)

			if isLastField {
				switch v := f.Value.(type) {
				case *ast.CallExpr:
					if err := c.compileCallExprMulti(v, valueReg, -1); err != nil {
						return err
					}
					arrayIdx++
					pendingArrayCount++
					batchNum := (arrayIdx-pendingArrayCount)/50 + 1
					c.emitABC(OP_SETLIST, dest, 0, batchNum, line)
					c.nextReg = pendingArrayBase
					pendingArrayCount = 0
					pendingArrayBase = -1
					continue
				case *ast.MethodCallExpr:
					if err := c.compileMethodCallExprMulti(v, valueReg, -1); err != nil {
						return err
					}
					arrayIdx++
					pendingArrayCount++
					batchNum := (arrayIdx-pendingArrayCount)/50 + 1
					c.emitABC(OP_SETLIST, dest, 0, batchNum, line)
					c.nextReg = pendingArrayBase
					pendingArrayCount = 0
					pendingArrayBase = -1
					continue
				case *ast.VarArgExpr:
					c.emitABC(OP_VARARG, valueReg, 0, 0, line)
					arrayIdx++
					pendingArrayCount++
					batchNum := (arrayIdx-pendingArrayCount)/50 + 1
					c.emitABC(OP_SETLIST, dest, 0, batchNum, line)
					c.nextReg = pendingArrayBase
					pendingArrayCount = 0
					pendingArrayBase = -1
					continue
				}
			}

			if err := c.compileExprTo(f.Value, valueReg); err != nil {
				return err
			}
			arrayIdx++
			pendingArrayCount++

			if pendingArrayCount >= 50 {
				flushArrayBatch()
				c.nextReg = pendingArrayBase
				pendingArrayBase = -1
			}
		} else {
			// Flush pending array elements first
			if pendingArrayCount > 0 {
				flushArrayBatch()
				c.freeRegs(pendingArrayCount)
				pendingArrayBase = -1
			}

			// Key-value field
			if strKey, ok := f.Key.(*ast.StringLit); ok {
				if isNilLiteral(f.Value) {
					continue
				}
				fieldK := c.stringConst(strKey.Value)
				valReg := c.allocReg()
				if err := c.compileExprTo(f.Value, valReg); err != nil {
					return err
				}
				c.emitABC(OP_SETFIELD, dest, fieldK, valReg, line)
				c.freeReg()
			} else if identKey, ok := f.Key.(*ast.IdentExpr); ok {
				if isNilLiteral(f.Value) {
					continue
				}
				fieldK := c.stringConst(identKey.Name)
				valReg := c.allocReg()
				if err := c.compileExprTo(f.Value, valReg); err != nil {
					return err
				}
				c.emitABC(OP_SETFIELD, dest, fieldK, valReg, line)
				c.freeReg()
			} else {
				keyReg := c.allocReg()
				if err := c.compileExprTo(f.Key, keyReg); err != nil {
					return err
				}
				valReg := c.allocReg()
				if err := c.compileExprTo(f.Value, valReg); err != nil {
					return err
				}
				c.emitABC(OP_SETTABLE, dest, keyReg, valReg, line)
				c.freeRegs(2)
			}
		}
	}

	if pendingArrayCount > 0 {
		flushArrayBatch()
		if pendingArrayBase >= 0 {
			c.nextReg = pendingArrayBase
		}
	}

	return nil
}

func (c *compiler) compileSmallFixedTableLitExpr(e *ast.TableLitExpr, dest int, line int) (bool, error) {
	const maxSmallFixedFields = 8
	fields := make([]staticStringField, 0, len(e.Fields))
	seen := make(map[string]struct{}, len(e.Fields))
	for _, f := range e.Fields {
		if f.Key == nil {
			return false, nil
		}
		if isNilLiteral(f.Value) {
			continue
		}
		key, ok := staticStringFieldName(f.Key)
		if !ok {
			return false, nil
		}
		if _, dup := seen[key]; dup {
			return false, nil
		}
		if !c.smallFixedCtorValueSafe(f.Value) {
			return false, nil
		}
		seen[key] = struct{}{}
		fields = append(fields, staticStringField{
			key:      key,
			keyConst: c.stringConst(key),
			value:    f.Value,
		})
		if len(fields) > maxSmallFixedFields {
			return false, nil
		}
	}
	if len(fields) <= 2 {
		return false, nil
	}
	ctor := c.addTableCtorN(fields)
	if ctor < 0 || ctor > 255 {
		return false, nil
	}

	valueBase := c.nextReg
	for range fields {
		c.allocReg()
	}
	for i := range fields {
		if err := c.compileExprTo(fields[i].value, valueBase+i); err != nil {
			c.nextReg = valueBase
			return true, err
		}
	}
	c.emitABC(OP_NEWOBJECTN, dest, ctor, valueBase, line)
	c.nextReg = valueBase
	return true, nil
}

func (c *compiler) smallFixedCtorValueSafe(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.NumberLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit:
		return true
	case *ast.IdentExpr:
		return c.resolveLocal(v.Name) >= 0
	case *ast.BinaryExpr:
		return c.smallFixedCtorValueSafe(v.Left) && c.smallFixedCtorValueSafe(v.Right)
	case *ast.UnaryExpr:
		return c.smallFixedCtorValueSafe(v.Operand)
	default:
		return false
	}
}

func (c *compiler) compileTwoFieldTableLitExpr(e *ast.TableLitExpr, dest int, line int) (bool, error) {
	fields := make([]staticStringField, 0, 2)
	for _, f := range e.Fields {
		if f.Key == nil {
			return false, nil
		}
		if isNilLiteral(f.Value) {
			continue
		}
		key, ok := staticStringFieldName(f.Key)
		if !ok {
			return false, nil
		}
		fields = append(fields, staticStringField{
			key:      key,
			keyConst: c.stringConst(key),
			value:    f.Value,
		})
		if len(fields) > 2 {
			return false, nil
		}
	}
	if len(fields) != 2 || fields[0].key == fields[1].key {
		return false, nil
	}
	ctor := c.addTableCtor2(fields[0].keyConst, fields[1].keyConst, fields[0].key, fields[1].key)
	if ctor < 0 || ctor > 255 {
		return false, nil
	}

	valueBase := c.nextReg
	val1 := c.allocReg()
	val2 := c.allocReg()
	if err := c.compileExprTo(fields[0].value, val1); err != nil {
		return true, err
	}
	if err := c.compileExprTo(fields[1].value, val2); err != nil {
		return true, err
	}
	c.emitABC(OP_NEWOBJECT2, dest, ctor, valueBase, line)
	c.nextReg = valueBase
	return true, nil
}

func staticStringFieldName(key ast.Expr) (string, bool) {
	switch k := key.(type) {
	case *ast.StringLit:
		return k.Value, true
	case *ast.IdentExpr:
		return k.Name, true
	default:
		return "", false
	}
}

func (c *compiler) addTableCtor2(key1Const, key2Const int, key1, key2 string) int {
	for i := range c.proto.TableCtors2 {
		ctor := &c.proto.TableCtors2[i]
		if ctor.Key1Const == key1Const && ctor.Key2Const == key2Const {
			return i
		}
	}
	if len(c.proto.TableCtors2) >= 256 {
		return -1
	}
	c.proto.TableCtors2 = append(c.proto.TableCtors2, TableCtor2{
		Key1Const: key1Const,
		Key2Const: key2Const,
		Runtime:   runtime.NewSmallTableCtor2(key1, key2),
	})
	return len(c.proto.TableCtors2) - 1
}

func (c *compiler) addTableCtorN(fields []staticStringField) int {
	for i := range c.proto.TableCtorsN {
		ctor := &c.proto.TableCtorsN[i]
		if len(ctor.KeyConsts) != len(fields) {
			continue
		}
		match := true
		for j := range fields {
			if ctor.KeyConsts[j] != fields[j].keyConst {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	if len(c.proto.TableCtorsN) >= 256 {
		return -1
	}
	keyConsts := make([]int, len(fields))
	keys := make([]string, len(fields))
	for i := range fields {
		keyConsts[i] = fields[i].keyConst
		keys[i] = fields[i].key
	}
	c.proto.TableCtorsN = append(c.proto.TableCtorsN, TableCtorN{
		KeyConsts: keyConsts,
		Runtime:   runtime.NewSmallTableCtorN(keys),
	})
	return len(c.proto.TableCtorsN) - 1
}

func isNilLiteral(e ast.Expr) bool {
	_, ok := e.(*ast.NilLit)
	return ok
}

// ---- Function literal ----

func (c *compiler) compileFuncLitExpr(e *ast.FuncLitExpr, dest int) error {
	line := e.P.Line
	protoIdx, err := c.compileFunction("<anonymous>", e.Params, e.Body, line)
	if err != nil {
		return err
	}
	c.emitABx(OP_CLOSURE, dest, protoIdx, line)
	return nil
}

// --------------------------------------------------------------------
// Function compilation
// --------------------------------------------------------------------

func (c *compiler) compileFunction(name string, params []ast.FuncParam, body *ast.BlockStmt, line int) (int, error) {
	isVarArg := false
	for _, p := range params {
		if p.IsVarArg {
			isVarArg = true
		}
	}

	child := newCompiler(c, name, line, isVarArg)
	child.enterScope()

	numFixedParams := 0
	for _, p := range params {
		if p.IsVarArg {
			break
		}
		child.addLocal(p.Name)
		numFixedParams++
	}

	for _, stmt := range body.Stmts {
		if err := child.compileStmt(stmt); err != nil {
			return 0, err
		}
	}

	child.leaveScope()

	code := child.proto.Code
	needReturn := true
	if len(code) > 0 {
		if DecodeOp(code[len(code)-1]) == OP_RETURN {
			needReturn = false
		}
	}
	if needReturn {
		child.emitReturn(0, 1)
	}

	proto := child.finish()
	proto.NumParams = numFixedParams
	proto.IsVarArg = isVarArg
	proto.Name = name

	idx := len(c.proto.Protos)
	c.proto.Protos = append(c.proto.Protos, proto)
	return idx, nil
}

// --------------------------------------------------------------------
// Disassemble: human-readable bytecode dump
// --------------------------------------------------------------------

func Disassemble(proto *FuncProto) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("function %q (%d instructions, %d constants, %d upvalues, %d protos)\n",
		proto.Name, len(proto.Code), len(proto.Constants), len(proto.Upvalues), len(proto.Protos)))
	sb.WriteString(fmt.Sprintf("  params=%d, vararg=%v, maxstack=%d\n", proto.NumParams, proto.IsVarArg, proto.MaxStack))

	if len(proto.Constants) > 0 {
		sb.WriteString("  constants:\n")
		for i, k := range proto.Constants {
			sb.WriteString(fmt.Sprintf("    [%d] %s\n", i, k.String()))
		}
	}
	if len(proto.Upvalues) > 0 {
		sb.WriteString("  upvalues:\n")
		for i, uv := range proto.Upvalues {
			sb.WriteString(fmt.Sprintf("    [%d] %s instack=%v index=%d\n", i, uv.Name, uv.InStack, uv.Index))
		}
	}

	for i, inst := range proto.Code {
		op := DecodeOp(inst)
		a := DecodeA(inst)
		b := DecodeB(inst)
		cc := DecodeC(inst)
		bx := DecodeBx(inst)
		sbx := DecodesBx(inst)

		line := 0
		if i < len(proto.LineInfo) {
			line = proto.LineInfo[i]
		}

		var desc string
		switch op {
		case OP_LOADNIL:
			desc = fmt.Sprintf("LOADNIL    R%d..R%d", a, a+b)
		case OP_LOADBOOL:
			desc = fmt.Sprintf("LOADBOOL   R%d %v skip=%d", a, b != 0, cc)
		case OP_LOADINT:
			desc = fmt.Sprintf("LOADINT    R%d %d", a, sbx)
		case OP_LOADK:
			desc = fmt.Sprintf("LOADK      R%d K%d  ; %s", a, bx, proto.Constants[bx].String())
		case OP_MOVE:
			desc = fmt.Sprintf("MOVE       R%d R%d", a, b)
		case OP_GETGLOBAL:
			desc = fmt.Sprintf("GETGLOBAL  R%d K%d  ; %s", a, bx, proto.Constants[bx].String())
		case OP_SETGLOBAL:
			desc = fmt.Sprintf("SETGLOBAL  R%d K%d  ; %s", a, bx, proto.Constants[bx].String())
		case OP_GETUPVAL:
			desc = fmt.Sprintf("GETUPVAL   R%d U%d", a, b)
		case OP_SETUPVAL:
			desc = fmt.Sprintf("SETUPVAL   R%d U%d", a, b)
		case OP_NEWTABLE:
			desc = fmt.Sprintf("NEWTABLE   R%d array=%d hash=%d", a, b, cc)
		case OP_NEWOBJECT2:
			desc = fmt.Sprintf("NEWOBJECT2 R%d ctor=%d values=R%d,R%d", a, b, cc, cc+1)
		case OP_NEWOBJECTN:
			n := 0
			if b >= 0 && b < len(proto.TableCtorsN) {
				n = len(proto.TableCtorsN[b].KeyConsts)
			}
			desc = fmt.Sprintf("NEWOBJECTN R%d ctor=%d values=R%d..R%d", a, b, cc, cc+n-1)
		case OP_GETTABLE:
			desc = fmt.Sprintf("GETTABLE   R%d R%d R%d", a, b, cc)
		case OP_SETTABLE:
			desc = fmt.Sprintf("SETTABLE   R%d R%d R%d", a, b, cc)
		case OP_GETFIELD:
			desc = fmt.Sprintf("GETFIELD   R%d R%d K%d", a, b, cc)
		case OP_SETFIELD:
			desc = fmt.Sprintf("SETFIELD   R%d K%d R%d", a, b, cc)
		case OP_SETLIST:
			desc = fmt.Sprintf("SETLIST    R%d count=%d batch=%d", a, b, cc)
		case OP_APPEND:
			desc = fmt.Sprintf("APPEND     R%d R%d", a, b)
		case OP_ADD:
			desc = fmt.Sprintf("ADD        R%d R%d R%d", a, b, cc)
		case OP_SUB:
			desc = fmt.Sprintf("SUB        R%d R%d R%d", a, b, cc)
		case OP_MUL:
			desc = fmt.Sprintf("MUL        R%d R%d R%d", a, b, cc)
		case OP_DIV:
			desc = fmt.Sprintf("DIV        R%d R%d R%d", a, b, cc)
		case OP_MOD:
			desc = fmt.Sprintf("MOD        R%d R%d R%d", a, b, cc)
		case OP_POW:
			desc = fmt.Sprintf("POW        R%d R%d R%d", a, b, cc)
		case OP_UNM:
			desc = fmt.Sprintf("UNM        R%d R%d", a, b)
		case OP_NOT:
			desc = fmt.Sprintf("NOT        R%d R%d", a, b)
		case OP_LEN:
			desc = fmt.Sprintf("LEN        R%d R%d", a, b)
		case OP_CONCAT:
			desc = fmt.Sprintf("CONCAT     R%d R%d..R%d", a, b, cc)
		case OP_EQ:
			desc = fmt.Sprintf("EQ         %d R%d R%d", a, b, cc)
		case OP_LT:
			desc = fmt.Sprintf("LT         %d R%d R%d", a, b, cc)
		case OP_LE:
			desc = fmt.Sprintf("LE         %d R%d R%d", a, b, cc)
		case OP_TEST:
			desc = fmt.Sprintf("TEST       R%d %d", a, cc)
		case OP_TESTSET:
			desc = fmt.Sprintf("TESTSET    R%d R%d %d", a, b, cc)
		case OP_JMP:
			target := i + 1 + sbx
			desc = fmt.Sprintf("JMP        %d  ; to %d", sbx, target)
		case OP_CALL:
			desc = fmt.Sprintf("CALL       R%d B=%d C=%d", a, b, cc)
		case OP_YIELD:
			desc = fmt.Sprintf("YIELD      R%d B=%d C=%d", a, b, cc)
		case OP_RESUME:
			desc = fmt.Sprintf("RESUME     R%d B=%d C=%d", a, b, cc)
		case OP_RETURN:
			desc = fmt.Sprintf("RETURN     R%d B=%d", a, b)
		case OP_CLOSURE:
			desc = fmt.Sprintf("CLOSURE    R%d Proto%d", a, bx)
		case OP_CLOSE:
			desc = fmt.Sprintf("CLOSE      R%d", a)
		case OP_FORPREP:
			desc = fmt.Sprintf("FORPREP    R%d %d  ; to %d", a, sbx, i+1+sbx)
		case OP_FORLOOP:
			desc = fmt.Sprintf("FORLOOP    R%d %d  ; to %d", a, sbx, i+1+sbx)
		case OP_TFORCALL:
			desc = fmt.Sprintf("TFORCALL   R%d C=%d", a, cc)
		case OP_TFORLOOP:
			desc = fmt.Sprintf("TFORLOOP   R%d %d  ; to %d", a, sbx, i+1+sbx)
		case OP_VARARG:
			desc = fmt.Sprintf("VARARG     R%d B=%d", a, b)
		case OP_SELF:
			desc = fmt.Sprintf("SELF       R%d R%d K%d", a, b, cc)
		case OP_GO:
			desc = fmt.Sprintf("GO         R%d B=%d", a, b)
		case OP_MAKECHAN:
			desc = fmt.Sprintf("MAKECHAN   R%d B=%d C=%d", a, b, cc)
		case OP_SEND:
			desc = fmt.Sprintf("SEND       R%d <- R%d", a, b)
		case OP_RECV:
			desc = fmt.Sprintf("RECV       R%d = <-R%d", a, b)
		default:
			desc = fmt.Sprintf("%-10s %d %d %d", OpName(op), a, b, cc)
		}

		sb.WriteString(fmt.Sprintf("  [%03d] %-4d %s\n", i, line, desc))
	}

	for i, p := range proto.Protos {
		sb.WriteString(fmt.Sprintf("\n--- Proto %d ---\n", i))
		sb.WriteString(Disassemble(p))
	}

	return sb.String()
}
