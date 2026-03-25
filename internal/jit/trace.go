//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TraceCallHandler executes an external function call on behalf of trace JIT code.
type TraceCallHandler func(runtime.Value, []runtime.Value) ([]runtime.Value, error)

// TraceRecorder captures instructions during recording mode.
type TraceRecorder struct {
	traces    []*Trace
	current   *Trace
	recording bool
	depth     int // inline call depth
	maxDepth  int // max inline depth
	skipDepth int // >0: skip instructions from non-inlined callee (decremented on RETURN)
	maxLen    int // max trace length
	compile   bool
	debug     bool
	startBase int // base register of the traced function (set on first instruction)

	// inlineCallProto is the proto of the function being inlined (depth > 0).
	// Set by handleCall when inlining starts. Used to detect Method JIT
	// partial execution: if the first instruction from the callee doesn't
	// start at PC=0, the Method JIT ran part of it, and we fall back to
	// treating the CALL as non-inlined.
	inlineCallProto *vm.FuncProto
	inlineCallIR    *TraceIR // the CALL instruction IR, saved in case we need to emit it
	inlineCallDepth int      // depth before inlining started

	// skipNextJIT is set to true when handleCall decides to inline a function.
	// The VM checks this flag before attempting Method JIT execution for the callee.
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
	// After too many aborts, the loop is blacklisted to avoid repeated start-abort cycles.
	abortCounts map[loopKey]int

	// callHandler executes external function calls for traces with SSA_CALL.
	callHandler TraceCallHandler

	// inlineCallStack tracks the trace-relative call-destination slot for each
	// inline depth level. When handleCall inlines a function, the call register
	// (ir.A in trace-relative coords) is pushed here. When RETURN at depth > 0
	// is encountered, we pop the call register and emit a synthetic MOVE from the
	// callee's return register to the caller's call register.
	inlineCallStack []int

	// pendingGlobalCapture: GETGLOBAL constant capture is deferred because
	// OnInstruction is called BEFORE the VM executes the instruction, so
	// regs[base+a] still holds the pre-execution value. We capture at the
	// NEXT instruction call.
	pendingGlobalCapture    bool
	pendingGlobalCaptureIdx int // index into current.IR of the GETGLOBAL TraceIR
	pendingGlobalCaptureReg int // absolute register index (base+a)
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
	// before a loop is permanently blacklisted.
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

// SetDebug enables debug logging for trace compilation.
func (r *TraceRecorder) SetDebug(on bool) {
	r.debug = on
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
			// Inner loop back-edge during skip -- ignore
			return false
		}
		// Full nested recording: inner loop back-edge during body recording
		if r.innerLoopDepth > 0 && pc == r.innerLoopForPC {
			return false
		}
		// Finish the trace when we see ANY loop's back-edge while recording.
		if r.current != nil {
			r.finishTrace()
		}
		return false
	}

	key := loopKey{proto: proto, pc: pc}

	// Fast path: check compiled trace cache first
	if ct, ok := r.compiled[key]; ok {
		if ct.blacklisted {
			proto.BlacklistTracePC(pc)
			return false
		}
		r.pendingTrace = ct
		return true
	}

	// Fast reject: blacklisted loops
	if r.blacklist[key] {
		proto.BlacklistTracePC(pc)
		return false
	}

	// Slow path: track hotness and start recording
	r.loopCounts[key]++
	if r.loopCounts[key] >= r.threshold {
		r.startTrace(pc, proto)
	}
	return false
}

