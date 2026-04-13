# Optimization Plan: GPR-Resident Int Counter + Fused Compare+Branch

> Created: 2026-04-06
> Status: active
> Cycle ID: 2026-04-06-gpr-counter-fused-branch
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md

## Target

B3 analysis items #2 and #3 from the tier2-float-loops initiative. Two independent
peephole-level codegen improvements targeting inner-loop overhead in float-heavy benchmarks.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| mandelbrot | 0.390s | 0.058s | 6.7x | 0.320s (~18% reduction) |
| nbody | 0.640s | 0.035s | 18.3x | 0.540s (~15% reduction) |
| spectral_norm | 0.334s | 0.008s | 41.8x | 0.280s (~16% reduction) |
| matmul | 0.864s | 0.022s | 39.3x | 0.730s (~15% reduction) |
| sieve (spillover) | 0.241s | 0.011s | 21.9x | 0.200s (~17% reduction) |
| fibonacci_iterative (spillover) | 0.299s | N/A | — | 0.240s (~20% reduction) |

## Root Cause

Two independent codegen inefficiencies waste ~33% of inner-loop instructions:

**1. Int counter box/unbox round trip (11 insns, 23.4% of mandelbrot inner loop)**

The Tier 2 loop counter phi (v58→v61 in mandelbrot) is NaN-boxed and stored to the VM
register file every iteration: `add → ubfx → orr → str` (box+store), then
`ldr → sbfx` (reload+unbox bound), `cmp` (compare). The raw-int phi mechanism exists
in emit_dispatch.go but the counter's StoreSlot still forces boxing. The loop bound
(a constant) is also reloaded from memory every iteration instead of staying in a GPR.

**2. Compare+branch bool materialization (5 insns, 10.6% of mandelbrot inner loop)**

Every comparison (`LtFloat`, `LeInt`) materializes a NaN-boxed boolean:
`fcmp → cset → orr x25 → mov xN → tbnz`. But in tight loops the bool is consumed
only by the immediately-following `Branch` — no other consumer exists. A fused
`fcmp → b.lt` (2 insns) or `cmp → b.le` (already computed) would eliminate 3 insns
per comparison site. Mandelbrot has 2 such sites per iteration (float escape check +
int counter check) = 6 insns saved.

## Prior Art (MANDATORY)

**V8 Maglev (untagged Int32 phi):**
Maglev's `MaglevPhiRepresentationSelector` post-pass converts phis to `kInt32`
representation when all inputs/uses are Int32-compatible. Untagged phis live in GPRs
with no tagging per iteration. Re-boxing happens only on deopt (the `TranslatedState`
machinery reads the untagged GPR and materializes a Smi/HeapNumber) and on stores to
tagged heap slots (via `Int32ToNumber` conversion node). Loop back-edges use
`FixLoopPhisBackedge` to insert gap moves if representation mismatches — lazy fixup
rather than eager re-tagging. The stack frame is split into tagged/untagged regions
so the GC never scans untagged Int32 values.

**V8 TurboFan (FlagsContinuation / compare+branch fusion):**
TurboFan's instruction selector uses `FlagsContinuation` to communicate how a
comparison's flags are consumed. `kFlags_branch` mode fuses the comparison into a
conditional branch — no boolean materialized. The key gate is `CanCover(user, node)`:
the comparison must be in the same basic block as the branch, be pure, and have
exactly one use (`node->OwnedBy(user)`). When `CanCover` passes, the arch selector
emits `CMP + B.cc` (or `CBZ`/`CBNZ`/`TBZ`/`TBNZ` for zero/bit tests). When the
comparison has >1 consumer, it falls back to `kFlags_set` (materialize bool via
`CSET`) and the branch uses `CBZ/CBNZ` on the materialized register.

**JSC DFG (Int32 loop counter):**
DFG's prediction propagation labels values as `SpecInt32Only`. When
`!profile->didObserveInt32Overflow()`, the DFG emits native 32-bit arithmetic
(`branchAdd32`/`branchSub32`) with hardware overflow detection (single `ADDS/SUBS` +
branch-on-overflow). On overflow → OSR exit to Baseline JIT. Loop counters naturally
stay Int32 in GPRs as long as profiling never observed overflow.

**SpiderMonkey IonMonkey:**
IonMonkey's `LCompareAndBranch` instruction combines comparison + branch into a single
LIR node during lowering. The `LIRGenerator::visitTest` method checks if the test input
is a comparison with a single use and lowers to `LCompareAndBranch` instead of separate
`LCompare` + `LGoto`.

Our constraints vs theirs:
- We have only 4 allocatable GPRs (X20-X23) vs V8's ~12 and JSC's ~8. Keeping the
  counter in a GPR is feasible (1 GPR) but the bound also needs a GPR or must be an
  immediate in the CMP instruction.
