---
name: gs-opt-loop
description: GScript optimization loop. Launches the external orchestrator that runs MEASURE→ANALYZE→PLAN→IMPLEMENT→VERIFY→DOCUMENT as independent Claude sessions.
---

# GScript Optimization Loop

When `/gs-opt-loop` is invoked, launch the external orchestrator:

```bash
bash .claude/optimize.sh
```

## What It Does

Each phase runs as an **independent Claude session** — no context accumulation, no drift.
State passes between phases via files:

```
MEASURE → .claude/measure_report.md
ANALYZE → .claude/analyze_report.md
PLAN    → .claude/current_plan.md (waits for human approval)
IMPLEMENT → updates current_plan.md
VERIFY  → fills Results section
DOCUMENT → archives plan, updates state.json, commits
```

## Usage

```bash
# Full cycle
bash .claude/optimize.sh

# Resume from a specific phase
bash .claude/optimize.sh --from=analyze

# Preview what would run
bash .claude/optimize.sh --dry-run
```

## Human Gates

The orchestrator pauses for human input at ONE point:
- **PLAN → IMPLEMENT**: reviews current_plan.md, asks for approval

All other gates are enforced by output file checks (each phase must produce its expected output before the next phase starts).

## Prompt Files

Each phase prompt lives in `.claude/prompts/<phase>.md`. To adjust a phase's behavior, edit the corresponding prompt file. Do NOT edit the orchestrator script unless changing the phase sequence.

## Phase Descriptions

| Phase | Input | Output | Restrictions |
|-------|-------|--------|-------------|
| MEASURE | — | measure_report.md | Read-only, run benchmarks only |
| ANALYZE | measure_report.md, lessons-learned.md, known-issues.md | analyze_report.md | Read-only, classification only |
| PLAN | analyze_report.md | current_plan.md | Write .claude/ only, no code |
| IMPLEMENT | current_plan.md | updated current_plan.md | TDD, scope-bounded coders |
| VERIFY | current_plan.md, baseline.json | filled Results section | Tests + benchmarks only |
| DOCUMENT | current_plan.md | archived plan, workflow log | Docs + commit only |

## Parallel Research (Phase 1 enhancement)

For the ANALYZE phase, the prompt instructs the agent to spawn parallel sub-agents:
- **Profiler**: pprof on worst benchmark
- **Researcher**: web search for V8/LuaJIT solutions

These run concurrently within the ANALYZE session and feed into the analyze_report.

## Interrupting and Resuming

If a phase fails or is interrupted:
1. Fix the issue (edit files, run commands)
2. Re-run from the failed phase: `bash .claude/optimize.sh --from=<phase>`
3. The orchestrator checks that the previous phase's output exists before starting

## Direction Judgment

When choosing what to optimize (referenced by ANALYZE prompt):
- Pursue root causes affecting 3+ benchmarks first
- Prefer approaches with V8/LuaJIT prior art
- Abandon if 2 consecutive rounds fail on same category (ceiling hit)
- Check `docs-internal/lessons-learned.md` before every direction choice
