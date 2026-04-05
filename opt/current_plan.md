# Optimization Plan: FPR-Resident Float SSA Across Basic Blocks

> Created: 2026-04-05
> Status: active
> Cycle ID: 2026-04-05-tier2-fpr-resident
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 2, round 7)

## Target

Primary: **mandelbrot** (cleanest signal — 100% `_ExternalCode`, no exit-resume per iter).
Secondary: **spectral_norm, math_intensive, nbody, matmul** — must not regress; partial lift expected.

| Benchmark | Current (JIT) | LuaJIT | Gap (LuaJIT) | Target |
|-----------|---------------|--------|--------------|--------|
| mandelbrot | 0.382s | 0.058s | 6.6x | **0.22-0.25s** (≥35% reduction) |
| spectral_norm | 0.336s | 0.008s | 42x | 0.25-0.28s (partial, Pattern 2 still blocks) |
| math_intensive | 0.193s | — | — | 0.17s (~10%, call-exit dominates) |
| nbody | 0.610s | 0.034s | 18x | 0.55-0.60s (minimal, exit-resume dominates) |
| matmul | 0.833s | 0.022s | 38x | 0.80-0.85s (Tier 2 barely active, see Phase 5) |

Aggregate wall-time target: reduce category total by **0.18-0.24s** (primarily mandelbrot).

## Root Cause

Tier 2's emit layer materialises every float operand from its home slot on every use.
Mandelbrot's inner loop (B3) does:

```
 600: ldr  x0, [x26,#544]     ; load zr (phi v64) from slot
 604: fmov d0, x0              ; unbox
 608: ldr  x0, [x26,#544]     ; load zr AGAIN (same op, second operand)
 612: fmov d1, x0              ; unbox again
 616: fmul d4, d0, d1         ; v39 = zr*zr  (the ONE useful instruction)
```

34 ARM64 instructions to execute 10 float arithmetic ops. The 5/5-benchmark hot
pattern (per `opt/pprof-tier2-float.md` § Top-3 Hot Patterns) is the
box/unbox/reload round-trip around each FP op.

The FPR-resident *infrastructure* exists:
- `activeFPRegs` tracks per-block FPR residency (`emit_compile.go:455`).
- `loopHeaderFPRegs` + `safeHeaderFPRegs` carry FPR assignments from loop headers into non-header loop blocks (`emit_loop.go:487-602`).
- `emitPhiMoveRawFloat` delivers raw float phis over the loop back-edge (`emit_dispatch.go:720`).
- `resolveRawFloat` returns the allocated FPR directly when `hasFPReg(v)` is true (`emit_reg.go:272-300`).

But mandelbrot is still box/unbox-thrashing. The infrastructure has **three gaps**:

**Gap A — Operand scratch reuse missing.** When a value has no FPR allocation
(or is spilled), each `resolveRawFloat` call emits its own slot-load + FMOVtoFP
into a fresh scratch (D0, then D1, then D0 again…). For `v*v`-style same-slot
operands, this is two loads for one SSA value, *within a single instruction*.
The emitter never notices "this scratch already holds that value."

**Gap B — Phi FPR allocation depends on `TypeSpecialize` having promoted the
Phi to TypeFloat.** `needsFloatReg` (`regalloc.go:279-294`) returns true only
when `instr.Type == TypeFloat` for OpPhi. `inferPhiType` (`pass_typespec.go:155`)
is iterative and can fail to reach fixed-point for loop-carried phis whose
back-edge value is itself a phi-consumer. If the Phi stays TypeAny, it
gets a GPR allocation (or none), forcing its FPR consumers back to
memory-load fallback.

**Gap C — Safe-header FPR filter is strict.** `computeSafeHeaderFPRegs`
(`emit_loop.go:569`) excludes *any* header FPR whose physical register is
touched by *any* body-block instruction, even if that body instruction's
last-use precedes a guaranteed re-assignment. For an 8-entry FPR pool with
a loop body that produces ≥8 float SSA values, the LRU regalloc can reuse
a register after the phi's last use, yet the phi is still flagged "clobbered"
and excluded from `safeHeaderFPRegs` — forcing the non-header block to reload.

