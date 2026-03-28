# GScript Development Guidelines

## The Mission

**Surpass LuaJIT.** Build a multi-tier optimizing JIT compiler for GScript using V8's Method JIT approach. The work continues until GScript matches or exceeds LuaJIT on all standard benchmarks.

## Core Principles

### 1. Multi-Tier JIT with Seamless Tiering

The compiler has multiple optimization levels that activate **automatically** based on runtime profiling — never via compiler flags or user configuration:

```
Tier 0: Interpreter (vm.go)
  → Executes bytecode, collects type feedback (FeedbackVector)
  → After N calls with stable feedback → Tier 1

Tier 1: Method JIT — Baseline (internal/methodjit/)
  → Compiles entire function: bytecode → CFG SSA IR → ARM64
  → Type-generic operations, basic optimizations
  → After M executions with hot loops → Tier 2

Tier 2: Method JIT — Optimized (future)
  → Aggressive optimization: inlining, type specialization, loop opts
  → Deoptimizes back to Tier 0 on guard failure
```

Tiering is transparent. The same GScript code runs at every tier and produces identical results. The runtime decides when to promote based on call counts and type stability.

### 2. Pluggable Pass Pipeline

Every optimization technique is an **independent, self-contained pass**:

```go
// Each pass: Function → Function (immutable input, new output)
type Pass func(*Function) *Function

// Pipeline is an ordered list of passes, each can be enabled/disabled
pipeline := NewPipeline()
pipeline.Add("TypeSpecialize", TypeSpecializePass)
pipeline.Add("CSE", CSEPass)
pipeline.Add("ConstProp", ConstPropPass)
pipeline.Add("DeadBranch", DeadBranchPass)
pipeline.Add("LoopInvariant", LICMPass)
pipeline.Add("Inline", InlinePass)
```

Rules:
- Each pass has its own file (`pass_<name>.go`) and test file (`pass_<name>_test.go`)
- Passes can be enabled/disabled independently via the pipeline registry
- Adding a new optimization = one Go file + one test file + one line in the pipeline
- No pass depends on internal state of another pass
- Any pass can be reverted in isolation if it causes regressions

### 3. File Size and Structure

**No Go file exceeds 1000 lines.** Large files destroy LLM effectiveness — the model loses track of context, makes wrong assumptions, and wastes entire sessions debugging phantom issues.

Rules:
- **Every Go file starts with a doc comment** explaining its purpose, inputs, outputs, and relationship to other files
- **Design the file structure upfront** — decide the split BEFORE writing code, not after hitting 2000 lines
- **One concern per file**: IR types in one file, graph builder in another, printer in another
- **Test files mirror source files**: `graph_builder.go` → `graph_builder_test.go`, `pass_cse.go` → `pass_cse_test.go`
- When a file approaches 800 lines, proactively split it

### 4. Test-Driven Development (TDD)

Every feature is built test-first:

```
1. Write a failing test that specifies the desired behavior
2. Write the minimum code to make it pass
3. Refactor without changing behavior
4. Repeat
```

Rules:
- **Tests before code.** No exception. If you can't write a test, you don't understand the requirement.
- **Test files correspond to source files.** `pass_cse.go` → `pass_cse_test.go`. Never dump all tests into one file.
- **IR interpreter for correctness**: `Interpret(BuildGraph(proto), args)` must match `VM.Execute(proto, args)` for every benchmark. This is the ground truth.
- **Structural invariants**: Every graph builder test checks: all blocks terminated, succ/pred consistency, unique value IDs, no orphan blocks.

### 5. AI-Friendly Debug Infrastructure

LLMs cannot trace multi-stage compiler pipelines mentally. Build diagnostic tools that **convert reasoning problems into data-reading problems**:

Required infrastructure (build BEFORE needing it):
- **IR Printer**: human-readable dump of every basic block, every instruction, every phi node
- **Pipeline Dump**: snapshot IR after each pass. `pipeline.Dump("after_CSE")` → printable IR. `pipeline.Diff("before_CSE", "after_CSE")` → what changed.
- **IR Interpreter**: execute the IR in Go and compare with VM. Catches graph builder bugs without needing ARM64 codegen.
- **IR Validator**: check structural invariants (terminated blocks, consistent succ/pred, SSA dominance, type consistency). Run after every pass.
- **Deopt Trace**: when deoptimization fires, log: which guard failed, what type was expected vs actual, which function, which bytecode PC.

The principle: **when something goes wrong, the diagnostic tool should tell you WHERE and WHY in one call, without reading compiler source code.**

### 6. Architecture Review Every Round

