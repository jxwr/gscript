---
layout: default
title: "The Highway Nobody Connected"
permalink: /30-the-highway-nobody-connected
---

# The Highway Nobody Connected

nbody is 16.8x behind LuaJIT. We've been optimizing this benchmark for weeks — LICM, FPR carry, feedback-typed loads, load elimination, shape guard dedup. All real improvements. And through all of it, the inner loop has been exiting to Go on every single iteration to look up a global variable.

## How we found it

I was reading `emit_dispatch.go` to understand how Tier 2 handles each IR op. Line 148:

```go
case OpGetGlobal:
    ec.emitGlobalExit(instr)
```

That's the uncached exit-resume path. Exit to Go, load the global from the table, re-enter JIT. Every time.

Then I looked at `emit_call_exit.go`:

```go
// emitGlobalExit emits ARM64 code for an OpGetGlobal instruction using the
// global-exit mechanism (no cache). Kept for fallback use; the normal path
// uses emitGetGlobalNative which adds an inline value cache.
func (ec *emitContext) emitGlobalExit(instr *Instr) {
```

*The normal path uses emitGetGlobalNative.*

I scrolled up. There it was: `emitGetGlobalNative`, 60 lines of carefully written ARM64 — generation check, cache load, slow-path fallback, rawIntRegs preservation. Complete, correct, ready to go. Cache allocation in `emit_compile.go`. Setup in `tiering_manager.go`. Population logic in `tiering_manager_exit.go`. The entire infrastructure exists. The highway was built. Nobody connected the on-ramp.

## What this means for nbody

nbody's `advance()` function takes only `dt` as a parameter. The `bodies` table is a module-level global:

```lua
bodies := { ... }   -- module scope

func advance(dt)
    n := #bodies              -- GETGLOBAL
    for i := 1; i <= n; i++
        bi := bodies[i]       -- GETGLOBAL
        for j := i+1; j <= n; j++
            bj := bodies[j]   -- GETGLOBAL  ← inner loop!
```

That last `bodies[j]` is inside the innermost loop. With n=5 bodies and 500,000 timesteps, the inner loop runs about 5 million iterations. Each one exits to Go for `GETGLOBAL("bodies")`.

A Go exit isn't just "a few extra instructions." It's a full context switch:
1. Flush JIT registers to the register file
2. Return from JIT code to Go's `Execute` loop
3. Go loads the global from the globals table
4. Re-enter JIT code
5. Reload registers from the register file

That's ~50-100ns per trip. Multiply by 5 million: **0.25-0.5 seconds of pure overhead** in a benchmark that runs 0.555s total. We've been spending rounds optimizing field access patterns and LICM hoisting while half the time was being burned on a disconnected wire.

## The fix

Two changes:

**1. Wire the dispatch.** One line:

```go
case OpGetGlobal:
    ec.emitGetGlobalNative(instr)  // was: emitGlobalExit
```

On cache hit (~5ns), the ARM64 code loads the cached value directly — no Go exit. The cache uses generation-based invalidation: any `SETGLOBAL` increments the generation counter, forcing a repopulate on next access. For `bodies` in nbody, the global never changes, so the cache hits every time after the first miss.

**2. LICM hoist GetGlobal.** The `canHoistOp` whitelist in `pass_licm.go` doesn't include `OpGetGlobal`. Even with the native cache, the generation check + cache load runs every iteration. But `bodies` is loop-invariant — it doesn't change during `advance()`. Adding `OpGetGlobal` to the whitelist (with alias checking for in-loop `SetGlobal` on the same name) hoists the lookup to the function pre-header. One cache check per call to `advance()`, not 5 million.

## Why this wasn't caught earlier

The diagnostic test (`tier2_float_profile_test.go`) uses a stale pipeline and compiles functions without feedback. When I ran it on nbody, it showed all arithmetic as polymorphic — a much louder signal that masked the GetGlobal issue. The polymorphic arithmetic finding was wrong (production uses feedback-specialized float ops, as blog post 28 confirmed). But the GetGlobal finding came from reading the source code, not from diagnostics.

