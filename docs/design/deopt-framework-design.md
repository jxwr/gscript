# GScript Deoptimization Framework Design

**Date:** 2026-03-27
**Status:** Design Phase
**Goal:** Enable speculative optimization in GScript JIT with eager deoptimization on guard failures

---

## Executive Summary

GScript's JIT currently has side-exits but lacks a formal deoptimization framework. Speculative optimization requires:

1. **Guard nodes** that validate runtime assumptions (types, shapes, bounds)
2. **Bailout metadata** that maps guard failures to interpreter state
3. **Deoptimization handler** that reconstructs interpreter frame from optimized state
4. **Observability** to debug guard failures

This design adds eager deoptimization (immediate bailout on guard failure) — simpler and correct than lazy deopt.

**Why Eager Deopt:** V8 uses lazy deopt for specific cases (try/catch, overflow). Most V8 deopts are eager. Eager deopt is sufficient for GScript's use case and avoids complexity.

---

## Current Implementation Analysis

### Existing Exit Mechanism

**Files:** `internal/jit/trace.go`, `internal/jit/ssa_emit.go`

```go
type TraceContext struct {
    Regs           uintptr // input: pointer to vm.regs[base]
    Constants      uintptr // input: pointer to trace constants[0]
    ExitPC         int64   // output: bytecode PC where trace exited
    ExitCode       int64   // output: 0=loop done, 1=side exit, 2=guard fail, 3=call-exit, 4=break exit
    InnerCode      uintptr // input: code pointer for inner trace
    InnerConstants uintptr // input: constants pointer for inner trace
    ResumePC       int64   // input: bytecode PC to resume at after call-exit
    ExitSnapIdx    int64   // output: which snapshot to restore on exit
    // ExitState: saved trace registers for snapshot restore
    ExitGPR [4]int64   // X20, X21, X22, X23
    ExitFPR [8]float64 // D4-D11
}

type CompiledTrace struct {
    code        *CodeBlock
    proto       *vm.FuncProto
    loopPC      int
    constants   []runtime.Value

    // Snapshot-based state restore (existing, but partial)
    snapshots   []Snapshot
    regAlloc    map[SSARef]int   // SSARef -> register index

    // Side-exit tracking
    sideExitCount  int
    fullRunCount   int
    guardFailCount int
    blacklisted    bool
}
```

**Current Exit Codes:**
- `ExitCode = 0`: Loop done (normal completion)
- `ExitCode = 1`: Side exit (snapshot restore)
- `ExitCode = 2`: Guard fail (NOT FULLY IMPLEMENTED)
- `ExitCode = 3`: Call-exit (external function call)
- `ExitCode = 4`: Break exit (break statement)

**Existing Snapshot Mechanism:**
```go
type Snapshot struct {
    slots     []int       // Which slots are live
    values    []SSARef    // SSARef values at this point
    exitPoint  int         // Bytecode PC for resume
}

// CompiledTrace.snapshots stores snapshots at guard points
// On exit, ExitSnapIdx selects which snapshot to restore
```

### Current Limitations

1. **No formal guard nodes:** Guards are emitted but not tracked as first-class objects
2. **No bailout ID mapping:** Guard failures don't map to specific bailout points
3. **No value materialization:** SSA values needing reconstruction (e.g., unboxed int) can't be restored
4. **Limited observability:** No debug info on guard failures
5. **ExitCode=2 unused:** Guard fail path exists but isn't properly integrated

---

## Proposed Deoptimization Framework

### Core Components

