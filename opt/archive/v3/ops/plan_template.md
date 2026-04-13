---
# ══════════════════════════════════════════════════════════════════════════
# HARNESS v3 PLAN SCHEMA — machine-checkable by plan_check
# ══════════════════════════════════════════════════════════════════════════
# Every field below is REQUIRED unless marked (optional).
# plan_check will FAIL any plan with missing required fields.
# See opt/harness-core-principles.md P2 (evidence) and P4 (confidence).

cycle_id: YYYY-MM-DD-short-slug     # e.g. 2026-04-11-object-creation-fix
date: YYYY-MM-DD
category: one of [recursive_call, tier2_float_loop, tier2_correctness, allocation_heavy, gofunction_overhead, field_access, call_ic, regalloc, missing_intrinsic, tier1_dispatch, arch_refactor, other]
initiative: opt/initiatives/X.md   # or "standalone" or "NEW: <name>"

# ═══ Target ═══
# Each target entry is a benchmark the plan claims to move.
# Expected_delta_pct is the PRIMARY metric. Negative = faster (good).
# Confidence MUST be HIGH / MEDIUM / LOW with justification.
target:
  - benchmark: object_creation       # must match a key in benchmarks/data/reference.json
    current_jit_s: 1.053             # from benchmarks/data/latest.json
    reference_jit_s: 0.764           # from benchmarks/data/reference.json
    expected_jit_s: 0.800            # post-round prediction (numeric, no "~" or "around")
    expected_delta_pct: -24.0        # (expected - current) / current * 100
    confidence: MEDIUM               # HIGH/MEDIUM/LOW
    confidence_why: "Bisect + source inspection identified specific regression commit; fix is a revert. MEDIUM because side effects on other benchmarks are unverified."

# ═══ Scope budget (machine-enforced) ═══
max_files: 3                         # sanity R6 hard cap; excess = scope-violation
max_source_loc: 200                  # excludes *_test.go files per R22 review
max_commits: 2                       # excess = scope-violation

# ═══ Assumptions — THE KEY NEW SECTION ═══
# Each assumption MUST cite evidence. plan_check verifies every entry.
# Assumption types:
#   cited-evidence: evidence is a file:line or a named diagnostic output
#   derivable-from-code: a Reader can open the file and confirm by reading
#   requires-live-run: needs a live diagnostic command (plan_check runs it)
#   unverifiable: explicitly flagged; plan_check will REJECT the plan
assumptions:
  - id: A1
    claim: "object_creation's hot path is the Closure allocation in OP_CLOSURE at emit_closure.go:142"
    type: cited-evidence
    evidence: "opt/authoritative-context.json#targets[object_creation].hot_function"
    confidence: HIGH
    source: "CONTEXT_GATHER Diagnose() output from compileTier2() run"
  - id: A2
    claim: "The regression was introduced between commits f806a1f and 56b19e7"
    type: requires-live-run
    evidence: "git bisect run `bash benchmarks/run_all.sh --runs=3 -- object_creation`"
    confidence: MEDIUM
    source: "Bisect will be performed by Coder in Task 1"

# ═══ Prior art (from Step 2 Research sub-agent) ═══
prior_art:
  - system: V8
    reference: "v8/src/runtime/runtime-object.cc:NewObject"
    applicability: "Similar allocation fast path. V8 uses pre-allocated property backing store."
    citation: "/tmp/research-cache/v8/src/runtime/runtime-object.cc:line"
  - system: LuaJIT
    reference: "n/a"
    applicability: "LuaJIT trace JIT handles this via on-trace allocation sinking; not directly portable to method JIT."
    citation: "Reference only, not a template"

# ═══ Failure signals (specific, measurable) ═══
failure_signals:
  - condition: "object_creation delta > -10% at VERIFY (predicted ~-24%)"
    action: "pivot — investigate secondary bottleneck"
  - condition: "any other benchmark regresses > 3% at reference level"
    action: "revert, root-cause the cross-contamination"
  - condition: "TestDeepRecursionRegression or TestQuicksortSmall fail"
    action: "hard revert (correctness gate)"

# ═══ Tripwire-relevant self-assessment ═══
self_assessment:
  uses_profileTier2Func: false          # P3 violation if true
  uses_hand_constructed_ir_in_tests: false   # P3 / R32 violation if true
  authoritative_context_consumed: true  # P3 requirement
  all_predictions_have_confidence: true # P4 requirement
  all_claims_cite_sources: true         # P1 requirement

---

# Optimization Plan: [Human-readable title]

## Overview
One paragraph: what bottleneck, what fix, what we expect.

## Root Cause Analysis
Derived from `opt/authoritative-context.json`. Cite assumption IDs (A1, A2, …) from the frontmatter. Do NOT introduce new claims here that are not in the Assumptions section.

## Approach
Concrete implementation plan. What changes, in what files. Each file must be listed in the frontmatter `max_files` count.

## Task Breakdown
Each task = one Coder sub-agent invocation. Follow the 1-Coder rule (R27).

- [ ] Task 0 (optional, pre-flight by orchestrator) — any infra commits needed before Coder runs
- [ ] Task 1 — [description] — files: `X.go`, `X_test.go` — test: `TestY` — must include production-pipeline diagnostic test per R32

## Integration Test
Mandatory if plan touches tiering policy, Tier 2 promotion, or self-call emission.

```bash
go build -o /tmp/gscript_r<N> ./cmd/gscript/
timeout 60s /tmp/gscript_r<N> -jit benchmarks/suite/object_creation.gs
```

## Results (filled after VERIFY)
| Benchmark | Reference | Before | After | Change | Expected | Met? |
|-----------|-----------|--------|-------|--------|----------|------|

Also record:
- Cumulative drift vs reference.json for all non-excluded benchmarks
- Prediction vs actual entry for prediction_ledger.jsonl (Stage 2+)

## Lessons (filled after completion/abandonment)
What worked, what didn't. Specifically: were the Assumptions correct? Any that needed revising?

## Plan Check Feedback (populated by plan_check on rewrite cycles only)
If plan_check flagged an assumption, the feedback is inserted here and ANALYZE must rewrite the plan to address it. Max 2 rewrite cycles per S1.3 tripwire T3.
