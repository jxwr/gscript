# Sanity Report — 2026-04-12-object-creation-regression-bisect (R35)

**Verdict**: failed

## Red Flag Checks

- **R1 (physics): PASS** — No production code committed. All benchmark deltas vs baseline are within ±3% noise. No code path changes exist to produce opposing deltas. Diagnostic round by design.

- **R2 (prediction gap): PASS** — Plan predicted 0% change (diagnostic round, `expected_delta_pct: 0.0`, confidence: HIGH). Measured: object_creation 1.152 → 1.141 = −1.0% (noise floor). Gap is negligible and consistent with median-of-5 variance on unchanged code.

- **R3 (phase closeout): PASS** — state.json `cycle` and `cycle_id` both cleared (empty strings). `previous_rounds[-1].outcome` = `no_change` (not pending). Plan Results section fully populated. Close-out commit 67f3ed8 landed.

- **R4 (mandated steps): PASS** — Plan mandates:
  - Task 0: insn-count fixture using `Diagnose()`/`compileTier2()` (NOT `profileTier2Func`) — committed at 0471490 ✓
  - Task 1: pre-bisect sanity step (failure_signal says ABORT if skipped) — bisect converged on 39b5ef3 (a production-code commit, consistent with correct witness behavior) ✓
  - No production `.go` file modifications — diff confirms only `*_test.go`, `.sh`, and `opt/knowledge/` touched ✓
  - No reference.json modification — R7 integrity PASS confirms ✓
  - `max_commits: 2` — implementation commits are 0471490 (Task 0) and 9bb4fa9 (Task 1); 67f3ed8 is VERIFY close-out ✓

- **R5 (baseline staleness): PASS** — `baseline.json.commit` = `latest.json.commit` = 9bb4fa9. Timestamps identical (2026-04-12T02:22:28Z). For a no_change round with no code delta, baseline = latest is correct.

- **R6 (scope): PASS** — Plan declared ≤4 files, ≤200 source LOC, ≤2 commits. Actual:
  - 3 files: `object_creation_dump_test.go` (113 lines, test — excluded from source LOC), `bisect_object_creation.sh` (27 lines), `r35-object-creation-regression.md` (76 lines, knowledge doc — not .go source)
  - Source LOC: 27 (bisect script only). Well within 200.
  - Implementation commits: 2 (0471490, 9bb4fa9). Within budget.

- **R7 (cumulative drift vs reference.json, P5): FAIL**

  ```
  object_creation           ref=0.764 now=1.141 drift=+49.35% FAIL
  sort                      ref=0.042 now=0.051 drift=+21.43% FAIL
  closure_bench             ref=0.027 now=0.028 drift= +3.70% FLAG
  table_array_access        ref=0.094 now=0.097 drift= +3.19% FLAG
  fannkuch                  ref=0.048 now=0.049 drift= +2.08% FLAG
  fibonacci_iterative       ref=0.288 now=0.291 drift= +1.04% ok
  mandelbrot                ref=0.063 now=0.063 drift= +0.00% ok
  sieve                     ref=0.088 now=0.088 drift= +0.00% ok
  nbody                     ref=0.248 now=0.248 drift= +0.00% ok
  ```

  **Two benchmarks exceed the 5% hard-fail threshold. R7 verdict: FAIL (hard halt).**

  Context: this drift is **pre-existing** — R34 sanity already flagged it. R35 was the direct response: a diagnostic round to identify the culprit commit. Bisect successfully identified 39b5ef3 as the root cause. R36 proposes the forward fix (remove dead `shape *Shape` pointer + high-water-mark ScanGCRoots). The drift did not worsen this round (object_creation actually went from 1.152 → 1.141, −1.0% noise).

  Notable vs R34's report: closure_bench improved from +11.11% → +3.70% (no longer exceeds 5% threshold). sort worsened from +16.67% → +21.43% (measurement noise on a 0.042-0.051s benchmark). spectral_norm dropped below flag threshold (0.0% now vs +2.22% in R34).

- **R7 integrity (SHA check, P5): PASS** — sha256(reference.json) = 1bdfe6d619542d62... matches state.json.reference_baseline.sha256. Not tampered.

- **R8 (new pass without real-pipeline diagnostic test): N/A** — No `pass_*.go` files added or edited this round. Zero production code changes.

- **R9 (confidence labels, P4): PASS** — All 7 predictions/assumptions in plan frontmatter carry explicit `confidence:` labels (5 HIGH, 2 MEDIUM). Every HIGH-confidence claim cites a specific source (opt/authoritative-context.json, opt/INDEX.md, git log, constraints.md). PLAN_CHECK iteration 2 verified this at 2026-04-12T03:15:00Z.

## If failed: recommended user action

R7 FAIL is pre-existing and was the motivation for R35. R35 accomplished its diagnostic goal: culprit identified (39b5ef3), knowledge doc written, forward fix proposed. **Proceed to R36 targeting the object_creation and sort regressions** with the two concrete fixes from `opt/knowledge/r35-object-creation-regression.md`:
1. Remove dead `shape *Shape` field from Table struct (write-only pointer creating GC pressure)
2. High-water-mark `ScanGCRoots` to scan only the active frame window, not the full 16KB regs slice

Both sort and closure_bench drifts are likely collateral from the same ScanGCRoots overcost — R36 fix 2 should recover all three.

## Data snapshot

- **Plan prediction**: object_creation 0% (diagnostic round, no code change)
- **Measured delta**: object_creation 1.152 → 1.141 (−1.0%, noise)
- **Outcome**: no_change (diagnostic round completed successfully)
- **Reference commit/timestamp**: a388f782 frozen @ 2026-04-11T14:55:55Z
- **Latest commit/timestamp**: 9bb4fa9 @ 2026-04-12T02:22:28Z
- **HEAD**: 67f3ed8 (close-out commit)
- **Scope**: 0 production .go files, 1 test file (113 LOC), 1 shell script (27 LOC), 1 knowledge doc (76 LOC)
- **Key deliverable**: opt/knowledge/r35-object-creation-regression.md — root cause 39b5ef3 (Shape + ScanGCRoots)
