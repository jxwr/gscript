---
layout: default
title: "The Function That Never Graduated"
permalink: /25-the-function-that-never-graduated
---

# The Function That Never Graduated

matmul is called once. That one call runs 27 million iterations of float arithmetic through the slowest possible path, and our tiering system is designed to never notice.

## What we found

Here's the situation after 14 rounds of optimization. We have a three-tier system: interpreter → baseline JIT (Tier 1) → optimizing JIT (Tier 2). Tier 2 has SSA IR, type specialization, register allocation, loop-invariant code motion, feedback-typed guards — the works. The tiering policy promotes functions to Tier 2 after they've been called enough times:

| Profile | Threshold |
|---------|-----------|
| Pure-compute + loop + arith | callCount ≥ 2 |
| Loop + calls + arith | callCount ≥ 2 |
| Loop + table ops | callCount ≥ 3 |

matmul(300) — triple-nested loop, 300×300×300 = 27M inner iterations — is called exactly once. `callCount` never reaches 2. It runs its entire 27M iterations at Tier 1.

At Tier 1, the inner loop does:

```
sum = sum + ai[k] * b[k][j]
```

Each `MUL` and `ADD` goes through generic dispatch: check both operand tags (are they float? int? something else?), unbox from NaN-boxed representation, compute, re-box the result. About 10 ARM64 instructions per operation. At Tier 2 with type feedback, these would be `FMUL` + `FADD` — 2 instructions total.

We had OSR (On-Stack Replacement) — the mechanism for promoting mid-execution when a loop runs hot. Every production engine uses it: V8 triggers on back-edge interrupts, LuaJIT's trace recorder fires after 56 iterations, SpiderMonkey's IonMonkey compiles at loop headers. We had the full implementation. It was commented out.

```go
// OSR disabled for now: mandelbrot's Tier 2 float code is slower than Tier 1.
// Re-enable once Tier 2 float handling is fully optimized.
// if profile.HasLoop && !tm.tier2Failed[proto] {
// 	tm.tier1.SetOSRCounter(proto, osrDefaultIterations)
// }
```

That comment is from round 3. Since then, we've done 11 rounds of Tier 2 improvements: FPR-resident loop values, LICM with invariant carry, fused compare+branch, feedback-typed guards, native typed-array access. The reason for disabling OSR no longer exists.

But nobody re-enabled OSR.

Meanwhile, Round 14 built the other half of the puzzle. Tier 1 now collects type feedback during execution: when GETTABLE loads from a float array, a stub records `FBFloat` in the FeedbackVector. When the function eventually reaches Tier 2, the graph builder reads this feedback and inserts `GuardType(TypeFloat)` after each table load. TypeSpecialize sees the typed result and cascades: `Mul(float, float)` becomes `MulFloat`, which emits a single `FMUL` instruction.

The feedback pipeline was built and tested (TestFeedbackGuards_Integration passes). But matmul never reaches Tier 2 to use it.

## The plan

Re-enable OSR with a targeted safety gate: only for functions with `LoopDepth >= 2` (deeply nested loops). This targets matmul precisely (LoopDepth=3) and avoids triggering on simple single-loop functions.

The expected flow:
1. matmul called → Tier 1 compiles → starts executing
2. Tier 1 runs ~1000 inner iterations, collecting FBFloat feedback on float-array GETTABLEs
3. FORLOOP back-edge counter hits zero → ExitOSR
4. handleOSR: compile Tier 2 with feedback → GuardType → MulFloat/AddFloat cascade
5. Restart function at Tier 2 → 27M iterations with typed float ops

The 1000 warm-up iterations are negligible (0.004% of 27M). The improvement is from eliminating ~18 instructions per inner iteration on MUL+ADD. Halved for ARM64 superscalar effects: target **0.06–0.12s** (current: 0.207s).

The LoopDepth gate excludes simple single-loop functions. Existing OSR tests verify the mechanism works.

## What we built

The implementation was three lines of Go:

```go
if profile.HasLoop && profile.LoopDepth >= 2 && !tm.tier2Failed[proto] {
    tm.tier1.SetOSRCounter(proto, osrDefaultIterations)
}
```

Replacing a commented-out block that was disabled since round 3. The `LoopDepth >= 2` gate was the only new logic — everything else (OSR counter, handleOSR, compileTier2, feedback collection) already existed and was tested.

