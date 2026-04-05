# Optimization Plan: Profile Tier 2 Float Loops (spectral_norm + nbody + matmul + mandelbrot + math_intensive)

> Created: 2026-04-05
> Status: active
> Cycle ID: 2026-04-05-tier2-float-profile
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (promote draft → active)

## Target

**This is a diagnostic/profiling round, not an optimization round.** Phase 1 of the Tier 2 Float Loops initiative. Deliver a ranked, evidence-backed hotspot report that feeds Phase 2's target selection next round. No Go source files are modified.

| Benchmark | Current (JIT) | VM | LuaJIT | Gap vs LuaJIT | This-round target |
|-----------|---------------|-----|--------|---------------|-------------------|
| spectral_norm | 0.335s | 1.010s (0.33x) | 0.008s | 41.9x (0.33s wall) | Top-3 hot instructions identified in emitted Tier 2 ARM64 |
| nbody | 0.607s | 1.933s (0.31x) | 0.033s | 18.4x (0.57s wall) | Top-3 hot instructions identified |
| matmul | 0.811s | 1.044s (0.78x) | 0.022s | 36.9x (0.79s wall) | Top-3 hot instructions identified |
| mandelbrot | 0.379s | 1.392s (0.27x) | 0.058s | 6.5x (0.32s wall) | Top-3 hot instructions identified |
| math_intensive | 0.189s | 0.925s (0.20x) | — | — | Top-3 hot instructions identified |

**Total unblocked wall-time in scope: ~2.32s across 5 benchmarks.**

**Primary deliverable:** `opt/pprof-tier2-float.md` containing:
1. Per-benchmark: pprof top-20 (CLI run with `-cpuprofile`), Tier 2 IR dump for the inner-loop function, disassembly of the emitted ARM64 code for the hot block, count of per-iteration side-exits (if any).
2. Cross-benchmark synthesis: ranked list of the top-3 recurring hot patterns (e.g., "FPR spill in inner loop", "NaN-box unbox/rebox per float op", "int48 overflow check on loop counter that survives range analysis", "per-iteration GuardType"), each tied to specific IR ops / emitter functions.
3. **One-line Phase 2 target**: the single hot pattern that, if fixed, would move the most benchmarks. This becomes round 7's target.
4. **Escalation decision for Phase 2**: shallow (standard V8/HotSpot LICM / loop-peeling / FPR budget work) vs deep (NaN-box overhead dominates → arch_refactor territory).

**Secondary deliverable:** populate the initiative's "Rounds" table row for round 6.

**Abort outcome** (still counts as successful diagnosis): if profiles show a flat distribution with no dominant hotspot, document "flat profile — escalate Phase 2 to deep research on NaN-box overhead" and that IS the deliverable (initiative Risks section already names this case).

## Root Cause

Float-loop benchmarks have **never been profiled**. `known-issues.md` has listed "pprof Tier 2 emitted code for spectral_norm inner loop" as a standing action item since 2026-04-04 without execution. Hypothesized bottlenecks (to rank via profile, not argue from intuition):

- **FPR spill/reload per loop iteration** — only 8 allocatable FPRs (D4–D11); nested float loops may exceed budget and spill per iteration.
- **NaN-box unbox/rebox per float op** — every value is stored NaN-boxed; if the emitter unboxes before each arith op and reboxes after, inner loops pay 2 FMOV + 1 AND per op.
- **Per-iteration GuardType** — if type guards aren't hoisted out of the loop body, every float op pays a CMP + BNE.
- **Int48 overflow check bleed** — round-3 range analysis exempted `Aux2=1` loop counters and propagated ranges, but `spectral_norm` regressed from 0.138s → 0.335s and the wall-time was never recovered. Overflow check may still fire on derived values.
- **Table load per inner iteration** — `v[j]` / `av[i]` are table loads inside loops; no LoadElimination pass means every iteration reloads from memory.

**This round ranks these hypotheses by measurement, not argument.** The reason for fewer inferred hypotheses than would be expected is deliberate: Rule #1 ("observation beats reasoning") — profile first, theorize second.

## Prior Art (MANDATORY)

The target technique (LICM, loop-peeling, FPR budget tuning, boxing elimination) will be selected in round 7 *from* the profile data. For Phase 1 itself, the prior art is **methodology of profiling JIT code** and **which optimization to reach for once the hot pattern is known**:

**V8 TurboFan (methodology):**
TurboFan ships with `--trace-turbo` and `--print-opt-code` to emit per-function disassembly, plus `--prof` + `tick-processor` for sampling profiles that symbolize JIT code regions. For hot-loop diagnosis, V8 engineers dump the scheduled graph + final assembly side-by-side (the `trace-turbo.json` file in the Turbolizer web UI). Our closest analogues: `Print(fn)` on the IR, plus raw disassembly of `cf.Code`'s byte buffer via `otool -d` / `llvm-objdump -d --triple=aarch64`. Pprof already symbolizes our Go-side hot paths (assembler, emit layer, tiering manager); JIT-compiled code shows as `<unknown>` in the `[mmap]` region — we infer Tier 2 overhead as `Execute → jit-region → Go-handler-reentry` cycles.

**V8 TurboFan (optimization, if LICM-class pattern found):**
TurboFan's `LoadElimination` + `MachineOperatorReducer` + `LoopPeeling` are the canonical references for redundant-load / loop-invariant hoisting. For FPR allocation, TurboFan uses a linear-scan allocator with spill-cost-by-loop-depth (inner-loop values get higher priority).

**HotSpot C2 (if LICM pattern found):**
C2's `IdealLoopTree::policy_invariant` hoists any node that is loop-invariant and doesn't raise exceptions. Our Tier 2 currently has **no** loop-invariant hoisting pass — a standard gap. `PhaseIdealLoop::split_if` and `PhasePeephole` are the reference design.

**JavaScriptCore DFG/B3 (if boxing overhead dominates):**
JSC's B3 tier uses **unboxed float64 in registers throughout a function**, only boxing at function entry/exit and at type-heterogeneous merge points. Our Tier 2 appears to box/unbox per op (to be confirmed in this round's disassembly). JSC's `Air` phase groups float ops into unboxed "spans" separated by `FTLBoxDouble`/`FTLUnboxDouble` at the spans' boundaries.

**.NET RyuJIT (if spill pattern dominates):**
RyuJIT's LSRA (Linear Scan Register Allocator) has a concept of "loop-carried" intervals — values live across a back-edge get a spill-cost bonus proportional to loop trip count. Our current forward-walk allocator (5 GPR / 8 FPR) has no loop-depth awareness per `regalloc.go`.

**LLVM (canonical reference):**
`LICM.cpp` + `ScalarEvolution` + `LoopStrengthReduce` are the three-pass pattern every production compiler eventually converges on. We will not write LLVM-quality passes, but the divide-and-conquer structure is the reference: **hoist invariants → simplify induction → strength-reduce**.

**Academic (for boxing-overhead escape hatch):**
Leroy & Grall, "Coinductive big-step operational semantics" — JSC's unboxed-span approach traces back to tagged-vs-native-register literature. If Phase 1 shows NaN-box dominates, this is the research direction for round 7+.

**Our constraints vs theirs:**
- No JIT-symbolizing profiler today — we must cross-reference pprof "unknown PC" samples with the CLI process's mmap region. This round's profile methodology is coarser than V8's, but adequate to rank hot patterns.
- 5 GPR + 8 FPR allocatable — much tighter than V8/HotSpot (28/32 FPRs on ARM64). Spill-rank hypothesis is a priori more plausible for GScript than for production JITs.
- NaN-boxing is non-negotiable at the calling convention (dynamic typing requires a uniform value representation across function boundaries). But *within* a function, we can potentially run unboxed — this is the arch_refactor escape hatch if profiles demand it.

## Approach

**Phase A — Build a disassembly dump harness (test-only, not committed to main).**
One new test file `internal/methodjit/tier2_float_profile_test.go` (build-tagged `darwin && arm64`, committed) with tests that:
1. For each of the 5 benchmark sources, compile the inner-loop function to Tier 2.
2. Print `Print(fn)` (IR after all passes) to test log.
3. Print the raw ARM64 byte buffer from `cf.Code` as hex, and write it to `/tmp/gscript_<bench>_t2.bin` for external disassembly.
4. Print `cf.DirectEntryOffset` and key label offsets so round-7 work can map pprof samples back to IR blocks.

This harness is useful beyond this round (reusable profiling tool), so it stays in-tree.

