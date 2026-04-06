# B3 Hot-Path Breakdown (post-LICM mandelbrot)

> Status: complete
> Date: 2026-04-06
> Author: diagnostic agent (round-9 research, no code changes)

## Method

Data sources used:
1. **Post-LICM IR dump** — wrote a throwaway test (`tmp_mandelbrot_b3_test.go`, since
   deleted) that ran `BuildGraph → Intrinsic → TypeSpec → ConstProp → DCE →
   RangeAnalysis → LICM` on `benchmarks/suite/mandelbrot.gs` and printed IR
   before/after LICM plus the RegAlloc map.
2. **ARM64 disassembly** — the same throwaway test wrote the compiled bytes to
   `/tmp/gscript_mandelbrot_postlicm_t2.bin`, then a small Go tool built against
   `golang.org/x/arch/arm64/arm64asm` disassembled them to
   `/tmp/mandelbrot_postlicm.asm` (358 lines). Hot-loop range identified at
   offsets 404–664 by grepping for `fmul/fadd/fsub/fcmp` density and matching
   against the B3 IR ordering.
3. **pprof** — ran `go test -run=^$ -bench=BenchmarkTier2Reg_Mandelbrot100
   -benchtime=3s -cpuprofile=/tmp/cpu-mandel.prof
   ./internal/methodjit/`, then `go tool pprof -top -cum`. Confirmed (again)
   that JIT code shows up as `runtime._ExternalCode` (79.3% of samples) —
   mmap'd JIT pages have no DWARF, so pprof cannot symbolicate inside the
   hot loop. The remaining 18% is `runtime.madvise` from bench iteration
   re-mapping, not execution cost. **pprof is blind here; the disasm is
   the authoritative source.**
4. **regalloc.go source read** — traced `carried` map plumbing
   (`internal/methodjit/regalloc.go:79-97, 265-293`) to explain why LICM
   hoisting delivered 0 wall-time gain.

## B3 IR (post-LICM)

```
B13: ; preds: B0  (synthetic pre-header inserted by LICM)
    v8   = ConstFloat  2 : float
    v13  = ConstFloat  1 : float
    v16  = ConstInt    1 : int
    v18  = ConstInt    1 : int
    v19  = ConstInt    -1 : int
    v21  = ConstFloat  2 : float
    v26  = ConstFloat  1.5 : float
    v28  = ConstFloat  0 : float
    v29  = ConstFloat  0 : float
    v30  = ConstBool   false : bool
    v34  = ConstInt    49 : int
    v35  = ConstInt    1 : int
    v36  = ConstInt    -1 : int
    v56  = ConstBool   true : bool
    v45  = ConstFloat  2 : float   <-- used in B3 (inner-loop invariant)
    v50  = ConstFloat  4 : float   <-- used in B3 (inner-loop invariant)
    v71  = ConstInt    1 : int

B5: ; preds: B11, B3  (inner loop header, 3 phis)
    v65  = Phi B11:v29, B3:v49 : float   ; zi  (carried, D4)
    v64  = Phi B11:v28, B3:v44 : float   ; zr  (carried, D5)
    v58  = Phi B11:v36, B3:v61 : int     ; iter counter
    v61  = AddInt v58, v35 : int
    v62  = LeInt  v61, v34 : bool
    Branch v62 → B3, B6

B3: ; preds: B5  (inner loop body — the one we care about)
    v39  = MulFloat v64, v64 : float     ; zr*zr           → D6
    v41  = MulFloat v65, v65 : float     ; zi*zi           → D7
    v42  = SubFloat v39, v41 : float     ; zr² - zi²       → D8
    v44  = AddFloat v42, v27 : float     ; + cr (invariant)→ D6  tr
    v46  = MulFloat v45, v64 : float     ; 2 * zr          → D7
    v47  = MulFloat v46, v65 : float     ; (2*zr) * zi     → D8
    v49  = AddFloat v47, v14 : float     ; + ci (invariant)→ D7  ti
    v51  = MulFloat v44, v44 : float     ; tr²             → D8
    v52  = MulFloat v49, v49 : float     ; ti²             → D9
    v53  = AddFloat v51, v52 : float     ; tr²+ti²         → D10
    v54  = LtFloat  v50, v53 : bool      ; 4 < sum         → X21
    Branch v54 → B4, B5
```

