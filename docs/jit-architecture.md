# GScript JIT Compiler Architecture

This document describes the architecture of the GScript JIT compiler as implemented in `internal/jit/`. It is intended for developers joining the project who need to understand how the pieces fit together.

## Directory Structure

Every `.go` source file in `internal/jit/` and its one-line purpose:

| File | Purpose |
|------|---------|
| `assembler.go` | ARM64 assembler core: `Assembler` struct, label resolution, fixup system |
| `assembler_arith.go` | ARM64 integer/bitwise instructions (ADD, SUB, MUL, SDIV, CMP, shifts, CSET) |
| `assembler_branch.go` | ARM64 branch instructions (B, B.cond, CBZ, CBNZ, BL, BLR, RET) |
| `assembler_float.go` | ARM64 SIMD/FP instructions (FADD, FMUL, FMADD, FSQRT, SCVTF, FMOV) |
| `assembler_mem.go` | ARM64 memory instructions (LDR, STR, LDP, STP, LDRB, pre/post-index) |
| `callexit_ops.go` | Shared Go implementation of 16 call-exit opcodes (CALL, GETTABLE, EQ, etc.) |
| `codegen.go` | Method JIT entry point: `Codegen` struct, `compile()`, prologue/epilogue |
| `codegen_analysis.go` | Method JIT analysis: known-int data flow, for-loop detection, call-exit PC collection |
| `codegen_arith.go` | Method JIT arithmetic/comparison/branch emission (ADD, EQ, LT, LE, TEST, JMP) |
| `codegen_call.go` | Method JIT cross-call analysis and direct BLR emission for inter-function calls |
| `codegen_data.go` | Method JIT data ops: LOADBOOL, LOADINT, LOADK, MOVE, RETURN emission |
| `codegen_dispatch.go` | Method JIT body loop: instruction dispatch, call-exit resume labels |
| `codegen_field.go` | Method JIT native GETFIELD/SETFIELD with skeys linear scan |
| `codegen_inline.go` | Method JIT inline analysis: detects inlineable functions and self-recursive calls |
| `codegen_inline_emit.go` | Method JIT inline emission: emits callee body with register remapping |
| `codegen_loop.go` | Method JIT for-loop emission: FORPREP/FORLOOP with register pinning |
| `codegen_selfcall.go` | Method JIT self-recursive call emission: tail calls, full calls, direct args |
| `codegen_table.go` | Method JIT native GETTABLE/SETTABLE with type-specialized array paths |
| `codegen_value.go` | Method JIT value helpers: load/store NaN-boxed ints, floats, bools, RK access |
| `executor.go` | Method JIT `Engine`: compilation, hot-count tracking, exit-resume loop |
| `executor_callexit.go` | Method JIT call-exit handling: Go-side OP_CALL dispatch, cross-call fast path |
| `liveness.go` | SSA liveness analysis: determines which VM slots need store-back on trace exit |
| `memory.go` | `CodeBlock` struct: portable executable memory abstraction |
| `memory_darwin_arm64.go` | macOS ARM64 mmap/W^X implementation (MAP_JIT, icache invalidation) |
| `ssa.go` | SSA pipeline orchestration: `BuildSSA`, `OptimizeSSA`, `SSAIsUseful` |
| `ssa_builder.go` | SSA IR builder: converts `Trace` to `SSAFunc` with type inference |
| `ssa_codegen.go` | Trace JIT entry point: `CompileSSA`, `ssaCodegen` struct, phase orchestration |
| `ssa_codegen_alloc.go` | Trace JIT slot-based register allocation (frequency-based int/float) |
| `ssa_codegen_analysis.go` | Trace JIT analysis: compilability checks, write-before-read float detection |
| `ssa_codegen_array.go` | Trace JIT type-specialized LOAD_ARRAY/STORE_ARRAY (int/float/bool fast paths) |
| `ssa_codegen_emit.go` | Trace JIT instruction emission: dispatches each SSA op to ARM64, float forwarding |
| `ssa_codegen_emit_arith.go` | Trace JIT arithmetic emission: int ADD/SUB/MUL/MOD, float FADD/FMUL/FMADD |
| `ssa_codegen_loop.go` | Trace JIT loop body emission: resume dispatch, pre-loop guards/loads, cold paths |
| `ssa_codegen_resolve.go` | Trace JIT register resolution: ref-to-register mapping, store-back, float resolve |
| `ssa_codegen_table.go` | Trace JIT table operations: LOAD_FIELD/STORE_FIELD with shape guards, inner escape |
| `ssa_const_hoist.go` | SSA optimization pass: hoists loop-invariant constants before LOOP marker |
| `ssa_cse.go` | SSA optimization pass: Common Subexpression Elimination within loop body |
| `ssa_float_regalloc.go` | SSA ref-level float register allocator: linear scan with coalescing on D4-D11 |
| `ssa_fma.go` | SSA optimization pass: fuses MUL+ADD/SUB into FMADD/FMSUB |
| `ssa_guard_analysis.go` | SSA guard analysis: `computeLiveIn` for minimal pre-loop type guards |
| `ssa_ir.go` | SSA IR definitions: `SSAOp`, `SSAType`, `SSARef`, `SSAInst`, `SSAFunc` |
| `ssa_regalloc.go` | SSA register allocator pass: `AllocateRegisters` producing `RegMap` |
| `trace.go` | Trace data structures: `Trace`, `TraceIR`, `TraceRecorder`, intrinsic IDs |
| `trace_exec.go` | Trace execution: `CompiledTrace.Execute`, `TraceContext`, exit-resume loop |
| `trace_record.go` | Trace recording: `OnInstruction`, inner loop handling, inline call tracking |
| `trampoline.go` | Go declaration of `callJIT` (assembly trampoline for Go-to-native calls) |
| `trampoline_arm64.s` | ARM64 assembly trampoline: calls JIT code pointer with context argument |
| `usedef.go` | SSA use-def chain builder: `BuildUseDef` for dead code analysis |
| `value_layout.go` | NaN-boxing constants, Table struct offsets, EmitBoxInt/UnboxInt/GuardType helpers |

