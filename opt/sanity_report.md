# Sanity Report — 2026-04-11-scalar-promote-float-gate-fix (R34, v3.1 first real run)

**Verdict**: failed
**Note**: SANITY phase's own claude -p session failed with "Not logged in" (auth token expired mid-round). This report was produced manually by the outer Claude Code session using the R1-R9 rubric. state.json.sanity_verdict = failed is written manually.

## Red Flag Checks

- **R1 (physics): PASS** — No production code was committed this round (Coder reverted after diagnosing the upstream gate bail). Therefore no benchmark delta can be attributed to this round. Observed deltas (nbody 0.248→0.252, object_creation 1.053→1.152) are median-of-5 variance on unchanged code.

- **R2 (prediction gap): PASS** — Plan predicted nbody −4% (−0.010s). Actual: +1.6% noise. |gap| = 5.6%. Threshold is 10× prediction magnitude (40%). 5.6% << 40% → PASS. Plan was explicitly marked `data-premise-error` by IMPLEMENT, so the gap is due to an upstream gate blocking the fix, not measurement failure.

- **R3 (phase closeout): PASS** — previous_rounds[-1].outcome = `data-premise-error` (not pending). current_plan.md Results table filled. state.json.cycle / cycle_id cleared. Close-out commit b3d8824 landed.

- **R4 (mandated steps): PASS with note** — Plan's Task 1 aborted cleanly via R24 data-premise-error path. The production-pipeline diagnostic test (pass_scalar_promote_production_test.go) was written and ran against real `RunTier2Pipeline` output (R31/R32 rule honored); kept as observe-only template (t.Skip) because premise error means it can't make an assertion yet. Full-package tests green. No mandated step silently skipped.

- **R5 (baseline staleness): PASS** — baseline.json.commit = 2c743add = latest.json.commit = 2c743add. Timestamps match. VERIFY re-measured benchmarks (median-of-5) but did not change reference.json (correct per P5). HEAD is b3d8824 (close-out commit, opt/ + docs/ only).

- **R6 (scope): PASS** — 0 code files committed. Close-out commit b3d8824 touched opt/, docs/, state.json only. Well within plan's ≤2 files / ≤30 LOC / ≤1 commit budget.

- **R7 (cumulative drift vs reference.json, harness v3 P5): FAIL** 🚨

  ```
  object_creation   ref=0.764  now=1.152  drift=+50.79%  FAIL
  sort              ref=0.042  now=0.049  drift=+16.67%  FAIL
  closure_bench     ref=0.027  now=0.030  drift=+11.11%  FAIL
  spectral_norm     ref=0.045  now=0.046  drift= +2.22%  FLAG
  nbody             ref=0.248  now=0.252  drift= +1.61%  ok
  ```

  **Three benchmarks exceed the 5% hard-fail threshold. R7 verdict: FAIL (hard halt).**

  Important nuance: this drift is **pre-existing** (R28-R32 accumulated), not caused by R34. R34 committed no code. But R7's design purpose is exactly this: the harness must halt until drift is addressed regardless of which round caused it. The next round MUST target one of these before further work.

  New info vs initial freeze: object_creation rose from +37.83% → +50.79% (median-of-5 variance on a highly allocation-sensitive benchmark). closure_bench also crossed threshold. Still pre-existing, not new damage.

- **R7 integrity (SHA check, P5): PASS** — sha256 of reference.json matches state.json.reference_baseline.sha256 (1bdfe6d619542d62...). Not tampered.

- **R8 (new pass without real-pipeline diagnostic test, R32 rule): PASS** — The Coder DID write pass_scalar_promote_production_test.go against real RunTier2Pipeline output. Test is observe-only (t.Skip) because premise error means it can't assert yet. The spirit of R8 is honored: the harness's production-pipeline test DID catch the silent-no-op, at IMPLEMENT time, before any regression shipped. This is v3 working as designed.

- **R9 (confidence labels, P4): PASS** — PLAN_CHECK iteration 1 already verified every numeric prediction had a confidence label and HIGH-confidence claims cited sources. Verdict was PASS at iter 1.

## Meta-observation for next REVIEW (critical finding)

