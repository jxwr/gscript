# GScript Development Guidelines

## The Mission

**Surpass LuaJIT.** This is an open-ended, iterative optimization project. The work continues until GScript's JIT compiler matches or exceeds LuaJIT's performance on standard benchmarks. There is no "done" — only the next milestone.

## The Loop

Every major optimization milestone follows this cycle:

```
1. MEASURE  →  Run benchmark suite, profile with pprof, identify the #1 bottleneck
2. RESEARCH →  Study how LuaJIT/V8/SpiderMonkey/LLVM/academia solved this problem
3. BLOG     →  Write a rich, detailed blog post (English) documenting findings + plan
4. IMPLEMENT → TDD: tests first, then code. Commit after each working step.
5. BENCHMARK → Run full suite, compare before/after. Record exact numbers.
6. PUBLISH  →  Update blog with results, push to GitHub Pages, commit milestone.
7. GOTO 1   →  The next bottleneck is now exposed. Repeat.
```

This loop runs **forever** until we surpass LuaJIT on all benchmarks.

## Blog Standards ("Beyond LuaJIT")

Each blog post should be **interesting to read**, not just a dry technical report. Include:

- **Story**: What were we trying to do? What surprised us? What failed?
- **Data**: Before/after benchmarks with exact numbers. CPU profiles. Charts if useful.
- **Previous results recap**: Start each post with a summary of where we are (last post's results + analysis)
- **Research deep-dive**: What do the experts say? Quote Mike Pall, V8 engineers, academic papers.
- **Architecture diagrams**: Show the pipeline, data flow, register allocation strategy.
- **Code examples**: Real GScript code + generated ARM64 assembly side by side.
- **Honest assessment**: What worked, what didn't, what we'd do differently.
- **Next steps**: What's the next bottleneck? What does the research suggest?

Published at: https://jxwr.github.io/gscript/

## Research Protocol

Before each major architectural change:

1. **Web search**: Latest blog posts, conference talks, papers on the specific technique
2. **Study implementations**: Read LuaJIT source (SSA IR, codegen), V8 Maglev, SpiderMonkey Warp
3. **Academic papers**: Check PLDI, CGO, CC proceedings for relevant optimization techniques
4. **Synthesize**: Write findings into the blog post BEFORE implementing
5. **Design**: Document the architecture in the blog, get the design right on paper first

## Code Standards

- **TDD**: Write tests first, then implement. Red → Green → Refactor.
- **No code duplication**: Shared emitter layer between JIT tiers
- **Profile before optimizing**: `pprof` to identify actual bottlenecks, never guess
- **Revert failed optimizations**: If benchmarks don't improve, revert immediately
- **Commit often**: Each working step gets a commit with detailed message
- **One concern per file**: Split large files (>500 lines) into focused modules

## Benchmark Suite

Standard benchmarks (in `benchmarks/suite/`):
- fib, sieve, mandelbrot, spectral_norm, matmul, nbody, ackermann, binary_trees
- Plus: chess_bench_parallel.gs (the ultimate mixed workload)

Run the full suite before AND after every optimization. Record numbers in the blog.

## Architecture Principles

- **SSA IR is the core**: All optimizations happen on SSA, not on bytecode or ARM64
- **Type specialization is king**: Unboxed integers and floats in registers = the #1 speedup
- **Tracing JIT for hot loops**: Records actual execution, compiles the hot path
- **Method JIT as baseline**: Quick compilation for cold code
- **Shared codegen layer**: One set of ARM64 emitters, used by both JIT tiers
- **Snapshots for side-exits**: LuaJIT-style precise state reconstruction

## Current Status

- Sieve: **×13.7** (best result)
- N-body: **×3.13**
- Mandelbrot: **×2.27**
- Chess AI: **×1.81** single, **×2.27** parallel
- Target: **×10+** across all benchmarks (LuaJIT territory)