## Two-Tier JIT Architecture

GScript uses a two-tier JIT, inspired by LuaJIT:

```
Interpreter
    |
    | (call count >= 10)
    v
Method JIT (Tier 1)
    |  Compiles entire function to ARM64
    |  Handles: arithmetic, loops, branches, calls
    |  Falls back to interpreter on side-exit
    |
    | (side-exit ratio too high OR loop-heavy function)
    v
Trace JIT (Tier 2)
    Records one loop iteration during execution
    Builds SSA IR from the recording
    Optimizes: ConstHoist -> CSE -> FMA fusion -> RegAlloc -> Liveness
    Compiles SSA to tight ARM64 loop
```

### Method JIT (function-level)

The Method JIT compiles an entire `FuncProto` (bytecode function) to ARM64. It performs whole-function analysis to optimize integer arithmetic, for-loops, field access, and function calls. When it encounters an instruction it cannot handle natively (e.g., string concatenation, metatables), it uses a **call-exit** mechanism: the native code jumps to the epilogue with `ExitCode=2`, the Go executor handles the instruction, then re-enters the native code at the next PC via a dispatch table in the prologue.

Key features:
- **Register pinning**: for-loop control variables (idx, limit, step, loop var) and body accumulators are pinned to ARM64 callee-saved registers (X19-X25), eliminating memory traffic in hot loops.
- **Self-call inlining**: self-recursive functions (fib, ackermann) use direct `BL` to a shared entry point with depth tracking, tail-call optimization, and arg trace analysis.
- **Cross-call BLR**: calls to other compiled functions bypass the Go call handler entirely via pre-allocated `crossCallSlot` pointers.
- **Function inlining**: small functions (<=20 bytecodes, pure arithmetic) are inlined at the call site with register remapping.
- **Hot/cold code splitting**: type guard failures, side-exit stubs, and overflow handlers are deferred to a cold section after the hot path.

