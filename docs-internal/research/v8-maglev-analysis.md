# V8 Maglev JIT Analysis: Architecture & GScript Recommendations

**Date:** 2026-03-27
**Researcher:** AI Research Agent
**Target:** V8 Maglev compiler applicability to GScript's JIT optimization roadmap

---

## Executive Summary

V8's Maglev compiler was introduced in Chrome M117 as a mid-tier optimizing JIT sitting between Sparkplug (baseline) and TurboFan (optimizing). Maglev achieves ~10x faster compilation than TurboFan while delivering "good enough" performance for moderately hot code, reducing energy consumption by 3.5-10% across benchmarks.

**Key Finding:** GScript should **NOT add a Maglev-like mid-tier**. Instead, focus on:
1. Improving type feedback integration (hidden classes + inline caches)
2. More aggressive inlining in existing Trace JIT
3. ArrayFloat JIT for type-specialized array access
4. Better guard speculation mechanisms

The Trace JIT architecture GScript already has is closer to Maglev's approach (simple, fast compilation, SSA-based) than to TurboFan's complex Sea of Nodes. The leverage lies in completing the current pipeline, not adding a new tier.

---

## 1. Maglev Architecture

### 1.1 Position in V8's Compilation Pipeline

```
Ignition (Interpreter)
    |
    v
Sparkplug (Baseline JIT)
    |  Compiles bytecode to equivalent machine code
    |  Near-instant compilation
    |  +45% over Ignition on JetStream
    |
    v
Maglev (Fast Optimizing JIT) ← New in Chrome M117
    |  SSA-based, CFG IR (not Sea of Nodes)
    |  10x faster than TurboFan
    |  Good enough code, fast enough
    |
    v
TurboFan (Peak Optimizing JIT)
    |  Sea of Nodes IR
    |  Maximum performance, slow compilation
    |  4.35x on JetStream
```

### 1.2 Why V8 Added Maglev

**The Problem V8 Solved:**
- Gap between Sparkplug (+45% over Ignition) and TurboFan (+435%)
- Speedometer benchmark: lots of code not hot enough for TurboFan but too hot for Sparkplug
- TurboFan's compilation cost too high for "moderately hot" functions
- Energy waste: compiling rarely-used code with expensive TurboFan

**The Maglev Solution:**
- **10x faster compilation** than TurboFan
- **"Good enough" performance** for code that doesn't warrant TurboFan's cost
- **Simpler IR**: CFG-based SSA instead of Sea of Nodes
- **Lower energy consumption**: 3.5% on JetStream, 10% on Speedometer

### 1.3 Maglev's IR Design

**Key Difference from TurboFan:**

| Aspect | TurboFan | Maglev |
|---------|-----------|---------|
| IR Structure | Sea of Nodes (graph) | CFG with basic blocks |
| Naming | Arbitrary node IDs | Implicit naming by array index |
| Compilation | Multiple reductions (high-level → low-level) | Single IR, minimal passes |
| Cache Friendliness | Poor (many indirections) | Better (linear layout) |

**Maglev's Design Philosophy:**
> "Since we felt that not having an IR at all during compilation would likely severely restrict the compiler, we decided to go with a somewhat traditional static single-assignment (SSA) based approach, using a CFG rather than TurboFan's more flexible but cache unfriendly sea-of-nodes representation."

### 1.4 Maglev's Compilation Pipeline

