# ADR-006: Forward-Walk Register Allocation

**Date**: 2026-03-17 (decided), recorded 2026-03-29
**Status**: Accepted

## Context

The Method JIT needs register allocation for its SSA IR. ARM64 has 29 general-purpose registers (X0-X28) and 32 floating-point registers (D0-D31). However, some are reserved: X0 for return values, X16-X17 for platform calls, X19-X23 for callee-saved, X24 for int tag, X28 for stack pointer. This leaves 5 allocatable GPRs and 8 allocatable FPRs.

## Decision

Use **forward-walk register allocation** (single pass through the instruction stream in program order). For each instruction, allocate registers to inputs and outputs based on liveness (next-use distance). No separate liveness analysis pass needed.

This is similar to V8 Maglev's approach: simple, fast to compile, good enough for the number of available registers.

## Alternatives Considered

| Approach | Why rejected |
|----------|--------------|
| Graph-coloring (Chaitin) | NP-complete, overkill for 5 GPRs. High compile time. |
| Linear scan (Traub et al.) | Requires separate liveness analysis pass. More complex implementation. |
| SSA-based (Brisk et al.) | Optimal but complex. Justified when registers are scarce; 5+8 is enough for most functions. |

## Consequences

- **Positive**: O(n) compile time, where n is instruction count. Fast compilation.
- **Positive**: Simple implementation (~200 lines). Easy to verify.
- **Positive**: Works well with SSA form — each value has one definition, simplifying allocation.
- **Negative**: Not optimal — may spill values that a graph-coloring allocator would keep in registers.
- **Negative**: 5 GPRs is tight for complex expressions. Spill to stack when exhausted.
- **Trade-off**: Compile speed and simplicity over allocation quality. Correct for the current register budget.

## Register Budget

| Category | Registers | Notes |
|----------|-----------|-------|
| Allocatable GPRs | 5 | X1-X5 (approx, varies by calling convention) |
| Allocatable FPRs | 8 | D0-D7 |
| Reserved | X0 (return), X16-17 (plat), X19-23 (callee), X24 (int tag), X28 (SP) | |

## References

- Architecture: `docs-internal/architecture/overview.md`
