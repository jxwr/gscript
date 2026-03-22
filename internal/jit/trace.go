package jit

import (
	"fmt"

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

	// FieldIndex: for GETFIELD/SETFIELD, the index into table.skeys captured at recording time.
	// -1 means unknown (field not in skeys, or table not accessible).
	FieldIndex int
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
	IntrinsicSqrt     = 7 // math.sqrt(x) → FSQRT
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
	debug     bool // if true, print trace compilation diagnostics
	startBase int  // base register of the traced function (set on first instruction)

	// Inner loop skip range (for sub-trace calling)
	innerLoopSkipStart int // start PC of inner loop body (FORPREP PC + 1)
	innerLoopSkipEnd   int // end PC of inner loop body (FORLOOP PC, inclusive)

	// Full nested loop recording: record one iteration of inner loop body
	// inline into the outer trace (no sub-trace calling needed).
	innerLoopDepth     int  // >0 when recording inside an inner loop body
	innerLoopForPC     int  // FORLOOP PC of the inner loop (for back-edge detection)
	innerLoopFirstSeen bool // true after the initial FORLOOP (right after FORPREP) is seen
	innerLoopRecorded  bool // true after the body+FORLOOP have been recorded

	// Loop hotness tracking
	loopCounts map[loopKey]int
	threshold  int // recording starts after this many iterations

	// Compiled trace cache: keyed by (proto, loopPC)
	compiled     map[loopKey]*CompiledTrace
	pendingTrace *CompiledTrace
	lastExecuted *CompiledTrace // last trace that was executed (for blacklisting)

	// Blacklist: loops where compilation failed (don't retry)
	blacklist map[loopKey]bool

	// Abort tracking: count how many times recording was aborted per loop key.
	// After too many aborts, the loop is blacklisted to avoid repeated start→abort cycles.
	abortCounts map[loopKey]int

	// callHandler executes external function calls for traces with SSA_CALL.
	// Set via SetCallHandler, propagated to compiled traces that need it.
	callHandler TraceCallHandler
}

type loopKey struct {
	proto *vm.FuncProto
	pc    int
}

const (
	DefaultTraceThreshold = 10
	DefaultMaxTraceLen    = 500
	DefaultMaxInlineDepth = 3
	// MaxAbortBeforeBlacklist is the maximum number of aborted recording attempts
	// before a loop is permanently blacklisted. This prevents repeated start→abort
	// cycles for loops where recording is interrupted (e.g., function returns during
	// recording, different back-edge hit during recording).
	MaxAbortBeforeBlacklist = 3
)