```
┌─────────────────────────────────────────────────────────────┐
│                   Deoptimization Framework                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────────┐         ┌─────────────────────────┐  │
│  │  Guard Nodes    │───────▶│  Guard Emitter        │  │
│  │  (type checks)   │         │  (emit guard code)     │  │
│  └──────────────────┘         └───────────┬─────────────┘  │
│                                      │                       │
│                                      ▼                       │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Deoptimization Metadata Builder                     │   │
│  │  - Bailout ID generation                            │   │
│  │  - Live value mapping                              │   │
│  │  - Value materialization info                      │   │
│  └──────────────────────┬──────────────────────────────┘   │
│                         │                                     │
│                         ▼                                     │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Deoptimization Handler (exit.go)                 │   │
│  │  - Materialize heap objects                         │   │
│  │  - Fill interpreter registers                       │   │
│  │  - Set interpreter PC                              │   │
│  │  - Jump to interpreter                              │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### Data Structures

```go
// === Guard System ===

// GuardType defines the kind of runtime check being performed.
type GuardType int

const (
    GuardTypeNone GuardType = iota
    GuardTypeInt32        // Check: value is Int32 (not Float/String/etc)
    GuardTypeFloat64      // Check: value is Float64
    GuardTypeNotNil      // Check: value is not nil
    GuardTypeBounds      // Check: index is within array bounds
    GuardTypeTableShape  // Check: table has expected shapeID
    GuardTypeString      // Check: value is a string
    GuardTypeArrayKind   // Check: array has expected ArrayKind
)

// GuardNode represents a runtime check in the compiled trace.
// Guards validate speculation assumptions at runtime.
type GuardNode struct {
    Type      GuardType    // Kind of guard (Int32, TableShape, etc.)
    Value     SSARef      // SSA value being checked
    Expected  interface{}  // Expected value/type (e.g., expected shapeID, array length)
    BailoutID int         // Which bailout handler to call on failure
    Location  CodeLoc     // Source location for debugging
}

// CodeLoc tracks where a guard was generated (for debugging).
type CodeLoc struct {
    FuncProto *vm.FuncProto  // Function containing the guard
    BytecodePC int           // Bytecode offset of guard
    SSAInst    int           // SSA instruction index (if available)
}

// === Deoptimization Metadata ===

// BailoutInfo describes how to recover from a guard failure.
type BailoutInfo struct {
    BailoutID      int              // Unique ID for this bailout
    BytecodePC    int              // Where to resume in interpreter
    LiveValues     []LiveValueInfo // Which values need to be materialized
    SnapshotIdx    int              // Which snapshot to restore (if any)
    FrameMapping   []RegMapping     // Trace registers → interpreter slot mapping
}

// LiveValueInfo tracks a value that needs to be restored to interpreter.
type LiveValueInfo struct {
    SSARef      int         // SSA value identifier
    InterpreterSlot int         // Interpreter register/slot index
    ValueType   runtime.ValueType // Expected type for materialization
    NeedsBox    bool        // True if unboxed value needs re-boxing
}

// RegMapping maps a trace register to an interpreter register.
type RegMapping struct {
    TraceReg   int   // Trace register index (0-27)
    InterpSlot int   // Interpreter register/slot index
}

// DeoptMetadata holds all deoptimization information for a compiled trace.
type DeoptMetadata struct {
    Guards       map[int]*GuardNode    // Guard ID → GuardNode
    Bailouts     map[int]*BailoutInfo  // BailoutID → BailoutInfo
    NextBailoutID int                  // Next ID to assign
}

// === Deoptimization Runtime ===

// DeoptContext carries state during deoptimization.
type DeoptContext struct {
    TraceID      int              // Which trace failed
    BailoutID    int              // Which bailout point
    GuardType    GuardType        // What kind of guard failed
    LiveValues   []runtime.Value // Live SSA values (from registers)
    Frame        *vm.Frame        // Interpreter frame to restore
    PC           int              // Bytecode PC to resume at
}

// MaterializerFunc reconstructs a heap object from SSA values.
type MaterializerFunc func(*DeoptContext, int) runtime.Value

// MaterializationInfo describes how to reconstruct a heap object.
type MaterializationInfo struct {
    TargetSSA    int              // SSA value to reconstruct
    Materializer MaterializerFunc // How to build the value
}
```

---

## Integration with Existing Systems

### 1. SSA Builder Integration

**File:** `internal/jit/ssa_build.go`

```go
type SSABuilder struct {
    // ... existing fields ...

    // Deoptimization metadata
    deoptMetadata *DeoptMetadata
    nextGuardID   int
}