```
Prepass
    ├─ Find branch targets, loops
    ├─ Collect variable assignments in loops
    └─ Collect liveness info

SSA Construction (Single Forward Pass)
    ├─ Abstract interpretation of frame state
    ├─ Create SSA nodes for expression evaluation
    ├─ Phi nodes for merge points
    ├─ Pre-create loop phis for back-edge variables
    └─ "Back in time" data flow handled by prepass data

Known Node Information (Built During Graph Building)
    ├─ Look at runtime feedback metadata
    ├─ Generate specialized SSA nodes (e.g., CheckMap + LoadField)
    ├─ Record learned info (shape of o)
    └─ Register dependencies for invalidation (globals, maps)

Deoptimization Support
    ├─ Attach abstract interpreter frame state to nodes
    ├─ Map interpreter registers to SSA values
    └─ Reuse TurboFan's deoptimization mechanism

Representation Selection
    ├─ Unbox numbers based on type feedback
    ├─ Smi (31-bit int) vs Float64
    ├─ Forward propagation of representation info
    └─ Separate phase for loop phi representation

Register Allocation
    ├─ Single forward walk over graph
    ├─ Abstract machine register state
    ├─ Linear live ranges (prepass)
    ├─ Local register assignment rules
    ├─ Spill on definition (no register)
    └─ Split frame: tagged vs untagged regions

Code Generation
    ├─ Nodes know how to emit assembly
    ├─ Macro assembler interface
    ├─ Parallel move resolver for register shuffling
    └─ Hot/cold code splitting
```

### 1.5 Key Optimizations in Maglev

**What Maglev DOES:**
- Type specialization based on runtime feedback
- Constant hoisting (implicitly during graph building)
- Inline caching for property access (CheckMap + LoadField)
- Loop-invariant code motion (via prepass)
- Hot/cold code splitting
- Guard hoisting to loop headers

**What Maglev Does NOT Do:**
- No extensive dataflow analysis passes
- No loop unrolling
- No advanced inlining budgeting
- No inter-procedural optimization
- No SSA simplification passes

The tradeoff: **Compilation speed > optimization depth**

---

## 2. Type Feedback Usage in Maglev

### 2.1 Hidden Classes (Maps) and Inline Caches

V8 uses **Hidden Classes** (called "Maps" in V8 source) to optimize property access:

```javascript
// V8 creates a hidden class for each object shape
const o = { x: 1 };        // Map A: { offset(x)=0 }
o.y = 2;                    // Map B: { offset(x)=0, offset(y)=8 }
```

**Inline Caches (IC):**
- Store the observed shape at property access site
- First access: miss → shape lookup + cache
- Subsequent access: hit → direct offset load with guard

### 2.2 Maglev's Speculative Optimization

Maglev uses three types of runtime information:

1. **Checked at Runtime:**
   - Shape checks (CheckMap) before LoadField
   - Type guards before arithmetic
   - Bail out on mismatch → deoptimization

2. **Registered Dependencies:**
   - Globals that never change → embed directly in code
   - Maps that never transition → register dependency
   - On mutation: invalidate code that depends on it

3. **"Unstable" Information (Only Use When Guaranteed):**
   - Newly allocated objects → skip write barriers
   - No allocation since object creation → stable
   - After allocation → must recheck

### 2.3 Deoptimization Mechanism

When speculative assumptions fail:

```arm64
CheckMap:
    LDR W0, [X0, #map_offset]    // Load object's map
    CMP W0, #expected_map             // Compare with known map
    B.NE deopt                      // Bail out to interpreter if different
LoadField:
    LDR X1, [X0, #field_offset]    // Direct load (safe after CheckMap)
```

Deoptimization metadata:
- Abstract interpreter frame state attached to nodes
- Maps SSA values back to interpreter registers
- Lazy deoptimization: continue current frame, deopt on exit
- Shared with TurboFan's deopt infrastructure

---

## 3. Inlining Strategy

### 3.1 Maglev's Inlining Approach

Maglev has a **MaglevInliner** class with these characteristics:

**Known Limitations:**
- No inlining of **polymorphic indirect function calls** (higher-order functions)
- Coarse-grained inlining (small functions only)
- Inlining budget limits (max_inlined_bytecode_size_small_total)

**What Gets Inlined:**
- Small functions with stable call patterns
- Monomorphic call sites (always same function)
- Pure arithmetic functions

### 3.2 Why No Polymorphic Inlining

