---
module: tier2
description: Optimizing JIT. TieringManager orchestration, compileTier2Pipeline entry point, OSR. The sanctioned authoritative path for Tier 2 evidence.
files:
  - path: internal/methodjit/tiering_manager.go
  - path: internal/methodjit/tiering_manager_diag.go
  - path: internal/methodjit/pipeline.go
  - path: internal/methodjit/func_profile.go
last_verified: 2026-04-13
---

# Tier 2 — Optimizing JIT

## Purpose

Compile promoted functions through the full optimization pipeline to specialized ARM64 code. Type-specialized arithmetic, guard-protected fast paths, function inlining, LICM, load elimination, range analysis. Much higher per-op speed than Tier 1 but higher per-call setup cost — smart tiering decides when the trade is worth it.

## Public API

- `type TieringManager struct` — orchestrates Tier 1 / Tier 2 / OSR / feedback
- `func NewTieringManager() *TieringManager`
- `func (tm *TieringManager) compileTier2(proto *vm.FuncProto) (*CompiledFunction, error)` — **production entry, unexported**
- `func (tm *TieringManager) CompileForDiagnostics(proto *vm.FuncProto) (*DiagArtifact, error)` — **sanctioned diagnostic entry**
- `func (tm *TieringManager) compileTier2Pipeline(proto *vm.FuncProto, trace *Tier2Trace) (*CompiledFunction, error)` — **shared body, unexported**
- `type CompiledFunction struct` — final ARM64 code + resume addresses + direct entry offset
- `func shouldPromoteTier2(proto *vm.FuncProto) (bool, int)` — smart tiering policy (`func_profile.go`)

## Invariants

- **MUST**: all Tier 2 compilation goes through `compileTier2Pipeline`. No parallel pipeline. `profileTier2Func` is archived at `opt/archive/v3/methodjit-drift/`; `NewTier2Pipeline` is gated as a `Diagnose()`-only dump helper.
- **MUST**: `TestDiag_ProductionParity_*` remains green. Structural parity (insn count + histogram + post-pipeline IR text) between `compileTier2` and `CompileForDiagnostics` is the load-bearing invariant of rule 5.
- **MUST**: Tier 2 refuses the following, returning the proto permanently to Tier 1:
    - goroutine/channel ops (`canPromoteToTier2`)
    - `fn.Unpromotable` (variadic CALL from BuildGraph)
    - `hasCallInLoop(fn)` — call inside a loop after inlining, prevents spectral_norm-style 7× regression
    - pipeline error
    - validation error
- **MUST**: `proto.MaxStack` is updated if Tier 2's compiled function uses more register slots than the bytecode compiler originally allocated.
- **MUST**: `proto.NeedsTier2 = true` when intrinsic rewrites replaced Tier-1-observable calls, so Tier 1 callers dispatch to Tier 2.
- **MUST NOT**: diagnostic code paths mutate `tier2Attempts` or `tier2FailReason`. Those are production-only counters.

## Hot paths

Every benchmark that reaches Tier 2 goes through `compileTier2` once per promoted proto on the first qualifying call. Compilation itself is warm-up cost (not in the steady-state hot path), but:

- `executeTier2` is the steady-state execute loop — exit-code dispatch, resume-address lookup, resync
- `executeCallExit` / `executeGlobalExit` / `executeTableExit` — exit handlers bounce control between JIT and interpreter; each is a full Go call + state sync

## Known gaps

- Smart tiering is heuristic (see `func_profile.go`); edge cases like deeply recursive calls with small bodies thrash between tiers.
- OSR is all-or-nothing per function — no partial replacement at loop boundaries.
- `NewTier2Pipeline` still exists as a Diagnose-only dump helper; it shares a subtle drift risk with production and should eventually be replaced by a production-parity trace inside `compileTier2Pipeline`.

## Tests

- `tiering_manager_diag_test.go` — **parity gate**; TestDiag_ProductionParity_{Sieve,ObjectCreation,Mandelbrot}
- `tier2_recursion_hang_test.go` — BuildGraph B==0 correctness
- `tier2_correctness_test.go` (via dispatch to `emit_tier2_correctness_test.go`) — end-to-end Tier 2 correctness per op
- `tiering_manager_test.go` — promotion decisions, OSR, CallCount thresholds