This is the single most important sanity finding from R34:

> PLAN_CHECK evaluated the plan against authoritative-context.json and verified every cited file:line. But it did NOT verify that the cited code path is actually **reachable** when the target benchmark is compiled in production. PLAN_CHECK checks "does this line exist" not "does execution arrive here under production IR topology". The R34 premise error (float gate is correct, but exit-block-preds gate bails first) slipped through PLAN_CHECK's verification because the evaluator never simulated the pass's control flow on real IR.

**Suggested harness improvement for R35 REVIEW** (not applied here, for discussion):

PLAN_CHECK should get a "dry-run the target pass on production IR and confirm the plan's intervention point is actually reached" check. This requires instrumented pass execution: print when each gate is hit, verify the gate the plan targets is actually the first blocker. New mechanism for the evaluator-optimizer loop: not just "does cited code match" but "does execution reach cited code".

This is exactly the kind of lesson the harness is designed to learn from. REVIEW should formalize it into a new rule for plan_check.md.

## Data snapshot

- **Plan prediction**: nbody 0.248s → 0.238s (−4%, HIGH confidence per plan_check_verdict:PASS)
- **Measured delta**: nbody 0.248s → 0.252s (+1.6%, within noise, no code change)
- **Outcome**: data-premise-error (honest — Coder reverted after discovering upstream gate bails)
- **Reference commit/timestamp**: a388f782 frozen @ 2026-04-11T14:55:55Z (sha: 1bdfe6d619542d62...)
- **Latest commit/timestamp**: 2c743add @ 2026-04-11T16:00:40Z
- **HEAD**: b3d8824 (close-out, opt/ + docs/ only)
- **Scope**: 0 production .go files committed / 0 LOC. Untracked diagnostic test kept as template.
- **Category**: tier2_float_loop (category_failures unchanged at 2 — data-premise-error does NOT increment per R24 rule)
- **Round tokens**: ~11-13M (healthy, well under T1 50M cap)

## Required next round action (because of R7 FAIL)

Next round MUST pick one of these three to clear R7 before anything else:

1. **object_creation +50.79%** (0.764 → 1.152s) — highest priority, largest regression
2. **sort +16.67%** (0.042 → 0.049s)
3. **closure_bench +11.11%** (0.027 → 0.030s) — newly crossed threshold

CONTEXT_GATHER's R34 output already identified object_creation's root cause:
- `vec3_add` not inlined
- 6 `GetField:any` ops on hot path (float fast path missed)
- 3 generic `Add` ops (not `AddFloat`)

This gives R35 a clear target path. authoritative-context.json still valid (sha unchanged, data ~2h old — acceptable for R35 if re-used immediately, should regenerate if >6h).

## v3 Stage 1 overall assessment (first real round)

**Mechanisms that worked**:
- CONTEXT_GATHER produced real authoritative context (3.2M tokens, 3 candidates, structured observations) — **first time in the project's history that ANALYZE had production IR evidence before planning**
- Strict plan schema was honored by ANALYZE on first try (all required fields, assumptions cited)
- PLAN_CHECK verified every cited file:line word-for-word, PASS at iteration 1
- Full-package test + production-pipeline test caught the silent-no-op at IMPLEMENT time
- Coder aborted cleanly via R24 data-premise-error path instead of shipping a bad fix
- VERIFY re-measured and classified outcome honestly
- R7 caught cumulative drift that no previous sanity run had visibility into
- reference.json integrity SHA verified
- Round total tokens ~11-13M vs predicted 28-35M — **under budget**

**Mechanisms that revealed a gap**:
- PLAN_CHECK's "verify cited code exists" is too shallow; needs "verify execution reaches cited code". R34 found this on the very first real run — exactly what Stage 1 validation was supposed to reveal.

**Mechanism failures**:
- SANITY phase itself crashed on auth error (external, not harness bug). Worked around manually by outer session.

**Verdict on v3 Stage 1**: **working as designed**. The harness's new mechanisms collectively caught a silent-no-op, aborted cleanly, and surfaced a concrete improvement for the next REVIEW iteration. This is the first round in R28-R34 that produced actionable meta-feedback instead of just another "no_change" datapoint.
