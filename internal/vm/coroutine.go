package vm

import (
	"errors"
	"fmt"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
)

// VMCoroutineStatus represents the state of a VM coroutine.
type VMCoroutineStatus int

const (
	VMCoroutineSuspended VMCoroutineStatus = iota
	VMCoroutineRunning
	VMCoroutineDead
	VMCoroutineNormal // resumed another coroutine
)

// vmYieldResult carries yielded values from a paused coroutine VM.
type vmYieldResult struct {
	values []rt.Value
}

var errCoroutineYield = errors.New("coroutine yield")

const (
	coroutineCreateName      = "coroutine.create"
	coroutineResumeName      = "coroutine.resume"
	coroutineYieldName       = "coroutine.yield"
	coroutineIsYieldableName = "coroutine.isyieldable"

	goFunctionKindCoroutineWrapper = 1
	goFunctionKindCoroutineCreate  = 3
	goFunctionKindCoroutineResume  = 4
	goFunctionKindCoroutineYield   = 5
	goFunctionKindCoroutineStatus  = 6
	goFunctionKindCoroutineIsYield = 7
)

// VMCoroutine holds the state of a VM-based coroutine. Long-lived coroutines
// keep a child VM paused at the bytecode immediately after coroutine.yield.
type VMCoroutine struct {
	status     VMCoroutineStatus
	closure    *Closure
	started    bool
	leafNoCall bool
	wrapped    bool
	// stackYieldEnabled is set by the resumer (consumer) when static analysis
	// (ResumePayloadIsFieldOnly) of the bytecode after the resume site shows
	// the yielded payload is only read via GETFIELD. Consulted in vm.go's
	// OP_NEWOBJECTN fixed-record path to reuse pooledFixedRecord instead of
	// allocating a fresh FixedRecord per yield.
	stackYieldEnabled bool
	// pooledFixedRecord is a reusable inline FixedRecord allocated lazily when
	// stackYieldEnabled is true. The producer rewrites it in place per yield
	// instead of allocating a fresh record. Safe because the consumer's static
	// analysis guarantees GETFIELD-only access; the record cannot outlive the
	// iteration that produced it.
	pooledFixedRecord *rt.FixedRecord
	vm                *VM
	yieldDst          int
	yieldC            int

	yieldResult        vmYieldResult
	jitContinuation    MethodJITContinuation
	hasJITContinuation bool
	fastJITCode        uintptr
	fastJITCtx         uintptr
	fastJITResumePC    int

	wrappedGenerator *wrappedNumericGenerator
}

func init() {
	rt.RegisterVMCoroutinePtrResolver(func(p unsafe.Pointer) any {
		return (*VMCoroutine)(p)
	})
}

// NewVMCoroutine creates a new VM coroutine wrapping the given closure.
func NewVMCoroutine(cl *Closure) *VMCoroutine {
	return &VMCoroutine{
		status:     VMCoroutineSuspended,
		closure:    cl,
		leafNoCall: cl != nil && cl.Proto != nil && protoHasNoCalls(cl.Proto),
	}
}

// Status returns the human-readable status string.
func (co *VMCoroutine) Status() string {
	switch co.status {
	case VMCoroutineSuspended:
		return "suspended"
	case VMCoroutineRunning:
		return "running"
	case VMCoroutineDead:
		return "dead"
	case VMCoroutineNormal:
		return "normal"
	}
	return "dead"
}

func vmCoroutineFromValue(v rt.Value) (*VMCoroutine, bool) {
	if p := v.AnyCoroutinePointer(); p != nil {
		return (*VMCoroutine)(p), true
	}
	co, ok := v.Ptr().(*VMCoroutine)
	return co, ok
}

func vmCoroutineFromNativeData(p unsafe.Pointer) (*VMCoroutine, bool) {
	if p == nil {
		return nil, false
	}
	return (*VMCoroutine)(p), true
}

func (vm *VM) activeCoroutine() *VMCoroutine {
	return vm.currentCoroutine
}

func (vm *VM) CurrentCoroutinePtr() uintptr {
	if vm == nil || vm.currentCoroutine == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(vm.currentCoroutine))
}

// IsCoroutineYield reports whether err is the VM's internal coroutine
// suspension sentinel. JIT exit handlers use this to propagate suspension
// without wrapping it as an ordinary call failure.
func IsCoroutineYield(err error) bool {
	return err == errCoroutineYield
}

// CoroutineYieldError returns the internal sentinel used to unwind a VM
// coroutine after it suspends.
func CoroutineYieldError() error {
	return errCoroutineYield
}

// RegisterCoroutineLib installs VM-native coroutine functions into globals,
// overriding the tree-walker's coroutine library which cannot handle VM closures.
func (vm *VM) RegisterCoroutineLib() {
	vm.SetGlobal("coroutine", rt.TableValue(vm.newCoroutineLib()))
}

