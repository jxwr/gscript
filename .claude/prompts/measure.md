# MEASURE Phase

You are in the MEASURE phase of the GScript optimization loop.

## Context
Read these files first:
- `CLAUDE.md` — project mission and conventions
- `.claude/state.json` — current optimization state (if non-empty, a previous round exists)
- `benchmarks/data/baseline.json` — previous baseline (if exists)

## Task
1. Run `bash benchmarks/run_all.sh` — full benchmark suite
2. Run `bash benchmarks/set_baseline.sh` — save as baseline for this cycle
3. Read `benchmarks/data/latest.json` and `benchmarks/data/baseline.json`

## Output
Write a brief summary to `.claude/measure_report.md`:

```markdown
## Benchmark Results — [DATE]

| Benchmark | VM | JIT | LuaJIT | JIT/VM | JIT/LuaJIT |
|-----------|-----|-----|--------|--------|------------|

## Top 3 Gaps vs LuaJIT
1.
2.
3.
```

Do NOT start any analysis or optimization. Your only job is to measure.
