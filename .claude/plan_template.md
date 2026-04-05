# Optimization Plan: [TITLE]

> Created: [DATE]  
> Status: active | completed | abandoned  
> Cycle ID: [YYYY-MM-DD-short-name]

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

## Failure Signals
What would tell us this approach is wrong? Be specific:
- Signal 1: [condition] → [action: abandon / pivot / research more]
- Signal 2: [condition] → [action]

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [ ] 1. [task] — file(s): `X.go` — test: `TestY`
- [ ] 2. [task] — file(s): `X.go` — test: `TestY`
- [ ] 3. Integration test + benchmark

## Budget
- Max commits: [N]
- Max files changed: [N]
- Abort condition: [e.g., "3 commits without benchmark improvement"]

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|

## Lessons (filled after completion/abandonment)
What worked, what didn't, what to remember for next time.
