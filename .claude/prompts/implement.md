# IMPLEMENT Phase

You are in the IMPLEMENT phase of the GScript optimization loop.

## Context

Read the plan (one call):
```bash
cat opt/current_plan.md
```

CLAUDE.md is already loaded as project instructions — do NOT read it again.
Diagnostic docs (`docs-internal/diagnostics/`) — only read if a test fails and you need debugging guidance.

## Pre-flight: check for injected tasks

```bash
cat opt/inject_tasks.md 2>/dev/null
```

If `opt/inject_tasks.md` exists, it contains tasks the user wants inserted into this round.
**Prepend them to `current_plan.md`'s Task Breakdown** (before the existing tasks, as Task 0, 0b, etc.),
then delete the injection file:
```bash
rm opt/inject_tasks.md
```

This lets the user inject work into a running round without restarting.

## Task
Execute tasks from `current_plan.md` in order (including any injected tasks). For each task:

1. **Check plan task list** (`bash -c 'grep "^\- \[" opt/current_plan.md'` — don't re-read the whole plan)
2. **Pre-read code for the Coder** — before spawning, use Bash to read the files the task mentions:
   ```bash
   # Read only the relevant functions, not entire 900-line files
   sed -n '100,150p' internal/methodjit/regalloc.go   # the function to change
   cat internal/methodjit/regalloc_test.go | head -50  # existing test patterns
   ```
   Paste the output into the Coder's prompt so it doesn't need to Read them itself.

3. **Spawn a Coder sub-agent** (Opus model) with a bounded task:
   - The code snippets you just read (pasted in the prompt)
   - Specific file(s) to modify
   - Specific test(s) that must pass
   - What NOT to touch (scope boundary)
   - "If you need to read additional files not provided above, use Read. But try the provided code first."
   - "If you can't make it work in 3 attempts, return a failure report"
4. **Update current_plan.md**: mark task done or record failure
5. **Check scope**: did the Coder change files outside the plan?
6. **Collect incidental findings**: if the Coder reports pre-existing failures, stale tests,
   deprecated code, or other issues unrelated to the current task:
   - **Quick fix** (≤5 min, e.g. delete a stale test reference, fix a typo): add as a bonus task
     in this round, do it after the planned tasks complete. Note in current_plan.md.
   - **Not quick** (requires design, touches other modules): append to `docs-internal/known-issues.md`
     with a one-line description so it gets picked up by a future ANALYZE.
   - Do NOT ignore these findings. "Pre-existing, not related to our changes" is not a reason
     to drop information — it's a reason to record it properly.

## Abort Conditions (from current_plan.md)
- Budget exceeded → report to user, STOP
- Failure signal triggered → report to user, STOP
- Correctness regression (tests fail) → fix first, then continue

## Rules
- TDD: write failing test FIRST, then implementation
- No Go file exceeds 1000 lines
- **Commit frequently** — do NOT accumulate uncommitted work:
  - Commit after each task completes (not at the end of the phase)
  - Each commit should be a logical, reviewable unit
  - If a task touches 3+ files, consider splitting into 2 commits (e.g., tests first, then implementation)
  - Run `git status` after each Coder returns — if there are uncommitted changes, commit them
- Do NOT skip tasks or do tasks out of order
- Do NOT modify files outside the plan scope
- If a Coder fails 3 times on one task, STOP and report — do not continue to the next task

## After all tasks — Update the round blog

Read `docs/draft.md` (created by ANALYZE). Append the implementation section:

```markdown
## What we built

[Describe what actually happened during implementation. Not a task checklist —
tell the story. What was straightforward? What surprised you? Did TDD catch
anything? Did you have to deviate from the plan?

Show real code or IR diffs where they're interesting. "We added 20 lines to
graph_builder.go" is boring. "The key insight was that GetTable's result type
is already in the FeedbackVector — we just weren't reading it" is interesting.

If something broke, say what and how you fixed it. If a Coder agent struggled,
say why. Be honest about what was hard.]

*[Results coming next...]*
```

Write like you're explaining to a smart colleague what you just spent 2 hours on.
