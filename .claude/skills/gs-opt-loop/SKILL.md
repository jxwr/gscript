---
name: gs-opt-loop
description: GScript optimization loop. Launches the external orchestrator that runs REVIEW→MEASURE→ANALYZE→RESEARCH→PLAN→IMPLEMENT→VERIFY→DOCUMENT as independent Claude sessions.
---

# GScript Optimization Loop

When `/gs-opt-loop` is invoked, launch the external orchestrator:

```bash
bash .claude/optimize.sh
```

## What It Does

Each phase runs as an **independent Claude session** — no context accumulation, no drift.
State passes between phases via files:

```
REVIEW    → opt/reviews/<date>.md       (every 5 rounds, harness audit)
MEASURE   → opt/measure_report.md       (+ history snapshot + ASCII trajectory)
ANALYZE   → opt/analyze_report.md       (category + initiative + research_depth)
RESEARCH  → opt/research_report.md      (conditional: if research_depth=deep)
PLAN      → opt/current_plan.md         (auto-approved to IMPLEMENT)
IMPLEMENT → updates opt/current_plan.md (TDD, scope-bounded coders)
VERIFY    → fills Results section       (tests + benchmark diff + evaluator)
DOCUMENT  → archives plan, updates INDEX.md, initiatives, state.json
```

## Usage

```bash
bash .claude/optimize.sh                # full cycle
bash .claude/optimize.sh --from=analyze # resume from a specific phase
bash .claude/optimize.sh --review       # force review phase now
bash .claude/optimize.sh --no-review    # skip review even if due
bash .claude/optimize.sh --dry-run      # preview phases
```

## Cross-Round Infrastructure

**Files the agent reads across rounds:**

- `opt/INDEX.md` — flat table of every round (one line each). ANALYZE's pattern detector.
- `opt/initiatives/*.md` — multi-round engineering projects. ANALYZE prefers continuing them.
- `opt/state.json` — counters: `category_failures`, `rounds_since_review`, `rounds_since_research`.
- `opt/plans/*.md` — archived plans for deep dives / retrospectives.
- `opt/reviews/*.md` — harness self-audits.
- `opt/workflow_log.jsonl` — per-round metrics.
- `benchmarks/data/history/*.json` — daily benchmark snapshots.

## Ceiling Rule (Anti-Stall)

Categories with `category_failures >= 2` are **forbidden** as targets. ANALYZE must pick a different category or continue an active initiative in a different category. This prevents grinding on the same wall for 10 rounds.

## Initiative Rule (Multi-Round Architecture)

Big changes (Tier 2 native BLR, variadic IR model, escape analysis) span many rounds. Track them in `opt/initiatives/*.md`. ANALYZE prefers advancing active initiatives over opportunistic new targets.

## Research Depth

ANALYZE emits `research_depth: shallow|deep`. If `deep`, the RESEARCH phase runs between ANALYZE and PLAN — reads V8/JSC source directly for technique-level prior art, writes `opt/research_report.md`. PLAN uses it instead of shallow web-search.

## Review Cadence

Every 5 rounds, the REVIEW phase runs before MEASURE:
- Audits category distribution, outcome distribution, budget overruns
- Checks initiative health
- Recommends harness changes if patterns emerge
- Resets `rounds_since_review` counter

## Monitoring

Watch child session in real time:
```bash
bash .claude/watch-child.sh       # most recent child session
bash .claude/watch-child.sh --list
```

## Interrupting and Resuming

If a phase fails or is interrupted:
1. Fix the issue (edit files, run commands)
2. Re-run from the failed phase: `bash .claude/optimize.sh --from=<phase>`
3. The orchestrator checks that the previous phase's output exists before starting