func (vm *VM) newCoroutineLib() *rt.Table {
	coLib := rt.NewTable()

	// coroutine.create(fn) -> coroutine
	createFn := &rt.GoFunction{
		Name:       coroutineCreateName,
		NativeKind: goFunctionKindCoroutineCreate,
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsFunction() {
				return nil, fmt.Errorf("coroutine.create expects a function")
			}
			cl, ok := closureFromValue(args[0])
			if !ok {
				// Also accept GoFunctions — wrap in a tiny VM closure is not possible,
				// but we can use the GoFunction approach.
				return nil, fmt.Errorf("coroutine.create expects a GScript function, got Go function")
			}
			co := NewVMCoroutine(cl)
			vm.recordCoroutineCreated(false)
			return []rt.Value{rt.VMCoroutineValue(unsafe.Pointer(co), co)}, nil
		},
	}
	vm.coroutineCreateFn = createFn
	coLib.RawSet(rt.StringValue("create"), rt.FunctionValue(createFn))

	// coroutine.resume(co, args...) -> ok, values...
	resumeFn := &rt.GoFunction{
		Name:       coroutineResumeName,
		NativeKind: goFunctionKindCoroutineResume,
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.resume expects a coroutine")
			}
			co, ok := vmCoroutineFromValue(args[0])
			if !ok {
				return nil, fmt.Errorf("coroutine.resume expects a VM coroutine")
			}
			return vm.resumeCoroutine(co, args[1:])
		},
	}
	vm.coroutineResumeFn = resumeFn
	coLib.RawSet(rt.StringValue("resume"), rt.FunctionValue(resumeFn))

	// coroutine.yield(values...) -> resume args
	yieldFn := &rt.GoFunction{
		Name:       coroutineYieldName,
		NativeKind: goFunctionKindCoroutineYield,
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			return vm.yieldCoroutine(args)
		},
	}
	vm.coroutineYieldFn = yieldFn
	coLib.RawSet(rt.StringValue("yield"), rt.FunctionValue(yieldFn))

	// coroutine.status(co) -> string
	coLib.RawSet(rt.StringValue("status"), rt.FunctionValue(&rt.GoFunction{
		Name:       "coroutine.status",
		NativeKind: goFunctionKindCoroutineStatus,
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.status expects a coroutine")
			}
			co, ok := vmCoroutineFromValue(args[0])
			if !ok {
				return nil, fmt.Errorf("coroutine.status expects a VM coroutine")
			}
			return []rt.Value{rt.StringValue(co.Status())}, nil
		},
	}))

	// coroutine.isyieldable() -> bool
	coLib.RawSet(rt.StringValue("isyieldable"), rt.FunctionValue(&rt.GoFunction{
		Name:       coroutineIsYieldableName,
		NativeKind: goFunctionKindCoroutineIsYield,
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			return []rt.Value{rt.BoolValue(vm.activeCoroutine() != nil)}, nil
		},
	}))

	// coroutine.wrap(fn) -> iterator function
	coLib.RawSet(rt.StringValue("wrap"), rt.FunctionValue(&rt.GoFunction{
		Name: "coroutine.wrap",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsFunction() {
				return nil, fmt.Errorf("coroutine.wrap expects a function")
			}
			cl, ok := closureFromValue(args[0])
			if !ok {
				return nil, fmt.Errorf("coroutine.wrap expects a GScript function")
			}
			co := NewVMCoroutine(cl)
			co.wrapped = true
			co.wrappedGenerator = newWrappedNumericGenerator(cl)
			vm.recordCoroutineCreated(true)
			dead := false
			wrapper := &rt.GoFunction{
				Name:       "wrapped_coroutine",
				NativeKind: goFunctionKindCoroutineWrapper,
				NativeData: unsafe.Pointer(co),
				Fn: func(wargs []rt.Value) ([]rt.Value, error) {
					if dead {
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					ok, values, err := vm.resumeCoroutineRaw(co, wargs)
					if err != nil {
						return nil, err
					}
					if !ok {
						dead = true
						if len(values) > 0 {
							return nil, fmt.Errorf("%s", values[0].String())
						}
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					// Return yielded values; return nil if none (signals end of iteration).
					if len(values) > 0 {
						return values, nil
					}
					// Coroutine returned without values — mark as done, return nil for for-range
					dead = true
					return []rt.Value{rt.NilValue()}, nil
				},
			}
			return []rt.Value{rt.FunctionValue(wrapper)}, nil
		},
	}))

	return coLib
}

type generatorInitRefKind uint8

const (
	generatorInitInvalid generatorInitRefKind = iota
	generatorInitInt
	generatorInitConst
	generatorInitUpvalue
)

