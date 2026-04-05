# ANALYZE Report — 2026-04-05

Commit: `1932fbbb` (jit-v3-clean)

## Gap Classification

Classifying all 22 benchmarks by root cause category. `gap` = JIT_time − LuaJIT_time (where LuaJIT available).

| Root Cause | Benchmarks | Count | Avg JIT/LuaJIT | Total Absolute Gap | Solvable? |
|-----------|------------|-------|----------------|---------------------|-----------|
| **A. Call-heavy, no-loop (stays Tier 1 forever)** | fib, fib_recursive, ackermann, mutual_recursion, method_dispatch | 5 | ~47x (where LuaJIT ref) | ~1.79s (fib+ackermann+mutual_rec) | YES — V8-style inlining + policy fix |
| **B. Float-heavy hot loop** | spectral_norm, nbody, mandelbrot, matmul, fannkuch | 5 | ~23x | ~2.05s | LIKELY — need pprof first |
| **C. Tight integer hot loop barely beats VM** | sieve (0.97x VM), fibonacci_iterative, sum_primes | 3 | ~10x (where ref) | ~0.22s | PARTIAL — Tier 2 overhead for simple loops |
| **D. Allocation-heavy JIT regressions** | binary_trees (1.27x), object_creation (1.18x), coroutine_bench | 3 | regressed | ~0.5s vs VM | ARCH CHANGE — NEWTABLE exit-resume |
| **E. Already strong** | sort (4.8x), fannkuch (3.7x), sum_primes (2.0x) | 3 | ~3.5x | minor | Diminishing returns |
| **F. No LuaJIT reference** | closure_bench, string_bench, table_field_access, table_array_access, math_intensive | 5 | N/A | N/A | Defer |

## Selected Target

**Category: A — Call-heavy, no-loop functions (suboptimal tiering + lack of recursive inlining)**

### Why this over alternatives

**ROI analysis:**
- **Affects 5 benchmarks directly** (fib, fib_recursive, ackermann, mutual_recursion, method_dispatch), likely more indirectly (binary_trees has recursive tree-walk, coroutine_bench uses recursive closures).
- **Biggest single gap:** fib = 1.364s vs LuaJIT (60.3x slower). JIT is only 0.86x VM — we are barely beating the interpreter on the #1 Lua benchmark.
- **Root cause is structurally identified**: `func_profile.go:128-136` explicitly excludes call-heavy, no-loop functions from Tier 2 promotion. The rationale in the comment ("non-loop functions don't benefit enough from Tier 2's type specialization to justify compilation overhead") is a hypothesis, not measured fact — and it ignores the compounding benefit of inlining.
- **Pattern signature**: For these 5 benchmarks, `JIT/VM ≈ 0.85–0.92x`. JIT compiles them (at Tier 1) but the per-call NaN-boxing/unboxing overhead dominates. Moving them to Tier 2 with inlining would eliminate the boxing churn **and** eliminate call overhead for inlined levels.
- **Prior art is clean**: V8's Maglev/TurboFan aggressively inline small monomorphic callees, including limited recursive inlining (bounded by depth + size budget). This is exactly our architectural reference per `lessons-learned.md` #3.
- **Not a detour pattern**: None of the three documented detours match. Method JIT + SSA inlining is the V8 path we explicitly chose in ADR-001/002.

**Why not Category B (float-heavy)?**
- Known-issues already flags this with an open investigation thread ("Next investigation: pprof Tier 2 emitted code for spectral_norm inner loop"). The bottleneck is **unknown** (could be FPR spills, int48 overflow guards, Tier 2 codegen quality). Requires profiling before planning.
- Smaller per-benchmark leverage: best case recovery ~2.4x on spectral_norm (from 0.329s back to pre-overflow 0.138s). Still 47x from LuaJIT.
- Should be next round — but only after profiling clarifies the bottleneck.

