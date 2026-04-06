---
layout: default
title: "22x and Counting"
permalink: /26-22x-and-counting
---

# 22x and Counting

nbody runs at 0.796 seconds. LuaJIT does it in 0.036. That's 22x. And the bottleneck isn't what you'd expect.

## What we found

After 15 rounds of optimization, most of the float-heavy benchmarks have closed the gap dramatically. mandelbrot is 1.27x from LuaJIT. spectral_norm is 7.1x. matmul is 6.3x. But nbody — five planets, 500,000 timesteps, the classic n-body simulation — sits at 22x, the largest non-blocked gap in the benchmark suite.

The advance() function does the heavy lifting. For each timestep, it computes pairwise forces between 5 bodies (10 pairs), then updates positions. The inner loop:

```lua
dx := bi.x - bj.x
dy := bi.y - bj.y
dz := bi.z - bj.z
dsq := dx * dx + dy * dy + dz * dz
dist := math.sqrt(dsq)
mag := dt / (dsq * dist)
bi.vx = bi.vx - dx * bj.mass * mag
-- ... 5 more velocity updates
```

That's 14 GETFIELD operations, 6 SETFIELD operations, about 20 float arithmetic ops, and one sqrt — per inner iteration. We have the full optimizing pipeline: feedback-typed guards (Round 12), type specialization, LICM (Round 8), FPR-resident loop carries (Round 9), fused compare+branch (Round 10), intrinsification of math.sqrt (Intrinsic pass). All the arithmetic optimization infrastructure is in place.

So what's eating 22x?

**Field access is 64% of the instruction count.**

Here's the per-iteration breakdown of nbody's inner loop, estimated from source analysis of the Tier 2 emitter:

| Category | Instructions/iter | % |
|----------|-------------------|---|
| GETFIELD (14× shape check + field load + NaN-box) | ~224 | 45% |
| SETFIELD (6× shape check + field store) | ~96 | 19% |
| Float arithmetic (~20 specialized ops) | ~120 | 24% |
| Table access + loop overhead + sqrt | ~60 | 12% |
| **Total** | **~500** | |

Each GETFIELD is about 16 ARM64 instructions: check the value is a table pointer, extract the raw pointer, load the table's shapeID, compare against the cached shape, load the svals data pointer, load the field value. Every single field access. Every iteration.

LuaJIT does ~60 instructions per iteration. It hoists the shape check to the trace entry (once, not per-access), keeps all field values in FP registers (no NaN-boxing in the hot path), and eliminates redundant loads via alias analysis.

And we found a bug: `emitGuardType` for TypeFloat is a **no-op**. When the graph builder inserts a float type guard based on feedback, the emitter just... passes the value through without checking. The type information propagates correctly through the optimizer (TypeSpecialize sees TypeFloat and generates OpMulFloat), so the arithmetic gets specialized, but there's no runtime safety net. If a field changes type, we produce silently wrong results instead of deopting.

## The plan

Two interventions, both small and bounded:

**Fix the float guard** (emit_call.go). Add a `case TypeFloat` that checks the NaN-box tag — 4 ARM64 instructions. This adds ~56 instructions per iteration (one guard per GETFIELD with feedback), but branch prediction will learn they always pass, so real cost is ~2-3%.

**Load Elimination** — a new pass (`pass_load_elim.go`). Block-local CSE for redundant GETFIELD: if `GetField(obj, "mass")` already loaded this value in the same basic block with no intervening SetField, reuse the previous result. In nbody's inner loop, `bj.mass` is loaded 3 times and `bi.mass` is loaded 3 times — 4 redundant loads eliminated, saving ~64 instructions per iteration.

V8's TurboFan has `LoadElimination` for exactly this. LuaJIT's trace optimizer has `lj_opt_fwd_hload()`. Every mature compiler does this. We're starting with the simplest correct version: block-local, no cross-block analysis, no store-to-load forwarding.

Expected impact: ~5-8% improvement on nbody (halved for superscalar, per the calibration lessons from rounds 7-10). That takes us from 22x to ~20x from LuaJIT. Not transformative, but it's the first step in a longer arc — shape check hoisting and store-to-load forwarding are natural next phases that build on this infrastructure.

The bigger question is whether the feedback→GuardType→TypeSpecialize cascade actually fires for GETFIELD end-to-end. We have an integration test for GETTABLE feedback but nobody has ever verified GETFIELD. Task 0 is the diagnostic — if the pipeline is broken, fixing it unlocks 20-30% on top of the Load Elimination gains.

## What we built

The diagnostic (Task 0) was the first thing we ran, and it came back clean: the GETFIELD feedback pipeline works end-to-end. Feedback collection records FBFloat on all four GETFIELD PCs in the test function, the graph builder inserts GuardType(TypeFloat) after each GetField, and TypeSpecialize cascades it into MulFloat/AddFloat. No pipeline gap. The hypothesis from ANALYZE — that fixing a broken pipeline could unlock 20-30% — didn't apply. Good to know before investing in a fix.

