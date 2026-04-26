---
module: emit.call
description: OpCall emission — native BLR fast path with selective spill/reload around the call, exit-resume fallback for non-closures, uncompiled callees, and recursion limits.
files:
  - path: internal/methodjit/emit_call_native.go
  - path: internal/methodjit/emit_call_exit.go
  - path: internal/methodjit/emit_dispatch.go
last_verified: 2026-04-27
---

# Emit — Call (Native BLR + Exit Fallback)

## Purpose

Lower OpCall to an ARM64 `BLR` when the callee is a compiled `*vm.Closure` with a published direct entry; otherwise fall back to the `ExitCallExit` exit-resume path so Go can invoke GoFunctions, metatable `__call`, uncompiled protos, or overflowing stacks. Native BLR is ~10ns; exit-resume is ~80ns.

## Public API

- `func (ec *emitContext) emitCallNative(instr *Instr)` — primary Tier 2 OpCall emitter, dispatched from `emit_dispatch.go:OpCall`.
- `func (ec *emitContext) emitCallExitFallback(instr *Instr, funcSlot, nArgs, nRets int)` — shared fallback path for slow and callee-exit cases.
- `func (ec *emitContext) emitCallExit(instr *Instr)` — standalone exit-resume emitter (kept for cases where native path is disabled).
- `func (ec *emitContext) computeLiveAcrossCall(callInstr *Instr) (gprLive, fprLive map[int]bool)` — live-across-call analysis.
- `func (ec *emitContext) emitSpillSelectiveForCall(gprLive, fprLive map[int]bool)` / `emitReloadSelectiveForCall` — selective spill/reload helpers.

## Invariants

- **MUST**: function value and all args are stored to `regs[funcSlot]..regs[funcSlot+nArgs]` BEFORE any spill — `resolveValueNB` may read from registers that are about to be clobbered.
- **MUST**: only values LIVE across the call are spilled; liveness = `usedAfter[valueID] || ec.crossBlockLive[valueID]`. Grep: `computeLiveAcrossCall` in `emit_call_native.go`.
- **MUST**: the slow-path/callee-exit fallback first calls `emitStoreAllActiveRegs()` to upgrade from selective-spill to full-spill, because the Go-side exit handler may inspect any register-resident value.
- **MUST**: native BLR falls to slow path when any of:
  - `NativeCallDepth >= maxNativeCallDepth`
  - value is not a ptr-tagged VMClosure (sub-type 8)
  - both `DirectEntryPtr == 0` and `Tier2DirectEntryPtr == 0` (callee has no valid direct entry)
  - callee register window would exceed `ctx.RegsEnd` (stack-overflow guard)
  - callee's `CallCount+1` hits the Tier 2 threshold (`tmDefaultTier2Threshold`) — triggers Tier 2 compile via Go
- **MUST**: caller state is saved before BLR: `{FP, LR, mRegRegs, mRegConsts, CallMode, ClosurePtr}` plus global-cache fields for non-static-self calls. Restored in reverse after BLR.
- **MUST**: before BLR the emitter advances `mRegRegs += calleeBaseOff` (`nextSlot * jit.ValueSize`) so the callee's register window sits past all Tier 2 slots, then writes the advanced pointer to `ctx.Regs`.
- **MUST**: `ExecContext.CallMode = 1` before BLR so the callee's RETURN takes the direct-exit path (16-byte frame) instead of the baseline-exit path.
- **MUST**: `NativeCallDepth` is incremented immediately before BLR and decremented immediately after.
- **MUST**: after BLR, `ExitCode` is checked with `CBNZ` — nonzero means the callee exited mid-execution and control falls into `exitHandleLabel`, which shares a label with `slowLabel` and delegates to `emitCallExitFallback`.
- **MUST**: normal return loads the NaN-boxed result from `ExecContext.BaselineReturnValue`, writes it to `regs[funcSlot]`, then reloads live registers.
- **MUST**: the Tier 2 call IC slot is two words: boxed closure and direct-entry address. A hit re-derives the raw closure/proto from the current boxed value, then refreshes the direct-entry address from `DirectEntryPtr`, falling back to `Tier2DirectEntryPtr`, before calling or tail-jumping. This is the `KeepCachedDirectEntry` protocol.
- **MUST NOT**: reorder arg stores after `emitSpillSelectiveForCall` — spilling may invalidate the source register.
- **MUST NOT**: preserve `shapeVerified` / `tableVerified` across a call; `emit_dispatch.go:OpCall` resets both maps because a callee may mutate any table.

## Hot paths

- `fibonacci_recursive`, `ackermann` — tight recursive BLR in steady state; selective spill of 1–2 live GPRs is the critical path.
- `quicksort` — self-recursive call with cross-iteration live values.
- `nbody` — Tier 2 inner loop is `hasCallInLoop`-rejected from Tier 2 and stays on Tier 1; does NOT exercise `emitCallNative`.

## Known gaps

- **GoFunction / metatable `__call` / variadic CALL**: always slow-path (`ExitCallExit`). Variadic (`B==0`) is rejected earlier in `BuildGraph` via `fn.Unpromotable`.
- **Threshold race**: when `CallCount+1 == threshold`, the native path exits to Go to trigger compilation — the next call will be native, but the crossing-call itself pays slow-path cost.
- **Polymorphic call sites**: the call IC is monomorphic. A site that alternates callees still pays miss/update traffic.
- **`RegsEnd` guard is conservative**: it rejects any callee window that overlaps, not just the ones that would actually overflow, so deeply-nested non-recursive calls on a full stack fall to slow path.

## Tests

- `emit_call_exit_test.go` — exit-resume correctness across OpCall forms
- `tier2_bench_test.go`, `tier2_recursion_hang_test.go` — recursion, BLR correctness
- `tier2_call_entry_protocol_test.go` — `DirectEntryPtr` / `Tier2DirectEntryPtr` publication, clear, and call-IC refresh protocol
- `tier2_fpr_residency_test.go` — FPR spill/reload across BLR

## See also

- `kb/modules/emit/overview.md`
- `kb/modules/tier2.md` — `hasCallInLoop` gating, smart tiering thresholds
- `kb/modules/regalloc.md` — what "live across a call" means at the SSA level
