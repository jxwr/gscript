# The Long Road Back

We rewrote the JIT compiler two weeks ago. Deleted the Method JIT, went trace-only, got clean SSA and 11x on table access. It felt like progress. Then we looked at the numbers we'd lost.

## The Cost of the Rewrite

Before the rewrite (commit `badd2b8c`), the two-tier JIT had:

| Benchmark | Old JIT | LuaJIT | Gap |
|-----------|---------|--------|-----|
| fib(35) | **34ms** | 23ms | 1.4x |
| ackermann(3,4 x500) | **12ms** | 6ms | 2.0x |
| sieve(1M x3) | **23ms** | 11ms | 2.1x |
| mandelbrot(1000) | **158ms** | 58ms | 2.7x |

The rewrite deleted all of that. The new trace-only JIT couldn't compile recursive functions at all. fib went from 34ms to 1,550ms — a **45x regression**. sieve went from 23ms to 140ms. mandelbrot from 158ms to 260ms. We'd traded the old Method JIT's self-call inlining, register pinning, and cross-call BLR for clean architecture and correct snapshots.

The question for this round: can we get back?

## What We Tried (and What Failed)

### Attempt 1: Inline Function Calls in Traces

The profiler showed spectral_norm's inner loop calls `A(i,j)` — a tiny pure function. The trace recorder already had inlining infrastructure (depth tracking, synthetic MOVEs for args). If we could just kill the dead GETGLOBAL for the function reference, the trace should compile.

It worked at the trace IR level. The GETGLOBAL was marked dead, the SSA builder skipped it, `SSAIsUseful()` no longer rejected the trace.

Then the trace compiled. And crashed.

The problem: all recursion depths share the same `regRegs` memory. The inlined callee's temporary registers (depth > 0) overlap with the caller's register space. The store-back writes callee temporaries to the same memory slots the caller uses for its own data. We added `MaxDepth0Slot` tracking to limit store-back, but the corruption was deeper — the register allocator itself assigns ARM64 registers to callee slots, and nested execution clobbers them.

**Reverted.** Function call inlining in traces needs an architectural fix to the register allocator — separate register namespaces for different inlining depths, or stack-based temporaries instead of shared memory.

### Attempt 2: Fix the Build First

The committed code at HEAD didn't compile. `ssa_emit.go` referenced `SSA_SELF_CALL`, `FuncReturnCount`, `isFuncTrace` — symbols that only existed in uncommitted development files. A fresh `git clone` would fail.

We spent an entire sub-round just making the code build:
- Added `build_stubs.go` with temporary definitions
- Committed 11 untracked files (DCE, load elimination, strength reduction, disassembler, self-call emission, SSA pipeline)
- Dissolved the stubs into proper locations
- Fixed 8 pre-existing test failures (float store-back bug + SSA type test setup bug)

This was pure cleanup, zero performance impact. But necessary — you can't optimize code that doesn't compile.

### The Three Bugs

While investigating the function inlining failure, we found three real bugs:

**Bug 1: Division Always Returns Float.** GScript follows Lua semantics: `/` always returns float. The SSA builder was creating `SSA_DIV_INT` when both operands were integers, storing raw int64 values where IEEE 754 doubles were expected. The NaN-boxing mismatch corrupted everything downstream. The expression `(i+j)*(i+j+1)/2` in spectral_norm was the canary.

