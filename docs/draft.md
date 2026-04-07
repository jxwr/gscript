---
layout: default
title: "Seven Generic Ops and a Missing Guard"
permalink: /32-seven-generic-ops
---

# Seven Generic Ops and a Missing Guard

Last round we confirmed the feedback pipeline works: 33 out of 40 arithmetic ops in nbody's inner loop are float-specialized. The remaining 7 are generic. That's the kind of number that feels like it should be easy to fix.

It is. The root cause is one parameter.

## The `dt` problem

nbody's `advance(dt)` takes a single argument: the timestep. It's a float. It's always a float. Every caller passes `0.01` or some other floating-point constant. But the JIT doesn't know that.

When the graph builder processes `advance`, it emits `v0 = LoadSlot slot[0]` for the `dt` parameter. Type: `:any`. The function's entire interaction with `dt` inherits this uncertainty:

```
v43  = MulFloat    v39, v42 : float        // dsq * distance — typed!
v45  = Div         v0, v43 : float         // dt / (dsq * dist) — GENERIC
```

`v43` is `:float` (both operands have type guards from field feedback). But `v0` is `:any`. TypeSpecialize sees `Div(any, float)` and gives up — it needs both operands typed.

The clever thing: `Div` returns `:float` unconditionally in GScript (Lua semantics — division always produces a float). So `mag` IS typed in the type map. Downstream, `bj.mass * mag` sees `float * float` and becomes `MulFloat`. The damage doesn't cascade through the j-loop.

But the position update loop is less lucky:

```lua
b.x = b.x + dt * b.vx
b.y = b.y + dt * b.vy
b.z = b.z + dt * b.vz
```

Here `dt * b.vx` = `Mul(any, float)` → stays generic → result is `:any` → `Add(float, any)` → also generic. Six operations running full type-checking dispatch, 2.5 million times.

## The existing guard mechanism

TypeSpecialize already has a Phase 0 that handles this for integers. `insertParamGuards` scans the function for parameters used in arithmetic with `ConstInt` and inserts `GuardType(param, TypeInt)` at the function entry. If you write `for i = 0; i < size; i++` where `size` is a parameter, Phase 0 notices `size` is used with `ConstInt(0)` in a comparison and guards it as int.

But it doesn't check for float. `dt` isn't used with a `ConstFloat` directly — it's used with computed float values (the result of `dsq * distance`, the result of `GuardType(GetField(...), TypeFloat)`). Phase 0 doesn't see these because it runs BEFORE type inference.

## The fix

Run the float guard check AFTER Phase 1's type inference. At that point, the type map knows that `dsq * distance` is `:float`. For each unguarded parameter used in arithmetic with an inferred-float operand, insert `GuardType(param, TypeFloat)` at the entry block.

After the guard:
- `Div(float, float)` → `DivFloat` ✓
- `Mul(float, float)` → `MulFloat` ✓
- `Add(float, float)` → `AddFloat` ✓

Seven ops fixed. One guard inserted.

## The second issue: redundant guards

While reading the production IR, something else jumped out. `bj.mass` is guarded three times:

```
v48  = GetField    v18.field[7] : any      // bj.mass
v49  = GuardType   v48 is float : float    // guard #1
...
v57  = GuardType   v48 is float : float    // guard #2 (REDUNDANT)
...
v65  = GuardType   v48 is float : float    // guard #3 (REDUNDANT)
```

The graph builder inserts a GuardType after every GetField that has monomorphic feedback. LoadElimination CSEs the three `GetField(v18, "mass")` calls down to one (`v48`). But the three GuardType instructions on `v48` survive — LoadElim doesn't track guards.

Each redundant guard is ~3 ARM64 instructions × 5 million iterations. Not huge, but free to eliminate: track `(value_id, guard_type)` in the LoadElim pass, replace duplicates with the first guard's result.

## What we still can't fix this round

The 7.7x gap to LuaJIT isn't going to close from typing fixes. The production diagnostic showed the real cost structure:

- ~40 instructions of actual float computation per j-iteration
- ~320 instructions of field access overhead (shape checks, type validation, NaN-boxing)
- ~100 instructions of guard type checks

LuaJIT does the same computation in ~30 instructions. The difference is structural: our Method JIT re-validates every field access on every iteration, while LuaJIT's trace validates once at trace entry.

Closing that gap needs cross-block shape propagation — the shape check done in the preheader (from LICM-hoisted GetField) should be visible in the loop body. That's a future round.

## Implementation

Three changes, all clean:

**Float param guards** landed first. The key insight was ordering: run the check *after* Phase 1 propagation so we can see which operands the type map already knows are float. The new `insertFloatParamGuards` method scans for unguarded `LoadSlot` params used in arithmetic with TypeFloat operands. One new `GuardType(TypeFloat)` at the function entry, then re-propagate types. The cascade is immediate — all seven generic ops in nbody's advance function become float-specialized.

**GuardType CSE** was surgical. Added a `guardKey{argID, guardType}` tracking map to LoadEliminationPass alongside the existing `loadKey` map. When a duplicate `(value, type)` guard appears in the same block, replace uses and convert to Nop. The Nop matters — guards are side-effecting so DCE won't touch them otherwise. Three tests: same-guard elimination, different-type preservation, and call-kill semantics.

**LICM extensions** were the simplest. Added `OpSqrt` and `OpGetTable` to `canHoistOp`. Sqrt is trivially pure. GetTable needed alias analysis: no in-loop `SetTable` on the same object, no calls. The `SetTable` tracking was already in place (the `-1` sentinel in `setFields` from a previous round), so we just needed the check in the fixed-point loop. Four tests covered the positive and negative cases.

Nothing broke. The pre-existing `TestQuicksortSmall` SIGBUS crash (from an unrelated JIT codegen issue) is the only test failure in the full suite.

## Results

The nbody improvement surprised us:

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| nbody | 0.261s | 0.242s | **−7.3%** |
| spectral_norm | 0.046s | 0.041s | **−10.9%** |
| matmul | 0.120s | 0.123s | noise |

We predicted 2-3% on nbody (halved for superscalar). Got 7.3%. The calibration rule was too conservative here — branch elimination on M4's wide pipeline has outsized impact. When seven generic ops become typed, you're not just saving the type-check instructions; you're eliminating branch mispredictions in the dispatch. M4's branch predictor is good, but `any`-typed dispatch has indirect jumps that defeat static prediction.

spectral_norm's 10.9% improvement was unexpected — the plan didn't target it specifically. But `spectral_norm` has float parameters too, and the same pattern (param used with feedback-typed operands) triggers the new guard insertion.

*[Results coming next...]*
