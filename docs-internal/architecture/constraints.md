# Architecture Constraints & Notes

> **ANALYZE reads this every round.** Updated by Architecture Audit (every 2 rounds).
> Last full audit: Round 15 (2026-04-06)

## Tier Constraints

- **Tier 2 is net-negative for recursive functions** (Round 11): SSA construction + type guards + BLR ~15-20ns overhead > inlining gains. Recursive speedup needs Tier 1 BLR specialization or native recursive calling convention, NOT Tier 2 promotion.
- **8-FPR pool is a hard limit** (D4-D11): carried invariants + body temps share 8 registers. >5 carried invariants squeezes body temp space. Round 9's LICM-carry reserves up to 5, leaving 3 for body.
- **4-GPR pool** (X20-X23): int counter carry + loop bounds use 2-3, leaving 1-2 for body temps.

## Module Boundaries

- **`emit_dispatch.go` 961 lines** ⚠: approaching 1000-line limit. Next change must split first (extract `emit_branch.go` for fused compare+branch logic).
- **`graph_builder.go` 939 lines** ⚠: approaching limit. Round 12 added feedback-typed guards. Consider extracting `graph_builder_feedback.go`.
- **`emit_table.go` 937 lines** ⚠ (NEW): grew significantly in rounds 13-14 with ArrayFloat/ArrayBool fast paths + raw-int key bypass + const-value bypass. Consider extracting `emit_table_native.go` for Tier 2 table paths.
- **`regalloc.go` ↔ `emit_loop.go` coupling**: `carried` map concept spans both files. `regalloc.go` builds the map, `emit_loop.go` uses it for loop-exit boxing. Changes to one often require changes to the other.
- **25 source files lack test files** (up from 15 at Round 12 audit). Mostly Tier 1 handlers and emit files. Coverage is indirect via integration tests, but direct unit tests would catch regressions earlier.

## Pass Pipeline Order

Current (from `compileTier2`):
```
BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit
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
- **matmul stuck at Tier 1** (Round 15 finding): matmul called once, threshold=2 → never promotes. OSR disabled. Even at Tier 2 without feedback, inner loop is untyped (GetTable→any→generic dispatch). Needs BOTH OSR re-enable AND feedback-typed loads.
- pprof is useless for JIT code (79% shows as opaque `runtime._ExternalCode`). ARM64 disasm via `tier2_float_profile_test.go` is the only reliable profiling method.

## Feedback System

- `FeedbackVector` records per-PC result types. Round 12 added `OpGuardType` insertion in graph builder after `GetTable`/`GetField` when feedback says monomorphic.
- **Round 14**: Tier 1 now collects feedback via ARM64 stubs in GETTABLE typed-array fast paths (Float→FBFloat, Int→FBInt, Bool→FBBool) and GETFIELD (runtime value type extraction). `BaselineFeedbackPtr` in ExecContext points to `proto.Feedback[0]`.
- **Coverage gap**: GETTABLE mixed-array path does NOT record feedback (line 279 of tier1_table.go has no stub). Mixed-array accesses returning tables or mixed values get FBUnobserved. This is acceptable for now — the important case is typed arrays returning floats/ints.
- **End-to-end pipeline verified** (TestFeedbackGuards_Integration): feedback → GuardType → TypeSpecialize → MulFloat/AddFloat cascade works.
- **Tier-up timing**: threshold=2 for pure-compute functions. First call at Tier 1 collects feedback. Second call at Tier 2 uses feedback. Functions called only once (matmul) need OSR to benefit.
- NOT covered: Call return type, ForLoop counter type.

## Technical Debt

- `benchmarks/run_all.sh` has a bug: VM/JIT suite benchmarks silently fail (discovered round 12). Individual benchmark runs work.

## Test Coverage Notes

- 81% test-to-source ratio (14207 test lines / 17450 source lines)
- 15 source files have no corresponding test file (mostly Tier 1 handlers and IR definition files)
- Key gap: `loops.go` (loop infrastructure) has no dedicated tests — tested indirectly via `pass_licm_test.go`
