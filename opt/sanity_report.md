# Sanity Report — 2026-04-11-fib-regression-root-cause (R29)

**Verdict**: clean

## Red Flag Checks

- **R1 (physics): PASS** — R29 touched zero production code, so no shared-hot-path delta divergence is possible. All wall-time deltas sit within ±3% noise; the lone outlier is coroutine_bench at −16.2%, which the plan explicitly pre-labels as high-variance and excludes from credit.
- **R2 (prediction gap): PASS** — Plan predicted "no wall-time change; all within noise" for R29 itself (the fix is deferred to R30). Measured outcome matches: zero regressions ≥5%, zero non-coroutine improvements ≥5%. Prediction and reality agree.
- **R3 (phase closeout): PASS** — `previous_rounds[-1].outcome = "no_change"` (not pending). Results table in the plan is fully filled. `state.json` `cycle`/`cycle_id` cleared to `""`. `sanity_verdict` was empty (this report sets it).
- **R4 (mandated steps): PASS** — Plan mandated (a) `tier1_fib_dump_test.go` mirrors `tier1_ack_dump_test.go`, ≤100 lines; actual is 76 lines (`git show 237855e`). (b) Does not touch `tier1_call.go` or `ackTotalInsnBaseline`; confirmed — R29 Task 0 diff shows only the new test file + `opt/current_plan.md`. (c) `opt/knowledge/r29-fib-root-cause.md` exists (+30 LOC in close-out commit). No re-baseline was required (no production change).
- **R5 (baseline staleness): PASS** — `baseline.json.commit` = `3a512b7` = `latest.json.commit`; timestamps equal (`2026-04-11T09:09:24Z`). HEAD is `af56851`, but that is a pure close-out doc/state commit landing *after* the measurement run, which is expected. No code drift between baseline and measurement.
- **R6 (scope): PASS** — Plan declared "1 test file, +90 LOC, no production". R29 Task 0 commit (`237855e`) landed exactly 1 test file at +76 LOC (under budget). Follow-up commits (`3a512b7` blog, `af56851` close-out) are paperwork. Zero production `.go` files touched across the three R29 commits.

## If flagged/failed: recommended user action

Not applicable — verdict is clean. Auto-continue to R30 is permitted.

One note for R30 REVIEW (not a red flag, forward-looking): the plan calls out that R30 ANALYZE must re-run `TestDeepRecursionRegression` + `TestQuicksortSmall` before accepting candidate A (drop the self-call `CBZ`). If R30 skips that, this sanity phase should flag it then.

## Data snapshot

- **Plan prediction**: "Fixture adds 1 test file, +90 LOC; no wall-time change; round outcome = diagnostic."
- **Measured delta**: all benchmarks within ±3.5% except coroutine_bench −16.2% (pre-labeled high-variance, ignored). Zero regressions ≥5%. fib `fibTotalInsnBaseline = 635` recorded as the R30 sentinel.
- **Baseline commit/timestamp**: `3a512b77e5396313a89c0e2107cbab36d7c264fa` @ `2026-04-11T09:09:24Z`
- **Latest commit/timestamp**: `3a512b77e5396313a89c0e2107cbab36d7c264fa` @ `2026-04-11T09:09:24Z`
- **HEAD**: `af56851bac92f2156c5c84b55c93a5ab4d3fd2af` (post-measurement close-out, doc-only)
- **Production .go files touched in R29**: 0
- **Test .go files touched in R29**: 1 (`internal/methodjit/tier1_fib_dump_test.go`, +76 LOC)
