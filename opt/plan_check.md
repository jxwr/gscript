# Plan Check — 2026-04-12-object-creation-regression-bisect

**Iteration**: 2
**Verdict**: PASS

<evaluation>PASS</evaluation>

## Self-assessment audit

| field | value | audit |
|-------|-------|-------|
| uses_profileTier2Func | false | PASS (P3 honored) |
| uses_hand_constructed_ir_in_tests | false | PASS (R32 rule honored) |
| authoritative_context_consumed | true | PASS (A1/A4/Task 0 all cite opt/authoritative-context.json) |
| all_predictions_have_confidence | true | PASS (every assumption + target entry has explicit HIGH/MEDIUM/LOW) |
| all_claims_cite_sources | true | PASS (every assumption has a `source:` field) |

## Target audit

| bench | ref_jit_s (plan) | ref_jit_s (reference.json) | current_jit_s (plan) | latest_jit_s (latest.json) | expected_delta_pct | confidence |
|-------|-----|-----|-----|-----|-----|-----|
| object_creation | 0.764 | 0.764 | 1.152 | 1.152 | 0.0% | HIGH |

- `reference_jit_s` matches `benchmarks/data/reference.json` exact.
- `current_jit_s` matches `benchmarks/data/latest.json` exact.
- `expected_delta_pct: 0.0` is mathematically consistent (diagnostic round writes no production .go code — wall-time cannot change by construction).
- `confidence: HIGH` with `confidence_why` citing R29 precedent (opt/knowledge/r29-fib-root-cause.md, outcome=no_change round). P1 satisfied.

No target issues.

## Assumption verification

| id | claim (short) | type | verdict | evidence_match | feedback |
|----|---------------|------|---------|----------------|----------|
| A1 | object_creation drifted +50.79% (0.764→1.152) between a388f782 and b3d8824 | cited-evidence | verified | exact | (none) |
| A2 | Culprit is one of 7 production-code commits (12 commits total in filter range, 5 excluded as tests-only) | cited-evidence | verified | exact | (none) |
| A3 | bisect_object_creation.sh witness wrapper is deterministic at 5% threshold | requires-live-run (deferred) | verified | deferred | (none — deferral justified, failure_signal provides hard guardrail) |
| A4 | 39b5ef3 is highest-probability culprit (Shape/table/GC-scan rewrite, SetField-heavy target) | cited-evidence | verified | approximate | (none — plan honestly labels MEDIUM, bisect will decide) |
| A5 | Pure-diagnostic round with no .go code change is a valid harness-v3 shape (R29 precedent) | derivable-from-code | verified | exact | (none) |
| A6 | Task 0 fixture must use real compileTier2() via Diagnose()/TieringManager, not profileTier2Func | derivable-from-code | verified | exact | (none) |

### Per-assumption notes

**A1** — `opt/authoritative-context.json#drift_table[0]` contains `{"benchmark": "object_creation", "ref_jit_s": 0.764, "latest_jit_s": 1.152, "drift_pct": 50.79}`. Exact match to plan claim.

**A2** — Re-ran `git log --format='%h %s' a388f782..HEAD -- internal/methodjit internal/vm internal/compiler internal/runtime`. Returned exactly 12 commits. Spot-checked the 5 tests-only commits with `git show --stat <sha> -- internal/methodjit internal/vm internal/compiler internal/runtime`:

- `b3d8824`: only `internal/methodjit/pass_scalar_promote_production_test.go` (139 lines) under filter. Test-only confirmed.
- `cf9ce72`: only `internal/methodjit/tier2_float_profile_test.go` (+6 lines) under filter. Test-only confirmed.
- `f806a1f`: only `internal/methodjit/r32_nbody_loop_carried_test.go` (+254 lines) under filter. Test-only confirmed.
- `4b321fb`: three `_test.go` files under filter (main_test, offset_check_test, quicksort_asm_test). Test-only confirmed.
- `237855e`: only `internal/methodjit/tier1_fib_dump_test.go` (+76 lines) under filter. Test-only confirmed.

Plan's 7-commit production culprit set (`598bc1e, 39b5ef3, 144c1a4, 903e505, 4455fcf, c375913, 56b19e7`) is correct. Note: `903e505` / `4455fcf` are the R30-apply-then-revert pair; both are production-touching and both are correctly included — bisect will skip over them cleanly since they cancel out. Evidence exact.

**A3** — Plan explicitly marks this `requires-live-run` and defers verification to Task 1's pre-bisect sanity step, which is a non-negotiable tripwire promoted to `failure_signals[5]` in iter-2. Deferral is acceptable under the plan_check rule ("Budget note: ... mark assumptions `requires-live-run (deferred)`..."). The deferral is honest — `confidence_why` states "plan_check has NO way to verify the wrapper's exit-code semantics until Task 1 builds it". P4 satisfied. The failure_signal provides the guardrail that plan_check cannot: if Task 1 skips the sanity step, the round aborts.

