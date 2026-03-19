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

## Hard-Won Rules (from 2026-03-18 disaster)

### Rule 1: Never optimize wrong results
If the benchmark result doesn't match the interpreter, **the speedup is zero**. Run correctness checks BEFORE celebrating. The ×88 mandelbrot was fake — the trace was skipping 99.99% of the computation.

### Rule 2: Observation beats reasoning
Don't read code and guess. **Dump register state before/after trace execution.** Five hours of guessing vs five minutes of observation. Always:
1. Add `before/after` register dumps
2. Run the smallest possible test case (`mandelbrot(3)`, not `mandelbrot(1000)`)
3. Compare trace output vs interpreter-only output
4. Only remove dumps after correctness is confirmed

### Rule 3: Never stack optimizations on unverified correctness
Before adding a new optimization, ALL existing tests must pass with the trace JIT enabled. Run the full benchmark suite with correctness checks (compare trace vs interpreter results), not just timing.

### Rule 4: Architecture over patches
If you're fixing the third bug in the same subsystem, stop and redesign. The `writtenSlots` mechanism caused 3 separate bugs because it's ad-hoc manual tracking. The correct fix is liveness analysis on the SSA IR, not more special cases.

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

### When to Write Blog Posts

Don't wait for fixed milestones. Two best times to write:

1. **After a breakthrough** — huge progress with data and story, write while it's hot. Examples: mandelbrot jumping from 1.53x to 5.92x, discovering the FORPREP abort-blacklisting trick.

2. **After getting stuck** — research done, lessons learned, planning the next approach. Examples: realizing the trace JIT hurts 5/7 benchmarks, the nested loop tracing architecture problem. These posts have depth and reflection.

Published at: https://jxwr.github.io/gscript/

## Team Workflow

Each milestone uses parallel agents to maximize throughput. Two phases:

### Phase 1: Research (parallel)

Spawn 4 agents simultaneously, all research-only (no code changes):

| Role | Task | Output |
|------|------|--------|
| **Profiler** | Run benchmarks + pprof, identify #1 bottleneck | Benchmark data, CPU profile top functions, bottleneck analysis |
| **Researcher** | Web search LuaJIT/V8/SpiderMonkey/papers for the target technique | Research report with sources, recommended approach, priority ranking |
| **Architect** | Read all relevant code, audit architecture, design refactoring plan | Code audit, data flow map, concrete refactoring steps |
| **Blogger** | Read existing blog posts, prepare outline for next post | Blog outline with story angle, section structure, diagram plan |

Main agent waits for all 4 reports, then synthesizes into an implementation plan.

### Phase 2: Implement (parallel where possible)

After plan alignment with user, spawn coding agents in parallel for independent modules:

```
Main agent ──→ Plan + align with user
           ──→ Blogger writes research/design sections of blog
           ──→ Coding agents (parallel by module): tests first, then implementation
           ──→ Main agent integrates, runs full benchmark suite
           ──→ Blogger updates blog with results
```

Key rules:
- **Research agents never write code. Coding agents don't do open-ended research.**
- **Main agent is the integrator** — synthesizes reports, makes architectural decisions, resolves conflicts.
- **Main agent NEVER runs benchmarks directly** — always spawn a benchmark sub-agent.
- **Always use Opus model** for coding agents (user preference).
- **Each coding agent gets a clear, bounded scope** — one pass, one module, one test file.
- **Benchmark agent updates all docs** — after running benchmarks, update CLAUDE.md, benchmarks/README.md, docs/index.html with fresh numbers.

## Research Protocol

Before each major architectural change:

1. **Web search**: Latest blog posts, conference talks, papers on the specific technique
2. **Study implementations**: Read LuaJIT source (SSA IR, codegen), V8 Maglev, SpiderMonkey Warp
3. **Academic papers**: Check PLDI, CGO, CC proceedings for relevant optimization techniques
4. **Synthesize**: Write findings into the blog post BEFORE implementing
5. **Design**: Document the architecture in the blog, get the design right on paper first

## Benchmark Protocol

**CRITICAL: Never run benchmarks in the main agent.** Always spawn a sub-agent for benchmarking.

### Running Benchmarks

```bash
# Quick mode (~15s): Go warm benchmarks only
bash benchmarks/run_bench.sh --quick

# Full mode (~2min): VM + Trace + LuaJIT + Go warm
bash benchmarks/run_bench.sh
```

### After Every Benchmark Run

