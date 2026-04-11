# IMPLEMENT Phase

You are in the IMPLEMENT phase of the GScript optimization loop.

## Context

Read the plan and codebase index (one call):
```bash
cat opt/current_plan.md && echo "---" && cat opt/knowledge/codebase-index.md
```

For per-file purpose + key functions, consult `opt/knowledge/passes.md` (read on demand, not upfront).

CLAUDE.md is already loaded as project instructions — do NOT read it again.
Diagnostic docs (`docs-internal/diagnostics/`) — only read if a test fails and you need debugging guidance.
Baseline test snapshot (pre-existing failures): `opt/baseline_test_snapshot.txt` — read before spawning Coders so failures attributed correctly.

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

3. **Spawn a Coder sub-agent** (Opus model) with a bounded task — **maximum 1 implementation Coder per round** (R27 rule). If the plan has multiple implementation tasks, execute only the first and mark the rest as deferred:
   - The code snippets you just read (pasted in the prompt)
   - Specific file(s) to modify
   - Specific test(s) that must pass
   - What NOT to touch (scope boundary)
   - "If you need to read additional files not provided above, use Read. But try the provided code first."
   - "If you can't make it work in 3 attempts, return a failure report"
   - **Small-task cap (R27)**: if task changes ≤2 files, add to Coder prompt: "Cap at 15 tool calls."
   - **Full-package gate (R30, MANDATORY)**: every Coder prompt MUST end with: "Before marking this task done, run `go test ./internal/methodjit/... -short -count=1 -timeout 120s` as the FINAL gating command. A green targeted-test run is NOT enough. If any test fails (including tests not mentioned in the plan), STOP and report — do not mark the task done." Reason: R30 landed 903e505 under a curated correctness gate, VERIFY's full run caught `TestTier2RecursionDeeperFib` crash, round reverted. Curated test lists miss cross-test interactions.
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
- **Plan premise contradicted by code** (R24): Coder observes file/function/bottleneck ≠ plan's claim → STOP phase, write `opt/premise_error.md` (claim vs reality vs suspected tool bug), hand to VERIFY as `data-premise-error`. No silent adapt, no in-phase replan.

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
- **Bash iteration cap**: if a shell/script task fails after 3 attempts, write a minimal failing case to `opt/bash_failure.md` and report — do NOT keep iterating
- **JIT SIGSEGV = data-premise-error** (R26): Coder crash with `fault addr: 0xFFFE...`/`0xFFFF...` (NaN-boxed value) = goroutine stack corruption by JIT code. Abort immediately — do NOT retry. Revert all changes, write `opt/premise_error.md`, output `data-premise-error`. Reason: JIT blobs cannot call morestack; architecture mismatches cause immediate corruption, iteration is impossible.

## After all tasks — Update the round blog

Read `docs/draft.md` (created by ANALYZE). Continue the post naturally — tell what happened
during implementation. No fixed headers. Tell the story: what was easy, what broke, what
surprised you. Show interesting code or IR, not task checklists. Be honest.

End with `*[Results coming next...]*`.
