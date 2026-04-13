---
module: emit.guard
description: OpGuardType / OpGuardTruthy lowering — NaN-box tag checks, CSE at the IR level, LICM hoisting for invariant guards, shape-guard dedup in emit.
files:
  - path: internal/methodjit/emit_call.go
  - path: internal/methodjit/pass_load_elim.go
  - path: internal/methodjit/pass_licm.go
  - path: internal/methodjit/emit_table_field.go
last_verified: 2026-04-13
---

# Emit — Guards

## Purpose

Type and truthiness guards let the optimizer assume a value has a specific runtime type, enabling type-specialized arithmetic and typed-array fast paths. Misprediction deopts with `ExitCode = ExitDeopt (2)` and bails the whole function to the interpreter. Guard insertion is driven by Tier 1 feedback (`kb/modules/feedback.md`); guard elimination is via LoadElim CSE and LICM hoisting; guard lowering is a single `CMP + B.NE` in the common case.

## Public API

- `func (ec *emitContext) emitGuardType(instr *Instr)` — OpGuardType for `TypeInt` and `TypeFloat`; unsupported guard types pass through.
- `func (ec *emitContext) emitGuardTruthy(instr *Instr)` — OpGuardTruthy, converts any value to a NaN-boxed bool via nil/false check.
- `func (ec *emitContext) emitDeopt(instr *Instr)` — shared deopt exit sequence (writes `ExitDeopt` to `ctx.ExitCode`, jumps to `deopt_epilogue`).
- `func emitCheckIsInt(asm *jit.Assembler, valReg, scratch jit.Reg)` — shared helper for int tag check, leaves NZCV set.
- `func LoadEliminationPass(fn *Function)` — includes block-local `OpGuardType` CSE (in `pass_load_elim.go`).

## Invariants

- **MUST**: `OpGuardType` for `TypeInt` uses `emitCheckIsInt` which does `LSR X2, X0, #48; MOV X3, #0xFFFE; CMP X2, X3` — equal = int, not-equal = deopt.
- **MUST**: `OpGuardType` for `TypeFloat` checks `tag < 0xFFFC` (raw IEEE bits) via `LSR + MOV + CMP + BCond.GE, deopt`.
- **MUST**: unsupported guard types fall through to pass-through — the emitter stores the operand as the guard's result with no check. Grep: `default:` in `emitGuardType`.
- **MUST**: `OpGuardTruthy` is not a deopting guard; it materializes a NaN-boxed bool (`NB_TagBool|0` for nil/false, `NB_TagBool|1` otherwise) using the pinned `mRegTagBool` (X25) without branching to `deopt_epilogue`.
- **MUST**: `LoadEliminationPass` deduplicates `OpGuardType` by `(argID, guardType)` within each block. Redundant guards are replaced by `replaceAllUses` and the instruction is converted to `OpNop` (guards are side-effecting so DCE alone would not remove them). Grep: `guardKey` struct, `guardAvail` map.
- **MUST**: `LoadEliminationPass` clears both `available` and `guardAvail` maps at every `OpCall` / `OpSelf` — a call may change runtime types.
- **MUST**: LICM hoists `OpGuardType` when its operand is loop-invariant. Grep comment in `pass_licm.go`: `OpGuardType IS hoisted when its operand is invariant`.
- **MUST**: LICM does NOT hoist `OpGuardTruthy` or `OpGuardNonNil` — these are control-flow guards and hoisting past a branch would relocate a deopt to a path that should not take it.
- **MUST**: deopting guards share NZCV flags with the immediately-preceding instruction only if no other instruction overwrites NZCV. The emitter does not emit an explicit "preserve NZCV" pass; correctness is maintained by ordering.
- **MUST**: shape-guard dedup for `OpGetField` / `OpSetField` lives in the emitter, keyed by `(tblValueID → shapeID)` in `ec.shapeVerified`. First access emits the full `LDRW + CMP + BCond.NE, deopt`; subsequent accesses skip to the `svals` load. Grep: `ec.shapeVerified[tblValueID]` in `emit_table_field.go`.
- **MUST**: `ec.shapeVerified` and `ec.tableVerified` are cleared at every `OpCall`, `OpSelf`, and `OpSetTable` (see `emit_dispatch.go`).
- **MUST NOT**: hoist a guard out of a loop when the operand is defined inside the loop — LICM checks operand invariance first.
- **MUST NOT**: re-emit a shape guard inside a block after the same `(tblValueID, shapeID)` has been verified; the dedup map must be consulted.

## Hot paths

- `nbody` — `b.x`, `b.y`, `b.z`, `b.vx`, `b.vy`, `b.vz` shape guards on the same body value dedup to a single guard per block.
- `mandelbrot` — the complex-number shape is monomorphic; LICM hoists the GuardType on the `self` argument to the loop pre-header.
- Any feedback-typed GETFIELD — the `GraphBuilder` inserts `OpGuardType` right after `OpGetField` based on `Feedback[pc].Result`. In a tight loop this guard is then LICM-hoisted when the object is loop-invariant (e.g., `feedback_getfield_integration_test.go`).

## Known gaps

- **No peephole merging of consecutive guards on different values** — two `CMP` instructions in a row each get their own `B.NE, deopt_epilogue`.
- **No polymorphic type guards**: `TypeInt` and `TypeFloat` only; `TypeString`, `TypeTable`, `TypeFunction` fall through as pass-through no-ops.
- **CSE is block-local only**: a guard in block A followed by a loop in block B re-emits the guard at the top of B, unless LICM hoists. No global value numbering.
- **Shape guard dedup is block-local**: see `kb/modules/emit/table.md` Known gaps.
- **No guard fusion with the subsequent op**: `GuardType(x,Int) → LoadSlot(x)` emits a CMP/branch AND a separate load, even though the sequence could be fused into a single "load-and-type-check".

## Tests

- `pass_load_elim_test.go` — GuardType CSE correctness
- `pass_licm_test.go::TestLICM_HoistGuardType`, `TestLICM_GuardTypeHoist` — hoisting invariants
- `feedback_getfield_integration_test.go` — feedback-driven guard insertion + cascade
- `emit_tier2_correctness_test.go` — deopt path correctness

## See also

- `kb/modules/emit/table.md` — shape guard dedup lives in the field emitter
- `kb/modules/passes/load_elim.md` — guard CSE rules
- `kb/modules/passes/licm.md` — hoistability rules for guards
- `kb/modules/feedback.md` — where guards originate
