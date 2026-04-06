# Analyze Report — Round 13 (2026-04-06)

## Architecture Audit
Quick read: `rounds_since_arch_audit=0` — full audit was done last round.
- `emit_dispatch.go` (961 ⚠) and `graph_builder.go` (939 ⚠) still approaching 1000-line limit
- No new issues beyond what's documented in `constraints.md`
- arch_check.sh: 1 TODO marker, 25 test-gap files (mostly emit/tier1 handlers — pre-existing)

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (52.8x), ackermann (42.8x), mutual_recursion (46.8x) | 1.83s | **YES** (failures=2) |
| tier2_float_loop | matmul (37.7x), spectral_norm (42.3x), nbody (18.7x), mandelbrot (6.6x) | 2.07s | No (failures=1) |
| field_access | sieve (21.7x), fannkuch (3.5x), sort (4.8x) | 0.32s | No (failures=0) |
| allocation_heavy | binary_trees (0.81x regression), object_creation (0.84x regression) | N/A | No (failures=0) |
| gofunction_overhead | method_dispatch (huge ratio) | ~0.1s | No (failures=0) |

Regressions (JIT slower than VM):
- binary_trees: 2.079s JIT vs 1.688s VM (NEWTABLE exit-resume overhead)
- coroutine_bench: 19.112s vs 18.148s (Go runtime goroutine/channel ops)
- object_creation: 0.774s vs 0.653s (NEWTABLE exit-resume)

## Blocked Categories
- `recursive_call` (ceiling=2): Tier 2 SSA overhead > inlining gains for recursive functions

## Active Initiatives
- `tier2-float-loops.md`: active but at inflection — B3 peephole exhausted, Phase 3 blocked on feedback availability
- `recursive-tier2-unlock.md`: paused, blocked by ceiling rule
- `tier2-float-loops-b3-analysis.md`: complete (diagnostic reference)

## Selected Target
- **Category**: field_access
- **Initiative**: standalone (first round in this category)
- **Reason**: Fresh category (0 failures). Diagnostic revealed critical root cause: sieve's boolean array falls to exit-resume on every table op because the emitter only handles ArrayMixed(0) and ArrayInt(1). 6–7M exit-resume round-trips per sieve call. Fix is bounded (emit_table.go only) and binary (either exits or doesn't).
- **Benchmarks**: sieve (primary), potentially table_array_access, fannkuch

**Why not tier2_float_loop?** Initiative is at inflection point (B3 peephole exhausted, Phase 3 blocked). Category has 1 failure. Further gains require heavy architectural work (deopt frame descriptors / Tier 1 feedback collection). Following round 12 review recommendation to diversify.

**Why not allocation_heavy?** Also recommended by round 12 review. Valid target (2.87s untouched) but: (a) no LuaJIT baseline for comparison, (b) fix requires escape analysis (deep architectural change), (c) sieve's ArrayBool fix is more bounded and has a clear LuaJIT comparison.

**Why sieve over matmul?** Matmul also has a critical issue (stuck at Tier 1, see findings below), but fixing it touches tiering policy (risky, round 4 lesson) AND even at Tier 2 matmul's arithmetic stays generic (untyped GetTable results). Sieve's fix is pure codegen — no policy changes, no cross-cutting concerns.

## Prior Art Research

### Web Search Findings
- **LuaJIT sieve optimization**: LuaJIT achieves ~4 ARM64 instructions per table access via SCEV-based invariant bounds check hoisting (`lj_record.c:1396-1434`), copy-substitution loop optimization (`lj_opt_loop.c:22-90`), and fused array addressing (`lj_asm_arm64.h:165-198`). These are trace JIT techniques not directly applicable to method JIT, but the target (4 insns/access) is the ceiling.
- **V8 TurboFan element kind specialization**: TurboFan specializes loads/stores based on JSArray element kind (PACKED_SMI, PACKED_DOUBLE). Uses `CheckMaps` guards + unboxed representations. GScript's ArrayKind system is directly analogous.