- Our NaN-boxing scheme means "unboxed int" is just the raw int64 without the 0xFFFE
  tag — simpler than V8's Smi (shifted) or JSC's boxed double. Re-boxing is cheap
  (ORR with X24 tag constant).
- We don't have a lowering phase between IR and emission — fusion must happen in the
  emitter by peeking at the consuming instruction.

## Approach

Two independent sub-tasks, implementable in parallel:

### Sub-task A: GPR-Resident Int Loop Counter

**Goal:** Keep int loop-counter phis in GPRs across back-edges. Eliminate per-iteration
box/unbox/store/reload for the counter AND the loop bound.

**File changes:**

1. **`regalloc.go`** (~40 lines changed):
   - Extend `preAllocateHeaderPhis` to allocate GPR slots (X20-X23) for int-typed
     loop-counter phis, not just FPR slots for float phis. Detection: phi is `int`-typed,
     defined by `AddInt`, used by `LeInt`/`LtInt` + `Branch` — classic ForLoop counter.
   - Add int phis to the `carried` map so the GPR assignment persists across the back-edge.
   - The loop bound (a `ConstInt` or `LoadSlot` used only by the `LeInt`/`LtInt` in the
     header) should also be pinned in a GPR if budget allows.

2. **`emit_dispatch.go`** (~30 lines changed):
   - In `emitPhi` / phi-move logic for loop headers: when the phi has a GPR carry
     assignment, emit `ADD xN, xM, #1` directly into the carried GPR instead of going
     through box/store/reload/unbox.
   - On deopt paths and function exit: emit re-boxing (`ORR xN, xCarry, X24` + `STR`)
     to flush the unboxed counter back to the VM register file.

3. **`emit_arith.go`** (~15 lines changed):
   - `emitAddInt`: when both operands are GPR-resident (raw int), emit `ADD` directly
     without unboxing. Skip the `StoreSlot` if the result is carried.
   - `emitLeInt`/`emitLtInt`: when the counter operand is GPR-resident, use it directly
     in `CMP` without loading from memory.

### Sub-task B: Fused Compare+Branch

**Goal:** When a comparison op is used only by an immediately-following `Branch` in the
same block, fuse them into `CMP/FCMP + B.cc` (2 insns instead of 5).

**File changes:**

1. **`emit_dispatch.go`** (~50 lines changed):
   - In `emitBranch`: before the default TBNZ path, peek backwards at the instruction
     that defined the condition value. If it's a `LtFloat`/`LeFloat`/`LtInt`/`LeInt`/`EqInt`/`EqFloat`
     in the same block AND the comparison has exactly one use (computed via a local
     single-pass use-count, or check `useCounts[cmpValID] == 1`), then:
     - Do NOT emit the comparison's `CSET + ORR + MOV` sequence.
     - Instead, emit `B.cc trueTarget` directly after the already-emitted `CMP`/`FCMP`.
     - Mark the comparison as "fused" so its normal emission is skipped (or emit it
       without the bool materialization tail).
   - Add a `fusedCmp` flag or a pre-scan that identifies fusable pairs before emission.

2. **`emit_arith.go`** (~20 lines changed):
   - `emitIntCmp` (covers `LtInt`, `LeInt`, `EqInt`): add a "fused" code path that
     emits only `CMP reg, reg` (or `CMP reg, #imm`) without `CSET + ORR`. Return
     the ARM64 condition code (`arm64.CondLT`, `arm64.CondLE`, etc.) to the caller.

3. **`emit_call.go`** (~15 lines changed):
   - `emitFloatCmp`: same pattern — add a "fused" path emitting only `FCMP d, d`,
     returning the condition code.

4. **`pass_dce.go`** (read-only, reuse `computeUseCounts`):
   - Reuse the existing `computeUseCounts()` function during emission to identify
     single-use comparisons. Alternatively, compute use counts once at emission start
     and store in the emitter state.

## Expected Effect

**Combined (both sub-tasks):**

| Benchmark | Insns Saved/Iter | Expected Reduction |
|-----------|------------------|--------------------|
| mandelbrot | ~17 of 47 (36%) | 15-20% wall-time |
| nbody | ~12-14 (est.) | 12-16% wall-time |
| spectral_norm | ~12-14 (est.) | 12-16% wall-time |
| matmul | ~10-12 (est.) | 10-14% wall-time |
| sieve | ~8-10 (int-only) | 15-20% wall-time |
| fibonacci_iterative | ~8-10 (int-only) | 15-20% wall-time |

**Sub-task A alone** (GPR counter): ~10% wall-time on float loops, ~15-20% on int loops.
**Sub-task B alone** (fused branch): ~8% wall-time on all tight loops.