The benchmark agent MUST update these files with fresh numbers:
1. **`CLAUDE.md`** — "Current Status" tables (vs LuaJIT + full suite)
2. **`benchmarks/README.md`** — JIT vs LuaJIT table + JIT vs Interpreter table
3. **`docs/index.html`** — Blog homepage benchmark tables

### Known Issues
- **`binary_trees.gs`** — stack overflow crash (deep recursion exceeds Go goroutine stack). Excluded from suite.
- **Trace mode** may timeout on some benchmarks (30s limit per benchmark in runner).

### Benchmark Test Suite (fixed set)

Go warm benchmarks (`go test ./benchmarks/ -bench=Warm`):
- FibRecursiveWarm, FibIterativeWarm, HeavyLoopWarm, FunctionCallsWarm, AckermannWarm (JIT + VM pairs)

Suite benchmarks (14 .gs files, `binary_trees` excluded):
- fib, sieve, mandelbrot, ackermann, matmul, spectral_norm, nbody, fannkuch
- sort, sum_primes, mutual_recursion, method_dispatch, closure_bench, string_bench

## Code Standards

- **TDD**: Write tests first, then implement. Red → Green → Refactor.
- **No code duplication**: Shared emitter layer between JIT tiers
- **Profile before optimizing**: `pprof` to identify actual bottlenecks, never guess
- **Revert failed optimizations**: If benchmarks don't improve, revert immediately
- **Commit often**: Each working step gets a commit with detailed message
- **One concern per file**: Split large files (>500 lines) into focused modules
- **Pass pipeline architecture**: SSA builder, optimization passes, register allocator, and code emitter should be separate passes with clean interfaces. Do not mix analysis and code generation.
- **No time estimates**: Never estimate work in days/weeks/hours. For AI agents, wall-clock time is meaningless — focus on what needs to be done, not how long it might take.
- **Profile at milestones**: Run `pprof` after each milestone (not every small optimization — too slow). Verify the optimization actually shifted time away from the expected bottleneck. Don't blindly optimize — the profile tells you what to do next.

## Benchmark Suite

Standard benchmarks (in `benchmarks/suite/`):
- fib, sieve, mandelbrot, spectral_norm, matmul, nbody, ackermann, binary_trees
- Plus: chess_bench_parallel.gs (the ultimate mixed workload)

Run the full suite before AND after every optimization. Record numbers in the blog.
**Always verify correctness**: trace output must match interpreter output.

## Architecture Principles

- **SSA IR is the core**: All optimizations happen on SSA, not on bytecode or ARM64
- **Type specialization is king**: Unboxed integers and floats in registers = the #1 speedup
- **Tracing JIT for hot loops**: Records actual execution, compiles the hot path
- **Pass pipeline**: BuildSSA → Optimize → RegAlloc → Emit (no mixing)
- **Snapshots for side-exits**: LuaJIT-style precise state reconstruction (TODO: replace writtenSlots)
- **Decouple SSA refs from VM slots**: The SSA IR should use its own numbering, not bytecode slot numbers

## Architecture Audit

Full audit document: `docs/architecture_audit.md`
Key findings: slot-reuse problem, writtenSlots fragility, pass pipeline need.

## Current Status (2026-03-20, re-measured)

### vs LuaJIT (warm benchmarks)
| Benchmark | GScript JIT | LuaJIT | Result |
|-----------|-------------|--------|--------|
| **fib(20)** | **24.2us** | 24.5us | **1% faster** |
| fn calls (10K) | 5.1us | 2.5us | 2.0x gap |
| ackermann(3,4) | 30.4us | 12.1us | 2.5x gap |
| mandelbrot(1000) | 0.233s | 0.057s | 4.1x gap |

### Full benchmark suite (14 benchmarks + warm)
| Benchmark | VM | Trace | Speedup | LuaJIT | vs LuaJIT |
|-----------|-----|-------|---------|--------|-----------|
| mandelbrot(1000) | 1.356s | **0.233s** | **x5.8** | 0.057s | 4.1x gap |
| fib(35) | 0.033s | 0.032s | x1.0 | 0.025s | 1.3x gap |
| sieve(1M x3) | 0.112s | 0.118s | x0.95 | 0.011s | 10.7x gap |
| ackermann(3,4 x500) | 0.015s | 0.015s | x1.0 | 0.006s | 2.5x gap |
| matmul(300) | 1.209s | 1.447s | x0.84 | 0.026s | 55.7x gap |
| spectral_norm(500) | 0.785s | 0.808s | x0.97 | 0.008s | 101x gap |
| nbody(500K) | 2.421s | 2.397s | x1.01 | 0.034s | 70.5x gap |
| fannkuch(9) | 0.525s | timeout | — | 0.017s | 30.9x gap |
| sort(50K x3) | 0.148s | 0.149s | x0.99 | 0.011s | 13.5x gap |
| sum_primes(100K) | 0.023s | 0.028s | x0.82 | 0.002s | 14.0x gap |
| mutual_recursion(25 x1000) | 0.266s | 0.307s | x0.87 | 0.006s | 51.2x gap |
| method_dispatch(100K) | 0.123s | 0.124s | x0.99 | <0.001s | >100x gap |
| closure_bench | 0.069s | 0.070s | x0.99 | 0.009s | 7.8x gap |
| string_bench | 0.042s | 0.044s | x0.95 | 0.009s | 4.9x gap |

