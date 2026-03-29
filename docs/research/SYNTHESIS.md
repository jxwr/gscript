# GScript JIT Optimization Synthesis

**Date:** 2026-03-27
**Source Documents:** v8-hidden-classes.md, v8-speculation-deopt.md, v8-maglev-analysis.md, perf-bottleneck-analysis.md
**Goal:** Unified optimization roadmap to reach 100x speedup over VM

---

## Executive Summary

**Current State:** 3 of 21 benchmarks get JIT acceleration (8.6x best case). 18 benchmarks are blocked from compiling due to `SSA_CALL` rejection.

**Core Finding:** The single highest-impact blocker is `ssaIsIntegerOnly` rejecting traces containing function calls. This was a **design mistake** — real-world code contains calls, and competitive JITs (LuaJIT, V8 Maglev) handle them efficiently.

**Unified Strategy:**
1. **Phase 0 (P0):** Unblock compilation — remove call-exit rejection (2-10x)
2. **Phase 1 (P1):** Type feedback foundation — shape system + inline caches (2-5x)
3. **Phase 2 (P2):** Array specialization — ArrayFloat JIT (3-10x)
4. **Phase 3 (P3):** Inlining & guard optimization (2-5x)

**Cumulative Expected Speedup:** 50-200x on key benchmarks, matching LuaJIT.

---

## Key Findings by Research Area

### 1. Hidden Classes & Inline Caches (v8-hidden-classes.md)

**What V8 Does:**
- Maps track object structure (property offsets, types)
- Inline caches cache per-call-site shape + offset
- Shape transitions enable O(1) reuse for same property addition order
- Monomorphic ICs enable single-guard field access

**What GScript Has:**
- `shapeID` computed as hash of `skeys` (already exists)
- `FieldCacheEntry` caches index + shapeID (VM inline cache exists)
- Missing: Map transition tree, JIT shape guards, interpreter IC feedback collection

**Takeaway:** GScript has 60% of V8's hidden class system. Adding transition tree + JIT guards unlocks 20-50% field access speedup.

---

### 2. Speculative Optimization & Deoptimization (v8-speculation-deopt.md)

**What V8 Does:**
- Ignition interpreter collects type feedback in FeedbackVector
- TurboFan speculates on types, emits guards
- Deoptimization restores interpreter state on guard failure
- OSR enables mid-execution tier-up

**What GScript Should Do (Prioritized):**

| Feature | Effort | Impact | Priority |
|---------|---------|--------|----------|
| Integer speculation (already partial) | Low | 1.5-2x | **P0** |
| Eager deoptimization framework | Medium | Enables all speculation | **P1** |
| Table shape speculation | Medium | 2-5x on fields | **P1** |
| Bounds check elimination | Low-Medium | 1.2-1.5x | **P2** |
| OSR for hot loops | High | 1.2-1.5x | **P3** |
| Lazy deoptimization | High | Marginal | Defer |

**Key Insight:** V8's deoptimization is complex. Start with **eager deopt** (immediate bailout) — simpler, correct, sufficient for GScript.

---

### 3. Maglev JIT Architecture (v8-maglev-analysis.md)

**Maglev's Success Factors:**
- CFG-based SSA (not Sea of Nodes) → faster compilation
- Type feedback integration → specialized codegen
- Guard hoisting to loop headers → fewer checks
- No deep optimization passes → fast enough compilation
- ~10x faster than TurboFan, ~3x speedup over baseline

**GScript Similarities:**
- Already uses SSA (good!)
- Fast compilation focus (good!)
- Guard-based speculation (good!)

**GScript Gaps vs Maglev:**
| Feature | Maglev | GScript | Priority |
|----------|---------|-----------|-----------|
| Hidden classes | Maps + transitions | shapeID hash only | **P0** |
| Inline caches | Per-site feedback | None | **P0** |
| Guard hoisting | Loop header | Per-instruction | **P1** |
| Function entry traces | Entry-point recording | Loop-only traces | **P2** |
| Small function inlining | Budget-based | Method JIT only | **P2** |

