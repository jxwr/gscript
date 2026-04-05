# Initiative: Tier 2 Float Loop Performance

> Status: active
> Created: 2026-04-05
> Owner: gs-opt-loop
> Category: tier2_float_loop

## Motivation

Float-heavy loop benchmarks have never been measured or profiled. All 5 are correct but slow:

| Benchmark | JIT | VM ratio | LuaJIT | Gap vs LuaJIT |
|-----------|-----|----------|--------|---------------|
| spectral_norm | 0.33s | 2.0x (known regression vs pre-int48-overflow baseline 0.138s) | — | — |
| nbody | 0.61s | 1.0x | 0.0055s | 110x |
| mandelbrot | 0.39s | — | — | — |
| math_intensive | 0.19s | — | — | — |
| matmul | 0.82s | — | — | — |

Total untouched wall-time: ~2.3s, concentrated in tight float loops. `known-issues.md` explicitly flags spectral_norm next step as "pprof Tier 2 emitted code for inner loop" — a standing action item never taken.

## Expected Impact

Hypotheses (to test in Phase 1):
- FPR spill/reload per loop iteration (D4–D11 allocatable but loops may need more)
- Type guards firing per iteration (deopt-thrash)
- Int48 overflow check on loop counter despite Aux2=1 exemption
- Missing LICM (loop-invariant code motion) — constants hoisted out of loop body

Target (conservative, based on spectral_norm pre-regression): spectral_norm 0.33s → 0.15s. nbody 0.61s → 0.20s.

## Phases

- [x] Phase 1 (round 6): **Profile Tier 2 emitted code for all 5 float benchmarks** — IR + ARM64 disassembly + pprof captured. Deliverable: `opt/pprof-tier2-float.md`. Harness: `internal/methodjit/tier2_float_profile_test.go`.
- [x] Phase 2 (round 7): **Pattern 1 — eliminate per-op box/unbox** — scratch-FPR operand cache (Fix 1, commit 3ded153) + loop-header phi FPR carry into tight bodies (Fix 3, commit 686ba11). Infrastructure landed. Mandelbrot only -2.62% (target ≥35% missed); aggregate -1.88%. Loop-invariant materialisation (LICM) is the real remaining bottleneck.
- [ ] Phase 3 (round 8): **Pattern 2 — feedback-typed heap loads** — record observed result type for `GetTable`/`GetField`/`Call` in `FeedbackVector`; `TypeSpecialize` inserts `GuardType` after load so downstream `Mul`/`Add` promotes to `MulFloat`/`AddFloat`. Primary target: matmul, spectral_norm.
- [ ] Phase 4 (round 9+): **LICM pass** (`pass_licm.go`) — hoist loop-invariant `LoadSlot`/`ConstFloat`. Confirmed needed by mandelbrot's cr/ci reloads.
- [ ] Phase 5 (deferred): **Investigate matmul Tier 2 tier-up** — profile shows matmul stuck in Tier 1; needs `-jit-stats` instrumentation.
- [ ] Phase 6 (deferred): **Range analysis / overflow-check elimination in float loops** — extend round-3 work (commit f2bb4bf archive) to float arithmetic.

## Prior Art

- **V8 TurboFan**: LoopPeeling, LoopUnrolling, and MachineOperatorReducer strength-reduce inner loops. `LoadElimination` removes redundant loads.
- **HotSpot C2**: IdealLoopTree + Loop-invariant hoisting is a standard pass.
- **LLVM**: LICM + ScalarEvolution + LoopStrengthReduce — the canonical reference.
- **V8 Maglev**: for comparison, Maglev chose NOT to do LICM (too expensive at its tier); TurboFan does. Our Tier 2 is single-tier optimizing, so LICM is appropriate.

## Rounds

| Round | Phase | Outcome | Notes |
|-------|-------|---------|-------|
| 6 (2026-04-05) | Phase 1 diagnostic | **complete — non-flat, shallow escalation** | Top-3 hot patterns: (1) per-op box/unbox round-trip (5/5 benchmarks), (2) generic Mul/Add dispatch on `any`-typed loads (3/5), (3) redundant same-slot load (4/5). Primary target for round 7: Pattern 1 (FPR-resident SSA across blocks). Artifacts: `opt/pprof-tier2-float.md` + `opt/pprof-tier2-float-artifacts/` (5 pprof + 5 .asm). |
| 7 (2026-04-05) | Phase 2 FPR-resident | **improved (aggregate -1.88%, primary target missed)** | 2 functional commits: Fix 1 scratch-FPR operand cache (3ded153), Fix 3 loop-header phi FPR carry into tight 2-block bodies (686ba11). Fix 2 (phi typing) skipped — diagnostic harness showed all float phis already FPR-allocated. Mandelbrot -2.62% (0.382s→0.372s, target ≥35%). Zero regressions. `safeHeaderFPRegs` now populated for all 5 float benchmarks' inner loops — infrastructure win enables downstream LICM. |

## Next Step

**Phase 4 (round 8) — LICM pass.** Round 7 showed scratch-FPR caching is a
1% win by itself; the dominant remaining cost in mandelbrot's B3 is
loop-invariant materialisation (v27, v14, ConstFloat 2/4 re-loaded each
iteration). Skip Phase 3 (feedback-typed heap loads) for now — mandelbrot
has no heap loads in its hot loop, and LICM directly attacks the observed
bottleneck. Build `pass_licm.go` to hoist `LoadSlot`/`ConstFloat` whose
dominating definition is outside the loop body. Also relax Fix 3's
"tight 2-block body only" restriction once nested-loop correctness is
thoroughly tested (current guard on `TestTieringManager_NestedCallSimple`).
Primary target: mandelbrot (still 0.37s → ~0.22s expected); secondary:
spectral_norm, math_intensive.

## Risks / Failure Signals

- If pprof shows no dominant hotspot (flat profile), architecture is already optimal and the gap may be a deeper codegen issue (NaN-box overhead on every float op). Pivot to arch_refactor.
  - **Round 6 result: not flat — Pattern 1 (per-op box/unbox) accounts for >30% of mandelbrot's inner loop instructions and affects 5/5 benchmarks. Shallow escalation.**
- Abandon if 2 rounds of inner-loop tuning fail to move spectral_norm below 0.25s.
