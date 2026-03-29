# Function Inlining Design for GScript Trace JIT

**Date:** 2026-03-27
**Author:** coder-inlining
**Status:** Design Document (Not Implemented)
**Related:** Task #8, Phase 3 of SYNTHESIS.md

---

## Executive Summary

**Problem:** The sort benchmark (and other recursive benchmarks) don't get JIT speedup because:
1. Functions with loops are NOT inlined (current restriction)
2. Self-recursive calls cause OP_CALL → call-exit overhead
3. Small helper functions cannot be inlined

**Goal:** 2-5x speedup on recursive/call-heavy benchmarks (fibonacci_recursive, ackermann, sort, function_calls)

**Approach:** Two complementary inlining strategies:
1. **Function Entry Traces** for self-recursive functions (fib, ackermann)
2. **Small Function Inlining** for non-recursive small functions (≤10 bytecode)

---

## Current Inlining Infrastructure

### Existing Implementation (trace_record.go)

```go
// handleCall attempts to inline a function call into the trace.
func (r *TraceRecorder) handleCall(ir TraceIR, regs []runtime.Value, base int) bool {
    // Check depth limit
    if r.depth >= r.maxDepth { ... }

    // Check for intrinsic (already inlined)
    if gf := fnVal.GoFunction(); gf != nil {
        if intrinsic := recognizeIntrinsic(gf.Name); intrinsic != IntrinsicNone { ... }
    }

    // Check for self-recursion → SSA_SELF_CALL (BL to function entry)
    if cl.Proto == r.current.LoopProto {
        ir.IsSelfCall = true
        ...
    }

    // Check if callee has any loop (for-loop or while-loop) — CAN'T inline those
    for i, inst := range cl.Proto.Code {
        if op == vm.OP_FORPREP || (op == vm.OP_JMP && sbx < 0) {
            r.current.IR = append(r.current.IR, ir)
            return false  // ← NOT inlined
        }
    }

    // Simple callee without loops: inline it.
    // ← This is where we extend inlining
    irCopy := ir
    r.inlineCallProto = cl.Proto
    r.inlineCallIR = &irCopy
    r.inlineCallDepth = r.depth
    r.skipNextJIT = true
    r.inlineCallStack = append(r.inlineCallStack, ir.A)
    r.depth++
    return false
}
```

### Return Handling (trace_record.go)

```go
// recordInlinedReturn handles RETURN from an inlined function.
func (r *TraceRecorder) recordInlinedReturn(ir TraceIR, ...) {
    if len(r.inlineCallStack) > 0 && origB >= 2 {
        callDst := r.inlineCallStack[len(r.inlineCallStack)-1]
        r.inlineCallStack = r.inlineCallStack[:len(r.inlineCallStack)-1]
        retSrc := ir.A
        if retSrc != callDst {
            // Emit synthetic MOVE from callee's return to caller's destination
            moveIR := TraceIR{
                Op:  vm.OP_MOVE,
                A:  callDst,  // ← Caller's destination register
                B:  retSrc,  // ← Callee's return register
                ...
            }
            r.current.IR = append(r.current.IR, moveIR)
        }
    }
    r.depth--
}
```

---

## Design: Strategy 1 — Function Entry Traces

### Purpose
Enable self-recursive functions (fib, ackermann) to call themselves with native BL instead of call-exit.

### Current State
- `SSA_SELF_CALL` exists and works for function-entry traces
- Emitted code uses BL to the compiled function entry point
- Side-exit restores state correctly

### Gap
Only functions WITHOUT loops become function-entry traces. Functions with loops (like quicksort) are rejected.

### Proposed Enhancement: Loop-Free Self-Recursion

For self-recursive functions that contain loops, emit `SSA_SELF_CALL` if:
1. Loop body is simple (no nested function calls, no side-exits inside loop)
2. Loop has single entry/exit point (for-style loop, not while)
3. Function byte count ≤ 30 (arbitrary budget to prevent bloat)

This allows `quicksort(arr, lo, hi)` to be compiled as a function-entry trace, and its recursive calls use BL.

### Implementation Plan

