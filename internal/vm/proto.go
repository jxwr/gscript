package vm

import (
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// globalCacheEntry caches a global variable index for fast array lookup.
type globalCacheEntry struct {
	index   int32  // index into VM.globalArray (-1 = not resolved)
	version uint32 // matches VM.globalVersion when valid
}

// FuncProto is the bytecode function prototype.
// It contains the compiled instructions, constants, and metadata for a function.
type FuncProto struct {
	Name        string      // function name (for debugging)
	Source      string      // source file
	LineDefined int         // line where the function is defined
	NumParams   int         // number of fixed parameters
	IsVarArg    bool        // whether the function accepts varargs
	MaxStack    int         // maximum number of registers used
	Code        []uint32    // bytecode instructions
	Constants   []runtime.Value // constant pool
	Upvalues    []UpvalDesc // upvalue descriptors
	Protos      []*FuncProto // nested function prototypes
	LineInfo    []int       // source line for each instruction (debug)
	GlobalCache    []globalCacheEntry        // lazily-initialized cache indexed by constant pool index
	FieldCache     []runtime.FieldCacheEntry // lazily-initialized inline cache for GETFIELD/SETFIELD, indexed by PC
	TraceBlacklist []bool                    // lazily-initialized per-PC trace blacklist; true = skip OnLoopBackEdge
	JITEntry       unsafe.Pointer           // cached *compiledEntry from JIT engine (avoids map lookup on hot path)
	HasSelfCalls   bool                      // true if function has recursive calls to itself (set during JIT compilation)
	CallCount      int                       // JIT call count (avoids map lookup in VM hot path)
}

// BlacklistTracePC marks a FORLOOP PC as trace-blacklisted.
// Subsequent iterations of this loop skip the OnLoopBackEdge call entirely,
// avoiding the ~30-50ns interface dispatch + map lookup overhead per iteration.
func (p *FuncProto) BlacklistTracePC(pc int) {
	if pc < 0 || pc >= len(p.Code) {
		return
	}
	if p.TraceBlacklist == nil {
		p.TraceBlacklist = make([]bool, len(p.Code))
	}
	p.TraceBlacklist[pc] = true
}

// HasCallInLoop returns true if any for-loop body contains a CALL instruction.
// Traces with CALLs use call-exit which can produce incorrect results.
func (p *FuncProto) HasCallInLoop() bool {
	inLoop := false
	for _, inst := range p.Code {
		op := DecodeOp(inst)
		if op == OP_FORPREP {
			inLoop = true
		}
		if op == OP_FORLOOP {
			inLoop = false
		}
		if inLoop && op == OP_CALL {
			return true
		}
	}
	return false
}

// ForLoopCount returns the number of for-loops (FORLOOP instructions) in the function.
func (p *FuncProto) ForLoopCount() int {
	n := 0
	for _, inst := range p.Code {
		if DecodeOp(inst) == OP_FORLOOP {
			n++
		}
	}
	return n
}

// IsTraceBlacklisted returns true if the given PC has been trace-blacklisted.
func (p *FuncProto) IsTraceBlacklisted(pc int) bool {
	if p.TraceBlacklist == nil {
		return false
	}
	return p.TraceBlacklist[pc]
}

// UpvalDesc describes how an upvalue should be captured when creating a closure.
type UpvalDesc struct {
	Name    string // variable name (for debugging)
	InStack bool   // true: capture from enclosing function's register at Index
	                // false: capture from enclosing function's upvalue at Index
	Index   int    // register index (if InStack) or upvalue index in parent
}

// Closure is a bytecode closure: a FuncProto paired with captured upvalues.
type Closure struct {
	Proto    *FuncProto
	Upvalues []*Upvalue
}

// Upvalue is a mutable reference to a value.
// When "open", it points into a register in the call stack.
// When "closed", it holds its own copy (the register has gone out of scope).
type Upvalue struct {
	ref    *runtime.Value // points to register slot (open) or val field (closed)
	val    runtime.Value  // storage for closed upvalue
	open   bool
	regIdx int // original register index (for closing)
}

// NewOpenUpvalue creates an open upvalue pointing to a register slot.
func NewOpenUpvalue(reg *runtime.Value, idx int) *Upvalue {
	return &Upvalue{ref: reg, open: true, regIdx: idx}
}

// Get returns the current value.
func (u *Upvalue) Get() runtime.Value {
	return *u.ref
}

// Set assigns a new value.
func (u *Upvalue) Set(v runtime.Value) {
	*u.ref = v
}

// Close copies the value from the register to internal storage.
// After closing, the upvalue no longer depends on the register.
func (u *Upvalue) Close() {
	if u.open {
		u.val = *u.ref
		u.ref = &u.val
		u.open = false
	}
}

// CallFrame represents a single activation record on the VM call stack.
type CallFrame struct {
	closure     *Closure
	pc          int    // program counter within closure.Proto.Code
	base        int    // base register index in the VM register file
	numResults  int    // expected number of results (-1 = variable)
	varargs     []runtime.Value // extra arguments beyond fixed params
	resultBase   int    // register in parent frame where results should be placed (for inline return)
	resultCount  int    // C parameter from caller's OP_CALL (0 = return all; for inline return)
	traceEnabled bool   // true if this frame's loops should be trace-recorded (set on Method JIT side-exit)
}
