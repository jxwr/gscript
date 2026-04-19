# GScript Benchmark Suite

15 benchmarks covering compute, recursion, table/array, function calls, strings, and closures.

## Run

```bash
# Full suite (VM + Trace + LuaJIT, parallel)
bash benchmarks/run_bench.sh

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
