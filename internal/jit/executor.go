//go:build darwin && arm64

package jit

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// JIT compilation threshold: compile after this many calls.
const DefaultHotThreshold = 10

// compiledEntry holds a compiled function.
type compiledEntry struct {
	cf           *CompiledFunc
	fn           func(uintptr) int64 // purego wrapper (kept for backward compat)
	ptr          uintptr              // direct code pointer for callJIT trampoline
	totalExits   int                  // cumulative call-exit count
	totalCalls   int                  // cumulative execution count
	demoted      bool                 // true if function has been demoted (too many exits)
}

// CallHandler executes an external function call on behalf of JIT code.
// Provided by the VM so the JIT executor can handle OP_CALL without direct VM access.
type CallHandler func(fnVal rt.Value, args []rt.Value) ([]rt.Value, error)

// GlobalsAccessor provides safe access to VM globals and register state.
type GlobalsAccessor interface {
	GetGlobal(name string) rt.Value
	SetGlobal(name string, val rt.Value)
	Regs() []rt.Value // returns the current register slice (may change after calls)
}

// Engine manages JIT compilation and execution.
type Engine struct {
	entries     map[*vm.FuncProto]*compiledEntry
	blacklist   map[*vm.FuncProto]bool // functions known to not benefit from JIT
	threshold   int
	globals     map[string]rt.Value // reference to VM globals for function inlining
	callHandler CallHandler         // executes external function calls
	globalsAcc  GlobalsAccessor     // safe globals/regs access
}

// NewEngine creates a new JIT engine.
func NewEngine() *Engine {
	return &Engine{
		entries:   make(map[*vm.FuncProto]*compiledEntry),
		blacklist: make(map[*vm.FuncProto]bool),
		threshold: DefaultHotThreshold,
	}
}

// SetGlobals sets the globals map for function inlining.
func (e *Engine) SetGlobals(globals map[string]rt.Value) {
	e.globals = globals
}

// SetThreshold sets the call count threshold for JIT compilation.
func (e *Engine) SetThreshold(n int) {
	e.threshold = n
}

// SetCallHandler sets the function that executes external calls for the JIT.
func (e *Engine) SetCallHandler(handler CallHandler) {
	e.callHandler = handler
}

// SetGlobalsAccessor sets the globals/regs accessor for call-exit handling.
func (e *Engine) SetGlobalsAccessor(acc GlobalsAccessor) {
	e.globalsAcc = acc
}

// shouldCompile checks if a function is worth JIT compiling.
// With call-exit support, functions with external calls are now viable:
// the JIT exits at call sites, the executor handles the call, then re-enters JIT.
// Only TFORCALL (generic for iterator) is still a permanent side-exit concern.
func shouldCompile(proto *vm.FuncProto) bool {
	if len(proto.Code) == 0 {
		return false
	}
	hasLoop := false
	hasTForCall := false
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_FORLOOP {
			hasLoop = true
		}
		if op == vm.OP_TFORCALL {
			hasTForCall = true
		}
	}
	// TFORCALL (generic for) is still a permanent side-exit.
	// Only compile if there's a loop to offset the cost.
	if hasTForCall && !hasLoop {
		return false
	}
	return true
}

// isSelfCall checks whether the CALL at callPC is a self-recursive call
// by looking backward for a GETGLOBAL that loads the function's own name.
func isSelfCall(proto *vm.FuncProto, callPC int) bool {
	if proto.Name == "" {
		return false
	}
	callA := vm.DecodeA(proto.Code[callPC])
	for pc := callPC - 1; pc >= 0; pc-- {
		inst := proto.Code[pc]
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		if op == vm.OP_GETGLOBAL && a == callA {
			bx := vm.DecodeBx(inst)
			if bx < len(proto.Constants) {
				return proto.Constants[bx].Str() == proto.Name
			}
			return false
		}
		// If the register is written by another instruction, stop searching.
		if a == callA && op != vm.OP_EQ && op != vm.OP_LT && op != vm.OP_LE && op != vm.OP_TEST {
			return false
		}
	}
	return false
}

