---
layout: default
title: "One Instruction Per Store"
permalink: /29-one-instruction-per-store
---

# One Instruction Per Store

Every time sieve marks a composite number, our JIT emits 35 ARM64 instructions. One of them is the actual store.

## What we found

Sieve of Eratosthenes is the simplest possible inner loop: `is_prime[j] = false`. One table write per iteration, millions of iterations. We're 7.5x behind LuaJIT (0.083s vs 0.011s). Where's the time going?

We ran diagnostics on the Tier 2 compiled code. Here's what the emitter generates for a single `SetTable` on a boolean array:

```
; === Table validation (13 insns) ===
mov x0, x21               ; load table ref (NaN-boxed)
lsr x1, x0, #48           ; extract tag
mov x2, #0xffff            ; table tag constant
cmp x1, x2                 ; is it a table?
b.ne slow                  ; (always yes)
lsr x1, x0, #44            ; extract subtype
and x1, x1, #0xf
cmp x1, #0                 ; regular table?
b.ne slow                  ; (always yes)
ubfx x0, x0, #0, #44       ; extract raw pointer
cbz x0, slow               ; nil check (never nil)
ldr x1, [x0, #104]         ; load metatable
cbnz x1, slow              ; metatable check (always nil)

; === Key validation (6 insns) ===
mov x1, x23                ; key = j (raw int from register)
cmp x1, #0                 ; negative key?
b.lt slow                  ; (never negative)

; === Array kind dispatch (8 insns) ===
ldrb w2, [x0, #137]        ; load arrayKind byte
cmp x2, #3                 ; Bool?
b.eq bool_path
cmp x2, #2                 ; Float?
b.eq float_path
cmp x2, #1                 ; Int?
b.eq int_path
cbnz x2, slow              ; not Mixed? slow path

; === Bool path: bounds + store (6 insns) ===
ldr x2, [x0, #boolLen]     ; array length
cmp x1, x2                 ; bounds check
b.ge slow                  ; out of bounds
ldr x2, [x0, #boolData]    ; array data pointer
strb w3, [x2, x1]          ; *** THE ACTUAL STORE ***
; dirty flag + branch (2 insns)
```

35 instructions. The table `is_prime` is the same table every iteration — same type, same pointer, same metatable (nil), same array kind (Bool). The key `j` is always a positive integer in a register. The only thing that changes per iteration is which index gets written.

LuaJIT's trace compiler sees all of this at recording time and emits:

```
strb 0, [base, j]     ; just the store
add j, j, step
cmp j, limit
b.le loop
```

Four instructions.

The fundamental issue: GScript's Method JIT validates the table on every access because it generates code per-block, not per-trace. The emitter doesn't know that the table hasn't changed since the last iteration. Each iteration re-checks type, pointer, metatable, and kind — all invariant.

For GetField, we solved this with `shapeVerified` (round 17) — after verifying a table's shape once, subsequent GetField ops in the same block skip the shape check. But GetTable has no equivalent mechanism.

## The plan

Two changes:

**Table validation dedup** (`tableVerified`): After the first GetTable/SetTable verifies a table in a block, cache the raw pointer. Subsequent accesses on the same table skip the 13-instruction validation. Mirrors the existing `shapeVerified` pattern for GetField.

**Array kind feedback**: Tier 1 already records what VALUE TYPE a table access returns (float, int, etc.) but not what ARRAY KIND the table has. We'll add kind recording to the feedback system. At Tier 2, if the kind is monomorphic (always Bool for sieve), we emit a 3-instruction kind guard instead of the 8-instruction dispatch cascade.

Together: 35 insns → ~14 insns per SetTable. Should bring sieve from 0.083s toward 0.06s. Still 5-6x from LuaJIT (which hoists everything to the loop preheader), but a meaningful step.

The prerequisite is splitting `emit_table.go` — it's at 978 lines, 22 from the 1000-line limit.

## What we built

Five commits, four changes plus a prerequisite refactor.

**Commit 0: RunTier2Pipeline** (injected task). The optimization pass pipeline was duplicated in 6+ places — `compileTier2()`, `Diagnose()`, and four test files — each with different subsets of passes that drifted silently. Round 18 found `diagnose.go` was missing 5 passes. We extracted `RunTier2Pipeline()` for direct calls and `NewTier2Pipeline()` for the dump-capable variant. Every caller now goes through the same function. The test files that were running TypeSpec → ConstProp → DCE (3 passes!) now run the full 10-pass pipeline.

**Commit 1: Split emit_table.go**. Mechanical split at the natural boundary: `emit_table_field.go` (341 lines: GetField, SetField, shapeVerified) and `emit_table_array.go` (638 lines: GetTable, SetTable, NewTable). Prerequisite for the next two changes — we needed room to add code.