### Trace JIT (loop-level SSA)

The Trace JIT is activated when the Method JIT demotes a function (too many call-exits) or when a loop's back-edge counter crosses a threshold. It works by:

1. **Recording**: The `TraceRecorder` captures one complete loop iteration, including type information for every operand. It handles inner loops via sub-trace calling or full nested loop recording.
2. **SSA Construction**: `BuildSSA` converts the linear trace recording into SSA IR with type inference. Guards are placed before the `SSA_LOOP` marker; the loop body follows.
3. **Optimization**: A pipeline of passes (ConstHoist, CSE, FMA fusion, dead code elimination) reduces the IR.
4. **Register Allocation**: Frequency-based slot allocation for integers (X20-X23) and ref-level linear-scan allocation for floats (D4-D11) with loop-carried coalescing.
5. **Code Emission**: The SSA IR is lowered to ARM64 with type-specialized array access, float expression forwarding, and BOLT-style hot/cold code splitting.

### How They Interact

The two tiers share infrastructure but are largely independent compilation pipelines:

- **Shared**: `Assembler`, `value_layout.go` (NaN-boxing helpers), `memory.go` (executable memory), `callexit_ops.go` (Go-side opcode execution), `callJIT` trampoline.
- **Trigger**: Method JIT sets `proto.JITSideExited = true` when it demotes a function. The VM checks this flag to activate trace recording for the function's loops.
- **Coexistence**: A function can have both a Method JIT compiled form (for the whole function) and Trace JIT compiled forms (for specific hot loops). The VM prefers the trace for loops that have compiled traces, falling back to Method JIT or the interpreter for everything else.

## Core Data Structures

### Method JIT

**`Codegen`** (`codegen.go`): The central Method JIT compiler. Holds the assembler, function prototype, analysis results (known-int bitmasks, for-loop descriptors, inline candidates, cross-call info), and pinned register state. The `compile()` method orchestrates the pipeline: analysis passes, then prologue, body emission, cold stubs, epilogue.

**`Engine`** (`executor.go`): Manages compilation and execution. Tracks compiled entries per `FuncProto`, manages cross-call slots, and runs the exit-resume loop in `TryExecute`. When native code exits with `ExitCode=2` (call-exit), the engine handles the instruction in Go, updates `ResumePC`, and re-enters.

**`JITContext`** (`codegen.go`): The 64-byte struct shared between Go and ARM64 code. Native code reads `Regs` and `Constants` pointers, writes `ExitPC`/`ExitCode`/`RetBase`/`RetCount`. The `ResumePC` field enables re-entry after call-exits.

**`CompiledFunc`** (`codegen.go`): Holds a `CodeBlock` (executable memory) and the source `FuncProto`.

### Trace JIT

**`TraceRecorder`** (`trace.go`): Captures instructions during execution. Tracks inline depth, inner loop state, and builds the `Trace` recording with type annotations on every operand.

**`Trace`** / **`TraceIR`** (`trace.go`): A recorded loop iteration. Each `TraceIR` holds the original opcode plus runtime type info (`AType`, `BType`, `CType`), intrinsic ID, field index, and shape ID captured during recording.

**`SSAFunc`** / **`SSAInst`** (`ssa_ir.go`): The SSA intermediate representation. An `SSAFunc` is a flat array of `SSAInst` with an `SSA_LOOP` marker separating pre-loop guards from the loop body. Each `SSAInst` has an `Op` (SSAOp enum), `Type` (SSAType), two operand refs (`Arg1`, `Arg2`), a VM `Slot`, bytecode `PC`, and `AuxInt`.

**`RegMap`** (`ssa_regalloc.go`): The complete register allocation for a trace: integer slot allocation (X20-X23), float slot allocation (D4-D11, slot-level fallback), and float ref allocation (D4-D11, ref-level primary via linear scan).