// TryExecuteCompiled checks if a compiled trace exists for this loop.
// Unlike OnLoopBackEdge, this does NOT increment hotness counts or start recording.
// Safe to call during nested trace execution (call-exit handlers).
func (r *TraceRecorder) TryExecuteCompiled(pc int, proto *vm.FuncProto) bool {
	if r.recording {
		return false
	}
	key := loopKey{proto: proto, pc: pc}
	if ct, ok := r.compiled[key]; ok {
		if ct.blacklisted {
			proto.BlacklistTracePC(pc)
			return false
		}
		r.pendingTrace = ct
		return true
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
	r.lastExecuted = ct
	if ct == nil {
		return nil
	}
	return ct
}

func (r *TraceRecorder) startTrace(pc int, proto *vm.FuncProto) {
	r.recording = true
	r.depth = 0
	r.skipDepth = 0
	r.startBase = 0
	r.innerLoopSkipStart = 0
	r.innerLoopSkipEnd = 0
	r.innerLoopDepth = 0
	r.innerLoopForPC = 0
	r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
	r.inlineCallProto = nil
	r.inlineCallIR = nil
	r.inlineCallStack = r.inlineCallStack[:0]
	r.pendingGlobalCapture = false
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

		// Check for nested loop structures.
		var innerForloopPC int
		hasFullNesting := false
		for _, ir := range r.current.IR {
			if ir.Op == vm.OP_FORPREP {
				if ir.FieldIndex > 0 {
					innerForloopPC = ir.FieldIndex
					break
				}
				hasFullNesting = true
			}
		}

		if debugTrace {
			fmt.Printf("[TRACE-DEBUG] finishTrace: compile=%v PC=%d nIR=%d hasFullNesting=%v\n",
				r.compile, r.current.LoopPC, len(r.current.IR), hasFullNesting)
			for i, ir := range r.current.IR {
				fmt.Printf("[TRACE-IR] %d: op=%s A=%d B=%d C=%d BX=%d PC=%d depth=%d AType=%d BType=%d CType=%d\n",
					i, vm.OpName(ir.Op), ir.A, ir.B, ir.C, ir.BX, ir.PC, ir.Depth, ir.AType, ir.BType, ir.CType)
			}
		}

		if r.compile {
			key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
			compiled := false

			// SSA codegen pipeline (functions defined in ssa_*.go files)
			ssaFunc := BuildSSA(r.current)
			ssaFunc = OptimizeSSA(ssaFunc)
			ssaFunc = ConstHoist(ssaFunc)
			ssaFunc = CSE(ssaFunc)
			ssaFunc = FuseMultiplyAdd(ssaFunc)
			ssaOK := ssaIsIntegerOnly(ssaFunc)
			ssaUseful := SSAIsUseful(ssaFunc)

			if debugTrace {
				fmt.Printf("[TRACE-DEBUG] PC=%d intOnly=%v useful=%v nInsts=%d\n",
					r.current.LoopPC, ssaOK, ssaUseful, len(ssaFunc.Insts))
			}

			if ssaOK && ssaUseful {
				ct, err := CompileSSA(ssaFunc)
				if debugTrace && err != nil {
					fmt.Printf("[TRACE-DEBUG] CompileSSA error: %v\n", err)
				}
				if err == nil {
					// If trace has call-exits but no handler, skip compilation
					// (the trace would immediately side-exit on every call, wasting cycles)
					if ct.hasCallExit && r.callHandler == nil {
						if r.debug {
							fmt.Printf("[TRACE] Skipped: PC=%d, has call-exit but no handler\n", r.current.LoopPC)
						}
					} else {
						if ct.hasCallExit && r.callHandler != nil {
							ct.callHandler = r.callHandler
						}
						if innerForloopPC > 0 {
							innerKey := loopKey{proto: r.current.LoopProto, pc: innerForloopPC}
							if innerCT, ok := r.compiled[innerKey]; ok {
								ct.innerTrace = innerCT
							}
						}
						r.compiled[key] = ct
						compiled = true
						if r.debug {
							fmt.Printf("[TRACE] SSA compiled: PC=%d, %d IR instructions, %d bytes code",
								r.current.LoopPC, len(r.current.IR), ct.code.Size())
							if ct.hasCallExit {
								fmt.Printf(" (has call-exit)")
							}
							if ct.innerTrace != nil {
								fmt.Printf(" (calls inner trace at FORLOOP PC=%d)", innerForloopPC)
							}
							fmt.Println()
						}
					}
				} else if r.debug {
					fmt.Printf("[TRACE] SSA compile error: PC=%d, err=%v\n", r.current.LoopPC, err)
				}
			} else if r.debug {
				fmt.Printf("[TRACE] SSA rejected: PC=%d, %d IRs\n", r.current.LoopPC, len(r.current.IR))
			}

			if !compiled {
				r.blacklist[key] = true
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
	r.pendingGlobalCapture = false
}

// abortTrace stops recording and discards the current trace.
// Tracks abort count per loop key; after MaxAbortBeforeBlacklist aborts,
// the loop is permanently blacklisted.
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
	r.pendingGlobalCapture = false
}

// abortAndBlacklist aborts and permanently blacklists the loop.
// Used for structural limitations that won't change between attempts.
func (r *TraceRecorder) abortAndBlacklist() {
	if r.current != nil {
		key := loopKey{proto: r.current.LoopProto, pc: r.current.LoopPC}
		r.blacklist[key] = true
		r.current.LoopProto.BlacklistTracePC(r.current.LoopPC)
	}
	r.abortTrace()
}

// debugTrace enables verbose trace recording/compilation logging.
const debugTrace = false

// --- TraceContext and CompiledTrace ---

// TraceContext bridges compiled trace code and Go.
type TraceContext struct {
	Regs           uintptr // input: pointer to vm.regs[base]
	Constants      uintptr // input: pointer to trace constants[0]
	ExitPC         int64   // output: bytecode PC where trace exited
	ExitCode       int64   // output: 0=loop done, 1=side exit, 2=guard fail, 3=call-exit
	InnerCode      uintptr // input: code pointer for inner trace (sub-trace calling)
	InnerConstants uintptr // input: constants pointer for inner trace
	ResumePC       int64   // input: bytecode PC to resume at after call-exit
	ExitSnapIdx    int64   // output: which snapshot to restore on exit
	// ExitState: saved trace registers for snapshot restore
	ExitGPR [4]int64   // X20, X21, X22, X23
	ExitFPR [8]float64 // D4-D11
}

// TraceContext field offsets for ARM64 codegen.
const (
	TraceCtxOffRegs           = 0
	TraceCtxOffConstants      = 8
	TraceCtxOffExitPC         = 16
	TraceCtxOffExitCode       = 24
	TraceCtxOffInnerCode      = 32
	TraceCtxOffInnerConstants = 40
	TraceCtxOffResumePC       = 48
	TraceCtxOffExitSnapIdx    = 56
	TraceCtxOffExitGPR        = 64  // 4 * 8 = 32 bytes
	TraceCtxOffExitFPR        = 96  // 8 * 8 = 64 bytes
	TraceCtxSize              = 160 // total size
)

func init() {
	var ctx TraceContext
	if unsafe.Offsetof(ctx.Regs) != TraceCtxOffRegs {
		panic("jit: TraceContext.Regs offset mismatch")
	}
	if unsafe.Offsetof(ctx.Constants) != TraceCtxOffConstants {
		panic("jit: TraceContext.Constants offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitPC) != TraceCtxOffExitPC {
		panic("jit: TraceContext.ExitPC offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitCode) != TraceCtxOffExitCode {
		panic("jit: TraceContext.ExitCode offset mismatch")
	}
	if unsafe.Offsetof(ctx.InnerCode) != TraceCtxOffInnerCode {
		panic("jit: TraceContext.InnerCode offset mismatch")
	}
	if unsafe.Offsetof(ctx.InnerConstants) != TraceCtxOffInnerConstants {
		panic("jit: TraceContext.InnerConstants offset mismatch")
	}
	if unsafe.Offsetof(ctx.ResumePC) != TraceCtxOffResumePC {
		panic("jit: TraceContext.ResumePC offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitSnapIdx) != TraceCtxOffExitSnapIdx {
		panic("jit: TraceContext.ExitSnapIdx offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitGPR) != TraceCtxOffExitGPR {
		panic("jit: TraceContext.ExitGPR offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitFPR) != TraceCtxOffExitFPR {
		panic("jit: TraceContext.ExitFPR offset mismatch")
	}
}

// SideExitBlacklistThreshold is the minimum number of executions before
// blacklisting is considered.
const SideExitBlacklistThreshold = 50

// SideExitBlacklistRatio is the minimum side-exit ratio to trigger blacklisting.
const SideExitBlacklistRatio = 0.95

// CompiledTrace holds native code for a trace.
type CompiledTrace struct {
	code      *CodeBlock
	proto     *vm.FuncProto
	loopPC    int             // PC of the FORLOOP instruction this trace was compiled for
	constants []runtime.Value // trace-level constant pool

	// Sub-trace calling: if this trace contains a CALL_INNER_TRACE,
	// innerTrace points to the compiled inner loop trace.
	innerTrace *CompiledTrace

	// hasCallExit indicates this trace contains SSA_CALL instructions
	// that require call-exit re-entry (ExitCode=3).
	hasCallExit bool

	// callHandler executes external function calls for call-exit support.
	callHandler TraceCallHandler

	// Snapshot-based state restore
	snapshots []Snapshot       // snapshots from SSA compilation
	regAlloc  map[SSARef]int   // SSARef -> register index for restore

	// Blacklisting: tracks whether this trace is doing useful work.
	sideExitCount  int
	fullRunCount   int
	guardFailCount int
	blacklisted    bool
}

// guardFailBlacklistThreshold is the number of consecutive guard failures
// before a trace is blacklisted.
const guardFailBlacklistThreshold = 5

// --- SSA pipeline forward declarations ---
// BuildSSA and OptimizeSSA are defined in ssa_build.go.
// ConstHoist, CSE, FuseMultiplyAdd, ssaIsIntegerOnly, SSAIsUseful, CompileSSA
// are defined in ssa_emit.go.