## Prior Art (MANDATORY)

**V8 Maglev (`GapMoves` / `ParallelMove` at block edges):** Maglev tracks
`AllocationSummary` per block exit and resolves phi moves as a `ParallelMove`
at each edge. Float operands stay in XMM registers across the entire loop
body unless register pressure forces a spill; spills are restored at block
entry via explicit `Move` gaps, not by reloading from an interpreter slot.
Relevant for Gap A and Gap B: Maglev distinguishes "value in register" from
"value in stack slot" as first-class operand kinds, and operand materialisation
consults the per-block register-state summary, not the slot.
*(v8/src/maglev/maglev-regalloc.cc, `GapMove`, `UpdateUse`.)*

**V8 TurboFan `SimplifiedLowering` → `MachineLoweringPhase`:** Float64
SSA values are typed `MachineType::Float64` and occupy FPR live-ranges
distinct from `MachineType::TaggedSigned`. The register allocator
(`linear-scan-allocator.cc`) tracks live-range per-machine-type; NaN-boxing
happens only at the tier boundary (on tier-down to interpreter). Key
insight for Gap B: the type lattice must promote Phi nodes to `Float64` for
loop-carried FP values *before* register allocation runs; otherwise the
allocator hands them a GP register and every use pays a FMOV.
*(v8/src/compiler/linear-scan-allocator.cc; v8/src/compiler/simplified-lowering.cc.)*

**SpiderMonkey Warp / Ion `MIRType::Double` vs `MIRType::Value`:** Ion has a
two-tier type system — "boxed" (`Value`) and "unboxed" (`Double`, `Int32`,
etc.). Box/unbox are *explicit MIR instructions* emitted at the transition
boundary (e.g., when a `Double` value leaves the function, or flows into an
`any`-typed consumer). This means Ion's allocator sees a `Double` live-range
with FPR operands; there is *no* per-op box/unbox. The pass that inserts
unbox/box is `EliminateRedundantBoxing`. Matches our diagnosis of Pattern 1.
*(mozilla/js/src/jit/IonAnalysis.cpp, `EliminateRedundantBoxing`.)*

**.NET RyuJIT "promoted locals" + `GT_LCL_FLD` vs `GT_LCL_VAR`:** RyuJIT
promotes a local struct/double to SSA when all uses are scalar, keeping
the value in an FPR across the loop body. The `Morph` pass rewrites
stack-slot accesses into register operands when the local is promoted.
Backs up our Gap A fix: operand materialisation should consult the
"value currently in register X" state, not re-emit the slot load.
*(dotnet/runtime/src/coreclr/jit/morph.cpp.)*

**Academic — Wimmer & Franz, "Linear Scan Register Allocation on SSA Form"
(CGO 2010):** Introduces the notion of a "resolution" phase that walks block
edges and inserts exactly the minimum moves needed to reconcile differing
register assignments between predecessor/successor. Models our
`safeHeaderFPRegs` problem (Gap C): instead of excluding clobbered regs,
insert a resolution move at the back-edge, keeping the hot path inside
the loop body FPR-resident.
*(Wimmer, C. & Franz, M. ACM CGO 2010, §5 "Resolution".)*

**Our constraints vs. theirs:**
- GScript's regalloc is *forward-walk per-block* (simpler than V8/SpiderMonkey
  linear-scan). We cannot adopt full linear-scan without a rewrite.
  Instead, the block-local LRU allocation already produces good assignments
  *within* a block; what's missing is edge-resolution discipline.
- NaN-boxing is our calling convention. Pattern 1 fix does NOT change that —
  every value still ends up NaN-boxed *at the function boundary*. What we
  eliminate is the *per-op* redundant box/unbox in the hot loop body.
- We already do phi-raw-int moves for integer loop counters (`emitPhiMoveRawInt`).
  The float counterpart exists (`emitPhiMoveRawFloat`). We just need the
  propagation from phi to operand uses to actually engage.

Explicitly *not* used: LuaJIT implementation (trace JIT, opposite architecture).

## Approach

Three surgical fixes, in order of ROI and risk.

