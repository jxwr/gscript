---
layout: default
title: "Thirty-One Float Multiplies, Untyped"
permalink: /31-thirty-one-float-multiplies
---

# Thirty-One Float Multiplies, Untyped

nbody is 7.5x behind LuaJIT. Last round shaved 49% off by wiring up the GetGlobal native cache and LICM-hoisting it out of the inner loop. That was 5 million Go exits eliminated per benchmark run. Good progress.

But 7.5x is still a lot. Where is the time going now?

## The diagnostic that told us what we wanted to hear

I wrote a diagnostic test that compiles nbody's `advance()` function through the full Tier 2 pipeline and dumps the IR after each pass. Here's what LICM does to the inner j-loop:

```
PRE-HEADER B9 for loop B3: 4 hoisted instructions
  v20=GetField(v9.field[1])   — bi.x
  v23=GetField(v9.field[2])   — bi.y
  v26=GetField(v9.field[3])   — bi.z
  v59=GetField(v9.field[7])   — bi.mass
```

Four field loads hoisted out of the inner loop. `bi` is the outer-loop body — it doesn't change per j-iteration. LICM checks for writes to the same (object, field) pair, finds none (only `bi.vx/vy/vz` are written, not `bi.x/y/z/mass`), and hoists them to the preheader. The Intrinsic pass converted `math.sqrt` to `OpSqrt`, so there's no `OpCall` in the loop — `hasLoopCall` is false, GetField hoisting is unblocked.

This all looks correct. Infrastructure working as designed. But then the type analysis:

```
Inner loop B3: typed=4 (int/float/bool), untyped=44 (any/unknown)
Inner loop B3: generic_arith=29, specialized_arith=2
```

Twenty-nine arithmetic operations — every multiply, add, subtract, divide — running generic dispatch. Each one checks if its operands are int, float, or something else. Each one boxes the result back into a NaN-tagged uint64. On every single iteration of a loop that runs 5 million times.

## The pipeline gap

The feedback-to-type pipeline is supposed to prevent this. It works like this:

1. Tier 1 runs the function, accesses fields via inline cache
2. On IC miss (first access), Go handler records the result type in `proto.Feedback[pc]`
3. When Tier 2 compiles, graph builder reads `Feedback[pc].Result`
4. If monomorphic (always float), inserts `OpGuardType(TypeFloat)` after the GetField
5. TypeSpecialize sees the guard, converts `Mul(any, any)` → `MulFloat(float, float)`
6. FPR allocation, NaN-unboxing eliminated, float arithmetic in registers

The code for step 3-4 exists in `graph_builder.go:669-676`. Round 17 fixed the feedback recording in Go exit handlers. The end-to-end test passes. So why are all 29 arithmetic ops generic?

Because the diagnostic test compiled through `RunTier2Pipeline` — the raw pipeline, no TieringManager, no Tier 1 warmup. There was no feedback to read. The graph builder checked `proto.Feedback` and found an empty vector, so it emitted everything as `:any`.

## The question that actually matters

Is the production codegen typed or untyped?

In production, `advance()` runs through TieringManager: Tier 1 first (collecting feedback), then Tier 2 (using it). The diagnostic test bypassed this. So the "29 generic arith" finding might be an artifact of the test setup, not the actual production bottleneck.

Or it might be real. If advance reaches Tier 2 via OSR before Tier 1 has collected enough feedback, or if the feedback vector isn't propagated correctly to the Tier 2 graph builder, the production code would also be untyped.

The answer determines the entire optimization strategy:

- **If untyped in production**: fixing the feedback pipeline saves ~290 instructions per inner-loop iteration. That's a 30-50% wall-time reduction. Dramatic.
- **If typed in production**: the bottleneck is field access overhead — shape checks, NaN-boxing, memory indirection. A harder problem with smaller returns (~10-15%).

We need a production-accurate diagnostic before committing to either path.

## What the production diagnostic found

We wrote a new test — `TestDiag_NbodyProduction` — that runs `advance()` through the full TieringManager. Tier 1 runs first, the interpreter collects type feedback, and when CallCount crosses the threshold, TieringManager triggers Tier 2 compilation with the collected feedback.

The results were definitive:

```
Feedback summary: Float=24 Int=0 Table=0 Any=0 Unobserved=75
GuardType nodes: 24
Generic arithmetic (OpAdd/Sub/Mul/Div): 7
Typed arithmetic (OpAddFloat/SubFloat/MulFloat/etc): 33
GetField total: 20 (typed: 0, any: 20)
```

**Scenario A confirmed.** The feedback pipeline works. 33 float-specialized arithmetic ops. 24 GuardType guards inserted from monomorphic feedback. The inner loop's hot path looks like this:

```
v21  = GuardType   v20 is float : float     -- guard bi.x
v24  = SubFloat    v21, v23 : float          -- dx = bi.x - bj.x
v35  = MulFloat    v24, v24 : float          -- dx * dx
v39  = AddFloat    v37, v38 : float          -- dsq accumulation
v42  = Sqrt        v39 : float               -- math.sqrt(dsq)
v51  = MulFloat    v50, v45 : float          -- dx * bj.mass * mag
v52  = SubFloat    v47, v51 : float          -- bi.vx = bi.vx - ...
```

Float arithmetic throughout. `math.sqrt` lowered to `OpSqrt`. The pipeline is doing exactly what it's designed to do.

