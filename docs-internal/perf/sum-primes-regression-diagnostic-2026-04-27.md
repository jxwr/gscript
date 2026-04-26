# sum_primes frozen-baseline regression diagnostic

Date: 2026-04-27
Branch: `codex/sum-primes-regression-20260427`
Current head investigated: `93e25ff` (`origin/main`)

## Summary

`sum_primes` remains above the frozen baseline (`0.004s` baseline vs current
`~0.006s`), but the current evidence does not identify a safe general code fix.
The hot callee `is_prime` still enters Tier 2 and runs with zero exits. The
top-level `<main>` Tier 2 path is correctly blocked: forcing it with
`GSCRIPT_TIER2_NO_FILTER=1` slows the benchmark and produces a large exit storm
from global `sum`/`count` reads and writes.

This is therefore a diagnostic hold, not a code-change round.

## Validation

Focused diagnostic on `origin/main`:

```text
TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh sum_primes math_intensive

sum_primes vm        0.028s
sum_primes default   0.007s  T2 entered=1 failed=1
  failure: <main>: tier2: LoopDepth<2 candidate has read/write global state inside loop, staying at Tier 1
sum_primes no-filter 0.009s  T2 entered=2 failed=0

math_intensive vm        0.956s
math_intensive default   0.097s  T2 entered=4 failed=0
math_intensive no-filter 0.096s  T2 entered=4 failed=0
```

Before `origin/main` advanced, the same shape was also validated on `4165a8e`:
`sum_primes` was `0.006s` default and `0.008s` no-filter; `math_intensive`
was `0.091s` default and `0.092s` no-filter.

## History Probe

Single-run probe of key commits:

| Commit | Time | T2 entered/failed | Relevant status |
| --- | ---: | ---: | --- |
| `9bb4fa9` frozen baseline commit | `0.007s` | `0/0` | old CLI stats only reports `is_prime` in failed list shape |
| `30fd5c1` boxed raw-int kernel gate | `0.006s` | `1/1` | `is_prime` entered; `<main>` blocked on call-in-loop |
| `4f91360` float mod gate | `0.005s` | `1/1` | same shape |
| `555d996` exit-state checker era | `0.005s` | `1/1` | same shape |
| `d86d0a1` stable raw-int loop callees | `0.006s` | `1/1` | same shape |
| `56e08a2` exit-storm loop guard | `0.006s` | `0/2` | `is_prime` blocked by generic mod-in-loop gate |
| `b37d2cb` preserve Tier2 int mod semantics | `0.006s` | `1/1` | `is_prime` entered; `<main>` blocked on global state |
| `7f7ce9d` prior `origin/main` | `0.006s` | `1/1` | same current shape |
| `93e25ff` current `origin/main` | `0.007s` | `1/1` | same current shape |

The regression is not isolated to the recent int-mod or range commits. Those
commits changed the `is_prime` Tier2 IR/asm shape, but not the benchmark-level
gate decision, and the benchmark is already at `~0.006s` before `b37d2cb`.

## Warm Dump Comparison

Warm dumps compared:

```text
555d996:
  is_prime entered=true, compiled=true, failed=false
  insn_count=500, code_bytes=2000, histogram={branch:118,dpi:139,dpr:97,fp:63,load:38,store:45}
  exits=0
  <main> failed: LoopDepth<2 candidate has exit-resume-prone op Call inside loop

7f7ce9d / 93e25ff:
  is_prime entered=true, compiled=true, failed=false
  insn_count=293, code_bytes=1172, histogram={branch:70,dpi:71,dpr:67,load:36,store:49}
  exits=0
  <main> failed: LoopDepth<2 candidate has read/write global state inside loop
```

`is_prime` IR changed in the while loop from generic numeric ops:

```text
Mul / Le / Mod / Eq / Add
```

to typed integer ops:

```text
MulInt / LeInt / ModInt / EqInt / AddInt
```

The current asm is substantially smaller and removes the old float conversion
fallbacks from the loop body. Current `ModInt` emits full Lua modulo semantics:
zero-divisor deopt plus sign-adjust (`CBZ`, `SDIV`, `MSUB`, `CBZ`, `EOR`,
`TBNZ`, conditional `ADD`). That is a plausible remaining micro-cost in
`is_prime`, but a safe optimization would need path/range facts proving positive,
non-zero divisors and non-negative dividends at each modulo site.

## No-Filter Exit Profile

Forcing `<main>` Tier2 on current head:

```text
GSCRIPT_TIER2_NO_FILTER=1 /tmp/gscript_sum_current_93 -jit -jit-stats -exit-stats \
  -jit-dump-warm /tmp/sum_current_93_nofilter_dump benchmarks/suite/sum_primes.gs

Time: 0.009s
total exits: 38402
  ExitGlobalExit: 19197
  ExitOpExit:     19193
  ExitCallExit:       8
  ExitTableExit:      4

hot sites:
  9592 GetGlobal for sum
  9592 GetGlobal for count
  9592 SetGlobal for sum
  9592 SetGlobal for count
```

This validates the current `<main>` Tier1 gate. Opening it would regress
`sum_primes` unless global `sum`/`count` are localized or Tier2 gains a real
global-store residency scheme with correct visibility semantics.

## Candidate Future Fixes

1. Add path/range-aware modulo fast paths for proven positive int operands.
   This would target `is_prime` directly, but it needs verifier-backed facts for
   branch-refined parameter ranges, not just local constant divisors.

2. Add a global-localization or global-store residency optimization for top-level
   script locals lowered as globals. This would be broader than `sum_primes`, but
   the invalidation and externally visible global semantics need a design pass.

Neither is small enough to land as a safe general fix in this diagnostic round.
