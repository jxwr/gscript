---
# ══════════════════════════════════════════════════════════════════════════
# HARNESS v3 PLAN — R35 object_creation regression bisect (diagnostic round)
# ══════════════════════════════════════════════════════════════════════════

cycle_id: 2026-04-12-object-creation-regression-bisect
date: 2026-04-12
category: allocation_heavy
initiative: standalone

# ═══ Target ═══
target:
  - benchmark: object_creation
    current_jit_s: 1.152
    reference_jit_s: 0.764
    expected_jit_s: 1.152         # diagnostic round — no wall-time change expected
    expected_delta_pct: 0.0       # no code change; purely bisect + knowledge doc
    confidence: HIGH
    confidence_why: "Diagnostic round writes no production .go code, so wall-time cannot change. Any delta observed in VERIFY is noise (median-of-5). R29 precedent (fib +988% root-cause round) landed with outcome=no_change and delivered a targeted knowledge doc that enabled R30."

# ═══ Scope budget (machine-enforced) ═══
max_files: 4          # bisect driver script + insn-count fixture test + knowledge doc + analyze bookkeeping
max_source_loc: 200   # excludes *_test.go per R22/R32 review
max_commits: 2        # commit 1 = Task 0 fixture, commit 2 = Task 1 findings + knowledge doc

