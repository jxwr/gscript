---
name: gs-opt-loop
description: GScript optimization loop. Launches the orchestrator in background, sets up progress monitoring, reports when complete.
---

# GScript Optimization Loop

When `/gs-opt-loop` is invoked, do these three things in order:

## 1. Launch the orchestrator in background

```bash
bash .claude/optimize.sh
```

Run this with `run_in_background: true` and `timeout: 600000`.

## 2. Set up progress monitoring

Immediately after launch, create a recurring cron job (10-minute interval) that checks progress:

```
CronCreate:
  cron: "*/10 * * * *"
  recurring: true
  prompt: |
    Check optimization round progress:
    Run these commands and give a 2-3 line status update in Chinese:
    - grep "Phase .* complete" <task_output_file>
    - bash .claude/watch-child.sh --list 2>&1 | head -6  
    - bash scripts/token_usage.sh --last 2>&1 | tail -8
```

Replace `<task_output_file>` with the actual output file path from the background task.

Save the cron job ID — you'll need it for step 3.

## 3. When the round completes

When the `task-notification` arrives (background task completed):
1. `CronDelete` the monitoring job
2. Read the task output (tail -50) to get the round results
3. Report the results to the user

---

## What It Does

3-phase loop, each an independent Claude session:

```
REVIEW → ANALYZE+PLAN → IMPLEMENT → VERIFY+DOCUMENT
```

## Usage

```bash
bash .claude/optimize.sh                   # one full cycle
bash .claude/optimize.sh --rounds=5        # multiple cycles
bash .claude/optimize.sh --from=implement  # resume from phase
bash .claude/optimize.sh --no-review       # skip review
```

## Monitoring (manual, in separate terminal)

```bash
bash .claude/watch-child.sh       # session viewer with status bar
bash .claude/dashboard.sh         # terminal dashboard
bash scripts/token_usage.sh --last  # token consumption
```

## Interrupting and Resuming

If a phase fails:
1. Fix the issue
2. Resume: `bash .claude/optimize.sh --from=<phase>`
3. Valid phases: `analyze`, `implement`, `verify`
