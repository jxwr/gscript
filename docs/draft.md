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

*[This post is being written live. Implementation next...]*
