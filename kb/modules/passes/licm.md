---
module: passes/licm
description: Loop-invariant code motion. Moves pure invariant computations into a fresh pre-header block, innermost loops first. Conservative alias analysis for GetField / GetGlobal / LoadSlot.
files:
  - path: internal/methodjit/pass_licm.go
  - path: internal/methodjit/pass_licm_test.go
  - path: internal/methodjit/pass_licm_hoist_test.go
last_verified: 2026-04-13
---

# LICM Pass

## Purpose

For each loop in the function, move instructions whose operands do not change during loop execution out to a fresh pre-header block. The pre-header becomes the sole "outside → header" edge so the hoisted instructions run once per loop entry instead of once per iteration. Loops are processed innermost-first so hoists cascade outward.

## Public API

- `func LICMPass(fn *Function) (*Function, error)`
- `func canHoistOp(op Op) bool` — ops whose semantics permit hoisting (pure + no PC-dependent state)
- `func isIntArithOp(op Op) bool` — ops whose emitter carries an overflow check, requiring `Int48Safe`

## Invariants

- **MUST**: loops are processed in descending nesting depth (innermost first); `loopInfo` is recomputed after every loop's hoist because the CFG changes.
- **MUST**: `canHoistOp` whitelists only pure ops. The current set is:
  - Constants: `OpConstInt`, `OpConstFloat`, `OpConstBool`, `OpConstNil`
  - Loads: `OpLoadSlot`, `OpGetField`, `OpGetGlobal`, `OpGetTable` (each with alias-kill checks)
  - Pure ALU: `OpSqrt`, float arithmetic/negate, int arithmetic/negate (adds/subs/muls/negs + comparisons), `OpNot`
  - Guards: `OpGuardType` ONLY (GScript deopt is PC-independent, so relocating the deopt point is safe)
- **MUST NOT**: hoist `OpGuardTruthy` or `OpGuardNonNil` — these are control-flow guards whose failure point matters.
- **MUST**: `OpLoadSlot` hoist requires NO in-loop `OpStoreSlot` with the same `Aux` (slot number).
- **MUST**: `OpGetField` hoist requires: no in-loop `OpCall` / `OpSelf`, no in-loop `OpSetField` on the same `(objID, fieldAux)`, no in-loop `OpSetTable` on the same `objID` (sentinel `fieldAux=-1`), no in-loop `OpAppend`/`OpSetList` on the same `objID`.
- **MUST**: `OpGetTable` hoist requires: no in-loop call and no in-loop `OpSetTable` on the same `objID`.
- **MUST**: `OpGetGlobal` hoist requires: no in-loop call and no in-loop `OpSetGlobal` with the same `Aux` (constant-pool name index).
- **MUST**: int arithmetic hoist (`OpAddInt`/`OpSubInt`/`OpMulInt`/`OpNegInt`) requires `fn.Int48Safe[instr.ID] == true` — without it, the emitter's overflow check would be relocated past a point where it could still trap.
- **MUST**: after hoisting, `Validate(fn)` runs; any failure becomes a wrapping `"LICM produced invalid IR: ..."` error.
- **MUST**: the freshly created pre-header block `ph` has `ph.Succs = [hdr]` and `ph.Preds = outside` (the original outside predecessors of `hdr`). Every outside pred's terminator is retargeted via `retargetTerminator(p, hdr.ID, ph.ID)`.
- **MUST**: header phis are updated so `Args[0]` (the "from pre-header" slot) points into `ph`. Loop back-edge arg order is preserved.
- **MUST NOT**: hoist phi nodes or terminators. They are explicitly skipped.
- **MUST NOT**: hoist an op whose defining block is not inside the loop body — the hoist set is filtered by `bodyBlocks[instr.Block.ID]` before the move.

## Hot paths

- `nbody` — hoisting the loop-invariant mass constants + `GetField` on the `bodies[0]` reference pair + guard CSE.
- `spectral_norm` — hoisting `A(i,j)` constants and loop-invariant arithmetic out of the inner reduction.
- `mandelbrot` — complex-plane constant offsets and iteration-limit constants.
- `fibonacci_iterative` — trivial: no loop-invariant work, LICM is a no-op.

## Known gaps

- **No store hoisting / sinking.** SetField/SetTable are never moved.
- **No partial invariance.** An op invariant on some iterations but not others is never hoisted (would need PRE).
- **Conservative call model.** Any `OpCall` or `OpSelf` in the loop body blocks all load hoisting (field, table, global) — even if the call provably does not touch the object. No side-effect summary for callees.
- **Single pre-header per loop.** Multiple entries into a loop trigger a merge before the pass runs; no multi-header support.
- **No hoisting into enclosing pre-header.** Outer-loop pre-headers are separate — a value hoisted into an inner pre-header is re-hoisted (not optimal; innermost-first iteration helps but is not bulletproof).

## Tests

- `pass_licm_test.go` — core hoist-safe whitelist, alias kills, invariant detection, pre-header construction, phi rewire.
- `pass_licm_hoist_test.go` — int48-gated arithmetic hoisting, nested-loop cascade.
