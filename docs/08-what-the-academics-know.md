---
layout: default
title: "What the Academics Know That We Don't"
permalink: /08-what-the-academics-know
---

# What the Academics Know That We Don't

*March 2026 --- Beyond LuaJIT, Post #8*

## Where We Are

In [Post #7](07-the-day-we-beat-luajit), we beat LuaJIT on fib(20). 24us vs 26us. The method JIT, running code compiled from Go-syntax scripts on Apple Silicon, outperformed Mike Pall's hand-tuned trace compiler on the canonical recursive benchmark. It was a good day.

Here is the full scoreboard as it stands:

| Benchmark | GScript | LuaJIT | Ratio | Status |
|-----------|---------|--------|-------|--------|
| fib(20) | 24us | 26us | 0.92x | **GScript wins** |
| ackermann(3,11) | 17us | ~17us | ~1.0x | Tied |
| callMany (fn calls) | 5.1us | 3us | 1.7x | LuaJIT leads |
| mandelbrot(1000) | 0.23s | 0.056s | 4.0x | LuaJIT leads |
| table ops (nbody) | 268us | 36us | 7.5x | LuaJIT leads |

Two wins. Three gaps. And the nature of the remaining gaps has changed.

Through seven posts and nine phases of optimization, we have been working from the bottom up. Count instructions. Eliminate redundant loads. Pin registers. Propagate constants. Kill dead stores. This approach took fib from 53us to 24us --- more than a 2x improvement through sheer instruction-level grinding. It is satisfying work: you look at the disassembly, you see the waste, you remove it.

But the approach has a ceiling.

mandelbrot's inner loop is 26 instructions per iteration. The theoretical minimum is around 15. We could shave a few more instructions with better scheduling, maybe get to 22 or 23. That is a 10-15% improvement. The gap is 4.0x. Instruction-level hand-tuning will not close a 4.0x gap.

Table operations are 7.5x behind. The inner loop instruction count is almost irrelevant --- the bottleneck is that every table read moves 32 bytes of data instead of LuaJIT's 8. No amount of register pinning or constant propagation fixes a 4x data representation overhead.

We have exhausted the "look at the assembly and eliminate waste" approach. What is left requires thinking about the problem differently. Which means it is time to look up from the trenches and ask: what do the people who study this full-time know that we do not?

We surveyed five techniques from recent academic papers and production compilers. Some turned out to be immediately actionable. Some turned out to be fascinating but irrelevant. One turned out to be the single most important thing we could do.

## Copy-and-Patch: The Template JIT

The first technique comes from Haoran Xu and Fredrik Kjolstad's [OOPSLA 2021 Distinguished Paper](https://dl.acm.org/doi/10.1145/3485513). The idea is elegant: instead of emitting machine code one instruction at a time through a custom assembler, you pre-compile binary code templates ("stencils") at build time using a real optimizing compiler, then at runtime you just `memcpy` the stencil and patch in the runtime-specific values.

The insight is that most bytecode operations have a fixed structure with only a few moving parts. A type guard followed by an integer addition followed by a store --- the shape is always the same, only the register offsets and jump targets change. So you write the template in C, compile it through Clang/LLVM at build time, extract the resulting binary blob, and at runtime you copy the blob and fill in the "holes" where addresses and constants go.

CPython 3.13 shipped this as its [experimental JIT compiler](https://peps.python.org/pep-0744/). The WasmNow project used it to build a WebAssembly baseline compiler that is [5-6.5x faster at compilation than Chrome's Liftoff](https://github.com/sillycross/WasmNow) while generating code that runs 39-63% faster. It works.

### What it could do for GScript

Three places where copy-and-patch could help immediately:

**Guard sequences.** GScript's trace compiler emits multi-instruction sequences for guarded operations --- check the type tag, load the value, do the arithmetic, store it back. Each of these could be a pre-compiled stencil. Clang knows things about the Apple M-series pipeline that we do not: instruction fusion opportunities, optimal scheduling for the Firestorm cores, branch prediction interactions. A stencil compiled by Clang at `-O3` would probably schedule our guard sequences better than our hand-written assembler does.

**Prologue and epilogue code.** The 61-instruction sub-trace call prologue is a perfect stencil candidate. It is the same sequence every time, with different register offsets. Clang could reduce pipeline stalls in the spill/reload sequences that we might be missing.

**Complex multi-instruction patterns.** Float multiply-add chains, integer comparison cascades, NaN-box/unbox sequences (when we eventually implement NaN-boxing) --- all have a fixed structure with variable operands.

### Where it breaks down

The problem is that copy-and-patch is fundamentally a *per-operation* technique. It optimizes individual bytecode handlers in isolation. GScript's SSA pipeline already optimizes *across* operations --- common subexpression elimination, constant hoisting, and register allocation all work on the full trace, not on individual instructions. Stencils cannot cross operation boundaries.

This matters. When our SSA optimizer sees that the same type guard appears three times in a loop body, it eliminates the redundant checks. A stencil-based system would emit all three guard sequences because it cannot see across stencil boundaries.

Register allocation is another conflict. Stencils assume fixed register conventions --- the stencil was compiled with specific registers in specific roles. GScript's linear-scan allocator assigns registers dynamically based on live ranges. To use stencils, we would either need parameterized register variants (explosion in stencil count) or a fixed register convention (losing the allocator's optimization).

### Verdict

**Adopt ideas selectively; do not replace the assembler.** The best approach is a hybrid: use stencils for the "cold" parts of traces --- prologues, epilogues, side-exit handlers --- where our SSA optimizer provides no benefit anyway. Keep the custom assembler for hot inner loop bodies where cross-operation optimization matters.

Expected impact: 5-15% on mandelbrot from better-scheduled guard and prologue sequences. Negligible on fib (already beating LuaJIT). The implementation is medium-difficulty --- it requires a `go generate` pipeline that invokes Clang to produce stencil libraries at build time, plus careful handling of ARM64's position-independent code requirements (the [`copyjit`](https://github.com/Kimplul/copyjit) project confirmed that AArch64 stencils need `-mcmodel=tiny`).

Useful, but not transformative. This is an optimization, not an architecture change.

## Deegen: Write the Interpreter, Get a JIT for Free

This one is from the same authors --- Xu and Kjolstad again, [published in 2024](https://arxiv.org/abs/2411.11469), appearing at PLDI 2025. Deegen is a meta-compiler. You write bytecode execution semantics as C++ functions, and Deegen automatically generates three things: a high-performance threaded interpreter with register pinning and inline caching, a baseline JIT using copy-and-patch, and tier-switching logic to move between them.

The core idea is the "second Futamura projection" applied at build time. Rather than partially evaluating an interpreter at runtime (like PyPy's meta-tracing), Deegen performs deep LLVM IR analysis at build time. It understands boxing schemes. It automatically generates type-specialized fast paths. It produces inline cache machinery. All from a single C++ source per bytecode.

The results are impressive. The "LuaJIT Remake" (LJR) built with Deegen has an interpreter 31% faster than LuaJIT's, and a baseline JIT only 33% slower than LuaJIT's optimizing JIT --- with a fraction of the engineering effort.

### Why we cannot use it

The short answer: architectural mismatch at every level.

**Language.** Deegen requires bytecode semantics in C++. GScript's runtime is in Go. Using Deegen would mean rewriting the entire VM from scratch.

**Platform.** Deegen targets x86-64. GScript targets ARM64. The implementation includes x86-specific details in its stencil generation and calling conventions.

**JIT tier.** Deegen generates a *baseline* JIT --- method-level, no optimization beyond dispatch elimination and inline caching. GScript already has a *tracing* JIT with SSA optimization, type specialization, and register allocation. Deegen's output would be a step backward in code quality for our hot loops.

**Calling convention.** Deegen's continuation-passing interpreter needs GHC calling convention and tail call optimization --- features that Go does not provide.

### What we can steal

The paper is still worth reading for two ideas:

1. **Inline caching architecture.** Deegen decomposes inline cache operations into an idempotent computation phase and a cheap execution phase, then generates self-modifying JIT code for polymorphic inline caches. GScript has a VM-level inline field cache, but the Deegen approach of patching the JIT code itself to cache field offsets is more powerful. Worth exploring when we work on table operation performance.

2. **Hot-cold splitting.** Deegen automatically separates hot paths from cold paths in generated code. This overlaps with the BOLT-style layout optimization we will discuss below.

### Verdict

**Not applicable as a tool. Valuable as a source of ideas.** Read the paper; do not attempt adoption.

## MLGO: When Google Points Machine Learning at Compilers

Google's MLGO framework ([Trofin et al., 2022](https://arxiv.org/abs/2101.04808)) integrates trained neural networks directly into LLVM's optimization passes. Instead of hand-tuned heuristics for "should this function be inlined?" or "which register should be evicted?", MLGO trains models using reinforcement learning and deploys them inside the compiler.

The technical approach is sound. They compile a TensorFlow model via XLA ahead-of-time to avoid runtime TF dependencies, embed it in LLVM, and query it at each decision point. Training uses policy gradient methods and evolution strategies over a large program corpus. The trained models generalize --- a model trained on one codebase improves compilation of unrelated code.

Google deployed this in production for LLVM inlining decisions. The result: **0.3-1.5% QPS improvement** on data-center applications. They also applied it to register allocation with similar modest gains.

### Why it does not make sense for us

The numbers tell the story. 0.3-1.5% improvement on Google-scale applications, where shaving half a percent off a fleet of millions of servers saves real money. For GScript, where the remaining gaps are measured in multiples, not percentages, this is a sledgehammer applied to a thumbtack.

Beyond the scale mismatch, there are practical barriers:

**Decision space is tiny.** GScript's register allocator manages 5 integer registers (X20-X24) and 8 float registers (D4-D11). The decision space is so small that a linear scan with frequency heuristics already finds near-optimal solutions. ML shines when the decision space is enormous and the interactions are complex. Ours is not.

**No training corpus.** MLGO requires compiling a large corpus of programs repeatedly with different policies. GScript has 8 benchmarks. Training on 8 programs would overfit catastrophically.

**Latency budget.** MLGO is designed for ahead-of-time compilation where compile time is measured in seconds. GScript's JIT compiles traces in microseconds. Even lightweight ML inference would dominate compilation time.

**Marginal gains.** Even in the best case --- 5-15% improvement on trace selection heuristics --- the infrastructure cost is enormous. Building and maintaining a training pipeline, curating a program corpus, retraining when the compiler changes.

There is one practical takeaway: rather than full ML, systematic offline tuning of existing heuristics using grid search over the parameter space (hotness thresholds, blacklist abort counts, spill heuristics) gives most of the benefit without the infrastructure. We have already done a version of this --- the FORPREP blacklisting discovery from [Post #5](05-the-blacklist-that-changed-everything) was exactly this kind of heuristic improvement, found manually rather than by ML, and it gave a 4x speedup.

### Verdict

**Interesting but premature.** Revisit when three conditions hold: (1) GScript has a large user-written program corpus, (2) the remaining performance gaps are in single-digit percentages, and (3) simple heuristic improvements have been exhausted. We are nowhere near any of these conditions.

## BOLT: Rearranging the Binary After the Fact

BOLT (Binary Optimization and Layout Tool), developed at Meta and [published at CGO 2019](https://arxiv.org/abs/1807.06735), takes a fundamentally different approach from the techniques above. It does not improve the *quality* of generated code. It improves the *layout* of the code in memory.

The principle: BOLT takes a compiled binary, profiles it with Linux `perf`, then rearranges basic blocks and functions so that hot code is contiguous and cold code is pushed to separate memory pages. The result: 21% fewer instruction cache misses, 32% fewer iTLB misses, up to 7% speedup on top of PGO and LTO for data-center binaries, and up to 20% for binaries without PGO.

This works because modern CPUs fetch instructions in cache lines (64 bytes on Apple M-series). If a 40-byte hot path has a 20-byte cold error handler in the middle of it, that handler pollutes the cache line. Move the handler elsewhere, and the hot path fits in one cache line instead of two. Multiply this across thousands of basic blocks, and the icache hit rate improves dramatically.

BOLT itself operates on static ELF binaries --- it cannot optimize JIT-generated code directly. But its *principles* apply perfectly to a JIT.

### What we can do right now

**Hot/cold splitting of guard handlers.** This is the single most actionable idea from the entire survey. GScript traces currently have guard-failure handlers (side-exit stubs) interspersed with hot-path code. A type guard emits: compare, conditional branch to handler, handler code (restore state, jump to interpreter), then continue with the hot path. The handler is cold code that almost never executes, but it sits in the middle of the hot path, pushing subsequent hot instructions into the next cache line.

The fix: emit all side-exit stubs *after* the main trace body. The hot path becomes a contiguous stream of instructions with only the conditional branches remaining (which the branch predictor handles well since guards almost always pass). The cold handlers cluster at the end, out of the icache's way. Implementation: a two-pass emission --- first pass emits the hot path with placeholder branches, second pass emits cold stubs and patches the branch targets. Estimated effort: 200-300 lines of code.

**Cache-line alignment.** ARM64 benefits from aligning loop headers and trace entry points to 64-byte boundaries (Apple M-series cache line size). GScript's assembler does not currently do this. Adding NOP padding before trace entries is trivial --- 10-20 lines.

**Trace adjacency.** When an outer trace calls an inner trace (as in mandelbrot), placing them contiguously in memory reduces icache misses at the call boundary. GScript's memory allocator currently places traces sequentially, which may or may not result in adjacency. Explicit co-location based on the call graph requires a smarter allocator --- about 100-200 lines.

### Expected impact

This is where BOLT-style layout becomes compelling:

- **Hot/cold splitting:** 5-10% on mandelbrot, where guard handlers are frequent and pollute the icache.
- **Trace adjacency:** 3-5% on benchmarks with sub-trace calls (mandelbrot's inner+outer loops).
- **Cache-line alignment:** 2-5% depending on current alignment luck.
- **Combined:** 10-20% on mandelbrot. Less on single-trace benchmarks like fib (small traces that already fit in L1i).

The combined estimate of 10-20% is real, tractable, and synergistic with trace inlining. Once inner traces are inlined into outer traces (eliminating the 61-instruction prologue), the resulting larger code block benefits even more from contiguous layout. The two optimizations compound.

### Verdict

**The most immediately actionable "academic" technique.** Low implementation cost, clear measurement methodology, well-understood principles. Priority: after trace inlining stabilizes, implement hot/cold splitting first, then alignment, then adjacency.

## The Real Answer: NaN-Boxing and Pointer Compression

The four techniques above are optimizations. They trim percentages. They smooth edges. They matter, but none of them fundamentally changes the game.

This one does.

GScript's `runtime.Value` is 32 bytes:

```go
type Value struct {
    typ  uint8           //  1 byte  + 7 padding
    data uint64          //  8 bytes
    ptr  any             // 16 bytes (Go interface = type ptr + data ptr)
}
```

LuaJIT's TValue is 8 bytes. One NaN-boxed double.

That 4x size difference is the root cause of both remaining major gaps. For table operations, every array access reads 32 bytes instead of 8 --- 4x the memory traffic, 4x the cache pressure, 4x fewer values per cache line. The 7.5x table-ops gap starts here. For mandelbrot, the loop prologue and epilogue that load and store Values from memory carry 4x the data, and the GC scans 4x more pointers. Part of the 4.0x gap starts here too.

No amount of instruction scheduling, register pinning, or code layout will fix a 4x data representation overhead.

### How NaN-boxing works

IEEE-754 floating-point doubles use 64 bits. Most bit patterns represent valid numbers. But there is a special region: if the exponent bits are all 1s and the mantissa is non-zero, the value is NaN (Not a Number). There are two kinds of NaN --- signaling and quiet --- and only one bit distinguishes them. This leaves 51 bits of mantissa in a quiet NaN that the hardware ignores.

NaN-boxing exploits this: store actual doubles as themselves (all 64 bits), and encode everything else --- integers, pointers, booleans, nil --- as special NaN patterns with type tags in the upper bits and payloads in the lower bits.

On ARM64, with a 48-bit virtual address space (47-bit user space), a pointer fits comfortably in the NaN payload. The encoding:

- **Doubles:** stored directly, all 64 bits.
- **Integers:** upper 13 bits = `0xFFF8` + type tag, lower 51 bits = value.
- **Pointers** (strings, tables, functions): upper 13 bits = `0xFFF8` + type tag, lower 47 bits = pointer address.
- **Nil, booleans:** special NaN bit patterns.

This is exactly what LuaJIT does. SpiderMonkey does it. JavaScriptCore does it. It is the industry standard for dynamically-typed language runtimes that care about performance.

### V8's pointer compression: the other half

V8 took a different but complementary approach in [Chrome 80](https://v8.dev/blog/pointer-compression). Instead of encoding everything in 64 bits, they compressed heap pointers from 64 bits to 32 bits by constraining all V8 heap objects to a 4GB region and storing 32-bit offsets from a base address. Decompression is a single ADD: `full_pointer = base + sign_extend(compressed_32bit)`.

The result: up to [43% heap reduction](https://blog.platformatic.dev/we-cut-nodejs-memory-in-half), dramatically better cache utilization, meaningful speedups on real-world workloads.

For GScript, pointer compression is the natural extension of NaN-boxing: if all script objects are allocated within a 4GB arena (via `mmap`), pointers need only 32 bits in the NaN payload, leaving 19 bits for type tags and metadata. This gives a very flexible encoding with room to spare.

### The Go problem

There is one major obstacle, and it is not algorithmic --- it is our host language.

Go's garbage collector scans memory for pointers. NaN-boxed pointers are invisible to the GC because they are stored in `uint64` fields, not `unsafe.Pointer` fields. Go's `unsafe.Pointer` rules explicitly prohibit storing pointers in integer types --- the GC may collect objects it cannot see.

This means GScript cannot simply NaN-box pointers into uint64 values and hope for the best. The GC will collect the objects those pointers reference. We need one of:

1. **A global root set** --- maintain a Go-visible data structure that holds `unsafe.Pointer` references to all live script objects, updated by a write barrier. The NaN-boxed integers are the fast-path representation; the root set keeps the GC informed.

2. **A custom arena allocator** --- allocate all script objects in a memory-mapped region outside Go's heap (`syscall.Mmap`). Objects in this arena are invisible to Go's GC by design, so we manage their lifetime ourselves with manual reference counting or a custom mark-sweep collector.

3. **A hybrid** --- use Go's GC for the runtime infrastructure (goroutines, channels, the compiler itself) and a custom allocator for script-level values and objects.

Option 2 is the cleanest for performance but the most work. It is also what every production language runtime does --- LuaJIT, V8, SpiderMonkey, and JavaScriptCore all manage their own heaps.

### The path from 32 bytes to 8

The migration has natural staging points:

| Step | Value Size | Change | Impact |
|------|-----------|--------|--------|
| Current | 32 bytes | --- | Baseline |
| Hybrid 16B | 16 bytes | Replace `any` interface with `unsafe.Pointer` + type tag | 2x density, touches every Value creation site |
| NaN-boxed | 8 bytes | Full NaN-boxing with custom arena | 4x density, requires custom GC |

Step 1 (32B to 16B) is already planned. The `any` interface in Go is 16 bytes (type pointer + data pointer); replacing it with a raw `unsafe.Pointer` and using the existing `typ` byte for type dispatch cuts the Value in half. Every Value creation, comparison, and access site must change. JIT codegen constants change (`ValueSize` from 32 to 16, all offsets recalculated). This is a significant refactor but mechanically straightforward.

Step 2 (16B to 8B) is the architectural change. The Value type becomes a single `uint64`. All arithmetic, comparison, and type-check code changes. JIT codegen is completely reworked --- no more separate typ/data/ptr fields, everything becomes bitwise operations. Type checks become bitmask comparisons on the NaN bits instead of `LDRB` of a type tag. And a custom memory arena replaces Go's heap for script objects.

### What changes in the JIT

With NaN-boxing, the JIT codegen transforms fundamentally:

**Before** (32-byte Value):
```arm64
// Load integer from Value array
LDRB  W_tmp, [X26, #slot*32+0]   // load type tag
CMP   W_tmp, #TypeInt             // check type
B.NE  guard_fail                  // guard
LDR   X_val, [X26, #slot*32+8]   // load data field
```

**After** (8-byte NaN-boxed):
```arm64
// Load integer from Value array
LDR   X_val, [X26, #slot*8]      // load entire NaN-boxed value
LSR   X_tmp, X_val, #47           // extract tag bits
CMP   X_tmp, #IntTag              // check type
B.NE  guard_fail                  // guard
AND   X_val, X_val, #0x7FFFFFFFFFFF  // mask to 47-bit payload
```

The instruction count is similar, but the memory access pattern is 4x denser. Where before we loaded 32 bytes per value, we now load 8. Four values fit in a single cache line instead of one. For table operations iterating over arrays, this is the difference between streaming through memory at 32 bytes per element and 8 bytes per element.

For float operations, NaN-boxing is even better --- the hot path is a no-op. The Value *is* a double. No unboxing needed. Load it, compute with it, store it. LuaJIT gets this for free, and it is a big part of why mandelbrot is so fast there.

### The numbers

Here is what the size reduction buys:

- **Table ops:** 2-4x improvement. The 7.5x gap to LuaJIT becomes 2-3x (with the remaining gap from hash function quality, GC differences, and inline cache sophistication).
- **mandelbrot:** 10-20% improvement from reduced memory traffic in loop prologue/epilogue. The bigger mandelbrot gains come from trace inlining (eliminating the 61-instruction sub-trace call overhead), but NaN-boxing helps on top of that.
- **All benchmarks:** 10-30% improvement from reduced cache pressure.
- **Memory usage:** 50-75% heap reduction for Value-heavy programs, plus lower GC pressure.

### Verdict

**This is the single most important optimization remaining for GScript.** Not because the implementation is easy --- it is the hardest thing on the list. But because it is the only technique that changes the fundamental constants of the system. Every other optimization trims overhead. NaN-boxing changes the data model.

The 7.5x table-ops gap and the 4.0x mandelbrot gap both trace back to the same root: our Value is 4x bigger than LuaJIT's. Fix the Value, and everything downstream improves.

## The Priority Stack

Here are all five techniques ranked by impact-to-effort ratio, with honest assessments:

| Priority | Technique | Expected Impact | Effort | Why |
|----------|-----------|----------------|--------|-----|
| 1 | **NaN-boxing** | 2-4x on table ops, 10-30% everywhere | High | Changes the fundamental data model. The only technique that addresses the root cause of both major gaps. |
| 2 | **BOLT-style layout** | 10-20% on mandelbrot | Low-Medium | Well-understood, ~500 lines total. Compounds with trace inlining. |
| 3 | **Copy-and-patch (selective)** | 5-15% on specific sequences | Medium | Useful for cold code paths. Our SSA optimizer already handles hot loops better than stencils could. |
| 4 | **MLGO** | 2-5% | Very High | Sledgehammer for a thumbtack. Our decision spaces are too small and our gaps too large for ML-guided heuristics to matter. |
| 5 | **Deegen** | N/A | Impossible | Requires rewriting the VM in C++ for x86-64. Architecturally incompatible. Read the paper, do not use the tool. |

The honest conclusion: NaN-boxing is the only technique on this list that fundamentally changes the game. BOLT-style layout is the best bang-for-buck incremental win. The rest are interesting reading but not where our effort should go.

This is the difference between optimization and architecture. We have spent seven posts optimizing --- squeezing instructions out of hot loops, pinning registers, propagating constants. That work was necessary and it got us to where we are. But the remaining gaps are not instruction-level problems. They are data-representation problems. And data-representation problems require architectural changes.

The academics know this. Every major language runtime went through the same evolution. V8 started with tagged pointers, went to NaN-boxing (via "SMI" tagging), then added pointer compression. SpiderMonkey NaN-boxes everything. LuaJIT has used NaN-boxed TValues from the beginning --- Mike Pall understood that getting the Value representation right is prerequisite to everything else. We are arriving at the same conclusion from the other direction, having first built the JIT and now discovering that the JIT's speed is bottlenecked by the data it operates on.

## What We Are Going to Do

The plan, in order:

**First: 16-byte Hybrid Value.** Replace the `any` interface with `unsafe.Pointer` + type tag. Value drops from 32 bytes to 16. This is already in progress. It is a mechanical refactor --- every Value creation site changes, JIT constants update, tests verify. The 2x density improvement should be visible immediately on table-heavy benchmarks.

**Second: BOLT-style code layout.** Hot/cold splitting of guard handlers, cache-line alignment of trace entries, co-location of related traces. 200-500 lines of code, 10-20% expected gain on mandelbrot. This can happen in parallel with the Value refactor since it touches different parts of the codebase.

**Third: evaluate whether 16 bytes is enough.** If the 16-byte Value closes enough of the table-ops gap (7.5x to 3-4x), we may defer full NaN-boxing. If the gap remains stubbornly large, we proceed to the full 8-byte NaN-boxed Value with custom arena allocation. This is the "Season 2" rewrite --- it touches every file, requires a custom GC, and fundamentally changes the JIT's codegen. Worth it, but only after the cheaper options are exhausted.

The copy-and-patch ideas go on the shelf for now. When we eventually do NaN-boxing, Clang-optimized stencils for the NaN-box/unbox sequences could be valuable --- that is a natural synergy point. But it is not the priority today.

MLGO and Deegen stay in the "interesting papers" pile. If GScript ever has a large enough user base to generate a training corpus, ML-guided heuristics might make sense. That day is not today.

## What We Learned

Seven posts of bottom-up optimization taught us how to make code fast. This survey taught us something different: how to know when you are optimizing the wrong thing.

Every technique in this post attacks performance from a different angle. Copy-and-patch says: generate better code. BOLT says: arrange the code better in memory. MLGO says: make better decisions about code generation. Deegen says: automate the whole process. NaN-boxing says: change the data.

The first four are about the code. The last one is about the data. And in the end, the data wins. Not because the code does not matter --- it does, we have proved that across seven posts --- but because the code is already pretty good. The data is 4x too big. Fix the data, and the code optimizations we have already built will run 4x faster on memory-bound workloads, because every cache line now carries 4x the useful information.

The academics have known this for decades. It is why every production language runtime --- without exception --- uses a compact value representation. We built the JIT first and the compact values second. Next time, we would do it the other way around.

But there is no shame in the scenic route. We understand our JIT deeply because we built it before we had the right data model. We know exactly where the overhead is, exactly which instructions are wasted, exactly which cache lines are polluted. When we do implement NaN-boxing, that understanding will make the work faster and the result better. The JIT is ready. The data just needs to catch up.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