**Bug 2: BOX_INT Slot Zero Corruption.** The `emitIntToFloat` helper created temporary `SSA_BOX_INT` instructions with `Slot=0` (Go's zero-value default). The emitter's `spillFloat` then stored the converted float to VM slot 0 — overwriting `time.now()` tables, function references, whatever happened to be in slot 0. We spent hours chasing "bad argument #1 to time.since: expected time table, got number" before realizing the spill was writing to the wrong slot.

**Bug 3: Shared Memory in Self-Calls.** After finally getting function-entry traces working, fib(35) ran at 46ms but returned 291 instead of 9,227,465. The issue: when fib(4) calls fib(3) then fib(2), both nested calls use the same `regRegs` memory for intermediate results. The first call's result (stored in `memory[2]`) was overwritten by the second call's internal computation. Fix: use X28 (a callee-saved register) instead of shared memory for intermediate self-call results.

### The Modularization

`ssa_emit.go` had grown to 3,121 lines. Every concern was mixed together: prologue, arithmetic, guards, table operations, store-back, intrinsics, exit handlers. We split it into 8 files:

```
ssa_emit.go          (1116) → entry points, dispatch
ssa_emit_table.go     (768) → table/field/array/global
ssa_emit_exit.go      (296) → store-back, exits
ssa_emit_prologue.go  (271) → prologue, guards
ssa_emit_resolve.go   (232) → operand resolution
ssa_emit_intrinsic.go (185) → intrinsics
ssa_emit_guard.go     (179) → comparisons
ssa_emit_arith.go     (126) → arithmetic
```

Then split `ssa_build.go` and test files. No file exceeds 1,200 lines now. Pure mechanical refactoring — zero logic changes, zero behavior changes.

### Function-Entry Tracing

The big feature. When `fib(n)` is called 50 times, the recorder captures one execution of the body. The SSA builder converts recursive calls to `SSA_SELF_CALL`. The emitter generates ARM64 `BL` instructions.

Three sub-problems:
1. **Dead GETGLOBAL**: `SSAIsUseful()` rejected traces with non-table `LOAD_GLOBAL` for the function reference. NOP'd it in the SSA builder.
2. **While-loop exit misclassification**: `OptimizeSSA` marked the base-case guard (`n < 2`) as a while-loop exit, inverting the guard polarity. The trace exited when `n >= 2` instead of when `n < 2`. Skipped while-loop detection for function traces.
3. **Shared memory corruption**: Bug 3 above. Used X28 instead of memory.

Result: fib(35) = 46ms, correct. 33.6x speedup over interpreter.

## Where We Are Now

| Benchmark | Old JIT | New JIT | vs Old | LuaJIT | vs LuaJIT |
|-----------|---------|---------|--------|--------|-----------|
| fib(35) | 34ms | 46ms | **1.4x slower** | 23ms | 2.0x |
| sieve | 23ms | 130ms | **5.7x slower** | 10ms | 13.0x |
| mandelbrot | 158ms | 276ms | **1.7x slower** | 51ms | 5.4x |
| ackermann | 12ms | BROKEN | -- | 6ms | -- |
| table_field | -- | 65ms | **NEW** | -- | -- |
| nbody | -- | 313ms | **NEW** | 32ms | 9.8x |
| matmul | -- | 215ms | **NEW** | 21ms | 10.2x |

Honest assessment: **we haven't caught up.** fib is 1.4x slower than the old Method JIT. sieve is 5.7x slower. mandelbrot is 1.7x slower. ackermann is broken (2-arg recursion not implemented).

What the new architecture gained: table operations (11x), nbody (5.8x), matmul (4.5x) — benchmarks the old JIT couldn't touch. And clean, maintainable code with proper SSA, snapshots, and modular files.

What it lost: the Method JIT's register pinning for loops (sieve's `findAccumulators`), multi-argument self-call (ackermann), and raw speed from compiling entire functions instead of traces.

## What's Next

The gap against the old architecture comes from three missing features:

1. **Register pinning for for-loops** — the old `codegen_loop.go` pinned loop variables AND accumulators to callee-saved registers. The new register allocator is frequency-based but doesn't detect accumulator patterns. Porting `findAccumulators` would close the sieve gap.

2. **Multi-argument self-calls** — ackermann needs two parameters (m, n) saved across BL. Currently only single-arg works (X28 holds the intermediate result). Need a second overflow register or stack-based argument passing.

3. **Nested loop compilation** — mandelbrot's y/x loops run in the interpreter (only the innermost iter loop compiles). The old JIT compiled entire functions, including nested loops. Enabling nested loop tracing would give 2-3x on mandelbrot.

The gap against LuaJIT is larger — 5-100x on most benchmarks — and comes from deeper architectural differences: LuaJIT traces through function calls seamlessly, compiles all three loops in mandelbrot as one trace, and has a much faster allocator and C-based interpreter. Closing that gap requires the features above plus trace-through-calls, dual-path conditionals, and better native code quality.

## The Diagnostic Tools We Built

The debugging pain led to something useful: a diagnostic toolkit designed around the principle "make bugs visible from data, not from reasoning."

### DiagnoseTrace — one call, full picture

```go
diag := DiagnoseTrace(trace, regs, proto, DiagConfig{
    WatchSlots: []int{0, 1, 2, 3, 4},
    ShowASM:    true,
    MaxIter:    10,
})
t.Log(diag)
```

One function call gives you: pipeline stage status (ok/error for each of the 7 passes), final SSA IR, register allocation map, registers BEFORE and AFTER execution with raw NaN-boxing hex (`[0]=0xfffe000000000001 int(1)`), exit code, exit PC, iteration count, and optionally ARM64 disassembly.