At the start and end of every optimization round:
1. **Review file sizes** — any file approaching 1000 lines? Split it.
2. **Review module boundaries** — any circular dependencies? Any file doing two unrelated things?
3. **Review pass pipeline** — any pass doing too much? Any optimization that should be split into two passes?
4. **Review test coverage** — any source file without a corresponding test file?
5. **Review diagnostic tools** — did we debug something by reading source code? Add a diagnostic tool for that.

**Don't wait until things are broken to refactor.** Proactive architecture maintenance prevents the "3000-line ssa_emit.go" disasters.

### 7. Correctness First, Always

- **Never optimize wrong results.** If the benchmark produces different output in JIT vs interpreter, the speedup is zero.
- **IR interpreter is the oracle.** `Interpret(graph, args) == VM.Execute(proto, args)` for every function, every input.
- **All tests pass before benchmarking.** No exceptions.
- **Revert immediately** if an optimization breaks correctness. Analyze before retrying.

## Hard-Won Rules

### Rule 1: Never optimize wrong results
The ×88 mandelbrot speedup was fake — the trace skipped 99.99% of computation. The function-entry tracing hack got fib to 46ms but broke 8 benchmarks. Speed means nothing without correctness.

### Rule 2: Observation beats reasoning — USE DIAGNOSTIC TOOLS
LLMs have lost entire sessions reading ssa_emit.go trying to guess why a guard fails. Instead:
1. Run the IR interpreter — does it match the VM?
2. Run the pipeline dump — which pass introduced the error?
3. Read the hex dump — what type is actually in that register?
4. **Only then** read the source code, and only the specific file identified by diagnostics.

### Rule 3: Architecture over patches
If you're fixing the third bug in the same subsystem, stop and redesign. The trace JIT's `writtenSlots` caused 3 bugs; `function-entry tracing` caused 8 breakages. The correct response is architectural change, not more special cases.

### Rule 4: Never stack on unverified code
Before adding Pass N+1, ALL tests must pass with passes 1..N enabled. Run correctness checks (IR interpreter vs VM) before timing.

## Method JIT Architecture

```
internal/methodjit/
  ir.go              — IR types: Function, Block, Instr, Value, Type
  ir_ops.go          — Op enum covering all 45 bytecodes
  graph_builder.go   — Bytecode → CFG SSA (Braun et al. 2013)
  printer.go         — Human-readable IR dump
  interp.go          — IR interpreter (correctness oracle)
  validator.go       — Structural invariant checker
  pipeline.go        — Pass pipeline registry
  pass_*.go          — Individual optimization passes
  regalloc.go        — Register allocation (forward-walk)
  emit.go            — ARM64 code generation
  deopt.go           — Deoptimization framework
```

Pipeline: `BuildGraph → [Validate → Pass1 → Validate → Pass2 → ... → Validate] → RegAlloc → Emit`

Validation runs after EVERY pass to catch bugs immediately.

## Current Status

- **M1 DONE**: CFG SSA IR + graph builder. Bytecode → IR conversion with Braun SSA construction. 9 tests, all pass. fib/if-else/for-loop/tables all generate correct IR.
- **Trace JIT**: stable for loops (mandelbrot 4.9x, table_field 11x, nbody 5.8x). Function-entry tracing removed. Gradually deprecated as Method JIT matures.
- **Next**: IR interpreter (correctness validation), then optimization passes, then ARM64 codegen.

## Roadmap

### M2: IR Interpreter + Validator + Pipeline Framework
- IR interpreter: execute CFG SSA in Go, compare with VM
- Validator: check invariants after every pass
- Pipeline: ordered pass list with enable/disable

### M3: Type Feedback + Type Specialization Pass
- FeedbackVector in interpreter (per-instruction type lattice)
- Type specialization pass: Add → AddInt/AddFloat based on feedback
- GuardType insertion for speculative optimization

### M4: Register Allocation + ARM64 Code Generation
- Forward-walk register allocator (Maglev-style)
- ARM64 emission reusing existing assembler layer
- Deopt stubs: guard failure → restore interpreter state

### M5: Tiering + Integration
- Call count in interpreter → compile at threshold
- Install compiled code in FuncProto.JITEntry
- Deopt: JIT guard fail → interpreter with feedback reset

### M6: Optimization Passes + Inlining
- CSE, constant propagation, dead branch elimination, LICM
- Function inlining at monomorphic call sites
- Self-recursive optimization (BL to same code)

### Goal
Surpass LuaJIT on all benchmarks via Method JIT with speculative optimization.

## Blog

Published at: https://jxwr.github.io/gscript/

Each post: story + data + research + honest assessment + next steps. Write after breakthroughs or when stuck. All content in English.

## Benchmark Protocol

Run full suite (`bash benchmarks/run_all.sh`) before AND after every optimization.
Always compare VM, JIT, and LuaJIT. Never delete benchmarks. Target: JIT 100x faster than VM.