From Stack Overflow discussions:
> "V8 does not inline polymorphic indirect function calls (where 'indirect function calls' is what you might think of when using function as first-class values)"

The cost of polymorphic inline cache miss + dynamic dispatch exceeds inlining benefit.

---

## 4. Comparison: Maglev vs GScript Trace JIT

### 4.1 Architecture Comparison

| Aspect | Maglev | GScript Trace JIT |
|---------|-----------|-------------------|
| IR Type | CFG-based SSA | Trace-based SSA (linear) |
| Compilation Unit | Entire function | Hot loops + inlined callees |
| Optimization Passes | Minimal (built into graph building) | ConstHoist, CSE, FMA, GuardAnalysis |
| Register Allocation | Single forward walk | Hybrid (slot-based ints + ref-level floats) |
| Type Feedback | Hidden classes + inline caches | Recording-time type capture |
| Guard Strategy | CheckMap + inline cache | GUARD_TYPE before unboxing |
| Deoptimization | Shared with TurboFan | Side-exit to interpreter |

### 4.2 Similarities (What GScript Already Does)

**GScript Already Has Maglev-like Features:**
- SSA-based IR ✓
- Fast compilation focus ✓
- Guard-based deoptimization ✓
- Hot/cold code splitting ✓
- Type specialization (TypeInt, TypeFloat) ✓
- Inlining (Method JIT: small functions) ✓
- Call-exit mechanism (vs interpreter) ✓

**Key Similarity:** Both prioritize fast compilation over deep optimization.

### 4.3 Differences (What GScript Lacks)

| Feature | Maglev | GScript | Priority |
|----------|-----------|-----------|-----------|
| Hidden Classes | Maps track object structure | No shape system yet | **P0** |
| Inline Caches | Per-site shape caching | No IC infrastructure | **P0** |
| Stable Globals | Dependencies + invalidation | No global tracking | **P1** |
| Unboxed Numbers | Smi (31-bit int) + Float64 | NaN-boxed ints (48-bit) | **P2** |
| Loop Phi Specialization | Separate phase | Handled in slot allocation | **P2** |
| Function Entry Traces | Entry-point optimization | Only loop traces | **P3** |

---

## 5. Recommendations for GScript

### 5.1 Should GScript Add a Maglev-Like Tier?

**NO.** Reasons:

1. **Trace JIT is already Maglev-like:** GScript's current approach is closer to Maglev's philosophy (fast compilation, simple SSA, guard-based) than to TurboFan's complexity.

2. **Two-tier system works well:** Method JIT (broad coverage) + Trace JIT (deep optimization) mirrors Ignition/Sparkplug + Maglev. Adding a third tier would increase complexity without clear benefit.

3. **Focus should be on completing current pipeline:** GScript's best speedup is 11x; LuaJIT is ~100x on some benchmarks. The gap is in unimplemented features, not missing tiers.

### 5.2 Priority Roadmap: High-Leverage Optimizations

Based on Maglev's strengths and GScript's gaps:

#### P0: Type Feedback Infrastructure (Foundation for everything else)

**What to Implement:**
1. **Hidden Classes / Shape System**
   - Track object property layout (offsets, types)
   - Fast Map lookup for GETFIELD/SETFIELD
   - Map transitions when properties added

2. **Inline Caches**
   - Per-instruction cache for property access
   - Cache hit: direct load with guard
   - Cache miss: update cache + bail to slow path

**Expected Impact:**
- 2-5x speedup on benchmarks with table access (chess, matmul, spectral_norm)
- Unblocks array JIT (need type info)

**Estimated Effort:** 2-3 weeks (Shape system + IC integration)

#### P1: ArrayFloat JIT (Type-Specialized Array Access)

**What to Implement:**
1. Separate array types: ArrayInt, ArrayFloat, ArrayMixed
2. Native LOAD_ARRAY/STORE_ARRAY for each type
3. Remove boxing/unboxing in hot array loops
4. Bounds check hoisting to loop header