**`CompiledTrace`** (`trace_exec.go`): Holds native code for a trace, plus sub-trace pointers, call-exit state, and blacklisting counters. Implements `vm.TraceExecutor`.

**`TraceContext`** (`trace_exec.go`): The 56-byte struct bridging Go and trace native code. Similar to `JITContext` but includes inner trace code/constants pointers and a different exit code scheme (0=loop done, 1=side exit, 2=guard fail, 3=call-exit).

### Shared

**`Assembler`** (`assembler.go`): Emits ARM64 machine code into a byte buffer. Supports forward-reference labels with fixup resolution on `Finalize()`. Instruction encoding helpers are split across `assembler_arith.go`, `assembler_branch.go`, `assembler_float.go`, and `assembler_mem.go`.

**`CodeBlock`** (`memory.go` / `memory_darwin_arm64.go`): Wraps mmap'd executable memory. On macOS ARM64, uses `MAP_JIT` and `pthread_jit_write_protect_np` for W^X compliance. The `WriteCode` method pins to the current OS thread during the write-protect toggle.

## Compilation Pipelines

### Method JIT Pipeline

```
FuncProto.Code (bytecode)
    |
    v
analyzeInlineCandidates()   -- detect GETGLOBAL+CALL patterns, self-calls
analyzeCrossCalls()          -- detect direct BLR opportunities
analyzeKnownIntRegs()        -- forward data-flow: known TypeInt bitmasks per PC
analyzeForLoops()            -- detect numeric for-loops, step values, accumulators
analyzeCallExitPCs()         -- collect PCs needing call-exit resume entries
    |
    v
emitPrologue()   -- save callee-saved regs, load context, dispatch table
emitBody()       -- per-instruction emission with inline/cross-call/call-exit handling
emitColdStubs()  -- deferred guard failures, side-exit stubs
emitEpilogue()   -- restore regs, RET
    |
    v
Assembler.Finalize()  -- resolve labels
AllocExec() + WriteCode()  -- mmap, copy, icache flush
    |
    v
CompiledFunc (ready to execute)
```

### Trace JIT Pipeline

```
Hot loop detected (back-edge counter)
    |
    v
TraceRecorder.OnInstruction()  -- records one full iteration with types
    |
    v
Trace (linear IR with type annotations)
    |
    v
BuildSSA(trace)              -- SSA construction with guard hoisting
    |                           Pre-loop: LOAD_SLOT -> GUARD_TYPE -> UNBOX
    |                           SSA_LOOP marker
    |                           Loop body: typed arithmetic, comparisons, table ops
    v
ConstHoist(f)                -- move constants before LOOP, rewrite refs
CSE(f)                       -- eliminate duplicate computations in loop body
FuseMultiplyAdd(f)           -- MUL+ADD -> FMADD, MUL+SUB -> FMSUB
OptimizeSSA(f)               -- dead code elimination
    |
    v
BuildUseDef(f)               -- use-def chains for future passes
AllocateRegisters(f)         -- frequency-based int slots + linear-scan float refs
AnalyzeLiveness(f)           -- which slots need store-back on exit
    |
    v
CompileSSA(f)                -- emit ARM64 from analyzed SSA
    emitSSAPrologue()        -- save regs, load context, pin regTagInt
    emitSSAResumeDispatch()  -- call-exit resume table
    emitSSAPreLoopGuards()   -- NaN-boxing type guards
    emitSSAPreLoopLoads()    -- load slots into registers, hoist constants
    emitSSAPreLoopTableGuards() -- hoist loop-invariant table guards
    emitSSALoopBody()        -- emit loop with float forwarding, inner loops
    emitSSAColdPaths()       -- loop_done, inner_escape, side_exit, guard_fail
    emitSSAEpilogue()        -- restore regs, RET
    |
    v
CompiledTrace (ready to execute)
```