// New SSA opcodes for guards
const (
    SSA_GUARD_INT32 SSAOp = iota    // Emit Int32 type check
    SSA_GUARD_FLOAT64                // Emit Float64 type check
    SSA_GUARD_NOT_NIL               // Emit nil check
    SSA_GUARD_BOUNDS                 // Emit bounds check
    SSA_GUARD_TABLE_SHAPE            // Emit shapeID check
    SSA_GUARD_ARRAY_KIND            // Emit arrayKind check
)

// Add guard to IR
func (b *SSABuilder) AddGuard(typ GuardType, value SSARef, expected interface{}, bailoutPC int) int {
    guardID := b.deoptMetadata.NextBailoutID
    b.deoptMetadata.NextBailoutID++

    guard := &GuardNode{
        Type:      typ,
        Value:     value,
        Expected:  expected,
        BailoutID: guardID,
        Location:  CodeLoc{
            FuncProto: b.proto,
            BytecodePC: b.currentPC(),
        },
    }

    b.deoptMetadata.Guards[guardID] = guard

    // Record bailout point
    b.deoptMetadata.Bailouts[guardID] = &BailoutInfo{
        BailoutID:   guardID,
        BytecodePC: bailoutPC,
        SnapshotIdx: b.currentSnapshotIdx(),
    }

    // Emit guard SSA instruction
    return b.NewInst(SSA_GUARD_INT32 + int(typ), value, b.Const(expected))
}

// Example: Emit type guard for arithmetic speculation
func (b *SSABuilder) buildADD(ir TraceIR) SSARef {
    lhs := b.Slot(ir.Slot1)
    rhs := b.Slot(ir.Slot2)

    // Speculate both operands are integers
    lhsGuard := b.AddGuard(GuardTypeInt32, lhs, nil, b.currentPC())
    rhsGuard := b.AddGuard(GuardTypeInt32, rhs, nil, b.currentPC())

    result := b.NewInst(SSA_ADD, lhsGuard, rhsGuard)
    return result
}
```

### 2. ARM64 Code Emitter Integration

**File:** `internal/jit/ssa_emit.go`

```go
type emitCtx struct {
    // ... existing fields ...

    // Deoptimization
    deoptMetadata *DeoptMetadata
}

// Emit guard instructions
func (ec *emitCtx) emitGuard(guard *GuardNode) {
    asm := ec.asm

    switch guard.Type {
    case GuardTypeInt32:
        // Check value tag == INT32_TAG
        // Value tag is in upper 16 bits of NaN-boxed representation
        asm.LSR(W0, guard.Value.Reg, 48)  // Shift right to get tag
        asm.CMP(W0, Immediate(runtime.TypeInt))
        asm.B.NE("guard_fail_" + strconv.Itoa(guard.BailoutID))

    case GuardTypeTableShape:
        // Check table.shapeID == expected_shapeID
        // Load table pointer (guard.Value is the table SSARef)
        tableReg := guard.Value.Reg
        asm.LDR(X0, [tableReg, TableShapeIDOffset()])  // Load shapeID
        expected := ec.Const(guard.Expected.(uint32))
        asm.LDR(W1, [expected.Reg, 0])  // Load expected ID
        asm.CMP(W0, W1)
        asm.B.NE("guard_fail_" + strconv.Itoa(guard.BailoutID))

    case GuardTypeBounds:
        // Check 0 <= index < array_length
        asm.LDR(W0, [guard.Value.Reg, 0])  // Load index
        asm.LDR(W1, [ec.arrayPtrReg, TableLenOffset()])  // Load length
        asm.CMP(W0, Immediate(0))
        asm.B.LT("guard_fail_" + strconv.Itoa(guard.BailoutID))
        asm.CMP(W0, W1)
        asm.B.GE("guard_fail_" + strconv.Itoa(guard.BailoutID))

    // ... other guard types
    }

    // Emit guard failure label
    asm.Label("guard_fail_" + strconv.Itoa(guard.BailoutID))
    ec.emitGuardFail(guard)
}

