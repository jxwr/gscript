# Sanity Report — 2026-04-11-braun-redundant-phi-cleanup (R31)

**Verdict**: flagged

## Red Flag Checks

- **R1 (physics): PASS** — All deltas in the ±3% band except coroutine_bench (+12.2%, known high-variance, ignored per R29 precedent). Sieve hot path moved -1.2%; related nested-loop benchmarks that would share IR-cleanup wins (matmul 0%, spectral_norm 0%, nbody -1.2%, mandelbrot -1.6%) are directionally consistent (neutral-to-slightly-negative). No opposite-sign large deltas on a shared code path. Physically plausible for "infra landed, zero real work removed."

- **R2 (prediction gap): PASS** — Plan predicted sieve -8% to -12% (0.085→0.075-0.078); measured -1.2% (0.085→0.084). Absolute gap ≈ 9pp. 10× of prediction magnitude ≈ 100pp, so the gap is ~11% of the 10× threshold. The prediction missed its direction (too optimistic) but not in a way that signals broken measurement. The plan's Lessons section transparently root-causes the miss to **diagnostic tool debt**: `profileTier2Func` (flagged in `constraints.md` as "Diagnostic test pipeline mismatch") reads a stale pipeline; production `compileTier2` already collapses those phis via `tryRemoveTrivialPhi` + ConstProp + DCE, so SimplifyPhisPass catches nothing on sieve at production.

- **R3 (phase closeout): PASS** — `previous_rounds[-1].outcome = "no_change"` (not pending). Plan's Results table fully populated with median-of-5 numbers for all 22 benchmarks. `cycle`/`cycle_id` cleared to `""` in `state.json`. `completed_steps` = [analyze, implement, verify]. Close-out commit `cf9ce72` landed.

- **R4 (mandated steps): PASS with note** — Plan's Definition of Done #5 required sieve improvement ≥5% (floor); measured -1.2% does NOT meet that floor. But the round was honestly classified as `no_change` and the Lessons section explicitly acknowledges the miss and root-causes it — not a silent pass-through. DoD #4 (re-dump `TestProfile_Sieve` and verify the 3 self-phis are gone) became moot once Lessons established the diagnostic pipeline was the misleading input. Full-package `./internal/methodjit/...` and `./internal/vm/...` test gates both green (R30 lesson honored). No mandated step silently skipped.

- **R5 (baseline staleness): PASS** — `baseline.json.commit = c375913` (the R31 code commit). `latest.json.commit` = same. Timestamps identical (`2026-04-11T11:00:23Z`). HEAD is `cf9ce72` (close-out commit touching only `opt/*` — no code delta), so benchmarks-at-c375913 correctly represent the current code state. VERIFY re-baselined as required.

- **R6 (scope): FLAG (soft)** — Plan declared "≤300 LOC (~60 impl + ~80 test + ~20 pipeline wiring)". Actual R31 code commit (c375913) shipped **687 LOC** across the declared 3 files: `pass_simplify_phis.go` 226, `pass_simplify_phis_test.go` 450, `pipeline.go` 11. That is 2.29× the budget, mechanically tripping the `>2M` rule (687 > 600).
  - **Mitigating nuance**: files touched stayed **exactly** within the declared set — no cross-module leakage, no new files outside the plan. Overage is concentrated in the test file (450 vs 80 predicted), reflecting 6 test cases instead of 4. More thorough testing, not "scope creep into unrelated code."
  - Still a soft flag: plan underestimated test-file size by 5.6×, and wall-time prediction also missed badly. Two calibration misses in one round is a workflow signal worth surfacing.

## If flagged/failed: recommended user action

No revert or re-measurement needed — the data is honest and the round correctly reported `no_change`. Two process signals worth flagging to the next REVIEW cycle:

1. **Diagnostic tool debt is now provably load-bearing.** R19 (table-kind specialize) and R31 (Braun phi cleanup) both wasted an ANALYZE+IMPLEMENT cycle because the `profileTier2Func` test reads a stale pipeline. Next REVIEW should either delete it, rewrite it to call `compileTier2()` end-to-end, or gate it behind a `//go:build diagnostic_stale` tag. Until fixed, ANALYZE must treat any IR dump produced by that test as invalid evidence.
2. **LOC budget miscalibration.** Plans keep underestimating test-file size by 3–5×. Consider dropping the LOC budget field in favor of a "files touched" bound, which is the real scope-creep guard.

Auto-continue is blocked by the `flagged` verdict per protocol. Data integrity is fine (R1/R2/R3/R5 all clean), so if the user decides the R6 overrun + prediction miss do not warrant an immediate REVIEW, they may manually advance to the next round.

## Data snapshot

- **Plan prediction**: sieve 0.085s → 0.075–0.078s (-8% to -12%); LuaJIT ratio 7.7× → 7.0×
- **Measured delta**: sieve 0.085s → 0.084s (-1.2%, below 5% floor)
- **Baseline commit/timestamp**: `c375913d40ad9beb86d76c0364b2acbeea3fe77f` @ `2026-04-11T11:00:23Z`
- **Latest commit/timestamp**: `c375913d40ad9beb86d76c0364b2acbeea3fe77f` @ `2026-04-11T11:00:23Z` (identical — re-baselined)
- **HEAD**: `cf9ce7243a333d0db10f7388588c5bddcd01066c` (close-out commit, opt/ only)
- **Scope**: 3 files / 687 LOC (declared ≤300 LOC; 2.29× overrun, test file dominates)
- **Outcome classification**: `no_change` — honest (infra landed, zero wall-time win, Lessons fully documented)
- **Category**: `field_access` (now at ceiling 2 per Lessons bullet 4)
