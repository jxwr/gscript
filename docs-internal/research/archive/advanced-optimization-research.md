# Advanced Compiler Optimization Techniques: Feasibility Analysis for GScript

**Date:** 2026-03-19
**Context:** GScript tracing JIT on ARM64, targeting LuaJIT-level performance

## Executive Summary

This report evaluates five advanced compiler optimization techniques for their applicability to GScript's tracing JIT compiler. The techniques span the spectrum from code generation strategies (Copy-and-Patch, Deegen) to machine-learning-driven heuristics (MLGO), binary-level layout optimization (BOLT), and memory representation compression (Pointer Compression).

**Bottom-line ranking:**

| Priority | Technique | Expected Impact | Difficulty | Verdict |
|----------|-----------|----------------|------------|---------|
| 1 | Pointer Compression / NaN-boxing | 2-4x on table-heavy benchmarks | High | **Must-do for Season 2** |
| 2 | BOLT-style Code Layout | 10-25% on compute-heavy benchmarks | Medium | **Do after trace inlining** |
| 3 | Copy-and-Patch (selective) | 15-30% code quality improvement | Medium | **Adopt ideas selectively** |
| 4 | MLGO (ML-Guided Heuristics) | 5-15% on register allocation | High | **Interesting but premature** |
| 5 | Deegen | N/A (architectural mismatch) | Extreme | **Not applicable** |

---

## 1. Copy-and-Patch JIT

### 1.1 Principle

Copy-and-patch compilation, introduced by Haoran Xu and Fredrik Kjolstad (OOPSLA 2021, Distinguished Paper Award), is a code generation technique that pre-compiles binary code templates ("stencils") at build time using an optimizing compiler (typically Clang/LLVM), then at runtime simply copies the appropriate stencil and patches in the runtime-specific values (addresses, constants, operands) to produce executable machine code.

The key insight is that most bytecode operations have a fixed structure with only a few varying parts. Rather than emitting instructions one at a time through a custom assembler (as GScript currently does), you compile the entire operation template through LLVM's full optimization pipeline once, producing a high-quality binary fragment. At runtime, you just memcpy the stencil and fill in the "holes" -- specific memory offsets where runtime values (register base pointers, constant pool addresses, jump targets) need to be inserted.

The MetaVar system generates stencil variants using C++ template metaprogramming, with special macros marking the holes. LLVM compiles each variant to native code, and a post-processing step extracts the stencil bytes and hole locations into a data table. The entire stencil library is built at compile time, making the runtime code generator essentially a table-driven memcpy + patch loop.

### 1.2 Key Papers and Projects

