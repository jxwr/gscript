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
	TableOp       int64  // 0=NewTable, 1=GetTable, 2=SetTable, 3=GetField(deopt), 4=SetField(deopt)
	TableSlot     int64  // VM register slot for the table (or result slot for NewTable)
	TableKeySlot  int64  // VM register slot for the key (GetTable/SetTable)
	TableValSlot  int64  // VM register slot for the value (SetTable)
	TableAux      int64  // Aux data: NewTable=arrayHint, GetField/SetField=constIdx
	TableAux2     int64  // Aux2 data: NewTable=hashHint
	TableExitID   int64  // instruction ID for resolving resume address
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
	BaselineFieldCache   uintptr // pointer to proto.FieldCache[0] (nil if not yet allocated)
	BaselineClosurePtr   uintptr // pointer to *vm.Closure (for GETUPVAL/SETUPVAL)
	BaselineReturnValue    uint64  // NaN-boxed return value (set by RETURN, read by Execute)
	BaselineGlobalCache    uintptr // pointer to BaselineFunc.GlobalValCache[0] (for native GETGLOBAL)
	BaselineGlobalGenPtr   uintptr // pointer to engine.globalCacheGen (for version check)
	BaselineGlobalCachedGen uint64  // engine.globalCacheGen at time bf's cache was populated
	BaselineFeedbackPtr     uintptr // pointer to proto.Feedback[0] (for Tier 1 type feedback collection)
	// Caller context fields: used for JIT-to-JIT calls to save/restore caller state.
	CallerRegs      uintptr // caller's VM register base pointer (saved before callee entry)
	CallerConstants uintptr // caller's constants pointer (saved before callee entry)
	// Native call mode: 0 = normal entry (full prologue), 1 = direct entry (lightweight prologue).
	// RETURN checks this to decide between baseline_exit and direct_exit.
	CallMode int64
	// Native call exit fields (ExitCode=8): when a native BLR callee hits exit-resume.
	NativeCallA            int64 // caller's A field (destination slot)
	NativeCallB            int64 // caller's B field (arg count)
	NativeCallC            int64 // caller's C field (return count)
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

	// Tier 2 monomorphic call IC (R108). Each OpCall in the compiled code
	// gets a 2-uint64 cache slot: [boxed_closure_value, direct_entry_addr].
	// On hit (loaded fn value == cached), skip closure type checks + Proto
	// lookup + DirectEntry lookup — just use the cached direct entry.
	// On miss, take the full path (which updates the cache on success).
	// Pointer is set by executeTier2 to &CompiledFunction.CallCache[0].
	Tier2CallCache uintptr

	// ExitResumePC is the bytecode PC of the int-spec overflow instruction.
	// Set by emitIntSpecDeopt so that Execute can resume the interpreter at the
	// exact guard PC instead of restarting at pc=0 (which replays side effects).
	ExitResumePC int64
}

// ExitCode constants.
const (
	ExitNormal     = 0 // normal return
	ExitDeopt      = 2 // deopt: bail to interpreter for the entire function
	ExitCallExit   = 3 // call-exit: pause JIT, execute call via VM, resume JIT
	ExitGlobalExit = 4 // global-exit: pause JIT, load global via VM, resume JIT
	ExitTableExit  = 5 // table-exit: pause JIT, do table op via Go, resume JIT
	ExitOpExit         = 6 // op-exit: pause JIT, Go handles the operation, resume JIT
	ExitBaselineOpExit = 7 // baseline op-exit: bytecode-level exit for Tier 1
	ExitNativeCallExit = 8 // native call exit: callee hit exit-resume during BLR call
	ExitOSR            = 9 // OSR: Tier 1 loop counter expired, request Tier 2 compilation
)

