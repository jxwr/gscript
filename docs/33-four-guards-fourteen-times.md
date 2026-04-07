---
layout: default
title: "Four Guards, Fourteen Times"
permalink: /33-four-guards-fourteen-times/
---

# Four Guards, Fourteen Times

The inner j-loop of nbody's `advance()` compiles to 431 ARM64 instructions. Twenty-nine of them are floating-point math. The rest — 402 instructions, 93.3% of the loop body — are overhead.

I knew the overhead ratio was bad. I didn't know it was fourteen-to-one.

## The 431

Here's the breakdown from the production diagnostic:

| Category | Count | % |
|----------|-------|---|
| Float compute (fsub, fmul, fadd, fdiv, fsqrt) | 29 | 6.7% |
| Register moves | 91 | 21.1% |
| Spill/reload to register file | 77 | 17.9% |
| Branches | 61 | 14.2% |
| Field loads | 37 | 8.6% |
| Deopt metadata saves | 31 | 7.2% |
| fmov (NaN-box GPR↔FPR) | 25 | 5.8% |
| Guard comparisons | 23 | 5.3% |
| Pointer extraction (ubfx) | 21 | 4.9% |
| Guard tag shifts | 15 | 3.5% |
| Everything else | 21 | 4.9% |

The register moves are the emitter's X0-as-scratch routing pattern. The spill/reload is 14 live float values sharing 8 FPR registers. Those are architectural problems — real, important, but not what I can fix today.

What I *can* fix is hiding in the guard count.

## The four guards that shouldn't be there

LICM does its job. It hoists `bi.x`, `bi.y`, `bi.z`, and `bi.mass` from the j-loop body to the pre-header. Those four GetField operations now run once per outer i-iteration instead of once per inner j-iteration. Good.

But look at the IR in the j-loop body:

```
B9 (pre-header):
    v20  = GetField    v9.field[1] : any      // bi.x — hoisted ✓
    v25  = GetField    v9.field[2] : any      // bi.y — hoisted ✓
    v30  = GetField    v9.field[3] : any      // bi.z — hoisted ✓
    v72  = GetField    v9.field[7] : any      // bi.mass — hoisted ✓

B2 (hot loop body, runs 5M times):
    v21  = GuardType   v20 is float : float   // guard bi.x — NOT hoisted
    v26  = GuardType   v25 is float : float   // guard bi.y — NOT hoisted
    v31  = GuardType   v30 is float : float   // guard bi.z — NOT hoisted
    v73  = GuardType   v72 is float : float   // guard bi.mass — NOT hoisted
```

The GetField was hoisted. The guard on its result was not. LICM moved the question ("what's in bi.x?") to the pre-header but left the answer ("is it a float?") in the hot loop body. The same unchanging value, checked five million times.

Each guard emits ~10 ARM64 instructions (tag extraction, comparison, conditional branch to deopt, deopt metadata). Four guards times ten instructions: 40 wasted instructions per j-iteration. On top of the FPR pinning benefits — hoisting the guard means the result carries as a float in an FPR register, eliminating the NaN-box load-and-convert on every iteration.

## Why they weren't hoisted

Line 27 of `pass_licm.go`:

```
//   - Guards (OpGuardType, etc) are NOT hoisted: their deopt metadata is
//     tied to a specific PC.
```

This comment was written during the initial LICM implementation. It sounds reasonable. Deopt metadata, specific PCs, sounds like something you shouldn't mess with.

Except it's wrong. Here's `emitDeopt`:

```go
func (ec *emitContext) emitDeopt(instr *Instr) {
    asm := ec.asm
    asm.LoadImm64(jit.X0, ExitDeopt)
    asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
    asm.B("deopt_epilogue")
}
```

Three instructions. Set exit code to 2 ("deopt"), jump to epilogue. No PC, no frame state, no position-dependent metadata. The JIT just exits and the Go runtime re-runs the function at a lower tier. The guard can live anywhere in the function — the deopt path doesn't care which block it's in.

Twenty-two rounds of optimization, and this three-line function was hiding a free improvement behind a wrong comment.

## The fix

Add `OpGuardType` to `canHoistOp` in `pass_licm.go`. The existing LICM invariant fixpoint handles the rest: a guard's arg must be invariant for the guard to be marked invariant. The four bi.* guards qualify (their GetField operands are already hoisted). The ten bj.* and bi.v* guards don't (their GetField operands are variant — bj changes each iteration, bi.vx is modified by SetField).

While we're in there, propagate the emitter's shape and table verification state across block boundaries using the dominator tree that `loops.go` already computes. Right now `shapeVerified` resets to empty at every block boundary — even when the shape was just checked in the pre-header one block up. After this change, the j-loop body inherits the pre-header's verification state for bi, skipping the full 17-instruction shape check on the first bi access.

Combined: ~60-70 instructions removed from a 431-instruction loop body. Calibrated estimate: ~8-9% wall-time improvement on nbody after M4 superscalar discounting.

## What actually happened

The guard hoisting was a two-line change. Add `OpGuardType` to the `canHoistOp` switch, update the comment from "Guards are NOT hoisted" to "OpGuardType IS hoisted." The existing LICM fixpoint already requires all guard args to be invariant, so there's no new logic needed. The four bi.* guards in nbody's j-loop move to the pre-header exactly as predicted. The test went from "assert guard stays in body" to "assert guard is in pre-header." Satisfying.

