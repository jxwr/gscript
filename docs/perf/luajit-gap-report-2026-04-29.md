# LuaJIT gap report - 2026-04-29

Branch: `codex/post-breakthrough-luajit-gap-20260429235747`
Base commit: `d165661f82acbb5e3cea631f4d389f236f5ed5bb`
Platform: `Darwin arm64`, `go version go1.25.7 darwin/arm64`
LuaJIT: `/opt/homebrew/bin/luajit`

## Scope

This report captures the post-fixed-recursive-table-fold state at current
`origin/main`. The full guard was run as median-of-3 across VM, default JIT,
no-filter JIT, and LuaJIT:

```sh
bash benchmarks/regression_guard.sh \
  --runs=3 \
  --timeout=90 \
  --json /private/tmp/gscript-gap-20260429.json \
  --csv /private/tmp/gscript-gap-20260429.csv \
  --markdown /private/tmp/gscript-gap-20260429.md
```

The guard completed in 73.120s and exited 0. It reported zero regressions
against `benchmarks/data/baseline.json`.

I then ranked comparable LuaJIT rows with:

```sh
benchmarks/rank_luajit_gaps.py /private/tmp/gscript-gap-20260429.json --top=12
```

and ran focused Tier 2 diagnostics on the largest comparable gaps:

```sh
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh \
  matmul fib spectral_norm sort mutual_recursion sieve ackermann nbody fannkuch
```

## Full guard summary

| Benchmark | VM | Default JIT | NoFilter | LuaJIT | JIT/VM | JIT/LJ | Baseline | Regress | T2 a/e/f | Exits |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| fib | 0.824s | 0.088s | 0.087s | 0.025s | 9.36x | 3.52x | 1.462s | -94.0% | 1/1/0 | 0 |
| fib_recursive | 8.080s | 0.585s | 0.575s | missing | 13.81x | - | 14.479s | -96.0% | 2/2/0 | 14 |
| sieve | 0.246s | 0.027s | 0.027s | 0.010s | 9.11x | 2.70x | 0.088s | -69.3% | 2/2/0 | 18 |
| mandelbrot | 1.403s | 0.051s | 0.050s | 0.057s | 27.51x | 0.89x | 0.063s | -19.0% | 1/1/0 | 0 |
| ackermann | 0.165s | 0.014s | 0.014s | 0.006s | 11.79x | 2.33x | 0.270s | -94.8% | 2/2/0 | 14 |
| matmul | 1.024s | 0.081s | 0.080s | 0.021s | 12.64x | 3.86x | 0.123s | -34.1% | 2/2/0 | 32 |
| spectral_norm | 0.744s | 0.024s | 0.024s | 0.007s | 31.00x | 3.43x | 0.045s | -46.7% | 4/4/0 | 38 |
| nbody | 1.897s | 0.059s | 0.058s | 0.034s | 32.15x | 1.74x | 0.248s | -76.2% | 3/3/0 | 80 |
| fannkuch | 0.552s | 0.039s | 0.041s | 0.020s | 14.15x | 1.95x | 0.049s | -20.4% | 1/1/0 | 4 |
| sort | 0.176s | 0.033s | 0.034s | 0.010s | 5.33x | 3.30x | 0.051s | -35.3% | 1/1/0 | 4 |
| sum_primes | 0.025s | 0.003s | 0.003s | 0.002s | 8.33x | 1.50x | 0.004s | -25.0% | 2/2/0 | 15 |
| mutual_recursion | 0.116s | 0.015s | 0.015s | 0.005s | 7.73x | 3.00x | 0.189s | -92.1% | 3/3/0 | 20 |
| method_dispatch | 0.035s | 0.001s | 0.001s | 0.000s | 35.00x | - | 0.101s | -99.0% | 1/1/0 | 0 |
| closure_bench | 0.045s | 0.020s | 0.019s | no_time | 2.25x | - | 0.028s | -28.6% | 0/0/0 | 0 |
| string_bench | 0.040s | 0.022s | 0.020s | no_time | 1.82x | - | 0.030s | -26.7% | 0/0/0 | 0 |
| binary_trees | 0.603s | 0.373s | 0.369s | no_time | 1.62x | - | 2.006s | -81.4% | 1/1/0 | 0 |
| table_field_access | 0.738s | 0.020s | 0.021s | missing | 36.90x | - | 0.043s | -53.5% | 3/2/1 | 24 |
| table_array_access | 0.413s | 0.030s | 0.029s | missing | 13.77x | - | 0.097s | -69.1% | 5/5/0 | 102 |
| coroutine_bench | 0.026s | 0.026s | 0.026s | missing | 1.00x | - | 15.266s | -99.8% | 0/0/0 | 0 |
| fibonacci_iterative | 1.048s | 0.026s | 0.026s | missing | 40.31x | - | 0.291s | -91.1% | 2/2/0 | 2 |
| math_intensive | 0.898s | 0.052s | 0.052s | missing | 17.27x | - | 0.070s | -25.7% | 5/4/0 | 0 |
| object_creation | 0.229s | 0.004s | 0.004s | missing | 57.25x | - | 1.141s | -99.6% | 3/3/0 | 0 |

## Ranked LuaJIT gaps

Rows with missing, zero-time, or no-`Time:` LuaJIT output are excluded.