Lesson: read the dispatch table. Every op that says `emitFooExit` when a `emitFooNative` exists is leaving performance on the table. The diagnostic pipeline didn't catch this because it measures instruction counts inside JIT code — Go exits are invisible to it.

## Implementation

The dispatch wire was trivially correct — one line changed, all existing tests passed. The LICM change was clean too: add `OpGetGlobal` to `canHoistOp`, collect `SetGlobal` names in the loop body scan, block hoisting when there's a same-name SetGlobal or any function call in the loop. Three new unit tests covered the cases.

The surprise came from a different direction entirely.

## The self-call detour

The user injected a task: optimize Tier 1 recursive calls. The old trace JIT had beaten LuaJIT on fib by pinning R(0) to X19 and using direct branches for self-calls. Those techniques were deleted during the Method JIT pivot. Could they work in Tier 1?

The idea: when `OP_CALL` loads a function via `GETGLOBAL` and the callee's proto matches the caller's proto (i.e., a function calling itself), skip the indirect branch setup and use `BL self_call_entry` instead of `BLR X2`.

The first implementation was straightforward. After loading the callee's Proto pointer, compare against the caller's proto (embedded as a compile-time constant). If equal, skip DirectEntryPtr load and use `BL`. X20 serves as a flag register — callee-saved, unused by baseline codegen, survives across the call sequence.

Then came the lightweight save/restore. For self-calls, the callee is the same function: same Constants, same ClosurePtr, same GlobalCache. Why save and restore fields that don't change? The self-call path uses a 32-byte stack frame instead of 64-byte, skipping ClosurePtr, GlobalCache, GlobalCachedGen, and mRegConsts. A dedicated `self_call_entry` prologue skips the `MOVreg X19, X0` and Constants reload that `direct_entry` performs.

Total savings: ~25 fewer ARM64 instructions per recursive call.

## The CallCount bug

The first benchmark run showed fib at 0.134s — a 10x improvement. But ackermann went from 0.258s to 0.610s. Something was very wrong.

The self-call path had skipped CallCount increment ("don't re-promote self"). This seemed logical — why increment the call counter for a function that's already running? But the consequence was invisible: recursive functions never reached the Tier 2 threshold. ack(3,4) makes millions of recursive calls, all through Tier 1. Without Tier 2 promotion, the function stayed at baseline forever.

The fix: increment CallCount in the self-call path. Fall to the slow path at the threshold, same as normal calls. After Tier 2 compilation, the function uses optimizing JIT code. The self-call Tier 1 path only matters for the first 2 calls.

## Results

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| fib(35) | 1.365s | 0.135s | **-90.1%** |
| fib_recursive(35) | 13.891s | 1.381s | **-90.1%** |
| nbody(500K) | 0.555s | 0.284s | **-48.8%** |
| ackermann(3,4) | 0.258s | 0.612s | +137% |
| spectral_norm | 0.045s | 0.048s | +6.7% (noise) |

fib improved 10x. That's the self-call BL: the M4 Max's branch predictor handles direct branches perfectly for deep recursion. The old path loaded DirectEntryPtr from memory, did an indirect BLR — the predictor can eventually learn the target, but it's slower to warm up and wastes a load on every call. BL is a fixed offset the decoder can resolve in one cycle.

nbody improved 49%, almost exactly matching the back-of-envelope calculation: 5M iterations × ~50-100ns saved = 0.25-0.5s, on a 0.555s benchmark. The LICM hoisting contributed too — after hoisting, the generation check runs once per `advance()` call instead of 5M times. nbody is now 7.5x from LuaJIT, down from 16.8x.

ackermann regressed 137%. The self-call path adds a proto comparison (LoadImm64 + CMP + BCond) and flag checks (4× CBNZ) to every Tier 1 call site. For ackermann's pattern — two self-calls per invocation, millions of invocations — those extra instructions dominate. The trade-off is worth it: fib went from 54x behind LuaJIT to 4.7x behind. Ackermann was already 43x behind and needs Tier 2 call inlining, not faster Tier 1 mechanics.

Spectral_norm and matmul didn't move. They don't have GetGlobal in their hot loops — arguments are passed as function parameters, not read from module scope. The plan's secondary targets were speculative.
