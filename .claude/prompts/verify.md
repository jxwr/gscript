# VERIFY + DOCUMENT Phase

You are in the final phase of an optimization round.
Your job: verify the implementation (tests + benchmarks + evaluator), then close out the round (update all cross-round state).

## Context — Load ALL data in ONE call

```bash
bash scripts/verify_dump.sh
```

This dumps: current_plan.md, baseline.json, state.json, INDEX.md, workflow_log,
overview.md, constraints.md, docs/index.html, plus git diff stat.

CLAUDE.md is already loaded as project instructions — do NOT read it again.

---

## Part 1: VERIFY

### 1a. Run tests
```
go test ./internal/methodjit/... -short -count=1 -timeout 120s
go test ./internal/vm/... -short -count=1 -timeout 120s
```
If tests fail: **fix first**. Correctness before performance.
**JIT stack crash protocol**: on first SIGSEGV/SIGBUS in JIT code, immediately run `git stash && go test -run <failing_test> -timeout 10s; git stash pop` to confirm pre-existing. Do NOT investigate further — log in known-issues and continue.

### 1b. Run benchmarks
```
bash benchmarks/run_all.sh
```

### 1c. Compare vs baseline
Build before/after table. Flag regressions ≥5%.

### 1d. Evaluator
Spawn an Evaluator sub-agent (**use Sonnet model** to reduce token cost) to review the git diff:
- Correctness risks, scope creep, code quality, missed edge cases
- Output: pass/fail with specific issues
- If fail with actionable issues → fix and re-verify

### 1e. Fill Results in `opt/current_plan.md`

```markdown
## Results (filled by VERIFY)
| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|

### Test Status
- [X passing, Y failing]

### Evaluator Findings
- [pass/fail + notes]

### Regressions (≥5%)
- [list or "none"]
```

### 1f. Determine outcome
- Tests pass + target improved + evaluator pass → `improved`
- Tests pass + target unchanged → `no_change`
- Target regressed or unrelated ≥10% regressed → `regressed`
- Tests broken beyond budget → `abandoned`
- `premise_error.md` exists OR predictions inverted >2× OR Step-4 numbers unreproducible → `data-premise-error`

`data-premise-error` handling (R24): don't retry plan. Root-cause the measurement tool, write `opt/diagnostic_failure_<cycle_id>.md`, fix the tool in a `diagnostic-fix:` commit, set `previous_rounds[].outcome=data-premise-error` with the tool-failure (not the technique) as summary. REVIEW counts occurrences; 2 in 5 rounds → harness patch required.

---

## Part 2: DOCUMENT

### 2a. Fill Lessons in `opt/current_plan.md`
3-5 bullets: what worked, what didn't, what to remember. Do this BEFORE archiving.

### 2b. Update `opt/state.json`
- Clear: `cycle`, `cycle_id`, `target`, `next_action` → ""
- Clear `plan_budget`
- Append to `previous_rounds`:
  ```json
  {"cycle_id":"...","category":"...","initiative":"...","outcome":"...","summary":"..."}
  ```
- **Note**: An independent SANITY phase runs after you. It re-reads plan + state + benchmark data with fresh context and flags unclosed outcome, stale baseline, physics-violating deltas, or skipped MUST steps. If you cut corners here, sanity will halt auto-continue and surface it to the user. Close out fully.
- **category_failures** for the current round's category: `abandoned`/`no_change`/`regressed` → +1; `improved` → reset to 0
- **Ceiling decay (mechanical, run AFTER appending current round to previous_rounds)**:
  For every category in `category_failures` with value ≥ 2 that is NOT the current round's category:
    - Scan the last 3 entries of `previous_rounds` (including the round just appended)
    - If that category does NOT appear in any of those 3 entries → reset `category_failures[category] = 0` and note the reset in the Results section: `"Ceiling decay: reset <category> failures (3 rounds skipped)"`
  Rationale: ceiling rule is temporary deprioritization, not permanent block. After 3 rounds of trying other directions, the category becomes eligible again with fresh approach.
- Increment: `rounds_since_review += 1`, `rounds_since_arch_audit += 1`
  (Note: ANALYZE resets `rounds_since_arch_audit` to 0 when it does a full audit)

### 2c. Update `opt/INDEX.md`
Prepend new row (newest first):
```
| [#] | [cycle_id] | [date] | [category] | [1-line target] | [outcome] | [key commit] | [1-line lesson] |
```

### 2d. Update initiative (if applicable)
- Append row to initiative's Rounds table
- Update Phases checkboxes + Next Step
- All phases done → `Status: complete`
- Abandoned + architecture wrong → `Status: abandoned`

### 2e. Save benchmark data
```bash
bash benchmarks/set_baseline.sh    # promote latest → baseline + history snapshot
bash benchmarks/plot_history.sh    # trajectory
```

### 2f. Archive the plan
```bash
bash .claude/hooks/archive_plan.sh
```

### 2g. Append to workflow log
One JSON line in `opt/workflow_log.jsonl`:
```json
{"round":"...","date":"YYYY-MM-DD","category":"...","outcome":"...","initiative":"...","budget_used":"N/M","notes":"..."}
```

### 2h. Update architecture docs (only if architecture changed)
- `docs-internal/architecture/overview.md`
- `CLAUDE.md` (if conventions changed)

### 2i. Finalize the round blog

Read `docs/draft.md`. Finish the post — add the results, your honest take on what worked
and what didn't. No fixed structure. Include benchmark numbers but interpret them, don't
just dump a table. End naturally, not with a template footer.

Then:
1. Rename `docs/draft.md` → `docs/NN-slug.md` (next number, slug from title)
2. Fix frontmatter permalink
3. Remove any `*[...next...]*` markers
4. Add to `docs/index.html` top of Posts section (date: YYYY-MM-DD)

### 2i-token. Write token reflection

Run `bash scripts/token_usage.sh --last` and write `opt/token_reflection.md`:

```markdown
# Token Reflection — <cycle_id>

## Usage by Phase
<token_usage output>

## Waste Points
- [phase/sub-agent: what consumed excess tokens and why]

## Saving Suggestions
- [concrete change + estimated saving + risk to effectiveness: none/low/medium]
```

Rules: (a) only flag concrete waste (duplicate work, unnecessary web search, oversized prompts, runaway sub-agents). (b) each suggestion must state the trade-off. (c) keep it under 20 lines — REVIEW will read and decide.

### 2j. Commit all changes
Scoped message: `opt: close out <cycle_id> (<outcome>)`

Include the blog post in the commit.

### 2k. Push to remote
```bash
git push origin main
```

## Rules
- Part 1 (VERIFY) may loop: fix → re-test → re-verify
- Part 2 (DOCUMENT) is one-shot after VERIFY passes
- Do NOT leave `current_plan.md` in place after archiving
- Do NOT leave `docs/draft.md` — must be renamed to final post
- Do NOT write new implementation code (only test fixes if needed)
- **MUST push to remote at the end** — every round's results must be on GitHub