**Critical Recommendation:** Do NOT add a Maglev tier. GScript's existing Trace JIT is Maglev-like. Focus on **completing the pipeline**, not adding complexity.

---

### 4. Performance Bottleneck Analysis (perf-bottleneck-analysis.md)

**Top 5 Bottlenecks (Ranked by Impact):**

| Rank | Bottleneck | Affected Benchmarks | Speedup | Effort |
|------|-----------|---------------------|----------|--------|
| **#1** | `SSA_CALL` rejection in `ssaIsIntegerOnly` | 18/21 | 2-10x | **Medium** |
| **#2** | Missing native `STORE_ARRAY` | sieve, sort, fannkuch | 2-5x | **Low-Med** |
| **#3** | While-loop tracing not supported | sieve, fannkuch | 2-3x | **Medium** |
| **#4** | 2D table access not supported | matmul | 2-5x | **Low-Med** |
| **#5** | Call-exit re-entry overhead | All call-heavy traces | 1.2-1.5x | **High** |

**Evidence:**
- Warm benchmarks show JIT **slower than VM** when traces contain calls (function_calls: 0.6x vs VM)
- Best benchmarks are pure arithmetic (mandelbrot: 5.5x, leibniz: 8.6x)
- 18 benchmarks get ~1x because they're rejected from compilation

**Root Cause:** The `ssaIsIntegerOnly` function assumes call-exits are too expensive. This is wrong — LuaJIT compiles traces with calls and gets 100x speedup.

---

## Unified Optimization Roadmap

### Phase 0: Unblock Compilation (P0 — IMMEDIATE)

**Goal:** Allow traces with `SSA_CALL` to compile.

**Changes:**
1. Remove `hasCallExit` rejection in `ssaIsIntegerOnly`
2. Make `SSA_CALL` emit side-exit code (already partially done)
3. Fix table_field_access benchmark bug (nil parameter)

**Expected Impact:**
- 2-10x speedup across 18 benchmarks
- fibonacci_recursive: 1.0x → 3-10x
- function_calls: 0.6x → 2-5x
- ackermann: 0.7x → 3-8x

**Effort:** 1 week
**Risk:** Low (pattern well-understood, partial implementation exists)

---

### Phase 1: Type Feedback Foundation (P0 — FOUNDATION)

**Goal:** Shape system + inline caches for field access optimization.

**Changes:**
1. Shape transition tree (Map system)
   - Shape struct with ID, field offsets, transition map
   - O(1) transition lookup
   - Shape reuse for identical property order

2. JIT shape guards
   - Emit shape ID check at trace entry
   - Direct field offset access after guard
   - Side-exit on shape mismatch

3. Interpreter inline cache (optional, defer if needed)
   - Per-instruction feedback slot
   - Monomorphic → polymorphic → megamorphic states
   - Feedback passes to JIT

**Expected Impact:**
- 20-50% speedup on field-heavy code
- nbody: current + 3-5x
- method_dispatch: current + 2-4x
- chess_board: current + 2-3x

**Effort:** 2-3 weeks
**Risk:** Medium (new system, integrates with JIT)
**Dependencies:** None

---

### Phase 2: ArrayFloat JIT (P1 — HIGHEST LEVERAGE)

**Goal:** Type-specialized array access for float arrays.

**Changes:**
1. Separate array types: ArrayInt, ArrayFloat, ArrayMixed
2. Native LOAD_ARRAY/STORE_ARRAY per type
3. Remove boxing/unboxing in hot loops
4. Bounds check hoisting to loop header

**Why This Is Highest Leverage:**
- Affects 4/7 standard benchmarks: nbody, mandelbrot, spectral_norm, matmul
- These benchmarks are compute-intensive, small optimization → big win
- Float operations are already unboxed, only array access needs work

