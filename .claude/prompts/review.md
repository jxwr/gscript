# REVIEW Phase (harness self-audit)

You are in the REVIEW phase of the GScript optimization loop.
This phase runs every **5 rounds** to audit the harness itself, not the compiler code.

## Context

Read these files:
1. `opt/INDEX.md` — flat round history
2. `opt/state.json` — current counters (category_failures, rounds_since_*)
3. `opt/plans/` — archived plans from all rounds (sample the last 5)
4. `opt/workflow_log.jsonl` — per-round metrics
5. `opt/initiatives/*.md` — active initiatives
6. `docs-internal/lessons-learned.md` — project-level lessons

## Task

Audit the workflow itself for these patterns:

### A. Category Distribution

Count rounds by category. Are we stuck on one? Are some categories untouched?

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| >3 rounds same category, all failed | Ceiling hit | Force ≥2 round pause from this category |
| 1 category dominates >50% of all rounds | Blind spot | Spawn initiative in a different category |
| category_failures uncleared after `improved` | DOCUMENT bug | Fix DOCUMENT to reset counter on improved |

### B. Outcome Distribution

Count rounds by outcome (improved / no_change / abandoned / regressed).

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| >40% abandoned in recent 10 | Over-optimism in PLAN | Tighten failure-signal definitions; require more TDD in Task 1 |
| >60% no_change | Hitting the noise floor | Broader architectural moves needed — propose new initiatives |
| improved but tiny deltas | Local-optimum trap | Force one architectural round per 5 local rounds |

### C. Budget Overruns

Count rounds that exceeded commit/file budget.

| Pattern | Action |
|---------|--------|
| >50% overrun | Budgets too tight OR tasks too loose — retarget |
| Systematic 1-commit overrun | Add a "cleanup commit" slot to the template |

### D. Plan-Actual Gap

For each recent round, compare plan's "Expected Effect" to actual Results.

| Pattern | Action |
|---------|--------|
| Predictions consistently off by >3x | ANALYZE profiling is weak — require pprof before plan |
| Predictions accurate but deltas tiny | Target selection is sub-optimal — look elsewhere |

### E. Initiatives Health

For each active initiative: is it advancing? When was it last touched?

| Pattern | Action |
|---------|--------|
| Active but untouched for 3 rounds | Stall — either abandon or re-prioritize |
| Completed but no follow-up initiative | Success without continuation — propose next move |

### F. Workflow Friction

Where do phases fail or loop?

- Hook blocks that fired and confused the agent
- Phases re-run `--from=` more than once
- Patterns where the orchestrator itself exits 1

## Output

Write `opt/reviews/<date>-round<N>.md`:

```markdown
## Harness Review — Rounds [start-id] .. [end-id]

### Summary
[2-3 sentences: overall workflow health trajectory]

### Category Distribution (last 5 rounds)
| Category | Count | Outcomes |
|----------|-------|----------|

### Outcome Distribution (last 5 rounds)
| Outcome | Count |
|---------|-------|

### Initiatives State
| Initiative | Status | Last Round | Stalled? |
|------------|--------|-----------|----------|

### Concrete Recommendations

1. [Specific file change with reasoning based on data]
2. [...]

### Nothing to Change
[What's working that should stay]

### Request for Human Input
[Only if genuinely needed — strategic direction changes]
```

## Reset Counters

After writing the review, update `opt/state.json`:
- Set `rounds_since_review` to `0`

## Restrictions

- Only recommend changes **backed by data from the files read**.
- No speculative changes.
- Budget: 1 round = this review only, no code changes.
- If recommendations involve harness-file changes, apply them directly to `.claude/` (you have Write access).
- Do NOT spawn sub-agents. Read, analyze, recommend.
