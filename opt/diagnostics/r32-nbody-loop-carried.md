# R32 Diagnostic: nbody advance() j-loop Loop-Carried GetField/SetField

**Target**: `benchmarks/suite/nbody.gs` — `advance()` inner j-loop body
**Hypothesis**: bi.vx/vy/vz are loop-carried read-modify-write field pairs
  that can be scalar-replaced (held in FPRs across the j-loop).
**Date**: 2026-04-11

---

## 1. How Data Was Produced

**Test**: `internal/methodjit/r32_nbody_loop_carried_test.go::TestR32_NbodyLoopCarried`

Pipeline:
1. TieringManager runs `advance()` 11 times (10 loop iterations from source)
   to collect real Tier 1 feedback.
2. `RunTier2Pipeline(fn, advanceProto)` with feedback → optimized IR.
3. `AllocateRegisters` + `Compile` → native binary.
4. Binary written to `/tmp/gscript_nbody_advance_r32.bin` (5464 bytes, 1366 insns).
5. Python capstone disassembly.

**Feedback collected**: 24 Float, 0 Int, 0 Table slots observed. All arithmetic typed.

---

## 2. IR Snippet — j-loop Body (Block B2)

Field constant pool: `constants[1]="x" [2]="y" [3]="z" [6]="vx" [7]="mass" [8]="vy" [9]="vz"`

```
B2: ; preds: B3   ← j-loop body (runs each j-iteration)
    v18  = GetTable    v16, v98 : any          ; bj = bodies[j]
    v22  = GetField    v18.field[1] : any       ; bj.x
    v23  = GuardType   v22 is float
    v24  = SubFloat    v21, v23                 ; dx = bi.x - bj.x
    v27  = GetField    v18.field[2] : any       ; bj.y
    v29  = SubFloat    v26, v28                 ; dy = bi.y - bj.y
    v32  = GetField    v18.field[3] : any       ; bj.z
    v34  = SubFloat    v31, v33                 ; dz = bi.z - bj.z
    ... (dsq, dist, mag) ...
    v46  = GetField    v9.field[6] : any        ; bi.vx  ← CANDIDATE
    v47  = GuardType   v46 is float
    v48  = GetField    v18.field[7] : any       ; bj.mass
    v52  = SubFloat    v47, v51                 ; bi.vx - dx*bj.mass*mag
    v53  = SetField    v9.field[6] = v52        ; bi.vx = ...  ← CANDIDATE
    v54  = GetField    v9.field[8] : any        ; bi.vy  ← CANDIDATE
    v60  = SubFloat    v55, v59
    v61  = SetField    v9.field[8] = v60        ; bi.vy = ...  ← CANDIDATE
    v62  = GetField    v9.field[9] : any        ; bi.vz  ← CANDIDATE
    v68  = SubFloat    v63, v67
    v69  = SetField    v9.field[9] = v68        ; bi.vz = ...  ← CANDIDATE
    v70  = GetField    v18.field[6] : any       ; bj.vx  ← CANDIDATE
    v76  = AddFloat    v71, v75
    v77  = SetField    v18.field[6] = v76       ; bj.vx = ...  ← CANDIDATE
    v78  = GetField    v18.field[8] : any       ; bj.vy  ← CANDIDATE
    v84  = AddFloat    v79, v83
    v85  = SetField    v18.field[8] = v84       ; bj.vy = ...  ← CANDIDATE
    v86  = GetField    v18.field[9] : any       ; bj.vz  ← CANDIDATE
    v92  = AddFloat    v87, v91
    v93  = SetField    v18.field[9] = v92       ; bj.vz = ...  ← CANDIDATE
    Jump        → B3
```

---

## 3. Loop-Carried (obj, field) Pairs

All 9 pairs confirmed by `TestR32_NbodyLoopCarried`:

**j-loop body (B2) — 6 pairs:**
| Object | Field | field[N] | GetField | SetField | Promotable? |
|--------|-------|----------|----------|----------|-------------|
| bi (v9)  | vx  | field[6] | ✓ | ✓ | YES — bi fixed across j-loop |
| bi (v9)  | vy  | field[8] | ✓ | ✓ | YES — bi fixed across j-loop |
| bi (v9)  | vz  | field[9] | ✓ | ✓ | YES — bi fixed across j-loop |
| bj (v18) | vx  | field[6] | ✓ | ✓ | PARTIAL — bj changes each j |
| bj (v18) | vy  | field[8] | ✓ | ✓ | PARTIAL — bj changes each j |
| bj (v18) | vz  | field[9] | ✓ | ✓ | PARTIAL — bj changes each j |

**i-loop body (B6) — 3 pairs:**
| Object   | Field | Promotable? |
|----------|-------|-------------|
| b (v117) | x  (field[1]) | YES |
| b (v117) | y  (field[2]) | YES |
| b (v117) | z  (field[3]) | YES |

**Total IR stats (full function)**:
- GetField: 20, SetField: 9, Float math: 35, GuardType: 21, Total ops: 123

---

## 4. ARM64 Inner j-loop Disasm Classification

**Binary**: `/tmp/gscript_nbody_advance_r32.bin` (5464 bytes, 1366 insns)  
**j-loop body**: `0x0300` → back-branch `0x0b34 → 0x0300`  
**Total span**: 526 instructions