**Phase B — Run cpuprofile on the CLI for each benchmark.**
Build `/tmp/gscript_profile` once, then:
```
/tmp/gscript_profile -jit -cpuprofile=/tmp/<bench>.prof benchmarks/suite/<bench>.gs
go tool pprof -top -cum /tmp/<bench>.prof | head -30
```
Capture the top-20 from each profile. Cross-reference "unknown PC" time with the CLI process's mmap layout (the JIT region) — this gives a coarse estimate of how much wall-time is in Tier 2 emitted code vs Go handlers vs runtime.

**Phase C — Disassemble hot blocks.**
For each benchmark, take the `.bin` from Phase A and run:
```
llvm-objdump -d --triple=aarch64-apple-darwin /tmp/gscript_<bench>_t2.bin | head -200
```
(or `otool -tvV` if llvm-objdump absent). Cross-reference the disassembly with the IR dump from Phase A to identify the emitted inner-loop block. Inspect for: FPR spill/reload pairs, box/unbox sequences, guard check branches, overflow-check SBFX+CMP sequences.

**Phase D — Synthesize findings.**
Produce `opt/pprof-tier2-float.md` with the structure in Target → Primary deliverable. Rank hot patterns by count-of-benchmarks-affected (high leverage: appears in 4–5 benchmarks) × per-iteration cost.

## Expected Effect

**This round delivers no wall-time changes.** Expected artifacts:

| Artifact | Contents |
|----------|---------|
| `opt/pprof-tier2-float.md` | Per-benchmark profile + top-3 cross-benchmark hot pattern ranking + round-7 target + shallow/deep escalation decision |
| `internal/methodjit/tier2_float_profile_test.go` | Reusable IR + disassembly dump harness (build-tagged) |
| `opt/initiatives/tier2-float-loops.md` | Updated: status=active, Round 6 entry in Rounds table |

