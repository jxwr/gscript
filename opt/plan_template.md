# Optimization Plan: [TITLE]

> Created: [YYYY-MM-DD HH:MM]
> Status: active | completed | abandoned
> Cycle ID: [YYYY-MM-DD-short-name]
> Category: [recursive_call | tier2_float_loop | tier2_correctness | allocation_heavy | gofunction_overhead | field_access | call_ic | regalloc | missing_intrinsic | arch_refactor | other]
> Initiative: opt/initiatives/X.md | standalone

## Target
What benchmark(s) are we trying to improve, and by how much?

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|

## Root Cause
What is the architectural/implementation bottleneck causing the gap?

## Prior Art (MANDATORY)
How do production compilers solve this?

**V8:**  
**LuaJIT:**  
**SpiderMonkey (if relevant):**  
**Academic papers (if relevant):**  

Our constraints vs theirs:

## Approach
Concrete implementation plan. What changes, in what files.

## Expected Effect
Quantified predictions for specific benchmarks.

**Prediction calibration (MANDATORY):** Halve percentage estimates derived from instruction-count
analysis on superscalar ARM64 — OoO execution hides much of the overhead that looks dominant in
static analysis. If the previous round's prediction was off by >3x, state explicitly why this
round's estimate will be more accurate. Data: rounds 7-10 overestimated primary targets by 2-25x
when anchoring to instruction counts without modeling pipeline effects.

## Failure Signals
What would tell us this approach is wrong? Be specific:
- Signal 1: [condition] → [action: abandon / pivot / research more]
- Signal 2: [condition] → [action]

**MANDATORY if plan touches tiering policy (`func_profile.go`) or Tier 2 promotion criteria:**
Task N MUST include an integration check via the compiled CLI binary
(`go build -o /tmp/gscript_<round> ./cmd/gscript` + `perl -e 'alarm T; exec'` or `timeout Ns`)
running the target benchmark source. `go test` alone does NOT catch tiering hangs —
round 4 (2026-04-05) burned 3 commits and a hang because unit tests passed while CLI hung.

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [ ] 1. [task] — file(s): `X.go` — test: `TestY`
- [ ] 2. [task] — file(s): `X.go` — test: `TestY`
- [ ] 3. Integration test + benchmark

## Budget
- Max commits: [N_functional] (+1 revert slot if plan includes a speculative policy/code flip)
- Max files changed: [N]
- Abort condition: [e.g., "3 commits without benchmark improvement"]

The revert slot is consumed only if a Task is reverted at VERIFY; otherwise it is dropped
and the actual commit count comes in under the stated cap.

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|

## Lessons (filled after completion/abandonment)
What worked, what didn't, what to remember for next time.
