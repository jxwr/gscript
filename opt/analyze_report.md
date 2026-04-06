# Analyze Report — Round 19

> Date: 2026-04-07
> Cycle ID: 2026-04-07-table-kind-specialize

## Architecture Audit

**Full audit** (rounds_since_arch_audit=2). Key findings:

- **emit_table.go 978 lines** ⚠ CRITICAL (unchanged since R17). MUST split this round — plan includes as Task 0.
- **emit_dispatch.go 969 lines** ⚠ CRITICAL (unchanged). No changes planned this round.
- **graph_builder.go 939 lines** ⚠ (unchanged). Minor change planned (kind feedback propagation, ~10 lines).
- **pass_licm.go 546 lines** — grew +40 from R17 (GetField hoisting in R18). Healthy.
- **Total source: 18,104 lines** (up from 17,450 at R17 audit). Test ratio: 85% (up from 81%).
- **Diagnose() pipeline synced** (R18 commit 92e08d1). But `tier2_float_profile_test.go:profileTier2Func` still uses simplified pipeline (only TypeSpec→ConstProp→DCE, no Intrinsic/Inline/LoadElim/RangeAnalysis/LICM, no feedback). Diagnostic data from this test is misleading for type-specialized analysis. Added to constraints.md.
- **New finding: table access overhead** — GetTable/SetTable emit 35 insns per access (1 actual load/store). No dedup exists (unlike GetField's shapeVerified). No array kind feedback. Added to constraints.md.
- **LICM GetField hoisting** (R18 infra): works correctly for loops without calls or same-field writes. Infrastructure is clean.
- **Feedback pipeline**: end-to-end working. GETFIELD records on Tier 1 fast path (line 142 tier1_table.go). GETTABLE records on Tier 1 typed-array fast paths.

Updated: `docs-internal/architecture/constraints.md` with Table Access Overhead section and diagnostic test mismatch note.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (56.8x), ackermann (41.7x), mutual_recursion (45.0x) | 1.770s | **BLOCKED** (failures=2) |
| tier2_float_loop | nbody (16.3x), spectral_norm (5.6x), matmul (6.0x), mandelbrot (1.2x), sum_primes (2.0x) | 0.660s | No (failures=1) |
| field_access | sieve (7.5x), sort (4.6x), fannkuch (2.7x) | 0.144s | No (failures=0) |
| gofunction_overhead | method_dispatch (regression) | 0.100s | No (failures=0) |
| allocation_heavy | binary_trees, object_creation (regressions) | N/A | No (failures=0) |

## Blocked Categories
- `recursive_call` (category_failures=2): Tier 2 net-negative for recursive functions. Needs native recursive BLR or Tier 1 specialization.

## Active Initiatives
- `opt/initiatives/tier2-float-loops.md` — paused (R18 no_change, failures=1)
- `opt/initiatives/recursive-tier2-unlock.md` — paused (blocked)

## Selected Target

- **Category**: field_access
- **Initiative**: standalone
- **Reason**: (1) field_access has 0 failures, safe to try. (2) sieve at 7.5x has clear, data-backed bottleneck in table access overhead. (3) Table access optimization benefits ALL table-heavy benchmarks (sieve, matmul, spectral_norm, fannkuch, nbody). (4) Previous rounds (13-14) showed field_access optimizations can yield large gains (matmul -80%, sieve -56%). (5) tier2_float_loop has failures=1 — one more no_change risks ceiling.
- **Benchmarks**: sieve (primary), matmul/spectral_norm/fannkuch (secondary)

## Prior Art Research

### Web Search Findings
V8, LuaJIT, and SpiderMonkey all specialize table/array access based on observed element kind. V8 uses CheckMaps + element-kind-specific LoadElement/StoreElement. LuaJIT records exact table layout during trace recording and emits direct AREF/HREF ops. SpiderMonkey Warp reads CacheIR stubs for kind-specific emit.

### Reference Source Findings
- V8 `load-elimination.cc:786`: ReduceCheckMaps eliminates redundant shape/kind checks when already known
- V8 `simplified-lowering.cc`: Element kind from Maps drives LoadElement/StoreElement lowering
- LuaJIT `lj_record.c`: AREF instruction specializes array-part access, HREF for hash part. No runtime dispatch.
- SpiderMonkey `WarpBuilder.cpp`: GuardShape + kind-specific LoadElement

### Knowledge Base Update
Research agent writing `opt/knowledge/table-access-specialization.md` (in progress).

## Source Code Findings

### Files Read
- `emit_table.go` (978 lines): Full GetTable/SetTable emit with 4-way kind dispatch. `emitGetTableNative` and `emitSetTableNative` handle all array kinds (Mixed/Int/Float/Bool). No dedup mechanism (unlike GetField's shapeVerified).
- `tier1_table.go` (774 lines): Tier 1 GETFIELD fast path with feedback recording (line 142). GETTABLE fast paths for typed arrays with feedback. No array KIND feedback — only value TYPE feedback.
- `graph_builder.go` (939 lines): GetTable/GetField with GuardType insertion from feedback. No kind information propagated.
- `regalloc.go` (684 lines): LICM invariant carry (FPR-only). Preheader detection and pinned invariants.
- `emit_compile.go` (585 lines): Compile pipeline, shapeVerified init, loop info computation.
- `pass_licm.go` (546 lines): LICM with GetField hoisting (R18). canHoistOp whitelist.

### Diagnostic Data

**Sieve inner marking loop (B7+B8, while-style loop):**
- IR: `Le v33, v34 → Branch → SetTable v77, v33, v37 → AddInt v33, v78 → Jump`
- ARM64: ~62 insns per iteration on fast path
- SetTable breakdown: 35 insns (1 actual store, 32 overhead, 2 branching)
- AddInt: 11 insns (includes NaN-box unbox + overflow check)
- Le+Branch: ~8 insns (comparison + NaN-box bool + branch)
- Note: diagnostic from simplified pipeline (no LICM/carry). Production may be better for AddInt/Le.

**nbody advance() inner loop:**
- 2,923 total ARM64 insns (simplified pipeline, no feedback → no type specialization)
- Float compute ops: 35 (actual fmul/fadd/fsub/fdiv/fsqrt)
- Type check sequences: 101 (would be eliminated in production with feedback)
- Frame spills: 730
- Deopt stubs: 39 × ~16 insns = 624 (21% of binary, dead code on fast path)

**CAVEAT**: Both diagnostics are from `profileTier2Func` which uses a simplified pipeline without feedback, Intrinsic, Inline, LoadElim, RangeAnalysis, or LICM. Production codegen via TieringManager is significantly better — type-specialized arithmetic, LICM-hoisted invariants, shape guard dedup, etc. The sieve table access overhead (35 insns per SetTable) is accurate regardless of pipeline because it's emitter-level structural overhead, not pass-dependent.

### Actual Bottleneck (data-backed)

**Sieve**: The inner marking loop's SetTable emits 35 ARM64 instructions per store. The table `is_prime` is loop-invariant (defined before the outer loop, never reassigned). The array kind is always ArrayBool (set during init loop, never changes). The table validation (type check, ptr extract, nil check, metatable check = 13 insns) and kind dispatch (8 insns) are redundant per iteration. Eliminating them would reduce SetTable to ~14 insns (kind guard 3 + bounds 4 + access 3 + dirty 3 + branch 1).

## Plan Summary

Split emit_table.go (mandatory, 978 lines), then add two complementary optimizations: (A) table validation dedup within blocks (`tableVerified`, mirrors shapeVerified for GetField), and (B) array kind feedback from Tier 1 → kind-specialized emit at Tier 2 (skip 4-way dispatch cascade). Together these eliminate ~15 instructions per GetTable/SetTable access. Expected sieve improvement: 20-25% wall-time. Risk is low: both mechanisms mirror existing patterns (shapeVerified, feedback pipeline). The emit_table.go split is the prerequisite and largest task.
