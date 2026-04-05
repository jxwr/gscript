# ANALYZE Phase

You are in the ANALYZE phase of the GScript optimization loop.

## Context
Read these files (MANDATORY, in this order):
1. `CLAUDE.md` — project mission
2. `.claude/state.json` — optimization state
3. `docs-internal/lessons-learned.md` — past detours and mistakes (READ THIS CAREFULLY)
4. `docs-internal/known-issues.md` — current known issues
5. `benchmarks/data/latest.json` — current benchmark results
6. `benchmarks/data/baseline.json` — previous baseline
7. `.claude/measure_report.md` — top gaps from MEASURE phase

## Task
1. Classify ALL benchmark gaps vs LuaJIT by root cause category.
   Common categories: field access overhead, call overhead, type specialization gaps, register allocation, exit-resume frequency, missing intrinsics, GC pressure, loop optimization, etc.

2. For the top gap category, decide if it's worth pursuing:
   - How many benchmarks does it affect?
   - Is there prior art in modern compilers? (see Research Focus below)
   - Does `lessons-learned.md` flag this as a known detour?

3. If bottleneck is unclear, spawn a Profiler sub-agent: `go test -run=BenchSpectralNorm -bench=. -cpuprofile=cpu.prof ./benchmarks/` then analyze with `go tool pprof`.

## Research Focus
When researching solutions, prioritize modern production compilers:

- **V8** (Sparkplug → Maglev → TurboFan): Our primary reference architecture. Method JIT, SSA IR, type feedback, speculative optimization, deoptimization. Always check V8 first.
- **SpiderMonkey** (Baseline → Warp): Similar tiering to V8. Good reference for register allocation and bailouts.
- **JavaScriptCore** (LLInt → Baseline → DFG → FTL): Four-tier system. Good reference for OSR and type profiling.
- **.NET CLR** (RyuJIT): Tiered JIT with on-stack replacement. Good reference for register allocation.
- **Academic compilers**: LLVM, GraalVM, Cranelift. Search for specific techniques (escape analysis, loop invariant code motion, scalar replacement).
- **LuaJIT**: Only reference for comparison of absolute numbers (our benchmark target), NOT for implementation techniques. We use a fundamentally different architecture (Method JIT vs Trace JIT).

Do NOT research LuaJIT implementation internals for technique guidance — our architecture is V8-style Method JIT, not trace-based.

## Output
Write `.claude/analyze_report.md`:

```markdown
## Gap Classification

| Root Cause | Benchmarks | Avg Gap vs LuaJIT | Solvable? |
|-----------|------------|-------------------|-----------|

## Selected Target
Category: [chosen root cause]
Reason: [why this over alternatives — reference ROI]
Benchmarks: [list]

## Detour Check
[Am I repeating a known detour from lessons-learned.md?]

## Prior Art Needed
[What research is needed before planning? If none, state "none"]
```

Do NOT write any code. Do NOT create a plan yet. Your only job is to analyze.