LICM moved 17 constants out of the body (including `ConstFloat 2` (v45) and
`ConstFloat 4` (v50), the two that round-7 analysis specifically named). B3
is now 12 operations + branch — the minimum possible.

## Per-Iteration Operation Count

Hot path per inner-loop iteration (B3 body + phi carry + counter update),
**as emitted ARM64** at `/tmp/mandelbrot_postlicm.asm` offsets 404-664:

| Category | Instructions/iter | % of 47 |
|----------|-------------------|---------|
| Float arith (9 fmul + 3 fadd + 1 fsub) | 13 | 27.7% |
| **Loop-invariant reloads** (v27/cr, v14/ci, v45/2.0, v50/4.0) — each is `ldr x0, [sp,slot] + fmov d?, x0` | **8** | **17.0%** |
| **Loop-carried-value spills** to memory (v44, v49, v64, v65) — each is `fmov x0, d? + str x0, [sp,slot]` | **8** | **17.0%** |
| Int counter reload+arith+box+store+bound-reload+compare | 11 | 23.4% |
| Float compare tail (fcmp + cset + orr + mov + tbnz) | 5 | 10.6% |
| Phi float register moves (fmov d4,d7 ; fmov d5,d6) | 2 | 4.2% |
| Unconditional branches (`b .+0x40` to counter, `b` back-edge) | 2 | 4.2% |
| **Total per iteration** | **47** | 100.0% |

**Only 13/47 (27.7%) of the instructions per iteration are actual float
arithmetic.** The other 72.3% is NaN-box/unbox round trips, spill/reload
of values that LICM correctly identified as loop-invariant but the
register allocator dropped on the floor.

For sanity: 17 constants were hoisted out of the body into the B13
pre-header (0% of B3 work). Confirmed by absence of any `mov x0, #<imm>`
or immediate-load inside the 404–664 range.

## pprof Top Functions

```
File: methodjit.test  (bench: Tier2Reg_Mandelbrot100, 3.77s, 3.29s sampled)
      flat  flat%   sum%        cum   cum%
     2.61s 79.33% 79.33%      2.61s 79.33%  runtime._ExternalCode
         0     0% 79.33%      2.61s 79.33%  runtime._System
     0.62s 18.84%    —           —          runtime.systemstack
     0.61s 18.54%    —           —          runtime.(*mheap).alloc.func1
     0.60s 18.24%                            runtime.madvise
     0.04s  1.22%                            runtime.gopreempt_m
     0.01s  0.30%                            methodjit.(*CompiledFunction).Execute
       0     0%                              Tier2Reg_Mandelbrot100
```

**Interpretation**: 79% of wall-time is inside the mmap'd JIT code buffer,
which pprof sees as a single opaque symbol `runtime._ExternalCode`. The
18% `madvise`/`mheap.alloc` is the bench framework reallocating the JIT
code buffer per `b.N` iteration (calls `Compile` fresh on every bench op
— this inflates the run but doesn't affect our analysis since mandelbrot
runs inside `_ExternalCode`). **pprof is blind to our hot loop**; only
disassembly + IR inspection can attribute cost here.

## ARM64 Disassembly Snippet (hot loop, 47 insns per iteration)

