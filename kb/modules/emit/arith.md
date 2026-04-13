---
module: emit.arith
description: Arithmetic emission — type-specialized int/float Add/Sub/Mul/Div/Mod, Int48Safe overflow-check elision, fused compare+branch, FPR-direct float ops.
files:
  - path: internal/methodjit/emit_arith.go
  - path: internal/methodjit/emit_call.go
  - path: internal/methodjit/emit_compile.go
  - path: internal/methodjit/emit_dispatch.go
last_verified: 2026-04-13
---

# Emit — Arithmetic

## Purpose

Lower SSA arithmetic ops to ARM64. Three specialization levels: (1) NaN-boxed generic path with runtime type dispatch (`OpAdd`, `OpSub`, `OpMul`, `OpMod`) for unspecialized sites; (2) raw-int specialized (`OpAddInt`, `OpSubInt`, `OpMulInt`, `OpModInt`, `OpNegInt`) when TypeSpec proves both operands int; (3) raw-float specialized (`OpAddFloat`, `OpSubFloat`, `OpMulFloat`, `OpDivFloat`, `OpNegFloat`, `OpSqrt`) on FPRs. Int overflow checks are elided when range analysis proves safety.

## Public API

- `func (ec *emitContext) emitIntBinOp(instr *Instr, op intBinOp)` — NaN-boxed int binary op.
- `func (ec *emitContext) emitRawIntBinOp(instr *Instr, op intBinOp)` — type-specialized raw-int path (Add/Sub use 12-bit immediate form when possible).
- `func (ec *emitContext) emitFloatBinOp(instr *Instr, op intBinOp)` — type-generic (int-or-float) dispatch path.
- `func (ec *emitContext) emitTypedFloatBinOp(instr *Instr, op intBinOp)` — typed float path, FPR-direct when `instr.Type == TypeFloat`.
- `func (ec *emitContext) emitDiv(instr *Instr)` — OpDiv always returns float; `OpDivFloat + TypeFloat` takes the FPR fast path.
- `func (ec *emitContext) emitNegInt(instr *Instr)` / `emitNegFloat` / `emitUnm` — unary negate.
- `func (ec *emitContext) emitIntCmp(instr *Instr, cond jit.Cond)` / `emitFloatCmp` — typed comparison, emits CMP/FCMP and optionally a fused B.cond.
- `func (ec *emitContext) emitInt48OverflowCheck(result jit.Reg, instr *Instr)` — `SBFX + CMP + BCond.EQ, ok` around a raw result; overflow flushes active regs and deopts.
- `func isFusableComparison(op Op) bool` — allowlist for fused compare+branch (in `emit_compile.go`).

## Invariants

- **MUST**: `OpAddInt`, `OpSubInt`, `OpMulInt`, `OpModInt` dispatch to `emitRawIntBinOp` and keep operands in raw int64 form across the op. Result is marked raw via `storeRawInt`; boxing happens on demand at block boundary, return, or guard crossing.
- **MUST**: Int48 overflow check (`SBFX X0, result, #0, #48; CMP X0, result; B.EQ ok`) is skipped when any of:
  - `instr.Aux2 == 1` (loop counter increment, bounded by loop limit)
  - `ec.int48Safe(instr.ID)` returns true (range analysis proved safety via `fn.Int48Safe[id]`)
  - `op == intBinMod` (modulo cannot overflow within int48 for non-zero divisors)
