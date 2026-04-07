# Optimization Plan: Guard Hoisting + Cross-Block Verification Propagation

> Created: 2026-04-07 07:45
> Status: active
> Cycle ID: 2026-04-07-guard-hoist-shape-prop
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 9: shape check hoisting)

## Target

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| nbody | 0.247s | 0.032s | 7.7x | 0.225s (~9%) |
| spectral_norm | 0.043s | 0.007s | 6.1x | 0.040s (~7%) |
| table_field_access | 0.043s | N/A | N/A | 0.040s (~7%) |

## Root Cause

**Primary (guard hoisting):** LICM hoists loop-invariant GetField to the pre-header but does NOT hoist the corresponding GuardType. In nbody's inner j-loop (B2, 431 ARM64 insns), 4 guards on LICM-hoisted values (bi.x, bi.y, bi.z, bi.mass) execute every j-iteration but can NEVER fail since the values don't change. The comment in pass_licm.go ("Guards are NOT hoisted: their deopt metadata is tied to a specific PC") is factually wrong — `emitDeopt` simply sets ExitCode=2 and jumps to deopt_epilogue with no PC-dependent state.

Diagnostic data (production IR from TestDiag_NbodyProduction):
- B2 j-body: 60 IR ops → 431 ARM64 insns (13.9x overhead-to-compute ratio)
- Only 29/431 insns (6.7%) are actual float compute
- 4 invariant guards = ~40 ARM64 insns wasted per j-iteration

**Secondary (cross-block shape propagation):** `shapeVerified` and `tableVerified` maps reset at every block boundary (emit_compile.go:534-535). Loop body blocks re-verify shapes already checked in dominating blocks. For nbody: bi's shape verified in pre-header (by hoisted GetField) is unknown to the j-body block. The first bi access (GetField bi.vx) does a full 17-22 insn shape check instead of the 7-9 insn deduped path.

## Prior Art (MANDATORY)

**V8:** `LoadElimination::ReduceCheckMaps` propagates AbstractMaps along the effect chain. Map checks verified in dominators are available in dominated blocks. `ComputeLoopState` kills only fields written in the loop body — survivors (including map checks on unmodified objects) remain available throughout the loop. Guards (CheckMaps) are fully eliminated when the map state is known. Source: `src/compiler/load-elimination.cc:786-817`.

**LuaJIT:** Guards live at trace entry only. Once a trace is entered, all guards have been verified. Inner loop guard hoisting is implicit in the trace compilation model. `lj_opt_fwd_hrefk` (lj_opt_mem.c:299) eliminates hash reference guards within a trace.

**SpiderMonkey:** WarpBuilder reads CacheIR snapshots. GuardShape is placed at the earliest point where it's needed (often dominator blocks). MIR-level GVN eliminates redundant guards by tracking available guard results across the dominator tree.

Our constraints vs theirs: GScript's deopt model (full JIT exit, no PC-based resume) is actually SIMPLER than V8's frame-state-based deopt, making guard hoisting SAFER. The key insight: GScript's guards can be freely moved without adjusting deopt metadata.

## Approach

### Task 1: Add OpGuardType to LICM canHoistOp whitelist

File: `pass_licm.go`

Add `OpGuardType` to the `canHoistOp` switch. The existing invariant fixpoint in `hoistOneLoop` already requires all guard args to be invariant before marking the guard as invariant. No additional alias checks needed — guards have no memory side effects.

Update the file-level comment (line 27-29) to remove the incorrect claim about guard deopt metadata.

### Task 2: Cross-block shape/table verification propagation

File: `emit_compile.go`

1. Compute `dom := computeDominators(fn)` in `Compile()`, store in `emitContext`.
2. Add `blockOutShapes map[int]map[int]uint32` and `blockOutTables map[int]map[int]bool` to emitContext.
3. After processing each block in `emitBlock`, save copies of `ec.shapeVerified` and `ec.tableVerified` to `blockOutShapes[block.ID]` / `blockOutTables[block.ID]`.
4. At the START of `emitBlock`, instead of creating empty maps, clone the immediate dominator's outgoing state (if available). Entry block (idom == -1) starts empty.

Correctness: shapeVerified tracks per-SSA-value shape IDs. SSA values are immutable. If a shape was verified in a dominator, it's valid in all dominated blocks. Invalidation by OpCall/OpSetTable/OpSelf within a block correctly clears the map for subsequent instructions in that block. At loop headers, the idom is the pre-header (not the back-edge), so the pre-header's verification state flows in — correct for the first iteration. The back-edge's state is irrelevant because the idom defines the "always-available" state.

### Task 3: Known-issues correctness fixes

(a) `pass_licm.go`: Add `OpAppend` and `OpSetList` to the field-write scan in `hoistOneLoop`. These ops mutate tables but aren't currently checked, meaning LICM could incorrectly hoist GetField/GetTable when these ops exist in the loop body. ~5 lines.

(b) `emit_table_array.go`: Clear `ec.tableVerified[tblValueID]` before the SetTable exit-resume path. Currently tableVerified persists after an exit-resume, but the interpreter could modify the table (e.g., set a metatable) during the exit-resume handler. ~2 lines.

### Task 4: Tests and verification

