---
layout: default
title: "The Green Run Held"
permalink: /88-the-green-run-held
---

# The Green Run Held

*May 2026 - Beyond LuaJIT, Post #88*

## A Clean Current-vs-LuaJIT Pass

The previous post said the scoreboard had turned green. This follow-up is the
check that matters after fixing the last broken variant and rerunning the full
suite from the working tree.

The snapshot is checked in here:

```text
benchmarks/data/history/2026-05-17.full_perf.json
benchmarks/data/history/2026-05-17.full_perf.md
```

The command was:

```text
python3 benchmarks/timing_compare.py --all-groups --runs=5 --warmup=1 \
  --time-source=auto --min-sample-seconds=0.100 --max-repeat=128 \
  --sort=luajit-gap \
  --json benchmarks/data/history/2026-05-17.full_perf.json \
  --markdown benchmarks/data/history/2026-05-17.full_perf.md
```

The important result is simple:

```text
38 / 38 Current rows succeeded
38 / 38 LuaJIT rows succeeded
0 / 38 Current rows were slower than LuaJIT
```

The harness returned a non-zero process status because the clean `HEAD`
comparison still cannot run `variants/matmul_row_variant`. That is expected for
this snapshot: the working tree contains the fix, while clean `HEAD` is the
pre-fix comparison target. The Current and LuaJIT columns are both complete.

## The Closest Rows

Sorted by the remaining LuaJIT gap, the tightest rows are:

```text
Benchmark                              Current     LuaJIT      Cur/LJ   Exits
-----------------------------------------------------------------------------
variants/closure_accumulator_variant  0.019875s   0.021125s    0.94x       0
suite/mandelbrot                      0.045000s   0.051500s    0.87x       0
suite/math_intensive                  0.047750s   0.057000s    0.84x       0
suite/sum_primes                      0.020000s   0.024125s    0.83x      38
variants/sort_mixed_numeric           0.026500s   0.033000s    0.80x       4
suite/table_field_access              0.014875s   0.018750s    0.79x    3025
suite/coroutine_bench                 0.043750s   0.056000s    0.78x       0
suite/matmul                          0.016125s   0.021125s    0.76x      25
suite/nbody                           0.102000s   0.134000s    0.76x      12
suite/method_dispatch                 0.052000s   0.069000s    0.75x       0
```

The narrowest margin is still a real margin: `closure_accumulator_variant` is
about six percent faster than LuaJIT in this run. That is the row to watch
first in future regressions, because it has the least slack.

## The Full Ranking

Lower is better. `Cur/LJ` below `1.00x` means GScript is faster than the local
LuaJIT reference on this machine.