// Emit guard failure handler (replaces existing side_exit_setup)
func (ec *emitCtx) emitGuardFail(guard *GuardNode) {
    asm := ec.asm

    // Store ExitCode = 2 (guard fail)
    asm.LoadImm64(X0, 2)
    asm.STR(X0, regCtx, TraceCtxOffExitCode)

    // Store BailoutID in ExitPC (reused field)
    asm.LoadImm64(X9, int64(guard.BailoutID))
    asm.STR(X9, regCtx, TraceCtxOffExitPC)

    // Store back all registers (reuse existing emitStoreBack())
    ec.emitStoreBack()

    // Jump to epilogue
    asm.B("epilogue")
}
```

### 3. Deoptimization Handler Integration

**File:** New `internal/jit/deopt.go` OR extend `internal/jit/exit.go`

```go
package jit

import (
    "fmt"
    "runtime"
    "runtime/vm"
)

// Global deoptimization handler registry
var deoptHandlers = make(map[int]func(*DeoptContext))

// RegisterDeoptHandler registers a handler for a specific guard type.
func RegisterDeoptHandler(guardType GuardType, handler func(*DeoptContext)) {
    deoptHandlers[int(guardType)] = handler
}

// HandleDeopt is called from trace exit when ExitCode = 2.
// Entry point from trace execution epilogue.
func HandleDeopt(ctx *TraceContext, frame *vm.Frame) int64 {
    bailoutID := int(ctx.ExitPC)

    // Lookup bailout info from trace metadata
    traceMeta := getTraceMetadata(frame.TraceID)
    if traceMeta == nil {
        // Fallback: no metadata, just return generic PC
        return 0  // Resume at start of function
    }

    bailout, ok := traceMeta.Bailouts[bailoutID]
    if !ok {
        panic(fmt.Sprintf("deopt: unknown bailout ID %d", bailoutID))
    }

    // Build deopt context
    deoptCtx := &DeoptContext{
        TraceID:   frame.TraceID,
        BailoutID: bailoutID,
        Frame:     frame,
        PC:        bailout.BytecodePC,
    }

    // Call materializers for heap objects
    for _, liveVal := range bailout.LiveValues {
        val := materializeValue(deoptCtx, liveVal)
        frame.SetSlot(liveVal.InterpreterSlot, val)
    }

    // Restore register mappings
    for _, mapping := range bailout.FrameMapping {
        // If trace register has live value, copy to interpreter slot
        if regVal, ok := deoptCtx.LiveValues[mapping.TraceReg]; ok {
            frame.SetSlot(mapping.InterpSlot, regVal)
        }
    }

    // Restore from snapshot (if any)
    if bailout.SnapshotIdx >= 0 && bailout.SnapshotIdx < len(traceMeta.snapshots) {
        snapshot := traceMeta.snapshots[bailout.SnapshotIdx]
        restoreFromSnapshot(frame, snapshot)
    }

    // Set interpreter PC and return
    return bailout.BytecodePC
}

// materializeValue reconstructs a heap object from SSA registers.
func materializeValue(ctx *DeoptContext, liveVal LiveValueInfo) runtime.Value {
    traceReg := ctx.LiveValues[liveVal.SSARef]

    // If already boxed, return as-is
    if !liveVal.NeedsBox {
        return traceReg
    }

    // Unboxed values need reconstruction
    switch liveVal.ValueType {
    case runtime.TypeInt:
        // Int was unboxed to raw int64, re-box
        return runtime.IntValue(traceReg.Int())

    case runtime.TypeFloat:
        // Float was unboxed to raw float64, re-box
        return runtime.FloatValue(traceReg.Float())

    case runtime.TypeTable:
        // Table pointer is already correct (heap allocation)
        return traceReg

    default:
        // Other types are always boxed in current JIT
        return traceReg
    }
}