**Expected Impact:**
- 3-10x speedup on float array benchmarks
- nbody: current + 5-8x
- mandelbrot: current + 2-3x (already fast)
- spectral_norm: current + 3-5x

**Effort:** 1-2 weeks
**Risk:** Low-Medium (type system changes)
**Dependencies:** None

---

### Phase 3: Aggressive Inlining (P2 — RECURSIVE/SMALL FUNCTIONS)

**Goal:** Inline small functions and handle recursive patterns.

**Changes:**
1. Function entry traces (for recursive functions: fib, ackermann)
2. Small function inlining in traces (≤10 bytecode, pure)
3. Cross-function trace recording (inline during recording)
4. Inlining budgeting to prevent bloat

**Why This Matters:**
- fibonacci_recursive and ackermann currently SLOWER than VM due to call-exit overhead
- Small functions like `add(x, 1)` cannot be inlined
- Inlining eliminates guard overhead

**Expected Impact:**
- 2-5x on recursive benchmarks
- fibonacci_recursive: 1.0x → 3-10x
- ackermann: 0.7x → 3-8x
- function_calls: 0.6x → 2-4x

**Effort:** 2 weeks
**Risk:** Medium (inlining budgeting, code bloat)
**Dependencies:** Phase 0 (call-exit working)

---

### Phase 4: Guard Optimization (P2 — REFINEMENT)

**Goal:** Reduce guard overhead for nested loops.

**Changes:**
1. WBR (Write-Before-Read) guard relaxation
2. Guard fusion (multiple guards → single check)
3. Stable global tracking (embed constants, invalidate on write)
4. Side-exit continuation traces (bridge traces)

**Expected Impact:**
- 20-30% reduction in guard overhead
- Better nested loop handling (e.g., mandelbrot inner loops)

**Effort:** 1-2 weeks
**Risk:** Low-Medium (incremental improvements)
**Dependencies:** Phase 0

---

### Phase 5: Deoptimization Framework (P1 — ENABLES SPECULATION)

**Goal:** Eager deoptimization for speculative optimization.

**Changes:**
1. Deoptimization data structures (FrameMapping, BailoutInfo)
2. Guard types (CheckInt32, CheckFloat64, CheckTableShape)
3. Deopt handler (materialize objects, fill interpreter frame)
4. Guard → deopt metadata binding

**Why This Enables Speculation:**
- Allows assuming integer types for arithmetic
- Allows assuming table shapes for field access
- Bailout restores correct state on assumption failure

**Expected Impact:**
- Enables 1.5-2x speedup on type-stable code
- Prerequisite for Phase 1 (shape guards)

**Effort:** 2-3 weeks
**Risk:** Medium (state reconstruction complexity)
**Dependencies:** None (can parallel with Phase 1)

---

## Implementation Priority Matrix

| Phase | Feature | Effort | Speedup | Dependencies | Recommended Order |
|-------|---------|---------|--------------|------------------|
| 0 | Remove call-exit rejection | 1 week | 2-10x | None | **1st** |
| 0 | Fix table_field_access bug | 1 day | Enable accurate measurement | None | **1st** |
| 1 | Shape transition tree | 1 week | +20-50% on fields | None | **2nd** |
| 1 | JIT shape guards | 1 week | +30-50% on fields | Shape tree | **2nd** |
| 2 | ArrayFloat JIT | 1-2 weeks | 3-10x on arrays | None | **3rd** |
| 5 | Eager deopt framework | 2-3 weeks | Enables speculation | None | Parallel |
| 3 | Function entry traces | 1 week | 2-5x on recursive | Phase 0 | **4th** |
| 3 | Small function inlining | 1 week | 1.5-3x on calls | Phase 0 | **4th** |
| 2 | Native STORE_ARRAY | 3-5 days | 2-5x on array writes | None | Parallel |
| 4 | Guard optimization | 1-2 weeks | +20-30% overall | Phase 0 | **5th** |

