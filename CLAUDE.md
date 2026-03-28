# GScript Development Guidelines

## The Mission

**Surpass LuaJIT.** This is an open-ended, iterative optimization project. The work continues until GScript's JIT compiler matches or exceeds LuaJIT's performance on standard benchmarks. There is no "done" — only the next milestone.

## Three Commandments

### 1. Coverage First — Native Code Coverage is the #1 Priority
Always prioritize **making more traces compile to native code** over polishing already-compiled traces. A trace that can't compile at all gets 0x speedup; a trace that compiles but isn't optimally scheduled still gets 2-5x. Systematically enumerate every reason a trace fails to compile (unsupported opcodes, call-exits, missing type specialization) and eliminate them one by one. Never spend time on micro-optimizations (ConstHoist, CSE, FMA) when there are entire benchmark categories blocked from compilation.

### 2. Pluggable Compiler Framework — Every Optimization is an Independent Pass
Design the compilation pipeline so that each optimization technique is a **self-contained, independently addable, testable, iterable, and fixable** pass. Each pass:
- Has its own file (`ssa_opt_<name>.go`)
- Has its own unit tests (`ssa_opt_<name>_test.go`)
- Can be enabled/disabled independently (pass registry)
- Takes `*SSAFunc` as input and returns `*SSAFunc` as output
- Never depends on internal state of other passes
- Can be reverted in isolation if it causes regressions

The pass pipeline should be a simple list: `BuildSSA → [Pass1, Pass2, ..., PassN] → RegAlloc → Emit`. Adding a new optimization means adding one file + one test file + one line in the pass registry.

### 3. Continuous Research — Expand the Optimization Frontier
Continuously research **all mainstream compiler optimization techniques** from LuaJIT, V8, SpiderMonkey, LLVM, GCC, academic papers (PLDI, CGO, CC). Maintain a living catalog of known techniques and their applicability to GScript's trace JIT. When the current architecture cannot support a promising technique, **evolve the architecture** — the framework serves the optimizations, not the other way around. The goal is an ever-expanding arsenal of optimizations, not a frozen pipeline.

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

### Rule 2: Observation beats reasoning — USE THE DIAGNOSTIC TOOLS
Don't read code and guess. The JIT has a 7-stage pipeline; reading the emitter to guess why a guard fails is a trap — **LLMs have lost entire sessions doing this.** Instead, use the built-in diagnostic tools (see `docs/jit-debug.md`):

1. **First call: `DiagnoseTrace()`** — one function gives you pipeline status, SSA IR, register hex dump, exit code/PC/iterations. This alone solves 80% of bugs.
2. **If the pipeline is suspect: `CompileWithDump()` + `dump.Diff()`** — binary search which pass introduced the error, don't read pass implementations.
3. **If values are wrong: compare register hex dumps** — NaN-boxing tag in upper 16 bits tells you the type instantly (`0xFFFE`=int, `0xFFFC`=nil, `0xFFFF`=pointer).
4. **If all else fails: `ShowASM: true`** — read the generated ARM64, but only AFTER you've narrowed to a specific instruction.

The principle: **build diagnostic output, read diagnostic output, fix based on evidence.** Never skip step 1 and jump straight into reading `ssa_emit.go`.

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
- **Benchmark agent updates all docs** — after running benchmarks, update README.md Performance section and docs/index.html with fresh numbers.

## Research Protocol

Before each major architectural change:

1. **Web search**: Latest blog posts, conference talks, papers on the specific technique
2. **Study implementations**: Read LuaJIT source (SSA IR, codegen), V8 Maglev, SpiderMonkey Warp
3. **Academic papers**: Check PLDI, CGO, CC proceedings for relevant optimization techniques
4. **Synthesize**: Write findings into the blog post BEFORE implementing
5. **Design**: Document the architecture in the blog, get the design right on paper first

## Benchmark Protocol