type generatorInitRef struct {
	kind  generatorInitRefKind
	intv  int64
	index int
}

type generatorOperandKind uint8

const (
	generatorOperandInvalid generatorOperandKind = iota
	generatorOperandCurrent
	generatorOperandLimit
	generatorOperandStep
	generatorOperandConst
)

type generatorOperand struct {
	kind  generatorOperandKind
	index int
}

type generatorExprKind uint8

const (
	generatorExprCurrent generatorExprKind = iota
	generatorExprAdd
	generatorExprSub
	generatorExprMul
)

type generatorExpr struct {
	kind        generatorExprKind
	left, right generatorOperand
}

type wrappedNumericGenerator struct {
	closure *Closure
	init    generatorInitRef
	limit   generatorInitRef
	step    generatorInitRef
	expr    generatorExpr

	initialized bool
	next        int64
	limitValue  int64
	stepValue   int64
}

func newWrappedNumericGenerator(cl *Closure) *wrappedNumericGenerator {
	if cl == nil || cl.Proto == nil {
		return nil
	}
	proto := cl.Proto
	if proto.NumParams != 0 || proto.IsVarArg || proto.MaxStack > 16 {
		return nil
	}
	code := proto.Code
	if len(code) < 5 || len(code) > 12 {
		return nil
	}
	p := newBytecodePattern(code)
	forprepPC := -1
	forA := 0
	for pc, inst := range code {
		if DecodeOp(inst) == OP_FORPREP {
			forprepPC = pc
			forA = DecodeA(inst)
			break
		}
	}
	if forprepPC < 0 || forA+3 >= proto.MaxStack {
		return nil
	}
	bodyPC, loopPC, ok := p.numericForLoop(forprepPC, forA)
	if !ok || loopPC <= bodyPC || loopPC >= len(code) {
		return nil
	}
	if !p.returnFixed(loopPC+1, 0, 1) {
		return nil
	}

	refs := make([]generatorInitRef, proto.MaxStack)
	for pc := 0; pc < forprepPC; pc++ {
		inst := code[pc]
		a := DecodeA(inst)
		switch DecodeOp(inst) {
		case OP_LOADINT:
			if a < len(refs) {
				refs[a] = generatorInitRef{kind: generatorInitInt, intv: int64(DecodesBx(inst))}
			}
		case OP_LOADK:
			if a < len(refs) {
				refs[a] = generatorInitRef{kind: generatorInitConst, index: DecodeBx(inst)}
			}
		case OP_GETUPVAL:
			if a < len(refs) {
				refs[a] = generatorInitRef{kind: generatorInitUpvalue, index: DecodeB(inst)}
			}
		case OP_MOVE:
			src := DecodeB(inst)
			if a < len(refs) && src >= 0 && src < len(refs) && refs[src].kind != generatorInitInvalid {
				refs[a] = refs[src]
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	if refs[forA].kind == generatorInitInvalid || refs[forA+1].kind == generatorInitInvalid || refs[forA+2].kind == generatorInitInvalid {
		return nil
	}

	yieldPC := loopPC - 1
	yieldInst, ok := p.op(yieldPC, OP_YIELD)
	if !ok || DecodeB(yieldInst) != 2 || DecodeC(yieldInst) != 0 {
		return nil
	}
	yieldReg := DecodeA(yieldInst) + 1
	iterReg := forA + 3
	expr := generatorExpr{kind: generatorExprCurrent}
	if yieldReg != iterReg {
		if yieldPC != bodyPC+1 {
			return nil
		}
		exprInst := code[bodyPC]
		if DecodeA(exprInst) != yieldReg {
			return nil
		}
		left, ok := generatorOperandForRegOrConst(proto, DecodeB(exprInst), forA, iterReg)
		if !ok {
			return nil
		}
		right, ok := generatorOperandForRegOrConst(proto, DecodeC(exprInst), forA, iterReg)
		if !ok {
			return nil
		}
		switch DecodeOp(exprInst) {
		case OP_ADD:
			expr = generatorExpr{kind: generatorExprAdd, left: left, right: right}
		case OP_SUB:
			expr = generatorExpr{kind: generatorExprSub, left: left, right: right}
		case OP_MUL:
			expr = generatorExpr{kind: generatorExprMul, left: left, right: right}
		default:
			return nil
		}
	}
	return &wrappedNumericGenerator{
		closure: cl,
		init:    refs[forA],
		limit:   refs[forA+1],
		step:    refs[forA+2],
		expr:    expr,
	}
}

func generatorOperandForRegOrConst(proto *FuncProto, rk, forA, iterReg int) (generatorOperand, bool) {
	if rk >= RKBit {
		idx := rk - RKBit
		if idx < 0 || proto == nil || idx >= len(proto.Constants) || !proto.Constants[idx].IsInt() {
			return generatorOperand{}, false
		}
		return generatorOperand{kind: generatorOperandConst, index: idx}, true
	}
	switch rk {
	case forA, iterReg:
		return generatorOperand{kind: generatorOperandCurrent}, true
	case forA + 1:
		return generatorOperand{kind: generatorOperandLimit}, true
	case forA + 2:
		return generatorOperand{kind: generatorOperandStep}, true
	default:
		return generatorOperand{}, false
	}
}

func (g *wrappedNumericGenerator) initInt(ref generatorInitRef) (int64, bool) {
	if g == nil || g.closure == nil || g.closure.Proto == nil {
		return 0, false
	}
	switch ref.kind {
	case generatorInitInt:
		return ref.intv, true
	case generatorInitConst:
		if ref.index < 0 || ref.index >= len(g.closure.Proto.Constants) {
			return 0, false
		}
		v := g.closure.Proto.Constants[ref.index]
		if !v.IsInt() {
			return 0, false
		}
		return v.Int(), true
	case generatorInitUpvalue:
		if ref.index < 0 || ref.index >= len(g.closure.Upvalues) || g.closure.Upvalues[ref.index] == nil {
			return 0, false
		}
		v := g.closure.Upvalues[ref.index].Get()
		if !v.IsInt() {
			return 0, false
		}
		return v.Int(), true
	default:
		return 0, false
	}
}

func (g *wrappedNumericGenerator) initialize() bool {
	if g == nil || g.initialized {
		return g != nil && g.initialized
	}
	init, ok := g.initInt(g.init)
	if !ok {
		return false
	}
	limit, ok := g.initInt(g.limit)
	if !ok {
		return false
	}
	step, ok := g.initInt(g.step)
	if !ok || step == 0 {
		return false
	}
	g.next = init
	g.limitValue = limit
	g.stepValue = step
	g.initialized = true
	return true
}

func (g *wrappedNumericGenerator) operandInt(op generatorOperand, current int64) (int64, bool) {
	switch op.kind {
	case generatorOperandCurrent:
		return current, true
	case generatorOperandLimit:
		return g.limitValue, true
	case generatorOperandStep:
		return g.stepValue, true
	case generatorOperandConst:
		if g == nil || g.closure == nil || g.closure.Proto == nil || op.index < 0 || op.index >= len(g.closure.Proto.Constants) {
			return 0, false
		}
		v := g.closure.Proto.Constants[op.index]
		if !v.IsInt() {
			return 0, false
		}
		return v.Int(), true
	default:
		return 0, false
	}
}

func (g *wrappedNumericGenerator) valueFor(current int64) (rt.Value, bool) {
	switch g.expr.kind {
	case generatorExprCurrent:
		return rt.IntValue(current), true
	case generatorExprAdd, generatorExprSub, generatorExprMul:
		left, ok := g.operandInt(g.expr.left, current)
		if !ok {
			return rt.NilValue(), false
		}
		right, ok := g.operandInt(g.expr.right, current)
		if !ok {
			return rt.NilValue(), false
		}
		switch g.expr.kind {
		case generatorExprAdd:
			return rt.IntValue(left + right), true
		case generatorExprSub:
			return rt.IntValue(left - right), true
		case generatorExprMul:
			return rt.IntValue(left * right), true
		}
	}
	return rt.NilValue(), false
}

func (vm *VM) tryFastWrappedGeneratorCall(co *VMCoroutine, base, a, nArgs, c int) (bool, error) {
	if vm == nil || co == nil || co.wrappedGenerator == nil || nArgs != 0 {
		return false, nil
	}
	if co.status == VMCoroutineDead {
		vm.recordCoroutineResume()
		vm.recordCoroutineResumeError()
		return true, fmt.Errorf("cannot resume dead coroutine")
	}
	if co.status == VMCoroutineRunning {
		vm.recordCoroutineResume()
		vm.recordCoroutineResumeError()
		return true, fmt.Errorf("cannot resume running coroutine")
	}
	gen := co.wrappedGenerator
	if !gen.initialize() {
		return false, nil
	}
	vm.recordCoroutineResume()
	vm.recordCoroutineWrappedGeneratorFastPath()
	co.started = true
	current := gen.next
	if (gen.stepValue > 0 && current > gen.limitValue) || (gen.stepValue < 0 && current < gen.limitValue) {
		co.status = VMCoroutineDead
		vm.recordCoroutineCompleted()
		vm.writeSingleCallResult(base+a, c, rt.NilValue())
		return true, nil
	}
	value, ok := gen.valueFor(current)
	if !ok {
		return false, nil
	}
	gen.next = current + gen.stepValue
	co.status = VMCoroutineSuspended
	vm.recordCoroutineYield()
	vm.writeSingleCallResult(base+a, c, value)
	return true, nil
}

// resumeCoroutine resumes a suspended VM coroutine.
func (vm *VM) resumeCoroutine(co *VMCoroutine, args []rt.Value) ([]rt.Value, error) {
	ok, values, err := vm.resumeCoroutineRaw(co, args)
	if err != nil {
		return nil, err
	}
	return vm.coroutineResumeResults(ok, values), nil
}

func (vm *VM) resumeCoroutineRaw(co *VMCoroutine, args []rt.Value) (bool, []rt.Value, error) {
	vm.recordCoroutineResume()
	if co.status == VMCoroutineDead {
		vm.recordCoroutineResumeError()
		return false, []rt.Value{rt.StringValue("cannot resume dead coroutine")}, nil
	}
	if co.status == VMCoroutineRunning {
		vm.recordCoroutineResumeError()
		return false, []rt.Value{rt.StringValue("cannot resume running coroutine")}, nil
	}

	co.status = VMCoroutineRunning

	if !co.started && co.leafNoCall {
		co.started = true
		vm.recordCoroutineLeafFastPath()
		results, err, ok := vm.evalLeafCoroutineExpression(co, args)
		if !ok {
			results, err, ok = vm.callLeafCoroutine(co, args)
		}
		if !ok {
			vm.recordCoroutineLeafFallback()
			coVM := newChildVM(vm, co)
			defer coVM.Close()
			results, err = coVM.call(co.closure, args, 0, 0)
		}
		if results == nil {
			results = []rt.Value{}
		}
		co.status = VMCoroutineDead
		vm.recordCoroutineCompleted()
		if err != nil {
			return false, []rt.Value{rt.StringValue(err.Error())}, nil
		}
		return true, results, nil
	}

	if !co.started {
		co.started = true
		vm.recordCoroutineGoroutineStart()
		co.vm = newChildVM(vm, co)
		vm.attachCoroutineJIT(co)
		results, err := co.vm.call(co.closure, args, 0, 0)
		return vm.finishCoroutineRun(co, results, err)
	}

	if co.vm == nil {
		co.status = VMCoroutineDead
		vm.recordCoroutineResumeError()
		return false, []rt.Value{rt.StringValue("cannot resume dead coroutine")}, nil
	}
	results, err, handled := vm.resumeCoroutineJITContinuation(co, args)
	if !handled {
		co.vm.writeCallResults(co.yieldDst, co.yieldC, args)
		results, err = co.vm.run()
	}
	return vm.finishCoroutineRun(co, results, err)
}

func (vm *VM) attachCoroutineJIT(co *VMCoroutine) {
	if vm == nil || co == nil || co.vm == nil || vm.methodJIT == nil {
		return
	}
	factory, ok := vm.methodJIT.(methodJITEngineWithCoroutineChild)
	if !ok {
		return
	}
	childEngine := factory.NewCoroutineChildEngine(co.vm)
	if childEngine != nil {
		co.vm.SetMethodJIT(childEngine)
	}
}

func (vm *VM) resumeCoroutineJITContinuation(co *VMCoroutine, args []rt.Value) ([]rt.Value, error, bool) {
	if co == nil || co.vm == nil || !co.hasJITContinuation {
		return nil, nil, false
	}
	exec, ok := co.vm.methodJIT.(methodJITEngineWithContinuation)
	if !ok {
		return nil, nil, false
	}
	co.vm.writeCallResults(co.yieldDst, co.yieldC, args)
	cont := co.jitContinuation
	co.jitContinuation = MethodJITContinuation{}
	co.hasJITContinuation = false
	results, err := exec.ExecuteContinuation(cont, co.vm.regs, co.vm.retBuf[:0])
	vm.recordCoroutineJITContinuation()
	if err == nil && !co.vm.coroutineYielded {
		co.vm.closeUpvalues(cont.Base)
		co.vm.PopFrame()
	}
	return results, err, true
}

func (vm *VM) finishCoroutineRun(co *VMCoroutine, results []rt.Value, err error) (bool, []rt.Value, error) {
	if err == errCoroutineYield || (co.vm != nil && co.vm.coroutineYielded) {
		result := co.yieldResult
		co.yieldResult = vmYieldResult{}
		if co.vm != nil {
			co.vm.coroutineYielded = false
		}
		co.status = VMCoroutineSuspended
		return true, result.values, nil
	}
	if results == nil {
		results = []rt.Value{}
	}
	co.releaseVM()
	if err != nil {
		co.status = VMCoroutineDead
		vm.recordCoroutineCompleted()
		return false, []rt.Value{rt.StringValue(err.Error())}, nil
	}
	co.status = VMCoroutineDead
	vm.recordCoroutineCompleted()
	return true, results, nil
}

func (co *VMCoroutine) releaseVM() {
	if co.vm == nil {
		return
	}
	co.jitContinuation = MethodJITContinuation{}
	co.hasJITContinuation = false
	co.vm.frameCount = 0
	co.vm.top = 0
	co.vm.Close()
	co.vm = nil
}

func (vm *VM) yieldCoroutine(args []rt.Value) ([]rt.Value, error) {
	if vm.activeCoroutine() == nil {
		return nil, fmt.Errorf("cannot yield from outside a coroutine")
	}
	return nil, fmt.Errorf("coroutine.yield requires VM call dispatch")
}

func (vm *VM) suspendCoroutine(args []rt.Value, dst, c int) error {
	co := vm.activeCoroutine()
	if co == nil {
		return fmt.Errorf("cannot yield from outside a coroutine")
	}
	vm.recordCoroutineYield()
	co.yieldResult = vmYieldResult{values: args}
	co.yieldDst = dst
	co.yieldC = c
	vm.coroutineYielded = true
	return nil
}

// SuspendCoroutineFromSlots is the JIT-facing form of OP_YIELD. absSlot is the
// absolute A register of OP_YIELD; yielded values start at A+1.
func (vm *VM) SuspendCoroutineFromSlots(absSlot, nArgs, c int) error {
	if vm == nil {
		return fmt.Errorf("cannot yield from outside a coroutine")
	}
	args, err := vm.coroutineYieldBoundaryFromSlots(absSlot, nArgs)
	if err != nil {
		return err
	}
	return vm.suspendCoroutine(args, absSlot, c)
}

func (vm *VM) coroutineYieldBoundaryFromSlots(absSlot, nArgs int) ([]rt.Value, error) {
	if vm == nil {
		return nil, fmt.Errorf("cannot yield from outside a coroutine")
	}
	if nArgs < 0 {
		nArgs = 0
	}
	start := absSlot + 1
	end := start + nArgs
	if start < 0 || end > len(vm.regs) {
		return nil, fmt.Errorf("coroutine.yield args out of range")
	}
	return vm.regs[start:end], nil
}

func (vm *VM) handleCoroutineYieldFromSlots(absSlot, nArgs, c int) ([]rt.Value, error, bool) {
	args, err := vm.coroutineYieldBoundaryFromSlots(absSlot, nArgs)
	if err != nil {
		return nil, err, true
	}
	if vm.currentCoroutine != nil {
		return nil, vm.suspendCoroutine(args, absSlot, c), true
	}
	results, err := vm.yieldCoroutine(args)
	if err != nil {
		return nil, err, true
	}
	return results, nil, false
}

// coroutineResumeBoundaryFromSlots is the shared slot boundary for OP_RESUME
// and JIT op-exit resume handling. nArgs is the bytecode CALL/RESUME argument
// count after subtracting one from B, so it includes the coroutine operand.
func (vm *VM) coroutineResumeBoundaryFromSlots(absSlot, nArgs int) (*VMCoroutine, []rt.Value, error) {
	if vm == nil || absSlot < 0 || absSlot >= len(vm.regs) {
		return nil, nil, fmt.Errorf("coroutine.resume expects a coroutine")
	}
	if nArgs < 1 || absSlot+1 >= len(vm.regs) || !vm.regs[absSlot+1].IsCoroutine() {
		return nil, nil, fmt.Errorf("coroutine.resume expects a coroutine")
	}
	co, ok := vmCoroutineFromValue(vm.regs[absSlot+1])
	if !ok {
		return nil, nil, fmt.Errorf("coroutine.resume expects a VM coroutine")
	}
	if nArgs == 1 {
		return co, nil, nil
	}
	start := absSlot + 2
	end := start + nArgs - 1
	if end > len(vm.regs) {
		return nil, nil, fmt.Errorf("coroutine.resume args out of range")
	}
	return co, vm.regs[start:end], nil
}

// SaveMethodJITContinuation records where the active coroutine should re-enter
// native code after the current yield is resumed.
func (vm *VM) SaveMethodJITContinuation(cont MethodJITContinuation) error {
	co := vm.activeCoroutine()
	if co == nil {
		return fmt.Errorf("cannot save JIT continuation outside a coroutine")
	}
	if err := vm.SetCurrentFramePC(cont.PC); err != nil {
		return err
	}
	if co.wrapped {
		co.jitContinuation = MethodJITContinuation{}
		co.hasJITContinuation = false
		return nil
	}
	co.jitContinuation = cont
	co.hasJITContinuation = true
	return nil
}

// SaveMethodJITFastContinuation records raw Tier 1 continuation state used by
// experimental native coroutine switching. The raw fields intentionally contain
// only uintptr/int data so native code does not write Go interfaces or slices.
func (vm *VM) SaveMethodJITFastContinuation(code, ctx uintptr, resumePC int) error {
	co := vm.activeCoroutine()
	if co == nil {
		return fmt.Errorf("cannot save JIT fast continuation outside a coroutine")
	}
	if co.wrapped {
		return nil
	}
	co.fastJITCode = code
	co.fastJITCtx = ctx
	co.fastJITResumePC = resumePC
	return nil
}

func VMCoroutineStatusOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.status))
}

func VMCoroutineStartedOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.started))
}

func VMCoroutineStackYieldEnabledOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.stackYieldEnabled))
}

func VMCoroutinePooledFixedRecordOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.pooledFixedRecord))
}

func VMCoroutineYieldDstOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.yieldDst))
}

func VMCoroutineYieldCOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.yieldC))
}

func VMCoroutineHasJITContinuationOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.hasJITContinuation))
}

func VMCoroutineFastJITCodeOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.fastJITCode))
}

func VMCoroutineFastJITCtxOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.fastJITCtx))
}

func VMCoroutineFastJITResumePCOffset() int {
	var co VMCoroutine
	return int(unsafe.Offsetof(co.fastJITResumePC))
}

// NewObjectNFromSlots is the JIT-facing form of OP_NEWOBJECTN. It shares the
// interpreter's fixed-record coroutine payload path instead of duplicating table
// construction policy in the JIT handler.
func (vm *VM) NewObjectNFromSlots(proto *FuncProto, ctorIdx, absDst, absStart int) error {
	if vm == nil || proto == nil || absDst < 0 || absDst >= len(vm.regs) {
		return nil
	}
	if ctorIdx < 0 || ctorIdx >= len(proto.TableCtorsN) {
		vm.regs[absDst] = rt.FreshTableValue(rt.NewEmptyTable())
		return nil
	}
	ctor := &proto.TableCtorsN[ctorIdx].Runtime
	n := len(ctor.Keys)
	if absStart < 0 || absStart+n > len(vm.regs) {
		vm.regs[absDst] = rt.FreshTableValue(rt.NewTableSized(0, n))
		return nil
	}
	vals := vm.regs[absStart : absStart+n]
	if co := vm.currentCoroutine; co != nil {
		if co.stackYieldEnabled {
			if co.pooledFixedRecord == nil {
				if rt.DefaultHeap != nil {
					co.pooledFixedRecord = rt.DefaultHeap.AllocFixedRecord()
				} else {
					co.pooledFixedRecord = &rt.FixedRecord{}
				}
			}
			if v, ok := rt.FillFixedRecordKnownCtor(co.pooledFixedRecord, ctor, vals); ok {
				vm.regs[absDst] = v
				return nil
			}
		}
		if v, ok := rt.NewFixedRecordValue(ctor, vals); ok {
			vm.regs[absDst] = v
			return nil
		}
	}
	vm.regs[absDst] = rt.FreshTableValue(rt.NewTableFromCtorN(ctor, vals))
	return nil
}

