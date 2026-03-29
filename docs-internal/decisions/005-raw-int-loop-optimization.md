# ADR-005: Raw-Int Loop Optimization (NaN-Boxing with Raw Integers)

**Date**: 2026-03-21 (decided), recorded 2026-03-29
**Status**: Accepted

## Context

GScript uses NaN-boxing for value representation: every value is a `uint64` where float64 is raw IEEE 754 bits, and other types use the NaN space for type tags. In JIT-compiled tight loops with integer arithmetic, every operation requires unbox → compute → rebox, adding 2-3 extra ARM64 instructions per operation.

## Decision

In Tier 2 (optimizing JIT), when type feedback confirms a loop variable is always an integer, the JIT emits **raw integer arithmetic** using ARM64's 64-bit integer registers directly — no boxing/unboxing inside the loop. Type guards at loop entry check that the value is actually an integer; if not, deoptimize.

A pinned register `X24` holds the int tag constant, reducing boxing from 3 instructions to 2 when reboxing is needed.

## Alternatives Considered

| Approach | Why rejected |
|----------|--------------|
| Always NaN-boxed (Tier 1 style) | 2-3x overhead on tight integer loops |
| Full unboxing everywhere | Requires escape analysis, complex deopt |
| Tagged pointers (LuaJIT style) | Limits to 47-bit integers on ARM64; NaN-boxing already in place |

## Consequences

- **Positive**: Tight integer loops run at near-native speed. Sum10000 achieved 21.4x over interpreter.
- **Positive**: Pinned X24 reduces reboxing overhead when transitioning between raw and boxed.
- **Negative**: Type guards add overhead on loop entry. If guards fail frequently, deopt cost is high.
- **Negative**: Limits integers to 48-bit payload in NaN-boxing (sufficient for most benchmarks).
- **Trade-off**: Speculative optimization — fast on the happy path, deopt on type mismatch.

## Key Metric

Sum10000: 21.4x over interpreter (raw-int loop). This is the primary validation of the approach.

## References

- Blog: `docs/11-eight-bytes-that-change-everything.md`
