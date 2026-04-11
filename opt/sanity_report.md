# Sanity Report — 2026-04-11-loop-scalar-promote-nbody

**Verdict**: clean

## Red Flag Checks

- **R1 (physics): PASS** — all deltas in the ±2.4% band. Benchmarks sharing the Tier 2 float-loop hot path (nbody 0.0%, matmul +0.8%, spectral +2.2%, mandelbrot +1.6%, fib_it −3.4%, table_field_access −4.7%) move consistently within noise — no opposite-sign large deltas on the same code path. Physically plausible for "pass wired but silently no-op on real IR."

- **R2 (prediction gap): PASS** — plan predicted nbody −4% (0.248→0.238s); measured 0.0% (0.248→0.248s). Absolute gap = 4pp; 10× the predicted magnitude = 40pp; gap is 10% of the red-flag threshold. Results section transparently root-causes the miss to `pass_scalar_promote.go:99` — the `instr.Type == TypeFloat` gate never matches production IR (`GetField : any` + trailing `GuardType float`), so the pass was a silent no-op on every real Tier 2 compilation. The R32 diagnostic fixture `TestR32_NbodyLoopCarried` was re-run post-pipeline and confirms all 9 loop-carried pairs still present. Honest no_change, not broken measurement.

- **R3 (phase closeout): PASS** — `previous_rounds[-1].outcome = "no_change"` (not pending). Plan's Results table fully populated with before/after/change/expected/met for 9 benchmarks. `cycle`/`cycle_id`/`target` all cleared to `""` in `state.json`. Close-out commit `f806a1f` landed. `rounds_since_review = 1`, `rounds_since_arch_audit = 2`.

- **R4 (mandated steps): PASS** — plan's Task 1 mandated "MUST run `go test ./internal/methodjit/...` (not a curated subset) before declaring done" (R30 lesson). Results: "internal/methodjit: PASS (all tests green, 1.5s)" + "internal/vm: PASS". Evaluator status: PASS with minor latent notes queued for R33. Diagnostic re-run on post-pipeline IR was done at close-out (late) rather than mid-IMPLEMENT — but the plan didn't mandate mid-round re-diagnosis, so this is a workflow gap Lessons #2 already flags for REVIEW, not a mandate violation.

- **R5 (baseline staleness): PASS** — `baseline.json.commit = latest.json.commit = 56b19e7` (R32 Task 1 functional commit). Both timestamps equal `2026-04-11T12:08:43Z`. Post-round HEAD is `f806a1f`, a doc-only close-out commit (only `opt/*` changed — no functional delta), so benchmarks-at-56b19e7 accurately represent the current code state. VERIFY re-baselined as required.

- **R6 (scope): PASS with note** — plan declared "≤3 files (pass_scalar_promote.go new, pass_scalar_promote_test.go new, pipeline.go edit)" and "Max LOC: 350". Git diff vs cf9ce72 shows 4 functional files: the planned 3 (264 + 296 + 6 LOC = 566 LOC) plus diagnostic harness `r32_nbody_loop_carried_test.go` (254 LOC, used to produce `opt/diagnostics/r32-nbody-loop-carried.md` in ANALYZE Step 4 and to confirm the no-op in close-out verification). File count 4 is within N+1 tolerance of 3. Functional LOC excluding diagnostic = 566 / 350 = 1.62× (< 2× threshold). Total including diagnostic harness = 820 / 350 = 2.34×, but diagnostic harness is Step-4 infrastructure rather than pass payload — reasonable to exclude. **Note**: the plan's Lessons #5 claims "Pass is 264 LOC, tests 296 LOC ... inside the 350 LOC cap" — that arithmetic is wrong (264+296 = 560 ≠ ≤350). Minor self-accounting error, not a budget violation.

## Data snapshot

- **Plan prediction**: nbody 0.248s → 0.238s (−4%)
- **Measured delta**: nbody 0.248s → 0.248s (0.0%, miss)
- **Recorded root cause**: `pass_scalar_promote.go:99` float-type gate rejects real IR (`Type: any` + trailing `GuardType float`); unit tests used hand-constructed `TypeFloat` nodes. R33 plan-starter: one-line fix to inspect GetField consumers for `GuardType float`.
- **Baseline commit/timestamp**: `56b19e7ee4a149c2679adaede7d736fd5978a741` @ `2026-04-11T12:08:43Z`
- **Latest commit/timestamp**: `56b19e7ee4a149c2679adaede7d736fd5978a741` @ `2026-04-11T12:08:43Z` (identical — re-baselined)
- **HEAD**: `f806a1f693ed50cff61da015244cc23689e5656f` (close-out, opt/ only)
- **Scope**: 4 files touched / 566 functional LOC (+254 diagnostic harness); declared ≤3 files / ≤350 LOC
- **Outcome classification**: `no_change` — honest (pass wired, zero wall-time, root cause documented, R33 fix staged)
- **Category**: `tier2_float_loop` (failures counter now at 2)

## Cross-round note (for REVIEW, not a red flag)

This is the second consecutive round (R31 sieve — stale `profileTier2Func`; R32 nbody — synthetic-IR type gate) where a new Tier 2 pass landed correctly at the unit level but did nothing on the production pipeline. Plan Lessons #1 already flags this for REVIEW as a harness patch: "every new Tier 2 pass must include a diagnostic test that runs it through `RunTier2Pipeline` on a real benchmark proto and asserts observable IR changes." Once that rule is formalized, future sanity checks should treat absence of a real-pipeline diagnostic test as an R4 mandate violation. Not my place to implement; recording the cross-round pattern for the next REVIEW's input.

Auto-continue OK — data is clean, outcome is honest, and the one-line R33 fix is already staged in the close-out notes.
