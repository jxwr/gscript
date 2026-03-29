//go:build darwin && arm64

// tier1_manager.go manages the Tier 1 baseline JIT engine.
// It implements the vm.MethodJITEngine interface, compiling functions
// to native ARM64 code using the baseline compiler (no SSA, no optimization).
//
// The execution loop uses exit-resume: when the JIT encounters an operation
// it cannot handle natively (calls, globals, tables, etc.), it exits to Go
// with a descriptor in ExecContext. Go performs the operation, then re-enters
// the JIT at the resume point following the exit.
//
// Flow:
//  1. TryCompile: if call count >= threshold, compile via CompileBaseline.
//  2. Execute: enter JIT code. On exit, handle the operation, resume.
//  3. On normal return: read result from regs[0] and return.

package methodjit

import (
	"fmt"
	"math"
	"strings"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// BaselineCompileThreshold is the number of calls before baseline compilation.
// Set low (2) for fast startup. Tier 2 takes over at 100+ calls.
const BaselineCompileThreshold = 2

// BaselineJITEngine implements vm.MethodJITEngine for the Tier 1 baseline compiler.
type BaselineJITEngine struct {
	compiled map[*vm.FuncProto]*BaselineFunc
	failed   map[*vm.FuncProto]bool
	callVM   *vm.VM
}

// NewBaselineJITEngine creates a new baseline JIT engine.
func NewBaselineJITEngine() *BaselineJITEngine {
	return &BaselineJITEngine{
		compiled: make(map[*vm.FuncProto]*BaselineFunc),
		failed:   make(map[*vm.FuncProto]bool),
	}
}

// SetCallVM sets the VM used for exit-resume during JIT execution.
func (e *BaselineJITEngine) SetCallVM(v *vm.VM) {
	e.callVM = v
}

// TryCompile checks if a function should be baseline-compiled.
// Returns the compiled function (as interface{}) if available, nil if not ready.
func (e *BaselineJITEngine) TryCompile(proto *vm.FuncProto) interface{} {
	if bf, ok := e.compiled[proto]; ok {
		return bf
	}
	if e.failed[proto] {
		return nil
	}
	if proto.CallCount < BaselineCompileThreshold {
		return nil
	}

	bf, err := CompileBaseline(proto)
	if err != nil {
		e.failed[proto] = true
		return nil
	}
	e.compiled[proto] = bf
	return bf
}

// Execute runs a baseline-compiled function using the VM's register file.
// Arguments are already in regs[base..base+numParams-1].
func (e *BaselineJITEngine) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	bf := compiled.(*BaselineFunc)

	// Ensure register space.
	needed := base + proto.MaxStack + 1
	if needed > len(regs) {
		return nil, fmt.Errorf("baseline: register file too small: need %d, have %d", needed, len(regs))
	}

	// Initialize unused registers to nil.
	for i := base + proto.NumParams; i < base+proto.MaxStack; i++ {
		if i < len(regs) {
			regs[i] = runtime.NilValue()
		}
	}

	// Set up ExecContext. Heap-allocate for stability across Go stack growth.
	ctx := new(ExecContext)
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	codePtr := uintptr(bf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))

	// resyncRegs re-reads the VM's register file after exits.
	resyncRegs := func() {
		if e.callVM != nil {
			regs = e.callVM.Regs()
			ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
		}
	}

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			// Normal return: result is in regs[base] (slot 0).
			result := regs[base]
			return []runtime.Value{result}, nil

		case ExitBaselineOpExit:
			// Baseline op-exit: handle operation via Go, then resume.
			if err := e.handleBaselineOpExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("baseline: op-exit: %w", err)
			}
			resyncRegs()

			// Resume at the next bytecode PC.
			resumePC := int(ctx.BaselinePC)
			resumeOff, ok := bf.Labels[resumePC]
			if !ok {
				return nil, fmt.Errorf("baseline: no resume label for PC %d", resumePC)
			}
			codePtr = uintptr(bf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("baseline: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// handleBaselineOpExit dispatches a baseline op-exit to the appropriate handler.
func (e *BaselineJITEngine) handleBaselineOpExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	opCode := vm.Opcode(ctx.BaselineOp)
	switch opCode {
	case vm.OP_CALL:
		return e.handleCall(ctx, regs, base, proto)
	case vm.OP_GETGLOBAL:
		return e.handleGetGlobal(ctx, regs, base, proto)
	case vm.OP_SETGLOBAL:
		return e.handleSetGlobal(ctx, regs, base, proto)
	case vm.OP_NEWTABLE:
		return e.handleNewTable(ctx, regs, base, proto)
	case vm.OP_GETTABLE:
		return e.handleGetTable(ctx, regs, base, proto)
	case vm.OP_SETTABLE:
		return e.handleSetTable(ctx, regs, base, proto)
	case vm.OP_GETFIELD:
		return e.handleGetField(ctx, regs, base, proto)
	case vm.OP_SETFIELD:
		return e.handleSetField(ctx, regs, base, proto)
	case vm.OP_SETLIST:
		return e.handleSetList(ctx, regs, base, proto)
	case vm.OP_APPEND:
		return e.handleAppend(ctx, regs, base, proto)
	case vm.OP_CONCAT:
		return e.handleConcat(ctx, regs, base, proto)
	case vm.OP_LEN:
		return e.handleLen(ctx, regs, base, proto)
	case vm.OP_CLOSURE:
		return e.handleClosure(ctx, regs, base, proto)
	case vm.OP_CLOSE:
		return e.handleClose(ctx, regs, base, proto)
	case vm.OP_GETUPVAL:
		return e.handleGetUpval(ctx, regs, base, proto)
	case vm.OP_SETUPVAL:
		return e.handleSetUpval(ctx, regs, base, proto)
	case vm.OP_SELF:
		return e.handleSelf(ctx, regs, base, proto)
	case vm.OP_VARARG:
		return e.handleVararg(ctx, regs, base, proto)
	case vm.OP_TFORCALL:
		return e.handleTForCall(ctx, regs, base, proto)
	case vm.OP_TFORLOOP:
		return e.handleTForLoop(ctx, regs, base, proto)
	case vm.OP_POW:
		return e.handlePow(ctx, regs, base, proto)
	default:
		return fmt.Errorf("unhandled baseline op-exit: %s (%d)", vm.OpName(opCode), opCode)
	}
}

// handleCall handles OP_CALL exit: execute the function call via the VM.
// BaselineB and BaselineC are the raw B and C fields from the instruction:
//   B=0: variable args (use vm.top), else nArgs=B-1
//   C=0: return all values, C=1: no results, else nRets=C-1
func (e *BaselineJITEngine) handleCall(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for call-exit")
	}
	callSlot := int(ctx.BaselineA)
	rawB := int(ctx.BaselineB)
	rawC := int(ctx.BaselineC)

	absSlot := base + callSlot
	if absSlot >= len(regs) {
		return fmt.Errorf("call slot %d out of range", absSlot)
	}
	fnVal := regs[absSlot]

	// Determine number of arguments.
	var nArgs int
	if rawB == 0 {
		// Variable args: nArgs = vm.top - (absSlot + 1).
		// The vm.top was set by a previous CALL with C=0.
		top := e.callVM.Top()
		nArgs = top - (absSlot + 1)
		if nArgs < 0 {
			nArgs = 0
		}
	} else {
		nArgs = rawB - 1
	}

	// Collect arguments.
	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := absSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	results, err := e.callVM.CallValue(fnVal, callArgs)
	if err != nil {
		return err
	}

	// Re-read regs in case the callee grew the register file.
	currentRegs := e.callVM.Regs()

	// Place results: overwrite starting from the function slot.
	if rawC == 0 {
		// Return all values. Write all results and update vm.top.
		for i, r := range results {
			idx := absSlot + i
			if idx < len(currentRegs) {
				currentRegs[idx] = r
			}
		}
		e.callVM.SetTop(absSlot + len(results))
	} else {
		// Fixed number of results.
		nr := rawC - 1
		for i := 0; i < nr; i++ {
			idx := absSlot + i
			if idx < len(currentRegs) {
				if i < len(results) {
					currentRegs[idx] = results[i]
				} else {
					currentRegs[idx] = runtime.NilValue()
				}
			}
		}
	}
	return nil
}