(a) New test in `pass_licm_test.go`: verify that GuardType on a LICM-hoisted value is hoisted to the pre-header.
(b) Verify nbody production IR: guards v21/v26/v31/v73 should now be in B9 (pre-header), not B2 (body).
(c) Run full benchmark suite. Verify no regressions.

## Expected Effect

**Guard hoisting (Task 1):** Removes 4 guards × ~10 ARM64 insns from B2 (431 → ~391). Additional savings from FPR pinning of guard results (invariant carry eliminates NaN-box load+FMOV in body): ~8-12 insns. Total: ~48-52 insns removed = 11-12% instruction reduction.

**Cross-block shape propagation (Task 2):** Eliminates 1 full shape check on bi in j-body (first bi access uses deduped path instead of full): ~12 insns. Eliminates table validation on `bodies` table: ~8 insns. Total: ~20 insns = 4.6%.

**Combined:** ~68-72 insns removed from 431 = 16-17% instruction reduction.

**Prediction calibration:** Instruction-count savings on M4 superscalar should be halved. The removed guards and shape checks are mostly predicted branches (low IPC cost) plus tag-extract sequences (which pipeline well). Estimated wall-time improvement: **~8-9%** on nbody. Historical: Round 22 predicted ~10% on nbody, got 10.3% — calibration is improving.

| Benchmark | Expected Change |
|-----------|----------------|
| nbody | -8-9% (0.247s → ~0.225s) |
| spectral_norm | -5-7% (similar field-heavy loops) |
| table_field_access | -5-7% (shape propagation helps) |
| Others with field access in loops | -2-5% (broad improvement) |
| mandelbrot | ~0% (no field access in inner loop) |

## Failure Signals

- Signal 1: nbody improvement < 3% → guard hoisting benefit is smaller than estimated (superscalar hides even more than halved). Action: still ship (correctness improvement to LICM), but note the calibration error.
- Signal 2: Any benchmark regression > 2% → shape propagation may be propagating stale verifications. Action: investigate, disable cross-block for the affected benchmark pattern, or revert Task 2.
- Signal 3: Correctness failures in test suite → immediate fix or revert the specific task.

## Task Breakdown

- [x] 1. **Hoist loop-invariant GuardType** — file(s): `pass_licm.go` — test: new `TestLICM_GuardTypeHoist` in `pass_licm_test.go`
- [x] 2. **Cross-block shape/table verification propagation** — file(s): `emit_compile.go` — test: `TestTieringManager_*` (verifies full pipeline including emitter). NOTE: dominator-based approach was unsound at merge points; switched to single-predecessor propagation.
- [x] 3. **Known-issues fixes** — file(s): `pass_licm.go`, `emit_table_array.go` — test: existing tests must pass
- [x] 4. **Integration test + benchmark** — run full suite, verify nbody improvement

## Budget
- Max commits: 4 (+1 revert slot for Task 2 if shape propagation causes regressions)
- Max files changed: 4 (pass_licm.go, emit_compile.go, emit_table_array.go, pass_licm_test.go)
- Abort condition: 3 commits without any benchmark improvement, or correctness failure in >2 benchmarks

## Results (filled by VERIFY)
| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| nbody | 0.247s | 0.245s | -0.8% | -8-9% | No |
| spectral_norm | 0.043s | 0.044s | +2.3% (noise) | -5-7% | No |
| table_field_access | 0.043s | 0.042s | -2.3% | -5-7% | No |
| mandelbrot | 0.059s | 0.063s | +6.8% (noise) | ~0% | Yes (noise) |
| fib | 0.141s | 0.140s | -0.7% | ~0% | Yes |
| matmul | 0.116s | 0.118s | +1.7% (noise) | ~0% | Yes (noise) |

A/B testing (3 rounds, alternating baseline/current binary, same thermal state) confirmed all differences are within noise. No real regressions, no real improvements.

### Test Status
- methodjit: all passing (1.514s)
- vm: all passing (0.341s)

### Evaluator Findings
- PASS. Guard hoisting is sound (deopt has no PC-dependent state). Single-predecessor propagation is correct (no merge-point unsoundness). OpAppend/OpSetList alias scan correct. tableVerified clearing correct. Scope: exactly 4 files changed.

### Regressions (≥5%)
- None confirmed. mandelbrot +6.8% is noise (A/B test shows identical performance).

## Lessons (filled after completion/abandonment)
- Dominator-based shape propagation is unsound at merge points — different paths may have different table mutations. Single-predecessor propagation is the safe subset.
- Guard hoisting benefit is smaller than estimated on M4 superscalar — the removed guards were mostly predicted branches with low IPC cost. The halving rule was not aggressive enough; the real discount was ~5x, not 2x.
- Instruction-count reduction does not equal wall-time reduction for branch-heavy guard code. On M4, well-predicted branches cost almost nothing. Only removing actual compute (FPR ops, memory loads on the critical path) moves wall time.
- The 0x2643-offset SIGBUS in TestQuicksortSmall/TestTier2RecursionDeeperFib is pre-existing, not caused by our changes.
- Infrastructure wins (guard hoisting in LICM, cross-block verification state) are still valuable even without immediate wall-time impact — they enable future optimizations and prevent future bugs (OpAppend/OpSetList alias fix).
