package vm

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/gscript/gscript/internal/runtime"
)

const (
	maxStack      = 256  // max registers per call frame
	maxCallDepth  = 200  // max call stack depth
	maxMetaDepth  = 50   // max __index chain depth
)

// JITEngine is the interface for JIT compilation engines.
// This allows the VM to call into the JIT without a direct package dependency.
type JITEngine interface {
	// TryExecute attempts to JIT-execute a function.
	// Returns (results, resumePC, ok).
	// ok=true: function completed, results contains return values.
	// ok=false: JIT bailed at resumePC, interpreter should continue from there.
	TryExecute(proto *FuncProto, regs []runtime.Value, base int, callCount int) ([]runtime.Value, int, bool)
}

// VM is the bytecode virtual machine.
type VM struct {
	regs       []runtime.Value // register file (shared across frames via base offset)
	frames     []CallFrame     // call stack
	frameCount int             // current number of active frames
	globals    map[string]runtime.Value
	globalsMu  *sync.RWMutex   // protects globals for goroutine safety (shared across VMs)
	openUpvals []*Upvalue // list of open upvalues (sorted by regIdx descending)
	top        int        // top of used registers (for variable returns)
	stringMeta *runtime.Table // string metatable
	jit        JITEngine      // optional JIT engine
	callCounts map[*FuncProto]int // per-function call counts for JIT hot detection
	argBuf     [16]runtime.Value  // pre-allocated arg buffer for OP_CALL
	retBuf     [8]runtime.Value   // pre-allocated return buffer for OP_RETURN
}

// SetJIT sets the JIT engine for this VM.
func (vm *VM) SetJIT(engine JITEngine) {
	vm.jit = engine
	if engine != nil && vm.callCounts == nil {
		vm.callCounts = make(map[*FuncProto]int)
	}
}

// Regs returns the register file. Used by the JIT executor.
func (vm *VM) Regs() []runtime.Value {
	return vm.regs
}

// New creates a new VM with the given globals.
func New(globals map[string]runtime.Value) *VM {
	v := &VM{
		regs:      make([]runtime.Value, 1024),
		frames:    make([]CallFrame, maxCallDepth),
		globals:   globals,
		globalsMu: &sync.RWMutex{},
	}
	v.RegisterCoroutineLib()
	v.registerChannelBuiltins()
	return v
}

// newChildVM creates a child VM that shares globals, mutex, and string metatable
// with the parent. Used by OP_GO for goroutines.
func newChildVM(parent *VM) *VM {
	child := &VM{
		regs:       make([]runtime.Value, 1024),
		frames:     make([]CallFrame, maxCallDepth),
		globals:    parent.globals,
		globalsMu:  parent.globalsMu,
		stringMeta: parent.stringMeta,
	}
	child.RegisterCoroutineLib()
	return child
}

// registerChannelBuiltins adds channel-related builtins (close) to globals.
func (vm *VM) registerChannelBuiltins() {
	if vm.globalsMu != nil {
		vm.globalsMu.Lock()
	}
	vm.globals["close"] = runtime.FunctionValue(&runtime.GoFunction{
		Name: "close",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 1 || !args[0].IsChannel() {
				return nil, fmt.Errorf("close expects a channel")
			}
			ch := args[0].Channel()
			if err := ch.Close(); err != nil {
				return nil, err
			}
			return nil, nil
		},
	})
	if vm.globalsMu != nil {
		vm.globalsMu.Unlock()
	}
}

// SetStringMeta sets the string metatable (for string method calls).
func (vm *VM) SetStringMeta(meta *runtime.Table) {
	vm.stringMeta = meta
}

// Execute runs a top-level function prototype.
func (vm *VM) Execute(proto *FuncProto) ([]runtime.Value, error) {
	cl := &Closure{Proto: proto}
	vm.frameCount = 0
	vm.top = 0
	return vm.call(cl, nil, 0, 0)
}

// CallValue calls a function value with the given arguments (exported for gscript wrapper).
func (vm *VM) CallValue(fn runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	return vm.callValue(fn, args)
}

