# GScript Tracing JIT Design

## Overview

A tracing JIT that records hot loop execution traces (including cross-function
calls) and compiles them to native ARM64 code. Target: 5-10x over the current
interpreter on the chess AI benchmark.

## Architecture

```
Interpreter Loop (vm.run)
    │
    ├── Loop back-edge detected → increment counter
    │
    ├── Counter >= threshold → START RECORDING
    │       │
    │       ├── Record each executed instruction as IR node
    │       ├── Inline function calls (record callee instructions)
    │       ├── Record type guards for polymorphic ops
    │       └── Stop at loop back-edge → COMPILE TRACE
    │
    └── Compiled trace exists → EXECUTE NATIVE CODE
            │
            ├── Guard passes → continue native execution
            └── Guard fails → SIDE EXIT to interpreter
```

## Phase A: Trace Recorder

**Goal**: Record execution traces from the interpreter. No compilation yet —
just capture the instruction stream and verify it's correct by replaying.

### Data Structures

```go
// internal/jit/trace.go

// TraceIR represents one instruction in a recorded trace.
type TraceIR struct {
    Op       vm.Opcode    // original bytecode opcode
    A, B, C  int          // decoded operands
    PC       int          // bytecode PC (for side-exit mapping)
    Proto    *vm.FuncProto // which function this instruction belongs to

    // Type info captured during recording:
    AType    ValueType    // type of R(A) at this point
    BType    ValueType    // type of RK(B) at this point (if applicable)
    CType    ValueType    // type of RK(C) at this point (if applicable)
}

// Trace is a recorded execution trace (one loop iteration).
type Trace struct {
    ID        int
    LoopPC    int           // bytecode PC of the loop back-edge
    LoopProto *vm.FuncProto // function containing the loop
    IR        []TraceIR     // recorded instruction stream
    Constants []Value       // constants referenced by the trace

    // Compiled state (nil until Phase B):
    Code      *CodeBuf      // native ARM64 code
    EntryPC   int           // bytecode PC where native execution starts
}

// TraceRecorder captures instructions during recording mode.
type TraceRecorder struct {
    trace     *Trace
    recording bool
    depth     int           // call depth (0 = loop function, >0 = inlined)
    maxDepth  int           // max inline depth (e.g., 3)
    maxLen    int           // max trace length (e.g., 200 instructions)
    aborted   bool          // recording aborted (trace too long, unsupported op, etc.)
}
```

### Integration Point

The trace recorder hooks into `vm.run()` at two points:

1. **Loop back-edge** (OP_FORLOOP, OP_TFORLOOP, OP_JMP with negative offset):
   - If not recording: increment loop counter, start recording if hot
   - If recording: stop recording, trace complete

2. **Every instruction** (when recording):
   - Capture the instruction + operand types into TraceIR
   - For OP_CALL: push inline frame, continue recording callee
   - For OP_RETURN: pop inline frame, continue recording caller
   - For unsupported ops: abort recording

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/jit/trace.go` | **Create** | Trace, TraceIR, TraceRecorder types |
| `internal/jit/trace_test.go` | **Create** | Unit tests for recorder |
| `internal/vm/vm.go` | **Modify** | Add recorder hooks in run() |

### Tests (TDD)

```
TestTraceRecorder_SimpleForLoop
  - Record a `for i := 1; i <= 10; i++ { sum = sum + i }` trace
  - Verify trace has FORLOOP + ADD instructions
  - Verify type info: all TypeInt

TestTraceRecorder_NestedCall
  - Record a loop that calls a function: `for i := 1; i <= 10; i++ { sum = sum + f(i) }`
  - Verify function call is inlined in the trace
  - Verify call/return boundaries are correct

TestTraceRecorder_TableAccess
  - Record a loop with GETFIELD: `for i := 1; i <= 10; i++ { sum = sum + t.x }`
  - Verify GETFIELD is captured with type info

TestTraceRecorder_Abort
  - Record a loop with unsupported op (e.g., coroutine.yield)
  - Verify recording is aborted, no trace produced