// TableOp constants (stored in ExecContext.TableOp).
const (
	TableOpNewTable  = 0
	TableOpGetTable  = 1
	TableOpSetTable  = 2
	TableOpGetField  = 3 // deopt fallback for GetField (no field cache)
	TableOpSetField  = 4 // deopt fallback for SetField (no field cache)
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
	execCtxOffBaselineOp         = int(unsafe.Offsetof(ExecContext{}.BaselineOp))
	execCtxOffBaselinePC         = int(unsafe.Offsetof(ExecContext{}.BaselinePC))
	execCtxOffBaselineA          = int(unsafe.Offsetof(ExecContext{}.BaselineA))
	execCtxOffBaselineB          = int(unsafe.Offsetof(ExecContext{}.BaselineB))
	execCtxOffBaselineC          = int(unsafe.Offsetof(ExecContext{}.BaselineC))
	execCtxOffBaselineFieldCache   = int(unsafe.Offsetof(ExecContext{}.BaselineFieldCache))
	execCtxOffBaselineClosurePtr   = int(unsafe.Offsetof(ExecContext{}.BaselineClosurePtr))
	execCtxOffBaselineReturnValue     = int(unsafe.Offsetof(ExecContext{}.BaselineReturnValue))
	execCtxOffBaselineGlobalCache     = int(unsafe.Offsetof(ExecContext{}.BaselineGlobalCache))
	execCtxOffBaselineGlobalGenPtr    = int(unsafe.Offsetof(ExecContext{}.BaselineGlobalGenPtr))
	execCtxOffBaselineGlobalCachedGen = int(unsafe.Offsetof(ExecContext{}.BaselineGlobalCachedGen))
	execCtxOffBaselineFeedbackPtr     = int(unsafe.Offsetof(ExecContext{}.BaselineFeedbackPtr))
	// Caller context offsets
	execCtxOffCallerRegs      = int(unsafe.Offsetof(ExecContext{}.CallerRegs))
	execCtxOffCallerConstants = int(unsafe.Offsetof(ExecContext{}.CallerConstants))
	// Native call mode offset
	execCtxOffCallMode = int(unsafe.Offsetof(ExecContext{}.CallMode))
	// Native call exit offsets
	execCtxOffNativeCallA         = int(unsafe.Offsetof(ExecContext{}.NativeCallA))
	execCtxOffNativeCallB         = int(unsafe.Offsetof(ExecContext{}.NativeCallB))
	execCtxOffNativeCallC         = int(unsafe.Offsetof(ExecContext{}.NativeCallC))
	execCtxOffNativeCalleeBaseOff   = int(unsafe.Offsetof(ExecContext{}.NativeCalleeBaseOff))
	execCtxOffNativeCalleeResumePC = int(unsafe.Offsetof(ExecContext{}.NativeCalleeResumePC))
	execCtxOffNativeCalleeClosurePtr = int(unsafe.Offsetof(ExecContext{}.NativeCalleeClosurePtr))
	execCtxOffRegsEnd              = int(unsafe.Offsetof(ExecContext{}.RegsEnd))
	execCtxOffRegsBase            = int(unsafe.Offsetof(ExecContext{}.RegsBase))
	execCtxOffTopPtr              = int(unsafe.Offsetof(ExecContext{}.TopPtr))
	execCtxOffNativeCallDepth     = int(unsafe.Offsetof(ExecContext{}.NativeCallDepth))
	execCtxOffOSRCounter          = int(unsafe.Offsetof(ExecContext{}.OSRCounter))
	// Tier 2 global cache offsets
	execCtxOffTier2GlobalCache    = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalCache))
	execCtxOffTier2GlobalCacheGen = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalCacheGen))
	execCtxOffTier2GlobalGenPtr   = int(unsafe.Offsetof(ExecContext{}.Tier2GlobalGenPtr))
	execCtxOffGlobalCacheIdx      = int(unsafe.Offsetof(ExecContext{}.GlobalCacheIdx))
	execCtxOffExitResumePC        = int(unsafe.Offsetof(ExecContext{}.ExitResumePC))
	execCtxOffTier2CallCache      = int(unsafe.Offsetof(ExecContext{}.Tier2CallCache))
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

	// DirectEntryOffset is the byte offset of the BLR-compatible direct entry
	// point within the code block. When non-zero, Tier 1 BLR callers can jump
	// to Code.Ptr() + DirectEntryOffset. The direct entry uses a 16-byte stack
	// frame (FP+LR only), matching Tier 1's direct entry calling convention.
	DirectEntryOffset int

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

	// CallCache (R108) is a per-OpCall-site monomorphic IC.
	// Layout: 2 × uint64 per call site.
	//   [2*i]   = cached boxed closure value (NaN-boxed 0xFFFF...)
	//   [2*i+1] = cached direct-entry address (uintptr)
	// Populated only when callee is Tier 2 compiled (direct_entry != 0).
	// R109: the CallCount inc + threshold check is miss-path-only, since
	// a hit implies the callee is already Tier 2.
	CallCache []uint64
}

// Execute, executeCallExit, executeGlobalExit, executeTableExit, executeOpExit
// are in emit_execute.go.
