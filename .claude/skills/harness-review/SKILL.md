---
name: harness-review
description: Meta-level workflow review. Reads user session log for interventions, audits workflow health, applies structural changes. Runs every round (REVIEW_INTERVAL=1 in early stage).
---

# Harness Review — Workflow Self-Optimization

You review the optimization workflow itself, not the compiler code.
Your output: `opt/reviews/<date>-round<N>.md` with applied changes documented.

## When to Run

- Every round (REVIEW_INTERVAL=1 in early stage; increase to 3-5 once workflow stabilizes)
- When the user feels the workflow is stuck or inefficient
- After a round that went badly (abandoned plan, major drift)

## What It Does

1. **User Intervention Analysis** — reads user's session log, identifies corrections/redirections, classifies each as implemented/partial/pending
2. **Workflow Statistics** — category distribution, outcome distribution, plan accuracy, initiative health, budget adherence, token usage anomalies
3. **Process Understanding** — synthesizes what the workflow does well, what needs fixing, user's implicit priorities
4. **Consistency Audit** — cross-checks all workflow documents for internal consistency (phase names, role descriptions, category taxonomy, pass pipeline, state fields, hook branches, file references, dead content, README, file sizes)
5. **Self-Evolution** — applies fixes directly to `opt/`, `docs-internal/`, `scripts/`, `.claude/` (via Bash for .claude/ files)

## Data Sources

1. **User session JSONL log** — interventions are the #1 signal
2. `opt/INDEX.md` — round history
3. `opt/state.json` — counters
4. `opt/plans/` — archived plans
5. `opt/workflow_log.jsonl` — per-round metrics
6. `opt/initiatives/*.md` — active initiatives
7. `.claude/prompts/*.md`, `.claude/skills/*/SKILL.md`, `.claude/hooks/*.sh` — current workflow config
8. `docs-internal/architecture/` — pipeline, constraints

## Output

`opt/reviews/<date>-round<N>.md` — structured review with intervention log, statistics, consistency audit, self-evolution actions, and evolution tracker.

## Rules

1. **Only recommend changes backed by data.** Cite the user intervention or round failure.
2. **Act, don't just recommend.** Write fixes directly — use Bash for `.claude/` files (Edit/Write blocked by Claude Code).
3. **Don't re-request what's done.** Read current state before proposing changes.
4. **Track previous changes.** Check last review's "Verify" items.