The 7 remaining generic ops include a `Div` for `dt / (dsq * dist)` where the parameter `dt` enters as `:any` (no parameter type guard). And 20 GetField ops all returning `:any` with GuardType narrowing after each one.

## Where the gap actually lives

The "29 generic arith" from the first diagnostic was an artifact. But the 7.5x gap to LuaJIT is still real. Now we know it lives in two places:

1. **GetField overhead**: 20 field accesses per iteration, each ~16 ARM64 instructions (shape guard, field index lookup, NaN-unbox). That's ~320 instructions just reading object properties.

2. **GuardType overhead**: 24 type guards per iteration, each ~4-6 instructions. Another ~100-144 instructions verifying types that are always float.

Total overhead: ~420-460 instructions on top of the ~40 instructions of actual computation. LuaJIT's inner loop runs in ~30 instructions total.

The fix is cross-block shape propagation: when LICM hoists a GetField to the preheader, the shape check in the preheader validates the table's shape once. All subsequent GetField accesses on the same table in the loop body should skip the shape check. For nbody's inner loop with 2 tables (`bi`, `bj`), this eliminates ~11 instructions per table per iteration.

That's a task for the next round.

## The self-call optimizations

While the nbody diagnostic was the main goal, we also implemented two self-call optimizations for recursive functions:

**Task 0a: NaN-boxed closure cache (X21).** We cache the current function's NaN-boxed closure value in callee-saved register X21. At CALL sites, a 2-instruction compare detects self-calls and skips the entire 14-instruction type-check + proto-comparison sequence. The self-call fast path goes straight to bounds check and call setup.

The implementation was clean: 3 instructions in each prologue (load closure pointer, NaN-box it, store in X21), and a `CMPreg + BCond` gate at the top of each CALL handler. Non-self calls pay 2 extra instructions (the compare that doesn't match). Self-calls save ~10 instructions.

**Task 0b: Pin R(0) to X22.** We pinned VM register R(0) — the first function parameter — to ARM64 callee-saved register X22. Every read of slot 0 uses `loadSlot()` (register-to-register move instead of memory load). Every write to slot 0 uses `storeSlot()` (updates both memory and X22).

This touched 5 files and ~60 call sites — systematic but tedious. The `loadSlot`/`storeSlot` helpers make the pattern mechanical: `if slot == 0 { use X22 } else { memory }`. At compile time (Go code generating ARM64), the `slot == 0` check adds zero ARM64 instructions for non-zero slots.

## What the benchmarks actually said

Then we ran the benchmarks. The prediction was "performance-neutral on M4 Max" — the L1D cache is 1 cycle, so register-vs-memory should be invisible.

The prediction was wrong.

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| nbody | 0.284s | 0.261s | **-8.1%** |
| matmul | 0.130s | 0.120s | -7.7% |
| coroutine_bench | 21.866s | 16.717s | -23.5% |
| binary_trees | 2.705s | 2.208s | -18.4% |
| fibonacci_iterative | 0.341s | 0.279s | -18.2% |
| sort | 0.050s | 0.041s | -18.0% |
| method_dispatch | 0.119s | 0.100s | -16.0% |
| closure_bench | 0.033s | 0.028s | -15.2% |
| fannkuch | 0.053s | 0.046s | -13.2% |

18 of 22 benchmarks improved. The R(0) pin to X22 — a change that eliminates one memory load per slot-0 access — gave the broadest improvement in the project's history. Not the deepest, but the widest.

Why did "1-cycle L1D" not mean "free"? Because eliminating a load isn't just about latency. It's also about address generation (ADD for offset), pipeline occupancy, and — on tight loops that saturate the load-store unit — freeing a load port for something else. The M4 Max has excellent L1D latency, but it can still only issue so many loads per cycle. When every bytecode handler does 2-4 loads from the VM register array and one of those is always slot 0, pinning that slot to a physical register removes thousands of loads per function call.

The self-call cache in X21 contributed too, but less broadly — it only helps functions that call themselves. The R(0) pin helps everything.

## Where we stand

nbody is now 7.7x behind LuaJIT (was 7.5x baseline, 8.4x after this round's improvement — wait, 0.261s / 0.034s = 7.7x). The gap closed by 8% in absolute terms, but LuaJIT's 34ms target remains distant.

The diagnostic told us what matters next: 20 GetField accesses per inner-loop iteration, each running the full shape-check pipeline. Cross-block shape propagation — inheriting the preheader's shape verification into the loop body — is the clear next step. That's a Tier 2 emitter change, not a Tier 1 one.

mandelbrot sits at 1.1x from LuaJIT. fannkuch at 2.3x. sort at 3.7x. The suite is getting competitive on the benchmarks where Tier 2 can work its magic. The holdouts are the recursive functions (ackermann at 99x, mutual_recursion at 59x) where Tier 2 is structurally net-negative and Tier 1 self-call improvements haven't closed the gap.

The round's most important output was a single line: `Typed arithmetic: 33`. The previous diagnostic had us convinced the typing pipeline was broken. It wasn't — the diagnostic was broken. The production path works correctly. This matters because it changes the entire optimization strategy. "Fix the feedback pipeline" is a quick win. "Reduce field access overhead" is an architectural problem. Knowing which one is real before spending engineering time on either is the difference between a productive round and a wasted one.
