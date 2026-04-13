---
module: passes/overview
description: Pass contract and pipeline ordering. One pass per file; every pass returns a Function and leaves the IR valid.
files:
  - path: internal/methodjit/pipeline.go
  - path: internal/methodjit/pass_typespec.go
  - path: internal/methodjit/pass_intrinsic.go
  - path: internal/methodjit/pass_inline.go
  - path: internal/methodjit/pass_constprop.go
  - path: internal/methodjit/pass_load_elim.go
  - path: internal/methodjit/pass_dce.go
  - path: internal/methodjit/pass_range.go
  - path: internal/methodjit/pass_licm.go
  - path: internal/methodjit/pass_scalar_promote.go
  - path: internal/methodjit/pass_simplify_phis.go
last_verified: 2026-04-13
---

# Tier 2 Passes — Contract and Order

## Purpose

Every Tier 2 optimization is a pass: a function that takes a `*Function`, returns a (possibly rewritten) `*Function`, and leaves the IR in a state where `Validate(fn)` returns no errors. Passes compose through `RunTier2Pipeline` (production) or `NewTier2Pipeline` (Diagnose dump helper). Pass order is fixed and load-bearing.

## Public API

- `type PassFunc func(*Function) (*Function, error)` — canonical pass signature
- `type Pipeline struct` — named, ordered list of passes with optional post-pass Validator and IR dump
- `func NewPipeline() *Pipeline`
- `func (p *Pipeline) Add(name string, fn PassFunc)`
- `func (p *Pipeline) Run(fn *Function) (*Function, error)`
- `func RunTier2Pipeline(fn *Function, opts *Tier2PipelineOpts) (*Function, []string, error)` — production pipeline driver
- `func NewTier2Pipeline() *Pipeline` — diagnostic dump helper only; not bit-identical to production

## Invariants

- **MUST**: each pass lives in `internal/methodjit/pass_<name>.go` with a companion `pass_<name>_test.go`. No pass shares a file.
- **MUST**: production pass order is exactly:
  `SimplifyPhis → TypeSpec → Intrinsic → TypeSpec → Inline → SimplifyPhis → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → ScalarPromotion`
- **MUST**: `TypeSpec` runs before `Intrinsic` so generic ops carry type info; and after `Intrinsic` so rewritten `OpSqrt` is retyped; and after `Inline` so inlined-body ops get specialized in the caller's context.
- **MUST**: `ConstProp` runs before `LoadElim` so folded constant stores forward to loads; `LoadElim` runs before `DCE` so redundant-replaced loads become dead.
- **MUST**: `RangeAnalysis` runs before `LICM`: LICM refuses to hoist `isIntArithOp` unless `fn.Int48Safe[instr.ID]` is set, which only `RangeAnalysis` populates.
- **MUST**: `LICM` runs before `ScalarPromotion`: scalar promotion relies on LICM's dedicated pre-header (it requires `hdr.Preds[0]` to be the pre-header) and LICM's loop invariant hoisting.
- **MUST**: `Validate` runs after `BuildGraph` and immediately before `AllocateRegisters`; individual passes may run it internally (LICM does). Any pass must leave the IR valid.
- **MUST NOT**: a pass mutates the input `*Function` without returning it — the result is assigned back in `RunTier2Pipeline`; shared-pointer mutation is allowed.
- **MUST NOT**: a new pass be added without a matching `_test.go`, and without updating both `RunTier2Pipeline` AND `NewTier2Pipeline` (the parity test will flag drift).

## Hot paths

The full pipeline runs once per promoted proto on its first qualifying call. Steady-state hot-loops run the emitted code, not the passes. Benchmarks that exercise the whole pipeline most heavily (many promoted protos): `method_dispatch`, `object_creation`, `closure_bench`. Benchmarks where a single pass dominates compiled-code quality: `spectral_norm` (RangeAnalysis + LICM), `nbody` (LoadElim + LICM + ScalarPromote), `fibonacci_iterative` (RangeAnalysis).

## Known gaps

- **No GCSE** — cross-block common subexpression elimination does not exist; `LoadElim` is block-local.
- **No loop unrolling** — even fully bounded tight loops run N iterations in the compiled code.
- **No partial redundancy elimination (PRE)** — hoists that require sinking into some predecessors are not performed.
- **No loop fusion / distribution** — adjacent loops over the same range stay separate.
- **No strength reduction** — e.g. multiplication by loop-invariant constant not rewritten to adds.
- **No tail-call elimination** — recursive calls always go through the call-exit path.

## Tests

Each pass has a dedicated `pass_<name>_test.go` that drives it directly against hand-built or BuildGraph-produced IR. End-to-end pipeline correctness is covered by `emit_tier2_correctness_test.go` and `tier2_correctness_test.go`. Parity between `RunTier2Pipeline` (prod) and `NewTier2Pipeline` (diag) is guarded by `TestDiag_ProductionParity_*` in `tiering_manager_diag_test.go`.
