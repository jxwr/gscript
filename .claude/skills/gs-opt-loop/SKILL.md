---
name: gs-opt-loop
description: GScript optimization loop. Launches the orchestrator in background, sets up progress monitoring, reports when complete.
---

# GScript Optimization Loop

When `/gs-opt-loop` is invoked, do these four things in order:

## 0. Detect partial / crashed state (added R23 review)

Before launching, check whether a previous round left partial state behind:

```bash
# Read current cycle + uncommitted files + HEAD
python3 -c "
import json, subprocess
s = json.load(open('opt/state.json'))
head = subprocess.check_output(['git','rev-parse','HEAD']).decode().strip()
cycle = s.get('cycle','')
plan_exists = __import__('os').path.exists('opt/current_plan.md')
print(f'cycle={cycle!r} plan_exists={plan_exists} head={head[:8]}')"
```

Decision tree:

- **`cycle=""` AND no `opt/current_plan.md`** → clean state, proceed to step 1 (fresh launch).
- **`cycle` is non-empty OR `opt/current_plan.md` exists with unchecked `- [ ]` tasks** → a previous round did not close out. Recover instead of starting fresh:
  - If `opt/current_plan.md` has unchecked tasks AND `cycle=IMPLEMENT` → resume with `bash .claude/optimize.sh --from=implement`
  - If `opt/current_plan.md` is fully checked but the round wasn't committed → resume with `bash .claude/optimize.sh --from=verify`
  - If `cycle=ANALYZE` and no plan exists → resume with `bash .claude/optimize.sh --from=analyze` (re-does analyze from scratch — cheap)
  - If `opt/current_plan.md` exists AND phase_log shows `plan_check start` with no matching `end` → **plan_check crashed mid-run**. Resume with `bash .claude/optimize.sh --from=plan_check` (reads state.json for iteration, re-runs plan_check at the crashed iteration, then continues the analyze⇄plan_check loop if needed, then implement→verify→sanity).
  - If `opt/plan_check.md` exists with verdict `NEEDS_IMPROVEMENT`/`FAIL` but no subsequent analyze run → **plan_check returned feedback but analyze didn't get to rewrite**. Resume with `bash .claude/optimize.sh --from=plan_check` (detects NEEDS_IMPROVEMENT, runs analyze iter+1, then plan_check).
  - If unclear → ask the user: "Previous round left {cycle} state with {plan status}. Resume from {phase} or start fresh?"

Rationale: R22 died mid-VERIFY and was manually resumed. R23/R24 died on token exhaustion and the user had to manually investigate where it stopped. Both cost operational time. The skill should detect and recommend the resume action, not start a fresh round on top of partial state.

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
    If the task is still running: remind the user that the orchestrator advances phases automatically — do NOT manually run optimize.sh --from=<phase>. Manual intervention causes duplicate phase execution.
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
3. Valid phases: `context_gather`, `analyze`, `plan_check`, `implement`, `verify`, `sanity`
