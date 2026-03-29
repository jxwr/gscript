# ADR-003: CFG SSA IR with Braun Graph Builder

**Date**: 2026-03-16 (decided), recorded 2026-03-29
**Status**: Accepted

## Context

The Method JIT needs an IR to represent compiled functions. The trace JIT used a linear SSA IR (no basic blocks, loop markers instead). This made it impossible to represent control flow merges (if/else, loop exits), which caused the slot-reuse bug (5 independent bugs from `writtenSlots` manual tracking).

## Decision

Use **CFG-based SSA IR** (basic blocks with successors/predecessors, phi nodes at merge points) constructed via the **Braun et al. 2013** algorithm ("Simple and Efficient Construction of Static Single Assignment Form").

The IR covers all 45 bytecodes as opcodes. Each function is a directed graph of basic blocks, each block contains SSA instructions, and phi nodes resolve data flow at control flow merges.

## Alternatives Considered

| IR type | Why rejected |
|---------|--------------|
| Linear SSA (trace JIT style) | No basic blocks → no phi nodes → manual slot tracking → 5 bugs |
| Sea of Nodes (V8 TurboFan old) | V8 itself is migrating away from it to CFG-based Turboshaft. Too complex for GScript's scale |
| Extend trace JIT's linear IR | Deeply embedded (loop markers, pre-loop guards, side-exit model). Refactoring risks breaking trace JIT |

## Consequences

- **Positive**: Phi nodes eliminate the slot-reuse problem entirely.
- **Positive**: Each optimization pass works on immutable Function → Function, independently testable.
- **Positive**: IR interpreter serves as correctness oracle: `Interpret(graph, args) == VM.Execute(proto, args)`.
- **Positive**: IR validator checks structural invariants after every pass.
- **Negative**: Braun builder is ~300 lines. Simpler than dominance-frontier construction but less well-known.
- **Trade-off**: More upfront complexity than linear IR, but prevents entire classes of bugs.

## Key Reference

Braun, M., Buchwald, S., Hack, S., Leißa, R., Mallon, C., Zwinkau, A. (2013). "Simple and Efficient Construction of Static Single Assignment Form." CC 2013.

## References

- Research: `docs-internal/research/method-jit-research.md`
- Blog: `docs/20-the-pivot.md`
