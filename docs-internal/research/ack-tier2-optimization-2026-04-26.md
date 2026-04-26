# Ackermann Tier2 Optimization Review - 2026-04-26

## Executive Summary

The original Ackermann regression was not caused by the idea of Tier2 itself. It was caused by the interaction of three infrastructure gaps:

1. `<main>` could not resolve top-level function declarations before they executed, so the conservative loop-call filter kept `<main>` at Tier1.
2. Tier2 recursive native calls used stack frames invisible to Go stack-split checks, so deeper recursion could corrupt runtime state unless stack budget was reserved before entering JIT code.
3. The raw-int numeric self-call path needed a real call-boundary ABI: raw args/return, caller liveness, boxed fallback materialization, and numeric self-global handling had to be unified.

The stable fix in this round keeps method JIT, does not introduce tracing, and moves Ackermann from "JIT slower than VM" to a clear Tier2 win:

| Mode | Result |
| --- | ---: |
| CLI VM `benchmarks/suite/ackermann.gs` | ~0.287s |
| CLI JIT before `<main>` fix | ~0.41s |
| CLI JIT after boxed self-call path | ~0.027-0.030s |
| CLI JIT after raw self ABI v1 | ~0.017-0.019s |
| CLI JIT after thin raw entry + 64B raw caller frame | ~0.014-0.015s steady |
| LuaJIT `benchmarks/lua/ackermann.lua` | ~0.006-0.007s |
| Go benchmark VM `BenchmarkGScriptVMAckermannWarm` | ~512-551us/op |
| Go benchmark JIT `BenchmarkGScriptJITAckermannWarm` | ~58-70us/op |
| Go forced Tier2 steady `BenchmarkAckermannForcedTier2CallValueSteady` | ~26.3-30.3us/op |

Tier2 is now clearly faster than the VM for Ackermann. In the steady CLI script
it is roughly 19x faster than VM and about 2-2.5x slower than LuaJIT on this
machine. The remaining gap is now mostly recursive frame/call overhead rather
than boxed argument/result traffic.

## Why Tier2 Was Slower Than VM

The suite benchmark spends almost all time in:

```gscript
for r := 1; r <= REPS; r++ {
    result = ack(3, 4)
}
```

Before this round, `ack` could be compiled, but `<main>` stayed at Tier1 because the loop body still contained an `OpCall`. The filter could not prove that `ack` was a native-call candidate because `ack` was assigned by `OP_CLOSURE; OP_SETGLOBAL` in the same top-level body and had not yet appeared in the VM global table when `<main>` was compiled.

The result was a bad tier boundary: hot loop in Tier1 repeatedly crossing into Tier2.

## Changes Made

### Lexical top-level function discovery

Added a conservative proto-prefix scanner:

- Recognizes only entry straight-line `OP_CLOSURE` / `OP_MOVE` / `OP_SETGLOBAL` declarations.
- Stops at the first executable non-declaration instruction.
- Uses this map only for the Tier2 loop-call safety filter, not for InlinePass.

This avoids the previous unsafe experiment where top-level lexical globals were fed directly to InlinePass and caused loop-callee inlining regressions.

### VM global map key fix

`buildInlineGlobals()` now keys the inline map by the actual VM global name rather than `cl.Proto.Name`. That makes aliases and global binding semantics less surprising.

### Tier2 native stack reserve

Added `ensureTier2NativeStack()` before entering Tier2 native code. Tier2 recursive JIT frames are invisible to Go stack growth checks, so this reserves enough goroutine stack before recursive native calls run.

This fixed the prior repeated-call runtime corruption canary.

### Static self-call boxed fast path

Added a boxed static self-call fast path for non-tail recursive calls:

- Keeps boxed argument/result semantics.
- Keeps `CallMode=1`, `ExitCode`, `BaselineReturnValue`, and `ctx.Regs` protocol.
- Skips generic closure type checks, call IC lookup, direct-entry load, and global-cache switching for proven static self calls.
- Tail self calls still use the existing in-frame loop lowering.

This is a conservative method-JIT optimization and does not require the raw-int ABI to be correct.

## Raw-Int Self ABI Findings

The raw-int self ABI is now enabled for specialized static self calls accepted by `AnalyzeSpecializedABI`.

Key points:

- The raw call path is separate from the boxed `emitCallNative` path.
- The caller passes raw int args in `X0..X3` and receives raw int in `X0`.
- The caller saves raw args plus the boxed function operand in a small native frame so fallback can materialize a normal VM call frame.
- `ctx.BaselineClosurePtr` stays invariant while the raw callee runs; v1 raw
  ABI rejects upvalues and nested protos, so the self closure does not need a
  per-call context switch.
- Numeric self `GETGLOBAL` materializes the current closure instead of taking the global cache exit path. This prevents mid-tier Ackermann fallback storms.
- VM fallback results are tag-checked before rejoining the raw continuation; non-int fallback results deopt instead of being unboxed as raw ints.
- `t2_numeric_self_entry_N` now uses a thin FP/LR frame. Raw callers preserve
  live allocated registers through selective spill/reload around the BL.
- The raw caller frame is now 64 bytes and only stores caller `mRegRegs`,
  caller `CallMode`, raw args, and the boxed function operand needed for
  fallback.
- The raw caller carries the callee VM frame base directly in `mRegRegs`; it no
  longer writes callee `ctx.Regs` before the BL or reloads it in the numeric
  entry.

The current implementation still keeps a callee VM frame window and a raw caller
fallback frame, so it is correct but not yet LuaJIT-class.

This is the main remaining path toward LuaJIT-class Ackermann performance.

## V8/Tier2 Takeaway

The user's original assumption, "Tier2 should always be faster than Tier1", is not a safe compiler invariant. V8 does not optimize by tier number alone; it optimizes when feedback, guards, deopt metadata, and calling conventions make the optimized code cheaper after accounting for exits.

For this codebase, the comparable rule is:

> Tier2 should only take ownership of a loop when the loop body can stay native or when exits are provably cold.

The Ackermann fix follows that rule: `<main>` is allowed into Tier2 only after the loop call can be proven to target a self-recursive numeric candidate, and raw-int recursion is enabled only for functions whose specialized ABI descriptor proves a raw self-recursive shape.

## Next Work

The next high-impact work is no longer "turn raw self BL on"; it is to reduce the remaining overhead in the now-correct raw ABI:

1. Introduce an explicit IR/call-lowering concept for static self calls, separate from generic `OpCall`, so hot self `GETGLOBAL` instructions disappear from both passes.
2. Remove or shrink the callee VM frame window on raw success.
3. Move fallback-only raw arg/function saves out of the hot path once precise
   callee-exit resume metadata exists.
4. Tighten raw ABI eligibility with range/deopt metadata so overflow-heavy functions avoid entering raw continuations.
5. Re-measure Ackermann against LuaJIT after each frame/call reduction.
