# LuaJIT gap guard report - 2026-04-27

Branch: `codex/luajit-gap-report-20260427`
Base commit: `b8013202fedccc39413625453b3eba919326bdc4`
Platform: `darwin/arm64`, `go1.25.7`, LuaJIT at `/opt/homebrew/bin/luajit`

## Scope

This is a representative guard run, not a statistically stable benchmark
publication. I used one sample per benchmark to keep the run bounded while still
covering the full guard surface:

```sh
bash benchmarks/regression_guard.sh \
  --runs=1 \
  --timeout=30 \
  --json benchmarks/data/luajit_gap_guard_20260427.json
```

The guard exits non-zero when it sees regressions against
`benchmarks/data/baseline.json`; in this run it completed and reported three
regression rows.

I then ran focused Tier 2 diagnostics on the nearest/farthest LuaJIT rows and
the interesting guard failures:

```sh
TIMEOUT_SEC=30 bash benchmarks/diagnose_tier2.sh \
  mandelbrot nbody matmul fannkuch sum_primes sort \
  closure_bench binary_trees coroutine_bench
```

## LuaJIT comparison

Only rows with a usable LuaJIT `Time:` line are included here. Lower `JIT/LJ`
is closer to LuaJIT; values below `1.0x` mean GScript was faster in this run.

| Benchmark | VM | GScript JIT | LuaJIT | JIT/VM | JIT/LJ | Tier2 attempted/entered/failed | Exits |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| mandelbrot | 1.411s | 0.050s | 0.058s | 28.22x | 0.86x | 1/1/0 | 0 |
| nbody | 1.936s | 0.087s | 0.034s | 22.25x | 2.56x | 3/3/0 | 124 |
| ackermann | 0.311s | 0.017s | 0.006s | 18.29x | 2.83x | 2/2/0 | 535 |
| fannkuch | 0.566s | 0.063s | 0.020s | 8.98x | 3.15x | 1/1/0 | 9 |
| sum_primes | 0.028s | 0.007s | 0.002s | 4.00x | 3.50x | 2/1/1 | 0 |
| fib | 1.733s | 0.091s | 0.025s | 19.04x | 3.64x | 1/1/0 | 2 |
| mutual_recursion | 0.220s | 0.016s | 0.004s | 13.75x | 4.00x | 3/3/0 | 1094 |
| spectral_norm | 1.051s | 0.033s | 0.008s | 31.85x | 4.12x | 4/3/1 | 1 |
| sort | 0.185s | 0.050s | 0.011s | 3.70x | 4.55x | 3/1/2 | 17 |
| sieve | 0.253s | 0.053s | 0.011s | 4.77x | 4.82x | 2/2/0 | 63 |
| matmul | 1.086s | 0.136s | 0.022s | 7.99x | 6.18x | 2/2/0 | 912 |

Closest to LuaJIT:

- `mandelbrot`: `0.050s` vs LuaJIT `0.058s` (`0.86x`), with Tier2 entering
  cleanly and zero exits.
- `nbody`: `0.087s` vs `0.034s` (`2.56x`), no Tier2 failures but still 124
  exits.
- `ackermann`: `0.017s` vs `0.006s` (`2.83x`), fast but still exit-heavy.

Farthest from LuaJIT among comparable rows:

- `matmul`: `0.136s` vs `0.022s` (`6.18x`), despite both Tier2 candidates
  entering. The focused diagnostic repeated the same shape at `0.142s` for
  both default and no-filter, so this is not primarily a Tier2 admission gate.
- `sieve`: `0.053s` vs `0.011s` (`4.82x`), with Tier2 entered but 63 exits.
- `sort`: `0.050s` vs `0.011s` (`4.55x`), where the remaining quicksort body is
  deliberately blocked from Tier2.

## Guard regressions

Against `benchmarks/data/baseline.json`, the one-sample guard reported:

| Benchmark | Current JIT | Baseline JIT | Delta |
| --- | ---: | ---: | ---: |
| matmul | 0.136s | 0.123s | +10.6% |
| fannkuch | 0.063s | 0.049s | +28.6% |
| sum_primes | 0.007s | 0.004s | +75.0% |

These are smoke-run signals, not final regression verdicts. They are still worth
tracking because all three sit on active optimization paths:

- `matmul` is the current largest LuaJIT gap and has a real baseline guard hit.
- `fannkuch` remains much faster than VM, but the guard says it lost ground
  versus the saved baseline.
- `sum_primes` is short enough that one sample is noisy, but the diagnostic
  shows a Tier2 filter miss in default mode.

## Tier2 diagnostic notes

Focused diagnostic results:

| Benchmark | Default | No-filter | Tier2 note |
| --- | ---: | ---: | --- |
| mandelbrot | 0.053s | 0.052s | 1/1 entered; no failures |
| nbody | 0.087s | 0.087s | 3/3 entered; no failures |
| matmul | 0.142s | 0.142s | 2/2 entered; no failures |
| fannkuch | 0.066s | 0.065s | 1/1 entered; no failures |
| sum_primes | 0.006s | 0.009s | default blocks one LoopDepth<2 global-state candidate |
| sort | 0.051s | 0.050s | default blocks `<main>` Call-in-loop and quicksort recursive SetTable |
| closure_bench | 0.029s | 0.029s | default blocks `map_array` Call-in-loop |
| binary_trees | 1.184s | 1.342s | no-filter is slower; default gate is protective |
| coroutine_bench | 2.784s | 1.846s | no Tier2 entry in either mode |

The useful split is:

- `mandelbrot`, `nbody`, `matmul`, `fannkuch`: not blocked by the default Tier2
  filter. Further gains need better generated code, fewer exits, or runtime
  fast paths rather than simply admitting more candidates.
- `sort`, `closure_bench`, `binary_trees`: still gated by Call-in-loop or
  recursive table mutation. The existing sort diagnostic already showed that
  opening the recursive mixed table mutation path can make no-filter time out,
  so these gates should stay conservative.
- `coroutine_bench`: still slower than VM in default JIT (`2.269s` in guard,
  `2.784s` in focused diagnostic) and has no Tier2 path; treat as a separate
  runtime/coroutine overhead problem.

## Recent improvement picture

Compared with the older v3 report, the current mainline is in a different
performance regime:

| Benchmark | Older v3 JIT | Current guard JIT | Change |
| --- | ---: | ---: | ---: |
| mandelbrot | 1.433s | 0.050s | about 29x faster; now near LuaJIT |
| matmul | 1.045s | 0.136s | about 7.7x faster, but still farthest from LuaJIT |
| spectral_norm | 1.004s | 0.033s | about 30x faster |
| nbody | HANG / later 0.245s | 0.087s | hang fixed; about 2.8x faster than recent saved data |
| fib | 1.383s | 0.091s | about 15x faster |
| fib_recursive | 18.446s | 0.676s | about 27x faster |
| ackermann | 0.270s | 0.017s | about 16x faster |
| object_creation | 0.755s | 0.005s | about 151x faster |
| coroutine_bench | 16.550s reference / 4.720s v3 | 2.269s | improved, but still slower than VM |

The recent commit series explains most of that shift:

- call boundary and call entry protocol work moved recursion-heavy cases from
  JIT-slower or barely faster into the sub-second range.
- float and numeric table work moved `mandelbrot`, `spectral_norm`, `nbody`,
  and `matmul` out of interpreter-equivalent performance.
- table preallocation, array append, shared heap allocation, and raw peer call
  work improved table-heavy loops, while also surfacing narrower regressions in
  `fannkuch`, `sum_primes`, and `matmul`.
- guard/reporting work now makes the remaining tradeoffs visible: no-filter can
  help some rows (`coroutine_bench`, `fibonacci_iterative`, `string_bench`) but
  can also hurt (`binary_trees`) or expose unsafe paths (`sort`).

## Next priorities

1. `matmul`: highest-priority LuaJIT gap. Tier2 already enters, so focus on exit
   profile and generated code shape, especially table numeric load/store and
   float arithmetic paths. The 912 exits make it the clearest current gap.
2. `sieve` and `sort`: both are table-heavy and still 4.5-4.8x off LuaJIT.
   `sieve` enters Tier2 but still exits; `sort` needs a recursive-safe mixed
   array swap strategy rather than a broad gate exception.
3. `fannkuch` and `sum_primes`: investigate guard regressions first. They are
   small enough that repeated runs are needed, but both represent active paths
   where recent work may have traded a local win for a guard loss.
4. `nbody`: now reasonably close to LuaJIT, but its 124 exits are still a
   concrete target after `matmul`.
5. `coroutine_bench`: keep separate from Tier2 work. The current JIT path is
   slower than VM and Tier2 does not enter, so this likely needs runtime or
   coroutine dispatch work, not admission tuning.

## Verification

- `bash benchmarks/regression_guard.sh --runs=1 --timeout=30 --json ...`
  completed and wrote JSON, then exited `1` because the guard found three
  baseline regressions.
- `TIMEOUT_SEC=30 bash benchmarks/diagnose_tier2.sh ...` completed for the
  focused set.
- No core code or benchmark scripts were changed for this report.