// NewTraceRecorder creates a new trace recorder.
func NewTraceRecorder() *TraceRecorder {
	return &TraceRecorder{
		maxDepth:    DefaultMaxInlineDepth,
		maxLen:      DefaultMaxTraceLen,
		threshold:   DefaultTraceThreshold,
		loopCounts:  make(map[loopKey]int),
		compiled:    make(map[loopKey]*CompiledTrace),
		blacklist:   make(map[loopKey]bool),
		abortCounts: make(map[loopKey]int),
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

// SetDebug enables trace compilation diagnostics.
func (r *TraceRecorder) SetDebug(on bool) {
	r.debug = on
}

// SetCallHandler sets the function that executes external calls for trace call-exit support.
func (r *TraceRecorder) SetCallHandler(handler TraceCallHandler) {
	r.callHandler = handler
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
		if r.innerLoopSkipEnd > 0 {
			// Inner loop back-edge during skip — ignore
			return false
		}
		// Full nested recording: inner loop back-edge during body recording
		if r.innerLoopDepth > 0 && pc == r.innerLoopForPC {
			// Inner loop's back-edge — ignore (we're recording the inner body)
			return false
		}
		// Only finish the trace on the SAME loop's back-edge.
		// If a different loop's back-edge is hit, it means we exited the
		// recorded loop and entered a different loop — abort the trace.
		if r.current != nil && pc == r.current.LoopPC {
			r.finishTrace()
		} else {
			// Different loop's back-edge — abort recording
			r.abortTrace()
		}
		return false
	}

	key := loopKey{proto: proto, pc: pc}

	// Fast path: check compiled trace cache first
	if ct, ok := r.compiled[key]; ok {
		if ct.blacklisted {
			// Propagate to proto-level blacklist so the VM skips
			// the interface dispatch on subsequent iterations.
			proto.BlacklistTracePC(pc)
			return false
		}
		r.pendingTrace = ct
		return true
	}

	// Fast reject: blacklisted loops
	if r.blacklist[key] {
		// Propagate to proto-level blacklist so the VM skips
		// the interface dispatch on subsequent iterations.
		proto.BlacklistTracePC(pc)
		return false
	}

	// Slow path: track hotness and start recording
	r.loopCounts[key]++
	if r.loopCounts[key] >= r.threshold {
		r.startTrace(pc, proto)
		// After first recording attempt, if not compiled, blacklist to avoid
		// repeated hash lookups on every iteration
	}
	return false
}

// IsBlacklisted returns true if the loop at (proto, pc) was blacklisted.
func (r *TraceRecorder) IsBlacklisted(pc int, proto *vm.FuncProto) bool {
	return r.blacklist[loopKey{proto: proto, pc: pc}]
}

// RecordSideExit records that a compiled trace side-exited.
func (r *TraceRecorder) RecordSideExit(ct *CompiledTrace) {
	ct.sideExitCount++
	total := ct.sideExitCount + ct.fullRunCount
	if total >= SideExitBlacklistThreshold {
		ratio := float64(ct.sideExitCount) / float64(total)
		if ratio >= SideExitBlacklistRatio {
			ct.blacklisted = true
			// Propagate to proto-level blacklist so the VM skips
			// the interface dispatch on subsequent iterations.
			if ct.proto != nil {
				ct.proto.BlacklistTracePC(ct.loopPC)
			}
		}
	}
}

// RecordFullRun records that a compiled trace completed a full loop.
func (r *TraceRecorder) RecordFullRun(ct *CompiledTrace) {
	ct.fullRunCount++
}

// RecordResult updates side-exit/full-run counters on the last executed trace.
// Called by the VM after every trace execution with the outcome.
func (r *TraceRecorder) RecordResult(sideExit bool) {
	if r.lastExecuted == nil {
		return
	}
	if sideExit {
		r.RecordSideExit(r.lastExecuted)
	} else {
		r.RecordFullRun(r.lastExecuted)
	}
}

// PendingTrace returns the compiled trace to execute (set by OnLoopBackEdge).
// Implements vm.TracePendingHook.
func (r *TraceRecorder) PendingTrace() vm.TraceExecutor {
	ct := r.pendingTrace
	r.pendingTrace = nil
	r.lastExecuted = ct // save for RecordTraceExit
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

	// Inner loop skip: when recording the outer loop and the inner loop body
	// is being executed, skip all instructions until we pass the FORLOOP PC.
	if r.innerLoopSkipEnd > 0 {
		if pc > r.innerLoopSkipEnd {
			// Past inner loop — resume recording
			r.innerLoopSkipEnd = 0
			r.innerLoopSkipStart = 0
			// Reset full-nesting state if this was a full nested recording
			if r.innerLoopDepth > 0 {
				r.innerLoopDepth = 0
				r.innerLoopForPC = 0
				r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
			}
			// Fall through to record this instruction
		} else {
			// Still inside inner loop body — skip
			return false
		}
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

	// For comparison opcodes (EQ, LT, LE), the A field is a boolean flag
	// (0 or 1), NOT a register index. Do not remap it with baseOff.
	remappedA := baseOff + a
	switch op {
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
		remappedA = a // A is a flag, not a register
	}

	ir := TraceIR{
		Op:    op,
		A:     remappedA,
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

	// Capture global VALUE for GETGLOBAL (snapshot at recording time).
	// The interpreter already executed GETGLOBAL, so regs[base+a] has the value.
	// We store it as a trace constant so the compiled trace can reload it each iteration.
	if op == vm.OP_GETGLOBAL {
		absSlot := base + a
		if absSlot < len(regs) {
			constIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, regs[absSlot])
			ir.BX = constIdx // repurpose BX to point to the value constant
		}
	}

	// Capture field index for GETFIELD/SETFIELD (skeys position at recording time)
	ir.FieldIndex = -1
	if op == vm.OP_GETFIELD || op == vm.OP_SETFIELD {
		origB := vm.DecodeB(inst)
		tableSlot := base + origB
		if op == vm.OP_SETFIELD {
			tableSlot = base + a
		}
		if tableSlot < len(regs) && regs[tableSlot].IsTable() {
			tbl := regs[tableSlot].Table()
			if tbl != nil {
				// Get field name from proto constants (use original C, not remapped)
				origC := vm.DecodeC(inst)
				if origC < len(proto.Constants) {
					fieldName := proto.Constants[origC].Str()
					ir.FieldIndex = tbl.FieldIndex(fieldName)
				}
			}
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

	// Check for unsupported ops that abort recording.
	// These are structural limitations (nested loops, concurrency) that won't
	// change between attempts, so blacklist permanently.
	if r.shouldAbort(op) {
		r.abortAndBlacklist()
		return false
	}

	// Handle FORPREP for nested loop (SSA only).
	// Two strategies:
	//   1. Full nesting: record one inner iteration inline (no sub-trace call).
	//      Eliminates ~61 instruction prologue/epilogue per inner call.
	//   2. Sub-trace calling: skip inner body, call pre-compiled inner trace.
	//      Fallback when full nesting is already in use (deeper nesting).
	// Priority: full nesting first (better codegen, unified register allocation).
	if op == vm.OP_FORPREP && r.useSSA && r.depth == 0 {
		forloopPC := pc + ir.SBX + 1

		// Strategy 0: When already in full-nesting mode (innerLoopDepth > 0) and
		// a compiled inner trace exists, use sub-trace calling to avoid triple nesting.
		// Example: y-loop (full nesting x-loop) encounters iter-loop FORPREP —
		// use compiled iter-loop trace instead of going 3 levels deep.
		if r.innerLoopDepth > 0 {
			innerKey := loopKey{proto: proto, pc: forloopPC}
			if innerCT, ok := r.compiled[innerKey]; ok && innerCT != nil && innerCT.ssaCompiled {
				r.innerLoopSkipStart = pc + 1
				r.innerLoopSkipEnd = forloopPC
				ir.FieldIndex = forloopPC
				r.current.IR = append(r.current.IR, ir)
				return false
			}
		}

		// Strategy 1: Full nested recording (preferred for first level of nesting).
		// Record the FORPREP normally, then record exactly ONE iteration
		// of the inner body. The inner FORLOOP will be recorded too.
		// Remaining inner iterations are skipped via innerLoopSkipEnd.
		if r.innerLoopDepth == 0 {
			r.innerLoopDepth = 1
			r.innerLoopForPC = forloopPC
			r.innerLoopFirstSeen = false
			r.innerLoopRecorded = false
			ir.FieldIndex = 0
			r.current.IR = append(r.current.IR, ir)
			return false
		}

		// Deeper nesting: Strategy 0 already handles compiled inner traces above.
		// If we reach here, there's no compiled trace for this inner loop — abort.
		r.abortAndBlacklist()
		return false
	}

	// Handle inner FORLOOP during full nested recording.
	// The FORPREP jumps directly to the FORLOOP. So the sequence is:
	//   1. FORPREP recorded → interpreter jumps to FORLOOP
	//   2. First FORLOOP encounter (setup): DON'T record. Let interpreter
	//      execute it to check condition and jump to body start.
	//   3. Body instructions: recorded normally.
	//   4. Second FORLOOP encounter (after body): record it, then skip remaining.
	if op == vm.OP_FORLOOP && r.innerLoopDepth > 0 && pc == r.innerLoopForPC {
		if !r.innerLoopFirstSeen {
			// First encounter (right after FORPREP): skip this FORLOOP.
			// The interpreter will execute it, increment idx, check limit,
			// and if the loop continues, jump to the body start.
			r.innerLoopFirstSeen = true
			return false
		}
		if !r.innerLoopRecorded {
			// Second encounter (after one body iteration): record the FORLOOP
			// and set up skip for remaining inner iterations.
			r.innerLoopRecorded = true
			r.current.IR = append(r.current.IR, ir)
			// Skip remaining inner iterations.
			r.innerLoopSkipStart = pc + ir.SBX + 1 // inner body start
			r.innerLoopSkipEnd = pc                  // inner FORLOOP PC (inclusive)
			return false
		}
		// Subsequent encounters should not happen (skip is active)
		return false
	}

	// If we're recording and encounter a FORLOOP that is NOT our recorded loop,
	// the inner loop has exited and we're seeing the outer loop's FORLOOP.
	// Don't record it — the trace is complete without it.
	if op == vm.OP_FORLOOP && r.depth == 0 && pc != r.current.LoopPC {
		// This is an outer FORLOOP — the recorded loop body is complete.
		// Don't record this instruction; finishTrace will be called when
		// OnLoopBackEdge sees this back-edge (and aborts since PC != LoopPC).
		return false
	}

	// Detect unconditional JMP that exits the loop (break statement).
	// Only abort for JMPs NOT preceded by a comparison (those are if-else skips).
	// Break JMPs go past the FORLOOP PC.
	if op == vm.OP_JMP && r.depth == 0 {
		jmpTarget := pc + vm.DecodesBx(inst) + 1
		if jmpTarget > r.current.LoopPC {
			// Check if the previous recorded instruction was a comparison/test.
			// If so, this JMP is a conditional skip (if-else), not a break.
			isConditionalSkip := false
			if len(r.current.IR) > 0 {
				prevOp := r.current.IR[len(r.current.IR)-1].Op
				switch prevOp {
				case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
					isConditionalSkip = true
				}
			}
			if !isConditionalSkip {
				// Unconditional JMP past loop = break
				r.abortTrace()
				return false
			}
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
	absIdx := r.startBase + ir.A
	if absIdx < 0 || absIdx >= len(regs) {
		r.current.IR = append(r.current.IR, ir)
		return false
	}
	fnVal := regs[absIdx]
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
		if r.useSSA {
			return false // SSA codegen handles nested loops via sub-trace calling
		}
		return true // old compiler can't handle nested loops
	}
	return false
}

func (r *TraceRecorder) startTrace(pc int, proto *vm.FuncProto) {
	r.recording = true
	r.depth = 0
	r.startBase = 0 // will be set on first OnInstruction call
	r.innerLoopSkipStart = 0
	r.innerLoopSkipEnd = 0
	r.innerLoopDepth = 0
	r.innerLoopForPC = 0
	r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
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

		// Check if this trace has nested loop structures.
		var innerForloopPC int  // sub-trace calling marker (FieldIndex > 0)
		hasFullNesting := false // full nested loop (FORPREP with FieldIndex == 0 inside loop body)
		for _, ir := range r.current.IR {
			if ir.Op == vm.OP_FORPREP {
				if ir.FieldIndex > 0 {
					innerForloopPC = ir.FieldIndex
					break
				}
				// FieldIndex == 0 means full nesting (inner FORPREP recorded inline)
				hasFullNesting = true
			}
		}

		// Compile the trace if enabled
		if debugSSAStoreBack {
			fmt.Printf("[TRACE-DEBUG] finishTrace: compile=%v PC=%d nIR=%d hasFullNesting=%v hasInlinedCode=", r.compile, r.current.LoopPC, len(r.current.IR), hasFullNesting)
			hasInl := false
			for _, ir2 := range r.current.IR {
				if ir2.Depth > 0 {
					hasInl = true
					break
				}
			}
			fmt.Printf("%v\n", hasInl)
		}
		if r.compile {
			key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
			compiled := false


			// Try SSA codegen first (handles int, float, tables, intrinsics, globals)
			if r.useSSA {
				ssaFunc := BuildSSA(r.current)
				ssaFunc = OptimizeSSA(ssaFunc)
				ssaFunc = ConstHoist(ssaFunc)
				ssaFunc = CSE(ssaFunc)
				ssaFunc = FuseMultiplyAdd(ssaFunc)
				ssaOK := ssaIsIntegerOnly(ssaFunc)
				ssaUseful := SSAIsUseful(ssaFunc)
				if debugSSAStoreBack {
					fmt.Printf("[TRACE-DEBUG] PC=%d intOnly=%v useful=%v nInsts=%d\n", r.current.LoopPC, ssaOK, ssaUseful, len(ssaFunc.Insts))
				}
				if ssaOK && ssaUseful {
					ct, err := CompileSSA(ssaFunc)
					if debugSSAStoreBack && err != nil {
						fmt.Printf("[TRACE-DEBUG] CompileSSA error: %v\n", err)
					}
					if err == nil {
						ct.ssaCompiled = true
						// Wire call handler for traces with call-exit support
						if ct.hasCallExit && r.callHandler != nil {
							ct.callHandler = r.callHandler
						}
						// Attach inner trace if this is an outer loop with sub-trace calling
						if innerForloopPC > 0 {
							innerKey := loopKey{proto: r.current.LoopProto, pc: innerForloopPC}
							if innerCT, ok := r.compiled[innerKey]; ok {
								ct.innerTrace = innerCT
							}
						}
						r.compiled[key] = ct
						compiled = true
						if r.debug {
							fmt.Printf("[TRACE] SSA compiled: PC=%d, %d IR instructions, %d bytes code", r.current.LoopPC, len(r.current.IR), ct.code.Size())
							if ct.hasCallExit {
								fmt.Printf(" (has call-exit)")
							}
							if ct.innerTrace != nil {
								fmt.Printf(" (calls inner trace at FORLOOP PC=%d)", innerForloopPC)
							}
							fmt.Println()
						}
					} else if r.debug {
						fmt.Printf("[TRACE] SSA compile error: PC=%d, err=%v\n", r.current.LoopPC, err)
					}
				} else if r.debug {
					fmt.Printf("[TRACE] SSA rejected: PC=%d, %d IRs\n", r.current.LoopPC, len(r.current.IR))
				}
			}

			// Fall back to regular trace compiler.
			// Skip fallback for full-nesting traces: the regular compiler
			// doesn't understand inner loop structure and produces wrong results.
			// Also skip for traces with inlined functions (depth > 0): the regular
			// compiler's side-exit PCs use the callee's bytecode PCs, but the VM
			// would resume at those PCs in the outer function, causing wrong behavior.
			hasInlinedCode := false
			for _, ir := range r.current.IR {
				if ir.Depth > 0 {
					hasInlinedCode = true
					break
				}
			}
			if !compiled && !hasFullNesting && !hasInlinedCode {
				ct, err := compileTrace(r.current)
				if debugSSAStoreBack {
					if err != nil {
						fmt.Printf("[TRACE-DEBUG] Regular compile error: PC=%d err=%v\n", r.current.LoopPC, err)
					} else {
						fmt.Printf("[TRACE-DEBUG] Regular compiled: PC=%d\n", r.current.LoopPC)
					}
				}
				if err == nil {
					r.compiled[key] = ct
					compiled = true
					if r.debug {
						fmt.Printf("[TRACE] Regular compiled: PC=%d\n", r.current.LoopPC)
					}
				}
			}

			// If compilation failed, blacklist this loop to avoid re-recording
			if !compiled {
				r.blacklist[key] = true
				// Propagate to proto-level blacklist so the VM skips
				// the interface dispatch on subsequent iterations.
				r.current.LoopProto.BlacklistTracePC(r.current.LoopPC)
				if r.debug {
					fmt.Printf("[TRACE] Blacklisted: PC=%d\n", r.current.LoopPC)
				}
			}
		}
	}
	r.current = nil
	r.recording = false
	r.depth = 0
	r.innerLoopSkipStart = 0
	r.innerLoopSkipEnd = 0
	r.innerLoopDepth = 0
	r.innerLoopForPC = 0
	r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
}

// abortTrace stops recording and discards the current trace.
// Tracks abort count per loop key; after MaxAbortBeforeBlacklist aborts,
// the loop is permanently blacklisted to prevent repeated start->abort cycles
// (e.g., short-lived functions that return before the trace completes).
func (r *TraceRecorder) abortTrace() {
	if r.current != nil {
		key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
		r.abortCounts[key]++
		if r.abortCounts[key] >= MaxAbortBeforeBlacklist {
			r.blacklist[key] = true
			r.current.LoopProto.BlacklistTracePC(r.current.LoopPC)
			if r.debug {
				fmt.Printf("[TRACE] Abort-blacklisted: PC=%d (aborted %d times)\n",
					r.current.LoopPC, r.abortCounts[key])
			}
		}
	}
	r.current = nil
	r.recording = false
	r.depth = 0
	r.innerLoopSkipStart = 0
	r.innerLoopSkipEnd = 0
	r.innerLoopDepth = 0
	r.innerLoopForPC = 0
	r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
}

// abortAndBlacklist aborts and permanently blacklists the loop.
// Used for structural limitations (nested loops) that won't change between attempts.
func (r *TraceRecorder) abortAndBlacklist() {
	if r.current != nil {
		key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
		r.blacklist[key] = true
		// Propagate to proto-level blacklist so the VM skips
		// the interface dispatch on subsequent iterations.
		r.current.LoopProto.BlacklistTracePC(r.current.LoopPC)
	}
	r.abortTrace()
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
	case "math.sqrt":
		return IntrinsicSqrt
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
