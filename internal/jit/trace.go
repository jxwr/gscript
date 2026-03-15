package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TraceIR represents one instruction in a recorded trace.
type TraceIR struct {
	Op    vm.Opcode        // original bytecode opcode
	A     int              // decoded A operand
	B     int              // decoded B operand
	C     int              // decoded C operand
	BX    int              // decoded Bx operand (for ABx format)
	SBX   int              // decoded sBx operand (for AsBx format)
	PC    int              // bytecode PC in the source function
	Proto *vm.FuncProto    // function this instruction belongs to

	// Type info captured during recording:
	AType runtime.ValueType // type of R(A) at this point
	BType runtime.ValueType // type of RK(B) or R(B)
	CType runtime.ValueType // type of RK(C) or R(C)

	// Inline depth (0 = root function, >0 = inlined callee)
	Depth int
	// Base register offset for this inline level
	Base  int
}

// Trace is a recorded execution trace (one loop iteration).
type Trace struct {
	ID        int
	LoopPC    int              // bytecode PC of the loop back-edge
	LoopProto *vm.FuncProto    // function containing the loop
	IR        []TraceIR        // recorded instruction stream
	EntryPC   int              // bytecode PC where the trace starts
}

// RecorderHook is the interface that vm.VM uses to communicate with the trace recorder.
// Defined here to avoid circular imports.
type RecorderHook interface {
	// OnInstruction is called for every instruction during execution.
	// Returns true if the recorder wants to stop execution (trace complete or aborted).
	OnInstruction(pc int, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base int) bool

	// OnLoopBackEdge is called when a loop back-edge is detected.
	// Returns true if a compiled trace should be executed instead.
	OnLoopBackEdge(pc int, proto *vm.FuncProto) bool

	// IsRecording returns true if currently recording a trace.
	IsRecording() bool
}

// TraceRecorder captures instructions during recording mode.
type TraceRecorder struct {
	traces    []*Trace
	current   *Trace
	recording bool
	depth     int  // inline call depth
	maxDepth  int  // max inline depth
	maxLen    int  // max trace length
	compile   bool // if true, compile traces after recording

	// Loop hotness tracking
	loopCounts map[loopKey]int
	threshold  int // recording starts after this many iterations

	// Compiled trace cache: keyed by (proto, loopPC)
	compiled     map[loopKey]*CompiledTrace
	pendingTrace *CompiledTrace
}

type loopKey struct {
	proto *vm.FuncProto
	pc    int
}

const (
	DefaultTraceThreshold = 10
	DefaultMaxTraceLen    = 500
	DefaultMaxInlineDepth = 3
)

// NewTraceRecorder creates a new trace recorder.
func NewTraceRecorder() *TraceRecorder {
	return &TraceRecorder{
		maxDepth:   DefaultMaxInlineDepth,
		maxLen:     DefaultMaxTraceLen,
		threshold:  DefaultTraceThreshold,
		loopCounts: make(map[loopKey]int),
		compiled:   make(map[loopKey]*CompiledTrace),
	}
}

// SetCompile enables trace compilation and execution.
func (r *TraceRecorder) SetCompile(on bool) {
	r.compile = on
}

// GetCompiled returns a compiled trace for the given loop, or nil.
func (r *TraceRecorder) GetCompiled(pc int, proto *vm.FuncProto) *CompiledTrace {
	return r.compiled[loopKey{proto: proto, pc: pc}]
}

// Traces returns all recorded traces.
func (r *TraceRecorder) Traces() []*Trace {
	return r.traces
}

// IsRecording returns true if currently recording.
func (r *TraceRecorder) IsRecording() bool {
	return r.recording
}

// OnLoopBackEdge is called when the interpreter detects a loop back-edge.
// Returns true if a compiled trace was executed (caller should re-read registers).
func (r *TraceRecorder) OnLoopBackEdge(pc int, proto *vm.FuncProto) bool {
	if r.recording {
		// We've completed one loop iteration — stop recording
		r.finishTrace()
		// If we just compiled a trace, don't execute it yet (let the
		// interpreter handle the current iteration normally)
		return false
	}

	// Check for existing compiled trace
	key := loopKey{proto: proto, pc: pc}
	if ct, ok := r.compiled[key]; ok {
		// Mark for execution — the VM will call ExecuteTrace
		r.pendingTrace = ct
		return true
	}

	// Track loop hotness
	r.loopCounts[key]++
	if r.loopCounts[key] >= r.threshold {
		r.startTrace(pc, proto)
	}
	return false
}

