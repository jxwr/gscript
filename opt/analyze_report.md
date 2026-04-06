# Analyze Report — Round 18

> Date: 2026-04-06
> Cycle ID: 2026-04-06-licm-getfield

## Architecture Audit

Quick read (rounds_since_arch_audit=1). arch_check.sh:
- ⚠ emit_table.go 978 lines (22 from limit) — no changes planned this round
- ⚠ emit_dispatch.go 969 lines — no changes planned
- ⚠ graph_builder.go 939 lines — no changes planned
- 1 TODO/HACK marker. 25 source files without test files. constraints.md current (updated round 17).

No new issues. Plan touches only pass_licm.go and pass_load_elim.go (506 and 85 lines respectively — safe).

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (47.9x), ackermann (41.8x), mutual_recursion (47.3x) | 1.744s | **BLOCKED** (failures=2) |
| tier2_float_loop | nbody (15.9x), spectral_norm (6.0x), matmul (5.5x), sum_primes (2.0x), mandelbrot (1.10x) | 0.648s | No (failures=0) |
| field_access | sieve (8.5x), sort (5.4x), fannkuch (2.7x) | 0.153s | No (failures=0) |
| gofunction_overhead | method_dispatch (huge) | 0.097s | No (failures=0) |
| allocation_heavy | binary_trees (regression), object_creation (regression) | N/A (no LuaJIT baseline) | No |

## Blocked Categories
- `recursive_call` (category_failures=2): Tier 2 net-negative for recursive functions. Needs native recursive BLR or Tier 1 specialization.

## Active Initiatives
- `opt/initiatives/tier2-float-loops.md` — Phase 9 next (shape check / field load hoisting)
- `opt/initiatives/recursive-tier2-unlock.md` — paused (blocked)

## Selected Target

- **Category**: tier2_float_loop
- **Initiative**: opt/initiatives/tier2-float-loops.md (Phase 9)
- **Reason**: nbody has the largest absolute gap (0.507s) among non-blocked benchmarks. Active initiative with clear next phase. LICM for GetField builds on existing infrastructure (pass_licm.go from round 8-9). No architectural constraints blocking this work.
- **Benchmarks**: nbody (primary), spectral_norm/matmul (secondary)

## Prior Art Research

### Web Search Findings
Confirmed V8 TurboFan, LuaJIT, and SpiderMonkey all implement cross-block load elimination with store-to-load forwarding. V8's approach (ComputeLoopState kills written fields, survivors propagate) maps directly to GScript's LICM fixpoint.

### Reference Source Findings
- V8 `load-elimination.cc:1363-1465`: ComputeLoopState scans loop body, kills StoreField fields, propagates survivors as loop-invariant
- V8 `load-elimination.cc:786-817`: ReduceCheckMaps eliminates shape checks when Maps already known (cross-block)
- V8 `load-elimination.cc:1048`: ReduceStoreField records stored value for forwarding
- LuaJIT `lj_opt_mem.c:162`: fwd_ahload with ALIAS_MUST → store-to-load forwarding
- LuaJIT `lj_opt_loop.c:77-85`: cross-iteration forwarding via loop_unroll re-emission

### Knowledge Base Update
Written: `opt/knowledge/cross-block-load-elim.md` — comprehensive comparison of V8/LuaJIT/SpiderMonkey load elimination with GScript gap analysis and implementation guidance.

## Source Code Findings

### Files Read
- `pass_licm.go` (506 lines): `canHoistOp` whitelist does NOT include OpGetField. LICM currently only hoists constants, LoadSlot, and pure arithmetic. Extension point is clear: add OpGetField with field-write alias check.
- `pass_load_elim.go` (85 lines): Block-local only. OpSetField case does `delete(available, key)` but never records the stored value. 3-line fix enables store-to-load forwarding.
- `emit_table.go` (978 lines): `emitGetField` full shape check ~16 insns, deduped path ~5-6 insns. shapeVerified reset at block boundaries (emit_compile.go:527).
- `graph_builder.go:639-661`: GetField emits with TypeAny, inserts OpGuardType when feedback is available. Aux = constant pool index, Aux2 = shapeID<<32|fieldIndex.
- `regalloc.go`: carried map + LICM invariant carry already support pinning preheader-defined values in FPRs across loop body. Hoisted GetField values would automatically benefit.

### Diagnostic Data

**CAVEAT**: The profile test (tier2_float_profile_test.go) uses a simplified pipeline missing IntrinsicPass, InlinePass, LoadEliminationPass, RangeAnalysisPass, LICM. Production codegen is better.

From the simplified pipeline IR dump (advance() block B2 = inner j-loop body):
- 56 IR instructions: 19 GetField, 6 SetField, 19 float arith, 1 Call (math.sqrt before intrinsic), 2 GetGlobal + 1 GetTable
- Redundant loads: bi.mass×3, bj.mass×3 (handled by LoadElim in production)
- Static ARM64: ~1,768 insns total in B2+B3
- Estimated hot-path: ~1,250 insns/iter (simplified pipeline)
- Only 28/1,250 are float compute (2.2%) — rest is overhead

In production (full pipeline), TypeSpecialize eliminates 3-way type dispatch, IntrinsicPass converts math.sqrt to FSQRT, LoadElim deduplicates mass loads, ShapeGuardDedup reduces shape checks. Estimated production: ~250-350 insns/iter. Still ~3-5x more than LuaJIT's ~60 insns/iter.

### Actual Bottleneck (data-backed)

For the PRODUCTION pipeline, the remaining overhead in nbody's inner j-loop per iteration:
1. **4 loop-invariant GetField loads** (bi.x, bi.y, bi.z, bi.mass): ~40 insns + 8 dependent-load stalls (~32 cycles). These never change but are reloaded every iteration because LICM doesn't hoist GetField.
2. **3 GetField + 3 SetField on bi.vx/vy/vz** (read-modify-write per iteration): ~36 insns. Store-to-load forwarding doesn't help here (no subsequent read after write in same block).
3. **Full shape check per iteration** for bi and bj (first access each): ~34 insns.
4. **7 GetField + 3 SetField on bj** (different bj each iteration, unavoidable): ~60 insns.

Bottleneck #1 is addressable via LICM GetField hoisting. Bottleneck #3 partially addressable (bi's shape check moves to preheader with hoisted GetField).

## Plan Summary

Extend LICM to hoist loop-invariant GetField operations to loop preheaders, and add store-to-load forwarding to LoadElimination. For nbody's inner j-loop, this hoists 4 field loads (bi.x, bi.y, bi.z, bi.mass) out of the hot loop, eliminating ~40 instructions and 8 dependent-load stalls per iteration. Estimated nbody improvement: 8-10% (superscalar-discounted). Store-to-load forwarding is a 3-line change that enables value reuse after SetField. Risk is low: the LICM extension reuses existing infrastructure (fixpoint iteration, preheader creation, invariant carry), and the alias analysis is conservative (kills all GetField hoisting if any OpCall exists in the loop). Two new test files verify both optimizations independently.