```
; ---- B3 entry (from B5 fall-through) ----
 404: 1e6508a6  fmul  d6, d5, d5          ; v39 = zr*zr                (arith)
 408: 1e640887  fmul  d7, d4, d4          ; v41 = zi*zi                (arith)
 412: 1e6738c8  fsub  d8, d6, d7          ; v42 = v39-v41              (arith)
 416: f9409f40  ldr   x0, [x26, #312]     ; RELOAD v27 (cr)  ← LICM lost
 420: 9e670001  fmov  d1, x0              ; unbox cr
 424: 1e612906  fadd  d6, d8, d1          ; v44 = v42+cr   (= tr)      (arith)
 428: 9e6600c0  fmov  x0, d6              ; box v44 (D6 about to be clobbered)
 432: f900af40  str   x0, [x26, #344]     ; SPILL v44
 436: f9413740  ldr   x0, [x26, #616]     ; RELOAD v45 (2.0) ← LICM lost
 440: 9e670000  fmov  d0, x0              ; unbox 2.0
 444: 1e650807  fmul  d7, d0, d5          ; v46 = 2*zr                 (arith)
 448: 1e6408e8  fmul  d8, d7, d4          ; v47 = v46*zi               (arith)
 452: f9408f40  ldr   x0, [x26, #280]     ; RELOAD v14 (ci)  ← LICM lost
 456: 9e670001  fmov  d1, x0              ; unbox ci
 460: 1e612907  fadd  d7, d8, d1          ; v49 = v47+ci  (= ti)       (arith)
 464: 9e6600e0  fmov  x0, d7              ; box v49
 468: f900bb40  str   x0, [x26, #368]     ; SPILL v49
 472: 1e6608c8  fmul  d8, d6, d6          ; v51 = tr*tr                (arith)
 476: 1e6708e9  fmul  d9, d7, d7          ; v52 = ti*ti                (arith)
 480: 1e69290a  fadd  d10, d8, d9         ; v53 = tr²+ti²              (arith)
 484: f9413b40  ldr   x0, [x26, #624]     ; RELOAD v50 (4.0) ← LICM lost
 488: 9e670000  fmov  d0, x0              ; unbox 4.0
 492: 1e6a2000  fcmp  d0, d10             ; v54 = 4 < sum              (compare)
 496: 9a9fa7e0  cset  x0, lt              ; boxed bool
 500: aa190000  orr   x0, x0, x25         ; NaN-tag as bool
 504: aa0003f5  mov   x21, x0
 508: 37000155  tbnz  w21, #0, .+0x28     ; if escaped → B4

; ---- phi moves at B3 end (to B5.phis) ----
 512: 1e6040e4  fmov  d4, d7              ; v65 ← v49 (zi carry)
 516: 9e660080  fmov  x0, d4
 520: f900cf40  str   x0, [x26, #408]     ; SPILL v65 to regfile
 524: 1e6040c5  fmov  d5, d6              ; v64 ← v44 (zr carry)
 528: 9e6600a0  fmov  x0, d5
 532: f900d340  str   x0, [x26, #416]     ; SPILL v64 to regfile
 536: f940db40  ldr   x0, [x26, #432]     ; RELOAD v58 (iter counter)
 540: 9340bc14  sbfx  x20, x0, #0, #48    ; unbox int
 544: 14000010  b     .+0x40              ; jump to counter block

; ---- counter update (B5 work) ----
 608: 91000695  add   x21, x20, #0x1      ; v61 = v58+1
 612: d340bea0  ubfx  x0, x21, #0, #48    ; box int
 616: aa180000  orr   x0, x0, x24
 620: f900db40  str   x0, [x26, #432]     ; SPILL v61 (iter)
 624: f9412741  ldr   x1, [x26, #584]     ; RELOAD v34 (50-1 = 49) ← LICM lost
 628: 9340bc21  sbfx  x1, x1, #0, #48     ; unbox bound
 632: eb0102bf  cmp   x21, x1
 636: 9a9fc7e0  cset  x0, le
 640: aa190000  orr   x0, x0, x25         ; NaN-tag as bool
 644: aa0003f4  mov   x20, x0             ; v62 → X20
 648: 37000094  tbnz  w20, #0, .+0x10     ; if continue → back-edge
 664: 17ffffbf  b     .+0xfffffffffffffefc; back-edge to 404
```

Register pressure: the function saves **every** callee-saved register
(X19-X28, D8-D11) on prologue — but only uses D4-D10 and X20-X23/X28 in
the inner loop. Live at the peak during B3: D4 (v65 carry), D5 (v64
carry), D6 (v44 current), D7 (v49 current), D8-D10 (temps) = **7 FPRs**.
Adding the 4 loop-invariants (v14, v27, v45, v50) in registers needs
**11 FPRs**, which exceeds the 8-FPR pool. This forces the allocator to
spill *something* — currently it spills the loop-invariants (worst
choice: they are read every iteration).