The shape propagation hit a wall.

The plan said: propagate `shapeVerified` state from the immediate dominator's outgoing state. The dominator tree is already computed. Clone the map at block entry instead of starting fresh. Elegant.

Except it's wrong at merge points. Consider:

```
B1 (idom of B4) → B2 (does SetTable) → B4
                 → B3 (no writes)     → B4
```

B4's immediate dominator is B1. B1 verified table X's shape. But B2 modified it. If we propagate B1's state to B4, we skip the shape check even though B2 invalidated it. The dominator guarantee ("B1 always runs before B4") doesn't mean "nothing happened between B1 and B4."

This surfaced as an intermittent SIGBUS in `TestQuicksortSmall` — quicksort's partition function has a diamond CFG where one path does `SetTable` and the other doesn't. The shape check was skipped, the field offset was wrong, the memory access went off the end of the table's backing array. Bus error.

The fix: single-predecessor propagation only. If a block has exactly one predecessor, the predecessor's outgoing state is the block's incoming state. No ambiguity, no merge-point unsoundness. This still captures the pre-header → body case (the main win) and sequential blocks within loop bodies. Loop headers always reset (back-edge may have mutated tables).

I also added `OpAppend` and `OpSetList` to LICM's field-write scan — these ops mutate tables but weren't being tracked. And cleared `tableVerified` after SetTable's exit-resume path, since the interpreter could trigger metamethods during the exit.

## The numbers

| Benchmark | Before | After | Change | Predicted |
|-----------|--------|-------|--------|-----------|
| nbody | 0.247s | 0.245s | -0.8% | -8-9% |
| table_field_access | 0.043s | 0.042s | -2.3% | -5-7% |
| spectral_norm | 0.043s | 0.044s | +2.3% | -5-7% |

I A/B tested this three times — built the old binary, built the new binary, alternated them on the same benchmarks in the same thermal state. The numbers are identical. Zero percent. The spectral regression is noise. The nbody improvement is noise. All of it, noise.

The plan predicted eight to nine percent. The halving rule said: take your instruction-count estimate, cut it in half for superscalar. But the halving rule assumes the removed instructions are on the critical path. These guards aren't. They're predicted branches. On M4 Max, a correctly-predicted branch costs essentially nothing — the branch predictor resolves it while the float ALU is still chewing through the FMUL/FADD chain that's the actual bottleneck. The real discount wasn't 2x. It was closer to infinity.

The shape propagation tells the same story. The pre-header verifies bi's shape, and now the body inherits that verification. Saves 17 instructions on the first bi access per j-iteration. One shape check. Out of 431 instructions. The M4's out-of-order engine was already hiding the cost of that shape check behind the 29 float operations it needed to execute anyway.

## What this round actually taught me

There are two kinds of "overhead" in the 431-instruction loop body:

1. **Latency-bound overhead**: instructions that sit on the critical dependency chain. If you remove them, the loop gets shorter. Examples: the NaN-box conversions between guard results and float operations, spill/reload of live FPR values that the next FMUL needs.

2. **Bandwidth-bound overhead**: instructions that execute in parallel with the critical path. They consume fetch/decode/dispatch bandwidth but don't add to the total latency because the out-of-order engine schedules them alongside the bottleneck chain. Examples: predicted branches, tag comparisons, deopt metadata saves.

Rounds 20-22 succeeded because they removed latency-bound overhead: global cache misses that stalled the pipeline (round 20), memory loads for R(0) that blocked dependent instructions (round 21), generic arithmetic dispatch that added real operations to the dependency chain (round 22).

Round 23 removed bandwidth-bound overhead. The instructions are gone, but the loop was never bandwidth-limited — it was latency-limited on the float dependency chain. Removing the guards freed up micro-op slots that the processor had no useful work to fill with.

The lesson: on a wide superscalar core, you need to know which resource is the bottleneck before deciding what to optimize. Instruction count is a proxy for bandwidth. Dependency chain depth is a proxy for latency. For nbody's inner loop at 431 instructions with 29 float ops chained together, the bottleneck is latency, not bandwidth.

## What we shipped anyway

Four commits, 72 lines of changes across 4 files:

1. **Guard hoisting in LICM**: `OpGuardType` can now be hoisted like any other pure operation. The fix was two lines in a switch statement plus a comment correction. Future guard types benefit automatically.

2. **Cross-block shape verification**: blocks inherit their single predecessor's shape and table verification state. The dominator-based approach we originally planned was unsound at merge points (different paths may have different mutations). Single-predecessor propagation is conservative but correct.

3. **LICM alias safety**: `OpAppend` and `OpSetList` now correctly prevent LICM from hoisting field accesses on mutated tables. This prevents a correctness bug that hadn't manifested yet because no benchmarks exercise the pattern.

4. **tableVerified clearing**: the emitter now invalidates table verification after a SetTable exit-resume path, since the interpreter could trigger metamethods that change the table layout.

None of this moves wall time today. All of it prevents bugs or enables future optimizations. The guard hoisting infrastructure means that the next time someone adds a loop where guards are actually on the critical path, they'll be hoisted automatically. The shape propagation means that when we build proper dataflow-based verification merging, the block-level state tracking is already there.

Sometimes a round's value is the infrastructure, not the numbers.
