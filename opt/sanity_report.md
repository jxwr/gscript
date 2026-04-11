# Sanity Report — 2026-04-11-transient-op-exit-classification (R30)

**Verdict**: clean

## Red Flag Checks

- **R1 (physics): PASS** — Post-revert deltas vs R29 baseline are all within ±3% on shared hot paths (fib +0.2%, ack +1.5%, fib_recursive +0.8%, mutual_recursion +2.6%, nbody +2.4%, mandelbrot +3.3%). No opposite-sign magnitudes. coroutine_bench +16.8% is the known high-variance outlier, pre-labeled and ignored (same call as R29). Physically consistent with "no net code change landed."
- **R2 (prediction gap): PASS (by revert)** — Plan predicted fib ≤0.20s; post-revert measured 1.437s. Raw gap is huge, but the change was reverted after correctness failure, so the "reality" is the reverted baseline, which is noise vs R29. The prediction failure is captured in Lessons bullet 4 (598bc1e pivot still blocks fib). Not a data-integrity issue.
- **R3 (phase closeout): PASS** — `previous_rounds[-1].outcome = "regressed"` (not pending). Plan Results section is fully filled with outcome, root cause, benchmarks, and 5 lessons. `state.json` `cycle`/`cycle_id` cleared to `""`. Close-out commit `a224669` landed. `sanity_verdict` will be updated by this report.
- **R4 (mandated steps): PASS** — Plan-mandated correctness gate (TestDeepRecursionRegression, TestDeepRecursionSimple, TestQuicksortSmall, TestFibTier1TotalInstructions, full package `go test ./internal/methodjit/`) was ultimately enforced: VERIFY's full-package run caught `TestTier2RecursionDeeperFib` stack corruption and drove the revert. Post-revert state passes all gates. Note: IMPLEMENT's Coder skipped the full-package run and merged a failing commit (903e505); VERIFY caught and reverted (4455fcf). This process gap is explicitly captured in Lessons bullet 1 and routes to REVIEW as a harness fix. SANITY treats the mandated step as ultimately satisfied, not violated.
- **R5 (baseline staleness): PASS** — `baseline.json.commit == latest.json.commit == 4455fcf7` (post-revert), identical timestamps `2026-04-11T10:02:18Z`. HEAD is `a224669` (close-out commit, state/meta only, no code delta). VERIFY re-baselined to the post-revert head so baseline reflects true current code state. `history/2026-04-11.json` matches.
- **R6 (scope): PASS** — Plan declared ~20 LOC, ~2 files. The reverted R30 Task 1 commit (903e505) touched exactly `tier1_handlers.go` + `tier1_manager.go` + a unit test, under budget. Net committed code delta from R29 baseline to HEAD is zero (revert + opt/ meta files only). Scope fully respected.

## If flagged/failed: recommended user action

Not applicable — verdict is clean. Auto-continue to R31 is permitted.

One forward-looking note for REVIEW (not a SANITY red flag): Lessons bullet 1 identifies that IMPLEMENT's Coder skipped the full-package `go test ./internal/methodjit/...` gate despite the plan listing it as merge-blocking. This is a harness/prompt issue (Coder interpretation of "correctness gate") and should feed the next REVIEW cycle as a structural fix to the IMPLEMENT prompt — every Coder task MUST run the full package before reporting done.

## Data snapshot

- **Plan prediction**: fib ≤0.20s, ackermann ≤0.28s, fib_recursive ≤2.0s, mutual_recursion ±5%, others ±5%. Outcome restored to pre-598bc1e performance.
- **Measured delta (post-revert vs R29 baseline)**: fib 1.434→1.437s (+0.2%), ack 0.270→0.274s (+1.5%), fib_recursive 14.285→14.400s (+0.8%), mutual_recursion 0.189→0.194s (+2.6%), nbody 0.245→0.251s (+2.4%), mandelbrot 0.061→0.063s (+3.3%). coroutine_bench +16.8% (ignored high-variance). All other benchmarks ≤±3%.
- **Baseline commit/timestamp**: `4455fcf7174650c0fb396d25f10deb0494ee8a69` @ `2026-04-11T10:02:18Z`
- **Latest commit/timestamp**: `4455fcf7174650c0fb396d25f10deb0494ee8a69` @ `2026-04-11T10:02:18Z` (identical — correct post-revert state)
- **HEAD**: `a224669e81b3f55b8e0882c44047997b20d3fd24` (close-out commit, state/meta only)
- **Outcome**: `regressed` — R30 Task 1 reverted. `isTransientOpExit(OP_GETGLOBAL)` hypothesis invalid under Tier 2/Tier 1 cross-tier recursion: `TestTier2RecursionDeeperFib` crashed with "fatal error: unknown caller pc" (goroutine stack grew past JIT frame assumptions). fib remains 10× pre-598bc1e (1.437s vs 0.131s).
- **Production .go files touched in R30 (net)**: 0 (revert)
- **Open problem for R31**: restore fib without breaking `TestDeepRecursionRegression/quicksort_5000` AND `TestTier2RecursionDeeperFib`. Remaining angles: HasOpExits proto flag, depth-gated predicate.