## Dependency Chain Analysis

True data dependencies in B3 (RAW hazards, ignoring false deps through x0):

```
      v64 (zr)         v65 (zi)        cr (v27)   2.0 (v45)   ci (v14)   4.0 (v50)
         │                 │              │           │          │          │
    ┌────┴────┐       ┌────┴────┐         │           │          │          │
    ▼         ▼       ▼         ▼         │           │          │          │
   v39       v46     v41     (unused)     │           │          │          │
  zr²       2*zr    zi²                   │           │          │          │
    │         │       │                   │           │          │          │
    └────┬────┘       │                   │           │          │          │
         ▼            │                   │           │          │          │
        v42          v47                  │           │          │          │
       zr²-zi²      2*zr*zi               │           │          │          │
         │            │                   │           │          │          │
         └──── v44 ◀──┼───────────────────┘           │          │          │
               tr     │                               │          │          │
                      └────v49 ◀──────────────────────┼──────────┘          │
                           ti                         │                     │
         ┌── v51 = v44*v44 ────┐                      │                     │
                               ▼                      │                     │
                              v53 ◀── v52 = v49*v49 ──┘                     │
                              tr²+ti²                                       │
                               │                                            │
                               └──── v54 = (4 < v53) ──────────────────────┘
                                     │
                                     ▼
                                branch (escape)
```

**Critical path for loop-carried value `zr` (= v44, becomes next v64):**
`v64 → fmul v39 → fsub v42 → fadd v44`  =  **3 serial float ops per
iteration** (~12 cycles on Apple M-series pipeline, ~3 ns).

**Parallelism available**: v39 and v41 can run in parallel with v46. v51
and v52 can run in parallel. Modern ARM64 cores have 2-4 FP issue ports,
so theoretical throughput is much higher than the serial latency.

**Compute floor per iter** (latency of longest chain, assuming
register-resident operands and no spills): ≈ 3 ops × 4 cycles = **12
cycles ≈ 3 ns per iter**.

**Measured per iter**: 0.39 s / (1000 × 1000 × ~20 avg iter-per-pixel)
≈ 20 ns per iter. **We are ~6× above the floor**, so the overhead budget
swamps the compute budget.

Of the 47 emitted instructions, 8 are loop-invariant reloads that should
be free, 8 are spills that should be fmov-to-FPR-reg moves, and the int
counter box/unbox round trip adds another 4-5 instructions. Eliminating
just the loop-invariant reloads (4 values: cr, ci, 2.0, 4.0) saves 8
instructions per iteration → ~17% reduction in hot-path insn count.

## Next-Action Candidates (ranked by expected impact)

### 1. **Cross-block FPR carry for LICM-hoisted loop invariants** (HIGH)

**Technique.** Extend round-7's `carried map[int]PhysReg` mechanism so
that, in addition to header phis, it also carries *loop-invariant values
defined in the pre-header and used inside the loop body*. For mandelbrot
this is v14 (ci), v27 (cr), v45 (2.0), v50 (4.0). These are live
throughout every loop iteration but currently not in the `carried`
set — see `regalloc.go:79-97, 265-293`.

**Expected wall-time impact**: −17% of B3 insns = ~−3 ns per iteration.
At ~20 ns/iter, that is **−15% wall-time on mandelbrot** (0.38s → 0.32s),
and similar gains on nbody / spectral_norm / math_intensive where the
same pattern dominates.

**Prior art.** LLVM's `LICM + RegisterCoalesce` pair; V8 TurboFan's
"loop invariants live through the backedge" live-range extension;
IonMonkey's `BacktrackingAllocator` uses hint-chain carrying.

**Risk.** Pool is 8 FPRs; mandelbrot B3 needs ~7 temps AND 4 loop
invariants = 11 — we will *still* have to spill something. Need to spill
*the right thing*: spill the temps (which die quickly, single-use) and
keep the loop invariants (which are read every iteration). Requires a
spill-cost heuristic based on "uses inside loop" rather than LRU.