| Rank | Benchmark | VM | Default | NoFilter | LuaJIT | Default/LuaJIT | NoFilter/LuaJIT | JIT/VM | T2 a/e/f | Exits |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | matmul | 1.024s | 0.081s | 0.080s | 0.021s | 3.86x | 3.81x | 12.64x | 2/2/0 | 32 |
| 2 | fib | 0.824s | 0.088s | 0.087s | 0.025s | 3.52x | 3.48x | 9.36x | 1/1/0 | 0 |
| 3 | spectral_norm | 0.744s | 0.024s | 0.024s | 0.007s | 3.43x | 3.43x | 31.00x | 4/4/0 | 38 |
| 4 | sort | 0.176s | 0.033s | 0.034s | 0.010s | 3.30x | 3.40x | 5.33x | 1/1/0 | 4 |
| 5 | mutual_recursion | 0.116s | 0.015s | 0.015s | 0.005s | 3.00x | 3.00x | 7.73x | 3/3/0 | 20 |
| 6 | sieve | 0.246s | 0.027s | 0.027s | 0.010s | 2.70x | 2.70x | 9.11x | 2/2/0 | 18 |
| 7 | ackermann | 0.165s | 0.014s | 0.014s | 0.006s | 2.33x | 2.33x | 11.79x | 2/2/0 | 14 |
| 8 | fannkuch | 0.552s | 0.039s | 0.041s | 0.020s | 1.95x | 2.05x | 14.15x | 1/1/0 | 4 |
| 9 | nbody | 1.897s | 0.059s | 0.058s | 0.034s | 1.74x | 1.71x | 32.15x | 3/3/0 | 80 |
| 10 | sum_primes | 0.025s | 0.003s | 0.003s | 0.002s | 1.50x | 1.50x | 8.33x | 2/2/0 | 15 |
| 11 | mandelbrot | 1.403s | 0.051s | 0.050s | 0.057s | 0.89x | 0.88x | 27.51x | 1/1/0 | 0 |

## Focused diagnostic notes

The largest gaps are not primarily Tier 2 admission failures:

| Benchmark | Default | NoFilter | Diagnostic read |
|---|---:|---:|---|
| matmul | 0.086s | 0.091s | 2 Tier 2 entries, 0 failures; no-filter is worse. |
| fib | 0.088s | 0.089s | 1 Tier 2 entry, 0 failures, 0 guard exits in guard run. |
| spectral_norm | 0.030s | 0.024s | 4 entries, 0 failures; possible minor admission or variance signal, but guard medians were equal. |
| sort | 0.034s | 0.035s | No-filter attempts quicksort and records residual self-recursive SetTable mutation as blocked. |
| mutual_recursion | 0.018s | 0.015s | 3 entries, 0 failures; no-filter sample improved, guard medians were equal. |
| sieve | 0.034s | 0.028s | 2 entries, 0 failures; guard medians were equal at 0.027s. |
| ackermann | 0.015s | 0.015s | 2 entries, 0 failures. |
| nbody | 0.061s | 0.060s | 3 entries, 0 failures; still the highest exit count among comparable rows. |
| fannkuch | 0.041s | 0.041s | 1 entry, 0 failures. |

## Interpretation

The fixed recursive table fold breakthrough changed the shape of the gap
report. Baseline regressions are gone, recursion-heavy benchmarks are now much
closer to LuaJIT, and the default filter is no longer hiding a broad class of
obvious wins. `NoFilter` is neutral on the median guard for most comparable
rows and worse on `sort` and `fannkuch`.

`mandelbrot` remains past LuaJIT in this run: default JIT `0.051s` versus
LuaJIT `0.057s`.

The next 2x-class target should be `matmul`. It has the largest remaining
LuaJIT ratio (`3.86x`), a large absolute gap (`0.081s` versus `0.021s`), clean
Tier 2 entry (`2/2/0`), and no-filter is neutral-to-worse. That points away
from admission policy and toward generated code/runtime cost: nested numeric
table row access, array element load/store shape, and the remaining 32 exits.

`fib` is the second-largest ratio (`3.52x`) and has zero guard exits, so it is
also a strong target, but it needs a call-protocol/body-cost diagnostic before
editing. It is less likely to be unlocked by simple admission changes.

## Recommended worker tasks

1. `matmul` worker: profile Tier 2 generated code and exit classes for the
   nested-table benchmark. Acceptance should be median-of-5 `matmul` default
   JIT <= `0.040s` with no regression over 3% on `spectral_norm`, `nbody`,
   `sieve`, and `sort`.
2. `fib` worker: produce a no-code diagnostic comparing recursive self-call
   body cost against LuaJIT after the fixed fold path. Count native calls, exit
   count, boxed value traffic, and emitted instruction shape. Do not start with
   an implementation.
3. `spectral_norm`/`sieve` worker: investigate the diagnostic-only no-filter
   signal with 15 interleaved samples before editing. Guard medians do not yet
   prove a filter issue.
4. `sort` worker: leave the broad no-filter path closed. The diagnostic still
   reports self-recursive residual table mutation in quicksort; a future change
   needs a narrow, mutation-safe array swap strategy.

## Reproducibility change

Added `benchmarks/rank_luajit_gaps.py`. It reads a
`benchmarks/regression_guard.py` JSON output artifact and prints a deterministic
LuaJIT gap ranking in Markdown or CSV, excluding missing or invalid LuaJIT
rows.
