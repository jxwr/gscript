---
module: regalloc
description: Linear-scan register allocator with loop-aware carries. Forward-walk, 3 GPRs + 8 FPRs + fixed-purpose pins.
files:
  - path: internal/methodjit/regalloc.go
  - path: internal/methodjit/regalloc_test.go
  - path: internal/methodjit/regalloc_carry_test.go
last_verified: 2026-04-13
---

# RegAlloc — Linear-Scan with Loop Carries

## Purpose

Assign SSA values to ARM64 physical registers. Forward-walk the RPO block order, allocating values as they're defined and freeing them at last use. Loop-aware: values live across a loop back-edge must be carried in the same physical register across the pre-header to avoid reload cost.

## Public API

- `func AllocateRegisters(fn *Function) *RegAllocation`
- `type RegAllocation struct` — per-value register assignments + spill slot layout + LICM-hoisted FPR pins
- `type PhysReg struct` — `{Reg int, IsFloat bool}` identifies an ARM64 register

## Invariants

- **MUST**: GPR allocation pool = `{X20, X23, X28}` (3 primary), FPR pool = `{D4..D11}` (8). X19/X24–X27 are fixed-purpose and never allocated.
- **MUST**: X21 holds the closure cache, X22 holds slot-0 (R(0)). Both are pinned by the regalloc, not allocated over.
- **MUST**: every value that's live across a `Call` in SSA has a spill slot — the emitter reloads around BLR.
- **MUST**: loop-carried phis use the `carry` mechanism (`regalloc_carry.go`) to pin the same physical register for the phi and its back-edge operand, avoiding a reload at loop head.
- **MUST**: LICM-hoisted invariants pinned to FPRs get `RegAllocation.LICMInvariantFPRs` entries — emitters consult this to elide the load inside the loop.
- **MUST NOT**: spill a value that's referenced only once at its definition point (dead value); DCE must have run.
- **MUST NOT**: reuse a physical register within a block without first emitting a visible def-kill for the prior occupant — doing so breaks the emitter's def-use assumptions.

## Hot paths

RegAlloc runs once per compiled Tier 2 function (warm-up cost). The hottest invariants it enforces are exercised by:

- Float-heavy loops (`nbody`, `spectral_norm`, `mandelbrot`) — FPR carries and LICM-invariant FPR pins are load-bearing for their wall-time.
- Int-counter loops (`fibonacci_iterative`, `sieve`) — GPR counter carry avoids the reload of the induction variable at loop head.

## Known gaps

- **No graph coloring.** Linear scan is simple but overly conservative on high-pressure functions (nbody inner loop occasionally spills a value that a coloring allocator would keep in a register).
- **No rematerialization.** Values that are cheap to recompute are spilled instead of re-derived.
- **Pool size.** Only 3 general-purpose GPRs (X20, X23, X28) are allocatable. Functions with many live ints spill frequently. Expanding the pool requires freeing one of the fixed-purpose registers (X26/X27 are load-bearing).

## Tests

- `regalloc_test.go` — basic allocation + spill slot generation
- `regalloc_carry_test.go` — loop-phi carry correctness
- `loops_preheader_test.go` — LICM-invariant FPR pinning interaction
- `phi_regalloc_test.go` — multi-phi register assignment (R11 fix for 3+ phis sharing a register)
