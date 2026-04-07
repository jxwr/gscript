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

## The trap

The first version worked perfectly on nbody. Then math_intensive regressed 170%.

```javascript
func distance_sum(n) {
    for i := 1; i <= n; i++ {
        x := 1.0 * i / n
        ...
    }
}
```

`n` is an integer. But `1.0 * i` produces a float, so `Div(float, n)` has a float operand — and our heuristic said "n must be float." Guard inserted. Every call passes an integer. Every call deopts.

The fix was obvious in retrospect: if a parameter appears in BOTH integer contexts (`i <= n`) and float contexts (`1.0 * i / n`), it's an integer that auto-converts. Don't speculate. Track `intLikeParams` alongside `floatLikeParams`, delete the intersection.

nbody's `dt` survives because it's only ever used in float arithmetic — no integer comparisons, no integer arithmetic. Pure float parameter, correct speculation.

This is the kind of bug that makes you grateful for a benchmark suite. Without `math_intensive` catching it, the regression would have shipped. The fix was four lines.

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

## Results

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| nbody | 0.261s | 0.247s | **-5.4%** |
| spectral_norm | 0.046s | 0.043s | **-6.5%** |
| table_field_access | 0.052s | 0.043s | **-17.3%** |
| math_intensive | 0.070s | 0.069s | -1.4% |
| sieve | 0.089s | 0.082s | **-7.8%** |
| mandelbrot | 0.064s | 0.059s | -7.8% |

The prediction said 2-3% on nbody. Got 5-10% (varies by run). The calibration model was too conservative — branch elimination on M4's wide pipeline has outsized impact. When seven generic ops become typed, you're not just saving the type-check instructions; you're eliminating branch mispredictions in the dispatch cascade. M4's branch predictor is good, but `any`-typed dispatch has indirect jumps that defeat static prediction.

spectral_norm's improvement was a bonus — it has float parameters too. table_field_access benefited from the GuardType CSE (lots of redundant guards on the same fields).

The LuaJIT gap on nbody is now 7.7x (was 7.7x before this round — the baseline shifted from last round's improvements). Closing that gap further needs cross-block shape propagation, the structural change that eliminates per-iteration field validation entirely. That's a future round.
