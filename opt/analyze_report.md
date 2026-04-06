# Analyze Report — Round 15

> Date: 2026-04-06
> Cycle ID: 2026-04-06-osr-feedback-matmul

## Architecture Audit

**Full audit (rounds_since_arch_audit=2).**

### arch_check.sh findings
- **3 files over 800 lines**: `emit_dispatch.go` (961⚠), `graph_builder.go` (939⚠), `emit_table.go` (937⚠ NEW)
- `emit_table.go` grew significantly in rounds 13-14 (ArrayFloat/ArrayBool fast paths + raw-int key bypass + const-value bypass). Added to constraints.md.
- Source: 43 files, 17878 lines (+428 lines since Round 12 audit)
- Test ratio: 81% (14523/17878). 25 source files without tests (up from 15 at Round 12).
- Technical debt markers: 1 (unchanged)

### Key source file changes since Round 12
- **regalloc.go** (684 lines): GPR pool is actually 5 registers (X20-X23 + X28), not 4 as documented. X28 was freed when trace JIT was removed. Fixed in overview.md.
- **func_profile.go**: All tiering thresholds are now ≥2. The overview.md claimed threshold=1 for pure-compute; corrected to threshold=2.
- **tier1_table.go** (774 lines): Grew with feedback stubs (emitBaselineFeedbackResult, emitBaselineFeedbackResultFromValue). GETTABLE typed-array paths record FBFloat/FBInt/FBBool. GETFIELD records via value-type extraction. Mixed-array path has NO feedback stub.
- **tiering_manager.go** (773 lines): OSR remains disabled (lines 151-155 commented). Feedback initialization added at Tier 1 compile time (line 148-149).

### New infrastructure since Round 12
- Tier 1 feedback collection (BaselineFeedbackPtr in ExecContext)
- emit_table.go: native ArrayFloat/ArrayBool fast paths for Tier 2 (GetTable/SetTable)
- emit_table.go: raw-int key bypass + const-value bypass for Tier 2

### Opportunities spotted
- **OSR re-enable**: The reason for disabling OSR (mandelbrot Tier 2 regression) no longer applies — mandelbrot reaches Tier 2 via call count now. Re-enabling would unlock matmul.
- **Mixed-array feedback**: Adding `emitBaselineFeedbackResultFromValue` to the mixed-array path in tier1_table.go would improve feedback coverage.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (52x), ackermann (42x), mutual_recursion (49x) | ~1.83s | YES (failures=2) |
| tier2_float_loop | mandelbrot (6.6x), matmul (9.4x), spectral (19.5x), nbody (17.7x) | ~1.24s | No (failures=1) |
| field_access | sieve (7.5x), sort (5.0x), fannkuch (3.9x) | ~0.17s | No (failures=0) |
| gofunction_overhead | method_dispatch (huge) | ~0.10s | No (failures=0) |
| allocation_heavy | binary_trees (1.87x regr), object_creation (1.21x regr) | JIT regressions | No (failures=0) |

## Blocked Categories
- `recursive_call` (category_failures=2): Tier 2 is net-negative for recursive functions. Needs Tier 1 BLR specialization or native recursive calling convention.

## Active Initiatives
- `recursive-tier2-unlock.md` — paused (blocked by ceiling rule)
- `tier2-float-loops.md` — paused (Phase 7 partially done in rounds 13-14)
- `tier2-float-loops-b3-analysis.md` — complete (reference document)

## Selected Target
- **Category**: field_access
- **Initiative**: standalone
- **Reason**: matmul is 9.4x slower than LuaJIT and stuck at Tier 1 forever because it's called once and OSR is disabled. All infrastructure for the fix is already in place: Tier 1 feedback stubs (Round 14), GuardType insertion in graph builder (Round 12), OSR mechanism (existing, just disabled). This is a configuration change, not an architecture change. No flagged-⚠ files are touched. Zero risk of regression to other benchmarks (LoopDepth >= 2 gate targets only matmul in current suite).
- **Benchmarks**: matmul (primary), regression check on all others

## Prior Art Research

### Web Search Findings
No web search needed — OSR is a well-understood technique. V8, SpiderMonkey, and JSC all use OSR as the primary mechanism for promoting single-call long-running functions. GScript already has the full OSR implementation; it's just disabled.

### Reference Source Findings
- V8's `Runtime_CompileOptimizedOSR` — triggers on back-edge interrupt, compiles at loop header
- LuaJIT's hot-loop detection (default `hotloop=56`) — trace recording triggers on back-edge count, analogous to our OSR counter
- GScript's OSR: `tiering_manager.go:211-245`, `tier1_control.go:172-179` (FORLOOP counter decrement), `tier1_manager.go:184-187` (counter setup). All working code, just needs uncommenting.

### Knowledge Base Update
No new knowledge base entries. Existing entries `matmul-tier-up-gap.md` and `feedback-typed-loads.md` already document the problem and pipeline.

## Source Code Findings

### Files Read
- `tiering_manager.go:140-245` — OSR mechanism, shouldPromoteTier2, handleOSR
- `func_profile.go:104-142` — tiering thresholds (all ≥2 now)
- `tier1_table.go:220-343` — GETTABLE feedback stubs (Float/Int/Bool but NOT Mixed)
- `tier1_table.go:661-754` — emitBaselineFeedbackResult + emitBaselineFeedbackResultFromValue
- `graph_builder.go:614-661` — feedback reading + GuardType insertion for GetTable/GetField
- `graph_builder.go:925-937` — feedbackToIRType mapping
- `regalloc.go:0-200` — GPR/FPR pools, invariant carry
- `osr_test.go:81` — existing OSR test uses SetOSRCounter(proto, 500)
- `benchmarks/suite/matmul.gs` — triple-nested loop, float-array table access

### Diagnostic Data
Fresh benchmark run (CLI, commit 823b444):

| Benchmark | JIT | LuaJIT | Ratio |
|-----------|-----|--------|-------|
| matmul | 0.207s | 0.022s | 9.4x |
| mandelbrot | 0.383s | 0.058s | 6.6x |
| spectral_norm | 0.156s | 0.008s | 19.5x |
| nbody | 0.620s | 0.035s | 17.7x |
| sieve | 0.082s | 0.011s | 7.5x |
| fannkuch | 0.078s | 0.020s | 3.9x |
| sort | 0.055s | 0.011s | 5.0x |

### Actual Bottleneck (data-backed)
matmul's inner loop (`sum = sum + ai[k] * b[k][j]`) runs 27M iterations (300³) entirely at Tier 1. Generic MUL/ADD dispatch costs ~10 instructions each (type check, unbox, compute, re-box). With feedback-typed Tier 2: FMUL + FADD = 2 instructions. Saving ~18 insns/iter × 27M iters × ~0.3ns/insn ≈ 146ms on a 207ms baseline. Halved for superscalar: ~73ms savings → target 0.06-0.12s.

## Plan Summary
Re-enable OSR with a LoopDepth >= 2 safety gate in `tiering_manager.go`. This is a 3-line change that unlocks matmul for Tier 2 promotion mid-execution. Combined with Round 12's feedback-typed GuardType insertion and Round 14's Tier 1 feedback stubs, the Tier 2 inner loop will have typed float operations (MulFloat/AddFloat) instead of generic dispatch. Expected improvement: matmul 0.207s → 0.06-0.12s. Key risk: OSR restart correctness (mitigated by existing OSR tests). No other benchmarks affected.