// TryExecute attempts to JIT-execute a function.
// Returns (results, resumePC, ok).
// If ok is true, the function completed (results contains return values).
// If ok is false, the JIT bailed out at resumePC and the interpreter should take over.
func (e *Engine) TryExecute(proto *vm.FuncProto, regs []rt.Value, base int, callCount int) (results []rt.Value, resumePC int, ok bool) {
	// Check blacklist first.
	if e.blacklist[proto] {
		return nil, 0, false
	}

	// Check if already compiled (per-proto fast path).
	entry, compiled := e.entries[proto]
	if !compiled {
		// Check if hot enough to compile.
		if callCount < e.threshold {
			return nil, 0, false
		}
		// Check if worth compiling.
		if !shouldCompile(proto) {
			e.blacklist[proto] = true
			return nil, 0, false
		}
		// Compile the function.
		cf, err := CompileWithGlobals(proto, e.globals)
		if err != nil {
			e.blacklist[proto] = true
			return nil, 0, false
		}
		entry = &compiledEntry{cf: cf, ptr: uintptr(cf.Code.Ptr())}
		e.entries[proto] = entry
	}

	// Check if this function has been demoted due to excessive call-exits.
	if entry.demoted {
		return nil, 0, false
	}

	// Prepare JIT context.
	ctx := JITContext{
		Regs: uintptr(unsafe.Pointer(&regs[base])),
	}
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Exit-resume loop: JIT runs until normal return (0), permanent side-exit (1),
	// or call-exit (2). On call-exit, the executor handles the instruction and
	// re-enters JIT at the next PC.
	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	const debugCallExit = false
	exitCount := 0
	for {
		exitCode := callJIT(entry.ptr, ctxPtr)
		runtime.KeepAlive(ctx)

		if debugCallExit {
			op := vm.OpName(vm.DecodeOp(proto.Code[ctx.ExitPC]))
			fmt.Printf("[JIT] %s: exit=%d pc=%d(%s) resume=%d ptr=0x%x ctx=0x%x\n",
				proto.Name, exitCode, ctx.ExitPC, op, ctx.ResumePC, entry.ptr, ctxPtr)
		}

		switch exitCode {
		case 0:
			// Normal return. Update exit stats.
			entry.totalCalls++
			entry.totalExits += exitCount
			// After enough samples, demote if exit ratio is too high.
			// A high exit/call ratio means JIT is spending more time in
			// exit/re-enter overhead than executing native code.
			if entry.totalCalls >= 8 && entry.totalExits > entry.totalCalls*20 {
				entry.demoted = true
			}

			retBase := int(ctx.RetBase)
			retCount := int(ctx.RetCount)
			if retCount == 0 {
				return nil, 0, true
			}
			ret := make([]rt.Value, retCount)
			for i := 0; i < retCount; i++ {
				ret[i] = regs[base+retBase+i]
			}
			return ret, 0, true

		case 1:
			// Permanent side exit — interpreter takes over.
			return nil, int(ctx.ExitPC), false

		case 2:
			// Call-exit: handle the instruction in Go, then re-enter JIT.
			exitCount++
			newRegs, nextPC, err := e.handleCallExit(proto, regs, base, &ctx)
			if err != nil {
				// Error during call — fall back to interpreter at this PC.
				return nil, int(ctx.ExitPC), false
			}
			if newRegs != nil {
				regs = newRegs
			}

			// Set ResumePC so JIT dispatch table jumps to the right place.
			// Comparison call-exits (EQ/LT/LE) use high-bit flagging (nextPC | 0x8000)
			// to avoid dispatch collisions with regular call-exits that may share
			// the same nextPC value.
			exitOp := vm.DecodeOp(proto.Code[int(ctx.ExitPC)])
			if exitOp == vm.OP_EQ || exitOp == vm.OP_LT || exitOp == vm.OP_LE {
				ctx.ResumePC = int64(nextPC | 0x8000)
			} else {
				ctx.ResumePC = int64(nextPC)
			}
			ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
			// Recompute ctxPtr to ensure Go compiler hasn't moved ctx.
			ctxPtr = uintptr(unsafe.Pointer(&ctx))
			if debugCallExit {
				fmt.Printf("[JIT] %s: re-enter at ResumePC=%d (ctx.ResumePC=%d)\n",
					proto.Name, resumePC, ctx.ResumePC)
			}
			continue

		default:
			return nil, int(ctx.ExitPC), false
		}
	}
}

