---
round: 3
date: 2026-04-14
kind: diagnostic (no production code changes)
follows: round 2 (reverted, reverted round 1)
---

# Round 3 — Pure Diagnostic — sieve inner loop asm walk

## Why diagnostic only

Rounds 1 and 2 both attempted optimizations and both were reverted. Both shared a pattern: "trust the knowledge base's narrative about root cause, predict a win, measure, revert." Round 3 breaks the pattern by making NO prediction and NO code change. It reads the actual asm of sieve's inner loop and records what it sees.

## Sieve inner loop evidence

### IR shape (from `diag/sieve/sieve.ir.txt`)

The hot inner loop is B5 (GetTable + GuardTruthy), B7/B8 (j-loop setting is_prime[j] = false), and B11/B12/B13 (final counting loop):

```
B5 ; preds: B4
    v27 = GetTable   v1, v20 : any
    v28 = GuardTruthy v27 : bool
    Branch v28 → B6, B9

B8 ; preds: B7
    v40 = SetTable v1, v33, v37    ; is_prime[j] = false
    v42 = AddInt   v33, v20        ; j += i
    Jump B7
```

### ARM64 emit observations (from `diag/sieve/sieve.asm.txt`)

**Observation 1 — redundant moves at GetTable→GuardTruthy handoff (addr 0464-0470):**

```
0464  MOVD 232(R26), R20    ; load v27 → R20
0468  MOVD 232(R26), R0     ; load v27 AGAIN → R0 (REDUNDANT)
046c  MOVD R0, R20           ; R0 → R20 (REDUNDANT — R20 already has it)
0470  MOVD R20, R0           ; R20 → R0 (REDUNDANT)
```

Four instructions doing one load's worth of work. The emitter loads the GetTable result twice from memory and does two extra inter-register moves. A cleaner emit would be one MOVD, leaving the value in whichever register the next op expects. Cost: 4 insns × (loop iterations). For a 1M-iter sieve, ~3–4M wasted insns.

**Observation 2 — GuardTruthy multi-compare expansion (addr 0474-0498):**

```
0474  MOVD $-1125899906842624, R1   ; load nil NaN-box tag constant
0478  CMP R1, R0                      ; is value == nil?
047c  BEQ 5(PC)
0480  CMP R25, R0                     ; is value == false?
0484  BEQ 3(PC)
0488  ADD $1, R25, R0                 ; truthy result = false_tag + 1
048c  JMP 2(PC)
0490  MOVD R25, R0                    ; falsy result = false_tag
0494  MOVD R0, R21
0498  TBNZ $0, R21, 2(PC)             ; test bit 0 of result → branch
```

Nine instructions to check "is this value truthy?". The expansion computes a truthy-bit by materializing true/false as NaN-boxed bools, then testing bit 0. LuaJIT's equivalent is ~1–2 insns (direct tag check). The extra cost is ~7 insns per GuardTruthy in a hot loop.

Note: GuardTruthy is in B5, which fires once per outer `i` iteration (when checking `is_prime[i]` before entering the j-loop). For sieve, B5 fires O(n) = 1M times. 7 wasted insns × 1M = 7M insns.

**Observation 3 — SetTable 4-way arrayKind dispatch (addr 0528 onwards):**

SetTable's hot path is ~27 ARM64 insns for the ArrayBool case:
- ~7 insns for NaN-box unbox + ptr-tag check
- ~4 insns for shape guard
- ~1 insn loading `arrayKind` byte
- ~8 insns for the 4-way dispatch (ArrayMixed / ArrayInt / ArrayFloat / ArrayBool)
- ~7 insns for the actual store (boundschk + MOVB into boolArray + dirty flag)

If the emitter knew statically that `v1`'s arrayKind was ArrayBool (via feedback or type specialization), it could skip the 4-way dispatch, saving ~8 insns per SetTable. Hot loop fires O(n log log n) times for sieve ≈ 2.9M iterations. 8 insns × 2.9M ≈ 23M insns.

## Total estimated redundancy in sieve inner loops

Rough sum of the three observations above: ~30M wasted insns per sieve(1M) run. At M4's ~6 insns/cycle effective throughput, that's ~5M cycles = ~1.5ms. The current wall-time gap vs LuaJIT is ~78ms (88ms − 10ms). So the three observations together account for ~2% of the gap. Not 2× improvement; 2%.

**This is the most important finding of Round 3.** The gap between GScript JIT and LuaJIT on sieve is NOT dominated by the patterns I spotted in the asm. It's dominated by something structural — probably LuaJIT's shape-specialized inline cache eliminating the entire dispatch for a cached PC, which GScript's Tier 2 doesn't currently have as tightly.

## What Round 3 did NOT find

- A clear 50%+ improvement from any local change
- A specific dead instruction that can be deleted without refactoring
- A small surgical change with high confidence

Rounds 1 and 2 both tried to find such a change via narrative. Round 3 tried to find it via direct asm reading. Neither approach produced one.

## Directional conclusion

- The 5–9× LuaJIT gaps on sieve/nbody/matmul/spectral_norm are not closable by cosmetic instruction-level cleanups. They require a structural change to the emit layer — e.g., per-PC cached dispatch like LuaJIT's IC, or a unified hot-path for monomorphic table accesses.
- A structural change like that is a Q2 (module-level) or Q1 (architectural) direction, not a Q3 local fix.
- **Round 4 should NOT attempt another same-class Q3 micro-optimization.** Either pivot to a Q2/Q1 investigation, or pivot entirely away from optimization to meta-work (KB refinement, failure analysis, workflow tuning).

## Round 4 candidate directions

Three options to pick from when Round 4 opens:

1. **Write a focused Q2 proposal** for a per-PC specialized cache on SetTable, reading LuaJIT's fast-path as reference. Not an implementation — just a scoped design doc and a test-framework sketch.
2. **Update the KB cards** to reflect what Rounds 1–3 actually learned: the object_creation drift narrative was wrong, the GC scan overhead is not wall-time-dominant, the LuaJIT gap is structural. This is meta-work but prevents the next round from repeating Rounds 1–2.
3. **Pick a different benchmark entirely** — maybe one where the compiled asm is obviously shorter/longer than it needs to be, using mandelbrot (1.09×) as a reference for "good" shape.

My recommendation is (2) first — the KB is misleading future rounds (and me) right now. Then (1) as Round 5 if time permits.