// handleGetGlobal handles OP_GETGLOBAL exit.
func (e *BaselineJITEngine) handleGetGlobal(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for global-exit")
	}
	a := int(ctx.BaselineA)
	bx := int(ctx.BaselineB)
	if bx >= len(proto.Constants) {
		return fmt.Errorf("global const index %d out of range", bx)
	}
	name := proto.Constants[bx].Str()
	val := e.callVM.GetGlobal(name)
	absSlot := base + a
	if absSlot < len(regs) {
		regs[absSlot] = val
	}
	return nil
}

// handleSetGlobal handles OP_SETGLOBAL exit.
func (e *BaselineJITEngine) handleSetGlobal(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for setglobal-exit")
	}
	a := int(ctx.BaselineA)
	bx := int(ctx.BaselineB)
	if bx >= len(proto.Constants) {
		return fmt.Errorf("setglobal const index %d out of range", bx)
	}
	name := proto.Constants[bx].Str()
	absSlot := base + a
	if absSlot < len(regs) {
		e.callVM.SetGlobal(name, regs[absSlot])
	}
	return nil
}

// handleNewTable handles OP_NEWTABLE exit.
func (e *BaselineJITEngine) handleNewTable(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // array hint
	c := int(ctx.BaselineC) // hash hint
	absSlot := base + a
	tbl := runtime.NewTableSized(b, c)
	if absSlot < len(regs) {
		regs[absSlot] = runtime.TableValue(tbl)
	}
	return nil
}