// restoreFromSnapshot restores interpreter state from a snapshot.
func restoreFromSnapshot(frame *vm.Frame, snapshot Snapshot) {
    for i, slot := range snapshot.slots {
        if val, ok := snapshot.values[slot]; ok {
            frame.SetSlot(slot, val)
        }
    }
}
```

---

## Phase-by-Phase Implementation Plan

### Phase 1: Foundation (Week 1)

**Files to create:**
- `internal/jit/deopt.go` — Deoptimization handler

**Files to modify:**
- `internal/jit/ssa_ir.go` — Add GuardType, GuardNode, BailoutInfo structs
- `internal/jit/ssa_builder.go` — Add guard emission methods
- `internal/jit/trace.go` — Add DeoptMetadata to CompiledTrace

**Changes:**
1. Define guard types and node structures
2. Add DeoptMetadata to CompiledTrace
3. Implement guard SSA opcodes (SSA_GUARD_INT32, etc.)
4. Create `emitGuard()` in code emitter
5. Implement `HandleDeopt()` entry point

**Tests:**
- Test guard registration and lookup
- Test bailout metadata tracking
- Test deopt handler with simple guards

**Expected Impact:** No performance change yet, but foundation for speculation.

---

### Phase 2: Type Guards (Week 2)

**Files to modify:**
- `internal/jit/ssa_builder.go` — Emit type guards for arithmetic
- `internal/jit/ssa_emit.go` — Emit Int32/Float64 guard code

**Changes:**
1. Add `GuardTypeInt32` guard to arithmetic operations
2. Add `GuardTypeFloat64` guard to float operations
3. ARM64: emit tag check for type guard
4. Register materializers for int/float unboxing

**Tests:**
- Test trace compiles with type guards
- Test guard failure on type mismatch
- Test JIT correctness vs interpreter (type stability tests)

**Expected Impact:** Enables 1.5-2x speedup on type-stable integer/float code.

---

### Phase 3: Shape Guards (Week 2-3, parallel with Phase 2)

**Files to modify:**
- `internal/jit/ssa_builder.go` — Emit shape guards for field access
- `internal/jit/ssa_emit.go` — Emit shapeID check code

**Changes:**
1. Add `GuardTypeTableShape` guard to GETFIELD/SETFIELD
2. ARM64: emit shapeID load + compare
3. Register shape deopt handler (no materialization needed)

**Tests:**
- Test shape guard emission
- Test guard failure on shape mismatch
- Test JIT correctness on polymorphic field access

**Expected Impact:** 20-50% speedup on monomorphic field access (nbody, method_dispatch).

---

### Phase 4: Bounds Guards (Week 3)

**Files to modify:**
- `internal/jit/ssa_builder.go` — Emit bounds guards for array access
- `internal/jit/ssa_emit.go` — Emit bounds check code

**Changes:**
1. Add `GuardTypeBounds` guard to LOAD_ARRAY/STORE_ARRAY
2. ARM64: emit index + length comparison
3. Bounds check hoisting to loop header (future optimization)

**Tests:**
- Test bounds guard emission
- Test guard failure on out-of-bounds access
- Test JIT correctness on array edge cases

**Expected Impact:** 1.2-1.5x speedup by eliminating redundant bounds checks.

---

## ARM64 Code Examples

### Int32 Guard (Type Speculation)

```arm64
; Speculative integer addition
; LHS and RHS are expected to be integers

; Guard: LHS is int32
LSR  W0, X_lhs, 48      ; Shift right to get tag (NaN-boxing)
CMP  W0, #TypeInt      ; Compare to Int type tag
B.NE Lguard_fail         ; Bail on mismatch

; Guard: RHS is int32
LSR  W1, X_rhs, 48
CMP  W1, #TypeInt
B.NE Lguard_fail

; Fast path: both are integers, unboxed addition
ADD  X0, X_lhs, X_rhs

