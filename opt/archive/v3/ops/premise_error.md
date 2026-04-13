# R33 Plan Premise Error

**Reporter:** Coder sub-agent, task "fix float gate in pass_scalar_promote.go"
**Verdict:** `data-premise-error` — the plan's root-cause attribution is incomplete. Fixing just the float gate does NOT make `LoopScalarPromotionPass` fire on nbody IR. There are two additional structural gates that also fail, both of which are unrelated to the float gate and must be addressed for the pass to promote any pair.

## What the plan said

> `graph_builder.go:669` emits every `OpGetField` with `Type=TypeAny`. ... The pass at `pass_scalar_promote.go:99` gates float classification on `instr.Type == TypeFloat`, which is ALWAYS false on production IR. Fix: also accept a same-block consumer `OpGuardType` whose `Args[0].ID == instr.ID` and `Type(other.Aux) == TypeFloat`.

This framed the float gate as the sole cause of the no-op.

## What I verified

I wrote the production test (`pass_scalar_promote_production_test.go`, build tag `darwin && arm64`) that runs the real nbody `advance()` through `TieringManager → BuildGraph → RunTier2Pipeline`, then counts unpromoted `(objID, fieldAux)` pairs and `OpPhi(TypeFloat)` insertions.

**Pre-fix result:** 9 unpromoted pairs, 0 float phis. ✓ matches plan.
**Post-fix result (with plan's exact change applied):** 9 unpromoted pairs, 0 float phis. ✗ IDENTICAL to pre-fix.

The plan's fix compiles cleanly (`go build ./internal/methodjit/` is green) and is semantically correct — it does classify v9.field[vx/vy/vz] as float after the fix. But the pass never reaches the classification path because it bails earlier.

## Ground truth: where the pass actually bails

Dumped IR from `TestR32_NbodyLoopCarried` after `RunTier2Pipeline`. Block topology:

```
B0 → B10 → B4 (i-loop header, preds=[B10, B3]) → B1 (i-body) → B9 (j-preheader, hoisted bi loads) → B3 (j-header, preds=[B9, B2]) ⇄ B2 (j-body)
B3 exits to B4 (i-loop latch path)
B4 → B5 → B11 → B7 (second i-header) ⇄ B6 → B8 (Return)
```

### Gate failure #1 — exit-block-predecessor check bails the j-loop

`pass_scalar_promote.go:146-150`:
```go
for _, p := range exitBlock.Preds {
    if !bodyBlocks[p.ID] {
        return
    }
}
```

For the j-loop: `hdr=B3`, `bodyBlocks={B3,B2}`. The only out-of-body successor from the body is `B4` (via `B3 → B4`), so `exitBlock=B4`. But `B4.Preds=[B10, B3]`, and `B10` is NOT in the j-body (it's the i-loop preheader). → **pass returns, no pair ever classified.**

This is because the j-loop's exit target is the i-loop header, which unavoidably has the i-loop's own preheader as a co-pred. No amount of float-gate fixing will unblock this.

### Gate failure #2 — `isInvariantObj` bails the second i-loop

`pass_scalar_promote.go:187-196`. For the second i-loop (`hdr=B7`, `body={B7,B6}`, exit gate passes because `B8.Preds=[B7]`):

- `v117 = GetTable v115, v144` where `v144` is the i-loop AddInt (defined in B7, in body).
- `v117` is therefore defined inside the body → `isInvariantObj(v117) == false` → all three `b.x/y/z` pairs skipped.

The object `b := bodies[i]` is re-loaded each iteration, so it's loop-variant by construction. A plain `isInvariantObj` check cannot promote it even if the float gate were fixed.

### What the float gate alone fixes

It correctly classifies `v9.field[vx/vy/vz]` as float (v9 = `bi` is defined in B1 = i-body, outside the j-body, so it IS invariant to the j-loop). If Gate #1 were fixed, these 3 pairs would promote and `unpromoted` would drop 9 → 6. But Gate #1 bails first, so the fix has zero effect on production IR. Verified empirically: the pair count is bit-identical before and after.

## Evidence artifacts

- Test file written: `internal/methodjit/pass_scalar_promote_production_test.go` (100 LOC, plan-conformant — uses full `TieringManager` pipeline, no synthetic IR, no `profileTier2Func`).
- IR dump captured from `TestR32_NbodyLoopCarried` (rerun with current HEAD).
- Fix diff I tried (now reverted in `pass_scalar_promote.go`):
  ```go
  isFloat := instr.Type == TypeFloat
  if !isFloat {
      for _, other := range b.Instrs {
          if other.Op == OpGuardType &&
              Type(other.Aux) == TypeFloat &&
              len(other.Args) > 0 &&
              other.Args[0].ID == instr.ID {
              isFloat = true
              break
          }
      }
  }
  if isFloat { p.anyFloat = true } else { p.allFloat = false }
  ```

## What a correct R33 plan would require

At minimum ONE of the following, on top of the float gate:

1. **Relax exit-block-preds check** to allow external preds provided the edge from the body lands in a block where the phi semantics still hold (e.g. only require that the body's exit edges do not form a critical edge — ignore co-preds from outside the loop). This is the minimum change to unblock the j-loop.
2. **Insert a dedicated j-loop exit block** in LICM/preheader pass so `exitBlock.Preds == [B3]` only. This is a more structural fix and probably belongs in `computeLoopPreheaders`.
3. **Widen `isInvariantObj`** to recognize "obj loaded once per iteration from the same stable base" — but this is harder: the obj really IS different each iteration, so scalar promotion of its fields is NOT semantically valid for the second i-loop without per-iteration materialization. This loop genuinely cannot benefit from scalar promotion under the current pass design.

The float gate is a **necessary but insufficient** condition. Until Gate #1 is also fixed, the float-gate change is dead code on production nbody.

## State left

- `pass_scalar_promote.go`: unchanged (fix reverted).
- `pass_scalar_promote_production_test.go`: created, currently fails (9 pairs, 0 phis) — this is the correct failing state for a regression test once a complete fix lands. **I did not delete it** — it is valuable as a reproduction and should stay. Orchestrator may choose to delete it or keep it as the canonical R33 gate.
- No files touched outside these two.
- No commit.

## Files read beyond what the task pasted

- `internal/methodjit/r32_nbody_loop_carried_test.go` (template, as instructed)
- `internal/methodjit/pass_scalar_promote.go` (full file, to understand all bailouts — NOT just the pasted lines 80-112)
- `internal/methodjit/pipeline.go` lines 280-356 (to verify `ScalarPromotionPass` runs last)
- `internal/methodjit/loops.go` lines 180-210 (to confirm `headerBlocks[hdr.ID]` includes the header itself)
