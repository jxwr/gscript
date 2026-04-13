---
module: passes/range_analysis
description: Integer range dataflow over the SSA IR. Marks AddInt/SubInt/MulInt/NegInt whose range provably fits int48; populates fn.Int48Safe so the emitter skips overflow checks.
files:
  - path: internal/methodjit/pass_range.go
  - path: internal/methodjit/pass_range_test.go
last_verified: 2026-04-13
---

# RangeAnalysis Pass

## Purpose

Compute `[min, max]` integer ranges for every int-typed SSA value via forward dataflow, then populate `fn.Int48Safe` with the IDs of arithmetic ops whose range provably fits in the signed int48 space. The ARM64 emitter consults `Int48Safe` to skip the 3-instruction `SBFX + CMP + B.NE` overflow guard after every int op, recovering most of the ~3.7× `spectral_norm` regression caused by guard instructions in an inlined hot loop.

## Public API

- `func RangeAnalysisPass(fn *Function) (*Function, error)`
- `const MinInt48 int64 = -(1 << 47)`
- `const MaxInt48 int64 = (1 << 47) - 1`

## Invariants

- **MUST**: the pass has three phases:
  - **Phase A (`seedLoopRanges`)**: when a FORLOOP's init, limit, and step are all concrete `OpConstInt`, the counter's range is `[min(start, limit), max(start, limit)]`.
  - **Phase B**: forward propagation to a fixed point, capped at `maxIter = 5` iterations over `fn.Blocks` in RPO. Per-op range rules use saturating arithmetic for `OpAddInt`, `OpSubInt`, `OpMulInt`, `OpNegInt`, `OpModInt`.
  - **Phase C**: populate `fn.Int48Safe[id] = true` for each `OpAddInt`/`OpSubInt`/`OpMulInt`/`OpNegInt` whose computed range satisfies `fitsInt48()`.
- **MUST**: `fn.Int48Safe` is the ONLY output channel — the pass does not rewrite any instruction. It is a side table keyed on `Value.ID`.
- **MUST**: phi nodes join input ranges via `joinRange` (union: min-of-mins, max-of-maxes). An unknown input makes the phi `topRange`.
- **MUST**: `topRange` (unknown) values have `known == false`; `fitsInt48` returns `false` for any unknown range, so the emitter keeps the overflow guard.
- **MUST**: `RangeAnalysisPass` runs BEFORE `LICMPass` — LICM's int-arithmetic hoist check consults `fn.Int48Safe`. Reversing the order silently loses every int hoist.
- **MUST**: non-int ops are skipped (`if !instr.Type.isIntegerLike() { continue }`).
- **MUST NOT**: mark an op safe whose range depends on unknown inputs — `computeRange` must return `topRange` in that case.
- **MUST NOT**: modify `fn.Int48Safe` for ops other than the four listed; the emitter's guard logic only triggers on those.

## Hot paths

- `spectral_norm` — the inlined `A(i,j)` body contains `i+j`, `(i+j)*(i+j+1)`. Without RangeAnalysis every one of these carries a 3-instruction overflow check; the pass eliminates all of them in the provably-bounded loop.
- `fibonacci_iterative` — loop counter and the two running sums become int48-safe after a single Phase A seeding.
- `sieve` — index arithmetic on the prime table.
- `fannkuch` — index manipulation in the permutation loop.

## Known gaps

- **Int-only.** Floats have no range analysis; no value range propagation for float-dominated code.
- **Only arithmetic.** `OpDivInt` does not exist, so division is not considered. Shift ops (`OpShl`, `OpShr`) are not modeled.
- **Conservative phi join.** An unknown input forces `topRange` — a partial inference could still mark the known-sides safe but the current design gives up early.
- **No inter-procedural.** Values from `OpCall` or unknown globals are always top.
- **No bound refinement from comparisons.** A value guarded by `if x < 10` is not narrowed inside the then-branch.
- **Fixed cap of 5 iterations.** Deeply nested loops (>5 phi layers) may leave inner values unresolved; rely on the three pipeline runs of TypeSpec to narrow types upstream.

## Tests

- `pass_range_test.go` — loop-counter seeding, phi convergence, saturating multiplication, int48 boundary, post-pass `fn.Int48Safe` population correctness, LICM integration sanity.