1. **Relax loop check** in `handleCall`:
```go
// OLD: Reject any function with loops
if hasLoop(cl.Proto) {
    reject()
}

// NEW: Allow loops for self-calls if simple
if cl.Proto == r.current.LoopProto && hasLoop(cl.Proto) {
    if isSimpleLoopBody(cl.Proto) && byteCount(cl.Proto) <= 30 {
        // Accept: function-entry trace
    } else {
        reject()
    }
}
```

2. **Add `isSimpleLoopBody` check**:
```go
func isSimpleLoopBody(proto *vm.FuncProto) bool {
    // No nested function calls
    // No OP_CALL (non-intrinsic)
    // No table operations that could trigger side-exits (GETTABLE/SETTABLE with dynamic keys)
    // Single loop structure
}
```

3. **Add `byteCount` check**:
```go
func byteCount(proto *vm.FuncProto) int {
    // Count bytecode instructions
    // Exclude constants, debug info
}
```

### Risks & Mitigations

| Risk | Mitigation |
|-------|------------|
| Loop body has side-exits (guards) | Same as normal function-entry trace — already handled |
| Recursive depth grows stack | Stack growth on BL calls is handled by Go runtime (NOSPLIT check already in place) |
| Code bloat from large functions | Byte count budget (≤30) prevents bloat |

---

## Design: Strategy 2 — Small Function Inlining

### Purpose
Inline small, pure functions directly into caller's trace (like how intrinsics work).

### Current State
- Only loop-free functions are inlined
- Works via `inlineCallStack` for return value mapping
- Emits synthetic MOVE on RETURN

### Proposed Enhancement: Allow Small Loops

Extend inlining to functions with loops IF:
1. Byte count ≤ 10 (very small threshold)
2. Single simple loop (no nested loops)
3. No nested function calls inside loop

### Why Loops in Small Functions?

Functions like:
```go
func sum_array(arr, n) {
    total := 0
    for i := 1; i <= n; i++ {
        total = total + arr[i]
    }
    return total
}
```