After every major milestone, run the full benchmark suite using `bash benchmarks/run_all.sh`.
Update results in the top-level README.md Performance section and docs/index.html blog homepage.
Never skip benchmarks — if one fails, note it in the output. Run sequentially (not parallel).

### Hard Rules
1. **NEVER delete benchmarks from the test suite.** All 21 benchmarks must always be listed in the README. If a benchmark errors or hangs in JIT mode, show it as "ERROR" or "HANG" — do not remove the row.
2. **Always compare three modes: VM, JIT, and LuaJIT.** Every benchmark run must produce results for all three. The README Performance table must include VM, JIT, and LuaJIT columns so regressions are visible.
3. **Run the full suite, not a subset.** Never skip benchmarks because they're slow or broken. Broken benchmarks are signal — they expose JIT bugs that need fixing.

## Code Standards

- **High-leverage first**: Always prioritize optimizations with the biggest impact across the most benchmarks. Don't spend time on micro-optimizations (saving 2-3ms) when there are architectural changes (type-specialized arrays, guard fixes) that can improve entire categories of benchmarks by 2-5x. Ask: "does this change affect 1 benchmark or 10?"
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

Full architecture document: docs/jit-architecture.md
JIT debugging guide: docs/jit-debug.md

- **SSA IR is the core**: All optimizations happen on SSA, not on bytecode or ARM64
- **Type specialization is king**: Unboxed integers and floats in registers = the #1 speedup
- **Tracing JIT for hot loops**: Records actual execution, compiles the hot path
- **Pass pipeline**: BuildSSA → Optimize → RegAlloc → Emit (no mixing)
- **Snapshots for side-exits**: LuaJIT-style precise state reconstruction (TODO: replace writtenSlots)
- **Decouple SSA refs from VM slots**: The SSA IR should use its own numbering, not bytecode slot numbers

## Architecture Audit

Full audit document: `docs/architecture_audit.md`
Key findings: slot-reuse problem, writtenSlots fragility, pass pipeline need.

## Current Status

All phases complete through S2.3 + compiler optimizations + architecture refactor + function-entry tracing. Peak speedup: **33.6x** (fib). 10 of 21 benchmarks accelerated. fib(35) at 46ms, 2x behind LuaJIT. See README.md Performance section for latest benchmark numbers (updated 2026-03-28).

## Completed Phases

- Phase 0: Trace blacklisting ✓
- Phase 1: Pass pipeline refactor (BuildSSA → Optimize → ConstHoist → CSE → RegAlloc → Emit) ✓
- Phase 2: Native GETFIELD/SETFIELD + GETGLOBAL + sqrt intrinsic + FORPREP blacklisting ✓
- Phase 3: Constant hoisting + CSE + type-specialized LOAD_ARRAY ✓
- Phase 4: Sub-trace calling for nested loops (BLR to inner compiled trace) ✓
- SSA-ref-level float register allocator (linear scan with coalescing) ✓
- VM inline field cache (per-instruction hint-based O(1) GETFIELD/SETFIELD) ✓
- Blog #5 (breakthrough) + Blog #6 (stuck/reflecting) ✓
- **S2.0: NaN-boxing core package ✓**
- **S2.1: NaN-boxing migration + box/unbox optimization ✓** (Blog #11, #12)
- **S2.2: Custom heap (mmap arena + lock-free gcRoots) ✓**

## Roadmap: Surpass LuaJIT

### S2.3: Custom GC (mark-sweep) — next
1. GCHeader for all arena-allocated objects
2. Mark phase: trace from VM registers + stack + globals
3. Sweep phase: reclaim unmarked arena objects via free list
4. binary_trees should improve significantly (currently 8.5x gap)

### Phase 6: Compute-heavy benchmarks
5. **JIT native float array access** — the #1 bottleneck across 5+ benchmarks
6. **Inner trace code inlining** — copy inner trace ARM64 into outer trace (Approach C)
7. **Reduce per-iteration instructions** — eliminate remaining memory spills
8. **Method JIT type specialization + inlining**

