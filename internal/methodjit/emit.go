//go:build darwin && arm64

// emit.go defines the shared data structures for ARM64 code generation in
// the Method JIT. Contains ExecContext (the Go/JIT calling convention struct),
// exit code and table operation constants, field offset variables, pinned
// register aliases, and the CompiledFunction struct.
//
// The actual code generation is split across:
//   - emit_compile.go: Tier 2 compile pipeline (Compile, emitContext, prologue/epilogue)
//   - emit_dispatch.go: instruction dispatch, phi moves, control flow
//   - emit_arith.go: arithmetic and comparison emission
//   - emit_call.go: float ops, typed float binop, neg, div, guards
//   - emit_call_exit.go: call-exit and global-exit emission
//   - emit_call_native.go: native BLR call (spill/reload around BLR)
//   - emit_execute.go: CompiledFunction.Execute loop and exit handlers
//   - emit_op_exit.go: generic op-exit and SetList exit emission
//   - emit_reg.go: register resolution helpers
//   - emit_table.go: table operation emission (native + IC)
//   - emit_loop.go: loop analysis

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Pinned register aliases (must match trace JIT convention).
const (
	mRegCtx     = jit.X19 // ExecContext pointer
	mRegTagInt  = jit.X24 // NaN-boxing int tag 0xFFFE000000000000
	mRegTagBool = jit.X25 // NaN-boxing bool tag 0xFFFD000000000000
	mRegRegs    = jit.X26 // VM register base pointer
	mRegConsts  = jit.X27 // constants pointer
)

// nb64 converts a uint64 NaN-boxing constant to int64 for LoadImm64.
func nb64(v uint64) int64 { return int64(v) }