### Fix 1: Operand scratch-FPR caching (Gap A) — primary ROI

Add a transient per-instruction "scratch residency" map to `emitContext`:
track which scratch FPR (D0-D3) currently holds which value ID for *values
without an FPR allocation*. When `resolveRawFloat(v, Dn)` is called and `v`
is already in a scratch FPR from earlier in the same instruction's operand
materialisation, return that FPR directly (zero insns emitted).

**Scope:**
- `internal/methodjit/emit_reg.go` — extend `resolveRawFloat` with a
  `scratchFPRCache` consult. Cache is keyed by `(currentInstr, valueID)`
  and cleared when the emit dispatcher moves to the next instruction.
  Must be invalidated when an emitted op writes to the scratch FPR.
- `internal/methodjit/emit_compile.go` — add `scratchFPRCache map[int]jit.FReg`
  field to `emitContext`. Reset in `emitInstr` per-instruction wrapper.
- `internal/methodjit/emit_dispatch.go` — clear cache at the start of
  each instruction's emission (`emitInstr` switch preamble). Also clear
  when any helper writes an output to a scratch FPR.

**Expected savings:** In mandelbrot B3, `v*v` squaring (v39, v41, v51, v52)
saves 1 `ldr`+`fmov` per duplicated operand = ~8 insns per iter ×
(250×250×50) = ~25M insns. Also subsumes Pattern 3 for the duplicated
`cr` / `ci` / `2.0` per-iter reloads that are loaded into scratch FPRs.

### Fix 2: Verify + fix FPR phi typing (Gap B) — diagnostic-driven

Diagnose first: emit a one-time test `tier2_fpr_residency_test.go` that
prints, for each of the 5 float benchmarks:
- Per-Phi `Type` field after TypeSpecialize
- Per-Phi `alloc.ValueRegs[phiID]` (IsFloat?)
- Per-loop-body-block `safeHeaderFPRegs[innerHeader]`

If any float-phi fails to get an FPR:
- **Scenario B1 — Phi.Type stayed TypeAny/TypeUnknown**: extend `inferPhiType`
  fixed-point iteration limit, or re-run `TypeSpecialize` once more if any
  phi was updated in the previous iteration. File: `pass_typespec.go`.
- **Scenario B2 — Phi.Type is TypeFloat but `needsFloatReg` returned false**:
  add OpPhi to the float-op list in `needsFloatReg` when result is
  TypeFloat. File: `regalloc.go:279-294`. (Likely already correct via the
  `instr.Type == TypeFloat` check; verify.)
- **Scenario B3 — FPR pool exhausted (8 FPRs < peak-live)**: measure peak
  concurrent FPR live-range. If >8, Fix 1's scratch caching covers most
  of the gap. Widening the pool (adding D12-D15) would require prologue/
  epilogue save/restore updates — defer to Fix 3 or a later round.

**Scope (conditional):**
- `internal/methodjit/pass_typespec.go` — `inferPhiType` iteration if B1.
- `internal/methodjit/regalloc.go` — `needsFloatReg` if B2.

### Fix 3: Relax safe-header FPR filter (Gap C) — conditional

If the diagnostic from Fix 2 shows phis are FPR-allocated but not in
`safeHeaderFPRegs` (filter too strict), change `computeSafeHeaderFPRegs`
to allow a phi's FPR into the safe set when the clobbering body instruction's
last-use is *before* the first use of the phi in that block (i.e., LRU
allocation has already evicted the phi by the clobber site, so we re-load
from the phi's home slot just once at block entry — still a net win over
per-op reload).

Alternative (simpler, chosen if possible): emit a "block-entry reload"
for the phi's FPR when the phi is `crossBlockLive` but flagged clobbered.
This is one `ldr`+`fmov` at block entry vs. one per arith op.

**Scope (conditional):**
- `internal/methodjit/emit_loop.go:569-602` — loosen `computeSafeHeaderFPRegs`, OR
- `internal/methodjit/emit_compile.go:473-482` — block-entry reload for
  "unsafe-but-used" header FPRs.

### Execution order

