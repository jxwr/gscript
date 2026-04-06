---
name: gs-opt-loop
description: GScript optimization loop. Launches the external orchestrator that runs REVIEW→ANALYZE→IMPLEMENT→VERIFY as independent Claude sessions.
---

# GScript Optimization Loop

When `/gs-opt-loop` is invoked, launch the external orchestrator:

```bash
bash .claude/optimize.sh
```

## What It Does

Each phase runs as an **independent Claude session** — no context accumulation, no drift.
State passes between phases via files in `opt/`:

```
REVIEW    → opt/reviews/<date>.md           (every round, reads user session log)
ANALYZE   → opt/analyze_report.md           (gaps + research + source reading + diagnostics)
            opt/current_plan.md             (concrete plan with tasks + budget)
            opt/knowledge/<topic>.md        (persistent knowledge base updates)
IMPLEMENT → updates opt/current_plan.md     (TDD, scope-bounded Coder sub-agents)
VERIFY    → fills Results section in plan   (tests + benchmarks + evaluator)
            archives plan, updates INDEX.md, initiatives, state.json
```

## Usage

```bash
bash .claude/optimize.sh                   # one full cycle (3 phases)
bash .claude/optimize.sh --rounds=5        # run up to 5 cycles back-to-back
bash .claude/optimize.sh --from=implement  # resume from a specific phase
bash .claude/optimize.sh --review          # force review phase
bash .claude/optimize.sh --no-review       # skip review
bash .claude/optimize.sh --dry-run         # preview phases
```

Multi-round: round 1 honors `--from=`, rounds 2..N start from analyze.

## Cross-Round Infrastructure

- `opt/state.json` — counters: `category_failures`, `rounds_since_review`, `rounds_since_arch_audit`
- `opt/INDEX.md` — flat table of every round. ANALYZE's pattern detector.
- `opt/initiatives/*.md` — multi-round engineering projects.
- `opt/knowledge/*.md` — persistent technique knowledge base.
- `opt/plans/*.md` — archived plans for retrospectives.
- `opt/reviews/*.md` — harness self-audits (with user intervention analysis).
- `opt/workflow_log.jsonl` — per-round metrics.
- `docs-internal/architecture/constraints.md` — known architectural constraints + ceilings.
- `scripts/arch_check.sh` — mechanical architecture scan.

## Key Mechanisms

**Ceiling Rule**: `category_failures >= 2` → category blocked. Prevents grinding.

**Initiative Rule**: Active initiatives with a `Next Step` are preferred over new targets.

**Architecture Audit**: Every 2 rounds, ANALYZE does a thorough code reading + updates `constraints.md`.

**Review**: Every round, REVIEW reads the user's session log to learn from interventions and evolve the workflow.

## Monitoring

```bash
bash .claude/watch-child.sh       # interactive session picker, auto-follows across phases
bash .claude/watch-child.sh --list
bash .claude/watch-child.sh --main  # watch your own conversation
```

## Interrupting and Resuming

If a phase fails:
1. Fix the issue
2. Resume: `bash .claude/optimize.sh --from=<phase>`
3. Valid phases: `analyze`, `implement`, `verify`
