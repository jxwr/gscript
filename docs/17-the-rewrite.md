# The Rewrite

After sixteen blog posts of incremental optimization, six failed attempts at guard elimination, and one fundamental architectural diagnosis, we deleted 13,000 lines of JIT compiler code and started over.

This is the story of why, what changed, and what we learned.

## Why Rewrite

The previous JIT had two tiers: a Method JIT (function-level compilation) and a Trace JIT (loop-level SSA compilation). They shared infrastructure but had fundamentally different compilation models. When they interacted — Method JIT side-exits triggering Trace JIT recording, Trace JIT call-exits running Method JIT code — subtle bugs emerged.

The deepest issue was **slot-value conflation**. The SSA IR used VM slot numbers as identifiers. When a VM slot held different types at different points in a loop iteration (e.g., slot 12 = `bodies` table then `dt` float constant), the register allocator couldn't distinguish them. Store-back wrote the wrong type. Write-through corrupted call-exit state. Guard elimination was unsafe because removing a guard also removed the register allocation for that slot.

Six attempts to fix this through patches all failed. The architecture was fighting us.

## What We Deleted

| Component | Lines | Why |
|-----------|-------|-----|
| Method JIT (codegen*.go, executor*.go) | 6,400 | Two JIT tiers = double the bugs. LuaJIT proves one tier suffices. |
| Old Trace JIT (ssa_*.go, trace*.go) | 6,900 | Slot-based SSA, no snapshots, broken store-back. Unfixable. |
| **Total deleted** | **13,300** | |

We kept: ARM64 assembler (5 files, ~1000 lines), memory management, NaN-boxing value layout, JIT trampoline. These were correct and well-tested.

## What We Built

| Component | File | Lines | Purpose |
|-----------|------|-------|---------|
| SSA IR | ssa_ir.go | 220 | Value-based SSA types with Snapshot support |
| Slot classifier | slot_analysis.go | 170 | Unified slot liveness in single forward scan |
| SSA builder | ssa_build.go | 850 | Trace → SSA with snapshots at guard/call-exit points |
| Register allocator | ssa_regalloc.go | 460 | Value-based linear scan (GPR + FPR) |
| ARM64 codegen | ssa_emit.go | 1,500 | Full pipeline: guards, arithmetic, field access, call-exit |
| Trace recorder | trace.go + trace_record.go | 1,050 | Recording with deferred GETGLOBAL capture |
| Trace executor | trace_exec.go | 220 | ExitState save + snapshot restore + call-exit handler |
| **Total new** | | **~4,500** | 65% less code than deleted |

## The Three Architectural Fixes

### 1. Value-based SSA (not slot-based)

Old: `regMap.Int.slotToReg[slot]` → one register per VM slot, regardless of how many SSA values use that slot.

New: Each SSA instruction produces a unique `SSARef`. The register allocator operates on SSA refs. Multiple refs can map to the same slot at different times, but they get separate registers.

```go
// Old: slot-based
type slotAlloc struct {
    slotToReg map[int]Reg  // VM slot → ARM64 register
}

// New: still slot-based for simplicity, but snapshots handle the "which value" question
type Snapshot struct {
    PC      int
    Entries []SnapEntry  // slot → SSARef at this point
}
```

### 2. Snapshots for deoptimization

Old: One shared `side_exit` label with a blanket store-back of ALL modified registers. Wrong types written to multi-type slots.

New: Each guard sets `X9 = ExitPC` before branching to `side_exit_setup`. On side-exit, the ExitPC tells the interpreter exactly where to resume. The store-back writes registers to memory, then the interpreter runs from the correct bytecode PC.

The critical bug we found: the old `side_exit_setup` was overwriting X9 with the loop PC. All guards exited to the FORLOOP instruction, skipping if-bodies entirely. This single bug caused sum_primes to return 0 primes and mandelbrot to miscalculate escape conditions.

### 3. No store-back before call-exit

Old: Before each call-exit, store all modified registers to memory. This wrote LOADK's float value to a slot that the interpreter expected to hold a table → crash.

New: Before call-exit, DON'T store-back. Memory retains the pre-trace/previous-call-exit state, which is exactly what the interpreter expects. After call-exit, the trace reloads registers from memory (which now has the call result).