TestTraceRecorder_MaxLength
  - Record a loop with very long body
  - Verify recording is aborted at maxLen
```

### Deliverable

A trace recorder that can be enabled via `vm.SetTraceRecorder(recorder)`.
When enabled, it records traces but does NOT compile or execute them.
Traces can be inspected for correctness via `trace.IR` slice.

---

## Phase B: Simple Code Emitter

**Goal**: Compile recorded traces to native ARM64 code. No optimization —
direct 1:1 translation of trace IR to machine code.

### Design

Each TraceIR instruction is compiled to ARM64 using the existing codegen
helpers (loadRegIval, storeRegIval, etc.). The key difference from the
method JIT: the trace is LINEAR (no branches except guards and the
loop back-edge).

```
Trace compilation:
  1. Emit prologue (save registers, load context)
  2. For each TraceIR:
     - Emit type guard (based on recorded types)
     - Emit the operation (reuse method JIT emitters where possible)
     - Guard failure → side-exit (store PC, return to interpreter)
  3. Emit loop back-edge (jump to top)
  4. Emit epilogue
```

### Guard Mechanism

Every instruction that depends on type gets a guard:

```arm64
// Guard: R(B) is TypeInt
LDRB W0, [regs, B*32]        // load R(B).typ
CMP W0, #TypeInt
B.NE side_exit_N              // → interpreter resumes at this PC
```

Guard failure = the trace's assumption was wrong. Control returns to
the interpreter at the failing PC. The interpreter continues normally.

### Inlined Calls in Trace

When the trace crosses a function call boundary:
- The trace IR has the callee's instructions directly (no OP_CALL)
- Register mapping: callee's R(0..N) → caller's R(base+0..base+N)
- The code emitter adjusts register offsets based on the inline depth

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/jit/trace_compile.go` | **Create** | Trace → ARM64 compiler |
| `internal/jit/trace_compile_test.go` | **Create** | Compilation tests |
| `internal/jit/trace.go` | **Modify** | Add Compile() method |
| `internal/vm/vm.go` | **Modify** | Execute compiled traces |

### Tests

```
TestTraceCompile_SimpleAdd
  - Compile a trace with: LOADINT + ADD + FORLOOP
  - Execute native code
  - Verify result matches interpreter

TestTraceCompile_ForLoop
  - Compile: for i := 1; i <= 100; i++ { sum += i }
  - Execute and verify sum == 5050

TestTraceCompile_GuardFailure
  - Compile a trace assuming TypeInt
  - Feed a TypeFloat value
  - Verify side-exit triggers, interpreter resumes correctly

TestTraceCompile_InlinedCall
  - Compile a trace with inlined function call
  - Verify cross-function register mapping is correct

TestTraceCompile_TableGetField
  - Compile a trace with GETFIELD
  - Verify table field access works natively
```

### Deliverable

Traces are compiled and executed. The VM first checks if a compiled trace
exists for the current loop PC; if so, executes it. On side-exit, falls
back to the interpreter.

---

## Phase C: Optimization Passes

**Goal**: Optimize the trace IR before compilation for significant speedup.

### Optimizations

1. **Type Specialization**
   - If a register is always TypeInt in the trace, eliminate redundant guards
   - Specialize arithmetic: `ADD_INT` instead of generic `ADD` with type check

2. **Constant Propagation**
   - If a GETGLOBAL always returns the same function, inline the pointer
   - If a LOADK loads a constant, propagate through the trace

3. **Common Subexpression Elimination (CSE)**
   - `p.col` and `p.col` on the same table object → load once
   - `col * 100 + row` computed multiple times → reuse

4. **Dead Code Elimination**
   - Remove instructions whose results are never used

5. **Guard Hoisting**
   - Move type guards out of the loop body to the loop header
   - Once proven at the top, no need to re-check each iteration

### Files

