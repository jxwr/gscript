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
