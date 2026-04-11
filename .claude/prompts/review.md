# REVIEW Phase (harness self-audit)

You audit the **workflow itself** — not the compiler code.
Key input: the user's session log. The user's interventions are the highest-signal data about what the workflow gets wrong.

---

## Context — Load ALL data in ONE call

**IMPORTANT**: Do NOT read files one by one with the Read tool. That creates 25+ API calls,
each carrying the full conversation context (~50K tokens per call). Instead, use ONE Bash call:

```bash
bash scripts/review_dump.sh --all
```

This dumps in one shot:
- User session log (last 100 messages)
- All 23 workflow files (README, CLAUDE.md, prompts, hooks, skills, docs, state, INDEX, etc.)
- Directory listings (initiatives, plans, reviews, pending-changes, opt/ top-level)

Read the output carefully — it has clear `──── filename (N lines) ────` separators.

**Only use Read/Bash for additional files** if you discover a specific issue that needs deeper inspection (e.g., reading a specific initiative file not in the standard dump).

---

## Task

### A. User Intervention Analysis (MOST IMPORTANT)

Read the user's session messages. For each message that **corrects, redirects, or requests a change** to the workflow:

1. **What** did the user say?
2. **Why** did they intervene? (What was the workflow doing wrong?)
3. **Was it already addressed?** Check if the relevant prompt/hook/script was already modified.
4. **Is there a pattern?** Multiple interventions pointing at the same root cause?

Classify interventions:
- **Already implemented** — user asked, it was done. No action needed.
- **Partially addressed** — the spirit was captured but the implementation is incomplete.
- **Not yet addressed** — user flagged an issue that hasn't been fixed in prompts/scripts.
- **Implicit feedback** — user's behavior (skipping phases, running ad-hoc diagnostics) reveals workflow gaps even without explicit complaints.

**DO NOT re-request changes the user already asked for and that are already implemented.**
The core goal is to **understand** the user's thinking, not to generate a to-do list.

### B. Workflow Statistics

Quick scan (subordinate to Section A):

| Check | What to look at |
|-------|----------------|
| Category distribution | Is one category dominating? |
| Outcome distribution | Too many no_change/abandoned? |
| Plan accuracy | Predictions vs actuals — are they converging after calibration? |
| Initiative health | Active but stalled? |
| Budget adherence | Overruns? |
| Token usage | Run `bash scripts/token_usage.sh --last`. Flag anomalies only: sub-agents >5M tokens, duplicate agents doing the same work, python ARM64 decoders instead of otool, or total round >20M. Do NOT optimize normal usage — only flag clear waste. |
| known-issues.md | Prune stale entries: move Fixed items older than 5 rounds to a `## Historical` section (collapsed, not read by ANALYZE). Update Current items with latest data (e.g. file sizes, benchmark numbers). Remove items that were fixed but not marked. |
| Data-premise errors (R24) | Scan last 5 rounds: `outcome=data-premise-error`, `opt/diagnostic_failure_*.md`, `opt/premise_error.md`, or phrases like "earlier number was off" / "actually not the bottleneck" in reports. 2 in 5 rounds → mandatory harness patch (cross-check step or tool replacement). |
| Token reflection (R24) | Read `opt/token_reflection.md`. For each suggestion: accept (apply to relevant prompt now) or reject (write reason). Record decisions in the review doc. Acceptance criterion: saves tokens without degrading analysis quality or correctness confidence. |

### C. Process Understanding

