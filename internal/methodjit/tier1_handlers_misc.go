//go:build darwin && arm64

// tier1_handlers_misc.go contains the remaining Tier 1 baseline JIT exit handlers
// for less common operations: string concatenation, length, closures, upvalues,
// self/method calls, varargs, generic for loops, and power.

package methodjit

import (
	"fmt"
	"math"
	"strings"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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
	// Capture upvalues.
	var parentCl *vm.Closure
	if e.callVM != nil {
		parentCl = e.callVM.CurrentClosure()
	}
	for i, desc := range subProto.Upvalues {
		if desc.InStack {
			absIdx := base + desc.Index
			if absIdx < len(regs) {
				uv := vm.NewOpenUpvalue(&regs[absIdx], absIdx)
				if e.callVM != nil {
					uv = e.callVM.FindOrCreateUpvalue(absIdx)
				}
				cl.Upvalues[i] = uv
			}
		} else {
			// Parent upvalue: copy from the parent closure's upvalue list.
			if parentCl != nil && desc.Index < len(parentCl.Upvalues) && parentCl.Upvalues[desc.Index] != nil {
				cl.Upvalues[i] = parentCl.Upvalues[desc.Index]
			} else {
				cl.Upvalues[i] = vm.NewOpenUpvalue(new(runtime.Value), 0)
			}
		}
	}
	absA := base + a
	if absA < len(regs) {
		regs[absA] = runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)
	}
	return nil
}

// handleClose handles OP_CLOSE exit: close upvalues >= R(A).
func (e *BaselineJITEngine) handleClose(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return nil
	}
	a := int(ctx.BaselineA)
	e.callVM.CloseUpvalues(base + a)
	return nil
}

// handleGetUpval handles OP_GETUPVAL exit: R(A) = Upvalues[B].Get()
func (e *BaselineJITEngine) handleGetUpval(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for GETUPVAL")
	}
	cl := e.callVM.CurrentClosure()
	if cl == nil {
		return fmt.Errorf("GETUPVAL: no current closure")
	}
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	if b >= len(cl.Upvalues) || cl.Upvalues[b] == nil {
		return fmt.Errorf("GETUPVAL: upvalue %d out of range", b)
	}
	absA := base + a
	if absA < len(regs) {
		regs[absA] = cl.Upvalues[b].Get()
	}
	return nil
}

// handleSetUpval handles OP_SETUPVAL exit: Upvalues[B].Set(R(A))
func (e *BaselineJITEngine) handleSetUpval(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for SETUPVAL")
	}
	cl := e.callVM.CurrentClosure()
	if cl == nil {
		return fmt.Errorf("SETUPVAL: no current closure")
	}
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	if b >= len(cl.Upvalues) || cl.Upvalues[b] == nil {
		return fmt.Errorf("SETUPVAL: upvalue %d out of range", b)
	}
	absA := base + a
	if absA < len(regs) {
		cl.Upvalues[b].Set(regs[absA])
	}
	return nil
}

// handleSelf handles OP_SELF exit: R(A+1) = R(B); R(A) = R(B)[RK(C)]
// When the key is a constant string (the common case for method calls),
// uses the cached path to populate FieldCache for the native inline cache.
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

	if obj.IsTable() {
		tbl := obj.Table()
		// For constant string keys (the common case: method names), use cached lookup.
		if c >= vm.RKBit {
			key := proto.Constants[c-vm.RKBit]
			if key.IsString() {
				pc := int(ctx.BaselinePC) - 1
				ensureFieldCache(proto)
				regs[absA] = tbl.RawGetStringCached(key.Str(), &proto.FieldCache[pc])
				return nil
			}
			regs[absA] = tbl.RawGet(key)
		} else {
			absC := base + c
			if absC < len(regs) {
				regs[absA] = tbl.RawGet(regs[absC])
			}
		}
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