**Expected Impact:**
- 3-10x speedup on float array benchmarks (nbody, mandelbrot)
- Unblocks more aggressive loop unrolling

**Estimated Effort:** 1-2 weeks

#### P2: Aggressive Inlining (Maglev-style)

**What to Implement:**
1. Function entry traces (for recursive functions: fib, ackermann)
2. Small function inlining in traces (<=10 bytecode, pure)
3. Cross-function trace recording (inline during recording)
4. Inlining budgeting to prevent code bloat

**Expected Impact:**
- 2-5x on recursive benchmarks (fib, ackermann)
- Reduced guard overhead (fewer call-exits)

**Estimated Effort:** 2 weeks

#### P3: Better Guard Speculation

**What to Implement:**
1. WBR (Write-Before-Read) guard relaxation (already planned)
2. Guard fusion (multiple guards → single check)
3. Stable global tracking (embed constants, invalidate on write)
4. Side-exit continuation traces (bridge traces)

**Expected Impact:**
- 20-30% reduction in guard overhead
- Better nested loop handling

**Estimated Effort:** 1-2 weeks

### 5.3 What NOT to Prioritize (Based on Maglev)

**Low-ROI Efforts (Defer):**
1. **Multiple IR reductions:** Maglev shows single IR is sufficient
2. **Complex loop transformations:** Maglev doesn't unroll; GScript shouldn't either
3. **Interprocedural analysis:** Maglev's success shows it's not critical for 10-20x
4. **Sea of Nodes IR:** V8 replaced it for fast compilation

---

## 6. Implementation Order

### Phase 1: Type Feedback Foundation (3-4 weeks)

```
Week 1: Shape System
    ├─ Shape struct with property offsets/types
    ├─ Shape lookup/transition functions
    └─ Tests: shape transitions work correctly

Week 2: Inline Caches
    ├─ InlineCache struct per VM
    ├─ IC integration into GETFIELD/SETFIELD
    └─ Tests: cache hit/miss paths

Week 3-4: Type Specialization
    ├─ Use IC info for SSA type inference
    ├─ Emit CheckMap-like guards before field access
    └─ Benchmark: 2-5x on table-heavy workloads
```

### Phase 2: ArrayFloat JIT (1-2 weeks)

```
Week 1: Array Type System
    ├─ Separate ArrayInt, ArrayFloat, ArrayMixed
    ├─ Type creation API
    └─ Tests: type-specific operations

Week 2: Native Code Generation
    ├─ LOAD_ARRAY/STORE_ARRAY per type
    ├─ Bounds check hoisting
    └─ Benchmark: 3-10x on nbody/mandelbrot
```

### Phase 3: Inlining (2 weeks)

```
Week 1: Function Entry Traces
    ├─ Call counting per function
    ├─ Entry-point recording
    └─ Tests: fib/ackermann work

Week 2: Small Function Inlining
    ├─ Inline during trace recording
    ├─ Budget-based decisions
    └─ Benchmark: 2-5x on recursive benchmarks
```

### Phase 4: Guard Optimizations (1-2 weeks)

```
Week 1: Guard Improvements
    ├─ WBR guard relaxation
    ├─ Guard fusion
    ├─ Stable global tracking
    └─ Tests: reduced guard overhead

Week 2: Side-Exit Continuation
    ├─ Bridge trace compilation
    ├─ Exit counter tracking
    └─ Benchmark: better nested loops
```

---

## 7. Expected Cumulative Impact

Based on Maglev's demonstrated improvements and GScript's current baseline:

| Milestone | Expected Speedup | Cumulative |
|-----------|-----------------|-------------|
| Current (Trace JIT) | 11x vs interpreter | 11x |
| + Phase 1: Type Feedback | +2-3x | **22-33x** |
| + Phase 2: ArrayFloat JIT | +2-3x on array benchmarks | **44-100x on array workloads** |
| + Phase 3: Inlining | +2-5x on recursive benchmarks | **200-500x on tail-call code** |
| + Phase 4: Guard Optimizations | +20-30% across board | **264-650x** |