func protoHasNoCalls(proto *FuncProto) bool {
	for _, inst := range proto.Code {
		switch DecodeOp(inst) {
		case OP_CALL, OP_YIELD, OP_RESUME, OP_TFORCALL, OP_GO:
			return false
		}
	}
	return true
}

func (vm *VM) coroutineResumeResults(ok bool, values []rt.Value) []rt.Value {
	n := 1 + len(values)
	if n <= len(vm.coroutineResultBuf) {
		out := vm.coroutineResultBuf[:n]
		out[0] = rt.BoolValue(ok)
		copy(out[1:], values)
		return out
	}
	out := make([]rt.Value, n)
	out[0] = rt.BoolValue(ok)
	copy(out[1:], values)
	return out
}

func (vm *VM) callLeafCoroutine(co *VMCoroutine, args []rt.Value) ([]rt.Value, error, bool) {
	cl := co.closure
	if cl == nil || cl.Proto == nil {
		return nil, nil, false
	}

	base := vm.top
	if vm.frameCount > 0 {
		curFrame := &vm.frames[vm.frameCount-1]
		minBase := curFrame.base + curFrame.closure.Proto.MaxStack
		if base < minBase {
			base = minBase
		}
	}
	if base+cl.Proto.MaxStack+1 > len(vm.regs) {
		return nil, nil, false
	}

	savedTop := vm.top
	savedJIT := vm.methodJIT
	vm.methodJIT = nil
	defer func() {
		vm.methodJIT = savedJIT
		vm.top = savedTop
	}()

	results, err := vm.call(cl, args, base, 0)
	return results, err, true
}

