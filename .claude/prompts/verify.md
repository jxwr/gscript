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

Read `docs/draft.md`. Append the results section and finalize:

```markdown
## The results

[Before/after benchmark table. But don't just dump numbers — interpret them.
"sieve dropped 18% because the marking loop no longer exits to Go for bool
array writes" is better than "sieve: 0.227s → 0.186s."

Did it meet expectations? If not, why? What did we learn? Be specific about
what the remaining bottleneck is — this is the seed for the next round.]

## What I'd do differently

[Honest retrospective. Was the diagnostic accurate? Was the plan right?
What would you tell yourself before starting this round?]

*Previous: [last post title](/last-post-slug)*

*This is post NN in the [GScript JIT series](https://jxwr.github.io/gscript/).
All numbers from a single-thread ARM64 Apple Silicon machine.*
```

Then:
1. Determine the next post number: `ls docs/[0-9]*.md | wc -l` + 1, or check `docs/index.html`
2. Rename `docs/draft.md` → `docs/NN-slug.md` (slug from title, lowercase, hyphens)
3. Fix the frontmatter: set correct `permalink: /NN-slug`
4. Remove the `*[This post is being written live...]*` markers
5. Add the new post to `docs/index.html` at the top of the Posts section.
   Use **full date** (YYYY-MM-DD) in the `<span class="date">` tag, e.g. `<span class="date">2026-04-06</span>`

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
