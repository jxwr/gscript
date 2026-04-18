# GScript

A dynamically-typed scripting language with a V8-style multi-tier JIT targeting ARM64, implemented in Go. Architecture: **interpreter → baseline JIT (Tier 1) → optimizing JIT (Tier 2)**.

## Mission

**Surpass LuaJIT on wall-time for every benchmark.** No "done," only the next milestone.

## Hard rules

These are compressions of expensive lessons. Each is present-tense, grep-able, or test-expressible. They are not optional.

### Correctness
1. **TDD is mandatory.** Failing test first, then minimum code to pass, then refactor.
2. **Correctness first.** A wrong-but-fast compiler poisons every subsequent comparison.
3. **Never stack on unverified code.** All tests must pass before landing the next change.

### Diagnostics and measurement
4. **Profile before optimizing.** JIT code is opaque to `pprof` — use ARM64 disasm (`Diagnose()`), not Go profilers.
5. **Only `compileTier2()` / `RunTier2Pipeline` / `TieringManager.CompileForDiagnostics` produce authoritative Tier 2 evidence.** Parallel pipelines are banned — they drift from production.
6. **Median-of-N for every benchmark comparison.** Default `--runs=5` publish, `--runs=3` mid-round.
7. **Contradicted diagnostic data halts the round.** Root-cause the tool first; "number off but conclusion holds" is never valid.
8. **IR instruction-count savings do not imply wall-time savings.** M4 is 6–8-wide superscalar; removed guards are often free. Always validate with benchmarks.

### Architecture
9. **Architecture-first target selection.** Every round asks in order: (Q1) global architecture? (Q2) module boundary? (Q3) local pass/emit? Only Q3 proceeds without user discussion.
10. **Multiple regressions = architecture problem**, not implementation bug. Don't patch in one place.
11. **V8's architectural choices are the default.** Deviate only with explicit evidence.
12. **Tier 2 is not always faster than Tier 1.** BLR + SSA setup cost more than inlining gains for call-dominated code.

### Code hygiene
13. **No Go file exceeds 1000 lines.** Split at 800. Enforced by `.claude/hooks/file_size_guard.sh`.
14. **One concern per file.** Each file opens with a doc comment.
15. **Test files mirror source:** `foo.go` → `foo_test.go`.
16. **Commit per task.** Each working step is its own commit.

### Workflow (v5.1)
17. **REMOVED by R49.** (was: "One Claude session per round"). A round is a hypothesis-closure unit, not a time/session unit. Multi-round sessions are fine; single hypotheses may span sessions. Closure is mechanical: card + outcome + ledger update.
18. **REMOVED by R49.** (was: "Three-hour round budget"). Never fired in 48 rounds; rounds genuinely vary from 5min (meta) to multi-hour (IR pass). Rule 20 (no sunk cost) is the real brake.
19. **Only mechanical signals gate a round:** `reference.json` drift, `kb_check.sh` freshness, test failure, scope-budget breach, **missing/stale `rounds/NNN.yaml`**.
20. **Sunk cost is never a reason to keep broken code.**
21. **Hypothesis-class lookup is mandatory at Step 3.** Grep `program/ledger.yaml`. If `prior_reject_rate > 0.5` and `attempts >= 3`, write a `mitigation_description` in the round card or flip the round type to `strategy`. **Post-R49:** `prior_reject_rate` counts only rounds with `shipped_code: true` (`reverts / (bench_wins + reverts + shipped_no_wins)`). ADR-wins, meta-wins, and no-code halts do not dilute the denominator.
22. **Round-closing commits use the schema:** `round N [bench-win|adr-win|revert|no-win]: <one-liner>`. The closing commit is derivable by `git log --grep='^round N \['` — the card's `commit:` field is optional metadata, not load-bearing. (Pre-R49 commits used the legacy schema `[win|revert|hold|diag|KB|meta]`; grep both when scanning history.)
23. **Architecture rounds include a current-state audit.** Every `type: architecture` round MUST open with a "current state" section that produces at least one concrete production measurement disproving the null hypothesis "this is already done." R21 overscoped 40% gains based on an unverified assumption about typespec; R24's 30-minute feedback dump disproved it after R23's wasted implementation attempt. The audit prevents the R21→R23→R24 churn from recurring in any class.

    **Amendment (from R32)**: for tier-2 bench-optimization rounds, the audit MUST include reading the post-pipeline IR (`diag/<bench>/<hot_proto>.ir.txt`), not just the emit source code. R29 read source paths but missed that LICM (`pass_licm.go:237+`) already hoists the GetFields R29 proposed to hoist. A 5-minute IR read would have caught the overlap.

