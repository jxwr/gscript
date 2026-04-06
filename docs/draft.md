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

*[This post is being written live. Implementation next...]*