### Warm micro-benchmarks (Go test, JIT vs VM)
| Benchmark | JIT | VM | Speedup |
|-----------|-----|-----|---------|
| HeavyLoop | 25.3us | 725.6us | **x28.7** |
| FibIterative(30) | 212ns | 502ns | **x2.4** |
| FunctionCalls(10K) | 5.1us | 248.8us | **x48.8** |
| FibRecursive(20) | 24.2us | 637.2us | **x26.3** |
| Ackermann(3,4) | 30.4us | 301.5us | **x9.9** |

Target: **surpass LuaJIT** on compute-heavy benchmarks first, then table-heavy.

### Inner Loop Analysis (mandelbrot)
- 26 instructions per iteration (down from ~50, theoretical minimum ~15)
- 61 instructions for prologue/guards/loads (runs once per sub-trace call)
- Bottleneck: 1M sub-trace calls x 61-inst prologue = 61M wasted instructions

### LuaJIT Gap Analysis
| Gap | Root Cause | Fix | Difficulty |
|-----|-----------|-----|-----------|
| mandelbrot 4.1x | Sub-trace call overhead + 26 vs 15 inst/iter | Code inlining (Approach C) | High |
| **fib SURPASSED** | 24.2us vs 24.5us = 1% faster | Pin R(0) to X19 + const propagation | Done |
| table ops (matmul 56x, spectral 101x) | 32B Value vs 8B TValue | NaN-boxing | Extremely High |
| fn calls 2.0x | Remaining overhead from non-pinnable patterns | Further inline optimization | Medium |

## Completed Phases

- Phase 0: Trace blacklisting ✓
- Phase 1: Pass pipeline refactor (BuildSSA → Optimize → ConstHoist → CSE → RegAlloc → Emit) ✓
- Phase 2: Native GETFIELD/SETFIELD + GETGLOBAL + sqrt intrinsic + FORPREP blacklisting ✓
- Phase 3: Constant hoisting + CSE + type-specialized LOAD_ARRAY ✓
- Phase 4: Sub-trace calling for nested loops (BLR to inner compiled trace) ✓
- SSA-ref-level float register allocator (linear scan with coalescing) ✓
- VM inline field cache (per-instruction hint-based O(1) GETFIELD/SETFIELD) ✓
- Blog #5 (breakthrough) + Blog #6 (stuck/reflecting) ✓

## Roadmap: Surpass LuaJIT

### Phase 5: Compute-heavy benchmarks (current focus)
1. **Inner trace code inlining** — copy inner trace ARM64 into outer trace (Approach C), eliminates 61-inst prologue per pixel
2. **Reduce per-iteration instructions** — 26 → ~15 (eliminate remaining memory spills)
3. **Method JIT type specialization** — fib type-specialized int→int calls
4. **Method JIT function inlining** — inline small functions like `add(a,b)`

### Phase 6: Table-heavy benchmarks (future)
5. **NaN-boxing** — Value 32B → 8B, touches every file, 2-4 weeks ("Season 2")

## Hard-Won Lessons (added 2026-03-19)

### Lesson 5: Frequency-based allocation fails on flat distributions
Mandelbrot's float temps all have similar frequency (each used once/iteration). The frequency allocator assigned registers semi-randomly. Live-range-based (linear scan) + ref-level allocation was the fix.

### Lesson 6: The biggest optimization is often NOT about generating better code
FORPREP blacklisting (stopping 2.5M wasted recording attempts) gave mandelbrot its biggest jump (1.53x → 5.92x). This wasn't a codegen improvement — it was eliminating work that shouldn't happen.

### Lesson 7: Sub-trace calling has a structural overhead ceiling
Each BLR call to an inner trace requires full prologue/epilogue (61 instructions). For 1M pixels, that's 61M instructions = ~15ms. Code inlining (Approach C) is needed to eliminate this.