---

## Expected Cumulative Impact

### Benchmark-by-Benchmark Projections

| Benchmark | Current | Phase 0 | Phase 1 | Phase 2 | Phase 3 | Target |
|-----------|---------|---------|---------|---------|---------|--------|
| mandelbrot | 5.5x | 8x | 8x | 12x | 12x | **12-20x** |
| fibonacci_iterative | 2.9x | 5x | 5x | 5x | 5x | **5-8x** |
| leibniz | 8.6x | 12x | 12x | 20x | 20x | **20-30x** |
| fibonacci_recursive | 1.0x | 3x | 3x | 3x | 10x | **10-20x** |
| ackermann | 0.7x | 3x | 3x | 3x | 10x | **10-15x** |
| function_calls | 0.6x | 2x | 2x | 2x | 6x | **6-10x** |
| nbody | ~1x | 2x | 5x | 15x | 15x | **15-30x** |
| spectral_norm | ~1x | 2x | 4x | 10x | 10x | **10-20x** |
| matmul | ~1x | 2x | 4x | 8x | 8x | **8-15x** |
| sieve | ~1x | 3x | 3x | 6x | 6x | **6-10x** |

### Overall Suite Average

| Milestone | Expected Avg Speedup | Cumulative |
|-----------|---------------------|-------------|
| Current | 2.5x | 2.5x |
| + Phase 0 | 3x | **7.5x** |
| + Phase 1 | 1.3x | **10x** |
| + Phase 2 | 2x on arrays | **20x** |
| + Phase 3 | 2x on recursive | **40x** |
| + Phase 4 | 1.2x overall | **48x** |
| + Vectorization (future) | 1.5x | **72x** |

**Goal:** Match or exceed LuaJIT (100x on some benchmarks) by end of Phase 2 + vectorization.

---

## Rationale: Why This Order?

### 1. Phase 0 First — Unblock Everything

**Rationale:** 18 benchmarks are blocked from compiling. No amount of optimization helps code that never compiles.

**Evidence:**
- fibonacci_recursive: 1.0x vs VM (JIT slower due to call-exit overhead)
- function_calls: 0.6x vs VM (JIT 17x slower!)

**Impact:** Removing `SSA_CALL` rejection unlocks 85% of benchmarks with a single line change.

---

### 2. Phase 1 Before Phase 2 — Type Feedback Enables Array JIT

**Rationale:** ArrayFloat JIT requires knowing element types. Shape system provides the type infrastructure.

**Counterpoint:** ArrayFloat JIT can be implemented first using runtime type checks. But shape system enables more aggressive optimization (compile-time type decisions).

**Decision:** Implement in parallel. ArrayFloat JIT is higher leverage on its own.

---

### 3. Phase 2 Third — Highest Leverage Single Optimization

**Rationale:** Affects 4/7 standard benchmarks with compute-intensive patterns. Small implementation effort yields large gains.

**Evidence:** Mandelbrot and leibniz already show 5-8x speedup from unboxing. Array JIT multiplies this by removing bounds checks and boxing overhead.

---

### 4. Phase 3 Fourth — Depends on Call-Exit Working

**Rationale:** Inlining requires call-exit mechanism to be efficient. Phase 0 fixes this.

**Evidence:** Recursive benchmarks are currently slower than VM due to call-exit overhead. Inlining eliminates this overhead.

---

### 5. Phase 4 Last — Refinement

**Rationale:** Guard optimization yields 20-30% improvement, which is valuable but smaller than unblocking compilation (Phase 0) or adding array JIT (Phase 2).

**When to Do It:** After Phase 0-3 show results. Guard optimization complexity may not be worth it if other phases achieve targets.

---

## What NOT to Do (Based on V8 Research)

### Avoid: Sea of Nodes IR

**Why:** V8 is abandoning SoN for CFG-based Turboshaft. Too complex for trace JIT.