- **Paper:** Xu & Kjolstad, "Copy-and-Patch Compilation: A fast compilation algorithm for high-level languages and bytecode" (OOPSLA 2021) -- [ACM DL](https://dl.acm.org/doi/10.1145/3485513) | [PDF](https://fredrikbk.com/publications/copy-and-patch.pdf)
- **CPython 3.13 JIT:** Uses copy-and-patch for its experimental JIT compiler. [PEP 744](https://peps.python.org/pep-0744/) | [Blog post](https://tonybaloney.github.io/posts/python-gets-a-jit.html)
- **WasmNow:** WebAssembly baseline compiler using copy-and-patch, 5-6.5x faster compilation than Chrome's Liftoff with 39-63% faster generated code. [GitHub](https://github.com/sillycross/WasmNow)
- **LuaJIT Remake (Deegen):** Uses an improved copy-and-patch for its baseline JIT tier. [arxiv](https://arxiv.org/abs/2411.11469)

### 1.3 Applicability to GScript

GScript currently generates ARM64 machine code through a hand-written assembler (`internal/jit/assembler.go`) that emits instructions one at a time. The SSA codegen pass (`ssa_codegen.go`) walks SSA instructions and calls assembler methods like `a.MovImm64()`, `a.Add()`, `a.Ldr()` etc. This approach gives full control but means every instruction sequence is hand-optimized (or not) by the developer.

**Where copy-and-patch could help:**

1. **Type guard + operation sequences.** GScript's trace compiler emits multi-instruction sequences for guarded operations (e.g., "check type is int, load from memory, add, store back"). Each of these could be a pre-compiled stencil with holes for the register base address, slot offsets, and guard-failure jump targets. Clang could schedule instructions better than the hand-written sequence.

2. **Prologue/epilogue code.** The 61-instruction sub-trace call prologue is a perfect candidate for a pre-optimized stencil. Clang could potentially schedule loads and stores to reduce pipeline stalls that hand-written code might miss.

3. **Inner loop bodies.** For common patterns (float multiply-add, integer comparison chains), stencils optimized by LLVM's ARM64 backend would benefit from LLVM's knowledge of Apple M-series microarchitectural details (scheduling for Firestorm/Avalanche cores, fusing opportunities).

**Where it does NOT help:**

1. **Dynamic specialization.** GScript's tracing JIT records actual execution types and specializes code paths dynamically. Copy-and-patch is fundamentally a method/bytecode-level technique -- it cannot observe runtime types and specialize on the fly. GScript's traces already eliminate type checks for observed types, which stencils alone cannot do.

2. **Cross-operation optimizations.** Stencils are per-operation. GScript's SSA pipeline already performs CSE, constant hoisting, and register allocation across multiple operations. Stencils would need to be "stitched" together, and the cross-stencil boundary prevents the kind of global optimizations GScript already does.

3. **Register allocation.** Stencils assume fixed register conventions (or parameterized registers). GScript's live-range-based float allocator already maps SSA refs to physical registers optimally. Stencils would need to either accept parameterized registers (increasing variant count) or use a fixed convention (losing optimization).

**The hybrid approach:** Use copy-and-patch for the "cold" parts of traces (prologues, epilogues, guard failure handlers, side-exit code) while keeping the custom assembler for hot inner loop bodies where SSA-level optimization matters most. This is essentially what Deegen's LuaJIT Remake does -- copy-and-patch for the baseline tier, hand-tuned for the optimizing tier.

### 1.4 Implementation Difficulty: **Medium**

- Requires a build-time stencil generation pipeline (C code compiled with Clang, post-processed to extract stencils)
- ARM64 stencils need the "tiny" code model to ensure all references are position-independent and patchable (the `copyjit` project on GitHub confirmed AArch64 needs `-mcmodel=tiny`)
- Integration with Go build system adds complexity (need a `go generate` step that invokes Clang)
- The GScript assembler is ~800 lines; replacing it entirely is medium effort, but a hybrid approach (stencils for some patterns) is more practical

### 1.5 Expected Performance Impact

- **Prologue/epilogue:** 10-20% reduction in sub-trace call overhead (better instruction scheduling)
- **Guard sequences:** 5-10% improvement (LLVM can fuse compare-and-branch better)
- **Inner loops:** Minimal improvement over hand-tuned assembler for simple arithmetic
- **Compilation speed:** Not a bottleneck for GScript (traces compile in microseconds already)
- **Net estimate:** 5-15% on mandelbrot (dominated by inner loop quality), negligible on fib (already beating LuaJIT)

### 1.6 Interaction with Existing Optimizations

- **SSA pipeline:** Would need to be preserved. Stencils would replace only the final code emission step, not the SSA optimization passes.
- **Register allocator:** Conflict. Stencils assume specific register conventions; the linear-scan allocator assigns registers dynamically. Resolution: parameterized stencils with register holes, or accept the allocator's output and use stencils only for fixed-convention sequences.
- **Trace inlining (Approach C):** Copy-and-patch is orthogonal to trace inlining. Both could coexist, with stencils handling individual operations within an inlined trace.

### 1.7 Verdict

**Adopt ideas selectively, do not replace the assembler wholesale.** The biggest wins come from using LLVM-quality instruction scheduling for stereotyped sequences (prologues, guards, complex multi-instruction patterns). But GScript's trace-level SSA optimization already provides cross-operation optimization that stencils cannot replicate. Priority: after trace inlining (Phase 5) is done, evaluate whether specific hot sequences would benefit from Clang-optimized templates.

---

## 2. Deegen (Automatic JIT Generation from Interpreter)

### 2.1 Principle

Deegen, by Haoran Xu and Fredrik Kjolstad (published 2024, to appear PLDI 2025), is a meta-compiler that takes bytecode execution semantics written as C++ functions and automatically generates three things: (1) a high-performance threaded interpreter with register-pinning and inline caching, (2) a baseline JIT compiler using copy-and-patch, and (3) tier-switching logic to move between them.

The core idea is the "second Futamura projection" applied at build time: rather than partially evaluating an interpreter at runtime (like PyPy's meta-tracing), Deegen performs deep LLVM IR analysis and transformation at build time. It understands boxing schemes (NaN-boxing, SMI-boxing), automatically generates type-specialized fast paths, and produces inline cache machinery -- all from a single C++ source describing what each bytecode does.

The system generates a continuation-passing interpreter where each bytecode handler is a standalone function with six VM state values pinned to hardware registers via LLVM's GHC calling convention. The baseline JIT is generated using an improved copy-and-patch technique that supports polymorphic inline caching via self-modifying code and hot-cold splitting. A profiling system automatically identifies hot functions and triggers tier-up.

### 2.2 Key Papers and Projects

- **Paper:** Xu & Kjolstad, "Deegen: A JIT-Capable VM Generator for Dynamic Languages" (2024) -- [arxiv](https://arxiv.org/abs/2411.11469) | [HTML](https://arxiv.org/html/2411.11469)
- **Talk:** "Deegen: A Meta-compiler Approach for High Performance VMs at Low Engineering Cost" -- [ACM DL](https://dl.acm.org/doi/10.1145/3605158.3607815)
- **LuaJIT Remake (LJR):** Full Lua 5.1 VM built with Deegen. Interpreter 31% faster than LuaJIT's interpreter. Baseline JIT only 33% slower than LuaJIT's optimizing JIT. [Author's site](https://sillycross.github.io/)

### 2.3 Applicability to GScript

**The short answer: Deegen is architecturally incompatible with GScript.**

The fundamental mismatches:

1. **Language mismatch.** Deegen requires bytecode semantics written in C++ and uses Clang/LLVM IR as its intermediate representation. GScript's runtime is written in Go. Rewriting the entire VM in C++ to use Deegen would be equivalent to starting a new project from scratch.

2. **Architecture mismatch.** Deegen currently only supports x86-64. GScript targets ARM64 (Apple Silicon). While Deegen's approach is portable in principle, the actual implementation includes x86-64-specific details in its stencil generation and calling conventions.

3. **JIT tier mismatch.** Deegen generates a *baseline* JIT (method-level, no optimization beyond dispatch elimination and inline caching). GScript already has a *tracing* JIT with SSA optimization, type specialization, and register allocation -- a fundamentally more advanced compilation tier. Deegen's baseline JIT would be a step backward in code quality.

4. **Interpreter architecture mismatch.** GScript uses Go's goroutine-based execution with Go interfaces and closures. Deegen's continuation-passing style with register-pinned state requires control over the calling convention, which Go does not provide (no GHC convention, no tail call optimization).

**What ideas can be borrowed:**

- **Inline caching architecture.** Deegen's generic IC framework (decomposing cache operations into idempotent computation + cheap execution) is a good model. GScript already has a VM-level inline field cache, but the Deegen approach of self-modifying JIT code for polymorphic ICs is more powerful.
- **Automatic type specialization.** Deegen's approach of analyzing type check patterns in C++ and generating specialized fast/slow paths could inspire a similar analysis of GScript's Go bytecode handlers, done manually.
- **Hot-cold splitting.** Deegen automatically separates hot paths from cold paths in generated code. GScript's trace compiler naturally does this (the trace IS the hot path), but side-exit handlers could benefit from explicit cold placement.

### 2.4 Implementation Difficulty: **Extreme (Not Feasible)**

Adopting Deegen would require rewriting GScript's entire VM in C++. This is not an optimization -- it is a rewrite. The ideas can be borrowed, but the tool itself cannot be used.

### 2.5 Expected Performance Impact

If GScript were rewritten from scratch using Deegen: the baseline JIT alone would be ~33% slower than LuaJIT's optimizing JIT (based on LJR results). But GScript's existing tracing JIT already beats LuaJIT on fib and is within 2-4x on compute benchmarks. The tracing JIT's type specialization gives it an advantage over any baseline-only JIT.

**Net impact of borrowing ideas only:** 5-10% from improved inline caching in the JIT.

### 2.6 Interaction with Existing Optimizations

N/A -- the tool itself is incompatible. Borrowed ideas (IC patterns, type specialization patterns) are complementary to the existing architecture.

### 2.7 Verdict

**Not applicable as a tool. Valuable as a source of ideas.** Read the paper for its inline caching framework and type specialization strategies. Do not attempt to use Deegen directly with GScript's Go runtime.

---

## 3. MLGO -- ML-Driven Compiler Optimization

### 3.1 Principle

MLGO (Machine Learning Guided Compiler Optimizations) is a framework developed at Google for integrating machine learning models into LLVM's optimization passes. Rather than relying on hand-tuned heuristics for decisions like "should this function be inlined?" or "which register should be evicted?", MLGO trains neural networks using reinforcement learning to make these decisions.

The framework works by embedding a trained TensorFlow model (compiled via XLA AOT to avoid runtime TensorFlow dependencies) directly into the LLVM compiler. During compilation, when the compiler reaches a decision point (e.g., inline/no-inline for a call site, or which live range to evict during register allocation), it queries the ML model with features describing the current state and uses the model's output as the decision.

Training uses reinforcement learning with policy gradient and evolution strategies. The training loop iterates between: (1) compile a corpus of programs using the current policy, (2) measure the resulting binary size or performance, (3) update the policy to improve the metric. Training can be done on a single high-performance workstation (96 cores recommended) in about a day. The trained models generalize across different programs -- a model trained on one codebase can improve compilation of unrelated code.

### 3.2 Key Papers and Projects

- **Paper:** Trofin et al., "MLGO: a Machine Learning Guided Compiler Optimizations Framework" (2022) -- [arxiv](https://arxiv.org/abs/2101.04808) | [PDF](https://arxiv.org/pdf/2101.04808)
- **Blog:** [Google Research blog post](https://research.google/blog/mlgo-a-machine-learning-framework-for-compiler-optimization/)
- **Code:** [github.com/google/ml-compiler-opt](https://github.com/google/ml-compiler-opt)
- **LLVM docs:** [MLGO documentation](https://llvm.org/docs/MLGO.html)
- **.NET investigation:** [dotnet/runtime#92915](https://github.com/dotnet/runtime/issues/92915) -- .NET team's evaluation of ML for JIT heuristics

### 3.3 Applicability to GScript

GScript has several heuristic decision points where ML could theoretically improve outcomes:

1. **Register allocation.** GScript's SSA register allocator uses frequency-based heuristics for integer slots (X20-X24) and linear-scan for float refs (D4-D11). An ML model could potentially make better eviction decisions when register pressure is high. However, GScript currently has only 5 integer registers and 8 float registers to allocate -- the decision space is tiny compared to LLVM's general-purpose allocator. The marginal benefit of ML over a simple linear scan on 5-8 registers is minimal.

2. **Trace selection.** When should a loop be traced? When should a trace be blacklisted? GScript currently uses a simple hotness counter (trace after N iterations) and blacklists traces that abort too many times. An ML model trained on GScript benchmarks could potentially find better thresholds and blacklisting criteria. The FORPREP blacklisting heuristic (Lesson 6) was a manual discovery that gave a 4x speedup -- could ML find such heuristics automatically?

3. **Inlining decisions.** GScript's trace compiler currently handles inner loop sub-trace calls but doesn't inline function calls (except self-recursive calls in the method JIT). An ML model could decide which functions to inline during trace compilation.

4. **Guard ordering.** In a trace with multiple type guards, the order of guards affects performance (a failed early guard avoids executing later guards). ML could optimize guard placement.

**Practical barriers:**

- **Training infrastructure.** MLGO requires compiling a large corpus of programs repeatedly with different policies. GScript has a small benchmark suite (8 programs). Training on 8 programs would overfit massively. A much larger corpus of GScript programs would be needed.
- **Model integration.** MLGO is designed for LLVM passes. Integrating a TensorFlow model into a Go-based JIT compiler requires either: (a) calling out to Python/C++ for inference (adding milliseconds of latency per decision), or (b) reimplementing inference in Go (significant effort). Neither is practical for a JIT that compiles in microseconds.
- **Decision frequency.** MLGO optimizes decisions made during ahead-of-time compilation, where compilation time is measured in seconds. GScript's JIT makes decisions in microseconds. Even lightweight ML inference would dominate JIT compilation time.
- **Marginal gains.** MLGO's production results show 0.3-1.5% QPS improvement for register allocation on large datacenter applications. GScript's inner loops are small enough that simple heuristics work well. The 0.3-1.5% improvement range is within noise for GScript's benchmarks.

### 3.4 Implementation Difficulty: **High**

- Training infrastructure: need a large GScript program corpus (does not exist)
- Model integration into Go JIT: significant engineering to avoid adding latency
- Reward signal design: what metric to optimize? (instructions per iteration? total trace execution time?)
- Ongoing maintenance: models need retraining when the compiler changes

### 3.5 Expected Performance Impact

- **Register allocation:** 1-3% improvement (register pressure is low, 5-8 registers, small decision space)
- **Trace selection:** 5-15% potential if ML can discover heuristics like FORPREP blacklisting automatically, but this requires a large training corpus
- **Guard ordering:** 1-2% improvement
- **Net estimate:** 2-5% on benchmarks where register pressure or trace selection is the bottleneck

### 3.6 Interaction with Existing Optimizations

- **Linear-scan allocator:** ML would replace the eviction heuristic, not the overall algorithm
- **Trace blacklisting:** ML would augment/replace the hotness counter and abort-based blacklisting
- **SSA optimization passes:** ML could potentially order passes or tune pass parameters, but this is deep future work

### 3.7 Verdict

**Interesting but premature.** The potential gains (2-5%) do not justify the infrastructure investment at GScript's current stage. The manual heuristics (FORPREP blacklisting, frequency-based allocation) are working well for the current benchmark suite. Revisit when: (1) GScript has a large user-written program corpus for training, (2) the remaining performance gaps are in the 5-10% range rather than 2-7x, and (3) simple heuristic improvements have been exhausted.

**One practical takeaway:** Rather than full ML, consider systematic offline tuning of existing heuristics using a grid search over the parameter space (hotness thresholds, blacklist abort counts, etc.). This gives most of the benefit without the infrastructure cost.

---

## 4. BOLT -- Post-Link Profile-Guided Optimization

### 4.1 Principle

BOLT (Binary Optimization and Layout Tool), developed at Facebook/Meta, is a post-link binary optimizer that uses runtime profile data to reorder basic blocks, functions, and code pages for better instruction cache utilization. Published at CGO 2019, BOLT achieves up to 7% speedup on top of profile-guided optimization (PGO) and link-time optimization (LTO) for data-center applications, and up to 20% for binaries built without PGO.

BOLT works by: (1) disassembling an ELF binary into a control-flow graph, (2) mapping Linux `perf` sample data to basic blocks, (3) reordering blocks so hot paths are contiguous and cold code is split to a separate section, (4) reordering functions so frequently co-called functions are adjacent, and (5) rewriting the binary with the optimized layout. The key insight is that instruction cache misses dominate performance for large code footprints, and profile-guided code layout dramatically reduces icache and iTLB misses (21% and 32% respectively in BOLT's results).

BOLT operates on static binaries (ELF format on x86-64 and AArch64). It cannot optimize dynamically generated code -- as noted in the Chromium case study, BOLT's effect is restricted to just the code inside the binary, and V8's JIT-generated JavaScript code is untouched.

### 4.2 Key Papers and Projects

- **Paper:** Panchenko et al., "BOLT: A Practical Binary Optimizer for Data Centers and Beyond" (CGO 2019) -- [arxiv](https://arxiv.org/abs/1807.06735) | [ACM DL](https://dl.acm.org/doi/10.5555/3314872.3314876)
- **Code:** Now part of LLVM monorepo -- [llvm-project/bolt](https://github.com/llvm/llvm-project/blob/main/bolt/README.md)
- **Chromium case study:** [Optimizing Chromium with BOLT](https://aaupov.github.io/blog/2022/11/12/bolt-chromium) -- showed moderate speedups driven by 21% icache miss reduction
- **Linux kernel:** [BOLT for Linux Kernel](https://lpc.events/event/18/contributions/1921/attachments/1465/3154/BOLT%20for%20Linux%20Kernel%20LPC%202024%20Final.pdf)

### 4.3 Applicability to GScript

**BOLT itself cannot be used** -- it optimizes static binaries, and GScript's JIT generates code at runtime. However, BOLT's principles can be applied directly to JIT-generated code layout.

**BOLT-inspired optimizations for GScript's JIT:**

1. **Hot/cold splitting of trace code.** GScript traces currently have guard-failure handlers (side-exit stubs) interspersed with hot-path code. Moving all side-exit stubs to a separate cold region would make the hot path contiguous in memory, improving icache utilization. Currently, a guard failure at the beginning of a trace has its handler code followed by the rest of the hot path. If the handler is 20 bytes, that is 20 bytes of cold code polluting the icache line.

2. **Trace ordering in the code cache.** When multiple traces are compiled (e.g., outer loop trace + inner loop sub-trace), they should be placed contiguously in memory if they call each other frequently. GScript's current memory allocator (`internal/jit/memory.go`) allocates traces sequentially, which is already somewhat good, but explicit ordering based on the call graph would be better.

3. **Inner loop placement.** For the mandelbrot benchmark, the inner trace (pixel computation) is called 1M times from the outer trace. Placing the inner trace immediately after the outer trace's BLR instruction (or even at a cache-line-aligned boundary) would minimize icache misses on the call. Currently, the inner trace is compiled separately and may be at a distant address.

4. **Basic block reordering within traces.** GScript traces are linear (no branches in the hot path, only guards), so there is not much basic-block reordering to do. However, the guard-failure→side-exit blocks could be reordered to place the most likely failure points first (enabling faster stack unwinding).

5. **Code alignment.** ARM64 benefits from aligning loop headers and hot function entries to 16-byte or 64-byte boundaries (cache line size on Apple M-series). GScript's assembler does not currently align loop back-edge targets.

### 4.4 Implementation Difficulty: **Medium**

The individual optimizations are straightforward:

- **Hot/cold splitting:** Emit all side-exit stubs after the main trace body instead of inline. Requires a two-pass emission (first emit hot path with placeholder branches, then emit cold stubs and patch the branch targets). Estimated: 200-300 lines of code.
- **Trace ordering:** Track which traces call each other, allocate them in the same memory page. Requires a trace-graph data structure and a smarter memory allocator. Estimated: 100-200 lines.
- **Code alignment:** Add `NOP` padding before loop back-edge targets. Trivial: 10-20 lines.
- **Cache-line-aware allocation:** Align trace entries to 64-byte boundaries. Trivial in the memory allocator.

### 4.5 Expected Performance Impact

- **Hot/cold splitting:** 5-10% on mandelbrot (where guard handlers are frequent and pollute icache). Less impact on fib (small trace, fits in L1 icache entirely).
- **Trace ordering:** 3-5% on benchmarks with sub-trace calls (mandelbrot outer+inner). Negligible for single-trace benchmarks.
- **Code alignment:** 2-5% on loop-heavy benchmarks (depends on whether current alignment happens to be lucky or unlucky). Apple M-series cores have 192KB L1i cache, so alignment matters less than on x86.
- **Net estimate:** 10-20% on mandelbrot, 2-5% on other benchmarks. This compounds with trace inlining (Approach C) since inlined traces become one large hot block that benefits from contiguous layout.

### 4.6 Interaction with Existing Optimizations

- **Trace inlining (Approach C):** Highly synergistic. Once inner traces are inlined into outer traces, the resulting large code block benefits enormously from contiguous hot-path layout. Inlining eliminates the BLR overhead; BOLT-style layout eliminates the icache misses from the combined code.
- **Sub-trace calling:** BOLT-style trace ordering directly addresses the sub-trace call overhead (61 instructions of prologue per call). Placing sub-traces adjacently reduces icache misses during the BLR.
- **SSA codegen:** Orthogonal. Code layout optimization happens after code generation.

### 4.7 Verdict

**Do this after trace inlining.** BOLT-style code layout is low-hanging fruit with clear, measurable impact. The implementation is straightforward and the principles are well-understood. Priority:

1. Hot/cold splitting of guard handlers (biggest bang for buck)
2. Cache-line alignment of trace entry points
3. Adjacent allocation of related traces
4. Profile-guided reordering (if tracing data is available)

This should be Phase 5.5 -- after trace inlining but before tackling table-heavy benchmarks.

---

## 5. Pointer Compression (V8-style)

### 5.1 Principle

V8's pointer compression, shipped in Chrome 80 (2020), reduces the size of heap object pointers from 64 bits to 32 bits by constraining all V8 heap objects to a 4GB memory region and storing 32-bit offsets from a base address instead of full 64-bit pointers. This saves memory (up to 43% heap reduction) and improves cache utilization because more values fit in each cache line.

The implementation uses a dedicated "base register" (repurposed from V8's existing root register) that always holds the base address of the 4GB heap region. Decompression is a single ADD instruction: `full_pointer = base_register + sign_extend(compressed_32bit)`. Compression is implicit -- stores simply truncate the upper 32 bits. V8 went through multiple design iterations, including branchful vs branchless decompression, before settling on a "Smi-corrupting" scheme where the base is unconditionally added during decompression (which corrupts the upper 32 bits of Small Integers, but those bits are unused).

The 4GB limitation is important: each V8 isolate (JavaScript execution context) can only address 4GB of heap. For most web pages this is fine; for Node.js server applications it can be a constraint. V8 mitigates this by having each isolate get its own 4GB region, so a process with multiple isolates can use more than 4GB total.

### 5.2 Key Papers and Projects

- **V8 blog:** [Pointer Compression in V8](https://v8.dev/blog/pointer-compression) -- detailed implementation walkthrough
- **V8 Oilpan:** [Pointer compression in Oilpan](https://v8.dev/blog/oilpan-pointer-compression)
- **Node.js impact:** [Halving Node.js Memory Usage](https://blog.platformatic.dev/we-cut-nodejs-memory-in-half)
- **NaN-boxing reference:** [NaN boxing or how to make the world dynamic](https://piotrduperas.com/posts/nan-boxing/) -- complementary technique
- **LuaJIT TValue:** [LuaJIT Source Code Analysis Part 2](https://medium.com/@eclipseflowernju/luajit-source-code-analysis-part-2-data-type-59b501d59e7f) -- 8-byte NaN-boxed values

### 5.3 Applicability to GScript

This is the single most impactful optimization remaining for GScript's table-heavy benchmarks. The current performance gap analysis makes this clear:

**Current state:**
- GScript's `runtime.Value` is 32 bytes: `{typ uint8, [7 padding], data uint64, ptr any}`
- The `ptr any` field is a Go interface (16 bytes: type pointer + data pointer)
- LuaJIT's TValue is 8 bytes (NaN-boxed double)
- This 4x size difference directly explains the 7.5x table ops performance gap (more cache misses, more memory traffic, more GC pressure)

**The progression of Value representations:**

| Representation | Size | GScript Feasibility |
|----------------|------|---------------------|
| Current (typ + data + interface) | 32B | Current state |
| Hybrid 16B (typ + data + raw ptr) | 16B | Planned (use `unsafe.Pointer` instead of `any`) |
| Compressed pointer (typ + data + 32-bit ptr) | 12-16B | Feasible with 4GB heap arena |
| NaN-boxed (double with payload) | 8B | Feasible but invasive |

**Path to 8 bytes:**

1. **Step 1: 32B → 16B (Hybrid).** Replace the `any` interface with an `unsafe.Pointer` and a type tag. This is already planned. The Value becomes `{typ uint8, [7 padding], data uint64}` where `data` holds either a numeric value or a pointer (type-punned). This halves the Value size immediately.

2. **Step 2: 16B → 8B (NaN-boxing).** Use the IEEE-754 NaN space to encode type information. A valid float64 uses all 64 bits. A quiet NaN has the exponent bits all-1 and the quiet bit set, leaving 51 bits for a payload. On ARM64 with a 48-bit virtual address space (or 47-bit user space), a pointer fits in the NaN payload. The encoding:
   - Doubles: stored directly (all 64 bits)
   - Integers: upper 13 bits = `0xFFF8` + tag, lower 51 bits = value (supports 51-bit integers, or store 32-bit int + use remaining bits for tag)
   - Pointers (strings, tables, functions): upper 13 bits = `0xFFF8` + tag, lower 47 bits = pointer
   - Nil, bool: special NaN patterns

3. **Step 2b: Pointer Compression within NaN-boxing.** If all GScript heap objects are allocated within a 4GB arena (using `mmap` with a fixed base), pointers need only 32 bits of the NaN payload. This leaves 19 bits for type tags and metadata, giving a very flexible encoding.

**Go-specific challenges:**

- **Go GC interaction.** Go's garbage collector scans memory for pointers. NaN-boxed pointers are invisible to the GC because they are stored in `uint64` fields, not `unsafe.Pointer` fields. This means GScript would need its own GC for script-level objects, or use a write barrier to maintain a set of roots visible to Go's GC.
- **unsafe.Pointer rules.** Go's `unsafe.Pointer` rules prohibit storing pointers in integer types (the GC may move or collect them). NaN-boxing fundamentally violates this rule. Solutions: (a) use `runtime.KeepAlive` and a global root set, (b) allocate script objects in non-GC memory (mmap'd arena) and manage lifetime manually, (c) use CGo to allocate objects outside Go's heap.
- **Arena allocation.** For pointer compression, GScript needs a custom allocator that maps a contiguous 4GB region with `mmap`. Go's `syscall.Mmap` can do this. Objects allocated in this arena are outside Go's GC, requiring manual reference counting or a custom mark-sweep GC.

**LuaJIT's approach for reference:**
LuaJIT uses NaN-boxing with 8-byte TValues. In GC64 mode (64-bit GC references), the upper 13 bits must be `0xFFF8...` for a special NaN, the next 4 bits hold the internal tag, and the lowest 47 bits hold a pointer or zero-extended 32-bit integer. Numbers use all 64 bits as a double. This is the gold standard and exactly what GScript should aim for.

### 5.4 Implementation Difficulty: **High**

This is the "Season 2" rewrite described in GScript's roadmap. It touches every file in the project.

- **Phase A (16B hybrid):** Replace `any` with `unsafe.Pointer` + type tag. Every Value creation, comparison, and access site must change. JIT codegen constants change (ValueSize 32→16, offsets change). Estimated: 1-2 weeks.
- **Phase B (8B NaN-boxing):** Redesign the Value type entirely. All arithmetic, comparison, and type-check code changes. JIT codegen completely reworked (no more separate typ/data/ptr fields -- everything is bitwise operations on a uint64). Custom memory arena for script objects. Estimated: 3-6 weeks.
- **Phase C (Pointer compression):** Add 4GB arena allocation and 32-bit pointer encoding within NaN-boxed values. This is an incremental improvement on Phase B. Estimated: 1-2 weeks after Phase B.

### 5.5 Expected Performance Impact

This is where the numbers get exciting:

- **Table ops:** 2-4x improvement. Going from 32B to 8B Values means 4x more values fit in each cache line. Table array access drops from 32-byte stride to 8-byte stride. Hash table density quadruples. The 7.5x gap to LuaJIT becomes 2-3x (with the remaining gap due to other factors like hash function quality and GC differences).
- **Mandelbrot:** 10-20% improvement. The inner loop operates on registers (already unboxed), but loop prologue/epilogue that loads/stores Values from memory benefits from smaller Values.
- **All benchmarks:** 10-30% improvement from reduced memory traffic and cache pressure across the board.
- **Memory usage:** 50-75% reduction in heap usage for Value-heavy programs. This also reduces GC pressure.

### 5.6 Interaction with Existing Optimizations

- **JIT codegen:** Must be completely reworked. The current `ValueSize=32`, `OffsetTyp=0`, `OffsetData=8`, `OffsetPtr=16` constants in `value_layout.go` would change to a single 8-byte value with bitwise extraction. Type checks become bitmask operations on the NaN bits rather than `LDRB` of a type tag.
- **SSA type specialization:** Becomes more important. With NaN-boxing, the hot path for float operations is a no-op (the Value IS a double). Integer operations need to extract the 32-bit or 51-bit integer from the NaN payload. Type guards change from comparing a byte to checking bit patterns.
- **Register allocation:** Simpler. A Value fits in a single 64-bit register. No more separate typ/data/ptr loading. Integer and float operations both work on the same register (with different extraction patterns).
- **Inline field cache:** More effective. Smaller Values mean more of the table's skeys and svals arrays fit in L1 cache, making the inline cache hit rate higher.

### 5.7 Verdict

**Must-do. This is the single highest-impact optimization for GScript.** The 7.5x table ops gap and the general memory overhead of 32-byte Values are the biggest remaining barriers to LuaJIT-level performance. The implementation is large but well-understood (LuaJIT, SpiderMonkey, and JavaScriptCore all use NaN-boxing successfully).

**Recommended approach:**
1. Do Phase A (16B hybrid) first as a stepping stone
2. Then Phase B (8B NaN-boxing) as the main effort
3. Phase C (pointer compression) is optional -- NaN-boxing already gets to 8B, and V8-style compressed pointers add complexity for marginal gain on top of NaN-boxing

---

## Cross-Technique Interactions

### Synergies

| Technique A | Technique B | Synergy |
|-------------|-------------|---------|
| NaN-boxing | BOLT-style layout | Smaller Values → more code fits in icache → layout matters less, but still helps |
| Trace inlining | BOLT-style layout | Inlined traces are larger → layout optimization matters MORE |
| Copy-and-patch stencils | NaN-boxing | Stencils for NaN-box/unbox sequences → Clang can optimize bitwise extraction |
| BOLT-style layout | Copy-and-patch | Hot/cold splitting is orthogonal to code quality; both help independently |

### Conflicts

| Technique A | Technique B | Conflict |
|-------------|-------------|---------|
| Copy-and-patch stencils | SSA register allocation | Stencils assume fixed register conventions; SSA allocator is dynamic |
| NaN-boxing | Go GC | NaN-boxed pointers invisible to Go's GC; requires custom memory management |
| MLGO | JIT compilation speed | ML inference adds latency incompatible with microsecond JIT compilation |

---

## Recommended Roadmap

Based on this analysis, here is the recommended order of investment:

### Phase 5 (Current): Trace Inlining
Already planned. Inner trace code inlining (Approach C) to eliminate the 61-instruction prologue overhead. Expected: mandelbrot 2-3x improvement.

### Phase 5.5: BOLT-Style Code Layout
Low-hanging fruit after trace inlining:
1. Hot/cold split guard handlers out of the main trace body
2. Cache-line-align trace entry points
3. Place related traces adjacently in the code cache

Expected: 10-20% on top of trace inlining.

### Phase 6 (Season 2): NaN-Boxing
The big rewrite:
1. Step 1: Value 32B → 16B (hybrid unsafe.Pointer)
2. Step 2: Value 16B → 8B (NaN-boxing with custom arena allocator)
3. Rework all JIT codegen for 8-byte Values

Expected: 2-4x improvement on table-heavy benchmarks, 10-30% on compute benchmarks.

### Phase 7 (Selective): Copy-and-Patch Templates
After NaN-boxing stabilizes, evaluate whether specific hot sequences (NaN-box/unbox, type guards, math intrinsics) would benefit from Clang-optimized stencils:
1. Build a stencil generation pipeline with `go generate` + Clang
2. Replace the top 5-10 hottest codegen patterns with stencils
3. Benchmark each replacement individually

Expected: 5-15% on specific patterns.

### Deferred: MLGO
Revisit when GScript has a large enough program corpus and the remaining gaps are in the single-digit percent range.

### Not Recommended: Deegen
Architectural mismatch. Read the paper for ideas, do not attempt adoption.

---

## Sources

### Copy-and-Patch
- [Xu & Kjolstad, "Copy-and-Patch Compilation" (OOPSLA 2021)](https://dl.acm.org/doi/10.1145/3485513)
- [Copy-and-Patch Paper PDF](https://fredrikbk.com/publications/copy-and-patch.pdf)
- [CPython 3.13 JIT Blog Post](https://tonybaloney.github.io/posts/python-gets-a-jit.html)
- [PEP 744 -- JIT Compilation](https://peps.python.org/pep-0744/)
- [WasmNow GitHub](https://github.com/sillycross/WasmNow)
- [Copy-and-Patch Wikipedia](https://en.wikipedia.org/wiki/Copy-and-patch)
- [Worked Example of Copy-and-Patch](https://scot.tg/2024/12/22/worked-example-of-copy-and-patch-compilation/)
- [copyjit (architecture-agnostic attempt)](https://github.com/Kimplul/copyjit)

### Deegen
- [Xu & Kjolstad, "Deegen: A JIT-Capable VM Generator" (2024)](https://arxiv.org/abs/2411.11469)
- [Deegen HTML version](https://arxiv.org/html/2411.11469)
- [Haoran Xu's site](https://sillycross.github.io/)
- [Building a baseline JIT for Lua automatically](https://sillycross.github.io/2023/05/12/2023-05-12/)
- [Building the fastest Lua interpreter automatically](https://sillycross.github.io/2022/11/22/2022-11-22/)

### MLGO
- [Trofin et al., "MLGO" (2022)](https://arxiv.org/abs/2101.04808)
- [Google Research Blog](https://research.google/blog/mlgo-a-machine-learning-framework-for-compiler-optimization/)
- [GitHub: google/ml-compiler-opt](https://github.com/google/ml-compiler-opt)
- [LLVM MLGO Documentation](https://llvm.org/docs/MLGO.html)
- [.NET Runtime ML Investigation](https://github.com/dotnet/runtime/issues/92915)

### BOLT
- [Panchenko et al., "BOLT" (CGO 2019)](https://arxiv.org/abs/1807.06735)
- [BOLT in LLVM](https://github.com/llvm/llvm-project/blob/main/bolt/README.md)
- [Optimizing Chromium with BOLT](https://aaupov.github.io/blog/2022/11/12/bolt-chromium)
- [BOLT for Linux Kernel (LPC 2024)](https://lpc.events/event/18/contributions/1921/attachments/1465/3154/BOLT%20for%20Linux%20Kernel%20LPC%202024%20Final.pdf)

### Pointer Compression / NaN-Boxing
- [Pointer Compression in V8](https://v8.dev/blog/pointer-compression)
- [Pointer Compression in Oilpan](https://v8.dev/blog/oilpan-pointer-compression)
- [Halving Node.js Memory Usage](https://blog.platformatic.dev/we-cut-nodejs-memory-in-half)
- [NaN Boxing Explained](https://piotrduperas.com/posts/nan-boxing/)
- [LuaJIT TValue Analysis](https://medium.com/@eclipseflowernju/luajit-source-code-analysis-part-2-data-type-59b501d59e7f)
- [Value Representation in JavaScript Implementations](https://wingolog.org/archives/2011/05/18/value-representation-in-javascript-implementations)
- [Dynamic Typing and NaN Boxing](https://leonard.swiss/blog/nan-boxing/)