// call pushes a new call frame and executes.
// args are placed in registers starting at base.
// Returns the function's return values.
func (vm *VM) call(cl *Closure, args []runtime.Value, base int, numResults int) ([]runtime.Value, error) {
	proto := cl.Proto

	// Ensure register space
	needed := base + proto.MaxStack + 1
	if needed > len(vm.regs) {
		newRegs := make([]runtime.Value, needed*2)
		copy(newRegs, vm.regs)
		vm.regs = newRegs
	}

	// Place args in registers
	nParams := proto.NumParams
	var varargs []runtime.Value
	for i := 0; i < nParams && i < len(args); i++ {
		vm.regs[base+i] = args[i]
	}
	// Nil-fill missing params
	for i := len(args); i < nParams; i++ {
		vm.regs[base+i] = runtime.NilValue()
	}
	// Collect varargs
	if proto.IsVarArg && len(args) > nParams {
		varargs = make([]runtime.Value, len(args)-nParams)
		copy(varargs, args[nParams:])
	}

	// Push frame
	if vm.frameCount >= maxCallDepth {
		return nil, fmt.Errorf("stack overflow (max call depth %d)", maxCallDepth)
	}
	frame := &vm.frames[vm.frameCount]
	frame.closure = cl
	frame.pc = 0
	frame.base = base
	frame.numResults = numResults
	frame.varargs = varargs
	vm.frameCount++

	// Try JIT execution if available.
	if vm.jit != nil && !proto.IsVarArg {
		vm.callCounts[proto]++
		results, resumePC, ok := vm.jit.TryExecute(proto, vm.regs, base, vm.callCounts[proto])
		if ok {
			// JIT completed the function — close upvalues and return.
			vm.closeUpvalues(base)
			vm.frameCount--
			return results, nil
		}
		if resumePC > 0 {
			// JIT bailed out — resume interpreter from the exit PC.
			frame.pc = resumePC
		}
		// resumePC == 0 means JIT wasn't attempted (not hot enough); fall through.
	}

	result, err := vm.run()
	vm.frameCount--
	return result, err
}

// wrapLineErr wraps an error with source location info from the current frame.
func wrapLineErr(frame *CallFrame, err error) error {
	if err == nil {
		return nil
	}
	pc := frame.pc - 1
	line := 0
	if pc >= 0 && pc < len(frame.closure.Proto.LineInfo) {
		line = frame.closure.Proto.LineInfo[pc]
	}
	name := frame.closure.Proto.Source
	if name == "" {
		name = frame.closure.Proto.Name
	}
	if line > 0 {
		return fmt.Errorf("%s:%d: %w", name, line, err)
	}
	return err
}