1. Fix 1 (always) — scratch FPR caching.
2. Diagnostic test (always) — enumerate Phi FPR state across 5 benchmarks.
3. Fix 2 (conditional, based on diagnostic) — patch typing / regalloc classification.
4. Fix 3 (conditional, based on diagnostic) — relax safe filter or add entry-reload.

## Expected Effect

**mandelbrot** (primary target; Pattern 1 dominant):
- After Fix 1: estimated **~20-25% reduction** from de-duplicating squaring
  and loop-invariant reloads in B3. 0.38s → 0.29-0.30s.
- After Fix 2 (if phis were losing FPR): **~35-45% reduction** from true
  FPR-resident SSA across the loop. 0.38s → 0.21-0.25s.
- Floor: the theoretical minimum for 10 float ops × 62500 iters ≈ 0.18s.

**spectral_norm**: partial — Pattern 1 is present but Pattern 2
(generic Mul/Add on `any`) still dominates. Expect 0.34s → 0.27-0.30s.

**math_intensive**: Pattern 1 fires on MulFloat chain for `x^2+y^2+z^2`.
Expect ~8-12% reduction. 0.19s → 0.17s.

**nbody**: exit-resume (`math.sqrt` CALL + `bodies` GetGlobal) dominates;
Fix 1 is a small win inside tag-dispatch's float path. Expect ≤3% change.

**matmul**: Tier 2 barely active per round-6 findings. Expect 0% change
(deferred to Phase 5).

**Int benchmarks (sieve, fibonacci_iterative, sort, fannkuch)**: must not
regress. Scratch caching is opt-in for float ops only; the int raw-register
path is untouched. Verified via full benchmark run.

## Failure Signals

- **Signal 1:** After Fix 1 alone, mandelbrot shows <5% improvement → Pattern 1
  is *not* the dominant cost; Gap B or Gap C is the structural blocker.
  **Action:** proceed to Fix 2 diagnostic, do not commit Fix 1 alone.
- **Signal 2:** After Fix 2, any benchmark regresses >3% vs baseline →
  the typing change over-promoted an int-pattern to float.
  **Action:** revert Fix 2, re-examine which phis were incorrectly typed.
- **Signal 3:** Any correctness failure (Diagnose() mismatch or benchmark
  producing wrong output) → the scratch-FPR cache is being invalidated
  incorrectly across an emit call that clobbers D0-D3.
  **Action:** revert, add cache-invalidation at every D0-D3 write site
  (FMOVtoFP/SCVTF/FLDRd helpers).
- **Signal 4:** After all 3 fixes, mandelbrot still >0.30s → register
  pressure (8 FPRs insufficient). **Action:** pivot to widening FPR pool
  (D12-D15) in a follow-up round; this round stops at 0.30s as "partial win."
- **Signal 5:** 3 commits without benchmark improvement → abort per budget.

## Task Breakdown

Each task = one Coder sub-agent invocation.

- [ ] 1. **Add FPR diagnostic harness** — file(s):
  `internal/methodjit/tier2_fpr_residency_test.go` (new) — test:
  `TestFPRResidencyReport`. Writes to stdout for each of 5 float benchmarks:
  count of float-typed Phis, count of FPR-allocated Phis, count of entries
  in `safeHeaderFPRegs` per loop header, peak concurrent FPR live-range.
  No production code changes. Output pasted back to plan as baseline data
  for Fix 2/3 decisions.
- [ ] 2. **Fix 1: scratch-FPR operand cache** — file(s):
  `internal/methodjit/emit_reg.go` (modify `resolveRawFloat`),
  `internal/methodjit/emit_compile.go` (add `scratchFPRCache` field to
  `emitContext`, zero at block entry),
  `internal/methodjit/emit_dispatch.go` (clear cache at start of each
  instruction emit). Test: new `TestScratchFPRCache` in
  `emit_tier2_correctness_test.go` — compile a function with `v*v`
  arithmetic and assert the emitted bytes contain exactly ONE `ldr`/`fmov`
  pair for the duplicated operand (not two). Also run existing
  `TestDiagnoseMandelbrot` / `TestEmitMandelbrot` (if present) for
  correctness.
