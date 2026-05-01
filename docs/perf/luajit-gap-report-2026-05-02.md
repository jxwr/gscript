# LuaJIT gap report - 2026-05-02

## Scope

This report records the current main worktree benchmark comparison from
2026-05-02. The numbers below are median-of-3 results for GScript default JIT
and the local LuaJIT reference.

It also records a follow-up `strict_guard.py` median-of-5 pass over the current
remaining gaps. That guard is the preferred steering tool for this stage because
it compares `default`, `no_filter`, and `luajit`, preserves checksums, and emits
JSON/Markdown artifacts for ranking.

`0.000s` means the printed timing is below the harness display precision. It
does not mean literal zero runtime.

## Full LuaJIT comparison

| Benchmark | GScript JIT | LuaJIT | JIT/LuaJIT | Status |
| --- | ---: | ---: | ---: | --- |
| fib | 0.000s | 0.025s | n/a | below display precision |
| fib_recursive | 0.000s | 0.324s | n/a | below display precision |
| sieve | 0.004s | 0.010s | 0.40x | ahead |
| mandelbrot | 0.047s | 0.052s | 0.90x | slightly ahead |
| ackermann | 0.000s | 0.006s | n/a | below display precision |
| matmul | 0.008s | 0.021s | 0.38x | ahead |
| spectral_norm | 0.003s | 0.007s | 0.43x | ahead |
| nbody | 0.026s | 0.033s | 0.79x | ahead |
| fannkuch | 0.011s | 0.019s | 0.58x | ahead |
| sort | 0.005s | 0.010s | 0.50x | ahead |
| sum_primes | 0.001s | 0.002s | 0.50x | ahead |
| mutual_recursion | 0.001s | 0.005s | 0.20x | ahead |
| binary_trees | 0.003s | 0.166s | 0.02x | ahead |
| table_field_access | 0.019s | 0.019s | 1.00x | parity |
| table_array_access | 0.019s | 0.010s | 1.90x | behind |
| coroutine_bench | 0.019s | 0.009s | 2.11x | behind |
| closure_bench | 0.014s | 0.009s | 1.56x | behind |
| string_bench | 0.015s | 0.009s | 1.67x | behind |
| fibonacci_iterative | 0.024s | 0.026s | 0.92x | slightly ahead |
| math_intensive | 0.048s | 0.062s | 0.77x | ahead |
| object_creation | 0.003s | 0.008s | 0.38x | ahead |

## Interpretation

The April 29 report is now historical. It correctly identified `matmul`, `fib`,
`spectral_norm`, `sort`, `mutual_recursion`, `sieve`, and `ackermann` as the
largest comparable LuaJIT gaps at that point. The current run no longer
supports that ranking.

Most measured core-suite rows are now ahead of the local LuaJIT reference.
`table_field_access` is at parity. The remaining comparable gaps are
concentrated in:

| Benchmark | GScript JIT | LuaJIT | Gap |
| --- | ---: | ---: | ---: |
| coroutine_bench | 0.019s | 0.009s | 2.11x slower |
| table_array_access | 0.019s | 0.010s | 1.90x slower |
| string_bench | 0.015s | 0.009s | 1.67x slower |
| closure_bench | 0.014s | 0.009s | 1.56x slower |

Those are runtime and microbenchmark-shaped gaps rather than the old broad
numeric, recursive, and table-heavy kernel gaps.

## Strict guard follow-up

Command shape:

```bash
python3 benchmarks/strict_guard.py \
  --bench suite/table_array_access \
  --bench suite/coroutine_bench \
  --bench suite/closure_bench \
  --bench suite/string_bench \
  --bench suite/table_field_access \
  --bench extended/mixed_inventory_sim \
  --bench extended/producer_consumer_pipeline \
  --bench extended/actors_dispatch_mutation \
  --bench extended/json_table_walk \
  --bench extended/log_tokenize_format \
  --mode default --mode no_filter --mode luajit \
  --runs 5 --warmup 1 --timeout 120
```

Ranked by best GScript mode against LuaJIT:

| Benchmark | Best GScript | LuaJIT | Gap |
| --- | ---: | ---: | ---: |
| extended/mixed_inventory_sim | 0.152s | 0.022s | 6.91x slower |
| extended/actors_dispatch_mutation | 0.039s | 0.011s | 3.55x slower |
| extended/producer_consumer_pipeline | 0.127s | 0.043s | 2.95x slower |
| suite/coroutine_bench | 0.019s | 0.00925s | 2.05x slower |
| suite/table_array_access | 0.018s | 0.010s | 1.80x slower |
| extended/json_table_walk | 0.031s | 0.017s | 1.82x slower |
| suite/closure_bench | 0.0145s | 0.00875s | 1.66x slower |
| suite/string_bench | 0.013s | 0.008s | 1.62x slower |
| extended/log_tokenize_format | 0.133s | 0.083s | 1.60x slower |
| suite/table_field_access | 0.019s | 0.019s | parity |

This changes the active work queue. The largest current gaps are now the
extended mixed table and dispatch programs, not the old core numeric kernels.
`no_filter` is mostly neutral on the largest gaps, so broad gate relaxation is
not an evidence-backed direction.

The `json_table_walk` row reflects the follow-up string-format intrinsic change
landed after the initial strict-guard pass. It reduced the no-filter median
from 0.042s to 0.031s and cut the exit stream from roughly 54k exits to 571,
with matching checksums.

## Recommended wording

Use this as the current docs summary:

```text
On the 2026-05-02 median-of-3 local Darwin/arm64 comparison, GScript default
JIT is ahead of the local LuaJIT reference on most measured core-suite rows,
at parity on table_field_access, and still behind on table_array_access,
coroutine_bench, closure_bench, and string_bench. Timings printed as 0.000s
are below benchmark display precision, not literal zero runtime.

On the follow-up median-of-5 strict guard, the largest remaining comparable
gaps are extended/mixed_inventory_sim, extended/actors_dispatch_mutation,
extended/producer_consumer_pipeline, and extended/json_table_walk.
```

Avoid current-tense claims that `matmul`, `fib`, `spectral_norm`, `sort`,
`mutual_recursion`, `sieve`, or `ackermann` are still the largest LuaJIT gaps.
Those claims are now only correct when explicitly framed as April 2026 history.
