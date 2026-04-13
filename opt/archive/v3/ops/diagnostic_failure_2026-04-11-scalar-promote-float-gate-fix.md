# Diagnostic Failure — 2026-04-11-scalar-promote-float-gate-fix (R33)

**Outcome:** data-premise-error
**Type:** plan-premise incomplete (not a measurement-tool bug)
**Cycle ID:** 2026-04-11-scalar-promote-float-gate-fix
**Related:** `opt/premise_error.md` (coder's writeup with IR dumps and block topology)

## What was wrong

R33 ANALYZE framed the R32 `LoopScalarPromotionPass` no-op on nbody as a single-cause bug:

> `pass_scalar_promote.go:99` rejects GetField because it gates on `instr.Type == TypeFloat`, but production IR emits OpGetField with `TypeAny` and a separate OpGuardType(TypeFloat) consumer. Fix: classify via consumer-GuardType scan. One-line class of change.

That statement is **individually true** (A1 + A2, both HIGH-confidence with file:line citations) — yet **collectively insufficient**. Two separate upstream gates in the same pass fail before classification is ever consulted on nbody's IR:

### Gate failure #1 — exit-block-preds check (structural)
`pass_scalar_promote.go:146-150`:
```go
for _, p := range exitBlock.Preds {
    if !bodyBlocks[p.ID] { return }
}
```
nbody's j-loop exits to the i-loop header `B4`, but `B4.Preds = [B10, B3]` where `B10` is the i-loop preheader (not in j-body). Pass returns before ANY pair is classified. This is a structural property of nested-loop IR — the inner loop's natural exit target is the outer loop's header, which by construction carries the outer preheader as a co-pred.

### Gate failure #2 — isInvariantObj on loop-variant base (algorithmic)
For the second i-loop, `b := bodies[i]` is re-loaded every iteration (`v117 = GetTable v115, v144` where `v144` is the i-loop AddInt, defined inside the body). `isInvariantObj(v117) == false`, so all 3 `b.{x,y,z}` pairs are skipped. The object genuinely IS different each iteration — scalar promotion of its fields is not semantically valid without per-iteration materialization.

## Was it a "measurement tool" failure?

Partially. The R32 post-round diagnostic (`TestR32_NbodyLoopCarried`) correctly observed "9 loop-carried pairs still present after pipeline." That data was **true**. But R33 ANALYZE drew an incorrect inference from it: "pairs survive → only the float gate stopped them." The diagnostic counted pair *survival* and did not count *which gate bailed*. R33 ANALYZE had no tool to distinguish "float gate rejected" from "exit-preds gate rejected" from "isInvariantObj rejected."

The missing diagnostic primitive: a **per-gate bailout counter** on `promoteLoopPairs` that reports which condition rejected each candidate loop. That would have surfaced the two upstream bailouts in R32's own post-round re-run, not R33's IMPLEMENT phase.

## Fix / preventive action

Per R24 protocol this would nominally take a `diagnostic-fix:` commit to the measurement tool. For R33 specifically the "tool" is the pass's own bailout visibility. Two options:

**Option A (cheap, preventive):** add one `t.Logf("bail: exit-block-preds co-pred B%d from outside body", p.ID)` line at pass_scalar_promote.go:146-150 behind a debug flag + matching flag in the new production test. Cost: ~5 LOC.

**Option B (structural, next round):** fold the bailout counters into a `PassDiagnostics` struct returned by every optimization pass. This is broader scope and belongs in R34 ANALYZE, not in a diagnostic-fix commit.

**R33 commits Option A's spirit via the observe-only test** (`pass_scalar_promote_production_test.go` logs `unpromoted=%d float-phis=%d`) but without per-gate attribution. A fuller instrumentation pass is deferred to R34 if tier2_float_loop is revisited.

## Harness protocol

- Outcome in state.json: `data-premise-error`
- Summary on `previous_rounds[]`: the plan-premise failure, NOT the float-gate technique (per R24)
- Category counter: `tier2_float_loop` advances from 2 → 3 (real 3rd failure; user_priority contract says "if R33 still shows 0% on nbody, this counts as a real ceiling failure and the category sits for ≥3 rounds"). The category should decay naturally per the ceiling rule.
- Occurrence count toward harness patch: this is the 1st plan-premise error in a 5-round window. The 2-in-5 harness-patch threshold is NOT hit.

## What REVIEW should audit next round

1. **Does every "this gate is wrong" plan also verify no upstream gate fails on the same target?** This is a specific, testable rule that prevents the R33 inference gap.
2. **Do passes export per-gate bailout counters?** A single shared diagnostic primitive would make the "why didn't this fire" question cheap to answer.
3. **Does the failure pattern of R31 (SimplifyPhisPass no-op), R32 (LoopScalarPromotionPass silent-noop), R33 (LoopScalarPromotionPass upstream-gate-bailout) point at a class problem?** All three are Tier 2 passes that passed unit tests and failed production. The R33 production-pipeline diagnostic rule catches (1) and (2), but R33 shows it doesn't catch (3): a production test that fails can't by itself distinguish "plan premise wrong" from "plan premise right, implementation wrong." Consider adding a pre-plan validation step where the Coder runs the NEW production test BEFORE applying the fix, confirms it reports the premise-claim directly (e.g. "pass rejected at float gate"), and only then applies the fix.

## State left at end of round

- `internal/methodjit/pass_scalar_promote.go`: **unchanged** (plan fix applied then reverted).
- `internal/methodjit/pass_scalar_promote_production_test.go`: **new**, observe-only (`t.Skip` at end, logs the counts). Build tag `darwin && arm64`.
- `opt/premise_error.md`: **kept** (coder's original writeup with IR dumps; more detailed than this file).
- `opt/analyze_report.md`: **kept** as the starting point for next round's retrospective.
- No production code commit this round.