**Why not Category D (allocation regressions)?**
- Requires architectural change (NEWTABLE codegen or escape analysis). Known-issues documents these as expected given current design.
- Smaller absolute gaps.

### Benchmarks targeted

- **fib** — 1.387s → target sub-0.3s (inline 3-4 levels, int-specialize)
- **fib_recursive** — 13.919s (10 reps) — same pattern
- **ackermann** — 0.253s — very similar shape to fib
- **mutual_recursion** — 0.184s — cross-function inlining would collapse both bodies
- **method_dispatch** — 0.099s (currently 1.16x regression!) — BLR overhead dominates when callee is tiny

## Detour Check

Checked against `docs-internal/lessons-learned.md`:

| Detour | Match? |
|--------|--------|
| 1. Trace JIT | NO — we're staying within Method JIT. Opposite direction. |
| 2. Memory-to-Memory middle tier | NO — we have Tier 1 (template) → Tier 2 (SSA+regalloc). Not adding a new tier. |
| 3. Function-entry tracing | NO — we're promoting existing Tier 2 compilation to more functions, not recording traces. |

General lessons applied:
- ✓ **#3 V8 default**: Function inlining + aggressive tier-up is exactly what V8 does.
- ✓ **#6 Profile first**: Before implementing, we need to verify fib IS currently Tier 1 (not silently Tier 2) and measure what Tier 1 is actually spending time on.
- ⚠ **#1 Multiple-regression watch**: If pushing recursive functions to Tier 2 regresses 2+ float benchmarks (known Tier 2 overhead concern per `shouldPromoteTier2` comment), rethink.

## Prior Art Needed

Before PLAN phase, research the following (one Researcher sub-agent):

1. **V8 Maglev & TurboFan inlining heuristics**
   - What size/depth limits do they use for inlining?
   - How do they bound recursive inlining? (Typically: specialize for depth N, fall back to call for deeper recursion)
   - What counts as a "small" callee — in bytecode count, IR node count, or both?

2. **SpiderMonkey Warp inlining policy**
   - Warp's PolyIC-based inlining for monomorphic call sites
   - How they balance compile time vs inlined code size

3. **JavaScriptCore DFG/FTL tier-up for recursive functions**
   - Recursion handling at the DFG level (bounded inlining? tail-call specialization?)

4. **Call overhead decomposition (empirical, via pprof — Profiler sub-agent)**
   - Where does Tier 1 spend cycles on fib? (NaN-box/unbox, BLR prologue/epilogue, arg copy, CallCount increment, bounds check)
   - Confirm fib is running at Tier 1 (not Tier 2) currently
   - Measure: cost of NaN-box/unbox alone vs total per-call cost

**Secondary (can defer to PLAN if time-boxed):**
- Inlining literature: "Adaptive online inlining with recompilation" (Suganuma et al., HotSpot)
- Tail-call optimization in V8 (currently disabled by default — why?)

## Open Questions for PLAN Phase

1. Should the fix be **pure policy** (change `shouldPromoteTier2` to allow call-only functions at some threshold) or **policy + inliner improvements** (bounded recursive inlining in `pass_inline.go`)?
2. What recursion-inlining depth is safe? (IR graph explosion risk if unbounded.)
3. Is there a quick-win by lowering the fib body's boxing: e.g., treat `if n < 2` as an int guard and keep `n` in a raw int register throughout Tier 1?
4. Does `pass_inline.go` already handle recursive calls, or does it reject self-referential inlining?

## Success Criteria for Next Round

- **fib ≤ 0.5s** (2.8x improvement, reduces gap from 60x → 22x vs LuaJIT)
- **ackermann ≤ 0.1s** (2.5x improvement)
- **mutual_recursion ≤ 0.08s** (2.3x improvement)
- **No regression** on float benchmarks (spectral_norm, nbody, mandelbrot, matmul, fannkuch stay within 5% of current times)
- **No correctness regression**: all 22 benchmarks produce correct results