TDD surfaced the interesting part: the diagnostic test `TestOSR_FeedbackTypedMatmul` compiles a small 4x4 matmul, executes via VM to collect feedback, then builds Tier 2 IR. Of 5 GETTABLE instructions, only 2 get FBFloat feedback (the leaf element accesses `a[i][k]`, `b[k][j]`). The other 3 get FBTable (row accesses `a[i]`, `b[k]`, `c[i]`). This is correct — the matmul has two levels of table indirection, and feedback correctly distinguishes them. TypeSpecialize cascades the float type through: `GetTable → GuardType(Float) → MulFloat → AddFloat`.

The surprise came at integration testing. The plan predicted matmul as the sole beneficiary ("mandelbrot reaches Tier 2 via call count, it's called 1000 times"). That was wrong. mandelbrot is called *once* with parameter 1000. Same for spectral_norm's main function and fannkuch. The "called N times" is a confusion between the benchmark parameter and the call count.

Every benchmark that calls its compute function once and runs nested loops — matmul, mandelbrot, spectral_norm, fannkuch — now hits the OSR gate. The results:

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| matmul | 0.312s | 0.167s | **-46%** |
| mandelbrot | 0.580s | 0.087s | **-85%** |
| spectral_norm | 0.236s | 0.063s | **-73%** |
| fannkuch | 0.120s | 0.080s | **-33%** |
| nbody | 1.041s | 0.997s | ~0% |
| sieve | 0.124s | 0.130s | ~0% |

No regressions on nbody or sieve (which already reached Tier 2 via call count or have LoopDepth=1).

mandelbrot's 85% improvement was the biggest surprise. It went from running entirely at Tier 1 (never compiled at Tier 2) to full Tier 2 with all the float optimizations from rounds 3-14. Fourteen rounds of Tier 2 improvements were invisible to mandelbrot until now, because the tiering gate never let it through.

The lesson: when you build a tiering system and then disable the only mechanism that handles single-call functions, you're not just blocking one benchmark. You're blocking every benchmark whose outer function is called once. The `LoopDepth >= 2` gate is simple but it catches the right cases — production compute kernels are almost always deeply nested.

## The results

| Benchmark | Before | After | Change | vs LuaJIT |
|-----------|--------|-------|--------|-----------|
| mandelbrot | 0.393s | 0.080s | **-80%** | 1.27x |
| spectral_norm | 0.156s | 0.057s | **-64%** | 7.1x |
| matmul | 0.215s | 0.152s | **-29%** | 6.3x |
| fannkuch | 0.086s | 0.072s | **-16%** | 3.3x |
| nbody | 0.638s | 0.796s | ~0% | 22x |
| sieve | 0.084s | 0.106s | ~0% | 8.8x |

mandelbrot went from 5x behind LuaJIT to 1.27x. That's not from any new optimization — it's from removing a gate that blocked access to 11 rounds of existing optimizations. The FPR-resident loop values, LICM invariant carry, fused compare+branch, feedback-typed guards — all of it was sitting there, fully tested, waiting for a function that would never arrive.

matmul's improvement was smaller than predicted (29% vs the plan's 40-70% target). The prediction assumed matmul was the only beneficiary. With four benchmarks now reaching Tier 2 via OSR, the cold-start compilation cost is amortized differently, and the 1000-iteration warm-up at Tier 1 contributes more to wall time than the model assumed.

spectral_norm's 64% drop is from `multiplyAtAv` — a function with two nested loops that calls `multiplyAv` (also nested). Both reach Tier 2 via OSR now, and the float multiplication chain gets type-specialized end to end.

fannkuch benefits modestly because its inner loop is integer-heavy (array permutations), not float arithmetic. OSR gets it to Tier 2, but Tier 2's main advantage there is fused compare+branch on the loop counter, not type specialization.

nbody and sieve are unaffected — nbody's `advance()` is called 500,000 times (well above the Tier 2 call-count threshold), and sieve has `LoopDepth=1`.

## What I'd do differently

The plan's prediction model assumed a single beneficiary. When you re-enable a mechanism rather than optimizing within one, the blast radius is determined by the gate condition, not the target benchmark. I should have enumerated all functions matching `LoopDepth >= 2 && callCount < threshold` before predicting. The mandelbrot surprise would have been a prediction.

The stale comment ("mandelbrot LoopDepth=1, called 1000x") survived because nobody ran the profiler on mandelbrot's actual metadata. Observation beats reasoning — that's a hard-won rule, and it applies to analysis as much as to debugging.

*Previous: [The Missing Fast Paths](/24-the-missing-fast-paths)*

*This is post 25 in the [GScript JIT series](https://jxwr.github.io/gscript/).
All numbers from a single-thread ARM64 Apple Silicon machine.*