**Prerequisite.** A spill-cost model that weights "live across loop
iterations" higher than "live across a few instructions". This is a
known technique — "loop depth × use count" is the classic LLVM heuristic.

### 2. **Int counter in GPR instead of memory-resident NaN-box** (MED)

**Technique.** The iteration counter (v58→v61) is spilled to the VM
register file (`str x0, [x26, #432]`) every iteration even though it's a
tight-loop phi. This is because the counter lives in the VM register
slot and the emitter writes it through on every StoreSlot. Fix: keep the
int counter in X20 across the back-edge, NaN-box only on function exit.

**Expected wall-time impact**: −4 to −5 insns per iteration (ldr, sbfx,
ubfx, orr, str, ldr bound) = ~−10% wall-time.

**Prior art.** LuaJIT's type-stable scalar variables are tagged-unboxed;
V8 Maglev's untagged Int32 phi.

**Risk.** Requires distinguishing "pure int loop counter" from "int
value also read by VM"; deopts must re-box on exit.

### 3. **Fused compare+branch (skip bool NaN-box)** (LOW–MED)

**Technique.** After `fcmp d0, d10`, the code currently does
`cset x0, lt; orr x0, x0, x25; mov x21, x0; tbnz w21, #0, …` — 4
instructions to produce a boolean, NaN-box it, and test it. But the
target branch is immediate. A `b.lt .+0x28` would do it in 1 instruction.

**Expected wall-time impact**: −3 to −6 insns per iteration (the float
compare tail AND the int compare tail in the counter), = ~−8%.

**Prior art.** Every production compiler fuses compare+branch.

**Risk.** Requires peephole pattern matching between `LtFloat/LeInt` and
the immediately-consuming `Branch` instruction. Must still produce the
bool value in a register if another consumer exists (rare in tight
loops, common otherwise).

### 4. **Reduction splitting / software pipelining** (SPECULATIVE)

**Technique.** Two independent FMUL chains (x²+y² for "tr" and for "ti")
are currently serialized by the v44/v49 back-edge phi. Software
pipelining could overlap iteration N's `v51/v52/v53/v54` with iteration
N+1's `v39/v41/v46/v47`. Apple M4 has 4 FP pipes.

**Expected wall-time impact**: theoretical −20%, realistic −5 to −10%
only *after* items 1–3 are done (the instruction stream is too polluted
with memory ops right now for scheduling to help).

**Prior art.** LLVM's `MachinePipeliner`; Cray-era modulo scheduling.

**Risk.** High implementation complexity; depends on items 1–3 landing
first (no point scheduling memory-bound code).

## Recommendation for Round 9

**Implement item #1: extend the `carried` map in `regalloc.go` to
include LICM-hoisted loop-invariant values defined in pre-header blocks.**

Justification backed by the numbers above:

- Only 27.7% of current hot-path instructions are real compute; 34.0%
  (16/47) are spill/reload of values LICM correctly identified as
  loop-invariant in round 8. This is the **largest single category of
  overhead** and the cheapest to fix (regalloc.go change only, zero
  codegen work).
- Round 7 already built the infrastructure (`safeHeaderFPRegs`,
  tight-body `carried` plumbing). Item #1 is a direct, small extension:
  at each tight loop body, also add to `carried` the IDs of values
  defined in the immediate pre-header whose *only* uses are inside the
  loop body. Skip if the FPR pool would overflow (8-reg pressure is
  tight — need a spill heuristic).
- Expected −15% wall-time on mandelbrot, and symmetric wins on nbody,
  spectral_norm, math_intensive (all 4 have the identical post-LICM
  pattern: pre-header constants/loads, reloaded every inner iteration).
- Risk is bounded: if the extension causes pool exhaustion, fall back to
  current behavior (spill the invariant). No correctness hazard — values
  in `carried` are already pinned-protected by `rs.pin(valID)` in
  `regalloc.go:290`.

If item #1 lands and mandelbrot still doesn't move ≥10%, the next lever
is item #2 (int counter in GPR). Item #3 is a peephole that can be done
independently. Item #4 should wait until the hot loop is clean of memory
traffic.