func (vm *VM) evalLeafCoroutineExpression(co *VMCoroutine, args []rt.Value) ([]rt.Value, error, bool) {
	if co == nil {
		return nil, nil, false
	}
	return vm.evalLeafClosureExpression(co.closure, args)
}

func (vm *VM) evalLeafClosureExpression(cl *Closure, args []rt.Value) ([]rt.Value, error, bool) {
	if cl == nil || cl.Proto == nil || cl.Proto.IsVarArg {
		return nil, nil, false
	}
	proto := cl.Proto
	if proto.NumParams > len(args) || len(proto.Code) < 2 || len(proto.Code) > 8 {
		return nil, nil, false
	}
	if proto.MaxStack > 16 {
		return nil, nil, false
	}
	var values [16]rt.Value
	var valid uint32
	for i := 0; i < proto.NumParams; i++ {
		values[i] = args[i]
		valid |= 1 << uint(i)
	}
	for pc, inst := range proto.Code {
		op := DecodeOp(inst)
		if op == OP_RETURN {
			if pc != len(proto.Code)-1 {
				return nil, nil, false
			}
			a := DecodeA(inst)
			b := DecodeB(inst)
			if b == 1 {
				return nil, nil, true
			}
			if b != 2 {
				return nil, nil, false
			}
			if valid&(1<<uint(a)) == 0 {
				return nil, nil, false
			}
			return rt.ReuseValueSlice1(vm.retBuf[:0], values[a]), nil, true
		}
		a := DecodeA(inst)
		switch op {
		case OP_MOVE:
			src := DecodeB(inst)
			if valid&(1<<uint(src)) == 0 {
				return nil, nil, false
			}
			values[a] = values[src]
			valid |= 1 << uint(a)
		case OP_LOADINT:
			values[a] = rt.IntValue(int64(DecodesBx(inst)))
			valid |= 1 << uint(a)
		case OP_GETUPVAL:
			uv := DecodeB(inst)
			if uv < 0 || uv >= len(cl.Upvalues) || cl.Upvalues[uv] == nil {
				return nil, nil, false
			}
			values[a] = cl.Upvalues[uv].Get()
			valid |= 1 << uint(a)
		case OP_ADD, OP_MUL:
			leftReg := DecodeB(inst)
			rightReg := DecodeC(inst)
			if valid&(1<<uint(leftReg)) == 0 {
				return nil, nil, false
			}
			if valid&(1<<uint(rightReg)) == 0 {
				return nil, nil, false
			}
			left := values[leftReg]
			right := values[rightReg]
			var out rt.Value
			switch op {
			case OP_ADD:
				if !rt.AddNums(&out, &left, &right) {
					return nil, nil, false
				}
			case OP_MUL:
				if !rt.MulNums(&out, &left, &right) {
					return nil, nil, false
				}
			}
			values[a] = out
			valid |= 1 << uint(a)
		default:
			return nil, nil, false
		}
	}
	return nil, nil, false
}

