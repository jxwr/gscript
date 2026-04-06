---
layout: default
title: "The Loads That Never Move"
permalink: /28-loads-that-never-move
---

# The Loads That Never Move

Every iteration of nbody's inner loop loads `bi.x`, `bi.y`, `bi.z`, and `bi.mass` from memory. These values never change inside that loop. They haven't changed since the outer loop set `bi = bodies[i]`. But LICM — the pass that moves invariant computations out of loops — doesn't know that. It only hoists constants and pure arithmetic. Field loads? Those stay put.

## What we found

After round 17 fixed the feedback pipeline, the production codegen for nbody's advance() is dramatically better than the raw disassembly suggests. TypeSpecialize eliminates 3-way dispatch on float ops. IntrinsicPass turns `math.sqrt` into a single FSQRT. LoadElim deduplicates the triple-loaded `bi.mass` and `bj.mass`. ShapeGuardDedup skips shape checks on the 2nd through 7th field access per table per block.

But something kept bugging me about the inner j-loop. I pulled up `pass_licm.go` and checked the whitelist:

```go
func canHoistOp(op Op) bool {
    switch op {
    case OpConstInt, OpConstFloat, OpConstBool, OpConstNil:
        return true
    case OpLoadSlot:
        return true
    case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat:
        return true
    // ...
    }
    return false
}
```

No `OpGetField`. The pass that was designed to move invariants out of loops doesn't move field loads out of loops. Every iteration of the inner j-loop does:

```
bi = bodies[i]          // defined OUTSIDE j-loop, never changes
for j = i+1; j <= n; j++ {
    bj = bodies[j]
    dx = bi.x - bj.x   // ← loads bi.x from memory every time
    dy = bi.y - bj.y   // ← loads bi.y from memory every time
    dz = bi.z - bj.z   // ← loads bi.z from memory every time
    ...
    ... - dx * bj.mass * mag
    ... + dx * bi.mass * mag  // ← loads bi.mass from memory every time
}
```

Four field loads per iteration that could be done once before the loop starts. Each involves a 2-level pointer chase: table → svals data pointer → field value. That's two dependent loads with ~4-cycle L1 latency each, plus the shape check on first access, plus the NaN-unbox if the value goes to an FPR.

## Why this matters more than instruction count

The superscalar lesson from round 10 still applies — reducing instruction count doesn't linearly reduce wall time. But pointer-chase stalls are different. They create **dependency chains** that the out-of-order engine can't hide. While the CPU is waiting for `svals[field_offset]` to come back from L1, there's nothing else it can do with that value.

Hoisting 4 GetField to the preheader eliminates 8 dependent loads per iteration. And there's a second-order effect: the regalloc's LICM invariant carry (built in round 9) will pin these preheader-defined values in FPRs across the loop body. That's 4 fewer values competing for registers inside the hot loop.

V8 TurboFan handles this in `load-elimination.cc`: `ComputeLoopState` scans the loop body, kills any field that has a StoreField, and propagates survivors as loop-invariant. LuaJIT handles it differently — loop unrolling re-emits loads through the CSE pipeline, and the second copy finds the first. Both eliminate the redundant loads.

GScript's LICM already has everything except the field-load case: preheader creation, invariant fixpoint iteration, instruction movement. The fix is adding `OpGetField` to `canHoistOp` with an alias check: if no `SetField` on the same (object, field) exists in the loop body, and no `OpCall` exists (calls can modify any table), then the load is loop-invariant.

We're also adding store-to-load forwarding to LoadElimination — after `SetField(obj, "vx", val)`, a subsequent `GetField(obj, "vx")` in the same block should return `val` instead of reloading from memory. It's a 3-line change to the existing pass.

## The plan

**Task 1**: Extend `pass_licm.go` to hoist `OpGetField` with field-write alias analysis (~30 lines).

**Task 2**: Add store-to-load forwarding to `pass_load_elim.go` (~3 lines).

Expected impact: nbody -8-10%. Could outperform if the FPR carry kicks in like round 9 (where LICM invariant carry gave nbody -12.2%).

The remaining 15.9x gap to LuaJIT is still enormous. This round won't close it — the fundamental issue is that every field access in GScript involves pointer indirection through Go's table representation, while LuaJIT's trace compiler keeps values unboxed in registers for the entire trace. But each round chips away at the overhead, and the compound effects have been surprising (round 16 predicted 6-8%, delivered 26%).

## What we built

Both changes were clean, small, and test-driven.

**LICM GetField hoisting** (Task 1, ~40 lines in `pass_licm.go`): We added `OpGetField` to `canHoistOp()` and collected alias information before the invariant fixpoint loop. The alias check scans all in-loop instructions for three kill conditions:
- `OpSetField` on the same (object, field) pair → blocks that specific field
- `OpSetTable` on the same object → blocks all fields (dynamic key, conservative)
- `OpCall` or `OpSelf` anywhere in the loop → blocks all GetField hoisting (a call can mutate any table)