// handleGetTable handles OP_GETTABLE exit.
func (e *BaselineJITEngine) handleGetTable(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC)

	absB := base + b
	if absB >= len(regs) {
		return nil
	}
	tblVal := regs[absB]

	// Resolve RK(C)
	var key runtime.Value
	if c >= vm.RKBit {
		key = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			key = regs[absC]
		}
	}

	absA := base + a
	if tblVal.IsTable() {
		if absA < len(regs) {
			regs[absA] = tblVal.Table().RawGet(key)
		}
	} else if absA < len(regs) {
		regs[absA] = runtime.NilValue()
	}
	return nil
}

// handleSetTable handles OP_SETTABLE exit.
func (e *BaselineJITEngine) handleSetTable(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC)

	absA := base + a
	if absA >= len(regs) {
		return nil
	}
	tblVal := regs[absA]

	// Resolve RK(B) = key
	var key runtime.Value
	if b >= vm.RKBit {
		key = proto.Constants[b-vm.RKBit]
	} else {
		absB := base + b
		if absB < len(regs) {
			key = regs[absB]
		}
	}

	// Resolve RK(C) = value
	var val runtime.Value
	if c >= vm.RKBit {
		val = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			val = regs[absC]
		}
	}

	if tblVal.IsTable() {
		tblVal.Table().RawSet(key, val)
	}
	return nil
}

// handleGetField handles OP_GETFIELD exit: R(A) = R(B).Constants[Bx]
func (e *BaselineJITEngine) handleGetField(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC) // constant index for field name

	absB := base + b
	absA := base + a
	if absB >= len(regs) || absA >= len(regs) {
		return nil
	}
	tblVal := regs[absB]
	if c >= len(proto.Constants) {
		return nil
	}
	fieldName := proto.Constants[c].Str()

	if tblVal.IsTable() {
		regs[absA] = tblVal.Table().RawGetString(fieldName)
	} else {
		regs[absA] = runtime.NilValue()
	}
	return nil
}