**A4** — `git show --stat 39b5ef3` confirms the commit rewrites `runtime/shape.go`, splits `table.go`/`table_int.go` (+568 lines), and adds scan-all-regs in `vm/vm.go:ScanGCRoots`. Commit message title: "fix: Shape system, GC scan all regs, split table.go, fix empty-loop test". Claim that it rewrites "Shape/SetField lowering paths" is approximately accurate — shape.go is the hidden-class machinery that SetField uses for field indexing, and scan-all-regs directly widens GC work per object allocation. A4's confidence is appropriately kept at MEDIUM because "plausible fit ≠ caused"; bisect is the arbiter. Honest.

**A5** — `opt/INDEX.md` row 29: `"Root-cause fib +988% from 598bc1e; add fib Tier 1 insn-count fixture (baseline 635)"` with `outcome: no_change`. R29 is a valid precedent for a diagnostic round that writes a fixture + knowledge doc + no production .go code. Exact match.

**A6** — P3 in `opt/harness-core-principles.md` forbids `profileTier2Func` as evidence; permanent anti-pattern #1 reinforces it. The plan's Task 0 explicitly requires `TieringManager.TryCompile()` + `Diagnose()` and enumerates this in the "Implementation constraints" block. Claim is a direct restatement of existing harness rules. Exact match.

## Reachability audit (Step 4.5)

No assumption proposes a `file:line` fix point. This is a diagnostic round — the intervention (R36) is deliberately deferred, so there is no "the fix goes here" claim to reachability-check. A4's "highest-probability culprit" is an investigation hypothesis, not an intervention site.

Task 0's insn-count fixture does name specific code paths (hot functions `create_and_sum`, `transform_chain`, `new_vec3` in `benchmarks/suite/object_creation.gs`). Reachability is satisfied by plan constraint: Task 0 MUST run `TieringManager.TryCompile()` on the real `.gs` source, so the fixture exercises the production path by construction. `opt/authoritative-context.json#candidates[object_creation]` already captured 208/813/988 insn counts from the same production pipeline, so the fixture targets are proven-reachable.

Reachability: PASS.

## Scope audit

| limit | plan value | Task 0 spend | Task 1 spend | headroom |
|-------|-----------|--------------|--------------|----------|
| max_files | 4 | 1 (object_creation_dump_test.go) | 2 (bisect_object_creation.sh + r35-object-creation-regression.md) | 1 for analyze bookkeeping |
| max_source_loc | 200 (excludes *_test.go) | 0 (test file excluded) | ≤50 (shell script) + 0 (knowledge .md excluded) | ~150 LOC unused |
| max_commits | 2 | 1 (Task 0 fixture) | 1 (Task 1 bisect + doc) | 0 |

Budget is comfortable. Diagnostic-round shape means `.go` production LOC is 0 by contract, with failure_signal "Any production .go file ends up modified by Task 1 → hard revert". No `scope_too_tight` flag.

## Live runs performed

1. `git log --format='%h %s' a388f782..HEAD -- internal/methodjit internal/vm internal/compiler internal/runtime` — returned 12 commits exactly matching A2's enumeration (A2 verification).
2. `git show --stat b3d8824 cf9ce72 f806a1f 4b321fb 237855e -- internal/methodjit internal/vm internal/compiler internal/runtime` + `git show --stat 39b5ef3` — confirmed 5 tests-only classifications and verified 39b5ef3 touches Shape/table/vm.go/ScanGCRoots (A2 + A4 verification).

Both runs batched; 2 live commands used out of 2 allowed.

<feedback>
(none — verdict is PASS)
</feedback>

## Iteration decision

Plan verified. Proceed to IMPLEMENT.

**Notes for IMPLEMENT**:
1. Task 1's pre-bisect sanity step (failure_signal #5) is A3's sole validator. The Coder MUST run the wrapper on HEAD (expect exit 1) and on a388f782 (expect exit 0) before `git bisect run`. Skipping this is a hard ABORT per plan.
2. The strict scope boundaries in both tasks ("Do NOT modify any `internal/` directory .go file", "Do NOT modify reference.json/latest.json/baseline.json") are enforced by failure_signal #4 — any .go edit by Task 1 triggers hard revert.
3. Task 0's fixture values (208/813/988) must be validated against `opt/authoritative-context.json#candidates[object_creation].disasm_summary`; mismatch triggers failure_signal #3 (STOP — CONTEXT_GATHER was non-deterministic).