# ═══ Assumptions ═══
assumptions:
  - id: A1
    claim: "object_creation has drifted +50.79% vs reference.json (0.764 → 1.152s) and the drift entered between frozen_from_commit a388f782 and HEAD b3d8824."
    type: cited-evidence
    evidence: "opt/authoritative-context.json#drift_table[0] and opt/sanity_report.md R7 FAIL section"
    confidence: HIGH
    source: "CONTEXT_GATHER R34 output + R34 SANITY report"

  - id: A2
    claim: "The regression is caused by one (or at most two) commits in the set {598bc1e, 39b5ef3, 144c1a4, 903e505, 4455fcf, c375913, 56b19e7} (7 production-code-changing commits). The remaining 5 commits in the filter range are tests-only or bookkeeping (4b321fb, 237855e, b3d8824, f806a1f, cf9ce72) and are excluded as culprits."
    type: cited-evidence
    evidence: |
      `git log --format='%h %cI %s' a388f782..HEAD -- internal/methodjit internal/vm internal/compiler internal/runtime` returns 12 commits (re-run 2026-04-12 during plan_check iter-2 rewrite). Per-commit `git show --stat` classification:
      Production code (7): 598bc1e, 39b5ef3, 144c1a4, 903e505, 4455fcf, c375913, 56b19e7
      Tests/opt bookkeeping only (5, excluded as culprits):
        - 4b321fb: 3 internal/methodjit/*_test.go files (fixture infra)
        - 237855e: internal/methodjit/tier1_fib_dump_test.go only (R29 Task 0)
        - cf9ce72: internal/methodjit/tier2_float_profile_test.go only (+6 lines, close-out)
        - f806a1f: internal/methodjit/r32_nbody_loop_carried_test.go only (close-out)
        - b3d8824: pass_scalar_promote_production_test.go only (close-out)
      Tests-only commits cannot change benchmark wall-time (test files are not compiled into benchmark binaries), so the bisect-range set of production-code culprits is exactly the 7 listed.
    confidence: HIGH
    source: "git log + per-commit git show --stat, re-verified 2026-04-12 plan_check iter-2"

  - id: A3
    claim: "`benchmarks/run_all.sh --runs=3 -- object_creation` is a deterministic witness for `git bisect run`: it returns exit 0 when object_creation wall-time is within 5% of the reference and exit 1 when ≥5% slower."
    type: requires-live-run
    evidence: "Task 1 writes a 15-line shell wrapper that invokes the benchmark, parses median-of-3 wall-time, compares to reference 0.764s + 5% floor. Verification of the claim (exit 1 on HEAD / exit 0 on a388f782) happens as Task 1's FIRST action — it cannot be performed during plan_check because the wrapper does not yet exist (deferred per requires-live-run rule)."
    confidence: MEDIUM
    confidence_why: "Wrapper correctness depends on median-of-3 being stable enough at the 5% threshold for a 1-second benchmark. R25 confirmed median-of-5 is stable on this benchmark class; median-of-3 is ~2× noisier but still well within the 5% × 0.388s drift = 0.038s budget. MEDIUM (not HIGH) ceiling is driven by the deferral itself: plan_check has NO way to verify the wrapper's exit-code semantics until Task 1 builds it, so the only validation A3 ever receives is Task 1's own pre-bisect sanity step. If that step is skipped, the wrapper could silently mis-classify and bisect converges on garbage — see failure_signals entry 'Pre-bisect sanity step skipped'."
    source: "opt/knowledge/r25-measurement-repair.md (median-of-N rationale)"

  - id: A4
    claim: "39b5ef3 is the highest-probability culprit because it is the only commit in the set that rewrites Shape/SetField lowering paths, and object_creation's new_vec3 (hot-loop callee) does 3 SetFields per iteration."
    type: cited-evidence
    evidence: "git show --stat 39b5ef3 shows internal/runtime/{shape.go,table.go,table_int.go} modified (+568 lines runtime code) + internal/vm/vm.go ScanGCRoots change. opt/authoritative-context.json#bisect_candidates marks it 'HIGH — Shape system change directly affects SetField/NewTable lowering; object_creation is allocation-heavy'."
    confidence: MEDIUM
    confidence_why: "Strong circumstantial fit (SetField-heavy benchmark + SetField-touching commit) but not yet proven by bisect. Downgraded from HIGH because 'plausible fit' is not the same as 'caused'."
    source: "git show 39b5ef3 + opt/authoritative-context.json"

  - id: A5
    claim: "A pure diagnostic round with no production .go code change is a valid harness-v3 round shape (R29 precedent)."
    type: derivable-from-code
    evidence: "opt/INDEX.md row R29 (2026-04-11-fib-regression-root-cause): outcome=no_change, deliverables='opt/knowledge/r29-fib-root-cause.md + tier1_fib_dump_test.go, No production code touched.'"
    confidence: HIGH
    source: "opt/INDEX.md, opt/knowledge/r29-fib-root-cause.md"

  - id: A6
    claim: "Task 0's production-pipeline insn-count fixture must use the real compileTier2() path (e.g. via Diagnose() or TieringManager), NOT profileTier2Func — per P3 and constraints.md 'diagnostic test pipeline mismatch' rule."
    type: derivable-from-code
    evidence: "docs-internal/architecture/constraints.md 'Technical Debt' section + opt/harness-core-principles.md P3 + permanent anti-pattern #1"
    confidence: HIGH
    source: "constraints.md + harness-core-principles.md"

# ═══ Prior art ═══
prior_art:
  - system: GScript internal
    reference: "R29 (2026-04-11-fib-regression-root-cause)"
    applicability: "Identical pattern: diagnose a large regression via targeted instrumentation, ship knowledge doc, defer fix to next round. R29 shipped opt/knowledge/r29-fib-root-cause.md + no_change outcome; R30 then picked a fix path from the doc."
    citation: "opt/INDEX.md row 29, opt/knowledge/r29-fib-root-cause.md"
  - system: git
    reference: "git bisect run"
    applicability: "Classic automated bisect. bench-witness wrapper returns exit 0/1 based on wall-time threshold. log2(8) ≈ 3 bench runs to converge."
    citation: "git-bisect(1) manpage, not fetched this round"

# ═══ Failure signals ═══
failure_signals:
  - condition: "Bisect converges on a tests-only commit (4b321fb, 237855e, cf9ce72, f806a1f, b3d8824) — physically impossible since test files are not compiled into benchmarks"
    action: "Assumption A2 wrong; the named commit must have a hidden code-touching sibling file. Read the full commit diff, widen the production-code set to match, re-run bisect."
  - condition: "Bisect fails to converge (wrapper is non-deterministic at 5% threshold)"
    action: "Re-run bisect with median-of-5 and 3% threshold; if still non-convergent, expand scope to run each commit individually with median-of-10 and record wall-time table in the knowledge doc (abandon automated bisect, fall back to full table)."
  - condition: "Task 0 fixture captures insn counts that already DIFFER from authoritative-context.json (expected 208/813/988)"
    action: "STOP. Authoritative-context.json is fresh as of 02:10 today; a mismatch means CONTEXT_GATHER's capture was non-deterministic. Root-cause before proceeding — do NOT ship a fixture with drifting numbers."
  - condition: "Any production .go file ends up modified by Task 1"
    action: "Hard revert. This is a diagnostic round by contract — if Task 1 applies a fix it has exceeded scope. Fix goes to R36 with a proper plan."
  - condition: "Pre-bisect sanity step skipped — Task 1 runs `git bisect run` without first verifying the wrapper returns exit 1 on HEAD b3d8824 AND exit 0 on a388f782"
    action: "ABORT Task 1 immediately. The wrapper's exit-code semantics are the sole validator of A3 (plan_check cannot test them). If the wrapper is wrong, bisect converges silently on garbage and the R36 fix targets the wrong commit. Fix the wrapper, run the two sanity commands, verify both exit codes match expectation, THEN start `git bisect run`. This step is non-negotiable — promoted from plan-body prose to a failure_signal at plan_check iter-2 per evaluator feedback."

# ═══ Self-assessment ═══
self_assessment:
  uses_profileTier2Func: false
  uses_hand_constructed_ir_in_tests: false
  authoritative_context_consumed: true
  all_predictions_have_confidence: true
  all_claims_cite_sources: true
---

# Optimization Plan: R35 object_creation regression bisect (diagnostic)

## Overview

object_creation has drifted +50.79% (0.764s → 1.152s) vs frozen reference. Sanity R7 halted the harness until the next round targets one of the R7 drifters. This round is a **pure diagnostic**: identify the causing commit by `git bisect run` across 7 code-changing commits post-reference, read the culprit, write `opt/knowledge/r35-object-creation-regression.md` with root-cause + forward-fix proposal. No production `.go` code changes. R36 ships the surgical fix.

This is R29's playbook (R29 root-caused fib +988% with outcome `no_change`, enabling R30's targeted work). It is valid per harness v3 — there is no rule requiring every round to write production code.

## Root Cause Analysis

From frontmatter assumptions A1, A2, A4:

- **A1**: CONTEXT_GATHER + R34 SANITY confirm +50.79% drift on object_creation, HIGH confidence.
- **A2**: 7 code-changing commits exist between frozen `a388f782` and HEAD `b3d8824` (4b321fb excluded as test-only). The culprit is one of: 598bc1e, 39b5ef3, 144c1a4, 903e505, 4455fcf, c375913, 56b19e7.
- **A4**: 39b5ef3 (Shape system rewrite + ScanGCRoots scan-all-regs) is the highest-probability culprit because (a) it's the only commit rewriting SetField/NewTable lowering, (b) it widens GC scan work, and (c) object_creation is allocation- and SetField-heavy via new_vec3. But this is MEDIUM confidence — bisect will verify.

**Actual root cause is unknown until Task 1's bisect completes.** This plan commits to producing the evidence, not to a pre-decided fix. The knowledge doc will name the culprit commit with HIGH confidence and propose the R36 forward fix.

## Approach

1. Lock in the current regression as a reproducible diagnostic fixture (Task 0).
2. Bisect-run across the 7 code-changing commits to identify the culprit (Task 1, first half).
3. Read the culprit's diff, cross-reference authoritative-context observations, propose the R36 forward fix (Task 1, second half).
4. No production `.go` edits. Only new files: `opt/knowledge/r35-object-creation-regression.md` + a bisect driver script + a production-pipeline insn-count fixture test.

## Task Breakdown

### Task 0 (infra/diagnostic, orchestrator-driven) — Production-pipeline insn-count fixture ✅ DONE

**File**: `internal/methodjit/object_creation_dump_test.go` (NEW, ≤150 LOC including fixture)

**Goal**: Lock in the current regression baseline as a TDD-style assertion. R36 (or any future round) can then assert "insn count back to ≤ reference" as a non-flaky proof-of-fix.

**Implementation constraints** (P3 + R32 rule):
- Use `Diagnose()` on a real `compileTier2()` path via `TieringManager.TryCompile()`. **NEVER use `profileTier2Func`.** Load `benchmarks/suite/object_creation.gs`, locate each of the three hot protos (`create_and_sum`, `transform_chain`, `new_vec3`), compile via the production pipeline, count disassembled ARM64 instructions.
- Fixture values from `opt/authoritative-context.json#candidates[object_creation].disasm_summary`:
  - `create_and_sum`: 813 total insns, 466 memory
  - `transform_chain`: 988 total insns, 573 memory
  - `new_vec3`: 208 total insns, 129 memory
- Use R22-style structural match (tolerate ±2% for each count). Do NOT hard-code exact integers — the counts are regression-class numbers and the fixture's purpose is to detect >5% drift, not exact match.
- Test file must include a doc comment explaining the fixture's purpose ("regression witness for R35 → R36 fix; expected to shrink when R36 fix lands").

**Scope boundary (explicit)**:
- Do NOT touch any `.go` file outside `internal/methodjit/object_creation_dump_test.go`.
- Do NOT modify `benchmarks/data/reference.json` (P5).
- Do NOT spawn a Research sub-agent.

**Test**: the fixture IS the test. Running `go test ./internal/methodjit/ -run TestObjectCreationDump -v` must pass at HEAD.

**Commit**: `opt: R35 Task 0 — object_creation insn-count fixture (regression witness)`

### Task 1 (ONE Coder) — Automated bisect + root-cause knowledge doc ✅ DONE

**Files**:
1. `scripts/bisect_object_creation.sh` (NEW, ≤50 LOC)
2. `opt/knowledge/r35-object-creation-regression.md` (NEW, ≤300 LOC)

**Pseudocode** for `scripts/bisect_object_creation.sh`:

```
#!/bin/bash
# Witness script for `git bisect run`. Exit 0 = good (object_creation ≤ ref+5%).
# Exit 1 = bad (>5% slower). Exit 125 = skip (build failure or >10% variance).
set -e
go build -o /tmp/gscript_bisect ./cmd/gscript/ 2>/dev/null || exit 125
T1=$(./bench_once)   # run benchmarks/suite/object_creation.gs once, extract wall-time
T2=$(./bench_once)
T3=$(./bench_once)
# median-of-3
MEDIAN=$(printf '%s\n' "$T1" "$T2" "$T3" | sort -n | sed -n '2p')
# threshold = 0.764 * 1.05 = 0.802
awk -v m="$MEDIAN" 'BEGIN{exit !(m > 0.802)}' && exit 1 || exit 0
```

Invocation: `git bisect start b3d8824 a388f782 && git bisect run scripts/bisect_object_creation.sh`.

**Pre-bisect sanity** (MANDATORY):
1. Run the wrapper on HEAD — must return exit 1.
2. Run the wrapper on a388f782 — must return exit 0.
3. If either fails, the wrapper is wrong; fix before bisecting.

**Post-bisect steps**:
1. `git show <culprit-sha> --stat` — identify the file scope.
2. Read the full diff of the culprit commit. Cross-reference with `opt/authoritative-context.json#candidates[object_creation].observations` — which observation does the culprit explain?
3. Write `opt/knowledge/r35-object-creation-regression.md` with sections:
   - **Root cause** (1-paragraph): culprit SHA, what it changed, mechanism of slowdown
   - **Evidence chain**: the bisect log + the author-cited observation from CONTEXT_GATHER
   - **Proposed R36 forward fix**: specific file + function + approach. If the culprit is a correctness fix that cannot be reverted (e.g., 598bc1e, 39b5ef3's GC scan), propose a surgical refinement (e.g., "keep the GC scan semantic but walk frames instead of scanning all 16KB of registers").
   - **Risk notes**: cross-contamination with other R7 drifters (sort, closure_bench) — does the same fix apply?
   - **Reproducibility**: exact commands to re-run the bisect.
4. Return to HEAD: `git bisect reset`.

**Strict scope boundary (explicit)**:
- Do NOT modify any `internal/` directory .go file.
- Do NOT modify `reference.json`, `latest.json`, or `baseline.json`.
- Do NOT apply any fix, revert any commit, or cherry-pick. The fix is R36's work.
- Do NOT spawn additional sub-agents — Task 1 is a single Coder.

**Test**: none required; deliverable is the knowledge doc, and Task 0's fixture is the only code asset.

**Commit**: `opt: R35 Task 1 — object_creation regression bisect + knowledge doc`

## Integration Test

Not applicable (no tiering/codegen changes). Task 0's fixture IS the only test.

```bash
# Verify fixture passes at HEAD
go test ./internal/methodjit/ -run TestObjectCreationDump -v

# After Task 1 — verify bisect converged
cat opt/knowledge/r35-object-creation-regression.md | grep -E '^Culprit:' | head -1
```

## Results (filled by VERIFY)
| Benchmark | Reference | Before | After | Change vs baseline | Expected | Met? |
|-----------|-----------|--------|-------|--------------------|----------|------|
| object_creation | 0.764 | 1.152 | 1.141 | -1.0% (noise) | 0% (no code) | YES |

### Cumulative drift vs reference.json (non-excluded benchmarks)

| Benchmark | Reference | Latest | Drift | Status |
|-----------|-----------|--------|-------|--------|
| sieve | 0.088 | 0.088 | 0.0% | OK |
| mandelbrot | 0.063 | 0.063 | 0.0% | OK |
| matmul | 0.124 | 0.123 | -0.8% | OK |
| spectral_norm | 0.045 | 0.045 | 0.0% | OK |
| nbody | 0.248 | 0.248 | 0.0% | OK |
| fannkuch | 0.048 | 0.049 | +2.1% | FLAG |
| sort | 0.042 | 0.051 | +21.4% | FAIL |
| sum_primes | 0.004 | 0.004 | 0.0% | OK |
| method_dispatch | 0.102 | 0.101 | -1.0% | OK |
| closure_bench | 0.027 | 0.028 | +3.7% | FLAG |
| string_bench | 0.031 | 0.030 | -3.2% | FLAG |
| binary_trees | 2.311 | 2.006 | -13.2% | OK (improved) |
| table_field_access | 0.043 | 0.043 | 0.0% | OK |
| table_array_access | 0.094 | 0.097 | +3.2% | FLAG |
| coroutine_bench | 16.550 | 15.266 | -7.8% | OK (improved) |
| fibonacci_iterative | 0.288 | 0.291 | +1.0% | OK |
| math_intensive | 0.070 | 0.070 | 0.0% | OK |
| object_creation | 0.764 | 1.141 | +49.3% | FAIL |

Drifts ≥5% FAIL: object_creation (+49.3%), sort (+21.4%)
Knowledge doc identifies 39b5ef3 as culprit for all three R7 drifters. R36 forward fix proposed.

### Test Status
- All passing (methodjit 2.479s, vm 0.582s)

### Evaluator Findings
- PASS. No production .go files modified. Scope clean. Bisect script and fixture test correct.

### Regressions (≥5% vs baseline)
- None (all deltas vs baseline within ±5% noise; coroutine_bench -7.8% is known high-variance improvement)

### Additional deliverables
- opt/knowledge/r35-object-creation-regression.md — root cause: 39b5ef3 (Shape *Shape GC pointer + ScanGCRoots uncapped scan)
- internal/methodjit/object_creation_dump_test.go — production-pipeline insn-count fixture
- scripts/bisect_object_creation.sh — bisect witness script

## Lessons (filled by VERIFY)

1. **git bisect is the fastest diagnostic tool** — 4 bench runs (median-of-3 each = 12 total) converged on the culprit in under 5 minutes. Reading 7 diffs manually would have taken longer and produced lower confidence.
2. **Correctness fixes have performance budgets too** — 39b5ef3 fixed a real SIGSEGV but added GC overhead proportional to object count. The write-only `shape *Shape` pointer and uncapped `ScanGCRoots` scan are both unnecessary for the correctness property they protect.
3. **One commit can cause multiple drifters** — sort (+21.4%) and closure_bench (+3.7%) are likely victims of the same ScanGCRoots overcost, not independent regressions. R36's fix 2 (high-water mark) should recover all three.
4. **Frozen reference baseline caught what rolling could not** — 50.79% drift accumulated over 8 commits with no single round flagging it. This is exactly the P5 scenario.
5. **Diagnostic rounds have compounding ROI** — R29 (fib root-cause) enabled R30. R35 (object_creation root-cause) enables R36 with a specific commit SHA and two concrete fix proposals. No speculation.

## Plan Check Feedback (populated by plan_check on rewrite cycles only)

### Iteration 1 → 2 (2026-04-12)

plan_check verdict: NEEDS_IMPROVEMENT. Two mechanical issues flagged, both addressed in this rewrite with no target/approach change:

1. **A2 evidence stale** — original evidence field claimed "8 commits listed" from the `git log a388f782..HEAD -- internal/methodjit internal/vm internal/compiler internal/runtime` filter; actual re-run returned 12. Fix: A2's evidence block now enumerates the full 12-commit set, classifies each by `git show --stat` as either production-code (7: 598bc1e, 39b5ef3, 144c1a4, 903e505, 4455fcf, c375913, 56b19e7) or tests-only (5: 4b321fb, 237855e, b3d8824, f806a1f, cf9ce72), and explicitly justifies the tests-only exclusion ("test files not compiled into benchmark binaries → cannot change wall-time"). The production-code culprit set is unchanged — plan body narrative already said "7 code-changing commits", which was correct.

2. **A3 pre-bisect sanity was plan-body prose, not a failure_signal** — evaluator noted that the "wrapper must exit 1 on HEAD, exit 0 on a388f782" check was documented in Task 1's body as "MANDATORY" but was not a tripwire a Coder could not skip. Fix: (a) A3's confidence_why now explicitly states that MEDIUM (not HIGH) is forced by the deferral — plan_check cannot validate the wrapper's exit-code semantics, so the pre-bisect sanity step is A3's sole validator; (b) added a new failure_signal entry "Pre-bisect sanity step skipped" with an ABORT action that makes skipping a hard stop.

No changes to target, scope budget, task breakdown, approach, or prior art. iter-2 is a pure A2/A3 text-surgery rewrite.
