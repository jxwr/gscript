# MEASURE Phase

You are in the MEASURE phase of the GScript optimization loop.

## Context
Read these files first:
- `CLAUDE.md` — project mission and conventions
- `opt/state.json` — current optimization state (if non-empty, a previous round exists)
- `benchmarks/data/baseline.json` — previous baseline (if exists)

## Task

### Step 0 — Check if re-measurement is needed

Compare the commit hash in `benchmarks/data/latest.json` (field: `commit`) with `git rev-parse HEAD`.

- **If they match**: benchmarks are already up-to-date. **Skip `run_all.sh`** and go directly to
  reading existing data + writing the report. Print: "Benchmarks up-to-date (commit <hash>), skipping re-run."
- **If they differ** (or `latest.json` doesn't exist): run the full measurement below.

### Step 1 — Run benchmarks (only if needed per Step 0)
1. Run `bash benchmarks/run_all.sh` — full benchmark suite
2. Run `bash benchmarks/set_baseline.sh` — save baseline + history snapshot
3. Run `bash benchmarks/plot_history.sh` — ASCII trajectory across rounds

### Step 2 — Read results
4. Read `benchmarks/data/latest.json` and `benchmarks/data/baseline.json`

## Output
Write a brief summary to `opt/measure_report.md`:

```markdown
## Benchmark Results — [DATE]
> Commit: [hash] | Reused: [yes/no]

| Benchmark | VM | JIT | LuaJIT | JIT/VM | JIT/LuaJIT |
|-----------|-----|-----|--------|--------|------------|

## Top 3 Gaps vs LuaJIT
1.
2.
3.

## Trajectory (from plot_history.sh, if re-run)
[paste the ASCII table output]

### Notable Movement
- [benchmarks that moved ≥5% since last snapshot]
- [flag ≥10% regressions prominently]
```

Do NOT start any analysis or optimization. Your only job is to measure.
