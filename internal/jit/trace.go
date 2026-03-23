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
	// ShapeID: for GETFIELD/SETFIELD, the table's shapeID at recording time.
	// 0 means unknown or hash-mode table.
	ShapeID uint32
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

// TraceRecorder captures instructions during recording mode.
type TraceRecorder struct {
	traces    []*Trace
	current   *Trace
	recording bool
	depth     int  // inline call depth
	maxDepth  int  // max inline depth
	skipDepth int  // >0: skip instructions from non-inlined callee (decremented on RETURN)
	maxLen    int  // max trace length
	compile   bool // if true, compile traces after recording
	debug     bool // if true, print trace compilation diagnostics
	startBase int  // base register of the traced function (set on first instruction)

	// inlineCallProto is the proto of the function being inlined (depth > 0).
	// Set by handleCall when inlining starts. Used to detect Method JIT
	// partial execution: if the first instruction from the callee doesn't
	// start at PC=0, the Method JIT ran part of it, and we fall back to
	// treating the CALL as non-inlined.
	inlineCallProto *vm.FuncProto
	inlineCallIR    *TraceIR // the CALL instruction IR, saved in case we need to emit it
	inlineCallDepth int       // depth before inlining started

	// skipNextJIT is set to true when handleCall decides to inline a function.
	// The VM checks this flag before attempting Method JIT execution for the callee.
	// This ensures the trace recorder sees all instructions from the callee.
	skipNextJIT bool

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

	// inlineCallStack tracks the trace-relative call-destination slot for each
	// inline depth level. When handleCall inlines a function, the call register
	// (ir.A in trace-relative coords) is pushed here. When RETURN at depth > 0
	// is encountered, we pop the call register and emit a synthetic MOVE from the
	// callee's return register to the caller's call register.
	inlineCallStack []int
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

// ShouldSkipJIT returns true if the trace recorder needs the next callee to
// run in interpreter mode (so all instructions are visible to the recorder).
// Automatically clears the flag after reading.
func (r *TraceRecorder) ShouldSkipJIT() bool {
	if r.skipNextJIT {
		r.skipNextJIT = false
		return true
	}
	return false
}

// SetCallHandler sets the function that executes external calls for trace call-exit support.
func (r *TraceRecorder) SetCallHandler(handler TraceCallHandler) {
	r.callHandler = handler
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

func (r *TraceRecorder) startTrace(pc int, proto *vm.FuncProto) {
	r.recording = true
	r.depth = 0
	r.skipDepth = 0
	r.startBase = 0 // will be set on first OnInstruction call
	r.innerLoopSkipStart = 0
	r.innerLoopSkipEnd = 0
	r.innerLoopDepth = 0
	r.innerLoopForPC = 0
	r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
	r.inlineCallProto = nil
	r.inlineCallIR = nil
	r.inlineCallStack = r.inlineCallStack[:0]
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
			for i, ir2 := range r.current.IR {
				fmt.Printf("[TRACE-IR] %d: op=%s A=%d B=%d C=%d BX=%d PC=%d depth=%d AType=%d BType=%d CType=%d proto=%s\n",
					i, vm.OpName(ir2.Op), ir2.A, ir2.B, ir2.C, ir2.BX, ir2.PC, ir2.Depth, ir2.AType, ir2.BType, ir2.CType, ir2.Proto.Name)
			}
		}
		if r.compile {
			key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
			compiled := false

			// SSA codegen pipeline
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
	r.inlineCallProto = nil
	r.inlineCallIR = nil
	r.skipNextJIT = false
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
	r.inlineCallProto = nil
	r.inlineCallIR = nil
	r.skipNextJIT = false
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