// handleSetField handles OP_SETFIELD exit: R(A).Constants[Bx] = RK(C)
func (e *BaselineJITEngine) handleSetField(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // constant index for field name
	c := int(ctx.BaselineC) // RK(C) = value

	absA := base + a
	if absA >= len(regs) || b >= len(proto.Constants) {
		return nil
	}
	tblVal := regs[absA]
	fieldName := proto.Constants[b].Str()

	// Resolve RK(C) = value
	var val runtime.Value
	if c >= vm.RKBit {
		val = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			val = regs[absC]
		}
	}

	if tblVal.IsTable() {
		tblVal.Table().RawSetString(fieldName, val)
	}
	return nil
}

// handleSetList handles OP_SETLIST exit.
func (e *BaselineJITEngine) handleSetList(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // count
	c := int(ctx.BaselineC) // block

	absA := base + a
	if absA >= len(regs) {
		return nil
	}
	tblVal := regs[absA]
	if !tblVal.IsTable() {
		return fmt.Errorf("SETLIST on non-table")
	}
	tbl := tblVal.Table()
	offset := (c - 1) * 50
	for i := 1; i <= b; i++ {
		idx := absA + i
		if idx < len(regs) {
			tbl.RawSetInt(int64(offset+i), regs[idx])
		}
	}
	return nil
}

// handleAppend handles OP_APPEND exit.
func (e *BaselineJITEngine) handleAppend(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	absA := base + a
	absB := base + b
	if absA >= len(regs) || absB >= len(regs) {
		return nil
	}
	tblVal := regs[absA]
	if tblVal.IsTable() {
		tblVal.Table().Append(regs[absB])
	}
	return nil
}

// handleConcat handles OP_CONCAT exit: R(A) = R(B)..R(B+1)..R(C)
func (e *BaselineJITEngine) handleConcat(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // start register
	c := int(ctx.BaselineC) // end register

	absA := base + a
	if absA >= len(regs) {
		return nil
	}
	var sb strings.Builder
	for i := b; i <= c; i++ {
		absI := base + i
		if absI < len(regs) {
			sb.WriteString(regs[absI].String())
		}
	}
	regs[absA] = runtime.StringValue(sb.String())
	return nil
}

// handleLen handles OP_LEN exit: R(A) = #R(B)
func (e *BaselineJITEngine) handleLen(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	absA := base + a
	absB := base + b
	if absA >= len(regs) || absB >= len(regs) {
		return nil
	}
	v := regs[absB]
	if v.IsTable() {
		regs[absA] = runtime.IntValue(int64(v.Table().Len()))
	} else if v.IsString() {
		regs[absA] = runtime.IntValue(int64(len(v.Str())))
	} else {
		regs[absA] = runtime.IntValue(0)
	}
	return nil
}

// handleClosure handles OP_CLOSURE exit.
// This is complex: needs the parent closure's upvalues. For Tier 1 baseline,
// we exit to Go which creates the closure using the VM.
func (e *BaselineJITEngine) handleClosure(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	bx := int(ctx.BaselineB)
	if bx >= len(proto.Protos) {
		return fmt.Errorf("closure proto index %d out of range", bx)
	}
	subProto := proto.Protos[bx]
	cl := &vm.Closure{
		Proto:    subProto,
		Upvalues: make([]*vm.Upvalue, len(subProto.Upvalues)),
	}
	// Capture upvalues. For baseline JIT, upvalues InStack reference
	// the current frame's registers at regs[base+index].
	for i, desc := range subProto.Upvalues {
		if desc.InStack {
			absIdx := base + desc.Index
			if absIdx < len(regs) {
				cl.Upvalues[i] = vm.NewOpenUpvalue(&regs[absIdx], absIdx)
			}
		} else {
			// Parent upvalue: we don't have access to the parent closure's
			// upvalues in the baseline JIT. Store nil for now.
			// This works for simple closures where all upvalues are InStack.
			cl.Upvalues[i] = vm.NewOpenUpvalue(new(runtime.Value), 0)
		}
	}
	absA := base + a
	if absA < len(regs) {
		regs[absA] = runtime.FunctionValue(cl)
	}
	return nil
}

