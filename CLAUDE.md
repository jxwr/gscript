# GScript Development Guidelines

## Working Habits for Compiler Architecture Evolution

### Before Every Major Milestone

1. **Research Phase**: Conduct thorough investigation of state-of-the-art compiler techniques. Study LuaJIT, V8, SpiderMonkey, JavaScriptCore, LLVM, and academic papers. Understand WHY they made their architectural choices, not just WHAT they built.

2. **Blog Post**: Write a technical blog post (in English) documenting the research findings, architectural decisions, and implementation plan. The post should read like a real compiler engineer's blog — with benchmarks, code examples, architecture diagrams, and references. Publish to the `blogs/` directory and the project's GitHub Pages blog "Beyond LuaJIT".

3. **Implementation**: Based on the blog post's conclusions, implement the changes using TDD. Each optimization should have tests written BEFORE the implementation.

4. **Benchmark & Measure**: Run the chess AI benchmark (chess_bench_parallel.gs) before and after. Record exact numbers. Profile with pprof to understand WHERE time is spent.

5. **Commit & Document**: Commit with detailed messages explaining the WHY, not just the WHAT. Update docs/optimization-summary.md with the new results.

### Blog Structure

Each blog post should contain:
- **Motivation**: What performance gap are we addressing?
- **Research**: What do LuaJIT/V8/SpiderMonkey/academia say about this?
- **Design**: What architectural decision did we make and why?
- **Implementation**: Key code patterns and tradeoffs
- **Results**: Before/after benchmarks with exact numbers
- **Next Steps**: What's the next bottleneck?

### Code Standards

- **TDD**: Write tests first, then implement
- **No code duplication**: If two JIT tiers need the same ARM64 sequence, extract a shared emitter
- **Profile before optimizing**: Use pprof to identify actual bottlenecks, don't guess
- **Revert failed optimizations**: If a change doesn't measurably improve the benchmark, revert it (e.g., NaN-boxing, Table pool)
- **One concern per file**: Don't let files grow beyond ~500 lines. Split vm.go's 1700-line run() into separate files

### Architecture Principles

- **SSA IR is mandatory** for any serious optimization (type specialization, guard hoisting, CSE, DCE)
- **Tracing JIT is the primary optimization tier** for GScript's compute-heavy workloads
- **Method JIT serves as baseline tier** for functions without hot loops
- **Shared codegen layer** between method JIT and tracing JIT to avoid duplication
- **Snapshots for side-exits** (LuaJIT pattern) instead of per-instruction state reconstruction

### Blog Publishing

- Blog directory: `blogs/`
- GitHub Pages blog: "Beyond LuaJIT"
- Language: English
- Format: Markdown with code blocks and benchmark tables