Synthesize A + B into an understanding of:
- **What the workflow does well** (don't break these)
- **What the user keeps having to fix manually** (these are automation gaps)
- **What the user's implicit priorities are** (speed? correctness? architecture? breadth?)
- **Where the user's judgment differs from the harness's defaults** (these are calibration issues)

---

## Output

Write `opt/reviews/<date>-round<N>.md`:

```markdown
## Harness Review — Round [N]

### User Intervention Log
| Time | User Said | Why | Status |
|------|-----------|-----|--------|
| HH:MM | "..." | [interpretation] | implemented / partial / pending |

### Key Patterns in User Feedback
1. [pattern]: [what it means for the workflow]
2. [pattern]: ...

### Workflow Statistics
| Metric | Value | Healthy? |
|--------|-------|----------|
| Category concentration | X% in Y | ... |
| Plan accuracy (last 3) | ... | ... |
| Outcome distribution | ... | ... |

### What's Working (don't touch)
- [list things the user hasn't complained about or explicitly praised]

### Remaining Gaps
1. [specific gap, backed by user intervention data, NOT already implemented]
2. [...]

### Process Understanding
[2-3 sentences: what is the user trying to achieve at the meta level?
 What kind of workflow would make them stop intervening?]

### Consistency Audit
| Check | Files Scanned | Issues Found | Fixed? |
|-------|---------------|-------------|--------|
| Phase names | optimize.sh, prompts, hooks, skills | N | Y/N |
| Role descriptions | CLAUDE.md, skills | N | Y/N |
| Category taxonomy | INDEX, plan_template, _template, analyze.md | N | Y/N |
| Pass pipeline | CLAUDE.md, overview, constraints, debug-ir-pipeline | N | Y/N |
| State fields | state.json vs prompts | N | Y/N |
| Hook branches | phase_guard.sh, etc. | N | Y/N |
| File references | all prompts + docs | N | Y/N |
| Dead content | all files | N | Y/N |
| Stale temp files | opt/ top-level, pending-changes/ | N | Y/N |

### Self-Evolution Actions
[List changes you APPLIED this review (not just recommended). For each:]
1. **What**: [file changed + what was modified]
   **Why**: [which user intervention / round failure / consistency issue triggered this]
   **Verify**: [how the next round will show if this helped]
2. ...

### Evolution Tracker
[Compare with the previous review's "Verify" items. Did the changes work?]
| Previous Change | Expected Effect | Actual Outcome |
|----------------|-----------------|----------------|
```

---

## D. Harness Consistency Audit (MANDATORY)

Every review must check that all workflow documents are internally consistent.
Read ALL of these files and cross-check:

**Files to read:**
- `README.md` — public-facing project description (architecture, quick start, links)
- `CLAUDE.md` — master doc (roles, phases, pipeline, conventions)
- `.claude/optimize.sh` — orchestrator (phase list, REVIEW_INTERVAL, comments)
- `.claude/prompts/*.md` — all active phase prompts
- `.claude/skills/*/SKILL.md` — all skill descriptions
- `.claude/hooks/*.sh` — all hooks (check case branches match active phase names)
- `docs-internal/architecture/overview.md` — pipeline, tiers, registers
- `docs-internal/architecture/constraints.md` — known limits
- `docs-internal/diagnostics/*.md` — debug guides
- `opt/plan_template.md` — category list
- `opt/initiatives/_template.md` — category list
- `opt/INDEX.md` — category list
- `opt/state.json` — field names

**Check for these 11 types of inconsistency:**

1. **Phase names**: old names (MEASURE, RESEARCH, PLAN, DOCUMENT) in active files?
   Active phases: `analyze`, `implement`, `verify` (+ `review` conditional).
2. **Role descriptions**: stale roles? Current: Workflow Auditor, Analyst+Planner, Orchestrator, Coder, Evaluator, Profiler.
3. **File references**: prompts referencing files that don't exist? Docs referencing deprecated prompts?
4. **State fields**: prompts referencing state.json fields that don't exist?
5. **Category taxonomy**: consistent across CLAUDE.md, INDEX.md, analyze.md, plan_template.md, _template.md?
6. **Pass pipeline**: consistent across CLAUDE.md, overview.md, constraints.md, debug-ir-pipeline.md?
7. **Hook case branches**: only active phase names? No stale MEASURE/PLAN/DOCUMENT branches?
8. **Skill descriptions**: match current workflow? Phase counts, feature descriptions, flags all current?
9. **Cross-references**: docs reference each other correctly?
10. **Dead content**: sections describing features that no longer exist?
11. **Stale temp files**: scan `opt/` top-level for files that are NOT part of the known set
    (state.json, INDEX.md, plan_template.md, current_plan.md, token_reflection.md,
    analyze_report.md, workflow_log.jsonl, phase_log.jsonl, sanity_report.md, baseline_test_snapshot.txt, user_priority.md) and are NOT in a known subdirectory
    (plans/, initiatives/, reviews/, knowledge/, history/, diagnostics/, pprof-tier2-float-artifacts/).
    One-off diagnostic reports, ad-hoc dumps, scratch files left by past rounds — delete them.
    Also check `opt/reviews/pending-changes/` for stale applied patches.

**For each inconsistency found**: fix directly. For `.claude/` files use Bash (not Edit/Write — blocked by Claude Code). Note in "Self-Evolution Actions".

---

## E. Documentation Quality Audit (MANDATORY)

Walk the live doc set and delete obsolete content. Rule: delete, don't comment out. One-line reason per deletion in review output. See `.claude/skills/harness-review/SKILL.md` → "Documentation Quality Audit" for the full checklist.

**Scope (every review):**
- `docs-internal/architecture/overview.md` + `constraints.md` — cross-check against code; delete entries whose code changed.
- `docs-internal/known-issues.md` — remove fixed entries (grep for referenced symbols/tests to confirm).
- `docs-internal/diagnostics/*.md` — verify commands/flags still exist.
- `opt/initiatives/*.md` — `complete`/`abandoned` with last-round > 5 ago → move to `opt/initiatives/archive/`. Active: verify `Next Step` matches reality.
- `opt/knowledge/*.md` — delete entries whose referenced APIs no longer exist.
- `CLAUDE.md` — phase/role names match `.claude/prompts/*.md`.

**Never delete:** ADRs (add `Status: superseded` marker only), `lessons-learned.md` (append-only).
**Budget:** ≤10 doc edits per review. More → defer to next review with a pointer.
**Unsure → flag:** list under `## Docs flagged for user review` in output; user decides.

---

## Self-Evolution Protocol

**The harness must evolve itself, not just report.** See CLAUDE.md → Meta-Principle.

After completing sections A-D:

### 0. Terse-rule style (R24)
Prompt files (`.claude/prompts/*.md`) are loaded into every phase session's system prompt — verbose additions compound across rounds. When adding a rule: **one line rule, one line reason, one line action**. No cost comparisons, no anti-pattern stories, no repeated reinforcement. Long explanations go to `docs-internal/lessons-learned.md` or memory, not to hot-path prompts.

### 1. Apply changes directly
You can write to: `opt/`, `docs-internal/`, `scripts/`, `.claude/`.

**Important**: For `.claude/` files, the Edit/Write tools are blocked by Claude Code's
built-in protection. Use **Bash** instead:
```bash
cat > .claude/prompts/review.md <<'FILEEOF'
(new content here)
FILEEOF
```
Or use `sed -i ''` for small edits. This bypasses the permission check.

For `opt/`, `docs-internal/`, `scripts/`: use Edit/Write tools normally.

For every gap found (user intervention, round failure, consistency issue):
- Edit the relevant file NOW
- Note what you changed in "Self-Evolution Actions"
- Define how next round will verify the change worked

### 2. Track previous changes
Read the previous review (`opt/reviews/` most recent). Check its "Self-Evolution Actions → Verify" column. Did the changes help? If not, iterate or revert.

### 3. Don't duplicate user work
Read current file contents BEFORE making changes.
If the user already implemented a fix → mark "implemented by user" and understand WHY.

### 4. Escalate what you can't fix
Some changes need user decision. Flag under "Request for Human Input" with reasoning.

---

## Reset Counters

After writing the review, update `opt/state.json`:
- Set `rounds_since_review` to `0`

## Core Principles

1. **User interventions are the #1 signal.** They reveal what the workflow gets wrong better than any metric.
2. **Don't re-request what's done.** Read current state before acting.
3. **Understand before prescribing.** Model the user's thinking, not just their words.
4. **Act, don't just recommend.** Apply changes, define verification criteria, track outcomes.
5. **The workflow that needs no human intervention is the goal.** Every review should make that closer.