**Cone of outcomes for round 7 (set by this round's data):**
- **If LICM-class pattern dominates (≥3 benchmarks)**: round 7 is `pass_licm.go` creation — concrete, shallow research (V8 TurboFan LoopPeeling or HotSpot policy_invariant). Expected: 20–40% improvement on 3–5 float benchmarks.
- **If FPR spill dominates**: round 7 extends `regalloc.go` with loop-depth spill cost. Expected: 10–25% improvement on 2–3 deepest-loop benchmarks (nbody, mandelbrot).
- **If NaN-box per-op dominates**: round 7 escalates to `deep` research, investigate JSC-style unboxed-span emission — multi-round arch_refactor. No wall-time win next round.
- **If overflow-check bleed on float ops**: round 7 is a localized Aux2 exemption extension, ~1 file change.
- **If flat profile**: arch_refactor, round 7 is deep-research.

## Failure Signals

- **Signal 1 (flat profile, no dominant pattern)**: top-20 pprof + hot-block disassembly show no instruction/pattern accounting for >15% of any benchmark's inner loop. → **Action: write flat-profile finding into deliverable, escalate Phase 2 to `deep` research depth, recommend arch_refactor investigation of NaN-box overhead.** This is still a successful round — the negative result is the signal.
- **Signal 2 (profiler symbolization fails entirely)**: pprof output is 100% `<unknown>` with no exported Go-function hot paths. → **Action: profile-collection methodology is inadequate. Pivot to counter-based profiling: add temporary exit counters (ExitBaselineOpExit, ExitCallNative, deopt) to emit layer, re-run, count exits per iteration.** This is a tool-fix round, deliverable becomes the counter infrastructure.
- **Signal 3 (disassembly is unreadable / too large)**: Tier 2 emitted code for even one inner loop exceeds 500 ARM64 instructions and the hot block isn't obvious. → **Action: use the IR dump + `Pipeline.Dump()` to identify the inner loop's *IR block*, then label it in emit to emit a sentinel instruction (e.g., `BRK 0xDEAD`) at block entry during profiling. Scan disassembly for the sentinel.** Small emit-layer change, still in scope.
- **Signal 4 (harness doesn't build)**: Phase A test file has build errors due to API drift in `methodjit` package. → **Action: skip the in-tree harness, do disassembly via one-off shell-script that reads `/tmp/gscript_profile` binary sections.** Deliverable unchanged; tool is just ephemeral instead of reusable.
- **Signal 5 (budget exceeded)**: round exceeds 5 diagnostic iterations per benchmark without identifying top-3 patterns. → **Action: deliver partial report (whichever benchmarks did complete), escalate remainder to round 7.** Lesson #6: profile before optimizing means profile *effectively*, not forever.

**MANDATORY integration-check check:** This plan does **not** touch `func_profile.go` or Tier 2 promotion criteria. No integration CLI run required. (Phase B already runs CLI for profiling; this doubles as sanity check that no accidental regression landed.)

## Task Breakdown

Each task = one Coder sub-agent invocation. Strictly sequential.

- [x] **1. Build IR + disassembly dump harness.** Done. File committed (b922562). All 5 TestProfile_* tests pass. `/tmp/gscript_*_t2.bin` written (1324-12584 bytes each). IR dumps logged.

- [x] **2. Collect pprof profiles from CLI.** Done. 5 .prof + 5 _pprof.txt files under /tmp/. Wall-times captured: spectral_norm 0.340s, nbody 0.618s, matmul 0.818s, mandelbrot 0.373s, math_intensive 0.193s (total 2.34s).

- [x] **3. Disassemble hot Tier 2 blocks.** Done. `llvm-objdump -b binary` unavailable on macOS, so built a one-off golang.org/x/arch/arm64/arm64asm tool (`/tmp/gs_disasm_tool`). Produced 5 .asm files (331-3146 lines). Hot blocks identified by grep'ing for fmul/fadd/fsub density and cross-referencing with IR `Print(fn)` dumps.

- [x] **4. Write synthesis report.** Done. `opt/pprof-tier2-float.md` written. Top-3 hot patterns: (1) per-op NaN-box round-trip (5/5), (2) generic Mul/Add dispatch on `any`-typed loads (3/5), (3) same-slot redundant load (4/5). One-line Phase 2 target: eliminate per-op box/unbox in Tier 2 float loops. Escalation: SHALLOW.

- [x] **5. Update initiative + archive artifacts.** Done. `opt/initiatives/tier2-float-loops.md` flipped to Status: active, Round 6 row added, Phase plan refreshed with concrete round-7/8/9 targets. 10 artifacts copied to `opt/pprof-tier2-float-artifacts/`.

## Budget

- **Max commits:** 3 (1 harness + 1 report + 1 initiative update/artifacts). Strict cap.
- **Max files changed:** 1 new test file (harness) + 1 new report + 1 modified initiative + ≤10 archived artifacts under `opt/pprof-tier2-float-artifacts/`. Zero Go source changes outside the harness.
- **Abort condition:** If Task 1 harness does not compile after 2 API-drift fix attempts, pivot to external shell-based disassembly (Signal 4). If Task 2 profiles are all-`<unknown>`, pivot to counter-based profiling (Signal 2). If report cannot name a top-3 after Tasks 1–3 complete, write flat-profile finding (Signal 1) and finish the round there.

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| — | — | — | no wall-time changes (diagnostic round, as planned) |

Diagnostic artifact summary:
- 1 new test file: `internal/methodjit/tier2_float_profile_test.go` (146 lines, build-tagged `darwin && arm64`)
- 1 synthesis report: `opt/pprof-tier2-float.md` (~300 lines)
- 10 archived artifacts: `opt/pprof-tier2-float-artifacts/` (5 pprof + 5 disassembly)
- 1 initiative update: draft → active, Round 6 row, concrete Phase 2/3/4 targets

## Lessons (filled after completion/abandonment)

**Round-6 lessons:**
1. **`runtime._ExternalCode` is a first-class JIT health signal.** When a benchmark shows 100% `_ExternalCode` (mandelbrot), all wall-time is in emitted code — clean signal for codegen quality work. When it shows <30% (matmul 9%), the Go-side handlers dominate and codegen tuning is premature.
2. **llvm-objdump on macOS doesn't support `-b binary`.** Built a one-off `golang.org/x/arch/arm64/arm64asm`-based tool in /tmp in ~5 minutes. If round 7 wants persistent tooling, land as `cmd/gscript-disasm/`.
3. **The harness pays for itself immediately.** `TestProfile_*` dumps IR + ARM64 bytes in one `go test` run; round-7 can add a new (bench, fn) pair as 3 new lines. Reusable beyond this round.
4. **"Profile before optimizing" meant profile *twice*.** The IR dump told us TypeSpecialize failed on matmul (generic Mul). The disassembly told us even type-specialized loops box/unbox per-op (mandelbrot). Neither alone was sufficient.
5. **Skip the pivot signals when they don't trigger.** Risk-Signal 1 (flat profile → deep arch refactor) was ruled out in <5 minutes of disassembly inspection. Shallow escalation confirmed.