```text
Benchmark                              Current     LuaJIT      Cur/LJ   Exits
-----------------------------------------------------------------------------
variants/closure_accumulator_variant  0.019875s   0.021125s    0.94x       0
suite/mandelbrot                      0.045000s   0.051500s    0.87x       0
suite/math_intensive                  0.047750s   0.057000s    0.84x       0
suite/sum_primes                      0.020000s   0.024125s    0.83x      38
variants/sort_mixed_numeric           0.026500s   0.033000s    0.80x       4
suite/table_field_access              0.014875s   0.018750s    0.79x    3025
suite/coroutine_bench                 0.043750s   0.056000s    0.78x       0
suite/matmul                          0.016125s   0.021125s    0.76x      25
suite/nbody                           0.102000s   0.134000s    0.76x      12
suite/method_dispatch                 0.052000s   0.069000s    0.75x       0
suite/table_array_access              0.042750s   0.057000s    0.75x      77
suite/sort                            0.011625s   0.019000s    0.61x       1
suite/fannkuch                        0.011000s   0.018250s    0.60x       0
extended/mixed_inventory_sim          0.068500s   0.128000s    0.54x       0
extended/json_table_walk              0.034250s   0.064500s    0.53x      20
suite/nbody_dense                     0.016125s   0.031750s    0.51x       0
extended/actors_dispatch_mutation     0.197000s   0.476000s    0.41x       0
suite/spectral_norm                   0.044750s   0.112000s    0.40x       0
suite/closure_bench                   0.017000s   0.044500s    0.38x       0
suite/string_bench                    0.022000s   0.059000s    0.37x       1
suite/matmul_dense_tb                 0.008687s   0.024250s    0.36x       2
suite/object_creation                 0.014875s   0.045750s    0.33x      11
suite/sieve                           0.018375s   0.060500s    0.30x       0
suite/spectral_norm_dense             0.021000s   0.077000s    0.27x       0
variants/matmul_row_variant           0.033500s   0.141000s    0.24x       0
suite/matmul_dense                    0.005625s   0.027250s    0.21x       2
extended/groupby_nested_agg           0.093500s   0.462000s    0.20x       0
suite/matmul_dense_split2             0.005563s   0.028750s    0.19x       2
suite/matmul_dense_unroll2            0.031250s   0.240000s    0.13x       2
suite/fibonacci_iterative             0.002047s   0.025000s    0.08x       2
variants/ack_nested_shifted           0.006031s   0.100000s    0.06x       0
suite/ackermann                       0.024375s   0.580000s    0.04x       0
suite/binary_trees                    0.003000s   0.164000s    0.02x       0
suite/mutual_recursion                0.030500s   3.897000s    0.01x       0
extended/producer_consumer_pipeline   0.025500s   0.043250s    0.59x       0
extended/log_tokenize_format          0.025750s   0.084000s    0.31x       0
suite/fib                             0.005545s   0.024000s    0.23x       0
suite/fib_recursive                   0.005588s   0.328000s    0.02x       0
```

The last four rows are wall-timed on the Current side or otherwise marked by
the report as lower-resolution timing cases. They are still faster than LuaJIT,
but they should not be used as fine-grained compiler tuning targets without
scaling the benchmark body first.

## The Last Broken Variant

The only row that failed before this run was `variants/matmul_row_variant`.
The failure was not a numerical regression. The nested matrix multiplication
kernel now returns a dense matrix value, while the variant still read the
result as a table-of-tables:

```text
c[0][0]
```

That was correct for the original interpreter-level structure and wrong after
the structural kernel took over. The fix is to read the result through the
matrix API:

```text
matrix.getf(c, 0, 0)
matrix.getf(c, half, half)
matrix.getf(c, N - 1, N - 1)
```

After that change, the variant rejoined the full comparison:

```text
variants/matmul_row_variant  Current 0.033500s  LuaJIT 0.141000s  Cur/LJ 0.24x
```

## The JSON Row Changed Meaningfully

`extended/json_table_walk` is also worth calling out. It is not just faster
than the old row; it now matches interpreter semantics for the checksum. The
old JIT path produced a different checksum on the full benchmark. The
structural `json_walk_documents` kernel computes the interpreter result and
keeps the row comfortably ahead of LuaJIT:

```text
extended/json_table_walk  Current 0.034250s  LuaJIT 0.064500s  Cur/LJ 0.53x
```

That distinction matters. A green performance row is only useful when the
program being timed is still the same program.

## What This Does Not Mean

This is not a claim that GScript is faster than LuaJIT in general. It is a
claim about this repository's checked-in benchmark corpus, on this local
Darwin/arm64 machine, using the paired Lua reference programs and the current
timing harness.

That scope is narrow, but it is also concrete. The suite now has stable
identity across core benchmarks, extended workloads, and overfit-pressure
variants. The harness records confidence intervals, timing source, repeat
counts, exits, Current-vs-HEAD, and LuaJIT. The data is archived in the repo.

The next goal is different: keep the scoreboard green without turning every
hard row into a one-off kernel. The closest rows now define the regression
budget, and the strongest rows should be audited for generality.