// ExecContext is the calling convention struct between Go and JIT code.
// Passed via X0 from callJIT trampoline, saved into X19.
type ExecContext struct {
	Regs         uintptr // pointer to vm.regs[base]
	Constants    uintptr // pointer to proto.Constants[0] (or 0 if none)
	ExitCode     int64   // 0 = normal, 2 = deopt, 3 = call-exit, 4 = global-exit, 5 = table-exit
	ReturnPC     int64   // unused for now
	CallSlot     int64   // VM register slot of the function value (call-exit)
	CallNArgs    int64   // number of arguments for call-exit
	CallNRets    int64   // number of expected results for call-exit
	CallID       int64   // instruction ID for resolving resume address
	GlobalSlot   int64   // VM register slot for global-exit result
	GlobalConst  int64   // constant pool index for global name (global-exit)
	GlobalExitID int64   // instruction ID for resolving global-exit resume address
	// Table-exit fields (ExitCode=5): for OpNewTable, OpGetTable, OpSetTable
	TableOp      int64 // 0=NewTable, 1=GetTable, 2=SetTable, 3=GetField(deopt), 4=SetField(deopt)
	TableSlot    int64 // VM register slot for the table (or result slot for NewTable)
	TableKeySlot int64 // VM register slot for the key (GetTable/SetTable)
	TableValSlot int64 // VM register slot for the value (SetTable)
	TableAux     int64 // Aux data: NewTable=arrayHint, GetField/SetField=constIdx
	TableAux2    int64 // Aux2 data: NewTable=hashHint
	TableExitID  int64 // instruction ID for resolving resume address
	// Op-exit fields (ExitCode=6): generic exit for unsupported ops
	OpExitOp   int64 // which Op to execute (cast to Op)
	OpExitSlot int64 // destination slot for result
	OpExitArg1 int64 // operand 1 slot (or constant index)
	OpExitArg2 int64 // operand 2 slot (or constant index)
	OpExitAux  int64 // auxiliary data (e.g., constant pool index for strings)
	OpExitID   int64 // resume point ID (instruction ID)
	// Baseline JIT fields (ExitCode=7): bytecode-level op-exit
	BaselineOp int64 // vm.Opcode of the bytecode being executed
	BaselinePC int64 // bytecodePC of the NEXT instruction (resume point)
	BaselineA  int64 // decoded A field from the instruction
	BaselineB  int64 // decoded B field (or Bx for ABx format)
	BaselineC  int64 // decoded C field
	// Baseline JIT native table access support
	BaselineFieldCache      uintptr // pointer to proto.FieldCache[0] (nil if not yet allocated)
	BaselineClosurePtr      uintptr // pointer to *vm.Closure (for GETUPVAL/SETUPVAL)
	BaselineReturnValue     uint64  // NaN-boxed return value (set by RETURN, read by Execute)
	BaselineGlobalCache     uintptr // pointer to BaselineFunc.GlobalValCache[0] (for native GETGLOBAL)
	BaselineGlobalGenPtr    uintptr // pointer to engine.globalCacheGen (for version check)
	BaselineGlobalCachedGen uint64  // engine.globalCacheGen at time bf's cache was populated
	BaselineCallCache       uintptr // pointer to BaselineFunc.CallCache[0] (for native CALL)
	BaselineFeedbackPtr     uintptr // pointer to proto.Feedback[0] (for Tier 1 type feedback collection)
	// Caller context fields: used for JIT-to-JIT calls to save/restore caller state.
	CallerRegs      uintptr // caller's VM register base pointer (saved before callee entry)
	CallerConstants uintptr // caller's constants pointer (saved before callee entry)
	// Native call mode: 0 = normal entry (full prologue), 1 = direct entry (lightweight prologue).
	// RETURN checks this to decide between baseline_exit and direct_exit.
	CallMode int64
	// Native call exit fields (ExitCode=8): when a native BLR callee hits exit-resume.
	NativeCallA            int64   // caller's A field (destination slot)
	NativeCallB            int64   // caller's B field (arg count)
	NativeCallC            int64   // caller's C field (return count)
	NativeCalleeBaseOff    int64   // callee base offset from caller regs (MaxStack*8)
	NativeCalleeResumePC   int64   // callee's resume PC (saved before caller restores its own BaselinePC)
	NativeCalleeClosurePtr uintptr // callee's closure pointer (saved before caller restores its own ClosurePtr)
	// Register file bounds: pointer one past the last valid register slot.
	// Used by native BLR to detect when the callee's register window would
	// exceed the allocated register file, falling to slow path instead.
	RegsEnd uintptr
	// RegsBase is the pointer to regs[0] (start of the register file).
	// Used together with TopPtr for C=0/B=0 variable-arg/variable-return calls.
	RegsBase uintptr
	// TopPtr is a pointer to vm.top (int). Used by native BLR to set Top
	// after a C=0 CALL (variable returns) and read Top for B=0 (variable args).
	TopPtr uintptr
	// NativeCallDepth tracks the current depth of nested native BLR calls.
	// Incremented before BLR, decremented after. When it exceeds
	// maxNativeCallDepth, the BLR path falls to slow path (exit-resume)
	// to prevent native stack overflow. The slow path goes through Go which
	// triggers goroutine stack growth as needed.
	NativeCallDepth int64

	// OSRCounter is decremented on each FORLOOP back-edge in Tier 1.
	// When it reaches 0, the JIT exits with ExitOSR so the TieringManager
	// can compile Tier 2 and re-enter the function at Tier 2 speed.
	// Set to -1 to disable OSR (e.g., after a failed Tier 2 compilation).
	OSRCounter int64

	// Tier 2 global value cache fields. Mirrors Tier 1's per-PC global
	// cache but uses a per-GetGlobal-instruction index instead of per-PC.
	// Cache hit: load value directly from cache (~5ns).
	// Cache miss: exit-resume to Go which populates the cache.
	Tier2GlobalCache    uintptr // pointer to CompiledFunction.GlobalCache[0]
	Tier2GlobalCacheGen uintptr // pointer to CompiledFunction.GlobalCacheGen
	Tier2GlobalGenPtr   uintptr // pointer to tier1.globalCacheGen (shared counter)
	GlobalCacheIdx      int64   // cache index for current GetGlobal (set by emitter on exit)
	Tier2GlobalArray    uintptr // pointer to VM.globalArray[0] for indexed globals
	Tier2GlobalIndex    uintptr // pointer to []int32 const-index -> globalArray index
	Tier2GlobalVerPtr   uintptr // pointer to VM.globalVer
	Tier2GlobalVer      uint64  // VM.globalVer captured when the array pointer was prepared

	// Tier 2 monomorphic call IC (R108). Each OpCall in the compiled code
	// gets a 2-uint64 cache slot: [boxed_closure_value, direct_entry_addr].
	// On hit (loaded fn value == cached), skip closure type checks + Proto
	// lookup + DirectEntry lookup — just use the cached direct entry.
	// On miss, take the full path (which updates the cache on success).
	// Pointer is set by executeTier2 to &CompiledFunction.CallCache[0].
	Tier2CallCache uintptr

	// ExitResumePC is the bytecode PC of a precise interpreter continuation.
	// Tier 1 int-spec overflow and selected Tier 2 guards set it so Execute can
	// resume at the exact guard PC instead of restarting at pc=0, which would
	// replay earlier side effects.
	ExitResumePC int64

	// DeoptInstrID is written by emitDeopt (and related deopt exits) with the
	// IR Instr.ID that triggered the bail-out. Diagnostic infrastructure only —
	// it lets the tiering manager / tests identify which guard fired without
	// re-running disasm. Zero value means "no deopt" or "deopt site did not
	// populate the field".
	DeoptInstrID int64

	// ResumeNumericPass is written by resumable Tier 2 exits before setting
	// ExitCode. It selects the pass-specific resume entry after Go handles the
	// exit. Zero means normal pass; non-zero means numeric pass.
	ResumeNumericPass int64

	// ExitResumeCheckShadow points at a debug-only []runtime.Value shadow
	// buffer. When GSCRIPT_EXIT_RESUME_CHECK=1 at Tier 2 compile time, exit
	// stubs mirror materialized live register values here before returning to
	// Go so the execute loop can verify VM home-slot consistency.
	ExitResumeCheckShadow uintptr
}

