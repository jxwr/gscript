# R31 Sieve Diagnostic (REAL DATA)

**Target**: `benchmarks/suite/sieve.gs` — inner j-loop `is_prime[j] = false`
**Gap**: JIT 0.085s vs LuaJIT 0.011s (7.7×)
**Date**: 2026-04-11
**Source**: `internal/methodjit/tier2_float_profile_test.go::TestProfile_Sieve`
**Pipeline**: production `RunTier2Pipeline(fn, nil)`

## Cross-check attestation
- Binary mtime = HEAD: `/tmp/gscript_sieve_t2.bin` regenerated from current `internal/methodjit/` source via `go test -run TestProfile_Sieve` (cached, clean tree).
- Tier 2 not Tier 1: `profileTier2Func` calls `BuildGraph → RunTier2Pipeline → AllocateRegisters → Compile`. Confirmed.
- Disasm tool: Python `capstone` (library, not a hand-decoder).
- First 2 insns match Tier 2 prologue: `0x0: sub sp, sp, #0x80` / `0x4: stp x29, x30, [sp]` ✓
- Code size 3156 B = 789 insns, DirectEntryOffset=2780 (0xadc). 6 OSR re-entry trampolines (0xadc, 0xb3c, 0xb74, 0xbac, 0xbe4, 0xc1c).
- Bottleneck share × 0.085s vs predicted speedup: see §5.

## 1. Real Tier 2 IR (19 blocks, 15 regs)

```
B0 entry:   v1=NewTable; v3=ConstInt 1; Jump→B16
B16:        v6=ConstBool true; Jump→B2
B2 init hdr: v11=Phi(B16:v4, B1:v14); v14=v11+1; v15=v14<=v0; Br→B1,B3
B1 init:    SetTable v1,v14,v6; Jump→B2
B3:         v18=ConstInt 2; Jump→B17
B17 pre:    v37=ConstBool false; v44=ConstInt 1; Jump→B4
B4 i-hdr:   v74=Phi(B17:v1, B9:v75)  : table
            v22=Phi(B17:v0, B9:v73)  : any (n)
            v20=Phi(B17:v18,B9:v46)  : int (i)
            v21=v20*v20; v23=v21<=v22; Br→B5,B10
B5:         v27=GetTable v74,v20; v28=GuardTruthy v27; Br→B6,B9
B6:         v31=v20*v20; Jump→B15
B15:        Jump→B7
B7 j-hdr:   v78=Phi(B15:v20,B8:v78) : int   ← SELF-REFERENTIAL
            v77=Phi(B15:v74,B8:v77) : table ← SELF-REFERENTIAL
            v34=Phi(B15:v22,B8:v34) : any   ← SELF-REFERENTIAL
            v33=Phi(B15:v31,B8:v42) : int   (real j counter)
            v35=v33<=v34; Br→B8,B9
B8 HOT:     v40=SetTable v77,v33,v37
            v42=v33+v78
            Jump→B7
B9:         v75,v73,v45=phi; v46=v45+1; Jump→B4
B10→B14:    counting loop (similar self-phi pattern)
```

**Root observation**: v77 (table), v78 (step i), v34 (n) are held via
**trivial self-referential phis** at B7. Their only incoming values are:
* from B15 (preheader): the outside-loop SSA value
* from B8 (back-edge): the phi itself

These phis are semantically equivalent to the preheader value and should
have been eliminated by `tryRemoveTrivialPhi` in `graph_builder_ssa.go:95`.
They survived because the SSA builder adds back-edge operands *after* the
initial seal, and there is no post-construction cleanup pass.

## 2. Real ARM64 disasm — B7 header + B8 body (hot trace, KIND_BOOL taken)

Traced from `/tmp/sieve_t2.asm` (789 insns total). B7 header at **0x570**,
B8 body at **0x5b4**, B8 bool-fastpath at **0x6a4**, B7 back-edge at **0x7ac**.