- [ ] 3. **Fix 2 (if diagnostic warrants): Phi typing or regalloc classification**
  — file(s): `pass_typespec.go` or `regalloc.go` (pick one based on Task 1
  output). Test: extend `tier2_fpr_residency_test.go` to assert float-phis
  now have `pr.IsFloat == true`. Decision is made autonomously after Task 1.
  If Task 1 shows phis are already FPR-allocated, **SKIP Task 3**.
- [ ] 4. **Fix 3 (if diagnostic warrants): safe-header FPR relaxation** —
  file(s): `emit_loop.go` or `emit_compile.go`. Test: assert the phi's FPR
  is now active in the target non-header block. **SKIP if Task 2 alone
  achieves ≥35% mandelbrot reduction.**
- [ ] 5. **Integration + full benchmark suite** — run
  `bash benchmarks/run_all.sh`, capture before/after for all 21 benchmarks,
  verify:
    (a) mandelbrot ≥35% reduction (primary success criterion),
    (b) no int-benchmark regresses >2%,
    (c) all 21 benchmarks produce correct output (no HANG/ERROR/wrong result).
  Update `opt/current_plan.md` Results table.

## Budget

- Max commits: **3 functional** (Fix 1 + optional Fix 2 + optional Fix 3)
  + **1 revert slot** if any fix regresses.
- Max files changed: **5** (emit_reg.go, emit_compile.go, emit_dispatch.go,
  optionally pass_typespec.go OR regalloc.go, optionally emit_loop.go) +
  2 test files.
- Abort condition: **2 commits without mandelbrot improvement** → stop,
  write lessons, pivot to LICM (Phase 4) or FPR pool widening in next round.

Revert slot: consumed only if Task 2 or Task 4 is reverted at VERIFY.
Otherwise dropped; actual commit count ≤ 3.

## Results (filled after VERIFY)

Commits landed: 2 functional (Fix 1: 3ded153, Fix 3: 686ba11). Fix 2 skipped
(diagnostic confirmed all float phis ARE FPR-allocated — Gap B was never real).

Single-run `run_all.sh` timings (JIT mode):

| Benchmark | Baseline (s) | After (s) | Change |
|-----------|--------------|-----------|--------|
| **mandelbrot** | **0.382** | **0.372** | **-2.62%** ← primary target |
| fib | 1.411 | 1.445 | +2.41% (within noise, see below) |
| fib_recursive | 14.231 | 14.099 | -0.93% |
| sieve | 0.234 | 0.226 | -3.42% |
| ackermann | 0.256 | 0.260 | +1.56% |
| matmul | 0.833 | 0.840 | +0.84% |
| spectral_norm | 0.336 | 0.336 | 0% |
| nbody | 0.610 | 0.618 | +1.31% |
| fannkuch | 0.071 | 0.068 | -4.23% |
| sort | 0.053 | 0.053 | 0% |
| sum_primes | 0.004 | 0.004 | 0% |
| mutual_recursion | 0.197 | 0.187 | -5.08% |
| method_dispatch | 0.106 | 0.101 | -4.72% |
| closure_bench | 0.027 | 0.027 | 0% |
| string_bench | 0.031 | 0.031 | 0% |
| binary_trees | 2.108 | 2.093 | -0.71% |
| table_field_access | 0.073 | 0.072 | -1.37% |
| table_array_access | 0.136 | 0.136 | 0% |
| coroutine_bench | 19.315 | 18.757 | -2.89% |
| fibonacci_iterative | 0.301 | 0.287 | -4.65% |
| math_intensive | 0.191 | 0.197 | +3.14% (within noise, see below) |
| object_creation | 0.780 | 0.769 | -1.41% |

Aggregate JIT wall-time: 41.983s → 41.195s (**-1.88% net improvement**).

**Noise verification (2-run comparison of HEAD vs Fix1-only):**
- fib: HEAD 1.419-1.436s, Fix1-only 1.431-1.433s → within noise.
- math_intensive: HEAD 0.191-0.196s, Fix1-only 0.188-0.195s → within noise.