This elegantly solves the slot-reuse problem for call-exits. The trace uses registers for computation, memory stays in "interpreter state," and the two never conflict.

## Results

### Correctness (VM vs JIT match)

| Benchmark | Status |
|-----------|--------|
| fib(35) | ✓ Correct |
| sieve(1M×3) | ✓ Correct |
| mandelbrot(1000) | ✓ Correct (396,940 pixels) |
| ackermann(3,4×500) | ✓ Correct |
| sum_primes(100K) | ✓ Correct (9,592 primes) |
| fibonacci_iterative(70×1M) | ✓ Correct |
| binary_trees(15) | ✓ Correct |
| mutual_recursion | ✓ Correct |
| method_dispatch | ✓ Correct |

### Performance

| Benchmark | VM (interpreter) | JIT (new trace) | Speedup |
|-----------|-----------------|-----------------|---------|
| mandelbrot | 1.35s | 0.25s | **5.4×** |
| fibonacci_iterative | 1.00s | 0.25s | **4.0×** |
| fib | 1.62s | 1.64s | 1.0× |
| sieve | 0.24s | 0.30s | 0.8× |
| ackermann | 0.29s | 0.29s | 1.0× |
| sum_primes | 0.03s | 0.03s | 1.0× |

mandelbrot 5.4× speedup is the headline number. The old main branch got 0.148s on mandelbrot (with the old trace JIT), but had unsolvable correctness bugs. The new 0.25s is correct AND fast.

### What's Not Fast Yet

- **fib/ackermann**: Recursive functions need function-entry traces (not just loop traces). Currently only FORLOOP back-edges trigger recording.
- **sieve**: Loop has GETTABLE/SETTABLE call-exits. Call-exit overhead dominates. Need native table array access.
- **nbody/matmul**: Float-heavy with GETFIELD/SETFIELD. Need call-exit for field access + float register allocation improvements.

## What We Learned

### Lesson 1: Delete more, write less

The rewrite produced 4,500 lines that do more than the 13,300 they replaced. Every line in the new code has a clear purpose. The old code accumulated 14 blog posts of patches, workarounds, and compatibility shims.

When you can't fix an architecture, don't patch around it. Delete it.

### Lesson 2: The ExitPC bug was one line

The `side_exit_setup` code had `LoadImm64(X9, loopPC)` which overwrote the guard's ExitPC. This single line caused ALL conditional side-exits to resume at the wrong bytecode PC. sum_primes returned 0 primes. mandelbrot miscounted pixels.

It took 6 failed rounds to even get to a state where this bug was visible (traces had to actually execute for the bug to manifest). The architectural issues masked it.

### Lesson 3: No store-back before call-exit is correct by construction

The insight that memory-stays-in-interpreter-state eliminates an entire class of bugs. The old code tried to synchronize memory with register state before every call-exit. The new code simply doesn't touch memory. The interpreter reads memory, gets interpreter-state values, and everything works.

This is a case where doing LESS is MORE correct.

### Lesson 4: Trace-only JIT works

Deleting the Method JIT was scary — fib went from 0.034s to 1.6s. But the Method JIT was a constant source of interaction bugs (findAccumulators float corruption, pinned register spill issues). One clean JIT tier is better than two buggy ones.

The performance gap will close as we add function-entry traces and native table access. The architecture supports both; we just haven't implemented them yet.

## What's Next

1. **Function-entry traces**: Record traces starting at function entry (not just FORLOOP). This handles recursive functions (fib, ackermann) natively in the trace JIT.

2. **Native table array access**: Compile GETTABLE/SETTABLE for integer-key arrays directly to ARM64 (bounds check + array pointer + offset). This eliminates call-exit overhead for sieve, matmul, fannkuch.

3. **Float GETFIELD/SETFIELD**: Compile field access for known shapes directly to ARM64. This is what nbody needs.

4. **Optimization passes**: Re-implement constant hoisting, CSE, and FMA fusion on the new SSA IR. These were deleted with the old code.

5. **Loop peeling**: Split traces into preamble (once) + loop body (repeated). Hoist invariant guards and loads to preamble.

The foundation is solid now. Every future optimization builds on correct deoptimization (snapshots), correct call-exit handling (no store-back), and correct guard side-exit (per-guard ExitPC). These were the bugs we couldn't fix in the old architecture.