The hex dump is critical. When we saw `[0]=0x425d1a968a48c000 type=3` after a trace exit, the `0x425d...` prefix (no `0xFFFE` tag) immediately told us slot 0 was overwritten with a raw float — not a type-tagged int or table pointer. That's how we found the BOX_INT slot-zero bug: the hex made the corruption visible without tracing the causal chain.

### CompileWithDump — binary search the pipeline

```go
ct, dump := CompileWithDump(trace)
t.Log(dump.Diff("BuildSSA", "ConstHoist"))
```

Records SSA state at every pipeline stage (BuildSSA → OptimizeSSA → ConstHoist → CSE → FMA → RegAlloc → Emit). The `Diff` function shows exactly what changed between two stages. If ConstHoist moved a constant but broke the ref numbering, the diff shows it.

This is LLVM's `-print-after-all` adapted for a trace JIT. The key insight: when the output is wrong, you don't need to understand the whole pipeline — you need to find which stage introduced the error. Binary search: dump before and after each stage, find the first diff that looks wrong.

### Why these tools matter for AI agents

A human debugging the self-call memory corruption would:
1. Hypothesize "maybe the memory is shared"
2. Mentally simulate 3 recursion depths
3. Notice the write-read conflict on slot 2
4. Fix it

An AI agent can't reliably do step 2-3. But it CAN:
1. Call `DiagnoseTrace` with `MaxIter: 1`
2. Read the hex dump
3. Notice that slot 2 changed between iteration 1 and iteration 2 when it shouldn't have
4. Report the anomaly

The tools convert "deep causal reasoning" into "pattern matching on dump output" — something AI is much better at. We haven't fully realized this potential yet (the self-call bug was still found by human reasoning), but the tools make it possible in principle.

## Where AI Debugging Hits the Wall

This project is built entirely by AI agents (Claude). This round exposed the limits.

**What worked**: Mechanical refactoring (file splitting), research (LuaJIT architecture analysis), benchmark running, test writing, and pattern-matching on known bug categories. Agents can split a 3,000-line file into 8 modules, run benchmarks, and identify that "LOAD_GLOBAL rejects the trace" by reading SSAIsUseful().

**What didn't work**: Multi-step causal reasoning through the JIT pipeline.

The BOX_INT slot-zero bug is a good example. The symptom was `time.since` crashing with "expected time table, got number." An agent reading the emitter code sees `spillFloat` writes to `regRegs + slot*ValueSize`. It reads that `slot` comes from `inst.Slot`. It reads that `emitIntToFloat` creates a `BOX_INT` with no Slot set. It _should_ conclude that `Slot=0` causes a write to slot 0. But this chain — default Go zero value → Slot field → spillFloat path → memory offset → slot 0 overwrite — crosses 4 files and 3 abstraction layers. The agent tried multiple wrong hypotheses (store-back issue, register allocation conflict, float type mismatch) before human intervention narrowed it to "check what value Slot has in the BOX_INT instruction."

The shared-memory corruption in self-calls was worse. The symptom: fib(35) returns 291. The fix: use X28 instead of `memory[dstSlot]`. But finding it required mentally simulating nested ARM64 execution across 3 recursion depths, tracking which memory slots are read/written at each level, and noticing that the second BL's internal computation overwrites a slot that the first BL's result was stored in. This is a 15-step causal chain that crosses the Go emitter, ARM64 codegen, and runtime memory layout.

**The pattern**: AI agents excel at breadth (try many things fast) but struggle with depth (trace one execution path through 7 pipeline stages). The `/diagnose` skill with `DiagnoseTrace()` and `CompileWithDump()` helps — it shortens the chain by dumping intermediate state. But for bugs that span the emitter→ARM64→execution→memory boundary, human insight is still needed to ask the right "what if" question.

**What might help**:
- Better diagnostic tools that dump the FULL execution state at each self-call depth (not just entry/exit)
- A trace simulator that runs the generated ARM64 symbolically, checking for memory conflicts
- Smaller, more focused agents: instead of one agent debugging end-to-end, have one that dumps state and another that analyzes the dump

The goal isn't to replace human reasoning but to make the agent's observation surface large enough that the bug becomes obvious from the data, without requiring deep causal chains.

## Summary

The rewrite gave us a solid foundation. This round got us partway back — fib recovered, nbody/matmul are new wins, the code is modular and all tests pass. But sieve, mandelbrot, ackermann are still behind where they were. The road back is longer than expected, and the next steps (register pinning, nested loop tracing, multi-arg self-calls) each require careful work at the ARM64 level where AI debugging is hardest.