// ExitCode constants.
const (
	ExitNormal         = 0 // normal return
	ExitDeopt          = 2 // deopt: bail to interpreter for the entire function
	ExitCallExit       = 3 // call-exit: pause JIT, execute call via VM, resume JIT
	ExitGlobalExit     = 4 // global-exit: pause JIT, load global via VM, resume JIT
	ExitTableExit      = 5 // table-exit: pause JIT, do table op via Go, resume JIT
	ExitOpExit         = 6 // op-exit: pause JIT, Go handles the operation, resume JIT
	ExitBaselineOpExit = 7 // baseline op-exit: bytecode-level exit for Tier 1
	ExitNativeCallExit = 8 // native call exit: callee hit exit-resume during BLR call
	ExitOSR            = 9 // OSR: Tier 1 loop counter expired, request Tier 2 compilation
)

// TableOp constants (stored in ExecContext.TableOp).
const (
	TableOpNewTable = 0
	TableOpGetTable = 1
	TableOpSetTable = 2
	TableOpGetField = 3 // deopt fallback for GetField (no field cache)
	TableOpSetField = 4 // deopt fallback for SetField (no field cache)
)

// ExecContext field offsets (must match struct layout above).
var (
	execCtxOffRegs         = int(unsafe.Offsetof(ExecContext{}.Regs))
	execCtxOffConstants    = int(unsafe.Offsetof(ExecContext{}.Constants))
	execCtxOffExitCode     = int(unsafe.Offsetof(ExecContext{}.ExitCode))
	execCtxOffReturnPC     = int(unsafe.Offsetof(ExecContext{}.ReturnPC))
	execCtxOffCallSlot     = int(unsafe.Offsetof(ExecContext{}.CallSlot))
	execCtxOffCallNArgs    = int(unsafe.Offsetof(ExecContext{}.CallNArgs))
	execCtxOffCallNRets    = int(unsafe.Offsetof(ExecContext{}.CallNRets))
	execCtxOffCallID       = int(unsafe.Offsetof(ExecContext{}.CallID))
	execCtxOffGlobalSlot   = int(unsafe.Offsetof(ExecContext{}.GlobalSlot))
	execCtxOffGlobalConst  = int(unsafe.Offsetof(ExecContext{}.GlobalConst))
	execCtxOffGlobalExitID = int(unsafe.Offsetof(ExecContext{}.GlobalExitID))
	execCtxOffTableOp      = int(unsafe.Offsetof(ExecContext{}.TableOp))
	execCtxOffTableSlot    = int(unsafe.Offsetof(ExecContext{}.TableSlot))
	execCtxOffTableKeySlot = int(unsafe.Offsetof(ExecContext{}.TableKeySlot))
	execCtxOffTableValSlot = int(unsafe.Offsetof(ExecContext{}.TableValSlot))
	execCtxOffTableAux     = int(unsafe.Offsetof(ExecContext{}.TableAux))
	execCtxOffTableAux2    = int(unsafe.Offsetof(ExecContext{}.TableAux2))
	execCtxOffTableExitID  = int(unsafe.Offsetof(ExecContext{}.TableExitID))
	execCtxOffOpExitOp     = int(unsafe.Offsetof(ExecContext{}.OpExitOp))
	execCtxOffOpExitSlot   = int(unsafe.Offsetof(ExecContext{}.OpExitSlot))
	execCtxOffOpExitArg1   = int(unsafe.Offsetof(ExecContext{}.OpExitArg1))
	execCtxOffOpExitArg2   = int(unsafe.Offsetof(ExecContext{}.OpExitArg2))
	execCtxOffOpExitAux    = int(unsafe.Offsetof(ExecContext{}.OpExitAux))
	execCtxOffOpExitID     = int(unsafe.Offsetof(ExecContext{}.OpExitID))
	// Baseline JIT fields
	execCtxOffBaselineOp              = int(unsafe.Offsetof(ExecContext{}.BaselineOp))
	execCtxOffBaselinePC              = int(unsafe.Offsetof(ExecContext{}.BaselinePC))
	execCtxOffBaselineA               = int(unsafe.Offsetof(ExecContext{}.BaselineA))
	execCtxOffBaselineB               = int(unsafe.Offsetof(ExecContext{}.BaselineB))
	execCtxOffBaselineC               = int(unsafe.Offsetof(ExecContext{}.BaselineC))
	execCtxOffBaselineFieldCache      = int(unsafe.Offsetof(ExecContext{}.BaselineFieldCache))
	execCtxOffBaselineClosurePtr      = int(unsafe.Offsetof(ExecContext{}.BaselineClosurePtr))
	execCtxOffBaselineReturnValue     = int(unsafe.Offsetof(ExecContext{}.BaselineReturnValue))
	execCtxOffBaselineGlobalCache     = int(unsafe.Offsetof(ExecContext{}.BaselineGlobalCache))
	execCtxOffBaselineGlobalGenPtr    = int(unsafe.Offsetof(ExecContext{}.BaselineGlobalGenPtr))
	execCtxOffBaselineGlobalCachedGen = int(unsafe.Offsetof(ExecContext{}.BaselineGlobalCachedGen))
	execCtxOffBaselineCallCache       = int(unsafe.Offsetof(ExecContext{}.BaselineCallCache))
	execCtxOffBaselineFeedbackPtr     = int(unsafe.Offsetof(ExecContext{}.BaselineFeedbackPtr))
	// Caller context offsets
	execCtxOffCallerRegs      = int(unsafe.Offsetof(ExecContext{}.CallerRegs))
	execCtxOffCallerConstants = int(unsafe.Offsetof(ExecContext{}.CallerConstants))
	// Native call mode offset
	execCtxOffCallMode = int(unsafe.Offsetof(ExecContext{}.CallMode))
	// Native call exit offsets
	execCtxOffNativeCallA            = int(unsafe.Offsetof(ExecContext{}.NativeCallA))
	execCtxOffNativeCallB            = int(unsafe.Offsetof(ExecContext{}.NativeCallB))
	execCtxOffNativeCallC            = int(unsafe.Offsetof(ExecContext{}.NativeCallC))
	execCtxOffNativeCalleeBaseOff    = int(unsafe.Offsetof(ExecContext{}.NativeCalleeBaseOff))
	execCtxOffNativeCalleeResumePC   = int(unsafe.Offsetof(ExecContext{}.NativeCalleeResumePC))
	execCtxOffNativeCalleeClosurePtr = int(unsafe.Offsetof(ExecContext{}.NativeCalleeClosurePtr))
	execCtxOffRegsEnd                = int(unsafe.Offsetof(ExecContext{}.RegsEnd))
	execCtxOffRegsBase               = int(unsafe.Offsetof(ExecContext{}.RegsBase))
	execCtxOffTopPtr                 = int(unsafe.Offsetof(ExecContext{}.TopPtr))
	execCtxOffNativeCallDepth        = int(unsafe.Offsetof(ExecContext{}.NativeCallDepth))
	execCtxOffOSRCounter             = int(unsafe.Offsetof(ExecContext{}.OSRCounter))
	// Tier 2 global cache offsets
	execCtxOffTier2GlobalCache      = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalCache))
	execCtxOffTier2GlobalCacheGen   = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalCacheGen))
	execCtxOffTier2GlobalGenPtr     = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalGenPtr))
	execCtxOffGlobalCacheIdx        = int(unsafe.Offsetof(ExecContext{}.GlobalCacheIdx))
	execCtxOffTier2GlobalArray      = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalArray))
	execCtxOffTier2GlobalIndex      = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalIndex))
	execCtxOffTier2GlobalVerPtr     = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalVerPtr))
	execCtxOffTier2GlobalVer        = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalVer))
	execCtxOffExitResumePC          = int(unsafe.Offsetof(ExecContext{}.ExitResumePC))
	execCtxOffTier2CallCache        = int(unsafe.Offsetof(ExecContext{}.Tier2CallCache))
	execCtxOffDeoptInstrID          = int(unsafe.Offsetof(ExecContext{}.DeoptInstrID))
	execCtxOffResumeNumericPass     = int(unsafe.Offsetof(ExecContext{}.ResumeNumericPass))
	execCtxOffExitResumeCheckShadow = int(unsafe.Offsetof(ExecContext{}.ExitResumeCheckShadow))
)

