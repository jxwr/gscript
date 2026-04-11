# Initiative: Tier 2 Float Loop Performance

> Status: paused
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
- [x] Phase 3 (round 12): **Pattern 2 — feedback-typed heap loads** — graph builder reads `proto.Feedback[pc].Result` for GetTable/GetField; inserts `OpGuardType` when monomorphic. IR-level mechanism works correctly, but **blocked**: Tier 1 doesn't collect feedback and interpreter never runs (BaselineCompileThreshold=1), so FeedbackVector always empty at Tier 2 compile time. Code landed but inert. Requires Tier 1 feedback collection (Option A/B/C in plan lessons).
- [x] Phase 4 (round 8): **LICM pass** (`pass_licm.go`, commit f601801 + wiring 9da7d4c) — hoists loop-invariant `ConstInt`/`ConstFloat`/`LoadSlot`/pure arith, inside-out, with `Int48Safe` gate for int arith. 17 consts hoisted in mandelbrot including ConstFloat 2/4 named in round 7. Validator clean, zero correctness regressions. **Wall-time unmoved**: mandelbrot -1.6% (target ≥35%); aggregate LuaJIT-row ~+0.3%. Infrastructure landed — pre-header is now a home for future loop-aware transforms.
- [x] Phase 5 (resolved round 15): **matmul reaches Tier 2 via OSR** (LoopDepth >= 2 gate)
- [ ] Phase 6 (deferred): **Range analysis / overflow-check elimination in float loops** — extend round-3 work (commit f2bb4bf archive) to float arithmetic.
- [x] Phase 8 (round 16): **Load Elimination + GuardType fix** — block-local GetField CSE for redundant field loads (bj.mass, bi.mass in nbody) + fix emitGuardType TypeFloat no-op (correctness bug). nbody -26%, broad 17-49% improvement across GetField-heavy benchmarks.
- [x] Phase 13 (rounds 32-33): **Loop scalar promotion for float (obj,field) pairs** — `LoopScalarPromotionPass` (R32, commit 56b19e7) is wired correctly, passes its synthetic-IR tests, but does NOT fire on production nbody IR. R33 proved the float-gate is broken (true, file:line verified) but NOT the only broken gate (false): two upstream gates bail before classification on nbody — (1) `pass_scalar_promote.go:146-150` exit-block-preds rejects the j-loop because its exit block carries the i-loop preheader as a co-pred; (2) `isInvariantObj` rejects the second i-loop because `b := bodies[i]` is loop-variant by construction. The R33 plan's one-line fix was applied by the Coder and produced a bit-identical 9-pair / 0-phi result pre/post. Phase 13 is **parked**, not closed — the pass design is intact but its reachability on nested nbody-shape loops is structurally limited. Future work: (a) relax exit-block-preds for non-body co-preds that contribute no phi operand, or (b) insert dedicated j-loop exit blocks in `computeLoopPreheaders`, or (c) accept that the pass only fires on single-loop single-field shapes and pivot tier2_float_loop effort to non-nbody benchmarks. See `opt/premise_error.md` + `opt/diagnostic_failure_2026-04-11-scalar-promote-float-gate-fix.md`.

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
| 8 (2026-04-06) | Phase 4 LICM | **no_change (infrastructure landed, wall-time unmoved)** | 3 functional commits: extract dominator/loop infra to `loops.go` (387dd88), `pass_licm.go` with IonMonkey-shaped invariance + `Int48Safe` gate (f601801), wire into Tier 2 pipeline after RangeAnalysis (9da7d4c). 17 constants hoisted in mandelbrot_iter (including ConstFloat 2/4 named in round 7's B3 analysis). Validator clean, `TestTieringManager_NestedCallSimple` passes. Mandelbrot -1.6% (0.387s→0.381s, target ≥35%); LuaJIT-row aggregate ~+0.3%. **B3's critical path is the surviving FMUL/FADD chain, not constant rematerialisation.** |
| 9 (2026-04-06) | Phase 4b invariant carry | **improved (mandelbrot -6.2%, nbody -12.2%, spectral -15.2%, matmul -12.7%)** | 2 functional commits: pre-header + invariant detection helpers (8618876), carry LICM-hoisted invariants in FPRs across loop body (de874ce). Extended `carried` map in regalloc to pin pre-header-defined float invariants in FPRs with budget (8 - 3 reserved). Lazy harvest of pre-header allocations instead of pre-allocation. Second-order effects dominated: nbody/spectral improved more than mandelbrot. Zero regressions across 22 benchmarks. |
| 10 (2026-04-06) | B3 items #2+#3: GPR counter + fused branch | **improved (fibonacci_iterative -7.4%, matmul -2.7%, math_intensive -3.5%)** | 3 functional commits: GPR-resident int counter (f3ab338), fused compare+branch (aba72f0), verification tests (61f960c). Int-only loops benefit most (fibonacci_iterative). Float-heavy loops show only 1-2% due to superscalar execution hiding instruction-level savings. Instruction count ≠ wall time — key lesson. |
| 12 (2026-04-06) | Phase 3: feedback-typed heap loads | **no_change (mechanism works, feedback unavailable)** | Guard insertion code landed (graph_builder.go reads FeedbackVector, inserts OpGuardType). IR-level test confirms TypeSpecialize cascade works. But feedback never populated: BaselineCompileThreshold=1 → interpreter never runs → FeedbackVector empty at Tier 2 compile time. Needs Tier 1 feedback collection. |
| 16 (2026-04-06) | Phase 8: Load Elimination + GuardType fix | **improved (nbody -26%, broad 17-49%)** | Block-local GetField CSE (84 lines, pass_load_elim.go) + TypeFloat guard fix (emit_call.go). Compound effects (register pressure, cache, DCE cleanup) dominated instruction-count predictions. Diagnostic (Task 0) confirmed feedback pipeline works end-to-end. |
| 17 (2026-04-06) | Phase 3 unblock: feedback fix + shape dedup | **improved (nbody -8.3%, table_field -23.5%, spectral -8.7%)** | Fixed GETFIELD/GETTABLE feedback recording in Go exit handlers (4 lines in tier1_handlers.go). Added shape guard dedup in emitter (emit_table.go). Phase 3's GuardType→TypeSpecialize pipeline now works end-to-end in production, not just tests. |
| 18 (2026-04-06) | Phase 9: LICM GetField hoisting + S2L forwarding | **no_change (infra landed, target unchanged)** | LICM GetField hoisting works for loops without same-object writes. nbody's inner loop has SetField on same objects as GetField targets, blocking hoisting. S2L forwarding subsumed by existing block-local CSE. |
| 20 (2026-04-07) | Phase 10: Native GetGlobal + LICM + self-call | **improved (nbody -49%, fib -90%)** | Wired native GetGlobal into Tier 2 dispatch (1 line), added OpGetGlobal to LICM whitelist, Tier 1 self-call BL optimization. ackermann +137% regression documented. |
| 21 (2026-04-07) | Phase 11: Production diagnostic + R(0) pin | **improved (nbody -8.1%, broad -8-23%)** | Scenario A confirmed (feedback works, arithmetic IS typed in production). R(0) pin to X22 + NaN-boxed closure cache in X21 gave 18/22 benchmarks broad improvement. Next: cross-block shape propagation. |
| 22 (2026-04-07) | Phase 12: Float param guards + GuardType CSE | **improved (nbody -10.3%, spectral -8.7%, table_field -17.3%)** | Float param guard speculation on mixed int/float params caused 100-170% regressions — fixed by excluding params used in int contexts. GuardType CSE + LICM whitelist (OpSqrt, OpGetTable). |
| 23 (2026-04-07) | Phase 9: Guard hoisting + shape propagation | **no_change (infra landed, A/B confirmed zero wall-time)** | Guard hoisting in LICM (OpGuardType whitelisted). Cross-block shape/table verification propagation (single-predecessor, dominator approach unsound at merge points). LICM alias fix (OpAppend/OpSetList). All instruction-count improvements absorbed by M4 superscalar (predicted branches cost ~0 IPC). |
| 32 (2026-04-11) | Phase 13: Loop scalar promotion (nbody bi/bj float pairs) | **no_change (pass wired, silent no-op due to type-gate bug)** | `LoopScalarPromotionPass` (264 LOC + 296 LOC tests, commit 56b19e7) wired after LICM in both RunTier2Pipeline and NewTier2Pipeline. All tests green, evaluator PASS. nbody 0.248→0.248 (0.0%, target -4%). Post-round re-run of TestR32_NbodyLoopCarried confirms all 9 candidate pairs still in the IR after the pipeline. **Root cause**: float gate `if instr.Type == TypeFloat` never triggers — production GetField is `:any` + trailing GuardType float. Unit tests hand-built TypeFloat and passed. **R33 plan**: one-line fix to inspect consumer GuardType (or feedback kind); whole pass infra ready to reuse. Cross-round pattern with R31: new Tier 2 passes need post-pipeline diagnostic re-runs, not synthetic IR tests. |
| 33 (2026-04-12) | Phase 13 (continued): Float gate consumer-GuardType scan | **data-premise-error (plan premise incomplete; no production commit)** | Coder wrote production-pipeline diagnostic test `pass_scalar_promote_production_test.go` (uses TieringManager + RunTier2Pipeline, not profileTier2Func — P3 compliant), applied the plan's exact float-gate fix, and observed bit-identical 9 unpromoted pairs / 0 float phis pre/post. Two upstream gates bail before classification: (1) exit-block-preds check at pass_scalar_promote.go:146-150 rejects the j-loop because j-loop exit `B4.Preds = [B10, B3]` and B10 is the i-loop preheader (outside j-body) — structural nested-loop property; (2) isInvariantObj rejects the second i-loop because `b := bodies[i]` is loop-variant by construction. Plan fix reverted; new test kept as observe-only (`t.Skip`) template. **Lesson: "this gate is wrong" does NOT imply "fixing this gate is sufficient."** Chain-verify all upstream gates when claiming a single-cause root cause. Phase 13 parked; tier2_float_loop category should sit for ≥3 rounds per ceiling rule. |

## Next Step

**Phase 7 complete** (round 14): Tier 1 feedback collection + ArrayFloat/ArrayBool fast paths landed. matmul -80.2%, spectral -54.0%, sieve -55.9%. Phase 3's guard insertion code is now unblocked (feedback flows Tier 1 → FeedbackVector → Tier 2 graph builder).

**Phase 5 partially resolved** (round 15): matmul now reaches Tier 2 via OSR (LoopDepth >= 2 gate). The original "matmul stuck at Tier 1" problem is solved.

**Phase 8 complete** (round 16): Load Elimination + TypeFloat guard fix. nbody -26% (0.796s → 0.590s), now 17.4x from LuaJIT (was 22.1x). Broad 17-49% improvement across GetField-heavy benchmarks.

**Phase 3 unblocked** (round 17): GETFIELD feedback recording fixed in Go exit handlers. Shape guard dedup in emitter. nbody -8.3% (0.590s → 0.541s), now 15.9x from LuaJIT. table_field_access -23.5%. The feedback→GuardType→TypeSpecialize pipeline now works in production (was dead code since round 12).

Remaining deferred phases:
- **Phase 6 (range analysis for float loops)**: extend overflow-check elimination to float arithmetic.
- **Phase 9 (DONE, round 23)**: Guard hoisting + cross-block shape propagation. Infra landed, zero wall-time impact (M4 superscalar). Single-predecessor propagation is the sound subset; dominator-based needs dataflow merge.
- **Phase 10 (future)**: Store-to-load forwarding — after SETFIELD(obj, field, val), subsequent GETFIELD(obj, field) returns val without memory access.
- **Phase 13 (IN PROGRESS, round 33)**: Fix LoopScalarPromotionPass float gate. The pass landed in R32 but its type gate checks `GetField.Type == TypeFloat` while production emits `GetField:any` + `GuardType float`. R33 should change the gate to walk consumers (or check feedback kind), add negative tests for multi-exit / wide-kill / non-float gates, and normalize `phi.Args[0]` in addition to `phi.Args[1]` after `replaceAllUses`. Expected ~3 promoted pairs in nbody B2 (`bi.vx/vy/vz`) and 3 in B6 (`b.x/y/z`), delivering ~-4% nbody wall-time. **New harness requirement: every new Tier 2 pass needs a post-pipeline diagnostic re-run test (not just synthetic IR).**
- **Long-term**: Unboxed float SSA (eliminate NaN-boxing in Tier 2 float paths entirely) or loop unrolling. Register moves (91 of 431 in nbody) and spill/reload (77 of 431) are the next architectural bottleneck.

## Risks / Failure Signals

- If pprof shows no dominant hotspot (flat profile), architecture is already optimal and the gap may be a deeper codegen issue (NaN-box overhead on every float op). Pivot to arch_refactor.
  - **Round 6 result: not flat — Pattern 1 (per-op box/unbox) accounts for >30% of mandelbrot's inner loop instructions and affects 5/5 benchmarks. Shallow escalation.**
- Abandon if 2 rounds of inner-loop tuning fail to move spectral_norm below 0.25s.

## Round 21 Plan (2026-04-07)

**Phase 11: nbody Production Typing Diagnostic + Fix**

Diagnostic found that without Tier 1 feedback, 29/31 arithmetic ops in nbody's inner loop are generic (`:any` type). The graph builder code for GuardType insertion after GetField exists and is correct (`graph_builder.go:669-676`), but the diagnostic test bypassed Tier 1 feedback collection.

Round 21 tasks:
1. Production-accurate diagnostic: compile advance() through TieringManager (with Tier 1 feedback) to determine if production codegen has typed or untyped arithmetic
2. Fix confirmed bottleneck: either feedback pipeline gap (Scenario B: -30-50%) or cross-block shape propagation (Scenario A: -10-15%)
3. Bonus: fix ackermann +137% regression from self-call proto comparison overhead

Also confirmed LICM GetField hoisting is WORKING: bi.x/y/z/mass hoisted to j-loop preheader. Intrinsic pass converts math.sqrt → OpSqrt. hasLoopCall=false for inner loop.

**Phase 12: Parameter Type Specialization + GuardType CSE**

Round 21 Scenario A confirmed: production codegen has 33 typed + 7 generic arithmetic ops.
The 7 generic ops all involve `v0 = LoadSlot slot[0]` (the `dt` parameter, type `:any`).
TypeSpecialize Phase 0 only detects int-like params (paired with ConstInt), not float-like.

Round 22 tasks:
1. Float parameter guard: extend insertParamGuards to detect params used with float-typed
   operands (run check after Phase 1 inference). Insert GuardType(TypeFloat) at entry block.
2. GuardType CSE: extend LoadElim to track (value_id, guard_type), eliminate redundant guards.
3. LICM: add OpSqrt + OpGetTable to canHoistOp whitelist.
4. Verify matmul production typing (graph builder already inserts GuardType for GetTable).
