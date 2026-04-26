# GScript Benchmark Suite

15 benchmarks covering compute, recursion, table/array, function calls, strings, and closures.

## Run

```bash
# Full suite (VM + JIT + LuaJIT, one-shot table)
bash benchmarks/run_bench.sh

# Regression guard: VM + default JIT + no-filter JIT + LuaJIT, median-of-3
bash benchmarks/regression_guard.sh --runs=3 --json benchmarks/data/regression_guard_latest.json

# Publish-grade guard
bash benchmarks/regression_guard.sh --runs=5 --timeout=90

# Quick mode (Go warm benchmarks only, ~15s)
bash benchmarks/run_bench.sh --quick

# Suite benchmarks (CLI, one-shot timing)
cd benchmarks/suite && bash run_all.sh ../../cmd/gscript/main.go -trace

# Go micro-benchmarks (warm, no compilation overhead)
go test ./benchmarks/ -bench=Warm -benchtime=3s

# Compare with LuaJIT
luajit benchmarks/lua/run_all.lua
```

Performance results are in the [top-level README](../README.md).

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1

## Reading the output

`regression_guard.sh` is the preferred full comparison tool for method-JIT
performance work. It builds `cmd/gscript` once, runs every suite benchmark in:

| Mode | Meaning |
|------|---------|
| `VM` | bytecode VM, no JIT |
| `Default` | normal method JIT with `-jit-stats -exit-stats` |
| `NoFilter` | method JIT with `GSCRIPT_TIER2_NO_FILTER=1` |
| `LuaJIT` | matching `benchmarks/lua/<name>.lua`, when `luajit` is in `PATH` |

The table reports median wall time, `JIT/VM`, `JIT/LJ`, Tier 2
`attempted/entered/failed`, total Tier 2 exits, and a regression marker when
default JIT is more than 10% slower than `benchmarks/data/baseline.json`.
Each benchmark/mode/sample has its own timeout; a timeout or crash records that
cell and the runner continues with the rest of the suite. The process exits
non-zero only after the table is complete and at least one regression is marked.

`run_bench.sh` renders a table with five comparative columns and one
tiering indicator:

| Column | Meaning |
|--------|---------|
| `VM`, `JIT`, `LuaJIT` | Wall-clock seconds (one-shot; use `-count=3` for medians) |
| `JIT/VM` | Speedup of JIT over VM |
| `T2` | `entered/compiled` count from `-jit-stats` (R146). `1/1` means one proto compiled at Tier 2 and its native prologue actually ran; `0/N` means compiled but never executed (routing issue); `0/0` means no Tier 2 activity for this benchmark. |

When diagnosing a perf anomaly, **read the `T2` column before reasoning
about the wall time**. Several past rounds (R132, R133–R139, R145)
burned time analyzing Tier 2 emit for benchmarks that were not even
running Tier 2 native code. See `docs-internal/diagnostics/debug-jit-correctness.md`
§ Step 0.