// handleCallExit handles a call-exit (ExitCode=2) by executing the instruction
// at ctx.ExitPC in Go and placing results back in the register array.
// Returns (updatedRegs, nextPC, error). updatedRegs is non-nil only if regs were
// reallocated. nextPC is the bytecode PC to resume at (usually ExitPC+1, but
// comparison ops may skip an instruction, returning ExitPC+2).
func (e *Engine) handleCallExit(proto *vm.FuncProto, regs []rt.Value, base int, ctx *JITContext) ([]rt.Value, int, error) {
	pc := int(ctx.ExitPC)
	if pc < 0 || pc >= len(proto.Code) {
		return nil, 0, fmt.Errorf("jit: call-exit PC %d out of range", pc)
	}
	nextPC := pc + 1

	inst := proto.Code[pc]
	op := vm.DecodeOp(inst)

	switch op {
	case vm.OP_GETGLOBAL:
		if e.globalsAcc == nil {
			return nil, 0, fmt.Errorf("jit: no globals accessor for GETGLOBAL")
		}
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		name := proto.Constants[bx].Str()
		val := e.globalsAcc.GetGlobal(name)
		regs[base+a] = val
		return nil, nextPC, nil

	case vm.OP_SETGLOBAL:
		if e.globalsAcc == nil {
			return nil, 0, fmt.Errorf("jit: no globals accessor for SETGLOBAL")
		}
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		name := proto.Constants[bx].Str()
		e.globalsAcc.SetGlobal(name, regs[base+a])
		return nil, nextPC, nil

	case vm.OP_CALL:
		if e.callHandler == nil {
			return nil, 0, fmt.Errorf("jit: no call handler for OP_CALL")
		}
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		// Variable args (b==0) or variable returns (c==0) — fall back.
		if b == 0 || c == 0 {
			return nil, 0, fmt.Errorf("jit: variable args/returns not supported in call-exit")
		}

		fnVal := regs[base+a]
		nArgs := b - 1
		args := make([]rt.Value, nArgs)
		for i := 0; i < nArgs; i++ {
			args[i] = regs[base+a+1+i]
		}

		callResults, err := e.callHandler(fnVal, args)
		if err != nil {
			return nil, 0, err
		}

		// Check if regs were reallocated during the call.
		var newRegs []rt.Value
		if e.globalsAcc != nil {
			latestRegs := e.globalsAcc.Regs()
			if &latestRegs[0] != &regs[0] {
				newRegs = latestRegs
				regs = latestRegs
			}
		}

		// Place results in registers.
		nResults := c - 1
		for i := 0; i < nResults; i++ {
			if i < len(callResults) {
				regs[base+a+i] = callResults[i]
			} else {
				regs[base+a+i] = rt.NilValue()
			}
		}

		return newRegs, nextPC, nil

	case vm.OP_GETTABLE:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+b]
		key := resolveRK(cidx, regs, base, proto.Constants)
		if !tableVal.IsTable() {
			return nil, 0, fmt.Errorf("jit: GETTABLE on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return nil, 0, fmt.Errorf("jit: GETTABLE metatable not supported")
		}
		regs[base+a] = tbl.RawGet(key)
		return nil, nextPC, nil

	case vm.OP_SETTABLE:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+a]
		key := resolveRK(bidx, regs, base, proto.Constants)
		val := resolveRK(cidx, regs, base, proto.Constants)
		if !tableVal.IsTable() {
			return nil, 0, fmt.Errorf("jit: SETTABLE on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return nil, 0, fmt.Errorf("jit: SETTABLE metatable not supported")
		}
		tbl.RawSet(key, val)
		return nil, nextPC, nil

	case vm.OP_GETFIELD:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		tableVal := regs[base+b]
		key := proto.Constants[c]
		if !tableVal.IsTable() {
			return nil, 0, fmt.Errorf("jit: GETFIELD on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return nil, 0, fmt.Errorf("jit: GETFIELD metatable not supported")
		}
		regs[base+a] = tbl.RawGet(key)
		return nil, nextPC, nil

	case vm.OP_SETFIELD:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+a]
		key := proto.Constants[bidx]
		val := resolveRK(cidx, regs, base, proto.Constants)
		if !tableVal.IsTable() {
			return nil, 0, fmt.Errorf("jit: SETFIELD on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return nil, 0, fmt.Errorf("jit: SETFIELD metatable not supported")
		}
		tbl.RawSet(key, val)
		return nil, nextPC, nil

	case vm.OP_NEWTABLE:
		a := vm.DecodeA(inst)
		regs[base+a] = rt.TableValue(rt.NewTable())
		return nil, nextPC, nil

	case vm.OP_SETLIST:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		tbl := regs[base+a].Table()
		offset := (c - 1) * 50
		for i := 1; i <= b; i++ {
			tbl.RawSet(rt.IntValue(int64(offset+i)), regs[base+a+i])
		}
		return nil, nextPC, nil

	case vm.OP_LEN:
		a := vm.DecodeA(inst)
		bv := regs[base+vm.DecodeB(inst)]
		if bv.IsString() {
			regs[base+a] = rt.IntValue(int64(len(bv.Str())))
		} else if bv.IsTable() {
			tbl := bv.Table()
			if tbl.GetMetatable() != nil {
				return nil, 0, fmt.Errorf("jit: LEN metatable not supported")
			}
			regs[base+a] = rt.IntValue(int64(tbl.Len()))
		} else {
			return nil, 0, fmt.Errorf("jit: LEN on non-table/string")
		}
		return nil, nextPC, nil

	case vm.OP_CONCAT:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		var sb strings.Builder
		for i := b; i <= c; i++ {
			sb.WriteString(regs[base+i].String())
		}
		regs[base+a] = rt.StringValue(sb.String())
		return nil, nextPC, nil

	case vm.OP_MOD:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, proto.Constants)
		cv := resolveRK(cidx, regs, base, proto.Constants)
		if bv.IsInt() && cv.IsInt() {
			bi := cv.Int()
			if bi == 0 {
				return nil, 0, fmt.Errorf("attempt to perform 'n%%0'")
			}
			r := bv.Int() % bi
			if r != 0 && (r^bi) < 0 {
				r += bi
			}
			regs[base+a] = rt.IntValue(r)
		} else if bv.IsNumber() && cv.IsNumber() {
			bf := cv.Number()
			if bf == 0 {
				return nil, 0, fmt.Errorf("attempt to perform 'n%%0'")
			}
			r := math.Mod(bv.Number(), bf)
			if r != 0 && (r < 0) != (bf < 0) {
				r += bf
			}
			regs[base+a] = rt.FloatValue(r)
		} else {
			return nil, 0, fmt.Errorf("jit: MOD metatable not supported")
		}
		return nil, nextPC, nil

	case vm.OP_DIV:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, proto.Constants)
		cv := resolveRK(cidx, regs, base, proto.Constants)
		bn := bv.Number()
		cn := cv.Number()
		if cn == 0 {
			return nil, 0, fmt.Errorf("attempt to divide by zero")
		}
		regs[base+a] = rt.FloatValue(bn / cn)
		return nil, nextPC, nil

	case vm.OP_SELF:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		obj := regs[base+b]
		key := resolveRK(cidx, regs, base, proto.Constants)
		regs[base+a+1] = obj // R(A+1) = R(B)
		if !obj.IsTable() {
			return nil, 0, fmt.Errorf("jit: SELF on non-table")
		}
		tbl := obj.Table()
		if tbl.GetMetatable() != nil {
			return nil, 0, fmt.Errorf("jit: SELF metatable not supported")
		}
		regs[base+a] = tbl.RawGet(key) // R(A) = R(B)[RK(C)]
		return nil, nextPC, nil

	case vm.OP_EQ:
		// Comparison call-exit: evaluate equality in Go for non-integer operands.
		aFlag := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, proto.Constants)
		cv := resolveRK(cidx, regs, base, proto.Constants)
		equal := bv.Equal(cv)
		// EQ semantics: if (equal) != bool(aFlag) then skip next instruction
		if equal != (aFlag != 0) {
			nextPC = pc + 2 // skip the JMP
		}
		return nil, nextPC, nil

	case vm.OP_LT:
		aFlag := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, proto.Constants)
		cv := resolveRK(cidx, regs, base, proto.Constants)
		less, ok := bv.LessThan(cv)
		if !ok {
			return nil, 0, fmt.Errorf("jit: LT on incomparable types")
		}
		if less != (aFlag != 0) {
			nextPC = pc + 2
		}
		return nil, nextPC, nil

	case vm.OP_LE:
		// LE is implemented as !(C < B) in the VM.
		aFlag := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, proto.Constants)
		cv := resolveRK(cidx, regs, base, proto.Constants)
		// a <= b  ⟺  !(b < a)
		less, ok := cv.LessThan(bv)
		if !ok {
			return nil, 0, fmt.Errorf("jit: LE on incomparable types")
		}
		lessEq := !less
		if lessEq != (aFlag != 0) {
			nextPC = pc + 2
		}
		return nil, nextPC, nil

	default:
		return nil, 0, fmt.Errorf("jit: unsupported call-exit opcode %s", vm.OpName(op))
	}
}

// resolveRK resolves an RK index to a value (register or constant).
func resolveRK(idx int, regs []rt.Value, base int, constants []rt.Value) rt.Value {
	if idx >= vm.RKBit {
		return constants[idx-vm.RKBit]
	}
	return regs[base+idx]
}

// Free releases compiled code owned by this engine.
func (e *Engine) Free() {
	for _, entry := range e.entries {
		if entry != nil && entry.cf != nil {
			entry.cf.Code.Free()
		}
	}
	e.entries = nil
}
