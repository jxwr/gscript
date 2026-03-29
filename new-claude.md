# GScript

You are an expert compiler engineer. GScript is a dynamically-typed scripting language with a multi-tier JIT compiler targeting ARM64, implemented in Go. The architecture follows V8: **interpreter → baseline JIT → optimizing JIT**.

## Mission

**Surpass LuaJIT on all benchmarks.** Use V8's Method JIT approach (not LuaJIT's trace JIT) on Lua-like semantics. This is an open-ended, iterative project — there is no "done," only the next milestone.

## Non-Goals

- Trace JIT — deprecated, disconnected from CLI. `internal/jit/` is scheduled for deletion
- Hacks/workarounds that sacrifice correctness for speed (function-entry tracing broke 8 benchmarks)
- Arbitrary speedup ratios — theoretical max ~33x. Target is LuaJIT's absolute times

## Optimization Loop

Every major milestone follows this cycle. It runs **forever** until we surpass LuaJIT:

1. **MEASURE** → Run `bash benchmarks/run_all.sh`, profile with pprof, identify the #1 bottleneck
2. **RESEARCH** → Study how LuaJIT / V8 / SpiderMonkey / LLVM / academia solved this problem
3. **BLOG** → Write a detailed blog post documenting findings + plan (English)
4. **IMPLEMENT** → TDD: tests first, then code. Commit after each working step
5. **BENCHMARK** → Run full suite, compare before/after. Record exact numbers
6. **PUBLISH** → Update blog with results, push to GitHub Pages
7. **GOTO 1** → The next bottleneck is now exposed. Repeat.

## Team Workflow

Each milestone uses **parallel agents** to maximize throughput.

### Phase 1: Research (parallel — no code changes)

| Role | Task |
|------|------|
| **Profiler** | Run benchmarks + pprof, identify #1 bottleneck |
| **Researcher** | Web search LuaJIT/V8/SpiderMonkey/papers for the target technique |
| **Architect** | Read all relevant code, audit architecture, design refactoring plan |
| **Blogger** | Read existing blog posts, prepare outline for next post |

Main agent waits for all reports, then synthesizes into an implementation plan.

### Phase 2: Implement (parallel where possible)

After plan alignment with user, spawn coding agents in parallel for independent modules.

Rules:
- **Research agents never write code. Coding agents don't do open-ended research.**
- **Main agent is the integrator** — synthesizes reports, makes architectural decisions
- **Main agent NEVER runs benchmarks directly** — always spawn a benchmark sub-agent
- **Always use Opus model** for coding agents
- **Each coding agent gets a clear, bounded scope** — one pass, one module, one test file

## Coding Conventions

### File Size (IMPORTANT)
- **No Go file exceeds 1000 lines.** Split proactively at 800 lines
- One concern per file. Every file starts with a doc comment explaining its purpose
- Test files mirror source files: `foo.go` → `foo_test.go`

### Test-Driven Development (YOU MUST)
1. Write a failing test specifying desired behavior
2. Write minimum code to pass
3. Refactor without changing behavior
4. **Tests before code, no exception.** If you can't write a test, you don't understand the requirement

### Pass Pipeline
- Each pass: one file (`pass_<name>.go`) + one test file (`pass_<name>_test.go`)
- Passes are independent and reversible in isolation
- Pipeline: `BuildGraph → [Validate → Pass → Validate → ...] → RegAlloc → Emit`

### Code Standards
- **High-leverage first**: Prioritize optimizations affecting the most benchmarks. Ask: "1 benchmark or 10?"
- **Profile before optimizing**: `pprof` to find actual bottlenecks, never guess
- **Commit often**: Each working step gets a commit with detailed message
- **No time estimates**: Focus on what needs to be done, not how long it might take
- **Diagnostic tools first**: Use IR printer, pipeline dump, validator before reading source code. Convert reasoning problems into data-reading problems

## Benchmark Protocol

- Run `bash benchmarks/run_all.sh` before AND after every optimization
- Always compare VM, JIT, and LuaJIT
- **NEVER delete benchmarks.** If a benchmark errors or hangs, show as "ERROR"/"HANG" — do not remove the row
- Run the full suite, not a subset. Broken benchmarks expose JIT bugs

## Blog

Published at: https://jxwr.github.io/gscript/

Each post: **story + data + research + honest assessment + next steps.** All content in English. Make posts interesting to read, not dry technical reports.

Each post should include:
- **Previous results recap**: Summary of where we are from the last post
- **Research deep-dive**: What do the experts say? Quote relevant sources
- **Architecture diagrams**: Pipeline, data flow, register allocation strategy
- **Code examples**: GScript code + generated ARM64 assembly side by side
- **Honest assessment**: What worked, what didn't, what we'd do differently

**When to write:**
- **After a breakthrough** — write while data is fresh
- **After getting stuck** — research done, lessons learned, planning next approach

## Hard-Won Rules

### 1. Observation beats reasoning — use diagnostic tools
Don't read code and guess. Use diagnostic tools first: IR interpreter, pipeline dump, hex dump. Only then read source code — only the file diagnostics identified. Five hours of guessing vs five minutes of observation.

### 2. Architecture over patches
Third bug in the same subsystem? Stop and redesign. Don't add special cases to broken designs.

### 3. Never stack on unverified code
Before adding Pass N+1, ALL tests must pass with passes 1..N. Run correctness checks before timing.

### 4. Proactive architecture review
Every optimization round: review file sizes, module boundaries, pass pipeline, test coverage, diagnostic tools. Don't wait for things to break.

