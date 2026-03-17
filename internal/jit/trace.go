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
	Base int
	// Self-call flag (true if this OP_CALL is self-recursive)
	IsSelfCall bool
	// Intrinsic: recognized GoFunction replaced with inline ARM64
	// 0 = not intrinsic, >0 = intrinsic ID
	Intrinsic int
}

// Intrinsic IDs for recognized GoFunctions
const (
	IntrinsicNone     = 0
	IntrinsicBxor     = 1 // bit32.bxor(a, b) → EOR
	IntrinsicBand     = 2 // bit32.band(a, b) → AND
	IntrinsicBor      = 3 // bit32.bor(a, b) → ORR
	IntrinsicBnot     = 4 // bit32.bnot(a) → MVN
	IntrinsicLshift   = 5 // bit32.lshift(a, n) → LSL
	IntrinsicRshift   = 6 // bit32.rshift(a, n) → LSR
)

// Trace is a recorded execution trace (one loop iteration).
type Trace struct {
	ID        int
	LoopPC    int              // bytecode PC of the loop back-edge
	LoopProto *vm.FuncProto    // function containing the loop
	IR        []TraceIR        // recorded instruction stream
	EntryPC   int              // bytecode PC where the trace starts
	StartBase    int              // base register index of the traced function
	Constants    []runtime.Value  // trace-level constant pool (includes inlined function constants)
	HasSelfCalls bool             // true if trace contains self-recursive CALL
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
	useSSA    bool // if true, try SSA codegen for integer-only traces
	startBase int  // base register of the traced function (set on first instruction)

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

// SetUseSSA enables SSA-based codegen for integer-only traces.
func (r *TraceRecorder) SetUseSSA(on bool) {
	r.useSSA = on
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

	// Set startBase on first instruction
	if len(r.current.IR) == 0 && r.current.StartBase == 0 {
		r.startBase = base
		r.current.StartBase = base
		// Copy the root function's constants as the initial trace constants
		r.current.Constants = make([]runtime.Value, len(proto.Constants))
		copy(r.current.Constants, proto.Constants)
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

	// Register offset: remap from absolute base to trace-relative
	baseOff := base - r.startBase

	ir := TraceIR{
		Op:    op,
		A:     baseOff + a, // remap to trace-relative
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

	// Remap B and C register operands to trace-relative
	// (RK operands >= RKBit are constants, handled separately)
	if b < vm.RKBit {
		ir.B = baseOff + b
	}
	if c < vm.RKBit {
		ir.C = baseOff + c
	}

	// For inlined functions (depth > 0), remap constant references
	// by copying constants into the trace's constant pool
	if r.depth > 0 {
		if b >= vm.RKBit {
			constIdx := b - vm.RKBit
			if constIdx < len(proto.Constants) {
				traceConstIdx := len(r.current.Constants)
				r.current.Constants = append(r.current.Constants, proto.Constants[constIdx])
				ir.B = traceConstIdx + vm.RKBit
			}
		}
		if c >= vm.RKBit {
			constIdx := c - vm.RKBit
			if constIdx < len(proto.Constants) {
				traceConstIdx := len(r.current.Constants)
				r.current.Constants = append(r.current.Constants, proto.Constants[constIdx])
				ir.C = traceConstIdx + vm.RKBit
			}
		}
		// Remap BX for LOADK, GETGLOBAL, GETFIELD (constant index)
		switch op {
		case vm.OP_LOADK:
			if ir.BX < len(proto.Constants) {
				traceConstIdx := len(r.current.Constants)
				r.current.Constants = append(r.current.Constants, proto.Constants[ir.BX])
				ir.BX = traceConstIdx
			}
		case vm.OP_GETFIELD:
			// C is the constant index for the field name
			origC := vm.DecodeC(inst)
			if origC < len(proto.Constants) {
				traceConstIdx := len(r.current.Constants)
				r.current.Constants = append(r.current.Constants, proto.Constants[origC])
				ir.C = traceConstIdx // not RK, just constant index
			}
		}
	}

	// Capture type info
	ir.AType = safeRegType(regs, base+a)
	if vm.DecodeB(inst) < vm.RKBit {
		ir.BType = safeRegType(regs, base+vm.DecodeB(inst))
	} else {
		constIdx := vm.DecodeB(inst) - vm.RKBit
		if constIdx < len(proto.Constants) {
			ir.BType = proto.Constants[constIdx].Type()
		}
	}
	if vm.DecodeC(inst) < vm.RKBit {
		ir.CType = safeRegType(regs, base+vm.DecodeC(inst))
	} else {
		constIdx := vm.DecodeC(inst) - vm.RKBit
		if constIdx < len(proto.Constants) {
			ir.CType = proto.Constants[constIdx].Type()
		}
	}

	// Handle CALL: try to inline
	if op == vm.OP_CALL {
		return r.handleCall(ir, regs, base)
	}

	// Handle RETURN from inlined function
	if op == vm.OP_RETURN && r.depth > 0 {
		r.depth--
		return false
	}

	// Check for unsupported ops that abort recording
	if r.shouldAbort(op) {
		r.abortTrace()
		return false
	}

	// Detect JMP that exits the loop (break statement).
	// If JMP target is past the loop's FORLOOP PC, abort — the trace
	// can't capture the break path.
	if op == vm.OP_JMP && r.depth == 0 {
		jmpTarget := pc + vm.DecodesBx(inst) + 1
		if jmpTarget > r.current.LoopPC {
			// This JMP exits the loop (break) — abort recording
			r.abortTrace()
			return false
		}
	}

	r.current.IR = append(r.current.IR, ir)
	return false
}

// handleCall attempts to inline a function call into the trace.
func (r *TraceRecorder) handleCall(ir TraceIR, regs []runtime.Value, base int) bool {
	if r.depth >= r.maxDepth {
		// Too deep — record as a CALL (will be side-exit in compilation)
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Check if the callee is a VM closure we can inline
	// ir.A is trace-relative; add startBase to get absolute register index
	fnVal := regs[r.startBase+ir.A]
	if !fnVal.IsFunction() {
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	cl, ok := fnVal.Ptr().(*vm.Closure)
	if !ok || cl == nil {
		// Check for intrinsic GoFunctions (bit32.bxor, etc.)
		if gf := fnVal.GoFunction(); gf != nil {
			if intrinsic := recognizeIntrinsic(gf.Name); intrinsic != IntrinsicNone {
				ir.Intrinsic = intrinsic
				r.current.IR = append(r.current.IR, ir)
				return false
			}
		}
		// Unknown GoFunction — side-exit
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Check for self-recursion: callee is the same function as the trace's loop function
	if cl.Proto == r.current.LoopProto {
		// Self-recursive call — record as CALL (trace compiler handles natively)
		ir.IsSelfCall = true
		r.current.HasSelfCalls = true
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Check if callee has a for-loop (FORPREP) — can't inline those
	for _, inst := range cl.Proto.Code {
		if vm.DecodeOp(inst) == vm.OP_FORPREP {
			// Callee has nested loop — record as CALL (side-exit)
			r.current.IR = append(r.current.IR, ir)
			return false
		}
	}

	// Simple callee without loops: inline it
	r.depth++
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
	r.startBase = 0 // will be set on first OnInstruction call
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
			key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
			compiled := false

			// Try SSA codegen first for integer-only traces
			if r.useSSA {
				ssaFunc := BuildSSA(r.current)
				ssaFunc = OptimizeSSA(ssaFunc)
				if ssaIsIntegerOnly(ssaFunc) {
					ct, err := CompileSSA(ssaFunc)
					if err == nil {
						r.compiled[key] = ct
						compiled = true
					}
				}
			}

			// Fall back to regular trace compiler
			if !compiled {
				ct, err := compileTrace(r.current)
				if err == nil {
					r.compiled[key] = ct
				}
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

// recognizeIntrinsic returns the intrinsic ID for a known GoFunction, or 0.
func recognizeIntrinsic(name string) int {
	switch name {
	case "bit32.bxor":
		return IntrinsicBxor
	case "bit32.band":
		return IntrinsicBand
	case "bit32.bor":
		return IntrinsicBor
	case "bit32.bnot":
		return IntrinsicBnot
	case "bit32.lshift":
		return IntrinsicLshift
	case "bit32.rshift":
		return IntrinsicRshift
	}
	return IntrinsicNone
}

// safeRegType returns the type of a register, handling out-of-range gracefully.
func safeRegType(regs []runtime.Value, idx int) runtime.ValueType {
	if idx < 0 || idx >= len(regs) {
		return runtime.TypeNil
	}
	return regs[idx].Type()
}
