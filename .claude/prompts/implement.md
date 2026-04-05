# IMPLEMENT Phase

You are in the IMPLEMENT phase of the GScript optimization loop.

## Context
Read these files (in this order):
1. `.claude/current_plan.md` — the approved plan with task breakdown
2. `CLAUDE.md` — coding conventions (TDD, file size limits, commit style)
3. `docs-internal/diagnostics/debug-jit-correctness.md` — debugging tools
4. `docs-internal/diagnostics/debug-ir-pipeline.md` — IR pipeline debugging

## Task
Execute tasks from `current_plan.md` in order. For each task:

1. **Re-read current_plan.md** (prevent context drift)
2. **Spawn a Coder sub-agent** (Opus model) with a bounded task:
   - Specific file(s) to modify
   - Specific test(s) that must pass
   - What NOT to touch (scope boundary)
   - "If you can't make it work in 3 attempts, return a failure report"
3. **Update current_plan.md**: mark task done or record failure
4. **Check scope**: did the Coder change files outside the plan?

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
