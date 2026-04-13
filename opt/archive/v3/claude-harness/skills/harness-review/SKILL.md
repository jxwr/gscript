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
5. **Documentation Quality Audit** — walks the live doc set and deletes obsolete content instead of letting it accumulate. See section below for the checklist and rules.
6. **Self-Evolution** — applies fixes directly to `opt/`, `docs-internal/`, `scripts/`, `.claude/` (via Bash for .claude/ files)

## Documentation Quality Audit (Step 5 detail)

Run this every review. Stale docs are a drift vector — they mislead ANALYZE into planning against a model of the system that no longer exists.

### What to audit

1. **`docs-internal/architecture/overview.md`** — pipeline order, tier roles, register convention. Cross-check against the actual code: `internal/methodjit/pass_*.go` (pipeline), `internal/methodjit/tier*.go` (tier boundaries), constant files for register names.
2. **`docs-internal/architecture/constraints.md`** — every listed constraint/ceiling must still apply. Delete entries whose underlying code changed. Keep the R# reference that added it so the history is auditable.
3. **`docs-internal/decisions/*.md` (ADRs)** — ADRs are historical, DO NOT delete. But if an ADR was superseded, add a one-line `**Status: superseded by ADR-NNN (R##)**` header.
4. **`docs-internal/known-issues.md`** — remove entries that are fixed (check with `grep` for the referenced symbols / tests). Keep entries still reproducible.
5. **`docs-internal/lessons-learned.md`** — keep, it's append-only history. Do not edit old entries.
6. **`docs-internal/diagnostics/*.md`** — verify referenced commands, flags, file paths still exist. Delete sections that refer to removed tools.
7. **`opt/initiatives/*.md`** — every initiative with `Status: complete` or `Status: abandoned` and last-round > 5 rounds ago → move to `opt/initiatives/archive/`. Active initiatives: verify `Next Step` still matches reality.
8. **`opt/knowledge/*.md`** — knowledge entries should age well, but delete entries whose referenced APIs/files no longer exist.
9. **`CLAUDE.md`** — top-level file references, phase names, role names must match `.claude/prompts/*.md`. A drift here is the worst kind — it's the entry point every new session reads.
10. **`README.md`** (if present) — user-facing commands must still work.

### Rules

- **Delete, don't comment out.** Git preserves the history; dead content in live files is noise.
- **One reason per deletion.** Add a one-line entry to the review's "Documentation Quality" section: `deleted X from Y — reason (R## made it obsolete)`.
- **Never delete ADRs or lessons-learned.** These are historical records. Only add superseded markers.
- **If unsure, flag don't delete.** Put flagged items under `## Docs flagged for user review` in the review output; the user decides.
- **Budget cap**: ≤10 doc edits per review. If more is needed, prioritize top-level (CLAUDE.md, overview, constraints) and defer the rest to next review with a pointer.

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