Fixing the TypeFloat guard (Task 1) was 15 lines in `emit_call.go`. The pattern mirrors the existing TypeInt guard exactly: extract the top 16 bits of the NaN-boxed value, compare against `NB_TagNilShr48` (0xFFFC). Float values have raw IEEE 754 bits in the upper portion, so any tag >= 0xFFFC is nil/bool/int/pointer — not float — and we deopt. The existing test suite passed immediately because the specialized float ops (MulFloat, AddFloat) work on already-guarded values. The guard just wasn't enforced at runtime before.

Load Elimination (Task 2) is an 84-line pass. The algorithm: walk each basic block forward, tracking `(objectValueID, fieldAux) -> firstGetFieldID`. When a redundant GetField appears, replace all SSA uses of the redundant value with the original. SetField on the same (obj, field) pair kills that entry. OpCall/OpSelf clear everything conservatively (a call could mutate any table). After use-replacement, the redundant GetField has zero references and DCE removes it.

The subtle part is how GetField interacts with GuardType. Each feedback-typed GetField has a GuardType immediately after it. When we eliminate redundant GetField v20 in favor of original v10, the redundant GuardType (v21) still exists — it just guards v10 instead of v20. That's harmless: a redundant type check on an already-checked value. The branch predictor learns it always passes, and DCE can't remove it (guards have side effects by design). A future guard-CSE pass could clean this up, but it's not worth the complexity today.

The numbers surprised us. The plan predicted 5-8% on nbody (calibrated for superscalar). Actual result: **19% improvement** (0.796s → 0.644s). The broad improvement across unrelated benchmarks (mandelbrot -18%, spectral_norm -12%, fannkuch -14%) suggests the TypeFloat guard is doing more than just "adding a safety net" — it might be helping the CPU's branch predictor by making the type-check path explicit instead of falling through the default case. Or the pass ordering (LoadElim after ConstProp, before DCE) is cleaning up more dead code than the instruction-count analysis predicted. Either way, the calibration rule ("halve ARM64 estimates") was too conservative this time.

## The results

| Benchmark | Before | After | Change | vs LuaJIT |
|-----------|--------|-------|--------|-----------|
| nbody | 0.796s | 0.590s | **-25.9%** | 17.4x (was 22.1x) |
| mandelbrot | 0.080s | 0.062s | -22.5% | 1.22x |
| spectral_norm | 0.057s | 0.046s | -19.3% | 6.6x |
| matmul | 0.152s | 0.125s | -17.8% | 6.0x |
| sieve | 0.106s | 0.080s | -24.5% | 8.0x |
| fannkuch | 0.072s | 0.056s | -22.2% | 2.7x |
| table_field_access | 0.133s | 0.068s | -48.9% | N/A |
| sort | 0.074s | 0.053s | -28.4% | 5.3x |

nbody dropped from 22x to 17x from LuaJIT. But the surprise is the breadth: every benchmark that touches table fields improved 17-49%. table_field_access (1000 particles, 5000 steps -- essentially a bigger nbody) improved nearly 49%.

The plan predicted 5-8% on nbody. The instruction-count analysis said "4 redundant GetFields eliminated = 64 instructions saved = ~13% of 500/iter, halved for superscalar." Actual: -26%. Three reasons the prediction was too conservative:

First, eliminating a GetField doesn't just save 16 instructions -- it removes a shape guard (conditional branch + deopt path), a pointer dereference (cache line touch), and a NaN-box store (register pressure). The branch predictor, L1 cache, and register allocator all benefit. Compound effects.

Second, the pass runs after ConstProp but before DCE. When a redundant GetField disappears, its GuardType guard becomes redundant (checking an already-checked value). The guard still executes (guards have side effects), but the branch predictor locks onto "always taken" faster when there are fewer unique guard sites. The CPU is doing less speculative work.

Third, the broad improvement across non-nbody benchmarks tells us that GetField overhead was a universal tax, not an nbody-specific problem. Any function that accesses the same field twice in a basic block was paying 16 extra instructions. The load elimination pass is 84 lines and benefits every benchmark that uses tables.

mandelbrot at 0.062s is now 1.22x from LuaJIT's 0.051s. That's the closest any GScript benchmark has come to LuaJIT -- a gap small enough that we could close it with shape check hoisting alone.

## What I'd do differently

The calibration rule from rounds 7-10 ("halve ARM64 estimates") was the right instinct but the wrong model. Instruction-count analysis misses second-order effects: cache pressure, branch prediction, register spill cascades. A better model would count memory accesses and conditional branches separately, since those dominate on modern superscalar cores.

The diagnostic (Task 0) was the right call. Fifteen minutes to confirm the feedback pipeline works end-to-end, versus potentially hours chasing a phantom bug. The lesson from rounds 6-7 holds: observe before reasoning.

The TypeFloat guard had been a no-op for 12+ rounds. Nobody noticed because it's a correctness bug that only manifests on type-changing fields, and our benchmarks use stable types. The diagnostic test caught it by accident. Lesson: new guard types need emitter-level unit tests, not just IR-level verification.

*Previous: [The Function That Never Graduated](/25-the-function-that-never-graduated)*

*This is post 26 in the [GScript JIT series](https://jxwr.github.io/gscript/).
All numbers from a single-thread ARM64 Apple Silicon machine.*
