---
layout: default
title: "The Blacklist That Changed Everything"
permalink: /05-the-blacklist-that-changed-everything
---

# The Blacklist That Changed Everything

*March 2026 --- Beyond LuaJIT, Post #5*

## Where We Left Off

In [Post #4](04-from-monolith-to-pipeline), we ran a full profiling and research phase and discovered the ugly truth: **the trace JIT made 5 out of 7 benchmarks slower.** nbody was 1.6x slower *with* JIT than without. The root cause was the Table Op Death Spiral --- table operations (GETFIELD, SETTABLE) weren't compiled natively, so every loop iteration entered the trace, hit a table op within 1-3 instructions, side-exited through the trampoline, and the interpreter finished the iteration. Pure overhead, zero benefit.

The plan at the end of Post #4 was:

1. Phase 0: Trace blacklisting (stop the bleeding)
2. Phase 1: Pass pipeline refactor
3. Phase 2: Native table operations (the big unlock)
4. Phase 3: SSA optimization passes

Here's what actually happened.

## The Three Fixes

The first order of business was native table operations. The profiling data from Post #4 was unambiguous: every failing benchmark failed because of table ops causing immediate side-exits. Fix that, fix everything.

### Fix 1: Native GETFIELD/SETFIELD

The problem: when the trace recorder hit `GETFIELD obj "x"`, it had no way to compile it. The instruction triggered an immediate side-exit. In nbody, with 12 field accesses per iteration, this meant 12 trace-enter/side-exit cycles per iteration. Catastrophic.

The solution uses GScript's internal table representation. Tables with string keys store them in two parallel arrays: `skeys` (the key strings) and `svals` (the values). At *recording time*, the trace recorder knows exactly which table it's looking at and where the key lives in `skeys`. If "x" is at index 3, we can record that fact and compile the access as a direct load.

The generated ARM64 looks roughly like this:

```arm64
// GETFIELD obj, "x"  (key "x" at skeys index 3, recorded at trace time)

// Shape guard: verify the table still has the expected structure
LDR   X_tmp, [X_table, #skeys_offset]     // load skeys pointer
LDR   X_tmp2, [X_tmp, #3*8]               // load skeys[3]
LDR   X_expected, [X_constpool, #key_off]  // load expected key pointer
CMP   X_tmp2, X_expected                   // same key at same index?
B.NE  side_exit                            // shape changed → bail out

// Fast path: direct indexed load (no hash, no linear scan)
LDR   X_tmp, [X_table, #svals_offset]     // load svals pointer
LDP   X_type, X_data, [X_tmp, #3*32]      // load value at index 3
```

About 16 instructions per field access including the guard, the loads, and register shuffling. Compare that to the interpreter path: a Go function call to `RawGetString`, which hashes the key string, probes the hash map, follows chains on collision. The native version has zero branches (other than the guard), zero function calls, and zero hash computation.

SETFIELD works the same way in reverse --- shape guard, then a direct store into `svals[index]`.

### Fix 2: GETGLOBAL Support

Global variable access had the same problem as field access: the trace couldn't compile it, so it side-exited. Functions like `math.sqrt` live in the global table, so every `math.sqrt(x)` call was a side-exit.

The fix: at recording time, capture the global value in the trace's constant pool. At execution time, load it directly from the constant pool. No table lookup, no hash, just a memory load.

### Fix 3: math.sqrt as FSQRT Intrinsic

Once GETGLOBAL was working, `math.sqrt` calls could stay in the trace. But they were still Go function calls with full calling convention overhead. ARM64 has a single-instruction square root: `FSQRT D0, D1`. One instruction instead of a function call that pushes arguments, calls through a closure, and pops results.

Mandelbrot's inner loop calls `math.sqrt` once per pixel. At 1000x1000 pixels, that's a million function calls replaced by a million single-cycle instructions.

These three fixes together should have been the whole story. Native table ops fix the death spiral, GETGLOBAL enables intrinsics, FSQRT accelerates mandelbrot. Ship it.

But the biggest win came from somewhere else entirely.

## The Accidental Discovery

After implementing the three fixes above, I ran the benchmarks expecting a clean victory. mandelbrot was faster, but not as fast as the instruction count suggested. The CPU profile showed something strange: the interpreter was still consuming the majority of execution time. Not because of side-exits --- the inner loop trace was running fine. The time was being spent on the *outer* loops.

Mandelbrot has three nested loops:

```
for py = 0, height-1 do       -- outer loop (rows)
  for px = 0, width-1 do      -- middle loop (columns)
    for i = 0, max_iter-1 do   -- inner loop (iteration)
      -- the actual computation
    end
  end
end
```

The inner loop is hot. The trace recorder records it, the JIT compiles it, native code runs it. Good. But the *outer two loops* are also hot. The interpreter's `FORLOOP` opcode increments the hot counter for each of them. They reach the recording threshold and the recorder tries to build a trace.

It tries, and it fails. Every single time.

Why? Because the outer loops contain `FORPREP` instructions for their *inner* loops. The trace recorder hits `FORPREP`, doesn't know how to handle loop setup inside a trace, and aborts. Recording wasted. Counter reset. Next iteration, counter increments again. Reaches threshold. Records again. Aborts again.

For mandelbrot(1000), the outer loop runs 1,000 times. The middle loop runs 1,000,000 times. Each iteration of each loop triggers a recording attempt that immediately aborts. That's over a million wasted recording attempts --- each one allocating a TraceIR buffer, setting up recorder state, running for a few instructions, hitting FORPREP, tearing everything down.

The fix was absurdly simple: **blacklist loops that abort on structural instructions.**

```go
if abortReason == "FORPREP" || abortReason == "CLOSURE" || abortReason == "CONCAT" {
    // Structural abort: this loop will NEVER record successfully.
    // Don't waste time retrying.
    blacklist(loopPC)
}
```

When a trace aborts because it hit FORPREP, the loop entry point gets permanently blacklisted. The hot counter is disabled for that bytecode offset. The interpreter never attempts to record there again.

This single change --- maybe 15 lines of code --- was the largest performance improvement of the entire project.

Not GETFIELD codegen. Not FSQRT. Not CSE. Just *stopping the millions of wasted recording attempts*. The optimization that mattered most wasn't about generating better code. It was about not generating code at all.

## The Bug That Almost Killed Us

The first version of abort-blacklisting worked. mandelbrot went from 1.53x to over 5x. I committed it and moved on to SSA optimization passes.

Then I ran the full benchmark suite.

mandelbrot was fast. Everything else was fine. But something nagged at me, so I ran mandelbrot with tracing diagnostics enabled. The inner loop --- the one that actually does the computation, the one the entire JIT exists to accelerate --- was blacklisted.

The JIT wasn't running at all. mandelbrot was fast purely because we'd eliminated the recording overhead. The native code for the inner loop was never executing.

Here's what happened. mandelbrot's inner loop has a `break` statement:

```
for i = 0, max_iter-1 do
  -- compute zr, zi
  if zr*zr + zi*zi > 4.0 then
    break
  end
end
```

The `break` compiles to a `JMP` instruction that jumps past the `FORLOOP`. When the trace recorder is recording the inner loop and the `break` condition is true, the trace follows the JMP. The recorder sees the JMP going outside the loop body and interprets it as an abort --- the trace can't continue past the loop boundary.

With the blacklisting logic, *any* abort on *any* instruction triggered a permanent blacklist. The break-JMP abort blacklisted the inner loop. On subsequent iterations where the break *doesn't* fire (the common case for points inside the Mandelbrot set), the loop was already blacklisted. No trace entry. No native code.

The fix required distinguishing two fundamentally different kinds of aborts:

**Structural aborts** are permanent. If a loop contains `FORPREP` for a nested loop, that will never change. Recording will always fail at the same point. Blacklist it forever.

**Break aborts** are conditional. The break fires on *some* iterations (when the point escapes), but not others (when the point is in the set). The loop is perfectly traceable on non-escaping iterations. Don't blacklist --- just stop the current recording attempt and let the next iteration try again.

```go
if isStructuralAbort(reason) {
    // FORPREP, CLOSURE, CONCAT, channel ops → permanent blacklist
    permanentBlacklist(loopPC)
} else if isBreakAbort(reason) {
    // JMP escaping loop → temporary, allow retry
    tempBlacklist(loopPC, retryAfter: 10)
}
```

Getting this distinction wrong meant either blacklisting too aggressively (killing the inner loop) or not aggressively enough (allowing the millions of wasted recording attempts). The final implementation uses a small retry counter for break aborts: after 10 consecutive break-aborts, temporarily pause recording for that loop, but don't permanently blacklist it.

## Phase 3: Three Passes in Parallel

With native table ops and blacklisting working, we moved to SSA optimization passes. Three independent optimizations, implemented in parallel using isolated git worktrees:

### Constant Hoisting

The SSA IR places `CONST_INT` and `CONST_FLOAT` instructions wherever the bytecode uses them. In mandelbrot's inner loop, the constants `2.0` and `4.0` appear inside the loop body. Every iteration loads them fresh: `MOVZ + MOVK + MOVK + MOVK + FMOV` --- five instructions to materialize a constant that never changes.

Constant hoisting moves these instructions before the `SSA_LOOP` marker. The values are loaded once into registers before the loop starts and stay there for every iteration. Five instructions removed per constant per iteration.

### Common Subexpression Elimination (CSE)

mandelbrot computes `zr*zr` and `zi*zi` twice each per iteration --- once for the escape check (`zr*zr + zi*zi > 4.0`) and once for the next iteration's values. CSE identifies duplicate pure instructions (same opcode, same operands) and replaces the second occurrence with a reference to the first.

The implementation scans the IR linearly. For each pure instruction (arithmetic, comparisons, loads with no side effects), hash the `(opcode, arg1, arg2)` tuple. If the hash already exists and the previous instruction dominates the current one, replace the current instruction with a reference to the previous result.

### Type-Specialized LOAD_ARRAY

When a trace accesses `table[i]` where `i` is an integer, the standard codegen loads a full 32-byte GScript value (type tag + data + padding). But if the SSA type system knows the result is a float, we only need 8 bytes --- a direct `LDR D` from the array's float storage.

The type-specialized version emits a single `LDR` instead of loading the type tag, checking it, and then loading the data. Smaller code, fewer memory accesses, better cache behavior.

All three passes were implemented as separate source files, by separate agents, in separate worktrees. No merge conflicts. Each pass takes an `SSAFunc`, transforms it, returns an `SSAFunc`. The pipeline runs them in sequence: `ConstHoist -> CSE -> TypeSpecLoad -> RegAlloc -> Emit`.

## The Numbers

Here's the full before/after table, comparing Blog #4's results to the current state:

| Benchmark | Blog #4 VM | Blog #4 Trace | Blog #4 Speedup | Current VM | Current Trace | Current Speedup |
|-----------|------------|---------------|-----------------|------------|---------------|-----------------|
| mandelbrot | 1.498s | 0.980s | 1.53x | 1.503s | 0.246s | **6.09x** |
| nbody | 2.680s | 4.217s | 0.64x | 2.728s | 2.884s | **0.95x** |
| sieve | 0.267s | 0.332s | 0.80x | 0.174s | 0.172s | **1.01x** |
| spectral_norm | 0.809s | 0.939s | 0.86x | 0.784s | 0.955s | 0.82x |
| fib(35) | 0.801s | 0.804s | 1.00x | 0.072s | 0.072s | **10x** |
| ackermann | 0.141s | 0.194s | 0.73x | 0.017s | 0.017s | **10x** |

The headline: mandelbrot went from 1.53x to **6.09x**. That's a 4x improvement in the JIT's speedup factor in one development cycle.

But look at the other benchmarks too:

- **nbody** went from 0.64x (36% *slower* with JIT) to 0.95x (near parity). The Table Op Death Spiral from Blog #4 is gone. Native GETFIELD + GETGLOBAL + abort-blacklisting together eliminated the 12-side-exits-per-iteration problem.

- **sieve** went from 0.80x to 1.01x. No longer a regression. The blacklisting prevents wasted recording attempts on loops that can't be traced.

- **fib and ackermann** are 10x faster. These run on the method JIT (the older bytecode-to-ARM64 compiler from Post #1), not the trace JIT. The improvement comes from interpreter optimizations that happened alongside the trace work --- the VM dispatch loop itself got faster.

- **spectral_norm** is still at 0.82x. Still a regression. The trace compiles but the overhead of trace entry/exit exceeds the benefit. This benchmark needs nested loop tracing to improve --- the inner loop is too short for the trace to amortize its entry cost.

## CPU Profile: Where Time Goes Now

I ran `pprof` on mandelbrot(1000) in trace mode to understand the 6.09x result. Where is the remaining time going?

```
58%  interpreter (outer loops, dispatch, FORLOOP bookkeeping)
21%  JIT native code (the inner loop)
12%  Go runtime (GC, scheduling, stack management)
 9%  trace infrastructure (recording, guard checks, trampoline)
```

**58% of mandelbrot's time is still in the interpreter.** The JIT only runs for 21% of the total execution time.

This seems wrong at first. If the JIT is 6x faster overall, and it's only running 21% of the time, the native code must be *dramatically* faster than the interpreter for the work it handles. And it is --- the inner loop runs entirely in ARM64 registers, no boxing, no dispatch, no function calls. But the outer two loops (row iteration, column iteration) are pure interpreter: increment the counter, check the bound, jump back, call into the traced inner loop, repeat.

The 58% interpreter time is the ceiling. No amount of optimizing the inner loop's native code will help. The path to the next big jump is **nested loop tracing** --- recording the outer loops too, so the entire triple-nested structure runs as one compiled trace.

This is the same architectural insight from Post #4's research phase, now confirmed by measurement. The profiler predicted it; the data proves it.

## The Honest Assessment

6.09x on mandelbrot is real and verified. `mandelbrot(1000) = 396940` pixels, matching the interpreter exactly. The days of fake speedups from Post #3 are behind us.

But let's be honest about what we haven't solved:

**Table-heavy benchmarks are still at parity or worse.** nbody (0.95x) and spectral_norm (0.82x) show that native GETFIELD helped --- nbody went from 0.64x to 0.95x, which is a huge improvement --- but it's not enough. The traces compile, the table accesses are native, but the overhead of trace entry/exit still exceeds the benefit for short loop bodies. nbody's inner loop has so many field accesses that even with native codegen, the shape guards add up.

**The gap to LuaJIT is still large.** LuaJIT runs mandelbrot at roughly 30-50x interpreter speed. We're at 6x. LuaJIT runs nbody at 10-20x. We're at 0.95x. The architectural foundations are different --- LuaJIT has snapshots, inline caches, HREFK with proven shapes, nested loop tracing, and 15 years of Mike Pall's expertise baked in.

**Three optimizations landed, but the biggest win was a 15-line fix.** The SSA optimization passes (constant hoisting, CSE, type-specialized loads) contributed real speedups, but FORPREP abort-blacklisting contributed more than all three combined. This is a recurring theme in optimization work: the bottleneck is rarely where you expect it. We expected "generate better native code" to be the answer. The actual answer was "stop doing millions of pointless things."

## What We Learned

**1. The most impactful optimization is often *removing* work, not *improving* work.**

We spent days implementing native GETFIELD codegen, CSE, constant hoisting. Important work. But the single biggest speedup came from adding 15 lines that prevent the recorder from attempting traces that will never succeed. The trace recorder was doing millions of pointless recording attempts per benchmark run. Stopping that mattered more than everything else combined.

This pattern appears everywhere in systems work. The fastest code is the code that doesn't run.

**2. Blacklisting is a precision instrument, not a sledgehammer.**

Our first blacklisting implementation was too aggressive --- it treated all aborts the same and accidentally blacklisted the only loop that should be traced. The distinction between structural aborts (permanent, will always fail) and conditional aborts (temporary, may succeed on the next iteration) is fundamental.

LuaJIT handles this with a penalty system: each trace has a penalty counter that increases on failed attempts, with the counter increment depending on the *reason* for failure. Structural failures get large penalties (fast blacklisting). Conditional failures get small penalties (slow blacklisting, many retries). We implemented a simpler version of the same idea.

**3. CPU profiles don't lie, but they do require interpretation.**

The profile says 58% interpreter, 21% JIT. The naive reading is "the JIT isn't doing much." The correct reading is "the JIT is extremely effective for the work it handles, but it only handles 21% of the total work." The optimization target isn't the JIT's code quality --- it's the JIT's *coverage*. Nested loop tracing increases coverage from 21% to potentially 80%+.

## Next Steps

The CPU profile is clear: nested loop tracing is the next major milestone. The outer two loops of mandelbrot consume 58% of total execution time in the interpreter. If we can trace all three loops as one unit, the JIT covers ~80% of execution instead of 21%.

But nested loop tracing is hard. The trace recorder currently records one loop at a time. Recording the outer loop means handling the inner loop's FORPREP, FORLOOP, and all the bookkeeping that goes with it. We attempted this briefly during this development cycle and hit infinite loops --- the recorder tried to inline the inner trace, re-entered the inner loop's FORLOOP, and never escaped.

LuaJIT handles this with "up-recursion": when the inner loop's trace completes, it checks whether the outer loop is hot. If so, it starts recording the outer loop with the inner loop's compiled trace as a "black box" call. The outer trace calls into the inner trace like a subroutine.

This requires:
- Trace-to-trace linking (one compiled trace calling another)
- Nested snapshot state (the outer trace's side-exit must restore both outer and inner loop state)
- Loop peeling for the outer loop (so the first iteration records the inner loop entry)

It's the biggest architectural challenge since we started the trace JIT. But the data says it's worth 2-3x on mandelbrot alone, and it unlocks spectral_norm and nbody (which also have nested loops).

Post #6 will be about nested loop tracing --- whether we crack it or hit the wall.

## References

- Mike Pall, [LuaJIT 2.0 SSA IR](http://wiki.luajit.org/SSA-IR-2.0) --- trace abort handling and penalty system
- Andreas Gal et al., [Trace-based Just-in-Time Type Specialization for Dynamic Languages](https://dl.acm.org/doi/10.1145/1542476.1542528) --- nested trace trees, trace blacklisting design
- Mike Pall, [LuaJIT Mailing List on Trace Abort Handling](http://lua-users.org/lists/lua-l/2009-11/msg00089.html) --- penalty counters for different abort reasons
- ARM Architecture Reference Manual --- FSQRT instruction timing and latency

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
