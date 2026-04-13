# Tier 2 Float-Loop Profile Report

> Generated: 2026-04-05
> Cycle: 2026-04-05-tier2-float-profile (round 6, Phase 1 diagnostic)
> Initiative: opt/initiatives/tier2-float-loops.md
> Artifacts: opt/pprof-tier2-float-artifacts/ (5 pprof + 5 disassembly files)

## TL;DR

**Phase 2 target (one-line):** **Eliminate per-op box/unbox in Tier 2 float
loops — keep float SSA values resident in FPRs across the loop body; spill
to NaN-boxed memory slots only at loop exits or function return.**

**Escalation: SHALLOW.** Standard regalloc / emit-layer refactor; the
NaN-boxing calling convention is untouched. The fix is contained in
`regalloc.go` + the arith emitters.

**Secondary target (round 8):** Type-narrow `GetTable`/`GetField`/`Call`
results from `any` to `float` using feedback so `TypeSpecialize` can
promote generic `Mul`/`Add` to `MulFloat`/`AddFloat`. Affects 3 of 5
benchmarks; lifts matmul and spectral_norm inner loops out of tag-dispatch.

## Top-3 Cross-Benchmark Hot Patterns (Ranked)

| Rank | Pattern | Benchmarks hit | Per-op cost | Fix locus |
|------|---------|----------------|-------------|-----------|
| **1** | **Per-op NaN-box round-trip** — every float arith op does `ldr x0,slot; fmov d,x0; <fop>; fmov x0,d; str x0,slot` even when both operands came from an FPR the previous instruction. Values never live in FPRs across two consecutive ops. | 5/5 | ~6-10 insns (2× `ldr`, 3× `fmov`, 1× `str`) wrapping each 1-insn float op | `regalloc.go` (FPR persistence across blocks) + `emit_arith.go` (operand/result handoff) |
| **2** | **Generic `Mul`/`Add` dispatch on `any`-typed loads** — `TypeSpecialize` cannot promote `v23 = Mul v20, v22 : any` when `v20`/`v22` come from `GetTable`/`GetField`/`Call`. Each generic arith expands to a tag-check + dispatch + `scvtf` trampoline. | 3/5 (matmul, spectral_norm, nbody) | ~16-28 insns of tag test + branch + `sbfx` + `scvtf` per op | `pass_typespec.go` + `FeedbackVector` wiring for `GetTable`/`GetField`/`Call` result types |
| **3** | **Same-slot redundant load** — `v*v` (squaring), which needs both operands equal, emits two independent loads of the same slot address rather than CSE'ing to one load. Also seen for loop-carried constants (e.g. `cr` loaded per op). | 4/5 (mandelbrot, math_intensive, nbody, spectral_norm) | ~3-4 extra insns per duplicated load | `emit_arith.go` operand-materialization (CSE the operand-load step) OR a lightweight `pass_load_elim.go` |

Patterns 1 and 3 are facets of the same underlying problem: **the emit
layer materialises every operand from slot memory, every time, without
tracking that the SSA value is already live in a register**. Fixing
Pattern 1 structurally (FPR-resident span across a block) largely
subsumes Pattern 3.

---

## Per-Benchmark Findings

### spectral_norm (0.340s wall; pprof 290ms samples, 57% coverage)

**Hot function profiled:** `multiplyAv(n, v, av)` — nested loop,
`sum = sum + A(i,j) * v[j]`.