## SSA Optimization Passes

### ConstHoist (`ssa_const_hoist.go`)

Moves `SSA_CONST_INT` and `SSA_CONST_FLOAT` instructions from inside the loop body to just before the `SSA_LOOP` marker. This eliminates constant rematerialization (LoadImm64 sequences) on every iteration. Constants whose slots are also written by non-constant ops are not hoisted, because they re-initialize per iteration (e.g., inner loop control registers). After moving, all `SSARef` operands are remapped.

### CSE (`ssa_cse.go`)

Common Subexpression Elimination within the loop body. Instructions with the same `(Op, Type, Arg1, Arg2, AuxInt)` tuple are deduplicated: later occurrences are replaced with references to the first. Only pure (side-effect-free) operations qualify: arithmetic, comparisons, constants, box/unbox. Transitive CSE is supported: if an operand was already rewritten, its replacement is used when computing the key.

### FMA Fusion (`ssa_fma.go`)

Detects `MUL_FLOAT` + `ADD_FLOAT` patterns and fuses them into `SSA_FMADD` (fused multiply-add), and `MUL_FLOAT` + `SUB_FLOAT` into `SSA_FMSUB`. The ARM64 `FMADD`/`FMSUB` instructions are single-cycle on Apple Silicon and produce higher precision than separate MUL+ADD. The absorbed MUL is marked in `AbsorbedMuls` so codegen skips it, but it stays in the IR to preserve register allocation live ranges.

### RegAlloc (`ssa_regalloc.go`, `ssa_float_regalloc.go`, `ssa_codegen_alloc.go`)

Two-level register allocation:

**Integer slots** (X20-X23): Frequency-based. The 4 most-used integer VM slots get callee-saved registers. Float and table slots are excluded.

**Float refs** (D4-D11): Ref-level linear-scan allocation. Each SSA value (not each VM slot) gets its own D register assignment. Live intervals are computed per-ref; loop-carried values are coalesced so the MOVE at the loop tail uses the same register as the pre-loop load. This enables multiple temporaries from the same VM slot to live in different registers simultaneously. 8 registers are available (D4-D7 caller-saved, D8-D11 callee-saved with save/restore in prologue).

### Liveness (`liveness.go`)

Determines which VM slots are modified inside the loop body and need to be stored back to the VM register array on exit. Walks instructions after `SSA_LOOP`, collecting slots of value-producing operations. Skips NOPs and absorbed MULs. The result drives `emitSlotStoreBack`: only modified slots are written back, avoiding corruption of unmodified table references.

## Shared Infrastructure

### Assembler

The `Assembler` is a straightforward bytecode-to-ARM64 emitter. It maintains a byte buffer, a label map (name to byte offset), and a fixup list. Instructions are encoded as 32-bit little-endian values. Forward references use placeholder instructions that are patched during `Finalize()`. Three fixup kinds are supported: B (26-bit offset), B.cond (19-bit), and CBZ/CBNZ (19-bit).

### Value Layout and NaN-Boxing

GScript uses NaN-boxing: every value is a single `uint64`. The encoding:

- **Float64**: raw IEEE 754 bits. Identified by bits 50-62 NOT all being 1.
- **Tagged values**: bits 50-62 all 1 (quiet NaN), sign bit = 1. The top 16 bits encode the type:
  - `0xFFFC` = nil
  - `0xFFFD` = bool (payload bit 0: 0=false, 1=true)
  - `0xFFFE` = int (payload: 48-bit signed integer, sign-extended via SBFX)
  - `0xFFFF` = pointer (bits 44-47 = sub-type, bits 0-43 = 44-bit address)

`value_layout.go` provides ARM64 codegen helpers: `EmitUnboxInt` (SBFX), `EmitBoxIntFast` (UBFX+ORR with pinned tag register), `EmitExtractPtr` (UBFX 44-bit), `EmitGuardType` (LSR+CMP+B.cond per type), and `EmitCheckIsTableFull` (tag check + pointer sub-type check).

