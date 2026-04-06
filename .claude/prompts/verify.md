# VERIFY + DOCUMENT Phase

You are in the final phase of an optimization round.
Your job: verify the implementation (tests + benchmarks + evaluator), then close out the round (update all cross-round state).

## Context
Read:
1. `opt/current_plan.md` — plan with expected effects + task breakdown
2. `CLAUDE.md` — project conventions, test commands
3. `benchmarks/data/baseline.json` — baseline from previous round
4. `opt/state.json` — current counters
5. `opt/INDEX.md` — round table

---

## Part 1: VERIFY

### 1a. Run tests
```
go test ./internal/methodjit/... -short -count=1 -timeout 120s
go test ./internal/vm/... -short -count=1 -timeout 120s
```
If tests fail: **fix first**. Correctness before performance.

### 1b. Run benchmarks
```
bash benchmarks/run_all.sh
```

### 1c. Compare vs baseline
Build before/after table. Flag regressions ≥5%.

### 1d. Evaluator
Spawn an Evaluator sub-agent to review the git diff:
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
- **category_failures**: `abandoned`/`no_change`/`regressed` → +1; `improved` → reset to 0
- Increment: `rounds_since_review += 1`, `rounds_since_research += 1`

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

### 2i. Commit all changes
Scoped message: `opt: close out <cycle_id> (<outcome>)`

## Rules
- Part 1 (VERIFY) may loop: fix → re-test → re-verify
- Part 2 (DOCUMENT) is one-shot after VERIFY passes
- Do NOT leave `current_plan.md` in place after archiving
- Do NOT write new implementation code (only test fixes if needed)
