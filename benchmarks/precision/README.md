# Precision Benchmarks

This layer measures small benchmark kernels with Go's benchmark timer instead
of the suite scripts' printed `Time: %.3fs` values.

Run the focused set:

```bash
go test ./benchmarks -run '^$' -bench '^BenchmarkPrecision' -benchtime=500ms -benchmem
```

Run one benchmark family:

```bash
go test ./benchmarks -run '^$' -bench '^BenchmarkPrecision/fib_recursive' -benchtime=1s -benchmem
```

Or use the wrapper:

```bash
benchmarks/precision/run.sh
benchmarks/precision/run.sh '^BenchmarkPrecision/(ackermann|mutual_recursion)'
BENCHTIME=1s COUNT=3 benchmarks/precision/run.sh '^BenchmarkPrecision'
```

The Go benchmark currently compares only the in-process bytecode VM and method
JIT paths. LuaJIT is intentionally excluded here: calling the `luajit` binary
inside each `testing.B` iteration would mostly measure process startup, while
embedding LuaJIT is not part of this repository's Go benchmark harness. Keep
LuaJIT comparisons in the existing suite and guard scripts unless there is a
same-process LuaJIT harness with matching kernel work.