A pinned register (`X24 = regTagInt`) holds the int tag constant `0xFFFE000000000000` to reduce boxing from 3 instructions to 2 (UBFX+ORR).

### Memory Management

`CodeBlock` wraps platform-specific executable memory allocation. On macOS ARM64, `AllocExec` uses `mmap` with `MAP_JIT`. Writing requires a W^X toggle sequence: `LockOSThread`, `pthread_jit_write_protect_np(0)` (writable), copy code, `pthread_jit_write_protect_np(1)` (executable), `UnlockOSThread`, then `sys_icache_invalidate`.

### Call-Exit Mechanism

The call-exit mechanism allows JIT code to handle instructions it cannot compile natively. Shared between both tiers:

1. Native code sets `ExitPC` to the bytecode PC and `ExitCode=2` (Method JIT) or `ExitCode=3` (Trace JIT), then jumps to the epilogue.
2. The Go executor dispatches to `ExecuteCallExitOp` in `callexit_ops.go`, which handles 16 opcodes: CALL, GETGLOBAL, SETGLOBAL, GETTABLE, SETTABLE, GETFIELD, SETFIELD, NEWTABLE, SETLIST, LEN, CONCAT, MOD, DIV, SELF, EQ, LT, LE.
3. The executor sets `ResumePC` and re-enters the native code. The prologue's dispatch table routes to the correct resume label.

This design means adding a new call-exit opcode requires only a new `case` in `ExecuteCallExitOp` -- both JIT tiers get it for free.

## Key Design Decisions

### NaN-boxing (8-byte values)

Every value is 8 bytes, down from 24 bytes (type tag + data + pointer) in the original representation. This halves memory bandwidth for register loads/stores, improves cache utilization, and simplifies the codegen. The 48-bit integer payload is sufficient for GScript's use cases. Pointer values use 44 address bits, enough for macOS ARM64 user-space.

### Two-tier architecture

Method JIT provides broad coverage (all functions, all opcodes via call-exit), while Trace JIT provides deep optimization (hot loops with type specialization, unboxed registers, FMA fusion). The two tiers complement each other: Method JIT handles the function skeleton and non-loop code; Trace JIT handles compute-heavy inner loops that the Method JIT's whole-function approach cannot optimize as aggressively.

### Call-exit mechanism

Rather than side-exiting permanently when encountering an unsupported opcode, the call-exit mechanism allows the JIT to handle the instruction in Go and resume execution. This is critical for real-world code where hot loops contain table accesses, function calls, and string operations intermixed with arithmetic. The batching optimization (consecutive call-exit opcodes are handled without re-entering native code) further reduces overhead.

### Side-exit mechanism

Both tiers use side-exits for type guard failures and unsupported patterns. The Method JIT side-exits permanently (interpreter takes over at that PC). The Trace JIT has three exit codes: guard-fail (pre-loop type mismatch, trace not executed), side-exit (in-loop guard failure, store-back then interpreter), and loop-done (normal completion). The inner-escape optimization redirects float guard failures in nested loops to a continuation path instead of side-exiting, keeping execution in native code.

### Slot-based vs. ref-based register allocation

The Trace JIT uses slot-based allocation for integers (same ARM64 register for all operations on the same VM slot) to ensure loop-carried values persist across back-edges without explicit PHI nodes. For floats, ref-level linear-scan allocation assigns different D registers to different SSA values even when they share a VM slot, with coalescing constraints for loop-carried values. This hybrid approach balances simplicity (integers) with register pressure optimization (floats).

### Hot/cold code splitting

Both tiers place infrequently-executed code (guard failures, side-exit stubs, overflow handlers, loop-exit store-back) after the hot path. This BOLT-style layout reduces I-cache pressure in the hot loop. On Apple M4 (64-byte L1 icache lines), a tight inner loop fits in 2-3 cache lines when cold code is separated.
