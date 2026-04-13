---
module: emit.global
description: OpGetGlobal native value cache — generation-gated inline load with exit-resume slow path. SetGlobal invalidates via a per-engine generation counter.
files:
  - path: internal/methodjit/emit_call_exit.go
  - path: internal/methodjit/tier1_manager.go
  - path: internal/methodjit/tier1_handlers.go
  - path: internal/methodjit/emit.go
last_verified: 2026-04-13
---

# Emit — Global Value Cache

## Purpose

Give OpGetGlobal a ~5-instruction fast path that loads a previously-resolved NaN-boxed global value from a per-CompiledFunction cache, gated by a shared generation counter. The slow path exits to Go via `ExitGlobalExit` where `tiering_manager_exit.go` resolves the global and populates the cache. Every SetGlobal bumps the generation counter, invalidating every Tier 2 function's cache at once.

## Public API

- `func (ec *emitContext) emitGetGlobalNative(instr *Instr)` — inline cache + slow-path emitter, dispatched from `emit_dispatch.go:OpGetGlobal`.
- `func (ec *emitContext) emitGlobalExit(instr *Instr)` — no-cache fallback (writes `cacheIdx = -1` so Go skips cache population).
- `func (ec *emitContext) emitGlobalExitInner(instr *Instr)` — shared exit-resume body.
- `CompiledFunction.GlobalCache []uint64` — per-function cache slice, indexed by emitter-assigned `cacheIdx`.
- `CompiledFunction.GlobalCacheGen uint64` — generation snapshot at last populate.

## Invariants

- **MUST**: each OpGetGlobal instruction gets a unique monotonic index assigned at emission time from `ec.nextGlobalCacheIndex`. No two GetGlobals in the same function share a slot.
- **MUST**: cache slot size is 8 bytes (one NaN-boxed `uint64`). Offset = `cacheIdx * 8`. Grep: `cacheOff := cacheIdx * 8` in `emit_call_exit.go`.
- **MUST**: the fast path checks three things in order — (1) `Tier2GlobalGenPtr` non-zero, (2) `*genPtr == *cachedGenPtr`, (3) cache pointer non-zero, (4) cached value non-zero. Any null/mismatch → `slowLabel`.
- **MUST**: a zero cache entry is treated as "not populated" and falls to slow path. This means the native path cannot cache a genuinely-nil global (it will always round-trip). Intentional simplification.
- **MUST**: `Tier2GlobalGenPtr` points to the same `uint64` that Tier 1 uses — `&tm.tier1.globalCacheGen`. Set once per Execute entry in `tiering_manager.go`. Grep: `ctx.Tier2GlobalGenPtr = uintptr(unsafe.Pointer(&tm.tier1.globalCacheGen))`.
- **MUST**: on cache miss, the slow path writes the `cacheIdx` into `ExecContext.GlobalCacheIdx` before exiting — Go's `executeGlobalExit` uses this to populate `CompiledFunction.GlobalCache[cacheIdx]` and stamp `GlobalCacheGen`.
- **MUST**: the slow path saves and restores `rawIntRegs` state before/after `emitGlobalExitInner` because the inner helper calls `emitReloadAllActiveRegs` which clears raw-int tracking. Grep: `savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))`.
- **MUST**: `OP_SETGLOBAL` (handled by `BaselineJITEngine.handleSetGlobal` via exit-resume) increments `e.globalCacheGen` atomically after the store, so the very next Tier 1 or Tier 2 GetGlobal sees a mismatched generation and round-trips.
- **MUST**: the generation check must happen BEFORE the cache-pointer load — reversing the order would read a freed cache if `GlobalCache` was reallocated under a new generation.
- **MUST NOT**: cache a nil global (value == 0) by design — use CBZ to fall to slow path.
- **MUST NOT**: share a cache slice across compiled functions. `GlobalCache` is owned per `CompiledFunction`.

## Hot paths

- `nbody`, `spectral_norm`, `mandelbrot`, `sieve` — every Tier 2 compile hits `math.sqrt`, `math.sin`, print, etc. through GetGlobal. In the nbody inner loop, sqrt is hit per pair; native cache elimination of the global lookup is the difference between steady-state and exit-round-trip costs.
- Any function calling a hoistable library helper (`math.*`) — LICM cannot hoist a GetGlobal (side-effecting in the presence of SetGlobal from concurrent code), so the cache is the only way to avoid per-iteration round-trips.

## Known gaps

- **No per-PC cache in the emitter for globals that change**: if a SetGlobal fires inside a hot loop, every GetGlobal in every Tier 2 function takes the slow path until caches repopulate.
- **No cache prefetch**: the first call to a newly-compiled function always misses on every GetGlobal until Go populates the slots.
- **Generation pointer load is per-GetGlobal**: no CSE of the generation check across consecutive GetGlobals in the same block, even though `*genPtr` cannot change within a single Tier 2 block (no in-block SetGlobal because SetGlobal op-exits).
- **Nil globals cannot be cached** (see invariants) — a function that reads a globally-stored nil sentinel round-trips on every access.
- **No Tier 1 ↔ Tier 2 shared cache**: Tier 1 has its own `BaselineGlobalCache` (per-baseline-function); Tier 2 maintains `CompiledFunction.GlobalCache` separately. Both watch the same generation counter but do not share slots.

## Tests

- `emit_tier2_correctness_test.go` — end-to-end OpGetGlobal via Tier 2
- `tiering_manager_test.go` — generation invalidation behaviour
- `tier1_handlers_test.go` — SetGlobal generation bump

## See also

- `kb/modules/emit/overview.md`
- `kb/modules/tier2.md` — `executeGlobalExit` handler
- `kb/modules/tier1.md` — Tier 1's parallel `BaselineGlobalCache`