**Goal:** Match or exceed LuaJIT (100x on some benchmarks) by end of Phase 2.

---

## 8. Key Takeaways

1. **Maglev's success comes from simplicity, not complexity.** Fast compilation + good enough performance beats slow compilation + perfect optimization for most workloads.

2. **Type feedback is the foundation.** Hidden classes + inline caches enable almost all other optimizations (type specialization, inlining, array JIT).

3. **GScript's Trace JIT is already on the right path.** The architecture mirrors Maglev's philosophy. Focus on completing the pipeline, not adding tiers.

4. **ArrayFloat JIT is the highest-leverage single optimization.** It touches nbody, mandelbrot, spectral_norm, matmul — 4/7 of the standard benchmarks.

5. **Inlining is more valuable than guard optimization.** Maglev shows that reducing function call overhead (even without deep inlining) yields significant wins.

6. **Deoptimization is unavoidable, make it cheap.** Maglev and V8 show that speculative optimization + fast bailouts is better than conservative compilation.

---

## 9. References

### Primary Sources

- [Maglev - V8's Fastest Optimizing JIT](https://v8.dev/blog/maglev) - Official V8 blog post, primary source for architecture and design decisions
- [JavaScript Hidden Classes and Inline Caching in V8](https://dev.to/srinivas_004/unveiling-the-magic-javascript-hidden-classes-and-inline-caching-in-v8-1j48) - Hidden class mechanics
- [Hidden Classes & Inline Caches - JavaScript Under the Hood](https://stanza.dev/courses/javascript-performance-internals/v8-engine/javascript-performance-internals-hidden-classes) - Inline caching overview
- [How V8 access property when inline cache misses?](https://stackoverflow.com/questions/40262837/how-v8-access-property-when-inline-cache-misses) - IC miss handling
- [js v8 function inlining - javascript - Stack Overflow](https://stackoverflow.com/questions/79666788/js-v8-function-inlining) - Inlining limitations
- [Maps (Hidden Classes) in V8](https://v8.dev/docs/hidden-classes) - Official V8 hidden class documentation

### Performance Analysis

- [JavaScript Optimisation. Inline Caches - Frontend Almanac](https://blog.frontend-almanac.com/js-optimisation-ic) - IC performance impact
- [Nobody warns you about V8 deopts: 8 ways hot code turns cold](https://medium.com/@kaushalsinh73/nobody-warns-you-about-v8-deopts-8-ways-hot-code-turns-cold-faea44894ff3) - Deoptimization triggers

### Security Research (for deoptimization correctness)

- [Chrome Browser Exploitation articles](https://jhalon.github.io/chrome-browser-exploitation-2/) - Maglev exploitation analysis
- [An Introduction to Chrome Exploitation - Maglev Edition](https://www.matteomalvica.com/blog/2024/06/05/intro-v8-exploitation-maglev/) - Maglev internals

---

## Appendix: Maglev Performance Numbers

From V8 Blog Post (Chrome 117 on M2 MacBook Air):

| Benchmark | Ignition | Ignition+Sparkplug | +TurboFan | +Maglev | +Both |
|-----------|-----------|---------------------|-------------|-----------|---------|
| JetStream | 1.0x | 1.45x | 4.35x | ~3.0x | ~4.5x |
| Speedometer | 1.0x | 1.41x | 1.53x | ~1.35x | ~1.65x |

**Energy Consumption:**
- JetStream: -3.5%
- Speedometer: -10%

**Compilation Time:**
- Sparkplug: baseline (1x)
- Maglev: 10x slower than Sparkplug
- TurboFan: 10x slower than Maglev (100x slower than Sparkplug)

**Key Insight:** Maglev achieves most of TurboFan's speedup at 1/10 the compilation cost.