; Continue...

Lguard_fail:
; Store ExitCode = 2
MOV  X0, #2
STR  X0, [regCtx, #TraceCtxOffExitCode]
; Store bailout ID
MOV  X9, #bailout_id
STR  X9, [regCtx, #TraceCtxOffExitPC]
; Store back registers
; ... emitStoreBack() ...
B    epilogue
```

### Table Shape Guard

```arm64
; Speculative field access: obj.x
; obj is expected to have shapeID = expected_shape

; Guard: table has expected shape
LDR  X0, [X_obj, #TableShapeIDOffset]  ; Load table.shapeID
MOV  W1, #expected_shape_id
CMP  W0, W1
B.NE Lguard_fail                              ; Bail on shape mismatch

; Fast path: direct svals[offset] load
LDR  X2, [X_obj, #TableSvalsOffset]   ; Load svals pointer
MOV  X3, #field_offset                          ; Known offset from shape
LDR  X4, [X2, X3]                           ; Load svals[field_idx]

; Continue with value in X4...

Lguard_fail:
; Store ExitCode = 2, bailout ID
; Jump to deopt handler
```

### Bounds Guard

```arm64
; Speculative array access: arr[i]
; Expected: 0 <= i < arr.length

; Guard: i >= 0
LDR  W0, [X_i, 0]
CMP  W0, #0
B.LT Lguard_fail

; Guard: i < arr.length
LDR  W1, [X_arr, #TableLenOffset]
CMP  W0, W1
B.GE Lguard_fail

; Fast path: direct array element load
LDR  X2, [X_arr, #8*index]   ; Load arr[i]

Lguard_fail:
; Store ExitCode = 2, bounds info for debugging
; Jump to deopt handler
```

---

## Memory and Performance Analysis

### Memory Overhead

**Per CompiledTrace:**
- DeoptMetadata: ~200 bytes (guards + bailouts maps)
- GuardNodes: ~100 bytes per guard (type, location, bailout info)
- BailoutInfo: ~150 bytes per bailout (live values, mapping)

**Total Overhead:** ~500-1000 bytes per trace (acceptable for JIT gains).

### Performance Impact

**Eager Deopt Cost:**
- Guard check: 1-3 ARM64 instructions per guard
- Guard fail: ~50-100 cycles (store registers, jump to handler)
- Handler overhead: ~100-200 cycles (materialization, frame restore)

**Speedup from Speculation:**
- Type speculation: 1.5-2x on type-stable code
- Shape guards: 2-5x on monomorphic field access
- Bounds hoisting: 1.2-1.5x on array access

**Net:** Guard cost << speculation gain for hot loops.

---

## Observability and Debugging

### Debug Mode

```go
const DebugDeopt = false  // Enable/disable debug output

func (h *DeoptHandler) debug(msg string, args ...interface{}) {
    if DebugDeopt {
        fmt.Printf("[DEOPT] "+msg+"\n", args...)
    }
}

// On guard failure:
debug("Guard failed: type=%v, id=%d, location=%s:%d",
    ctx.GuardType, ctx.BailoutID,
    guard.Location.FuncProto.Name, guard.Location.BytecodePC)

// On materialization:
debug("Materializing value: SSA=%d, type=%v",
    liveVal.SSARef, liveVal.ValueType)
```

### Deopt Statistics

```go
type DeoptStats struct {
    GuardFails      map[GuardType]int  // Count per guard type
    TotalDeopts     int64                // Total deoptimizations
    TraceBlacklists int64                // Traces disabled due to deopt storms
}

var globalDeoptStats = &DeoptStats{
    GuardFails: make(map[GuardType]int),
}

// On deopt:
globalDeoptStats.GuardFails[ctx.GuardType]++
globalDeoptStats.TotalDeopts++

// Dump stats (for pprof analysis):
func DumpDeoptStats() {
    fmt.Printf("Deopt Statistics:\n")
    fmt.Printf("  Total: %d\n", globalDeoptStats.TotalDeopts)
    for typ, count := range globalDeoptStats.GuardFails {
        fmt.Printf("  %v: %d\n", typ, count)
    }
}
```

### Built-In Observability

```go
// On guard failure, dump state for post-mortem analysis
func dumpGuardFailure(ctx *DeoptContext, guard *GuardNode) {
    fmt.Printf("\n=== GUARD FAILURE ===\n")
    fmt.Printf("Guard Type: %v\n", guard.Type)
    fmt.Printf("Bailout ID: %d\n", ctx.BailoutID)
    fmt.Printf("Expected: %v\n", guard.Expected)
    fmt.Printf("Location: %s:%d\n", guard.Location.FuncProto.Name, guard.Location.BytecodePC)
    fmt.Printf("Live Values: %d\n", len(ctx.LiveValues))

    // Dump trace registers
    for i, val := range ctx.LiveValues {
        fmt.Printf("  Reg[%d] = %v\n", i, val)
    }

    // Dump interpreter frame state
    fmt.Printf("Frame PC: %d\n", ctx.Frame.PC)
    fmt.Printf("=====================\n\n")
}
```

---

## Risk Assessment

### Risk 1: Incorrect Value Materialization

**Concern:** Unboxed values (int64, float64) may not be correctly boxed on deopt.

**Mitigation:**
- Unit tests for all value types
- Debug mode validates boxed format
- Materializers explicitly handle NaN-boxing

### Risk 2: Frame Restoration Bugs

**Concern:** Interpreter frame not fully restored, causing corruption.

**Mitigation:**
- Extensive testing of all guard types
- Post-mortem dump on deopt (compare expected vs actual frame)
- Use snapshot mechanism (already partially working)

### Risk 3: Deopt Storm (Too Many Failures)

**Concern:** Guard failures cause performance to degrade worse than interpreter.

**Mitigation:**
- Track deopt rate per trace
- Blacklist traces with high guard fail rate (>50%)
- Fallback to interpreter for "bouncy" patterns

### Risk 4: Memory Overhead

**Concern:** Deopt metadata increases memory usage significantly.

**Mitigation:**
- Store minimal metadata per trace
- Drop metadata for blacklisted traces
- Share bailout info where possible (same PC = same bailout)

---

## Success Criteria

1. **Foundation:**
   - Guard nodes can be created and retrieved
   - Bailout metadata maps guard IDs to recovery info
   - Deopt handler can restore interpreter state

2. **Type Guards:**
   - Integer/float guards emit correct ARM64 code
   - Guard failure triggers deopt handler
   - JIT output matches interpreter on type-stable code

3. **Shape Guards:**
   - ShapeID guards emit correct ARM64 code
   - Guard failure on shape mismatch works correctly
   - Monomorphic field access: 20-50% speedup

4. **Observability:**
   - Guard failures are logged with full context
   - Deopt statistics are trackable
   - Debug mode enables post-mortem analysis

---

## Future Extensions (Beyond Initial Implementation)

### Lazy Deoptimization

Mark functions for lazy deopt instead of immediate bailout:
- Continue in optimized code to safepoint
- Deopt at function exit or loop boundary
- Reduces deopt overhead for non-critical guards

### OSR (On-Stack Replacement)

Enable mid-execution tier-up:
- Compile hot loops while already running
- Migrate from interpreter → JIT without restart
- Required for real-world workloads

### Polymorphic Inline Caches

Track multiple observed shapes at a call site:
- Emit guard chain (check shape1, shape2, shape3)
- Reduce deopt rate for moderately polymorphic code
- Megamorphic fallback after 4 shapes

---

## References

- V8 Speculative Optimization Research: `docs/research/v8-speculation-deopt.md`
- Research Synthesis: `docs/research/SYNTHESIS.md`
- Current Implementation: `internal/jit/trace.go`, `internal/jit/ssa_emit.go`
- Shape System Design: `docs/design/shape-system-design.md`