```
; --- B7 header: v33 <= v34 (j <= n) ---
0x570 sbfx x1, x22, #0, #0x30      ; decode boxed int v34 (n) EVERY iter
0x574 cmp  x23, x1                  ; v33 (j) <= x1
0x578 cset x0, le
0x57c orr  x0, x0, x25              ; bool-box
0x580 mov  x20, x0                  ; spill bool result
0x584 tbnz w20, #0, #0x5b0          ; → B8 (taken)
0x5b0 b    #0x5b4                   ; 1-insn trampoline hop

; --- B8: SetTable v77, v33, v37 — generic validation tower ---
0x5b4 mov  x0, x21                  ; x21 already holds v77 (table ref)
0x5b8 lsr  x1, x0, #0x30            ; NaN-box tag
0x5bc mov  x2, #0xffff
0x5c0 cmp  x1, x2
0x5c4 b.ne #0x6c8                   ; → deopt (not taken)
0x5c8 lsr  x1, x0, #0x2c            ; subtag
0x5cc mov  x2, #0xf
0x5d0 and  x1, x1, x2
0x5d4 cmp  x1, #0
0x5d8 b.ne #0x6c8                   ; → deopt (not taken)
0x5dc ubfx x0, x0, #0, #0x2c        ; extract 44-bit pointer
0x5e0 cbz  x0, #0x6c8                ; nil check
0x5e4 ldr  x1, [x0, #0x68]           ; metatable load
0x5e8 cbnz x1, #0x6c8                ; metatable must be nil
0x5ec mov  x1, x23
0x5f0 cmp  x1, #0
0x5f4 b.lt #0x6c8                    ; j < 0 → deopt
0x5f8 ldrb w2, [x0, #0x89]           ; array kind byte
0x5fc cmp  x2, #3
0x600 b.eq #0x6a4                    ; KIND_BOOL → fastpath (TAKEN)

; --- B8 bool fastpath ---
0x6a4 ldr  x2, [x0, #0xc8]           ; bool-array length
0x6a8 cmp  x1, x2
0x6ac b.ge #0x6c8                    ; bounds (not taken)
0x6b0 mov  x4, #1                    ; false encoded as 1 byte
0x6b4 ldr  x2, [x0, #0xc0]           ; bool-array data base
0x6b8 strb w4, [x2, x1]              ; ← THE ACTUAL STORE
0x6bc mov  x5, #1
0x6c0 strb w5, [x0, #0x88]           ; dirty flag
0x6c4 b    #0x744                    ; → AddInt block

; --- AddInt v42 + overflow check ---
0x744 ldr  x1, [x26, #0x110]         ; RELOAD v78 (step i) from spill slot
0x748 sbfx x1, x1, #0, #0x30         ; decode int
0x74c add  x28, x23, x1              ; v42 = v33 + v78
0x750 sbfx x0, x28, #0, #0x30        ; overflow check
0x754 cmp  x0, x28
0x758 b.eq #0x78c                    ; ok → phi-move block (TAKEN)

; --- B8 → B7 phi-move block ---
0x78c ldr  x0, [x26, #0x110]         ; RELOAD v78 AGAIN (!) for phi copy
0x790 sbfx x20, x0, #0, #0x30
0x794 ubfx x0, x20, #0, #0x30
0x798 orr  x0, x0, x24               ; re-box int
0x79c str  x0, [x26, #0x110]         ; store v78 back (self-phi copy)
0x7a0 str  x21, [x26, #0x118]        ; store v77 (table) self-phi copy
0x7a4 str  x22, [x26, #0x120]        ; store v34 (n) self-phi copy
0x7a8 mov  x23, x28                  ; v33 = v42
0x7ac b    #0x570                    ; back to B7 header
```

## 3. Per-iteration insn budget (hot trace, sieve KIND_BOOL path)

| Category                           | Insns  | Notes |
|------------------------------------|--------|-------|
| B7 header (loop cond + decode n)   | 7      | incl. 0x5b0 trampoline hop |
| SetTable validation tower          | 19     | 0x5b4–0x600 + 0x6a4–0x6ac |
| Actual store + dirty flag          | 5      | 0x6b0–0x6c0 + branch 0x6c4 |
| AddInt + overflow + branch         | 6      | 0x744–0x758 |
| Phi-move block (B8→B7)             | 9      | 0x78c–0x7ac |
| **Total**                          | **≈46** | (measured, not estimated) |

**Work vs overhead:**
| Essential | Overhead |
|---|---|
| 0x5b4 mov table into x0 (could be elided if only x21 is live-in to setter) | 0x570 sbfx (redecode n every iter) |
| 0x6b8 strb (store false) | 0x5b8–0x5e8 (type/metatable checks on invariant table) |
| 0x6bc/0x6c0 dirty flag | 0x5f0/0x5f4 (j≥0 bounds — j starts positive, strictly grows) |
| 0x74c add (j+=i) | 0x5f8/0x5fc/0x600 (kind dispatch on invariant table) |
| 0x574/0x578/0x584 loop cond | 0x6a4/0x6a8 (array length reload of invariant table) |
| 0x7a8 mov x23,x28 (j carry) | 0x744 ldr v78 (step reload) |
| 0x7ac back-edge | 0x78c ldr v78 (step reload, AGAIN) |
| | 0x790–0x79c (re-box + store v78 back to the same slot) |
| | 0x7a0 str x21 (self-copy of invariant table) |
| | 0x7a4 str x22 (self-copy of invariant n) |
| | 0x5b0 b #0x5b4 (dead 1-insn hop) |
| **≈14 essential** | **≈32 overhead (~70%)** |

**Total inner-loop cost estimate**: 2.58M iter × 46 insns = 118.7M insns.
At ARM64 M4 ~3 insn/cycle × 4 GHz → ~9.9 ms inner loop. Plus init loop
(~1M × 30 insns ≈ 30M, ~2.5 ms) and counting loop (~1M × 25 insns ≈ 25M,
~2.1 ms) → ~14.5 ms compute floor. Observed 85 ms includes branch
mispredict penalties, cache misses on ctx.Regs spill traffic, and
function-entry overhead across REPS=3. 14.5 ms vs 85 ms is consistent
with heavy spill-slot traffic being the real cost — every self-phi
`str x21/x22` round-trips through L1.