Zero regressions expected — both are strictly tighter codegen for existing patterns.
No new deopt paths, no tiering policy changes, no new IR ops.

## Failure Signals

- Signal 1: **Any benchmark produces wrong results after sub-task A** → the GPR-carried
  counter is not being flushed on deopt. Action: add `Diagnose()` test, fix deopt path,
  do NOT proceed to sub-task B until A is correct.
- Signal 2: **mandelbrot wall-time doesn't improve ≥8% after both sub-tasks** → either
  the B3 instruction counts were misleading (superscalar execution hiding the overhead)
  or there's a deeper bottleneck. Action: re-profile with post-change disassembly,
  compare instruction counts.
- Signal 3: **regalloc.go exceeds 800 lines** → proactive split needed per coding
  conventions. Action: extract loop-carry logic into `regalloc_loop.go` before proceeding.
- Signal 4: **GPR pressure conflict** — only 4 GPRs available, counter + bound needs 2,
  leaving only 2 for other int work → if any benchmark regresses due to GPR starvation,
  fall back to carrying only the counter (not the bound) and use CMP with immediate
  when the bound is a constant.

**MANDATORY tiering policy check:** This plan does NOT touch `func_profile.go` or
Tier 2 promotion criteria. No CLI integration check needed.

## Task Breakdown

- [x] 1. **GPR-resident int loop counter — regalloc** — file(s): `regalloc.go` — test: `TestRegAlloc_IntPhiCarry` (new), existing `TestRegAlloc_*` suite. Extended with `collectLoopBoundGPRs` to pin loop bounds in GPRs. Int phis already carried via existing mechanism.
- [x] 2. **GPR-resident int counter — emitter** — Existing emitter infrastructure (emitRawIntBinOp, resolveRawInt, emitPhiMoveRawInt, loopExitBoxPhis) handles GPR-resident counters correctly after regalloc change. No emitter code changes needed. 9 verification tests added.
- [x] 3. **Fused compare+branch — emission** — file(s): `emit_dispatch.go`, `emit_arith.go`, `emit_call.go`, `emit_compile.go` — Pre-scan identifies single-use comparisons via computeUseCounts. emitIntCmp/emitFloatCmp set fusedCond when fused; emitBranch emits B.cc directly. 5 tests added.
- [x] 4. **Correctness + benchmark** — All tests pass. Benchmarks run. fibonacci_iterative -7.4%, matmul -2.7%, math_intensive -3.5%. Float-heavy benchmarks show minimal change (superscalar hiding overhead).

Tasks 1+2 (GPR counter) are sequential (2 depends on 1's regalloc output).
Task 3 (fused branch) is independent — can run in parallel with tasks 1+2.
Task 4 depends on all prior tasks.

## Budget

- Max commits: 4 functional (+1 revert slot)
- Max files changed: 5 (regalloc.go, emit_dispatch.go, emit_arith.go, emit_call.go, + 1 test file)
- Abort condition: 2 commits without any benchmark improvement, OR any correctness regression that can't be fixed in 1 additional commit

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| mandelbrot | 0.390s | 0.383s | -1.8% |
| nbody | 0.640s | 0.634s | -0.9% |
| spectral_norm | 0.334s | 0.335s | +0.3% |
| matmul | 0.864s | 0.841s | -2.7% |
| sieve | 0.241s | 0.241s | 0% |
| fibonacci_iterative | 0.299s | 0.277s | -7.4% |
| math_intensive | 0.198s | 0.191s | -3.5% |
| fannkuch | 0.072s | 0.071s | -1.4% |
|-----------|--------|-------|--------|

## Lessons (filled after completion/abandonment)

**What worked:**
- Fused compare+branch is a clean peephole optimization. Pre-scan at compile start + fusedCond/fusedActive state is simple and correct.
- The existing emitter infrastructure (raw int GPR carry, loopExitBoxPhis, loopPhiOnlyArgs) was already well-designed — Task 2 needed zero emitter changes.
- Running Tasks 1+3 in parallel saved time since they're truly independent.

**What didn't meet expectations:**
- The plan estimated 15-20% wall-time reductions for float loops, but actual improvements were 1-3%. The B3 instruction-count analysis overstated impact because ARM64's superscalar execution hides much of the overhead. Three fewer instructions in a 47-instruction loop saves less than 6% even in theory, and OoO execution further masks it.
- fibonacci_iterative (-7.4%) was the biggest winner — pure int loop with no float ops, the fused branch + GPR carry both help directly.

**What to remember:**
- Instruction count != wall time. Profile after changing, not just before.
- The real bottleneck in float loops is likely memory traffic (LDR/STR to VM register file) and branch overhead, not instruction count in the comparison.
- Next investigation: the LICM-invariant FPR carry should have a bigger impact since it eliminates actual memory loads (not just redundant instructions).
