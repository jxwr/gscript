# ADR-004: Op-Exit Resume for Unsupported Operations

**Date**: 2026-03-16 (decided), recorded 2026-03-29
**Status**: Accepted

## Context

The Method JIT cannot natively emit ARM64 code for every bytecode operation (function calls, globals, tables, strings, closures, channels). The trace JIT's approach was to reject functions containing unsupported ops — this blocked 18 of 21 benchmarks from compiling (a "design mistake").

## Decision

**Every function compiles, no rejection.** Operations the JIT cannot emit natively use **op-exit resume**: the JIT exits to Go code at that instruction, Go performs the operation, then the JIT resumes at the next instruction.

The prologue contains a dispatch table keyed by PC, so the JIT can jump back to any instruction after an exit.

## Alternatives Considered

| Approach | Why rejected |
|----------|--------------|
| Reject functions with unsupported ops | Blocked 18/21 benchmarks — the worst design mistake in the project |
| Implement all ops natively | Too much work for MVP; some ops (string concat, closures) need Go runtime |
| Side-exit to interpreter permanently | Loses the rest of the function's native execution |

## Consequences

- **Positive**: Universal compilation — every benchmark runs through JIT.
- **Positive**: New ops can be added incrementally. Start with op-exit, replace with native emission when it matters.
- **Negative**: ~55ns overhead per exit (Go call overhead). Functions with many unsupported ops see limited speedup.
- **Trade-off**: Slower than native for unsupported ops, but infinitely faster than not compiling at all.

## Performance Impact

- Native ops: int/float arithmetic, comparisons, branches, loops, constants → fast path
- Exit-resume ops: function calls, globals, tables, strings, closures, channels → ~55ns per exit
- Future: migrate exit-resume ops to native as they become bottlenecks

## References

- Research: `docs-internal/research/SYNTHESIS.md` (identifies unblocking compilation as the single highest-impact change)
