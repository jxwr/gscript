---
module: passes/const_prop
description: Local constant propagation + constant folding. Replaces arithmetic on constant operands with a materialized ConstInt/ConstFloat. Single forward walk, no cross-block propagation.
files:
  - path: internal/methodjit/pass_constprop.go
  - path: internal/methodjit/pass_constprop_test.go
last_verified: 2026-04-13
---

# ConstProp Pass

## Purpose

Track known-constant SSA values and fold arithmetic whose operands are all constants into a new `OpConstInt` / `OpConstFloat`. Reduces the runtime op count and enables downstream passes: `LoadElim` can forward const-keyed stores, `RangeAnalysis` gets point ranges for free, `DCE` cleans up the now-dead producers.

## Public API

- `func ConstPropPass(fn *Function) (*Function, error)`

## Invariants

- **MUST**: the pass is a single forward walk over `fn.Blocks` in RPO; no fixed-point iteration, no worklist. Correct because it only consumes constants already recorded and constants are produced in definition order in SSA.
- **MUST**: folded instructions are rewritten in place — the `Op` is mutated to `OpConstInt` or `OpConstFloat`, `Args` cleared, `Aux` set to the folded value (`int64` for int, `math.Float64bits(f)` cast to `int64` for float). The instruction's `ID` is preserved.
- **MUST**: supported folds are:
  - Integer binary: `OpAddInt`, `OpSubInt`, `OpMulInt`, `OpModInt`
  - Integer unary: `OpNegInt`
  - Float binary: `OpAddFloat`, `OpSubFloat`, `OpMulFloat`, `OpDivFloat`
  - Float unary: `OpNegFloat`
  - Generic binary: `OpAdd`, `OpSub`, `OpMul`, `OpMod` (when both operands are constants, folds using the constant types)
- **MUST**: the constant table is keyed by SSA `Value.ID`; it lives only for the pass run and is not exposed.
- **MUST NOT**: fold `OpDivInt` (does not exist; GScript `/` is always float).
- **MUST NOT**: fold across block boundaries in a way that would duplicate side effects — the pass only rewrites the single defining instruction in place, so there is no duplication.
- **MUST NOT**: fold an operation whose either operand is not in the constant table (e.g. coming from a `LoadSlot` or a phi), even if the operand is a literal in the source.

## Hot paths

- `fibonacci_iterative` — literal initializers for loop bounds fold to point ranges, giving `RangeAnalysis` max info.
- `spectral_norm` — `2.0 * size / size` type of shape reduces after inline + constprop.
- `mandelbrot` — loop-bound constants and complex-plane offsets.

## Known gaps

- **No cross-block propagation.** A constant assigned in block A is not propagated to uses in block B (LoadElim and phi type inference pick up some of this).
- **No algebraic identities.** `x + 0`, `x * 1`, `x - x`, `x * 0` are not simplified. Strength reduction (e.g. `x * 2 → x + x`, `x * 4 → x << 2`) is not performed.
- **No comparison folding.** `OpLtInt`, `OpEqInt`, `OpLtFloat`, `OpEqFloat` are never folded even when both operands are constants — the branch stays runtime. (Dead-branch elimination is therefore also not available.)
- **No constant folding on integer division** — see above, `OpDivInt` does not exist.
- **No bit ops folding** — `OpBand`, `OpBor`, `OpBxor`, `OpShl`, `OpShr` are not folded.

## Tests

- `pass_constprop_test.go` — covers each supported op, checks that rewritten instruction is a ConstInt/ConstFloat with the correct `Aux`, and verifies DCE downstream removes the operands.