**pprof top (cum %):**
- 62% `runtime._ExternalCode` (time spent inside mmap'd JIT code — **good: JIT is running**)
- 38% `BaselineJITEngine.handleBaselineOpExit` / `handleCall` — Tier 1 exit-resume chain
- 7% `handleGetTable` + `Table.RawGet` (the `v[j]` load per inner iter)

**IR inner loop (B2 in `multiplyAv`, 4 insns of flopping hidden behind exit-resume):**
```
v17 = GetGlobal globals[1] : any     # the global `A`
v20 = Call      v17, v44, v30 : any  # A(i, j) — EXIT to Go
v22 = GetTable  v1, v30 : any        # v[j]
v23 = Mul       v20, v22 : any       # generic Mul
v25 = Add       v35, v23 : any       # generic Add
```

**ARM64 signature in `.asm` (byte offsets 896-1328, ~54 insns per inner iter):**
```
; GetTable v[j] native path:
 920: ldr  x0, [x2,x1,lsl #3]         ; load v[j] from array-backed table
 948: ldr  x0, [x2,x1,lsl #3]         ; (dict-backed fallback)
; Exit-resume for the A(i,j) call:
 984: str  x20, [x26,#248]            ; save caller regs
 992: mov  x0, #1 ; str [x19,#88]     ; exit code = 3 (call-exit)
1040: b    .+0x2d4                    ; jump out of JIT
; Resume entry:
1044: ldr  x20, [x26,#248]
; Generic Mul with tag dispatch:
1068: lsr  x2, x0, #48 ; cmp x2,#0xfffe ; b.ne .+0x2c  ; int-tag test
1100: sbfx x0,x0,#0,#48 ; sbfx x1,x1,#0,#48 ; mul x0,x0,x1 ; ubfx+orr (int×int)
1124: fmov d0,x0                      ; (fallback-to-float)
1128: ...tag tests + scvtf...
1144: scvtf d1, x1
1176: fmul  d0, d0, d1
1180: fmov  x0, d0
; Generic Add repeats the same ~15-insn dispatch...
```

**Per-iter side-exit count:** 1 CALL-exit (to `A(i,j)`). Every inner iter
leaves Tier 2 and returns. For N=500, that is 500×500 = 250 000 exit-resume
round-trips.

**Top-3 hot IR ops:** `Call A` (#1 per-iter cost), `Mul v20,v22` (generic
dispatch), `Add v35,v23` (generic dispatch).

### nbody (0.618s wall; 570ms samples, 70.5% coverage)

**Hot function profiled:** `advance(dt)` — double nested loop with
table-field access and `math.sqrt`.

**pprof top (cum %):**
- 40% `BaselineJITEngine.handleCall` → `VM.callValue` (exit-resume for
  `math.sqrt` + `bodies[i]` table reads)
- 38% `TieringManager.executeTier2` (Tier 2 IS executing — second-largest)
- 30% `runtime._ExternalCode` (JIT code)
- 16% `TieringManager.executeGlobalExit` — `bodies` GetGlobal fallthrough
- 10% `mapaccess2_faststr` (GetGlobal hit-path in runtime.Interpreter)

**IR inner loop (B2 in `advance`):** 22 `GetField`s, 24 generic `Mul`/`Add`/`Sub`/`Div`, plus `v36 = Call math.sqrt(v33)`.

**Per-iter side-exit count:** 1 math.sqrt CALL-exit + ~2 `GetGlobal bodies`
cache-miss exits per iter (N=500 000 iters → ~1.5M exit-resume events).

**ARM64 signature:** 12 584 bytes (3146 insns) of Tier 2 code for this
one function — largest of the 5. B2 alone is ~1500 insns because every
`GetField` expands to a shape-guard + slot-load sequence, and every Mul/Add
is the generic dispatch trampoline.

**Top-3 hot IR ops:** `GetGlobal bodies` (#1 per-iter cost, fired from
every `bodies[i]` access — 2 per iter), `Call math.sqrt` (#2), generic
`Mul` chain (#3).

### matmul (0.818s wall; 750ms samples, 74% coverage)

**Hot function profiled:** `matmul(a, b, n)` — triple-nested loop,
`sum = sum + ai[k] * b[k][j]`.

**pprof top (cum %):**
- 56% `runtime.madvise` / `sysUsedOS` — memory-allocation storm (GC pressure
  from `row := {}` inside outer loop, 300 allocations)
- 32% **Tier 1** (`BaselineJITEngine.Execute` + `handleBaselineOpExit` +
  `handleCall`). **Tier 2 barely appears in the profile.**
- 19% `handleGetTable` + `Table.RawGet` (Tier 1 GETTABLE exit-resume)

**Anomaly:** Despite the harness confirming `matmul` Tier 2-compiles cleanly
(3332 bytes of ARM64, DirectEntryOffset=2708), the run-time profile shows
the work happening in Tier 1. Hypotheses:
1. `matmul` is called once with N=300 → CallCount threshold (configured
   at 1-3 per smart tiering) likely crosses, but the outer call alone runs
   ~27M inner-loop iters. OSR-triggered Tier 2 may be firing late or
   being disabled after a deopt.
2. `row[j] = sum` SetTable + NewTable path may be deopting Tier 2
   back to Tier 1.

**Worth re-profiling** in Phase 2: add `jit-stats` + instrument promoter
to emit when matmul is promoted / deopted. This is a secondary finding
(Tier 2 tier-up for matmul), not Phase 2's primary target.

**IR inner loop (B3 in `matmul`):**
```
v31 = GetTable v13, v43 : any   # ai[k]
v35 = GetTable v33, v58 : any   # b[k][j]
v36 = Mul      v31, v35 : any   # generic Mul (same dispatch pattern as spectral_norm)
v38 = Add      v49, v36 : any   # generic Add
```

**ARM64 signature (offsets 1692-1900 for the generic Mul+Add pair):**
```
1692: fmov d0, x0
1696: lsr  x2, x1, #48 ; cmp x2,#0xfffe ; b.ne .+0x20  ; tag test
1712: sbfx x1, x1, #0, #48 ; scvtf d1, x1              ; int→float coercion
1744: fmul d0, d0, d1
...
1772-1816: tag test + sbfx + sbfx + add + ubfx + orr   ; int-path Add
1820-1868: fmov + tag test + scvtf + fadd              ; float-path Add
```

**Top-3 hot IR ops:** `GetTable ai[k]` (#1 cost), `GetTable b[k][j]` (#2),
`Mul`/`Add` generic-dispatch pair (#3).

### mandelbrot (0.373s wall; 330ms samples, 65% coverage)

**Hot function profiled:** `mandelbrot(size)` — triple-nested loop with
pure float arithmetic in the `iter` body; early-break on `|z|^2 > 4`.

**pprof top (cum %):**
- **100% `runtime._ExternalCode`** — essentially all time in emitted ARM64.
  No Go hot paths at all. Pure Tier 2 with no exit-resume per iteration.

**Interpretation:** mandelbrot is the **cleanest Tier 2 benchmark** — it
has no table loads, no calls, no globals in its hot path. So the entire
wall-time is the overhead of the emitted code itself. This is where
Pattern 1 (box/unbox) is most visible and most pure.

**IR inner loop (B3 in `mandelbrot`, all FP):**
```
v39 = MulFloat  v64, v64     # zr*zr  (type-specialized — good)
v41 = MulFloat  v65, v65     # zi*zi
v42 = SubFloat  v39, v41
v44 = AddFloat  v42, v27     # + cr
v46 = MulFloat  v45, v64     # 2*zr
v47 = MulFloat  v46, v65     # *zi
v49 = AddFloat  v47, v14     # + ci
v51 = MulFloat  v44, v44     # new_zr^2
v52 = MulFloat  v49, v49     # new_zi^2
v53 = AddFloat  v51, v52
v54 = LtFloat   v50, v53
```

**ARM64 signature (B3 body, offsets 600-736 — 136 bytes / 34 insns for
10 float ops + 1 compare + 1 branch):**
```
 600: ldr  x0, [x26,#544]     ; load v64 (zr) from slot
 604: fmov d0, x0              ; unbox
 608: ldr  x0, [x26,#544]     ; load v64 AGAIN (same slot)      ← Pattern 3
 612: fmov d1, x0              ; unbox again
 616: fmul d4, d0, d1         ; v39 = zr*zr
 620: ldr  x0, [x26,#536]     ; load v65 (zi)                   ← re-load, not in FPR
 624: fmov d0, x0
 628: ldr  x0, [x26,#536]     ; zi AGAIN                         ← Pattern 3
 632: fmov d1, x0
 636: fmul d5, d0, d1         ; v41 = zi*zi
 640: fsub d6, d4, d5         ; v42 = v39 - v41
 644: ldr  x0, [x26,#368]     ; load v27 (cr)                   ← re-load
 648: fmov d1, x0
 652: fadd d4, d6, d1         ; v44 = v42 + cr
 656: fmov x0, d4             ; BOX v44                           ← Pattern 1 (box)
 660: str  x0, [x26,#448]     ; spill v44 to slot
 664: mov  x0, #0x4000000000000000 ; ConstFloat 2.0
 668: fmov d5, x0              ; v45 = 2.0
 672: ldr  x0, [x26,#544]     ; load v64 (zr) — THIRD TIME THIS ITER
 676: fmov d1, x0
 680: fmul d6, d5, d1         ; v46 = 2 * zr
 684: ldr  x0, [x26,#536]     ; load v65 (zi) — THIRD TIME THIS ITER
 688: fmov d1, x0
 692: fmul d5, d6, d1         ; v47 = v46 * zi
 696: ldr  x0, [x26,#296]     ; load v14 (ci)
 700: fmov d1, x0
 704: fadd d6, d5, d1         ; v49 = v47 + ci
 708: fmov x0, d6             ; BOX v49
 712: str  x0, [x26,#480]     ; spill v49
 720: fmov d5, x0              ; (ConstFloat 4.0 material)
 724: fmul d7, d4, d4         ; v51 = v44 * v44 (already in d4!)
 728: fmul d8, d6, d6         ; v52 = v49 * v49 (already in d6!)
 732: fadd d9, d7, d8         ; v53 = v51 + v52
 736: fcmp d5, d9              ; v50 < v53
```

**Observations inside one iter:**
- `zr` (v64) loaded 3 times from `[x26,#544]`
- `zi` (v65) loaded 3 times from `[x26,#536]`
- `cr`, `ci` reloaded each time (they're loop-invariant, trivial LICM targets)
- `v44` boxed+stored then IS used in-register for the next `fmul d7,d4,d4`,
  so the emitter sometimes DOES keep a value in an FPR — but only for the
  adjacent next instruction, never across a slot-backed phi.
- Total: ~34 insns for what should be 10 float insns + 4 operand materialisations.

**Per-iter side-exit count:** 0. Pure Tier 2 from start to finish.

**Top-3 hot IR ops (per cost per iter, ranked):** `MulFloat` ×4 (boxed-loads
dominate), `AddFloat` ×3, `LtFloat`/`Phi` (back-edge spill).

### math_intensive / distance_sum (0.065s of 0.193s wall; 190ms samples, 63% coverage)

**Hot function profiled:** `distance_sum(n)` — `total + sqrt(x² + y² + z²)`.

**pprof top (cum %):**
- 63% `runtime._ExternalCode`
- 32% `runtime.madvise` / `sysUsedOS` (GC alloc — likely from math.sqrt boxing its `runtime.Value` return each call)

**IR inner loop (B1):**
```
v10 = Div      v8,  v0      : float   # 1.0*i/n  (promoted)
v12 = SubFloat v11, v10     : float
v13 = MulFloat v10, v12     : float
v14 = GetGlobal globals[2]  : any     # math
v15 = GetField  v14.field[3] : any    # math.sqrt
v16 = MulFloat v10, v10
v17 = MulFloat v12, v12
v18 = AddFloat v16, v17
v19 = MulFloat v13, v13
v20 = AddFloat v18, v19
v21 = Call     v15, v20     : any     # CALL-exit to Go for sqrt
v23 = Add      v32, v21     : any     # generic Add (v21 is any)
```

**Per-iter side-exit count:** 1 math.sqrt CALL-exit + 1 GetField (cached
after first iter). N=1 000 000 iters.

**Top-3 hot IR ops:** `Call math.sqrt` (#1), `Add v32,v21` generic (#2),
`MulFloat` chain (#3 — affected by Pattern 1).

---

## Cross-Cutting Findings

### 1. `runtime._ExternalCode` = quality-assessor

The 3 benchmarks where `_ExternalCode` dominates (spectral_norm 62%,
mandelbrot 100%, math_intensive 63%) are the ones where Tier 2 is
**actually running** but the emitted code is inefficient. nbody (30%)
and matmul (9%) spend much of their time in Go handlers
(exit-resume for globals / sqrt / table ops).

Concretely: mandelbrot at 100% `_ExternalCode` with wall-time 6.5× LuaJIT
is the canonical "the JIT produces slow code" benchmark. Fixing
Pattern 1 will move mandelbrot the most, measurable in isolation.

### 2. Exit-resume thrash dominates call-heavy benchmarks

nbody's `advance` does `bodies[i]` + `bodies[j]` inside the inner loop,
each via `GetGlobal bodies` → `GetTable`. The generation-cached
`GlobalCache` at PC-level should hit, yet pprof shows 16%
`executeGlobalExit` and 10% `mapaccess2_faststr` (map lookup). This
suggests the `globals[0] = "bodies"` GetGlobal is cache-missing (or
regenerating) more often than expected. **Diagnostic gap** — add a
counter for GlobalCache hit-rate in a future round.

### 3. Tier 2 may not be active for matmul

Only 32% of matmul's CPU time is in Tier 1 handlers; `executeTier2` is
absent from the top. Hypothesis: Tier 2 for `matmul` compiles, runs,
hits a deopt (probably from SetTable → NewTable exit-resume cascading
to a GuardType fail on the inner `sum := 0.0`-then-int-comparison
structure), then is disabled. **Needs instrumentation in Phase 2 before
any fix is attempted.**

### 4. Loop-invariant re-load is pervasive (LICM gap)

In mandelbrot B3: `cr` (v27), `ci` (v14), `ConstFloat 2.0` are reloaded
from memory each of the 50 iter-loop iterations. These are loop
invariants with trivial LICM eligibility (no aliasing, pure consts).
A LICM pass that just hoists `LoadSlot` for values never written in
the loop body would eliminate ~4 insns × 50 iters × (size×size) cells
for mandelbrot alone.

### 5. TypeSpecialize stops at heap boundary

`GetTable`, `GetField`, and `Call` all return `any`. `TypeSpecialize`
inspects the producer's static type; since producers return `any`, it
can't promote consumers. A feedback-driven narrowing (watch the type
observed at these sites during the interpreter warmup, emit `GuardType`
guards at the Tier 2 boundary) would unlock matmul's inner loop
completely.

---

## Phase 2 / Round 7 Plan

### Primary target (round 7): Pattern 1 — FPR-resident SSA across blocks

**Scope:** Modify the regalloc + emit handshake so that when a float SSA
value is produced by an arith op, its result FPR stays live until
the SSA value's last use in the block. Operand materialisation reuses
the producer's FPR instead of re-loading the slot.

**Files likely touched:**
- `internal/methodjit/regalloc.go` — add FPR allocation-domain for SSA
  values whose all uses are within one block (or a single loop body)
- `internal/methodjit/emit_arith.go` — teach operand/result handlers to
  prefer `RegAllocation.ValueRegs[id]` over `LoadSlot`-backed materialisation
- Possibly `internal/methodjit/emit_compile.go`'s `computeHeaderExitRegs` /
  `computeSafeHeaderRegs` — ensure loop back-edges persist FPRs

**Tests:** `internal/methodjit/tier2_float_profile_test.go` already dumps
IR + ARM64 for all 5 benchmarks. Before/after counts of
`fmov d?, x0` + `ldr x0, [x26,...]` pairs in mandelbrot's B3 is the
immediate quality signal.

**Expected impact (conservative):**
- mandelbrot: 0.37s → 0.20-0.25s (2× fewer FMOV per iter, 3× fewer LDR)
- spectral_norm: 0.34s → 0.25s (Pattern 1 partial, still blocked by
  Pattern 2 on the Mul/Add dispatch)
- math_intensive/distance_sum: ~10% improvement (call-exit dominates)
- nbody: minimal (exit-resume dominates)
- matmul: minimal (Tier 2 not active)

### Secondary target (round 8): Pattern 2 — feedback-typed loads

**Scope:** Wire `FeedbackVector` entries for GETTABLE/GETFIELD/CALL to
record observed result type. `TypeSpecialize` consults feedback; if the
site consistently returns `float`, it inserts a
`GuardType result is float : float` immediately after the `GetTable`
so downstream `Mul`/`Add` sees float-typed operands and promotes to
`MulFloat`/`AddFloat`.

**Expected impact:**
- matmul: 0.82s → 0.40s (inner loop leaves tag-dispatch)
- spectral_norm: 0.34s → 0.18s (combined with round 7)
- nbody: 0.61s → 0.45s (GetField results get float-typed)

### Deferred (round 9+)

- **LICM pass** (`pass_licm.go`) — mandelbrot's `cr`/`ci` hoisting. Worth
  ~10-20% on mandelbrot once Pattern 1 is fixed.
- **Global-cache invalidation audit** — nbody shows more GlobalCache miss
  than expected.
- **OSR tier-up + deopt-disable behaviour for matmul** — needs
  instrumentation.

---

## Fix Map (if-then)

| If profile shows... | Then touch... |
|---------------------|---------------|
| Per-op FMOV dominates (✔ confirmed) | `regalloc.go` + `emit_arith.go` — FPR persistence |
| Redundant same-slot LDR (✔ confirmed) | Same as above; also `operandReg()` helper in emit |
| Generic Mul/Add dispatch (✔ confirmed) | `pass_typespec.go` + feedback for `GetTable`/`GetField`/`Call` |
| CALL-exit per iter (✔ confirmed) | Investigate `emitCallNative` fallthrough conditions — why doesn't `A(i,j)` / `math.sqrt` use native BLR? |
| `GlobalCache` miss storm (suspected nbody) | Audit `executeGlobalExit` cache-fill + generation-bump logic |
| Tier 2 never activating for matmul (suspected) | `TieringManager.compileTier2` + OSR path; add `-jit-stats` hook |

---

## Artifacts

Committed under `opt/pprof-tier2-float-artifacts/`:

| File | Contents |
|------|----------|
| `spectral_norm_pprof.txt` | pprof top-30 (cum) |
| `nbody_pprof.txt` | pprof top-30 (cum) |
| `matmul_pprof.txt` | pprof top-30 (cum) |
| `mandelbrot_pprof.txt` | pprof top-30 (cum) |
| `math_intensive_pprof.txt` | pprof top-30 (cum) |
| `spectral_norm_t2.asm` | 543 lines, 2172 B of ARM64 |
| `nbody_t2.asm` | 3146 lines, 12584 B of ARM64 |
| `matmul_t2.asm` | 833 lines, 3332 B of ARM64 |
| `mandelbrot_t2.asm` | 331 lines, 1324 B of ARM64 |
| `math_intensive_t2.asm` | 350 lines, 1400 B of ARM64 |

The reusable dump harness is at
`internal/methodjit/tier2_float_profile_test.go`. Future rounds extend it
by adding a test for a new (benchmark, function) pair.

---

## Methodology Notes

**Profile-collection discipline:** `runtime._ExternalCode` reflects cycles
in the mmap'd JIT region — useful as a *quantity* signal ("is JIT
running?"), but pprof cannot symbolicate *which* instruction inside
the JIT region consumed those cycles. For the disassembly layer we
cross-referenced the `Print(fn)` IR dump with the hot-block signature
(characteristic FP-op density) in the `.asm` output. Mandelbrot's B3
block was identifiable by grep for `fmul d*, d*, d*` + proximate
`fcmp d5, d9` → `tbnz w20, #0` escape branch.

**Tool chain:** Disassembly uses a custom
`golang.org/x/arch/arm64/arm64asm`-based tool (`/tmp/gs_disasm_tool`,
one-off build, not in-tree) because `llvm-objdump`'s macOS binary
lacks `-b binary` support. If round 7 wants this tooling durably, it
should land as a `cmd/gscript-disasm/` CLI.

**Signal strength check (for Risks Signal 1):** Not flat — mandelbrot's
single hot pattern (Pattern 1) accounts for visibly >30% of the iter
loop's instruction count. Pattern 2 accounts for >50% of generic
Mul/Add in matmul's hot block. This is the *opposite* of a flat profile.
Shallow escalation is correct.