24. **Outcome taxonomy (from R49).** Every round closes with `outcome.status ∈ {bench-win, adr-win, revert, no-win}` plus orthogonal `outcome.shipped_code: bool`.
    - **bench-win**: a target benchmark measurably moved in the intended direction (verified post-round, median-of-N).
    - **adr-win**: the round's deliverable was an ADR, meta-rule, KB card, or diagnostic finding — shipped without expectation of immediate bench movement.
    - **revert**: code was committed then backed out in the same round.
    - **no-win**: the hypothesis was tested and failed to move any benchmark. `shipped_code` distinguishes "infrastructure left in tree" (true) from "pre-flight halt, no code" (false).

25. **Verify appends to `program/luajit_gap.yaml` (from R49).** On any round that re-runs a benchmark, Step 6 appends one row per measured benchmark: `{round, date, bench, gscript_jit_sec, luajit_sec, gap_factor}`. This is the cross-round per-benchmark trajectory view (ledger.yaml is per-hypothesis-class; luajit_gap.yaml is per-benchmark).

### Correctness rounds (v5.2)

Rules 26-29 apply when `class = correctness-bug-fix` AND the round modifies `internal/methodjit/emit_*.go`, `internal/methodjit/pass_*.go`, `internal/methodjit/regalloc*.go`, or similar codegen infrastructure. Trivial fixes (e.g. a missing switch case in a lookup table) are exempt when the change is ≤5 lines and contained in one function.

26. **Test-first for correctness rounds.** Step 5 (Act) cannot modify production code until Step 4 (Pre-flight) has written a failing Go test that:
    - lives under `internal/methodjit/` (or equivalent), not in `/tmp/*.gs`
    - fails on the current binary with a concrete assertion (not a flaky bench comparison)
    - runs in <5 seconds locally
    - the round card's `pre_flight.required` lists the test function name and the current failing output

    Rationale (from R65-R75): 6 of 7 arc attempts jumped straight to production edits. None had a fast red→green signal. Every revert took a full bench run (minutes) to discover the regression.

27. **Surgical scope: one path per round.** A single correctness round may modify at most ONE emit helper OR ONE pass function OR ONE regalloc decision point. "Centralize the fix in `storeResultNB`" (R65) or "Apply the change to all float store paths" (R66) violate this and must be split into N rounds.

    Rationale: codegen state (regalloc, cross-block live, Phi moves) is mutually coupled; a change that looks local may interact with siblings. Surgical scope keeps the blast radius small enough to debug.

28. **IR-diff or asm-diff evidence required.** For any round modifying a pass or emit helper, the round card's `current_state_audit_summary` MUST quote the relevant lines of IR (or emitted asm bytes) from before and after the change. Use `Pipeline.EnableDump()` in a test or `scripts/diag.sh` + `git diff diag/<bench>/<proto>.ir.txt`.

    A round without quoted IR diff is treated as rule 19 "missing mechanical evidence" and the round does not close.

29. **Bisect range is a required field for correctness rounds.** When the bug reproduces at parameter `P` but not `P'`, the round card's `class_gate.current_state_audit_summary` (or a dedicated `bisect_range:` field) MUST record the narrowest found range (e.g. "fails at N≥11, passes at N≤10, seed=42"). Bisection continues until the range is one of: single input size, single IR-op shape, single code path.

    Rationale (from R75): the N≥11-but-not-N=17 result signalled a non-obvious trigger condition that was never properly bisected. The round closed with the question open.

## Round shape (v5)