- **MUST**: on overflow-check failure, `emitInt48OverflowCheck` calls `emitStoreAllActiveRegs()` and `emitLoopExitBoxing(-1)` BEFORE jumping to `deopt_epilogue` — otherwise loop-header phi values live only in registers and the interpreter would see stale memory (documented `fibonacci_iterative` bug).
- **MUST**: constant-int commutative fold — `emitRawIntBinOp` for Add checks both operand orders for a 12-bit immediate constant via `constIntImm12`; Sub only checks the RHS (non-commutative).
- **MUST**: raw-int destination is the SSA-allocated GPR from `ec.alloc.ValueRegs[instr.ID]` when available; falls back to `X0` scratch.
- **MUST**: `emitTypedFloatBinOp` in raw-float mode (`instr.Type == TypeFloat`) keeps operands in FPRs via `resolveRawFloat` and writes the result via `storeRawFloat` with no FMOVtoGP round-trip. Grep: `ec.alloc.ValueRegs[instr.ID]; ok && pr.IsFloat`.
- **MUST**: `emitFloatCmp` uses FCMP on FPRs — integer CMP on raw float bits does not order negatives correctly.
- **MUST**: fused compare+branch — `ec.fusedCmps[instr.ID]` is pre-computed in `emit_compile.go` for single-use comparisons whose unique use is an immediately-following `OpBranch` in the same block (`isFusableComparison` allowlist: `OpLt, OpLtInt, OpLe, OpLeInt, OpEq, OpEqInt, OpLtFloat, OpLeFloat`). When fused, the comparison emits only CMP/FCMP and sets `ec.fusedActive = true; ec.fusedCond = cond`. The subsequent `emitBranch` sees `fusedActive` and emits a single `B.cond trueLabel` instead of `CSET + ORR + TBNZ`. Saves 3 instructions per fused pair.
- **MUST**: `ec.fusedActive` is cleared at the top of every non-Branch `emitInstr` call — a fused comparison must be immediately consumed by a Branch or it is discarded.
- **MUST**: boxed result (final `jit.EmitBoxIntFast` + `storeResultNB`) is mandatory on the NaN-boxed path; the raw-int path defers boxing.
- **MUST**: `OpDiv` / `OpDivFloat` always return float. Int-division would require a separate op.
- **MUST NOT**: emit an overflow check on `OpNegInt` unless `int48Safe` is false — but negating `minInt48` is a real overflow, so the check must fire when range analysis did not clear it.
- **MUST NOT**: fuse a comparison whose `useCounts[instr.ID] != 1` — its result is materialized for a non-Branch use.

## Hot paths

- `fibonacci_iterative`, `sieve`, `sum` — tight int-counter loops. Every inner arithmetic op is `OpAddInt`/`OpSubInt`/`OpMulInt` with Int48Safe-elided overflow check, raw-int register carry via loop-phi, and fused compare+branch on the loop guard.
- `nbody`, `spectral_norm`, `mandelbrot` — FPR-resident float chains; `emitTypedFloatBinOp` with TypeFloat keeps D4–D11 live, `emitFloatCmp` fuses into `B.cc`.
- `mandelbrot` — `OpSqrt` on FPR direct, no GPR round-trip.

## Known gaps

- **No SIMD / NEON**: every float op is scalar `FADDd` / `FMULd`; vectorizable loops (nbody pair interaction) emit one op at a time.
- **No FMA (fused multiply-add)**: `a*b+c` emits FMUL + FADD even on ARM64 where `FMADD` is cheaper.
- **Mod on float deopts**: `emitFloatBinOp` with `intBinMod` unconditionally calls `emitDeopt`. Grep: `case intBinMod: ec.emitDeopt(instr)`.
- **Overflow check always branches to function-wide deopt**: no local overflow recovery (e.g., promote to float and continue).
- **Fused compare is single-use only**: a comparison whose result is used by both a branch and a store materializes the bool AND emits CMP, missing the fusion.
- **Int48 range is narrower than Go int64**: overflow on the int48 edge forces deopt even when the true int64 result fits. Necessary consequence of NaN-boxing with a 16-bit tag.

## Tests

- `emit_fused_branch_test.go` — fused compare+branch correctness across int/float
- `emit_int_counter_test.go` — raw-int loop carry + overflow-check elision
- `emit_tier2_correctness_test.go` — per-op arithmetic correctness
- `pass_range_test.go` — Int48Safe analysis (drives overflow-check elision)

## See also

- `kb/modules/emit/overview.md`
- `kb/modules/passes/range.md` — where `Int48Safe` is computed
- `kb/modules/passes/typespec.md` — where `OpAdd` becomes `OpAddInt` / `OpAddFloat`
- `kb/modules/regalloc.md` — FPR carries and LICM-hoisted FPR pins