// PendingTrace returns the compiled trace to execute (set by OnLoopBackEdge).
// Implements vm.TracePendingHook.
func (r *TraceRecorder) PendingTrace() vm.TraceExecutor {
	ct := r.pendingTrace
	r.pendingTrace = nil
	if ct == nil {
		return nil
	}
	return ct
}

// OnInstruction is called for every instruction during execution.
func (r *TraceRecorder) OnInstruction(pc int, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base int) bool {
	if !r.recording {
		return false
	}

	// Check trace length limit
	if len(r.current.IR) >= r.maxLen {
		r.abortTrace()
		return false
	}

	op := vm.DecodeOp(inst)
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	ir := TraceIR{
		Op:    op,
		A:     a,
		B:     b,
		C:     c,
		PC:    pc,
		Proto: proto,
		Depth: r.depth,
		Base:  base,
	}

	// Decode format-specific fields
	switch op {
	case vm.OP_GETGLOBAL, vm.OP_SETGLOBAL, vm.OP_LOADK, vm.OP_CLOSURE:
		ir.BX = vm.DecodeBx(inst)
	case vm.OP_LOADINT, vm.OP_FORPREP, vm.OP_FORLOOP, vm.OP_JMP:
		ir.SBX = vm.DecodesBx(inst)
	}

	// Capture type info for operands
	ir.AType = safeRegType(regs, base+a)
	if b < vm.RKBit {
		ir.BType = safeRegType(regs, base+b)
	} else {
		ir.BType = proto.Constants[b-vm.RKBit].Type()
	}
	if c < vm.RKBit {
		ir.CType = safeRegType(regs, base+c)
	} else {
		ir.CType = proto.Constants[c-vm.RKBit].Type()
	}

	// Handle CALL: try to inline
	if op == vm.OP_CALL {
		return r.handleCall(ir, regs, base)
	}

	// Handle RETURN from inlined function
	if op == vm.OP_RETURN && r.depth > 0 {
		r.depth--
		// Don't record the RETURN itself — caller continues
		return false
	}

	// Check for unsupported ops that abort recording
	if r.shouldAbort(op) {
		r.abortTrace()
		return false
	}

	r.current.IR = append(r.current.IR, ir)
	return false
}

// handleCall attempts to inline a function call into the trace.
func (r *TraceRecorder) handleCall(ir TraceIR, regs []runtime.Value, base int) bool {
	if r.depth >= r.maxDepth {
		// Too deep — record as a CALL (will be call-exit in compilation)
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Check if the callee is a VM closure we can inline
	fnVal := regs[base+ir.A]
	if !fnVal.IsFunction() {
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Try to get the VM closure
	cl, ok := fnVal.Ptr().(*vm.Closure)
	if !ok || cl == nil {
		// GoFunction or tree-walker closure — can't inline
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Inline: increment depth, the interpreter will execute the callee's
	// instructions which will be captured by OnInstruction at depth+1
	r.depth++
	// Don't record the CALL instruction itself — callee's body is inlined
	return false
}

// shouldAbort returns true for opcodes that can't be traced.
func (r *TraceRecorder) shouldAbort(op vm.Opcode) bool {
	switch op {
	case vm.OP_GO, vm.OP_SEND, vm.OP_RECV, vm.OP_MAKECHAN:
		return true // concurrency ops
	case vm.OP_TFORCALL, vm.OP_TFORLOOP:
		return true // generic for (complex iterator)
	case vm.OP_FORPREP:
		return true // nested loop — abort (trace only handles one loop level)
	}
	return false
}

func (r *TraceRecorder) startTrace(pc int, proto *vm.FuncProto) {
	r.recording = true
	r.depth = 0
	r.current = &Trace{
		ID:        len(r.traces),
		LoopPC:    pc,
		LoopProto: proto,
		EntryPC:   pc,
	}
}

func (r *TraceRecorder) finishTrace() {
	if r.current != nil && len(r.current.IR) > 0 {
		r.traces = append(r.traces, r.current)

		// Compile the trace if enabled
		if r.compile {
			ct, err := compileTrace(r.current)
			if err == nil {
				key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
				r.compiled[key] = ct
			}
		}
	}
	r.current = nil
	r.recording = false
	r.depth = 0
}

func (r *TraceRecorder) abortTrace() {
	r.current = nil
	r.recording = false
	r.depth = 0
}

// safeRegType returns the type of a register, handling out-of-range gracefully.
func safeRegType(regs []runtime.Value, idx int) runtime.ValueType {
	if idx < 0 || idx >= len(regs) {
		return runtime.TypeNil
	}
	return regs[idx].Type()
}
