# Current regression radar - 2026-04-27

## Scope

This is a documentation-only radar for current `origin/main` after
`41faf03ea9e323b6c82402fc831f6d92516dae90` (`methodjit: keep typed table loads
unboxed`). No performance code was changed.

Focused benchmarks:

- `sum_primes`
- `math_intensive`
- `fannkuch`
- `table_array_access`
- `fibonacci_iterative`
- `sort`

Measurement commands:

```bash
bash benchmarks/regression_guard.sh --runs=5 --timeout=90 \
  --bench sum_primes --bench math_intensive --bench fannkuch \
  --bench table_array_access --bench fibonacci_iterative --bench sort \
  --json /tmp/gscript_regression_guard_20260427_focus.json

TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh \
  sum_primes math_intensive fannkuch table_array_access fibonacci_iterative sort
```

For isolating the `41faf03` delta, the same focused guard was also run in a
temporary detached worktree at parent commit
`b37d2cb76d0bd4aed475ab6bff0f476d94432bc5`.

Environment:

- Darwin arm64
- Go: `go version go1.25.7 darwin/arm64`
- LuaJIT: `/opt/homebrew/bin/luajit`
- Current guard timestamp: `2026-04-26T19:20:42Z`
- Parent guard timestamp: `2026-04-26T19:21:44Z`

## Headline

`41faf03` does not introduce a new focused regression versus its parent. The
only clear direct movement is positive: `table_array_access` improves from
`0.049s` to `0.044s` median (`-10.2%`).

The current guard still reports two regressions versus
`benchmarks/data/baseline.json`: `sum_primes` and `math_intensive`. Those are
present on the parent as well, so they should be treated as current-main debt,
not as regressions introduced by `41faf03`.

## Focused guard

`baseline drift` is against `benchmarks/data/baseline.json`. `41faf03 delta` is
current main versus parent `b37d2cb`.

| Benchmark | Baseline JIT | Parent JIT | 41faf03 JIT | 41faf03 delta | Baseline drift | LuaJIT | JIT/LuaJIT | T2 a/e/f | Exits |
|-----------|-------------:|-----------:|------------:|--------------:|---------------:|-------:|----------:|:--------:|------:|
| `sum_primes` | 0.004s | 0.006s | 0.006s | +0.0% | +50.0% REG | 0.002s | 3.00x | 2/1/1 | 0 |
| `math_intensive` | 0.070s | 0.097s | 0.096s | -1.0% | +37.1% REG | n/a | n/a | 4/4/0 | 1 |
| `fannkuch` | 0.049s | 0.053s | 0.052s | -1.9% | +6.1% | 0.020s | 2.60x | 1/1/0 | 12 |
| `table_array_access` | 0.097s | 0.049s | 0.044s | -10.2% | -54.6% | n/a | n/a | 5/5/0 | 3294 |
| `fibonacci_iterative` | 0.291s | 0.248s | 0.242s | -2.4% | -16.8% | n/a | n/a | 1/1/0 | 0 |
| `sort` | 0.051s | 0.050s | 0.050s | +0.0% | -2.0% | 0.010s | 5.00x | 3/0/3 | 0 |

LuaJIT notes:

- `sum_primes`, `fannkuch`, and `sort` have matching Lua files and valid ratios.
- `math_intensive`, `table_array_access`, and `fibonacci_iterative` have no
  matching `benchmarks/lua/<benchmark>.lua`, so ratio is not measurable from the
  current suite.

## Diagnose readings

| Benchmark | Default diagnose | No-filter diagnose | Main signal |
|-----------|-----------------:|-------------------:|-------------|
| `sum_primes` | 0.006s, T2 entered 1, failed 1 | 0.009s, T2 entered 2, failed 0 | `<main>` is blocked by read/write global state in a LoopDepth<2 candidate; no-filter is slower, so the gate is protecting this case. |
| `math_intensive` | 0.096s, T2 entered 4, failed 0 | 0.096s, T2 entered 4, failed 0 | Regression is not a filter artifact; investigate generated compiled path or earlier regression range. |
| `fannkuch` | 0.053s, T2 entered 1, failed 0 | 0.052s, T2 entered 1, failed 0 | Correct checksum (`8629`) and stable; LuaJIT gap remains 2.6x. |
| `table_array_access` | 0.043s, T2 entered 5, failed 0 | 0.044s, T2 entered 5, failed 0 | Correct sub-results; `41faf03` improves the benchmark but leaves a high exit count (`3294`). |
| `fibonacci_iterative` | 0.232s, T2 entered 1, failed 0 | 0.242s, T2 entered 1, failed 0 | Stable/improved versus baseline; no LuaJIT port. |
| `sort` | 0.051s, T2 entered 0, failed 3 | 0.049s, T2 entered 1, failed 2 | Still structurally blocked: main loop call, generic mod in `make_random_array`, and quicksort table mutation exit-storm gate. |

## Priority

1. `math_intensive`: highest regression-debt priority. It is +37.1% versus the
   frozen guard baseline and no-filter does not change it, so the next useful
   step is a narrow regression-range/IR-asm comparison around the changes before
   `41faf03`, not another gate experiment.
2. `sum_primes`: second regression-debt priority. It is +50.0% versus baseline
   but only +2 ms absolute; no-filter worsens it, so keep the current gate and
   inspect why the default path lost the old 0.004s median.
3. `sort`: highest LuaJIT-ratio priority among this set (`5.00x`). It is stable
   versus parent and baseline, but still has three Tier 2 failures in default
   mode. Treat as structural gap work, not a 41faf03 regression.
4. `fannkuch`: next LuaJIT-ratio target (`2.60x`) after sort. It is stable and
   correct, so prioritize only after the regression-debt items.
5. `table_array_access`: `41faf03` is a real win here (`-10.2%` versus parent).
   Follow-up should focus on explaining or reducing the remaining 3294 exits,
   not on reverting the current change.
6. `fibonacci_iterative`: low immediate priority. It is improved versus both
   parent and baseline, has no exits, and has no LuaJIT ratio in the suite.