Are useful to inline even though they have a loop:
- Eliminates call overhead
- Allows loop optimization passes (CSE, unrolling) to work across function boundary
- Enables better register allocation (caller's registers can be used directly)

### Implementation Plan

1. **Modify `handleCall` loop check**:
```go
// OLD: Reject ANY function with loops
for i, inst := range cl.Proto.Code {
    if op == vm.OP_FORPREP || (op == vm.OP_JMP && sbx < 0) {
        reject()
    }
}

// NEW: Allow simple small loops
hasLoop := false
for i, inst := range cl.Proto.Code {
    if op == vm.OP_FORPREP || (op == vm.OP_JMP && sbx < 0) {
        hasLoop = true
        break
    }
}

if hasLoop {
    if byteCount(cl.Proto) > 10 || !isSimpleLoopBody(cl.Proto) {
        reject()
    } else {
        // Inline: small function with simple loop
    }
}
```

2. **Preserve existing inlining behavior**:
- `inlineCallStack` still tracks return mapping
- `recordInlinedReturn` still emits synthetic MOVE
- `skipDepth` still skips nested callee instructions

### SSA IR Considerations

No new SSA opcodes needed. Inlined instructions are added to the trace IR directly.

---

## Design: Strategy 3 — Inlining Budget

### Purpose
Prevent code bloat from aggressive inlining.

### Budget Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Max byte count (with loops) | 10 | Very small functions only |
| Max byte count (loop-free) | 20 | Slightly larger pure functions |
| Max inline depth | 3 | Prevent exponential growth |
| Max inlined calls per trace | 5 | Prevent trace bloat |

### Implementation

```go
type InliningBudget struct {
    MaxByteCount      int
    MaxInlineDepth    int
    MaxInlinedCalls   int
}

func (r *TraceRecorder) shouldInline(proto *vm.FuncProto, budget *InliningBudget) bool {
    // Check byte count
    if byteCount(proto) > budget.MaxByteCount {
        return false
    }
    // Check inline depth
    if r.depth - r.inlineCallDepth >= budget.MaxInlineDepth {
        return false
    }
    // Check inlined calls count in current trace
    if countInlinedCalls(r.current.IR) >= budget.MaxInlinedCalls {
        return false
    }
    return true
}
```

---

## Deoptimization Implications

### Current Behavior
When a guard fails in a trace with inlined functions:
- Side-exit to interpreter at `ExitPC`
- Interpreter resumes execution
- No special handling for inlined calls

### Proposed Changes

No changes needed to deoptimization! Inlined functions are just part of the trace IR:
- Guards are emitted for inlined code
- Side-exit restores full VM state
- No additional complexity

### Example

```go
// Caller trace (with inlined function)
LOAD_SLOT x         // ← Caller's register
LOAD_CONST 10
ADD_INT
STORE_SLOT result      // ← Caller's result
// Inlined function body:
LOAD_SLOT arr
LOAD_SLOT 5
ADD_INT              // ← Optimized by CSE, eliminated by loop unrolling
STORE_SLOT local
RETURN → synthetic MOVE to result slot
// Guard check
GUARD_TYPE result
```

On guard failure, side-exit at PC of the original STORE_SLOT instruction. The inlined function's state is not visible to the interpreter (correct, since it was never executed).

---

## Expected Impact

### Benchmarks Affected

| Benchmark | Current Speedup | Expected with Inlining | Improvement |
|-----------|------------------|------------------------|-------------|
| fibonacci_recursive | 1.0x | 3-10x | +3-9x |
| ackermann | 0.7x | 3-8x | +3-7x |
| sort | 0.97x | 2-4x | +1-3x |
| function_calls | 0.6x | 2-4x | +1-3x |

### Overall Impact

- 2-5x speedup on 4 benchmarks that currently get < 1.2x
- Enables recursive functions to be competitive with LuaJIT
- No negative impact on existing fast benchmarks (no inlining overhead for already-fast code)

---

## Implementation Steps

### Step 1: Add Helper Functions (1 day)
1. `byteCount(proto *vm.FuncProto) int`
2. `isSimpleLoopBody(proto *vm.FuncProto) bool`
3. `countInlinedCalls(ir []TraceIR) int`

### Step 2: Modify handleCall (2 days)
1. Relax loop rejection for self-calls with simple loops
2. Add small loop inlining (≤10 byte count)
3. Integrate inlining budget checks
4. Add tests for new inlining behavior

### Step 3: Benchmark & Tune (1 day)
1. Run fibonacci_recursive, ackermann, sort benchmarks
2. Tune byte count threshold (10 vs 15 vs 20)
3. Tune loop simplicity criteria
4. Profile for code bloat

### Step 4: Documentation (0.5 day)
1. Update JIT architecture document
2. Add inline decisions to trace dump (debug mode)
3. Document inlining budget parameters

---

## Testing Strategy

### Unit Tests

```go
func TestInlining_LoopFreeFunction(t *testing.T) {
    // Verify loop-free function is inlined
}

func TestInlining_SmallLoopFunction(t *testing.T) {
    // Verify ≤10-byte function with loop is inlined
}

func TestInlining_LargeLoopFunction(t *testing.T) {
    // Verify >10-byte function with loop is NOT inlined
}

func TestInlining_RecursiveWithLoop(t *testing.T) {
    // Verify self-recursive function with loop gets SSA_SELF_CALL
}
```

### Integration Tests

```go
func TestInlining_QuickSort(t *testing.T) {
    // Verify quicksort compiles and produces correct results
}

func TestInlining_Fibonacci(t *testing.T) {
    // Verify fibonacci_recursive compiles and produces correct results
}
```

### Benchmarks

```bash
./gscript benchmarks/suite/fibonacci_recursive.gs  # Expect 3-10x
./gscript benchmarks/suite/ackermann.gs             # Expect 3-8x
./gscript benchmarks/suite/sort.gs                 # Expect 2-4x
./gscript benchmarks/suite/function_calls.gs        # Expect 2-4x
```

---

## Open Questions

1. **Byte count vs instruction count?**
   - Byte count is easier to compute
   - Instruction count may be more accurate
   - Start with byte count, switch if needed

2. **Should inline threshold be configurable?**
   - Start with hardcoded (10 bytes)
   - Make configurable via TraceRecorder.SetInlineThreshold() if profiling shows value

3. **How to measure "bouncy" inlining?**
   - Functions that oscillate between inlined/not-inlined states
   - May need profiling to detect
   - Could disable inlining for functions with high deopt rate

---

## References

- LuaJIT: Self-recursive loop optimization (rec + unroll)
- V8 Maglev: Small function inlining budget
- SYNTHESIS.md: Phase 3 context
- Current GScript inlining: trace_record.go handleCall, recordInlinedReturn
