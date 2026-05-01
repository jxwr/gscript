# GScript Benchmark Suite

15 benchmarks covering compute, recursion, table/array, function calls, strings, and closures.

## Run

```bash
# Full suite (VM + JIT + LuaJIT, one-shot table)
bash benchmarks/run_bench.sh

# Strict truth pass: suite + extended + variants, VM/JIT/no-filter/LuaJIT,
# median timing, output checksums, JIT stats, and overfit-win notes.
python3 benchmarks/strict_guard.py --runs=3 --warmup=1 --timeout=90 \
  --json benchmarks/data/strict_guard_latest.json \
  --markdown benchmarks/data/strict_guard_latest.md

# Representative strict subset while iterating
python3 benchmarks/strict_guard.py --runs=3 --warmup=0 --max-repeat=8 \
  --bench=suite/matmul --bench=variants/matmul_row_variant \
  --bench=extended/json_table_walk

# Regression guard: VM + default JIT + no-filter JIT + LuaJIT, median-of-3
bash benchmarks/regression_guard.sh --runs=3 \
  --json benchmarks/data/regression_guard_latest.json \
  --csv benchmarks/data/regression_guard_latest.csv \
  --markdown benchmarks/data/regression_guard_latest.md

# Publish-grade guard
bash benchmarks/regression_guard.sh --runs=5 --timeout=90

# Promote a reviewed guard JSON to the checked-in baseline
bash benchmarks/set_baseline.sh benchmarks/data/regression_guard_latest.json

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

`strict_guard.py` is the preferred truth pass before claiming benchmark wins.
It builds `cmd/gscript` once, discovers every `benchmarks/suite/*.gs`, every
extended benchmark in `benchmarks/extended/manifest.json`, and every
`benchmarks/variants/*.gs`, then runs each available cell in:

| Mode | Meaning |
|------|---------|
| `vm` | bytecode VM, no JIT |
| `default` | normal method JIT with `-jit-stats -exit-stats` |
| `no_filter` | method JIT with `GSCRIPT_TIER2_NO_FILTER=1` |
| `luajit` | matching Lua reference under `benchmarks/lua*`, when `luajit` is in `PATH` |

The strict report includes median timing, sample spread, repeat count, timing
source, output checksum hash, literal `checksum:` value when present, Tier 2
`attempted/entered/failed`, total exits, and a "Suspicious Kernel Wins" section.
That section calls out suite benchmarks that beat LuaJIT by a large margin but
are not confirmed by their structural variants. It is intentionally a review
signal, not a performance patch gate.

Use `--group=suite`, `--group=extended`, or `--group=variants` to narrow the
run; repeat `--bench=group/name` for a representative subset. `--dry-run`
prints the exact discovered matrix without building or running anything.

`regression_guard.sh` remains the preferred baseline regression tool for
method-JIT performance work. It builds `cmd/gscript` once, runs every core suite
benchmark in:

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
Use `--runs=N` to choose median sample count for the guard. `--count=N` is an
alias for the same setting; it does not invoke Go benchmark `-count`. For Go
micro-benchmarks, use `go test ./benchmarks/ -bench=Warm -count=N`.

For the push-before-push workflow and report examples, see
[docs/perf/bench-guard.md](../docs/perf/bench-guard.md).

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