// handleClose handles OP_CLOSE exit: close upvalues >= R(A).
func (e *BaselineJITEngine) handleClose(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	// Closing upvalues is handled by the VM's closeUpvalues.
	// In the baseline JIT, we don't track open upvalues, so this is a no-op.
	return nil
}

// handleGetUpval handles OP_GETUPVAL exit.
func (e *BaselineJITEngine) handleGetUpval(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	return fmt.Errorf("GETUPVAL not supported in baseline JIT (needs closure)")
}

// handleSetUpval handles OP_SETUPVAL exit.
func (e *BaselineJITEngine) handleSetUpval(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	return fmt.Errorf("SETUPVAL not supported in baseline JIT (needs closure)")
}

// handleSelf handles OP_SELF exit: R(A+1) = R(B); R(A) = R(B)[RK(C)]
func (e *BaselineJITEngine) handleSelf(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC)

	absA := base + a
	absB := base + b
	if absA+1 >= len(regs) || absB >= len(regs) {
		return nil
	}

	obj := regs[absB]
	regs[absA+1] = obj

	var key runtime.Value
	if c >= vm.RKBit {
		key = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			key = regs[absC]
		}
	}

	if obj.IsTable() {
		regs[absA] = obj.Table().RawGet(key)
	} else {
		regs[absA] = runtime.NilValue()
	}
	return nil
}

// handleVararg handles OP_VARARG exit.
func (e *BaselineJITEngine) handleVararg(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	return fmt.Errorf("VARARG not supported in baseline JIT")
}

// handleTForCall handles OP_TFORCALL exit.
func (e *BaselineJITEngine) handleTForCall(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for TFORCALL")
	}
	a := int(ctx.BaselineA)
	c := int(ctx.BaselineC)

	absA := base + a
	if absA+2 >= len(regs) {
		return nil
	}
	fnVal := regs[absA]
	args := []runtime.Value{regs[absA+1], regs[absA+2]}
	results, err := e.callVM.CallValue(fnVal, args)
	if err != nil {
		return err
	}
	for i := 0; i < c; i++ {
		idx := absA + 3 + i
		if idx < len(regs) {
			if i < len(results) {
				regs[idx] = results[i]
			} else {
				regs[idx] = runtime.NilValue()
			}
		}
	}
	return nil
}

// handleTForLoop handles OP_TFORLOOP exit.
func (e *BaselineJITEngine) handleTForLoop(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	// TFORLOOP is handled natively (compare + branch). Should not reach here.
	return fmt.Errorf("TFORLOOP should not op-exit")
}

// handlePow handles OP_POW exit.
func (e *BaselineJITEngine) handlePow(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC)
	absA := base + a

	var bv, cv runtime.Value
	if b >= vm.RKBit {
		bv = proto.Constants[b-vm.RKBit]
	} else {
		bv = regs[base+b]
	}
	if c >= vm.RKBit {
		cv = proto.Constants[c-vm.RKBit]
	} else {
		cv = regs[base+c]
	}

	var baseF, expF float64
	if bv.IsInt() {
		baseF = float64(bv.Int())
	} else {
		baseF = bv.Float()
	}
	if cv.IsInt() {
		expF = float64(cv.Int())
	} else {
		expF = cv.Float()
	}

	if absA < len(regs) {
		regs[absA] = runtime.FloatValue(math.Pow(baseF, expF))
	}
	return nil
}

// CompiledCount returns the number of compiled functions.
func (e *BaselineJITEngine) CompiledCount() int {
	return len(e.compiled)
}