// CompiledFunction holds the generated native code for a function.
type CompiledFunction struct {
	Code      *jit.CodeBlock // executable memory
	Proto     *vm.FuncProto  // source function
	NumSpills int            // stack space needed for spill slots
	numRegs   int            // total number of VM register slots (including temp slots)

	// ResumeAddrs maps call instruction ID to the native code offset (bytes)
	// of the resume label. Used to re-enter JIT code after a call-exit.
	ResumeAddrs map[int]int

	// NumericResumeAddrs maps resumable exit instruction ID to the numeric
	// pass resume entry. The Go-side exit loop selects this when
	// ExecContext.ResumeNumericPass is set.
	NumericResumeAddrs map[int]int

	// DirectEntryOffset is the byte offset of the BLR-compatible direct entry
	// point within the code block. When non-zero, Tier 1 BLR callers can jump
	// to Code.Ptr() + DirectEntryOffset. The direct entry uses the same full
	// frame as the normal Tier 2 entry so callee-saved registers are preserved.
	DirectEntryOffset int

	// DirectEntrySafe is false when a native caller would have to replay this
	// function from pc=0 after the function may already have performed a
	// visible side effect. In that case Tier 2 stays callable through the
	// execute loop, but direct BLR entries are not published.
	DirectEntrySafe bool

	// NumericParamCount (R124) is the number of int params the numeric
	// entry (t2_numeric_self_entry_N) takes (1-4). Zero if no numeric
	// entry was emitted. The entry label is part of THIS Code block
	// (added at the end by emitEpilogue when proto qualifies) so caller
	// BL is compile-time PC-relative.
	NumericParamCount int

	// NumericEntryOffset is the byte offset of t2_numeric_self_entry_N when
	// NumericParamCount is non-zero. It is diagnostic metadata for tests and
	// disassembly; raw callers branch to the label directly at codegen time.
	NumericEntryOffset int

	// DeoptFunc is called when the JIT bails out (ExitCode=ExitDeopt).
	// It runs the function via the VM interpreter. Set by the caller
	// (e.g., test harness or tiering engine) to provide VM fallback.
	// If nil, Execute returns an error on deopt.
	DeoptFunc func(args []runtime.Value) ([]runtime.Value, error)

	// CallVM is used for call-exit: executing calls via the VM interpreter.
	// Set by the caller. If nil, calls fall back to DeoptFunc.
	CallVM *vm.VM

	// GlobalCache is a per-GetGlobal-instruction NaN-boxed value cache.
	// Indexed by the emitter-assigned cache index (0, 1, 2, ...).
	// Populated on first miss by the global-exit handler in Go.
	// Invalidated when GlobalCacheGen mismatches the engine's globalCacheGen.
	GlobalCache []uint64

	// GlobalCacheGen is the engine's globalCacheGen at the time this
	// function's GlobalCache was last populated. A mismatch means the
	// cache may contain stale values and must be repopulated.
	GlobalCacheGen uint64

	// GlobalCacheConsts maps each Tier 2 GlobalCache index back to the
	// constant-pool index naming that global. It lets SetGlobal invalidate
	// only cache entries for the written global instead of bumping the shared
	// generation and flushing unrelated function/global ICs.
	GlobalCacheConsts []int

	// GlobalIndexByConst maps proto constant indices to VM.globalArray indices
	// for the native indexed global protocol. The tier manager prepares it
	// with the VM and publishes the backing pointer on FuncProto for direct
	// Tier 2 callees.
	GlobalIndexByConst []int32

	// NativeSetGlobals is the set of proto constant indices written by native
	// SetGlobal. The execute loop syncs these array slots back into the legacy
	// globals map before every exit and on normal return.
	NativeSetGlobals map[int]bool

	// CallCache (R108) is a per-OpCall-site monomorphic IC.
	// Layout: 4 × uint64 per call site.
	//   [4*i]   = cached boxed closure value (NaN-boxed 0xFFFF...)
	//   [4*i+1] = cached direct-entry address (uintptr)
	//   [4*i+2] = cached *vm.FuncProto
	//   [4*i+3] = cached direct-entry version
	// Hits validate FuncProto.DirectEntryVersion before reusing the cached
	// entry. Version changes refresh from DirectEntryPtr, then
	// Tier2DirectEntryPtr, preserving fallback when both entries are cleared.
	CallCache []uint64

	// NewTableCaches is indexed by IR instruction ID. Hinted dense table
	// allocation sites refill it on table-exit misses; native NewTable code pops
	// pre-boxed tables from the matching entry until empty.
	NewTableCaches []newTableCacheEntry

	// InstrCodeRanges maps IR instruction IDs to emitted machine-code byte
	// ranges. Diagnostic metadata only; execution never consults it.
	InstrCodeRanges []InstrCodeRange

	// ExitSites maps Tier 2 exit/deopt instruction IDs to production profile
	// metadata used by TieringManager.ExitStats.
	ExitSites map[int]ExitSiteMeta

	// ExitResumeCheck is populated only when GSCRIPT_EXIT_RESUME_CHECK=1 at
	// compile time. Nil keeps the normal execute path at near-zero overhead.
	ExitResumeCheck *exitResumeCheckMetadata
}

func (cf *CompiledFunction) resumeOffset(instrID int, numericPass bool) (int, bool) {
	if numericPass {
		off, ok := cf.NumericResumeAddrs[instrID]
		return off, ok
	}
	off, ok := cf.ResumeAddrs[instrID]
	return off, ok
}

// Execute, executeCallExit, executeGlobalExit, executeTableExit, executeOpExit
// are in emit_execute.go.
