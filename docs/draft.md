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

*[This post is being written live. Implementation next...]*