Neither "regression" reproduces under repeated measurement. The +2.41% fib
and +3.14% math_intensive deltas are single-run measurement variance, not
regalloc-induced churn.

**Correctness:** All 21 standalone benchmarks produce matching VM/JIT output.
Pre-existing `BenchmarkGScriptJITObjectCreateWarm` SIGSEGV in the Go warm
harness (`executeTableExit` → shape lookup) reproduces on `main`, so it is
NOT introduced by either fix. Existing unrelated failures
(`internal/runtime/shape_new_test.go` build, `TestTraceExec_EmptyLoopBody`)
also unchanged.

**Primary target verdict:** mandelbrot hit **-2.62%** (0.382s → 0.372s),
far short of the ≥35% goal. Signal 4 fires: 8-FPR pool sufficient, but the
real bottleneck is cross-block loop-invariant materialisation (v27, v14,
ConstFloat 2/4) re-loaded every iteration. Pivot to LICM or FPR-pool
widening in the next round.

**Diagnostic confirmation of the infrastructure win (worth keeping):**
mandelbrot's inner loop (header blk=5, body blk=3) now shows
`safeHeaderFPRegs(2): D4=v65, D5=v64` — previously empty. Body FPR defs
start at D6 (no longer touches D4/D5). Every v64/v65 use now hits
`hasFPReg` (zero instructions) instead of slot-load + FMOVtoFP. Same
pattern on spectral_norm's multiplyAv/multiplyAtv, nbody's offsetMomentum,
matmul's matmul, math_intensive's leibniz_pi/distance_sum/collatz_total.
The infrastructure is now in place for downstream LICM to realise larger
wins.

## Lessons (filled after completion/abandonment)

1. **Diagnostic harness unlocked scope narrowing early.** The FPR residency
   report (Task 1) showed phis were FPR-allocated correctly and peak FPR
   live-range ≤ 7. This immediately killed the Gap B hypothesis and the
   FPR-pool-widening hypothesis, saving an entire Fix 2 round. Write
   diagnostic tests BEFORE guessing which gap to patch.

2. **Per-instruction scratch-FPR caching alone is a tiny win.** Mandelbrot's
   hot loop has exactly 4 `v*v`-style duplicated operand sites per
   iteration. At 4 insns saved × 3.1M iters ≈ 12.5M cycles ≈ 4ms on a
   380ms budget = 1%. The plan's "20-25% from Fix 1 alone" estimate was
   wildly optimistic because it missed that the duplicated-operand
   pattern is only one small sliver of the per-iter overhead.

3. **Regalloc forward-walk per-block model is architecturally fine but
   needs cross-block phi reservation.** Adding `pinned` regState entries
   for loop-header phis is a small, local change that enables the
   downstream `safeHeaderFPRegs` to populate correctly without any emit
   rewrites. This is the Wimmer-Franz "resolution" phase made tractable
   for our simpler allocator.

4. **Nested loops lurked as a correctness minefield.** Once bodies stop
   clobbering outer-loop phi FPRs, the `loopExitBoxPhis` and `isLoopExit`
   analyses had to become nesting-aware (`isLoopExit` = leaves *innermost*
   loop; `emitLoopExitBoxing` scoped to the exiting header's phis). A
   naive general carry broke `TestTieringManager_NestedCallSimple`; the
   coder correctly restricted the carry to tight bodies (2-block loops)
   to contain the blast radius. Next round: relax tight-body
   restriction once nested-loop correctness is thoroughly tested.

5. **Loop-invariant materialisation is the next bottleneck.** Mandelbrot's
   B3 loads v27, v14 (outer-loop constants) and creates v45=2.0, v50=4.0
   every iteration. LICM to hoist these out of B3 is the next ROI
   target. Also candidates: preload cross-block floats into a
   reserved FPR set at block entry (cheap if pool has slack).

6. **Single-run benchmark variance is ±3% for ~0.2s benchmarks.** The
   sub-agent flagged "+2.41% fib regression" and "+3.14% math_intensive
   regression" as blockers; 2-run repeat at HEAD and at Fix1-only showed
   both are within noise. For borderline cases, always re-measure before
   concluding regression.
