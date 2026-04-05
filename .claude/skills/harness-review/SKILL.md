---
name: harness-review
description: Meta-level workflow review. Analyzes optimization round data to find workflow bottlenecks and improve the harness itself. Run after every 3 rounds or when workflow feels stuck.
---

# Harness Review — Workflow Self-Optimization

You review the optimization workflow itself, not the compiler code.
Your output: specific, actionable changes to hooks, skills, templates, or process.

## When to Run

- After every 3 completed optimization rounds
- When the user feels the workflow is stuck or inefficient
- After a round that went badly (abandoned plan, major drift)

## Data Sources

Read ALL of these before analysis:

1. **`.claude/workflow_log.jsonl`** — per-round metrics (time per phase, drift events, budget usage, plan accuracy, hook triggers, evaluator findings, outcome, stalls)
2. **`.claude/plans/`** — archived plans (compare predicted vs actual results, check if Prior Art was useful)
3. **`benchmarks/data/history/`** — benchmark trajectory over time
4. **`docs-internal/lessons-learned.md`** — are new detours being captured?
5. **`.claude/state.json`** — any stuck state?

## Analysis Framework

### A. Time Distribution
Where is time being spent across phases?

| Healthy | Unhealthy |
|---------|-----------|
| IMPLEMENT is 50-70% of total | IMPLEMENT is >80% (coding without direction) |
| ANALYZE + PLAN is 15-25% | ANALYZE + PLAN is <5% (rushing to code) |
| VERIFY is 5-15% | VERIFY is >25% (constant regressions) |

If IMPLEMENT dominates: Coder tasks may be too large, or plan was too vague.
If VERIFY dominates: approach is producing regressions, need better ANALYZE.
If ANALYZE+PLAN is too small: agent is skipping the thinking phase.

### B. Plan Accuracy
Compare predicted benchmark improvements vs actual.

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| Predictions consistently optimistic by >2x | ANALYZE is not profiling correctly | Add mandatory pprof in ANALYZE |
| Predictions accurate but improvements small | Correct direction, need bigger architectural moves | Trigger RESEARCH phase |
| Predictions wildly off | Root cause classification is wrong | Review analyze_report quality |

### C. Drift and Budget
How often do budget checks trigger? How often does scope creep happen?

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| Budget exceeded every round | Budgets set too tight, or tasks too loosely scoped | Increase budget or make Coder tasks smaller |
| Budget never approached | Budgets too generous, not constraining | Tighten budgets |
| Scope creep in >50% of rounds | Coder agents not bounded enough | Add explicit "DO NOT TOUCH" lists to Coder prompts |

### D. Evaluator Effectiveness
Are evaluator findings useful or noise?

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| Evaluator always says PASS | Checklist too lenient, or agent gaming it | Tighten checklist, add new checks based on actual bugs |
| Evaluator flags issues that are irrelevant | Checklist items outdated | Remove or update the specific check |
| Evaluator catches real issues | Working as intended | No change |

### E. Hook Value
Which hooks fire? Which never fire?

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| Hook fires every round | Either too sensitive, or agent keeps violating | Check threshold; if agent keeps violating, the constraint may be wrong |
| Hook never fires in 10+ rounds | Either very effective deterrent, or irrelevant | Keep if the cost of violation is high; remove if low-stakes |

### F. Stall Detection
How often do rounds stall (3 stops without progress)?

| Pattern | Diagnosis | Action |
|---------|-----------|--------|
| Frequent stalls | Tasks too hard, or wrong direction | Force RESEARCH before next PLAN |
| Stalls only on specific benchmark categories | Architectural ceiling | Update lessons-learned.md with new ceiling |

## Output Format

```
## Harness Review Report — Round [N] through [M]

### Summary
[2-3 sentences: overall workflow health]

### Metrics
| Metric | Value | Trend | Healthy? |
|--------|-------|-------|----------|
| Avg time in IMPLEMENT | X% | ↑/↓/→ | Y/N |
| Plan accuracy | X% | ↑/↓/→ | Y/N |
| Budget overruns | X/N rounds | ↑/↓/→ | Y/N |
| Evaluator useful findings | X | ↑/↓/→ | Y/N |
| Stalls | X | ↑/↓/→ | Y/N |

### Recommended Changes
1. [Specific change to a specific file, with reasoning]
2. [...]
3. [...]

### No Change Needed
[List things that are working well — don't fix what isn't broken]
```

## Rules

1. **Only recommend changes backed by data.** "Evaluator should be stricter" is not actionable. "Evaluator C3 should check ExecContext init in Execute() because round 4 missed this" is.
2. **Human approves all harness changes.** You propose, user decides.
3. **One change at a time.** Don't rewrite the harness in one review. Pick the highest-impact improvement.
4. **Update lessons-learned.md** if a new workflow pattern was discovered.
5. **Keep this lightweight.** 15 minutes max. Read data, analyze patterns, output report. Don't spawn sub-agents.