A round is a single session with seven internal steps. No orchestrator.

```
0. Recap         Read last 8 rounds/*.yaml + program/ledger.yaml +
                  last 5 revert autopsies. Identify class patterns.
1. Diag          scripts/diag.sh all                 → diag/
2. KB check      scripts/kb_index.sh                 → kb/index/
                  scripts/kb_check.sh                 (blocks on staleness)
3. Direction     Read: program/ledger.yaml + all kb/modules/*.md (28 cards)
                       + diag/summary.md + 3-5 relevant diag/<bench>/
                       + last 20 rounds/*.yaml + last 5 revert autopsies
                  Write: rounds/NNN.yaml with identity, type, target,
                  hypothesis (class + claim + expected_gain_pct +
                  expected_gain_mechanism + counterfactual_check),
                  class_gate (ledger_consulted=true, prior_reject_rate,
                  mitigation if required).
                  Q1 → Q2 → Q3 priority; only Q3 autonomous.
4. Pre-flight    (Wave 3; optional in Wave 1/2.) Microbench or
                  Diagnose-oracle confirming predicted cost shape.
                  For correctness-bug-fix rounds (rule 26): write a
                  FAILING Go test BEFORE Step 5; list its name in
                  the card's pre_flight.required.
5. Act           TDD, bounded by round card scope. For correctness
                  rounds (rules 27-28): single emit/pass/regalloc
                  path only; capture IR/asm diff.
6. Verify        Re-run diag + median-of-N bench. Pass or revert.
                  Append one row per measured benchmark to
                  program/luajit_gap.yaml (rule 25).
7. Close         Fill round card outcome (rule 24) + revert fields
                  if applicable. Update program/ledger.yaml (append
                  to classes_touched). Commit with schema:
                  round N [bench-win|adr-win|revert|no-win]: <one-liner>.
                  Separate KB update commit if card semantics changed.
```

## Directory pointers

| Path | Purpose |
|------|---------|
| `CLAUDE.md` | This file — hard rules + round shape |
| `rounds/` | **v5.** Per-round structured cards. `TEMPLATE.yaml` is the schema. |
| `program/ledger.yaml` | **v5.** Hypothesis classes + priors, aggregated across rounds. |
| `program/luajit_gap.yaml` | **v5.1 (R49).** Per-benchmark append-only gap log; one row per verified bench per round. |
| `docs-internal/workflow-v5-plan.md` | v5 design, three pillars, wave criteria |
| `docs-internal/decisions/adr-workflow-v5.md` | v4→v5 transition ADR |
| `docs-internal/architecture/overview.md` | Tiers, pipeline, register convention |
| `docs-internal/architecture/constraints.md` | Mechanical architectural constraints |
| `docs-internal/decisions/` | ADRs |
| `docs-internal/diagnostics/` | `Diagnose()`, IR pipeline, deopt debugging |
| `docs-internal/known-issues.md` | Current known bugs |
| `docs-internal/archive/` | Dead workflow docs (v3, v4). Do not read. |
| `docs/` | Blog journal — permanent record of the exploration |
| `kb/architecture.md` | Top-level invariants |
| `kb/modules/` | 28 module cards (schema-enforced) |
| `kb/index/` | L1 mechanical symbol index (auto-generated) |
| `scripts/diag.sh` | Production-parity diagnostic dump |
| `scripts/kb_index.sh` | Regenerate L1 index |
| `scripts/kb_check.sh` | Validate L2 cards against L1 + git blob SHAs |
| `scripts/round.sh` | Mechanical prep for Steps 1–2; direction gate at Step 4 |
| `benchmarks/data/reference.json` | Frozen baseline — never rotates |
| `opt/archive/v3/` | Dead v3 harness state. Do not read. |

## Doc-sync rule

See `.claude/rules/doc-sync.md`. On any architectural decision, update the relevant KB card or ADR in the same session. Never leave docs drifted.

## Memory hygiene

The round card + ledger ARE the cross-round memory. They supersede prose notes. When the round card and a prose doc disagree, the round card wins. `docs-internal/round-direction.md` is deprecated — do not read or write it.
