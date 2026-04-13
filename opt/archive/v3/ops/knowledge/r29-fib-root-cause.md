# R29 Fib Regression Root Cause

## TL;DR

`handleNativeCallExit` fires **once** for both fib and ack (triggered by a GETGLOBAL cache miss inside the first BLR'd callee), permanently zeros `proto.DirectEntryPtr`, and for fib this forces all ~29M subsequent recursive self-calls through the slow exit-resume Go/JIT roundtrip path; ack's pre-fix behavior was already broken (exponential `handleNativeCallExit` recursion), so the fix simultaneously cured ack's bug while regressing fib.

## Evidence

- `handleNativeCallExit` fires: **1** for fib(35), **1** for ack(3,4) (instrumented counter, reverted after collection)
- `DirectEntryPtr` zeroed-from-non-zero: **1** for both (proto name logged: `["fib"]` / `["ack"]`)
- `fibProto.DirectEntryPtr` before: `0x12c960054` → after execute: `0x0`
- `ackProto.DirectEntryPtr` before: `0x12c968054` → after execute: `0x0`
- Zeroing mechanism: **`handleNativeCallExit`** only; no int-spec deopt fires (not `EvictCompiled`)
- Trigger: `OP_GETGLOBAL` at fib pc=5 (and pc=9) fires inside first BLR'd callee — global value cache empty on cold start
- ack has `OP_GETGLOBAL` at pc=9, pc=15, pc=18 — same trigger mechanism
- Both functions use identical self-call fast path structure: `selfCallFastLabel → selfCallExecLabel → CBZ X3, slowLabel`
- Exit code chain: `ExitBaselineOpExit=7` (GETGLOBAL cache miss inside BLR) → BLR caller converts to `ExitNativeCallExit=8` → `handleNativeCallExit` → `DirectEntryPtr = 0`
- Zeroing happens on the **first BLR self-call** before any subsequent recursive call

## Mechanism

When fib first BLRs itself (DirectEntryPtr non-zero), the inner fib hits `OP_GETGLOBAL` (cache empty) → exits with code 7 → BLR caller upgrades to code 8 → `handleNativeCallExit` sets `proto.DirectEntryPtr = 0` permanently, then re-executes the callee via `e.Execute()`. After this, the Execute loop increments `e.globalCacheGen`. From this point, every future CALL to fib encounters `DirectEntryPtr=0` at the 598bc1e guard (`CBZ X3, slowLabel`) and falls to `emitBaselineOpExitCommon(OP_CALL)` — each call exits to Go, calls `handleCall`, which calls `e.Execute()`. For fib(35) with ~29M calls, this is ~29M Go/JIT roundtrips.

## Why ack is fine

Pre-fix (no `CBZ X3, slowLabel` guard), ack self-called via `BL self_call_entry` unconditionally even after `DirectEntryPtr=0`. Each fresh `Execute()` context started with a stale global cache → GETGLOBAL cache miss → another `handleNativeCallExit` → another nested `Execute()`. For ack(3,4) with call depth ~30, this caused deep `Execute()` recursion (the goroutine stack overflow risk). Post-fix, ack has exactly 1 `handleNativeCallExit` fire then uses exit-resume for all self-calls — much less overhead. Pre-fix fib was fast because its BL chain was bounded by base case `n<2` (no GETGLOBAL in base case), so the `handleNativeCallExit` chain resolved in O(n) not exponentially.

## Minimal-change fix direction (not a plan)

Do not permanently zero `DirectEntryPtr` in `handleNativeCallExit`; instead add a `HasOpExits bool` field to `FuncProto` (set once when NativeCallExit fires) that the `CBZ` guard checks instead — this blocks re-BLR calls to the callee while leaving the `BL self_call_entry` path intact for in-JIT self-calls that don't depend on `DirectEntryPtr`. The self-call path can remain fast for fib (29M calls via BL) while still preventing the exponential `handleNativeCallExit` chain that was ack's pre-fix bug.