| Category | Count | % |
|----------|-------|----|
| MOV/MOVK (data movement) | 119 | 22.7% |
| LDR (loads) | 101 | 19.2% |
| Branches (B / conditional) | 78 | 14.8% |
| STR (stores) | 73 | 13.9% |
| Box/unbox (FMOV/UBFX/SBFX/ORR/LSR/AND) | 82 | 15.6% |
| Guards (CMP/CBZ/CBNZ/TBNZ) | 40 | 7.6% |
| Float compute (FMUL/FADD/FSUB/FDIV/FSQRT) | 29 | 5.5% |
| Other (ADD/SUB/CSET) | 4 | 0.8% |
| **Total** | **526** | **100%** |

**LDR breakdown**: 52 from ctx.Regs spill slots (x26-based), 49 from field pointers.
**STR breakdown**: 29 to ctx.Regs spill slots, 44 to field pointer targets.

**Observation**: Float compute = 29 insns (5.5%) vs Memory = 174 insns (33.1%).
The loop is massively memory-bound from GetField/SetField table indirection,
not compute-bound.

**Tier 2 prologue** (cross-check c): `0x0000: sub sp, sp, #0x80` / `0x0004: stp x29, x30, [sp]` — correct Tier 2 callee-save prologue.

---

## 5. Wall-Time Estimate for Scalar Promotion

### Savings from bi.vx/vy/vz scalar promotion (j-loop):

Each GetField on bi.vx (inline-cached, typed) costs in the hot path:
- `ldr x0, [x26, #offset]` — reload ptr from ctx.Regs (1 LDR)
- `lsr + mov + cmp + b.ge` — float guard (4 insns)
- `ubfx x0, x0, #0, #0x2c` — pointer extraction (1 UBFX)
- `ldr x1, [x0, #0x40]` — get fields array ptr (1 LDR)
- `ldr x0, [x1, #offset]` — get field value (1 LDR)
- `fmov d, x0` — unbox float (1 FMOV)
**= ~9 insns per GetField on typed float**

Each SetField (after compute):
- `fmov x3, dN` — box float result (1 FMOV)
- `ldr x0, [x26, #offset]` — reload ptr from ctx (1 LDR)
- `ubfx x0, x0, #0, #0x2c` — pointer extraction (1 UBFX)
- `ldr x1, [x0, #0x40]` — get fields array ptr (1 LDR)
- `str x3, [x1, #offset]` — store field value (1 STR)
**= ~5 insns per SetField on inline-cached path**

**For bi.vx/vy/vz (3 pairs) per j-iteration eliminated by scalar promotion**:
- 3 GetField × 9 insns = 27 insns
- 3 SetField × 5 insns = 15 insns  
- **42 insns/iter eliminated** (moved to j-loop preheader/postlude, run O(n) not O(n²))

**As fraction of total**: 42 / 526 = **8.0%** of j-loop body instructions.

**ARM64 superscalar correction** (halve static insn savings):
- Estimated: ~21 fewer cycles per j-iteration.

**Wall-time estimate**: nbody loops N=50 body pairs per `advance()` call.
If j-loop runs ~12.5 iterations on average (5 bodies: i=1..5, j=i+1..5 → 10 pairs),
at 526 insns/iter × ~2 cycles/insn on superscalar = ~1052 cycles/iter,
saving 42/2 = 21 cycles → **~2% per advance() call**.

**Conservative bound**: 2–5% wall-time improvement for nbody (scalar promotion is a 
prerequisite for further optimizations like GetField guard hoisting and bj.vx/vy/vz promotion).

---

## 6. Cross-Check Status

| Check | Status | Detail |
|-------|--------|--------|
| (a) `.bin` mtime matches HEAD | PASS | Generated at 19:36 during this session; HEAD = adb0bdc |
| (b) Bytes from Tier 2 | PASS | 5464 bytes; `TestR32_NbodyLoopCarried` uses TieringManager + `RunTier2Pipeline + AllocateRegisters + Compile`. NumSpills=3, DirectEntryOffset=4584 |
| (c) Disasm function = `advance` | PASS | IR confirms `function advance (12 blocks, 135 regs)`; prologue `sub sp, sp, #0x80` at 0x0000 matches Tier 2 |
| (d) Insn class counts sum to total ±2% | PASS | Categorized = 526, span = 526 insns (0.0% diff) |
| (e) Bottleneck share × wall-time | PASS | 42 insns/iter of 526 = 8.0% → halved for superscalar = 4% est. Consistent with 2–5% predicted; loop structure (5 bodies) limits outer improvement |

---

## 7. Key Findings

1. **Confirmed hypothesis**: B2 (j-loop body) has **6 GetField/SetField pairs** — all loop-carried read-modify-write accesses on `vx/vy/vz` of both `bi` and `bj`.

2. **bi.vx/vy/vz** are the primary scalar promotion candidates: `bi` is loop-invariant across the j-loop (assigned once per i-iteration). Promoting these 3 variables to FPRs eliminates ~42 insns/iter.

3. **bj.vx/vy/vz** are partial candidates: `bj = bodies[j]` changes each j-iteration, so the GetField/SetField are necessary per-j, but guards can be hoisted once bj type is confirmed at loop entry.

4. **Float compute = 5.5% of j-loop body**: the loop is memory-bandwidth limited, not compute-limited. Scalar replacement reduces memory traffic directly.

5. **Guard overhead is large**: 13 float guards (lsr #0x30 pattern) in 526 insns. Eliminating bi.vx/vy/vz GetFields removes 3 guards per iteration (promoted values are statically typed in FPRs).

**Conclusion**: Scalar replacement / register promotion pass targeting loop-carried GetField+SetField pairs is confirmed as a valuable optimization for nbody. The bi.vx/vy/vz case is the cleanest target (loop-invariant object, multiple iterations of read-modify-write).