// run is the main execution loop. It handles frame switches internally
// to avoid Go function call overhead for recursive bytecode calls.
func (vm *VM) run() ([]runtime.Value, error) {
	frame := &vm.frames[vm.frameCount-1]
	code := frame.closure.Proto.Code
	constants := frame.closure.Proto.Constants
	base := frame.base

	for {
		if frame.pc >= len(code) {
			return nil, nil
		}
		inst := code[frame.pc]
		frame.pc++

		op := DecodeOp(inst)

		switch op {
		case OP_LOADNIL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			for i := a; i <= a+b; i++ {
				vm.regs[base+i] = runtime.NilValue()
			}

		case OP_LOADBOOL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			vm.regs[base+a] = runtime.BoolValue(b != 0)
			if c != 0 {
				frame.pc++
			}

		case OP_LOADINT:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			vm.regs[base+a] = runtime.IntValue(int64(sbx))

		case OP_LOADK:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			vm.regs[base+a] = constants[bx]

		case OP_MOVE:
			a := DecodeA(inst)
			b := DecodeB(inst)
			vm.regs[base+a] = vm.regs[base+b]

		case OP_GETGLOBAL:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			name := constants[bx].Str()
			vm.globalsMu.RLock()
			v, ok := vm.globals[name]
			vm.globalsMu.RUnlock()
			if ok {
				vm.regs[base+a] = v
			} else {
				vm.regs[base+a] = runtime.NilValue()
			}

		case OP_SETGLOBAL:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			name := constants[bx].Str()
			vm.globalsMu.Lock()
			vm.globals[name] = vm.regs[base+a]
			vm.globalsMu.Unlock()

		case OP_GETUPVAL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			vm.regs[base+a] = frame.closure.Upvalues[b].Get()

		case OP_SETUPVAL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			frame.closure.Upvalues[b].Set(vm.regs[base+a])

		case OP_NEWTABLE:
			a := DecodeA(inst)
			vm.regs[base+a] = runtime.TableValue(runtime.NewTable())

		case OP_GETTABLE:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cidx := DecodeC(inst)
			tableVal := vm.regs[base+b]
			var key runtime.Value
			if cidx >= RKBit {
				key = constants[cidx-RKBit]
			} else {
				key = vm.regs[base+cidx]
			}
			// Fast path: plain table (no metatable) → direct RawGet
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					vm.regs[base+a] = tbl.RawGet(key)
					break
				}
			}
			val, err := vm.tableGet(tableVal, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val

		case OP_SETTABLE:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			tableVal := vm.regs[base+a]
			var key, val runtime.Value
			if bidx >= RKBit {
				key = constants[bidx-RKBit]
			} else {
				key = vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				val = constants[cidx-RKBit]
			} else {
				val = vm.regs[base+cidx]
			}
			// Fast path: plain table → direct RawSet
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					tbl.RawSet(key, val)
					break
				}
			}
			if err := vm.tableSet(tableVal, key, val); err != nil {
				return nil, err
			}

		case OP_GETFIELD:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			tableVal := vm.regs[base+b]
			key := constants[c]
			// Fast path: plain table → direct RawGet
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					vm.regs[base+a] = tbl.RawGet(key)
					break
				}
			}
			val, err := vm.tableGet(tableVal, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val

		case OP_SETFIELD:
			a := DecodeA(inst)
			b := DecodeB(inst) // constant index for field name
			cidx := DecodeC(inst)
			tableVal := vm.regs[base+a]
			key := constants[b]
			var val runtime.Value
			if cidx >= RKBit {
				val = constants[cidx-RKBit]
			} else {
				val = vm.regs[base+cidx]
			}
			// Fast path: plain table → direct RawSet
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					tbl.RawSet(key, val)
					break
				}
			}
			if err := vm.tableSet(tableVal, key, val); err != nil {
				return nil, err
			}

		case OP_SETLIST:
			a := DecodeA(inst)
			b := DecodeB(inst) // count
			c := DecodeC(inst) // starting offset (1-based batch)
			t := vm.regs[base+a].Table()
			if t == nil {
				return nil, fmt.Errorf("SETLIST on non-table")
			}
			offset := (c - 1) * 50
			for i := 1; i <= b; i++ {
				t.RawSet(runtime.IntValue(int64(offset+i)), vm.regs[base+a+i])
			}

		case OP_APPEND:
			a := DecodeA(inst)
			b := DecodeB(inst)
			t := vm.regs[base+a].Table()
			if t == nil {
				return nil, fmt.Errorf("APPEND on non-table")
			}
			t.Append(vm.regs[base+b])

		// ---- Arithmetic ----
		case OP_ADD:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.AddInts(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__add", func(x, y float64) float64 { return x + y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_SUB:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.SubInts(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__sub", func(x, y float64) float64 { return x - y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_MUL:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.MulInts(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__mul", func(x, y float64) float64 { return x * y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_DIV:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit { bv = constants[bidx-RKBit] } else { bv = vm.regs[base+bidx] }
			if cidx >= RKBit { cv = constants[cidx-RKBit] } else { cv = vm.regs[base+cidx] }
			r, err := vm.arith(bv, cv, "__div", func(x, y float64) float64 { return x / y })
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_MOD:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit { bv = constants[bidx-RKBit] } else { bv = vm.regs[base+bidx] }
			if cidx >= RKBit { cv = constants[cidx-RKBit] } else { cv = vm.regs[base+cidx] }
			r, err := vm.arithMod(bv, cv)
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_POW:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit { bv = constants[bidx-RKBit] } else { bv = vm.regs[base+bidx] }
			if cidx >= RKBit { cv = constants[cidx-RKBit] } else { cv = vm.regs[base+cidx] }
			r, err := vm.arith(bv, cv, "__pow", func(x, y float64) float64 { return math.Pow(x, y) })
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_UNM:
			a := DecodeA(inst)
			bv := vm.regs[base+DecodeB(inst)]
			r, err := vm.unaryMinus(bv)
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_NOT:
			a := DecodeA(inst)
			bv := vm.regs[base+DecodeB(inst)]
			vm.regs[base+a] = runtime.BoolValue(!bv.Truthy())

		case OP_LEN:
			a := DecodeA(inst)
			bv := vm.regs[base+DecodeB(inst)]
			r, err := vm.length(bv)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = r

		case OP_CONCAT:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			var sb strings.Builder
			for i := b; i <= c; i++ {
				sb.WriteString(vm.regs[base+i].String())
			}
			vm.regs[base+a] = runtime.StringValue(sb.String())

		// ---- Comparison ----
		case OP_EQ:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			if bp.RawType() == runtime.TypeInt && cp.RawType() == runtime.TypeInt {
				if (bp.RawInt() == cp.RawInt()) != (a != 0) {
					frame.pc++
				}
			} else {
				if (*bp).Equal(*cp) != (a != 0) {
					frame.pc++
				}
			}

		case OP_LT:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			if lt, ok := runtime.LTInts(bp, cp); ok {
				if lt != (a != 0) {
					frame.pc++
				}
			} else {
				lt, ok := (*bp).LessThan(*cp)
				if !ok {
					return nil, fmt.Errorf("attempt to compare %s with %s", bp.TypeName(), cp.TypeName())
				}
				if lt != (a != 0) {
					frame.pc++
				}
			}

		case OP_LE:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			if le, ok := runtime.LEInts(bp, cp); ok {
				if le != (a != 0) {
					frame.pc++
				}
			} else {
				// a <= b  is  !(b < a)
				lt, ok := (*cp).LessThan(*bp)
				if !ok {
					return nil, fmt.Errorf("attempt to compare %s with %s", bp.TypeName(), cp.TypeName())
				}
				if !lt != (a != 0) {
					frame.pc++
				}
			}

		// ---- Logical ----
		case OP_TEST:
			a := DecodeA(inst)
			c := DecodeC(inst)
			if vm.regs[base+a].Truthy() != (c != 0) {
				frame.pc++
			}

		case OP_TESTSET:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			bv := vm.regs[base+b]
			if bv.Truthy() != (c != 0) {
				frame.pc++
			} else {
				vm.regs[base+a] = bv
			}

		// ---- Jump ----
		case OP_JMP:
			sbx := DecodesBx(inst)
			frame.pc += sbx

		// ---- Call / Return ----
		case OP_CALL:
			a := DecodeA(inst)
			b := DecodeB(inst) // arg count + 1; 0 = use top
			c := DecodeC(inst) // result count + 1; 0 = return all

			fnVal := vm.regs[base+a]
			nArgs := b - 1
			if b == 0 {
				nArgs = vm.top - (base + a + 1)
			}
			// Use pre-allocated buffer for small arg counts to avoid allocation
			var args []runtime.Value
			if nArgs <= len(vm.argBuf) {
				args = vm.argBuf[:nArgs]
			} else {
				args = make([]runtime.Value, nArgs)
			}
			for i := 0; i < nArgs; i++ {
				args[i] = vm.regs[base+a+1+i]
			}

			results, err := vm.callValue(fnVal, args)
			if err != nil {
				return nil, err
			}

			nResults := c - 1
			if c == 0 {
				// Return all results; store count in top
				for i, r := range results {
					vm.regs[base+a+i] = r
				}
				vm.top = base + a + len(results)
			} else {
				for i := 0; i < nResults; i++ {
					if i < len(results) {
						vm.regs[base+a+i] = results[i]
					} else {
						vm.regs[base+a+i] = runtime.NilValue()
					}
				}
			}

		case OP_RETURN:
			a := DecodeA(inst)
			b := DecodeB(inst)

			// Close upvalues
			vm.closeUpvalues(base)

			if b == 0 {
				// Return R(A) to top
				nret := vm.top - (base + a)
				var ret []runtime.Value
				if nret <= len(vm.retBuf) {
					ret = vm.retBuf[:nret]
				} else {
					ret = make([]runtime.Value, nret)
				}
				for i := 0; i < nret; i++ {
					ret[i] = vm.regs[base+a+i]
				}
				return ret, nil
			}
			if b == 1 {
				return nil, nil
			}
			nret := b - 1
			var ret []runtime.Value
			if nret <= len(vm.retBuf) {
				ret = vm.retBuf[:nret]
			} else {
				ret = make([]runtime.Value, nret)
			}
			for i := 0; i < nret; i++ {
				ret[i] = vm.regs[base+a+i]
			}
			return ret, nil

		case OP_CLOSURE:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			subProto := frame.closure.Proto.Protos[bx]
			cl := &Closure{
				Proto:    subProto,
				Upvalues: make([]*Upvalue, len(subProto.Upvalues)),
			}
			for i, desc := range subProto.Upvalues {
				if desc.InStack {
					// Capture from current frame's register
					cl.Upvalues[i] = vm.findOrCreateUpvalue(base + desc.Index)
				} else {
					// Copy from enclosing closure's upvalue
					cl.Upvalues[i] = frame.closure.Upvalues[desc.Index]
				}
			}
			vm.regs[base+a] = runtime.FunctionValue(cl)

		case OP_CLOSE:
			a := DecodeA(inst)
			vm.closeUpvalues(base + a)

		// ---- Numeric For Loop ----
		case OP_FORPREP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			// R(A) = init, R(A+1) = limit, R(A+2) = step
			// R(A) -= R(A+2) so the first FORLOOP increment brings it to init
			initV := vm.regs[base+a]
			stepV := vm.regs[base+a+2]
			if initV.IsInt() && stepV.IsInt() {
				vm.regs[base+a] = runtime.IntValue(initV.Int() - stepV.Int())
			} else {
				vm.regs[base+a] = runtime.FloatValue(initV.Number() - stepV.Number())
			}
			frame.pc += sbx

		case OP_FORLOOP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			idxP := &vm.regs[base+a]
			// Fast path: all-integer for loop (most common case)
			if idxP.RawType() == runtime.TypeInt {
				stepP := &vm.regs[base+a+2]
				limitP := &vm.regs[base+a+1]
				if stepP.RawType() == runtime.TypeInt && limitP.RawType() == runtime.TypeInt {
					step := stepP.RawInt()
					idx := idxP.RawInt() + step
					limit := limitP.RawInt()
					var cont bool
					if step > 0 {
						cont = idx <= limit
					} else {
						cont = idx >= limit
					}
					if cont {
						idxP.SetInt(idx)
						vm.regs[base+a+3].SetInt(idx)
						frame.pc += sbx
					}
					break
				}
			}
			// Slow path: float for loop
			step := vm.regs[base+a+2].Number()
			limit := vm.regs[base+a+1].Number()
			idx := vm.regs[base+a].Number() + step
			cont := false
			if step > 0 {
				cont = idx <= limit
			} else {
				cont = idx >= limit
			}
			if cont {
				if floatIsExactInt(idx) {
					vm.regs[base+a] = runtime.IntValue(int64(idx))
					vm.regs[base+a+3] = runtime.IntValue(int64(idx))
				} else {
					vm.regs[base+a] = runtime.FloatValue(idx)
					vm.regs[base+a+3] = runtime.FloatValue(idx)
				}
				frame.pc += sbx
			} else {
				vm.regs[base+a] = runtime.FloatValue(idx)
			}

		case OP_VARARG:
			a := DecodeA(inst)
			b := DecodeB(inst)
			va := frame.varargs
			if b == 0 {
				// Copy all varargs
				for i, v := range va {
					vm.regs[base+a+i] = v
				}
				vm.top = base + a + len(va)
			} else {
				n := b - 1
				for i := 0; i < n; i++ {
					if i < len(va) {
						vm.regs[base+a+i] = va[i]
					} else {
						vm.regs[base+a+i] = runtime.NilValue()
					}
				}
			}

		case OP_SELF:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cidx := DecodeC(inst)
			obj := vm.regs[base+b]
			vm.regs[base+a+1] = obj
			var key runtime.Value
			if cidx >= RKBit {
				key = constants[cidx-RKBit]
			} else {
				key = vm.regs[base+cidx]
			}
			val, err := vm.tableGet(obj, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val

		case OP_TFORCALL:
			a := DecodeA(inst)
			c := DecodeC(inst) // number of results
			// R(A) = iterator, R(A+1) = state, R(A+2) = control
			fnVal := vm.regs[base+a]

			// Channel range: for v := range ch
			if fnVal.IsChannel() {
				ch := fnVal.Channel()
				val, ok := ch.Recv()
				if ok {
					vm.regs[base+a+3] = val
					for i := 1; i < c; i++ {
						vm.regs[base+a+3+i] = runtime.NilValue()
					}
				} else {
					// Channel closed — set first result to nil to end loop
					for i := 0; i < c; i++ {
						vm.regs[base+a+3+i] = runtime.NilValue()
					}
				}
			} else {
				args := []runtime.Value{vm.regs[base+a+1], vm.regs[base+a+2]}
				results, err := vm.callValue(fnVal, args)
				if err != nil {
					return nil, err
				}
				for i := 0; i < c; i++ {
					if i < len(results) {
						vm.regs[base+a+3+i] = results[i]
					} else {
						vm.regs[base+a+3+i] = runtime.NilValue()
					}
				}
			}

		case OP_TFORLOOP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			if !vm.regs[base+a+1].IsNil() {
				vm.regs[base+a] = vm.regs[base+a+1]
				frame.pc += sbx
			}

		case OP_GO:
			a := DecodeA(inst)
			b := DecodeB(inst)
			fnVal := vm.regs[base+a]
			nArgs := b - 1
			if b == 0 {
				nArgs = vm.top - (base + a + 1)
			}
			// Copy args (must snapshot before goroutine starts)
			args := make([]runtime.Value, nArgs)
			for i := 0; i < nArgs; i++ {
				args[i] = vm.regs[base+a+1+i]
			}
			// Launch goroutine with a new VM sharing globals and mutex
			go func(fn runtime.Value, goArgs []runtime.Value) {
				goVM := newChildVM(vm)
				if cl, ok := fn.Ptr().(*Closure); ok {
					goVM.call(cl, goArgs, 0, 0)
				} else if gf := fn.GoFunction(); gf != nil {
					gf.Fn(goArgs)
				}
			}(fnVal, args)

		case OP_MAKECHAN:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cc := DecodeC(inst)
			capacity := 0
			if cc == 1 {
				// Size is in R(B)
				sizeVal := vm.regs[base+b]
				if sizeVal.IsInt() {
					capacity = int(sizeVal.Int())
				} else if sizeVal.IsFloat() {
					capacity = int(sizeVal.Float())
				}
			}
			ch := runtime.NewChannel(capacity)
			vm.regs[base+a] = runtime.ChannelValue(ch)

		case OP_SEND:
			a := DecodeA(inst)
			b := DecodeB(inst)
			chVal := vm.regs[base+a]
			if !chVal.IsChannel() {
				return nil, fmt.Errorf("send on non-channel value (got %s)", chVal.TypeName())
			}
			ch := chVal.Channel()
			val := vm.regs[base+b]
			if err := ch.Send(val); err != nil {
				return nil, err
			}

		case OP_RECV:
			a := DecodeA(inst)
			b := DecodeB(inst)
			chVal := vm.regs[base+b]
			if !chVal.IsChannel() {
				return nil, fmt.Errorf("receive from non-channel value (got %s)", chVal.TypeName())
			}
			ch := chVal.Channel()
			val, ok := ch.Recv()
			if ok {
				vm.regs[base+a] = val
			} else {
				vm.regs[base+a] = runtime.NilValue()
			}

		default:
			return nil, fmt.Errorf("unhandled opcode %d (%s)", op, OpName(op))
		}
	}
}

// callValue dispatches a function call (supports Closure, GoFunction, and __call metamethod).
func (vm *VM) callValue(fnVal runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	if fnVal.IsFunction() {
		if cl, ok := fnVal.Ptr().(*Closure); ok {
			newBase := vm.top
			if vm.frameCount > 0 {
				curFrame := &vm.frames[vm.frameCount-1]
				minBase := curFrame.base + curFrame.closure.Proto.MaxStack
				if newBase < minBase {
					newBase = minBase
				}
			}
			return vm.call(cl, args, newBase, -1)
		}
		if gf := fnVal.GoFunction(); gf != nil {
			return gf.Fn(args)
		}
		// Check for tree-walker Closure (from stdlib)
		if c := fnVal.Closure(); c != nil {
			// This is a tree-walker closure; we need an interpreter to run it.
			// For now, return an error. The integration layer handles this.
			return nil, fmt.Errorf("cannot call tree-walker closure from VM")
		}
	}
	if fnVal.IsTable() {
		// __call metamethod
		mt := fnVal.Table().GetMetatable()
		if mt != nil {
			callMM := mt.RawGet(runtime.StringValue("__call"))
			if !callMM.IsNil() {
				newArgs := make([]runtime.Value, len(args)+1)
				newArgs[0] = fnVal
				copy(newArgs[1:], args)
				return vm.callValue(callMM, newArgs)
			}
		}
	}
	return nil, fmt.Errorf("attempt to call a %s value", fnVal.TypeName())
}

// tableGet performs table access with __index metamethod support.
func (vm *VM) tableGet(t runtime.Value, key runtime.Value) (runtime.Value, error) {
	return vm.tableGetDepth(t, key, 0)
}

func (vm *VM) tableGetDepth(t runtime.Value, key runtime.Value, depth int) (runtime.Value, error) {
	if depth > maxMetaDepth {
		return runtime.NilValue(), fmt.Errorf("__index chain too deep")
	}

	// String metatable
	if t.IsString() {
		if vm.stringMeta != nil {
			v := vm.stringMeta.RawGet(key)
			if !v.IsNil() {
				return v, nil
			}
		}
		return runtime.NilValue(), nil
	}

	if !t.IsTable() {
		return runtime.NilValue(), fmt.Errorf("attempt to index a %s value", t.TypeName())
	}

	tbl := t.Table()
	v := tbl.RawGet(key)
	if !v.IsNil() {
		return v, nil
	}

	// Check __index
	mt := tbl.GetMetatable()
	if mt == nil {
		return runtime.NilValue(), nil
	}
	idx := mt.RawGet(runtime.StringValue("__index"))
	if idx.IsNil() {
		return runtime.NilValue(), nil
	}
	if idx.IsTable() {
		return vm.tableGetDepth(runtime.TableValue(idx.Table()), key, depth+1)
	}
	if idx.IsFunction() {
		results, err := vm.callValue(idx, []runtime.Value{t, key})
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return runtime.NilValue(), nil
	}
	return runtime.NilValue(), nil
}

// tableSet performs table assignment with __newindex metamethod support.
func (vm *VM) tableSet(t runtime.Value, key runtime.Value, val runtime.Value) error {
	if !t.IsTable() {
		return fmt.Errorf("attempt to index a %s value", t.TypeName())
	}
	tbl := t.Table()

	// Check __newindex if key doesn't exist
	existing := tbl.RawGet(key)
	if existing.IsNil() {
		mt := tbl.GetMetatable()
		if mt != nil {
			ni := mt.RawGet(runtime.StringValue("__newindex"))
			if !ni.IsNil() {
				if ni.IsFunction() {
					_, err := vm.callValue(ni, []runtime.Value{t, key, val})
					return err
				}
				if ni.IsTable() {
					return vm.tableSet(runtime.TableValue(ni.Table()), key, val)
				}
			}
		}
	}

	tbl.RawSet(key, val)
	return nil
}

// ---- Arithmetic helpers ----

func (vm *VM) arith(a, b runtime.Value, metamethod string, op func(float64, float64) float64) (runtime.Value, error) {
	// Fast path: both numbers
	if a.IsInt() && b.IsInt() {
		switch metamethod {
		case "__add":
			return runtime.IntValue(a.Int() + b.Int()), nil
		case "__sub":
			return runtime.IntValue(a.Int() - b.Int()), nil
		case "__mul":
			return runtime.IntValue(a.Int() * b.Int()), nil
		case "__pow":
			// Power always returns float
			return runtime.FloatValue(math.Pow(float64(a.Int()), float64(b.Int()))), nil
		}
	}
	if a.IsNumber() && b.IsNumber() {
		result := op(a.Number(), b.Number())
		// Try to keep as int if both were int (except div/pow)
		if a.IsInt() && b.IsInt() && metamethod != "__div" && metamethod != "__pow" {
			if floatIsExactInt(result) {
				return runtime.IntValue(int64(result)), nil
			}
		}
		return runtime.FloatValue(result), nil
	}
	// Try to coerce strings to numbers (Lua semantics)
	ac, aok := a.ToNumber()
	bc, bok := b.ToNumber()
	if aok && bok {
		return vm.arith(ac, bc, metamethod, op)
	}
	// Try metamethod
	mm, err := vm.getMetamethod(a, b, metamethod)
	if err == nil && !mm.IsNil() {
		results, err := vm.callValue(mm, []runtime.Value{a, b})
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return runtime.NilValue(), nil
	}
	return runtime.NilValue(), fmt.Errorf("attempt to perform arithmetic on %s and %s", a.TypeName(), b.TypeName())
}

func (vm *VM) arithMod(a, b runtime.Value) (runtime.Value, error) {
	if a.IsInt() && b.IsInt() {
		bi := b.Int()
		if bi == 0 {
			return runtime.NilValue(), fmt.Errorf("attempt to perform 'n%%0'")
		}
		r := a.Int() % bi
		// Lua-style: result has same sign as divisor
		if r != 0 && (r^bi) < 0 {
			r += bi
		}
		return runtime.IntValue(r), nil
	}
	if a.IsNumber() && b.IsNumber() {
		bf := b.Number()
		if bf == 0 {
			return runtime.NilValue(), fmt.Errorf("attempt to perform 'n%%0'")
		}
		r := math.Mod(a.Number(), bf)
		if r != 0 && (r < 0) != (bf < 0) {
			r += bf
		}
		return runtime.FloatValue(r), nil
	}
	return vm.arith(a, b, "__mod", func(x, y float64) float64 { return math.Mod(x, y) })
}

func (vm *VM) unaryMinus(v runtime.Value) (runtime.Value, error) {
	if v.IsInt() {
		return runtime.IntValue(-v.Int()), nil
	}
	if v.IsFloat() {
		return runtime.FloatValue(-v.Float()), nil
	}
	// Coerce string to number
	if nv, ok := v.ToNumber(); ok {
		return vm.unaryMinus(nv)
	}
	mm, err := vm.getMetamethod(v, v, "__unm")
	if err == nil && !mm.IsNil() {
		results, err := vm.callValue(mm, []runtime.Value{v})
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
	}
	return runtime.NilValue(), fmt.Errorf("attempt to negate a %s value", v.TypeName())
}

func (vm *VM) length(v runtime.Value) (runtime.Value, error) {
	if v.IsString() {
		return runtime.IntValue(int64(len(v.Str()))), nil
	}
	if v.IsTable() {
		// Check __len metamethod
		mt := v.Table().GetMetatable()
		if mt != nil {
			mm := mt.RawGet(runtime.StringValue("__len"))
			if !mm.IsNil() {
				results, err := vm.callValue(mm, []runtime.Value{v})
				if err != nil {
					return runtime.NilValue(), err
				}
				if len(results) > 0 {
					return results[0], nil
				}
				return runtime.IntValue(0), nil
			}
		}
		return runtime.IntValue(int64(v.Table().Len())), nil
	}
	return runtime.NilValue(), fmt.Errorf("attempt to get length of a %s value", v.TypeName())
}

func (vm *VM) getMetamethod(a, b runtime.Value, name string) (runtime.Value, error) {
	key := runtime.StringValue(name)
	if a.IsTable() {
		mt := a.Table().GetMetatable()
		if mt != nil {
			mm := mt.RawGet(key)
			if !mm.IsNil() {
				return mm, nil
			}
		}
	}
	if b.IsTable() {
		mt := b.Table().GetMetatable()
		if mt != nil {
			mm := mt.RawGet(key)
			if !mm.IsNil() {
				return mm, nil
			}
		}
	}
	return runtime.NilValue(), fmt.Errorf("no metamethod %s", name)
}

// ---- Upvalue management ----

func (vm *VM) findOrCreateUpvalue(regIdx int) *Upvalue {
	// Check if an open upvalue for this register already exists
	for _, uv := range vm.openUpvals {
		if uv.regIdx == regIdx {
			return uv
		}
	}
	uv := NewOpenUpvalue(&vm.regs[regIdx], regIdx)
	vm.openUpvals = append(vm.openUpvals, uv)
	return uv
}

func (vm *VM) closeUpvalues(fromReg int) {
	kept := vm.openUpvals[:0]
	for _, uv := range vm.openUpvals {
		if uv.regIdx >= fromReg {
			uv.Close()
		} else {
			kept = append(kept, uv)
		}
	}
	vm.openUpvals = kept
}

// ---- Helpers ----

func floatIsExactInt(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	return f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64
}

// Ptr returns the ptr field of a Value (needed for Closure type assertion).
func init() {
	// Register a method to access Value.ptr from outside the runtime package
}