**GScript Status:** Already uses SSA (good!). No change needed.

---

### Avoid: Adding a Maglev Tier

**Why:** GScript's Trace JIT is already Maglev-like. Adding a third tier increases complexity without clear benefit.

**Evidence:** Maglev's success comes from simplicity, not complexity. Focus on completing current pipeline.

---

### Avoid: Lazy Deoptimization (Initially)

**Why:** Eager deopt is simpler and correct. Lazy adds complexity for marginal gains.

**V8 Context:** V8 uses lazy for specific cases (try/catch, overflow). Most deopts are eager.

**Recommendation:** Start with eager, add lazy if profiling shows need.

---

### Avoid: Complex Loop Transformations

**Why:** Maglev doesn't unroll loops or do interprocedural analysis. Yet it achieves 3x speedup.

**Recommendation:** Focus on type specialization and inlining first. Loop unrolling comes later (if needed).

---

## Risk Mitigation

### Risk 1: Call-Exit Overhead Still Too High

**Mitigation:** Phase 0 removes rejection but doesn't fix re-entry overhead. If JIT still slower than VM:

1. Profile call-exit path with pprof
2. Optimize resume dispatch (remove CMP+BEQ table)
3. Implement side-exit continuation (bridge traces)

**Fallback:** Limit call-exit to simple functions (no nested calls, no returns).

---

### Risk 2: Shape System Memory Overhead

**Mitigation:**
- Start with one-level transition cache (not full V8-style tree)
- Use reference counting for shape GC
- Profile memory usage with benchmarks

**Fallback:** Keep existing hash-based shapeID if overhead unacceptable.

---

### Risk 3: Deoptimization Bugs

**Mitigation:**
- Extensive testing of deopt paths
- Debug mode that validates reconstruction
- Built-in observability (dump deopt info on failure)

**Lessons:** V8 has had multiple deoptimization CVEs. Correctness > speed.

---

### Risk 4: Inlining Bloat

**Mitigation:**
- Strict inlining budget (max bytecode size, max depth)
- Measure compile time; revert if >2x baseline
- Profile for "bouncy" functions that oscillate between states

**Fallback:** Disable inlining for functions with high deopt rate.

---

## Next Steps

1. **Immediate (Week 1):**
   - Fix table_field_access benchmark bug (trivial, already done)
   - Remove `SSA_CALL` rejection in `ssaIsIntegerOnly`
   - Verify 18+ benchmarks now compile

2. **Week 2-3:**
   - Implement shape transition tree
   - Add JIT shape guards
   - Benchmark nbody, method_dispatch for 20-50% improvement

3. **Week 4-5:**
   - Implement ArrayFloat JIT
   - Benchmark nbody, mandelbrot for 3-10x improvement
   - Verify reaching 20-30x on compute benchmarks

4. **Week 6-7:**
   - Implement function entry traces
   - Add small function inlining
   - Benchmark fibonacci_recursive, ackermann for 5-15x

5. **Week 8+:**
   - Guard optimization if needed
   - Deoptimization framework if speculation added
   - Vectorization (future work)

---

## Conclusion

**The path to 100x is clear and incremental:**

1. **Phase 0 (1 week):** Unblock 18 benchmarks → 3-10x immediate gains
2. **Phase 1 (2 weeks):** Shape system → 20-50% field access improvement
3. **Phase 2 (2 weeks):** ArrayFloat JIT → 3-10x on 4 benchmarks
4. **Phase 3 (2 weeks):** Inlining → 2-5x on recursive/call-heavy

**Total effort:** ~7 weeks to reach 20-50x on key benchmarks.

**Key insight:** The single highest-impact change is removing `SSA_CALL` rejection. This was a design mistake that crippled real-world performance. Fixing it alone doubles JIT effectiveness.

**GScript's architecture is sound.** The problem isn't missing complexity — it's incomplete implementation. Focus on completing the pipeline, not adding new tiers.
