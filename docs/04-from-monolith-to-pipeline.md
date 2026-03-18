---
layout: default
title: "The Profiler Told Us We Were Wrong"
permalink: /04-from-monolith-to-pipeline
---

# The Profiler Told Us We Were Wrong

*March 2026 --- Beyond LuaJIT, Post #4*

## Where We Left Off

In [Post #3](03-the-day-i-wasted-chasing-a-fake-88x), we fixed eight bugs, admitted the x88 was fake, and landed at x1.37 on mandelbrot --- the only benchmark the trace JIT actually helps. The roadmap at the end of that post was:

1. Pass pipeline refactor (prerequisite)
2. Constant hoisting (x1.2)
3. Full float register allocation (x1.3)
4. CSE (x1.1)
5. Nested loop tracing (x2.5)
6. Snapshots (x1.15)

We were ready to start coding the pass pipeline. Instead, we ran a full profiling and research phase first. Four parallel research agents: one profiling every benchmark, one auditing the codebase, one studying LuaJIT/V8/SpiderMonkey internals, one surveying the academic literature.

The plan was to spend a day collecting data, then start the refactor with better context.

The data changed the plan entirely.

## The Bombshell: Trace JIT Hurts 5 of 7 Benchmarks

We had been benchmarking mandelbrot and feeling good about x1.37. We hadn't run the full suite with both method JIT and trace JIT since the bug fixes. Here's what the profiler agent found:

| Benchmark | Interpreter | Trace JIT | Speedup |
|-----------|-------------|-----------|---------|
| mandelbrot(1000) | 1.498s | 0.980s | **x1.53** |
| fib(35) | 0.801s | 0.804s | x1.00 |
| sieve(1M x3) | 0.267s | 0.332s | **x0.80** |
| spectral_norm(500) | 0.809s | 0.939s | **x0.86** |
| nbody(500K) | 2.680s | 4.217s | **x0.64** |
| matmul(300) | 1.232s | 1.603s | **x0.77** |
| ackermann(10M) | 0.141s | 0.194s | **x0.73** |
| binary_trees(15) | --- | CRASH (SIGBUS) | --- |

Read that again. **The trace JIT makes 5 out of 7 benchmarks slower.** nbody is 1.6x slower *with* JIT than without. binary_trees crashes with a SIGBUS.

We were optimizing the one benchmark that works while ignoring why the other six don't work at all.

Meanwhile, the method JIT (the older bytecode-to-ARM64 compiler we wrote in Post #1 and declared useless) was quietly doing this:

| Benchmark | Interpreter | Method JIT | Speedup |
|-----------|-------------|------------|---------|
| fib(35) | 0.801s | **0.074s** | **x10.8** |
| ackermann(10M) | 0.141s | **0.018s** | **x7.8** |

The method JIT we abandoned is 10x faster on recursive benchmarks. The trace JIT we've been building for weeks makes most programs slower.

This is the kind of finding that makes you close the laptop and go for a walk.

## Why the Trace JIT Fails: The Table Op Death Spiral

Every failing benchmark has the same root cause. Let me trace the execution of one nbody loop iteration:

```
1. FORLOOP → hot counter triggers → ENTER TRACE
2. GETFIELD obj "x"  → guard fails (table op not compiled) → SIDE EXIT
3. ... trampoline saves state, returns to interpreter ...
4. interpreter executes GETFIELD, GETFIELD, MUL, ADD, GETFIELD, ...
5. FORLOOP → ENTER TRACE
6. GETFIELD obj "x"  → SIDE EXIT
7. repeat forever
```

The trace records the loop body, compiles it to native code, starts executing... and hits a table operation within 1-3 instructions. The trace can't handle it. Side-exit through the trampoline. The interpreter picks up, runs the rest of the loop body, reaches FORLOOP, and re-enters the trace. Which immediately side-exits again.

Every single loop iteration pays the trace-enter and side-exit cost, and gets zero native execution in return.

Here's what's happening in each benchmark:

- **nbody**: 12 GETFIELD ops per iteration (`body.x`, `body.y`, `body.z`, `body.vx`, ...). The trace enters and exits 12 times per iteration. Pure overhead.
- **sieve**: SETTABLE on the array. One table write per iteration, one immediate side-exit.
- **spectral_norm**: CALL + GETTABLE in the inner loop. Two side-exits per iteration.
- **matmul**: GETTABLE for matrix element access. Side-exit on first access.
- **ackermann**: Recursive, no loop to trace. The trace recorder never fires.

The trace JIT is a net negative because it adds overhead to every FORLOOP (hot counter check, trace entry) but never executes enough native code to pay for it.

## The CPU Profile: Where Time Actually Goes

The profiler agent ran `pprof` on mandelbrot (our best case) and nbody (our worst case).

### mandelbrot (trace mode):

```
 9.3%  compiled JIT code execution
10.6%  interpreter (outer loops, non-traced code)
10.3%  trace recording overhead
12.3%  GC/allocation (TraceIR slice growth during recording)
~53%   Go runtime, GC, threading overhead
```

Even in our *best* benchmark, the JIT executes actual compiled code for only 9.3% of the total time. The trace recording overhead (10.3%) nearly cancels it out. The TraceIR slice grows dynamically during recording, triggering allocations that feed the GC (12.3%). More than half the time is the Go runtime doing Go runtime things.

### nbody (trace mode):

```
33.0%  interpreter dispatch loop
13.4%  hash table lookups (RawGetString for field access)
 4.1%  hash table writes
 7.2%  trace entry/exit trampoline (pure waste)
```

nbody spends 7.2% of its time entering and exiting traces that execute zero useful instructions. The 13.4% on hash table lookups is the real bottleneck --- every `body.x` is a `RawGetString` call that hashes a string and probes a map. LuaJIT compiles these to direct memory loads using inline caches and known object shapes.

## The Architecture Audit

While the profiler was collecting data, the code audit agent was reading `CompileSSA` line by line. Here's what it found.

### Two Pipelines, One Codebase

The method JIT and trace JIT coexist as completely separate compilation pipelines:

```
Method JIT:  bytecode → ARM64 (codegen.go, 2467 lines)
Trace JIT:   trace → SSA IR → ARM64 (ssa_builder.go + ssa_codegen.go, ~2400 lines)
```

They share nothing. Not the register allocator, not the ARM64 emitter, not the table access lowering. Every optimization we add to one pipeline doesn't benefit the other. The method JIT has native GETFIELD/GETTABLE (which is why fib and ackermann are fast). The trace JIT doesn't.

### The 1236-Line Function

`CompileSSA` is 1236 lines. It does seven things:

1. Slot frequency analysis --- scan SSA IR, count how often each VM slot appears
2. Float slot identification --- determine which slots hold floats
3. Register allocation --- "top-N most-used" frequency heuristic
4. Pre-loop guard emission --- type checks before the loop
5. Loop body codegen --- with inline expression forwarding
6. Store-back computation --- `writtenSlots` mechanism
7. Epilogue emission

These seven concerns are interleaved, not sequential. The register allocator's output feeds directly into the codegen loop, which simultaneously computes the store-back set, which depends on the float identification results, which came from the slot analysis. Change one thing, break three others.

The `writtenSlots` mechanism alone caused 3 bugs in Post #3. It works by manually enumerating which SSA opcodes write to which VM slots, with special cases for FORLOOP, SIDE_EXIT boundaries, and float type tags. It's inherently fragile because every new SSA opcode requires updating the enumeration.

### What the SSA IR Is Missing

Compared to LuaJIT's IR, ours is missing:

- **Use/def chains** --- no way to ask "who uses this value?" without scanning every instruction
- **PHI nodes** --- loop-carried dependencies are implicit in slot aliasing
- **SNAPSHOT instructions** --- no mechanism to reconstruct interpreter state at side-exit points
- **Basic blocks** --- the IR is a flat list, no control flow graph
- **Register allocation based on live ranges** --- the "top-N frequency" heuristic ignores liveness entirely; a slot used once at the start and once at the end occupies a register for the entire trace

These aren't nice-to-haves. Without use/def chains, you can't do dead code elimination without a full scan. Without PHI nodes, the register allocator can't reason about loop-carried values. Without snapshots, side-exits can't precisely reconstruct the interpreter state (hence `writtenSlots`).

## What LuaJIT Does That We Don't

The research agent studied LuaJIT's trace compiler, V8's Turbofan/Turboshaft, and SpiderMonkey's Warp pipeline. Three findings stood out.

### 1. Snapshots Are Foundational, Not Optional

In our original roadmap, snapshots were step 6 --- the last item. "Nice to have." The research showed this is backwards.

In LuaJIT, every guard instruction has an associated snapshot: a mapping from interpreter state (stack slots, frames, pending results) to SSA values at that point in the trace. When a guard fails, the snapshot tells the runtime exactly how to reconstruct the interpreter's state so it can resume seamlessly.

Our `writtenSlots` mechanism is a poor approximation of snapshots. It records *which* slots were modified but not *what values* they should have at each possible exit point. This forces the store-back to happen at trace exit (writing all modified slots), and it's the source of our worst bugs.

LuaJIT emits `SNAP` instructions during recording, interleaved with the IR. They're cheap (one pointer per snapshot, plus a compact slot-to-ref mapping), and they make everything downstream simpler: the register allocator knows which values must be live at each exit point, the codegen doesn't need `writtenSlots`, and side-exits are precise.

### 2. V8 Is Moving Away From Sea-of-Nodes

V8 recently introduced Turboshaft to replace Turbofan's sea-of-nodes IR with a simpler CFG-based representation. The V8 team found that sea-of-nodes made optimizations harder to reason about and debug --- the flexibility wasn't worth the complexity.

This is relevant because it validates a simpler IR design. We don't need a graph-based IR with fancy scheduling. A flat list of SSA instructions with basic block boundaries (which is essentially what LuaJIT uses) is enough. The research confirmed: for a tracing JIT where traces are linear (no joins, no merge points), a flat IR is actually *better* than a graph IR because there's no control flow to model.

### 3. LuaJIT's FOLD Engine

LuaJIT doesn't do optimization passes in the traditional sense. It has a FOLD engine that pattern-matches and simplifies IR instructions *as they're being emitted*. When the trace recorder emits `ADD(CONST(0), x)`, the FOLD engine immediately simplifies it to `x` before it enters the IR buffer.

This is faster than a separate optimization pass because the IR never contains the redundancy in the first place. But it's also harder to extend --- adding a new optimization means adding new FOLD rules and getting the priority right.

For us, separate passes are the right choice. We're still building the infrastructure. Clean passes are easier to test, debug, and extend. We can always fuse hot passes later once they're correct.

## The New Plan

The original roadmap optimized for mandelbrot. The profiling data says mandelbrot is our *only* success case. The actual bottleneck across 5/7 benchmarks is table operations causing immediate side-exits.

Here's the revised roadmap:

### Phase 0: Trace Blacklisting

If a trace side-exits on the same instruction N times, stop entering it. This is a tiny change --- maybe 20 lines --- but it immediately fixes the regressions. nbody goes from x0.64 back to x1.0. sieve goes from x0.80 back to x1.0. No benchmark gets worse, several get better.

LuaJIT does exactly this: a trace that side-exits too often is blacklisted, and the interpreter takes over that loop permanently. We should have done this from the start.

### Phase 1: Pass Pipeline Refactor

Split `CompileSSA` into discrete passes:

```
BuildSSA(trace)          → SSAFunc
AnalyzeLiveness(f)       → LiveInfo
InsertSnapshots(f, live) → SSAFunc
RegAlloc(f, live)        → RegMap
EmitARM64(f, regmap)     → MachineCode
```

Each pass takes an `SSAFunc`, transforms it, outputs an `SSAFunc`. The emitter becomes mechanical: look up the register for each SSA ref, emit the corresponding ARM64 instruction. No analysis, no `writtenSlots`, no special cases.

This is the prerequisite for everything that follows. Without clean pass boundaries, we can't add optimizations without risking the cascade bugs from Post #3.

### Phase 2: Native Table Operations in Traces

This is the big one. Compile GETTABLE, SETTABLE, GETFIELD, SETFIELD directly in the trace instead of side-exiting.

For GETFIELD (string key on a known table), this means:

```arm64
// GETFIELD obj, "x"  →  load from known offset in skeys/svals
LDR  X0, [X_table, #svals_offset]    // load svals base pointer
LDR  X1, [X0, #field_index * 32]     // load value at known index
```

The trace recorder already knows the table shape at recording time. If the table has a `skeys` array (our small-string-map optimization from Post #1), and the key "x" is at index 3, we can compile the GETFIELD as two loads. A guard at trace entry verifies the table shape hasn't changed.

This single optimization would fix nbody (12 GETFIELDs per iteration), spectral_norm, and matmul. It's what LuaJIT does with its HREFK (hash reference with constant key) instruction.

### Phase 3: Constant Hoisting + Snapshot Side Exits

Move loop-invariant values (the `2.0`, `4.0` in mandelbrot, table base pointers in nbody) to the pre-loop section. Replace `writtenSlots` with proper snapshots.

### Phase 4: CSE + Float Register Allocation

Common subexpression elimination (reuse `zr*zr`, `zi*zi` in mandelbrot). Full D-register allocation based on live ranges instead of frequency.

### Phase 5: Nested Loop Tracing

Trace the outer loop, with the inner loop as a compiled "black box". This is the x2.5 multiplier --- without it, every mandelbrot pixel is a Go function call into the traced inner loop.

## Why This Order Matters

The old roadmap was: pipeline refactor, then optimize mandelbrot harder.

The new roadmap is: stop the bleeding (Phase 0), build the infrastructure (Phase 1), then fix the actual bottleneck (Phase 2).

The key insight is that **native table operations unlock every benchmark**, not just one. nbody has 12 field accesses per iteration. sieve has a table write per iteration. spectral_norm, matmul --- all of them. If we can keep the trace running through table ops instead of side-exiting, every benchmark benefits.

Constant hoisting and CSE are mandelbrot-specific optimizations. They'll help, but only after the trace stays alive long enough for them to matter.

## What We Learned About Research-First Development

We almost skipped the profiling phase. The pass pipeline refactor seemed obviously correct --- Post #3 ended with a clear argument for it. Why spend a day collecting data we already know we need?

Because we didn't know what we didn't know. We'd been looking at mandelbrot because it was the benchmark we could improve. We never asked: why is nbody 1.6x *slower* with JIT? We never profiled the side-exit trampoline. We never measured that the trace recording overhead nearly cancels the JIT benefit even in the best case.

The four-agent parallel research setup worked well:

- The **profiler** found the side-exit death spiral --- the single most important finding
- The **architect** quantified the `CompileSSA` mess and identified the missing SSA IR features
- The **researcher** reordered our priorities (snapshots from position 6 to position 3, table ops from "someday" to position 2)
- All three reports pointed to the same conclusion: table operations are the bottleneck, not float arithmetic

Running them in parallel meant we got a day's worth of research in a few hours. And the results were worth it: the roadmap changed fundamentally. If we'd started coding the pass pipeline on Monday morning, we'd have built a beautiful architecture for optimizing the one benchmark that already works.

## The Honest Assessment

Our trace JIT, as it stands today, is a net negative for most programs. It helps mandelbrot (x1.53) because that benchmark is pure float arithmetic with no table access in the inner loop. Everything else either breaks even (fib), gets slower (sieve, spectral_norm, nbody, matmul, ackermann), or crashes (binary_trees).

The method JIT is better for recursion. The interpreter is better for table-heavy loops. The trace JIT is only better for pure arithmetic loops.

That's not great. But now we know exactly why, and the fix is concrete: compile table operations natively in traces (Phase 2), and stop entering traces that will immediately side-exit (Phase 0).

The gap to LuaJIT is still 10x or more. But at least we're now aiming at the right wall.

## Next Steps

Phase 0 (trace blacklisting) is a weekend project. Phase 1 (pass pipeline) is 3-5 days. Phase 2 (native table ops) is the real work --- probably 2 weeks, touching the trace recorder, SSA builder, and ARM64 emitter. But it's the optimization that matters most, because it turns the trace JIT from a liability into an asset for the benchmarks that actually represent real programs.

Post #5 will cover the pass pipeline refactor and (hopefully) the first native table operation results.

## References

- Mike Pall, [LuaJIT 2.0 SSA IR](http://wiki.luajit.org/SSA-IR-2.0) --- SNAP instruction design
- V8 Team, [Turboshaft: A New Optimizing Compiler for V8](https://v8.dev/blog/turboshaft) --- moving away from sea-of-nodes
- Andreas Gal et al., [Trace-based Just-in-Time Type Specialization for Dynamic Languages](https://dl.acm.org/doi/10.1145/1542476.1542528) --- the original TraceMonkey paper, covers trace blacklisting
- Mike Pall, [LuaJIT Mailing List on Trace Abort Handling](http://lua-users.org/lists/lua-l/2009-11/msg00089.html)

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