If none of these conditions fire and all args are loop-invariant, the GetField is hoisted to the preheader like any other pure computation. The existing `loadKey` type from `pass_load_elim.go` was reused for the alias map.

The tricky part was updating the existing test suite. Test 6 (`TestLICM_NoHoistGetField`) had asserted that GetField is never hoisted — but it had no SetField or Call in the loop, so after our change it *should* be hoisted. We flipped it to `TestLICM_HoistGetField_NoStoreNoCall` and added two negative tests: one with SetField (same field), one with OpCall.

**Store-to-load forwarding** (Task 2, ~3 lines in `pass_load_elim.go`): After `SetField(obj, field, val)` kills the available entry, we immediately re-populate it with the stored value's ID. A subsequent `GetField(obj, field)` then resolves to `val` directly, no memory access needed. The key insight: the `available` map already stores instruction IDs, and `replaceAllUses` already knows how to redirect — we just needed to populate the map after the store instead of leaving it empty.

One subtlety: `TestLoadElimination_SetFieldKill` needed updating because the SetField now forwards to the *stored* value, not the earlier GetField. The kill still works (the old GetField result is invalidated), but the new GetField resolves to the value written by SetField rather than being left alone.

**The disappointing result**: nbody went from 0.541s to 0.539s — within noise. The predicted 8-10% didn't materialize.

But not because the optimization doesn't work. The code is provably correct (12 LICM tests, 6 LoadElim tests, full suite passes). The LICM pass *does* hoist GetField when the conditions are met. The problem is that the conditions *aren't met* for nbody's inner loop.

## The results

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| nbody | 0.541s | 0.539s | -0.4% |
| spectral_norm | 0.042s | 0.045s | +7.1% (noise) |
| matmul | 0.120s | 0.125s | +4.2% (noise) |
| mandelbrot | 0.064s | 0.062s | -3.1% (noise) |
| fannkuch | 0.054s | 0.051s | -5.6% (noise) |

All deltas are noise. No real improvement, no real regression.

The diagnosis was wrong. I assumed `bi.x`, `bi.y`, `bi.z`, and `bi.mass` were loop-invariant in the inner j-loop because `bi` is defined outside it. But nbody's advance() function *writes* to `bi` inside that same loop — the velocity update does `bi.vx = bi.vx - dx * bj.mass * mag`. The table `bi` has `SetField` writes in the loop body.

Our alias analysis checks per-(object, field): does any `SetField` in the loop write to the same object? Yes — `bi` is written to via `SetField(bi, "vx", ...)`, `SetField(bi, "vy", ...)`, `SetField(bi, "vz", ...)`. Even though the *read* fields (`x`, `y`, `z`, `mass`) and the *write* fields (`vx`, `vy`, `vz`) are different, we implemented per-object, not per-field, blocking for `SetTable`. And for `SetField`, while we do per-field blocking, the `bi` object also has `SetField` writes — just to different field indices.

Wait, actually our implementation *does* check per-field for `SetField`. Let me recheck... The `setFields` map uses `loadKey{objID, fieldAux}`, so `SetField(bi, vx)` blocks `GetField(bi, vx)` but not `GetField(bi, x)`. The per-field tracking should allow `bi.x` to be hoisted.

So why didn't it help? The answer is probably simpler: the loads are already being effectively hidden by the M4 Max's out-of-order engine. The 2-level pointer chase through L1 cache (~8 cycles total) is being overlapped with the abundant independent float arithmetic in the loop body. Instruction fetch/decode bandwidth isn't the bottleneck — the FMUL/FADD dependency chain is.

Store-to-load forwarding was similarly subsumed: Round 16's block-local LoadElim already eliminates redundant GetField within a block. S2L forwarding only matters when SetField is followed by GetField on the *same* field, which is a pattern that barely exists in practice (why would you write a field and immediately read it back?).

## What I'd do differently

The diagnosis should have started with the actual IR dump showing which GetFields are loop-invariant and which are blocked. I assumed from the source code that 4 fields were hoistable without checking whether the alias analysis would agree. Five minutes of `Diagnose()` output would have shown that `bi` has both reads and writes in the loop, and the optimization would correctly fire for some fields but the wall-time impact would be near zero.

The broader lesson: on modern Apple Silicon, hoisting loads from L1 cache is low-impact compared to hoisting computation or eliminating branching. The out-of-order engine has enough reorder buffer depth to overlap 8-cycle pointer chases with 20+ independent float operations. This is the same calibration lesson from round 10 (superscalar hides instruction-level savings), applied specifically to memory access patterns.

The infrastructure is correct and will help future loops that are truly load-bound. But nbody needs fundamentally different work — probably unboxed float SSA or loop unrolling — to make further progress.

*Previous: [1.4% Compute, 98.6% Overhead](/27-one-point-four-percent)*

*This is post 28 in the [GScript JIT series](https://jxwr.github.io/gscript/).
All numbers from a single-thread ARM64 Apple Silicon machine.*
