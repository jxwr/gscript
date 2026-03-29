# ADR-002: Three-Tier Architecture

**Date**: 2026-03-28 (decided), recorded 2026-03-29
**Status**: Accepted

## Context

The single-tier Method JIT (v3) failed due to its emission layer. It tried to handle both NaN-boxed values and raw integers with implicit mode flags spread across 14 files. Results: sieve ran 470x slower than LuaJIT (type guards fired every iteration); fibonacci_iterative returned 0 (register clobbered between phi resolution and first use). The IR, passes, and register allocation were all correct — the bugs were always in the emission layer.

## Decision

Split into three tiers like V8 (Sparkplug → Maglev → TurboFan):

- **Tier 0: Interpreter** — executes all bytecodes, collects type feedback (FeedbackVector)
- **Tier 1: Baseline JIT** — 1:1 bytecode-to-ARM64, no IR, no optimization, no rejection. Every value stays NaN-boxed. Compiles at first call.
- **Tier 2: Optimizing JIT** — CFG SSA IR, type-specialized registers, deopt guards. Compiles at 500+ calls with stable feedback.

Each tier has its own emission layer. The upper half (IR, passes, regalloc) is shared.

## Alternatives Considered

| Approach | Why rejected |
|----------|--------------|
| Fix single emission layer incrementally | Each fix broke something else (14 files with implicit state) |
| Skip baseline, go straight to optimizing | No fast startup, no correctness fallback |
| Two tiers only (interpreter + optimizing) | Large gap between interpreter and optimizing JIT, long warmup |

## Consequences

- **Positive**: Tier 1 is "correct by inspection" — no optimization means no optimization bugs.
- **Positive**: Clean separation of concerns. Tier 1 has simple templates; Tier 2 has full optimization pipeline.
- **Positive**: 4,200 lines of proven IR infrastructure preserved with 200+ tests.
- **Negative**: Two emission layers to maintain. Shared assembler layer mitigates this.
- **Trade-off**: More code total, but each piece is independently verifiable.

## Key Principle

"Keeping broken code because it was expensive to write is the sunk cost fallacy." — 5,600 lines of broken emission code deleted, replaced with two clean layers.

## References

- Blog: `docs/21-three-tiers.md`
