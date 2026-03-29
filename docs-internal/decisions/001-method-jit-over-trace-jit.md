# ADR-001: Method JIT over Trace JIT

**Date**: 2026-03-15 (decided), recorded 2026-03-29
**Status**: Accepted
**Supersedes**: Trace JIT architecture (deprecated)

## Context

GScript initially used a Trace JIT (modeled on LuaJIT). It achieved 5.8x on nbody and 11x on table_field_access but failed on function-heavy benchmarks: fib 1.0x, spectral_norm 0.9x, mutual_recursion 0.8x. Three attempts to fix this failed:

1. **Trace-through-calls** — follow execution into callees. Store-back corruption from shared `regRegs` across call depths.
2. **Function-entry tracing** — compile function bodies as traces. Got fib(35) to 46ms but broke 8 benchmarks with segfaults and wrong results.
3. **Trace-only rewrite** — deleted 13,000 lines including the working Method JIT. Wrong direction.

The fundamental problem: traces are "blind to functions." A trace captures a linear path through a loop but cannot represent function boundaries, recursion, or branchy code.

## Decision

Pivot to V8-style Method JIT: compile entire functions (not loops) with CFG SSA IR, type feedback, and speculative optimization. Keep the ARM64 assembler and NaN-boxing infrastructure from the trace JIT era.

## Alternatives Considered

| Approach | Result | Why rejected |
|----------|--------|--------------|
| Trace-through-calls | Store-back corruption | No frame-aware snapshots |
| Function-entry tracing | Broke 8/21 benchmarks | No mechanism for returns, variadic, mutual recursion |
| Trace-only rewrite | Lost working Method JIT | Wrong direction |
| Sea of Nodes IR | Too complex | V8 itself is abandoning it for CFG-based |

## Consequences

- **Positive**: Functions, recursion, branches all compile naturally. Universal compilation (every function compiles).
- **Positive**: Aligns with V8/SpiderMonkey/JavaScriptCore — proven architecture at scale.
- **Negative**: More complex IR than traces (need CFG, phi nodes, SSA construction).
- **Negative**: Cannot copy LuaJIT's trace-specific optimizations (trace linking, trace trees).
- **Trade-off**: Taking a different path than LuaJIT means we can't directly use LuaJIT's techniques, but avoids its ceiling on function-heavy code.

## References

- Blog: `docs/14-one-jit-to-rule-them-all.md`, `docs/20-the-pivot.md`
- Research: `docs-internal/research/method-jit-research.md`