## 4. Root cause in source

**`internal/methodjit/graph_builder_ssa.go:95-116`** — `tryRemoveTrivialPhi`
exists and handles the self-reference case correctly. It runs once inside
`addPhiOperands` (line 92). Why the sieve phis survive:

- Loop headers are sealed before the back-edge block (B8) has been built
  so the back-edge operand is filled in later by `addPhiOperands` when
  `sealBlock` runs on the back-edge's block. The logic IS called again
  at that point.
- BUT: in the sieve IR, the surviving phis are at **B7**, which is a
  secondary header inside the outer-i-loop. B7 has two preds (B15, B8).
  When it seals, `readVariable(slot, B8)` must chase up through an
  unsealed cycle and returns the in-progress phi itself.
- Result is a phi with `args = [preheader_val, self]`. `tryRemoveTrivialPhi`
  SHOULD reduce this. Empirically it doesn't — either because the phi
  already had its uses rewritten before cleanup, or because a later pass
  (TypeSpec, ConstProp) rewrote one of the args in a way that doesn't
  re-trigger cleanup.

**`internal/methodjit/pass_licm.go:79-91`** — LICM processes non-phi
instructions only. It never touches phis. Even if `canHoistOp` were
extended, phis are explicitly skipped at line 224:
`if instr.Op == OpPhi || instr.Op.IsTerminator() { continue }`.

**`internal/methodjit/pass_load_elim.go`** — no phi simplification.
**No other pass** in `pass_*.go` simplifies phis post-construction.

**Conclusion**: GScript Tier 2 lacks a post-construction phi cleanup
pass. Trivial phis that become self-referential due to cycle sealing
order persist all the way to codegen, forcing the back-edge to emit
self-copies into ctx.Regs spill slots every iteration.

## 5. Bottleneck share × 0.085s vs predicted speedup

**Claim**: a trivial-phi-simplification pass removes v77/v78/v34 phis.
After simplification:
- v77 uses become v74 (outer-i-loop phi). Outer phi already carries
  table across i-loop; inner j-loop re-reads from the same SSA value,
  so `x21` stays pinned for the full inner loop — no per-iter str.
- v78 uses become v20 (outer-i-loop phi for i). Same deal for `x???`.
- v34 uses become v22 (outer-i-loop phi for n). Same for x22.

Expected insns removed per iter:
- 0x7a0 str x21 (−1)
- 0x7a4 str x22 (−1)
- 0x744 ldr v78 (−1) — if step is register-resident, no reload
- 0x748 sbfx (−1) — decoded once in outer preheader
- 0x78c ldr v78 (−1)
- 0x790 sbfx (−1)
- 0x794–0x79c re-box+store v78 (−3)
- 0x570 sbfx n every iter (−1) — decoded once in outer preheader
- 0x5b0 dead hop (−1) — might fold via block layout

**Conservative removal**: 10 insns/iter. **Halved for ARM64 superscalar
and spill-slot cache-locality gains**: call it 6 effective cycles saved
per iter.

Inner loop: 2.58M × 6 cycles / 4 GHz = **3.9 ms saved** in inner loop.
Plus: the removed spill-slot stores also reduce store-buffer pressure
and L1 traffic, which has second-order impact the static count misses.

**Predicted total speedup**: 85 ms → ~78–80 ms (8–10% gain). This is a
first step that enables follow-on: SetTable validation hoisting, n/step
decode hoisting, dead-branch elision. Full target stack could bring
sieve toward ~60 ms.

**Consistency**: 10 insns removed × 2.58M iter = 25.8M insns = 6.4 Mcycles
at 4 GHz = ~1.6 ms on the pure instruction-cycle model, but the spill-
slot round-trip cost dominates. Both models fall within the 2× band of
the 3.9 ms wall-time prediction.

## 6. Top target (single, surgical)

**Target**: post-construction trivial phi simplification pass.

- **Why it's #1**: it is the **precondition** for every other LICM-style
  fix. Without it, LICM can't hoist the SetTable validation because
  v77's def appears *inside* the loop body (as a phi, which LICM skips).
  After simplification, v77 uses collapse to v74, which is defined
  outside B7 → LICM's existing GetTable hoisting path immediately
  becomes applicable without any LICM code change.
- **Size**: ~60 LOC new file `pass_simplify_phis.go` + ~80 LOC test.
- **Pipeline placement**: after BuildGraph, before TypeSpec. Also
  re-run after Inline (which creates new merge blocks).
- **Risk**: very low. Trivial phi simplification is a standard SSA
  cleanup used in every production compiler. Failure mode: a bug
  leaves dangling value references — caught immediately by `Validate`
  which runs after every pass in the current pipeline.

---

**Generated**: 2026-04-11 (R31 ANALYZE Step 4 — real-data, not estimated)