func (vm *VM) TryResumeLeafClosureToSlots(cl *Closure, args []rt.Value, dst, c int) (bool, error) {
	if vm == nil || cl == nil || cl.Proto == nil || !protoHasNoCalls(cl.Proto) {
		return false, nil
	}
	results, err, ok := vm.evalLeafClosureExpression(cl, args)
	if !ok {
		return false, nil
	}
	if err != nil {
		vm.writeCoroutineResumeResults(dst, c, false, []rt.Value{rt.StringValue(err.Error())})
		return true, nil
	}
	vm.writeCoroutineResumeResults(dst, c, true, results)
	return true, nil
}

// TryFastCoroutineCallValue handles VM-owned coroutine builtins directly from
// JIT call-exit handlers. absSlot is the absolute VM register slot containing
// the function value; c is the bytecode CALL C field.
func (vm *VM) TryFastCoroutineCallValue(fnVal rt.Value, absSlot, nArgs, c int) (bool, error) {
	if vm == nil || !fnVal.IsFunction() {
		return false, nil
	}
	gf := fnVal.GoFunction()
	if gf == nil {
		return false, nil
	}
	handled, err := vm.tryFastCoroutineCall(gf, 0, absSlot, nArgs, c)
	if vm.coroutineYielded {
		return handled, errCoroutineYield
	}
	return handled, err
}

func (vm *VM) ResumeCoroutineFromSlots(absSlot, nArgs, c int, payloadFieldOnly bool) error {
	co, args, err := vm.coroutineResumeBoundaryFromSlots(absSlot, nArgs)
	if err != nil {
		return err
	}
	co.stackYieldEnabled = payloadFieldOnly
	okResult, values, err := vm.resumeCoroutineRaw(co, args)
	if err != nil {
		return err
	}
	vm.finishCoroutineResumeToSlots(absSlot, c, okResult, values)
	return nil
}

func (vm *VM) finishCoroutineResumeToSlots(dst, c int, ok bool, values []rt.Value) {
	vm.writeCoroutineResumeResults(dst, c, ok, values)
}