### Reference Source Findings
- **LuaJIT `lj_opt_fold.c:1886-1951`**: ABC fold rules eliminate redundant bounds checks. `abc_invar` specifically folds iteration-dependent bounds checks when SCEV analysis proves the loop variable is bounded by array length.
- **LuaJIT `lj_asm_arm64.h:1037-1043`**: Colocated array optimization — small tables allocated with TNEW have array part colocated with header, eliminating pointer load.

### Knowledge Base Update
- Created `opt/knowledge/array-kind-table-access.md` — documents ArrayBool/ArrayFloat encoding, offsets, and the emitter dispatch gap
- Created `opt/knowledge/matmul-tier-up-gap.md` — documents matmul stuck at Tier 1 (for future round)

## Source Code Findings

### Files Read
- `emit_table.go:369-666` — emitGetTableNative and emitSetTableNative. Both have identical dispatch: Mixed(0) fall-through, Int(1) branch, else deopt. ArrayBool(3) and ArrayFloat(2) always deopt.
- `ir_ops.go:1-222` — OpGetTable and OpSetTable are the only table access ops in IR. No typed variants.
- `pass_licm.go:473-492` — canHoistOp whitelist does NOT include OpGetTable/OpSetTable (correctly: they have side effects). Future optimization: decompose into guard + raw access, let LICM hoist guard.
- `graph_builder.go:614-637` — GETTABLE/SETTABLE → OpGetTable/OpSetTable. No arrayKind-aware lowering.
- `jit/value_layout.go:57-77` — ArrayKind constants: AKMixed=0, AKInt=1, AKFloat=2, AKBool=3. Missing: `TableOffFloatArrayLen`, `TableOffBoolArrayLen`.
- `runtime/table.go:8-46` — ArrayBool encoding: `[]byte`, 0=nil, 1=false, 2=true. ArrayFloat: `[]float64`, raw IEEE 754 (= NaN-boxed Value directly).

### Diagnostic Data

**Sieve tiering**: Matches first clause in shouldPromoteTier2 (HasLoop + ArithCount >= 1 + no calls). Threshold=2. With REPS=3: call 1 at Tier 1, calls 2-3 at Tier 2. Sieve IS at Tier 2 for 2/3 of execution.

**ArrayBool exit-resume storm**: Sieve's `is_prime` table stores only booleans → Go runtime promotes to ArrayBool(3). The emitter dispatch hits `CBNZ X2, deoptLabel` for ArrayBool(3). Result: ~6–7M exit-resume round-trips per sieve(1M) call.

**Matmul stuck at Tier 1**: matmul function called only once, threshold=2, OSR disabled. The hot O(n³) loop runs entirely at Tier 1 baseline JIT. Even at Tier 2, inner loop arithmetic would be generic (GetTable results typed `:any`). Documented in knowledge base for future round.

### Actual Bottleneck (data-backed)
**Sieve**: ~100% of table operations fall to exit-resume due to missing ArrayBool fast path. This is the dominant bottleneck. The fast path type checks + bounds checks (~25 ARM64 insns per access) are secondary — they won't even execute until the ArrayBool dispatch is fixed.

## Plan Summary
Add native ARM64 fast paths for ArrayBool and ArrayFloat to `emitGetTableNative` and `emitSetTableNative` in `emit_table.go`. This eliminates the exit-resume storm that makes sieve barely faster than VM (1.05x). Expected: sieve 0.239s → 0.10–0.15s (30–50% improvement). The fix is bounded (emit_table.go + value_layout.go + tests), high-confidence (binary optimization — either exits or doesn't), and establishes the `field_access` category for future rounds. Key risk: if sieve's table demotes to ArrayMixed at runtime due to nil writes, the optimization won't fire (diagnostic test required as Task 1 validation).