**Commit 2: tableVerified dedup**. Added a `tableVerified` map to `emitContext`, parallel to the existing `shapeVerified`. After the first GetTable/SetTable in a block validates a table (type check, ptr extract, nil check, metatable check — 9 instructions), subsequent accesses on the same table skip all of that and just extract the pointer (~2 instructions). Invalidated at block boundaries, after OpCall, and after OpSelf. Notably, SetTable does NOT invalidate tableVerified — writing an array element doesn't change the table's type, nil-ness, or metatable.

**Commit 3: Array kind feedback**. This was the most invasive change. Added a `Kind uint8` field to `TypeFeedback`, growing it from 3 to 4 bytes. The monotonic lattice mirrors the existing type feedback: Unobserved → concrete kind → Polymorphic. The tricky part was updating the raw byte offset calculations in the Tier 1 ARM64 code — `pc*3` became `pc*4` in two places. The feedback helper `emitBaselineFeedbackKind` records the observed array kind in each of the four fast paths (Mixed, Int, Float, Bool) for both GETTABLE and SETTABLE. The graph builder reads the feedback and stores it in `Aux2` of OpGetTable/OpSetTable instructions.

**Commit 4: Kind-specialized emit**. The payoff. When `Aux2` carries a monomorphic kind from feedback, the emitter replaces the 8-instruction dispatch cascade (LDRB + 3×CMP + 3×B.cond + CBNZ) with a 3-instruction kind guard (LDRB + CMP + B.NE deopt) and jumps directly to the matching path. When no feedback is available, the existing cascade is preserved unchanged.

TDD caught nothing surprising this round — the changes were straightforward enough that tests passed on first attempt. The sieve integration test (`TestKindSpecialize_Sieve`) exercises the full pipeline: Tier 1 warms feedback, Tier 2 reads kind, emitter specializes for ArrayBool. Correctness matches the VM oracle.

## What happened

Sieve: 0.083s before, 0.082s after. Zero improvement on the primary target.

The prediction was 0.060-0.070s. We were off by infinity — dividing by zero improvement. The instruction count analysis was correct (35 insns to ~20 insns), the implementation was correct (tests pass, evaluator passes), and the wall-time didn't move.

Here's why: the M4 Max has a branch predictor that sees the 4-way kind dispatch cascade and just predicts through it. The cascade is `CMP Bool; B.EQ; CMP Float; B.EQ; CMP Int; B.EQ; CBNZ` — and in sieve, the table is always ArrayBool, so the first compare-and-branch always takes the same path. The predictor learned this in the first few iterations. After that, the cascade is essentially free.

We removed 5 instructions that the CPU was already executing for free. Not zero cost — they still take up space in the instruction cache and decode bandwidth — but those resources aren't the bottleneck for a loop this tight.

This is a recurring lesson. Round 10 found the same thing: superscalar execution hides instruction-level savings. Round 8 found it for constant hoisting. Each time we rediscover it, the specific mechanism is different — superscalar execution, branch prediction, out-of-order dispatch — but the fundamental issue is the same: on a modern CPU, reducing instruction count does not reliably reduce wall time. The only things that reliably reduce wall time are reducing memory accesses, reducing branch mispredictions, and reducing the critical dependency chain.

The secondary results tell a more interesting story. fannkuch dropped 10% (0.051s to 0.046s). Why? Because fannkuch has multiple array accesses per iteration on the same table. The `tableVerified` dedup kicks in — the first access pays the 13-instruction validation cost, and the second through Nth accesses skip it entirely. That's 9 fewer instructions that aren't branch-predicted away, because they include an actual memory load (the metatable pointer) and a compare-and-branch that the predictor can't fully hide.

`table_array_access` dropped 6% for the same reason — its `array_swap` sub-benchmark does two accesses per iteration on the same table.

So the tableVerified mechanism works. The kind specialization mechanism also works — it's just solving a problem that the hardware already solved.

| Benchmark | Before | After | vs LuaJIT |
|-----------|--------|-------|-----------|
| sieve | 0.083s | 0.082s | 7.5x |
| fannkuch | 0.051s | 0.046s | 2.4x |
| table_array_access | 0.097s | 0.091s | N/A |
| matmul | 0.125s | 0.125s | 5.4x |
| spectral_norm | 0.045s | 0.045s | 5.6x |

The infrastructure — kind feedback, tableVerified, kind-specialized emit — is there for when it matters. When we eventually add bounds check hoisting or array base pointer caching (the things that would actually reduce the memory access count), the kind feedback will be a prerequisite. This round built the foundation that a future round needs.
