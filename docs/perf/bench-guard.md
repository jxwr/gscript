# Benchmark Guard Workflow

Use this guard before pushing performance-sensitive changes. It compares the
current checkout against the checked-in baseline, the VM, no-filter Tier 2, and
LuaJIT where matching Lua files exist.

## Push Check

```bash
bash benchmarks/regression_guard.sh --runs=3 \
  --threshold=10 \
  --json benchmarks/data/regression_guard_latest.json \
  --csv benchmarks/data/regression_guard_latest.csv \
  --markdown benchmarks/data/regression_guard_latest.md
```

The command builds `cmd/gscript` once, runs every benchmark sample in isolation,
prints the full table, and exits non-zero after the table if any default-JIT
benchmark is more than `--threshold` percent slower than
`benchmarks/data/baseline.json`.

For a publish-grade run, use more samples and a longer timeout:

```bash
bash benchmarks/regression_guard.sh --runs=5 --timeout=90 \
  --json benchmarks/data/regression_guard_latest.json \
  --csv benchmarks/data/regression_guard_latest.csv \
  --markdown benchmarks/data/regression_guard_latest.md
```

## Parameters

`--runs=N` controls how many samples are collected for each benchmark and mode.
The reported time is the median of successful samples. Higher values reduce
noise but multiply runtime by `N`.

`--count=N` is an alias for `--runs=N` in this guard. It is provided for people
coming from `go test -count=N`, but it does not run Go tests.

`--timeout=SECONDS` is applied to each individual sample, not to the full suite.
A timeout records that cell and the runner continues with the remaining
benchmarks.

`--threshold=PCT` marks a regression when default JIT is more than `PCT` percent
slower than the baseline default-JIT time for the same benchmark. The default is
`10`.

`--bench=NAME` runs one benchmark. Repeat it to run a small focused set while
debugging:

```bash
bash benchmarks/regression_guard.sh --runs=3 --bench=sieve --bench=fannkuch
```

`--no-luajit` skips LuaJIT even when it is installed.

## Outputs

`--json` writes the full nested result, including all samples, status, captured
output tails, Tier 2 counters, exit counters, platform, commit, and threshold.
Use this for archival or deeper diagnosis.

`--csv` writes one flat row per benchmark. It is convenient for spreadsheet
diffs and dashboards. The key columns are:

| Column | Meaning |
|---|---|
| `default_seconds` | median default-JIT time |
| `jit_vm_speedup` | `vm_seconds / default_seconds`; higher is better |
| `jit_luajit_ratio` | `default_seconds / luajit_seconds`; lower is closer to LuaJIT |
| `baseline_seconds` | default-JIT baseline loaded from `--baseline` |
| `regression_pct` | percent change from baseline; positive is slower |
| `regression` | true when `regression_pct > threshold` |
| `t2_attempted`, `t2_entered`, `t2_failed` | Tier 2 health counters from `-jit-stats` |
| `exit_total` | total Tier 2 exits from `-exit-stats` |

`--markdown` writes the same summary as a Markdown table for PRs or handoff
notes.

## Example Summary

```text
Benchmark                     VM   Default  NoFilter    LuaJIT   JIT/VM   JIT/LJ  Baseline    T2 a/e/f   Exits   Regress
-------------------------------------------------------------------------------------------------------------------------
sieve                     0.242s    0.088s    0.083s    0.010s    2.75x    8.80x    0.088s      1/1/0       0         -
fannkuch                  0.570s    0.049s    0.048s    0.020s   11.63x    2.45x    0.049s      1/1/0       2      +0.0%
binary_trees              1.644s    2.350s    2.120s   skipped    0.70x        -    2.006s      1/1/0      18  REG +17.1%

Regression threshold: >10.0% slower than baseline default JIT
Regressions: 1
```

Interpret the table in this order:

1. Check `Regress`. A `REG` marker means the command exits non-zero.
2. Compare `Default` with `Baseline` or `regression_pct` in CSV/JSON.
3. Check `T2 a/e/f`. `attempted/entered/failed` tells whether Tier 2 was used
   and whether compilation failed.
4. Compare `JIT/VM` and `JIT/LJ`. `JIT/VM` should usually be above `1.00x`;
   `JIT/LJ` is a gap ratio where lower is better.

## Updating The Baseline

Only update `benchmarks/data/baseline.json` after a clean publish-grade run on a
known machine and after intentional performance changes have been reviewed:

```bash
bash benchmarks/regression_guard.sh --runs=5 --timeout=90 \
  --json benchmarks/data/regression_guard_latest.json
bash benchmarks/set_baseline.sh benchmarks/data/regression_guard_latest.json
```