| File | Action | Description |
|------|--------|-------------|
| `internal/jit/trace_opt.go` | **Create** | Optimization passes |
| `internal/jit/trace_opt_test.go` | **Create** | Tests for each pass |

### Tests

```
TestOpt_TypeSpecialization
  - Trace with 5 ADD instructions, all TypeInt
  - After optimization: guards removed from inner ops, only at trace entry

TestOpt_ConstantPropagation
  - LOADK + ADD → constant folded

TestOpt_CSE
  - Two GETFIELD on same table + same field → second becomes MOVE

TestOpt_DeadCode
  - Instruction whose result register is overwritten before use → removed

TestOpt_GuardHoisting
  - Guard inside loop body → moved to trace entry
```

---

## Phase D: Trace Linking & Side Exits

**Goal**: Connect multiple traces for complex control flow. Handle side exits
efficiently.

### Trace Linking

When trace A's loop body branches to a different path, a side exit occurs.
If the same side exit happens frequently, record a NEW trace (trace B)
starting from the exit point. Link A's exit directly to B's entry:

```
Trace A (main path):
  ... instructions ...
  guard_3: CMP type, #TypeInt
  B.NE → Trace B entry (instead of interpreter!)

Trace B (side trace):
  ... instructions for the alternative path ...
  JMP → Trace A loop header (or its own loop)
```

### Efficient Side Exits

Instead of returning to the interpreter on every guard failure:
1. First few failures: return to interpreter (cold path)
2. If a specific exit is hot: record a side trace
3. Link the exit directly to the side trace (zero overhead)

### Files

| File | Action | Description |
|------|--------|-------------|
| `internal/jit/trace_link.go` | **Create** | Trace linking logic |
| `internal/jit/trace_link_test.go` | **Create** | Tests |
| `internal/jit/trace.go` | **Modify** | Add SideExitInfo, link fields |

### Tests

```
TestTraceLink_SideTrace
  - Main trace with type guard
  - Feed mixed types to trigger side exit
  - Verify side trace is recorded and linked

TestTraceLink_ChainedTraces
  - Three traces linked together (A → B → C → A)
  - Verify execution flows correctly through the chain
```

---

## Implementation Order & Milestones

```
Phase A: Trace Recorder           ~500 lines, ~2 days
  ├── trace.go + tests
  ├── vm.go hooks
  └── Milestone: traces can be recorded and inspected

Phase B: Code Emitter             ~1500 lines, ~4 days
  ├── trace_compile.go + tests
  ├── vm.go execution hooks
  └── Milestone: simple loops compile and execute 2-3x faster

Phase C: Optimization             ~800 lines, ~3 days
  ├── trace_opt.go + tests
  └── Milestone: chess benchmark loops 1.5-2x faster than Phase B

Phase D: Trace Linking            ~300 lines, ~2 days
  ├── trace_link.go + tests
  └── Milestone: complex control flow handles gracefully

Total: ~3100 lines, ~11 days
```

## Expected Performance

| Phase | Chess d=5 (est.) | vs Baseline |
|-------|-----------------|-------------|
| Current (method JIT) | 3.6s | ×2.75 |
| Phase B (unoptimized traces) | 2.0s | ×5 |
| Phase C (optimized traces) | 1.2s | ×8 |
| Phase D (linked traces) | 1.0s | ×10 |

## Key Design Decisions

1. **Recording granularity**: One loop iteration = one trace. Inner loops
   are unrolled up to a limit.

2. **Inline depth**: Max 3 levels of function inlining during recording.
   Deeper calls abort recording or fall back to call-exit.

3. **Trace cache**: Keyed by (FuncProto, loop PC). One trace per loop.
   Side traces keyed by (parentTrace, exitPC).

4. **Register allocation**: Phase B uses a simple 1:1 mapping (VM register
   → ARM64 register or spill slot). Phase C can improve with linear scan.

5. **GC safety**: Traces reference FuncProto and Constants via pointers.
   These are kept alive by the VM. Native code pointers in CodeBuf are
   managed by the trace cache.
