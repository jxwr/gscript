---
module: passes/scalar_promote
description: Loop scalar promotion. Promotes a loop-carried (obj, field) float pair into an SSA phi at the loop header, with init-load in the pre-header and store-back at the loop exit.
files:
  - path: internal/methodjit/pass_scalar_promote.go
  - path: internal/methodjit/pass_scalar_promote_test.go
  - path: internal/methodjit/pass_scalar_promote_production_test.go
last_verified: 2026-04-13
---

# ScalarPromotion Pass

## Purpose

Eliminate repeated field-load / field-store sequences on a loop-invariant object when the field is a float, there is exactly one store per iteration, and there are no interfering writes. The field is promoted to a phi at the loop header; the initial value is loaded once in the pre-header; the final value is stored back in the loop exit block. This is the "keep it in a register" optimization for per-iteration accumulators stored in fields — the shape that shows up in tight float reductions that store to a result slot each iteration.

## Public API

- `func ScalarPromotionPass(fn *Function) (*Function, error)`

## Invariants

- **MUST**: the pass runs AFTER `LICMPass` — it depends on LICM's dedicated pre-header and assumes `hdr.Preds[0]` is the pre-header and `hdr.Preds[1]` is the back-edge predecessor.
- **MUST**: promotion applies to a `(objID, fieldAux)` pair in a loop body iff ALL of:
  - exactly one `OpSetField` on the pair in the loop body
  - at least one `OpGetField` on the pair in the loop body
  - every get AND set on the pair has `Type == TypeFloat` (`allFloat && anyFloat`)
  - no `OpCall` / `OpSelf` anywhere in the loop body (`hasLoopCall == false`)
  - no wide-kill write (`OpSetTable` / `OpAppend` / `OpSetList`) on the same `objID` anywhere in the loop body
  - `obj` is loop-invariant (defined outside the loop body OR a function parameter) — checked by `isInvariantObj`
  - the loop has exactly one exit block, and every predecessor of that exit block is in the loop body (no critical edge)
  - `len(hdr.Preds) == 2` and `hdr.Preds[0]` is the pre-header, `hdr.Preds[1]` is inside the loop body
- **MUST**: the IR mutation, when promotion fires, is exactly:
  1. Insert a new `OpGetField(obj, field)` in the pre-header before its terminator — the init load
  2. Create a new `OpPhi` at the top of the header with `Args = [initLoad.Value(), storeInstr.Args[1]]`
  3. `replaceAllUses` of every in-loop `OpGetField` on the pair with the new phi, then remove them
  4. Remove the in-loop `OpSetField`
  5. Insert `OpSetField(obj, field, phi)` at the top of the exit block after any leading phis — the store-back
- **MUST**: pair iteration is deterministic — pairs are sorted by `(objID, fieldAux)` before promotion to avoid output drift between runs.
- **MUST NOT**: promote integer, bool, or string fields. The pass is float-only by design (R32 scope).
- **MUST NOT**: promote when the exit block has a pred outside the loop body — would require a critical-edge split that the pass does not implement.
- **MUST NOT**: promote when the loop contains a call — a call could read or write any object's fields.

## Hot paths

- `nbody` — velocity/position components are loaded and stored on every body per timestep; promotion keeps them in FPRs across the full inner loop.
- `spectral_norm` — accumulator stored back to the result table each iteration; the promoted pair lives in a D-register across the reduction.
- `mandelbrot` — zr/zi squared accumulators in the iteration loop.

## Known gaps

- **Float only.** Integer accumulators in fields are not promoted. The shape is equally beneficial for sieve-style int accumulators, but the float-only gate excludes them.
- **Single store per pair.** Loops that write a field twice (e.g. once for init, once for update) are refused even if both stores are reachable linearly.
- **Single exit block.** Loops with multiple exits (e.g. early `return`) are skipped entirely.
- **No call tolerance.** Any call in the loop kills the whole analysis — even a pure leaf call. Inlining typically handles this, but uninlineable calls leave the optimization on the table.
- **Single-pair per iteration.** The pass works on one pair at a time inside `promoteOnePair`; the alias-kill check does not model `obj1 == obj2`, so two SSA objects that alias could be doubly promoted (in practice BuildGraph does not create such aliases).
- **Requires LICM ordering.** Without LICM's fresh pre-header the `hdr.Preds[0]` invariant fails silently and promotion skips.

## Tests

- `pass_scalar_promote_test.go` — core promotion path, preconditions, determinism.
- `pass_scalar_promote_production_test.go` — end-to-end through `RunTier2Pipeline` on hand-built IR matching `nbody`-style patterns.
