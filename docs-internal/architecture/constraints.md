# Architecture Constraints & Notes

> **ANALYZE reads this every round.** Updated by Architecture Audit (every 2 rounds).
> Last full audit: Round 19 (2026-04-07)

## Tier Constraints

- **Tier 2 is net-negative for recursive functions** (Round 11): SSA construction + type guards + BLR ~15-20ns overhead > inlining gains. Recursive speedup needs Tier 1 BLR specialization or native recursive calling convention, NOT Tier 2 promotion.
- **8-FPR pool is a hard limit** (D4-D11): carried invariants + body temps share 8 registers. >5 carried invariants squeezes body temp space. Round 9's LICM-carry reserves up to 5, leaving 3 for body.
- **4-GPR pool** (X20-X23): int counter carry + loop bounds use 2-3, leaving 1-2 for body temps.

## Module Boundaries

- **`emit_table.go` 978 lines** ⚠ CRITICAL: 22 lines from 1000-line limit. Grew 41 lines in round 17 (shape guard dedup). Must split BEFORE any changes (extract `emit_table_native.go` for Tier 2 table paths).
- **`emit_dispatch.go` 969 lines** ⚠ CRITICAL: 31 lines from limit. Must split before any changes (extract `emit_branch.go` for fused compare+branch logic).
- **`graph_builder.go` 939 lines** ⚠: approaching limit. Round 12 added feedback-typed guards. Consider extracting `graph_builder_feedback.go`.
- **`regalloc.go` ↔ `emit_loop.go` coupling**: `carried` map concept spans both files. `regalloc.go` builds the map, `emit_loop.go` uses it for loop-exit boxing. Changes to one often require changes to the other.
- **24 source files lack test files** (up from 15 at Round 12 audit). Mostly Tier 1 handlers and emit files. Coverage is indirect via integration tests, but direct unit tests would catch regressions earlier.

## Pass Pipeline Order

Current (from `compileTier2`):
```
BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit
```

Ordering constraints:
- LICM AFTER RangeAnalysis (depends on `Int48Safe` flag)
- Inline AFTER TypeSpecialize (needs type info for inline decisions)
- ConstProp BEFORE DCE (fold first, then remove dead)
- RegAlloc is LAST before Emit (must see final CFG after all transforms)

## Known Performance Ceilings

- `recursive_call` (ceiling=2): needs fundamentally different mechanism (see Tier Constraints)
- `allocation_heavy` (binary_trees, object_creation): NEWTABLE is exit-resume, can't inline allocation; needs escape analysis + scalar replacement
- mandelbrot inner loop: FMUL/FADD dependency chain is the compute floor (~3ns/iter). All peephole opportunities exhausted (Rounds 7-10). Further gains need loop unrolling or software pipelining.
- **matmul Tier 2 via OSR** (Round 15, RESOLVED): Was stuck at Tier 1 (called once, threshold=2). Fixed by re-enabling OSR with LoopDepth >= 2 gate (commit 056607b). matmul now reaches Tier 2, mandelbrot -80%, spectral_norm -64%. Remaining gap: inner loop still uses generic GetTable dispatch — feedback-typed loads (Phase 3 infra from round 12) could further specialize once Tier 1 feedback collection covers GETTABLE mixed-array path.
- pprof is useless for JIT code (79% shows as opaque `runtime._ExternalCode`). ARM64 disasm via `tier2_float_profile_test.go` is the only reliable profiling method.

## Feedback System

- `FeedbackVector` records per-PC result types. Round 12 added `OpGuardType` insertion in graph builder after `GetTable`/`GetField` when feedback says monomorphic.
- **Round 14**: Tier 1 now collects feedback via ARM64 stubs in GETTABLE typed-array fast paths (Float→FBFloat, Int→FBInt, Bool→FBBool) and GETFIELD (runtime value type extraction). `BaselineFeedbackPtr` in ExecContext points to `proto.Feedback[0]`.
- **Coverage gap**: GETTABLE mixed-array path does NOT record feedback (line 279 of tier1_table.go has no stub). Mixed-array accesses returning tables or mixed values get FBUnobserved. This is acceptable for now — the important case is typed arrays returning floats/ints.
- **End-to-end pipeline verified** (TestFeedbackGuards_Integration): feedback → GuardType → TypeSpecialize → MulFloat/AddFloat cascade works.
- **Tier-up timing**: threshold=2 for pure-compute functions. First call at Tier 1 collects feedback. Second call at Tier 2 uses feedback. Functions called only once (matmul) need OSR to benefit.
- NOT covered: Call return type, ForLoop counter type.

## Feedback Pipeline

- **GETFIELD/GETTABLE feedback cold-start gap** (Round 17, **FIXED**): Go exit handlers now record type feedback via `proto.Feedback[pc].Result.Observe()`. Both `handleGetField` and `handleGetTable` in `tier1_handlers.go` record feedback on the slow path. End-to-end pipeline verified in production: feedback → GuardType → TypeSpecialize → MulFloat/AddFloat.
- **Shape guard deduplication** (Round 17): `emitContext.shapeVerified` tracks per-block shape-verified table SSA values. Subsequent GetField/SetField on same table with same shapeID skip type+nil+shape check (~11 insns saved). Invalidated by OpCall, OpSelf, OpSetTable, and block boundaries.
- **Remaining feedback gap**: GETTABLE mixed-array path does NOT record feedback (line 279 of tier1_table.go). Mixed-array accesses returning tables or mixed values get FBUnobserved. Acceptable — typed arrays are the important case.

## Table Access Overhead (Round 19 audit)

- **GetTable/SetTable per-access: ~35 ARM64 insns** (from diagnostic on sieve). Only 1-2 are the actual load/store. Overhead: table type check (10), nil/metatable check (3), key validation (6), array kind dispatch (8), bounds check + base load (4), dirty flag (3).
- **No dedup for GetTable/SetTable** — unlike GetField (which has `shapeVerified`), GetTable does full validation on every access. Multiple table accesses on the same table in the same block repeat all checks.
- **No cross-block table validation** — even for loop-invariant tables (e.g., sieve's `is_prime`), the full validation runs every iteration. V8 hoists CheckMaps to loop preheaders; GScript does not.
- **No array kind feedback** — Tier 1 does not record which array kind (Mixed/Int/Float/Bool) is used. The 4-way dispatch in GetTable/SetTable runs every time. Adding kind feedback would let Tier 2 specialize to a single path.
- **Diagnostic test pipeline mismatch**: `tier2_float_profile_test.go:profileTier2Func` uses a simplified pipeline (no Intrinsic, Inline, LoadElim, RangeAnalysis, LICM, no feedback). Diagnostics from this test do NOT reflect production codegen. Use `Diagnose()` or TieringManager for production-accurate data.

## Technical Debt

- `benchmarks/run_all.sh` has a bug: VM/JIT suite benchmarks silently fail (discovered round 12). Individual benchmark runs work.
- `tier2_float_profile_test.go:profileTier2Func` uses stale simplified pipeline — does not match `compileTier2()` (missing 6 passes + feedback). Diagnostic data is misleading for type-specialized analysis.

## Test Coverage Notes

- 85% test-to-source ratio (15,459 test lines / 18,104 source lines) — up from 81% at Round 17
- 24 source files have no corresponding test file (mostly Tier 1 handlers and emit files)
- Key gap: `loops.go` (loop infrastructure) has no dedicated tests — tested indirectly via `pass_licm_test.go`
