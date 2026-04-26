# sum_primes tiering / ModInt diagnostic - 2026-04-27

Branch: `codex/sum-primes-tiering-20260427`
Base: `9b2887c`

## Summary

The current `<main>` gate remains correct. Forcing `<main>` Tier 2 still slows
`sum_primes` because the top-level loop mutates script globals (`sum` and
`count`) and produces a global/op exit storm.

This change keeps the gate closed and instead adds a general guarded scalar
fast path for Tier 2 `ModInt`: when range analysis proves the divisor is
non-zero and operand signs make ARM64 `SDIV/MSUB` match Lua modulo semantics,
the emitter skips both the zero-divisor guard and sign-adjust path.

## Effect

`is_prime` is still the only default Tier 2 body and still runs with zero exits.
The four hot modulo sites in `is_prime` are now emitted as `SDIV/MSUB` only.

Warm dump for `is_prime`:

| Metric | Before | After |
| --- | ---: | ---: |
| ARM64 instructions | 293 | 244 |
| Code bytes | 1172 | 976 |
| Branch instructions | 70 | 46 |
| Exits | 0 | 0 |

The suite wall-time remains at benchmark resolution:

| Runner | 11-sample median |
| --- | ---: |
| pre-change GScript JIT | 0.006s |
| post-change GScript JIT | 0.006s |
| LuaJIT | 0.002s |

## Gate Check

`TIMEOUT_SEC=90 bash benchmarks/diagnose_tier2.sh sum_primes`:

```text
sum_primes vm        0.028s
sum_primes default   0.007s  T2 entered=1 failed=1
  failure: <main>: LoopDepth<2 candidate has read/write global state inside loop
sum_primes no-filter 0.009s  T2 entered=2 failed=0
```

The remaining LuaJIT gap is therefore not solved by this local scalar fast path.
Next useful designs are still: top-level global/local residency with correct
visibility semantics, or a Tier 1 caller to Tier 2 raw-int entry path that avoids
boxed per-iteration calls into `is_prime`.
