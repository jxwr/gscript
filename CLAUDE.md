# GScript Development Guidelines

## The Mission

**Surpass LuaJIT on all benchmarks.** This is an AI-driven compiler engineering experiment: build a production-quality JIT compiler using V8's Method JIT architecture, entirely by AI agents.

### Why This Project Exists

1. **Prove AI can build compilers.** Not toy compilers — a real JIT that generates ARM64 machine code, does speculative optimization with deopt, and competes with hand-tuned C code (LuaJIT).
2. **Explore AI-friendly compiler architecture.** Traditional compilers are built for human engineers. We design for AI: modular files (<1000 lines), diagnostic tools that dump state instead of requiring reasoning, TDD with correctness oracles.
3. **Beat LuaJIT.** Not by copying it (trace JIT), but by using V8's approach (Method JIT) on Lua-like semantics. A fundamentally different strategy to reach the same goal.

### Non-Goals

- Trace JIT. We tried it. It hit a ceiling on function calls, recursion, and branchy code. **Trace JIT is deprecated and disconnected from the CLI.** `internal/jit/` is scheduled for deletion.
- Hacks and workarounds. Function-entry tracing was a hack that broke 8 benchmarks. We don't bolt partial solutions onto the wrong architecture.
- 100x over interpreter. The theoretical maximum for a JIT over a Go interpreter is ~33x (10ns/op vs 0.3ns/op). Our real target is beating LuaJIT's absolute times, not a ratio.

### The Architecture

```
Tier 0: Interpreter (internal/vm/)
  → Executes all bytecodes, collects type feedback (FeedbackVector)
  → After 100 calls with stable feedback → Tier 1

Tier 1: Method JIT (internal/methodjit/)
  → Compiles entire functions: bytecode → CFG SSA IR → ARM64
  → Handles ALL operations (native or via op-exit resume)
  → No function is ever rejected — everything compiles
  → Optimization passes: TypeSpec, ConstProp, DCE, Inline
  → For ops that can't be native: exit to Go, perform op, resume JIT
```

No trace JIT. Three tiers: interpreter → baseline → optimizing. Like V8.

## Core Principles

### 1. Single Method JIT, Universal Compilation

Every function compiles. No `canCompile` rejection. Operations the JIT can't emit natively use **op-exit resume**: the JIT exits to Go, Go performs the operation, the JIT resumes at the next instruction. This is slower than native (~55ns per exit) but faster than rejecting the entire function.

```
Native ops (fast):     int/float arithmetic, comparisons, branches, loops, constants
Exit-resume ops (slow): function calls, globals, tables, strings, closures, channels
Future native ops:     inline field access (shape guards), native BL calls, inlined callees

Tier 1: Baseline JIT (planned)
  → Fast compilation, minimal optimization
  → Compiles ALL ops natively (no op-exit) using simple templates
  → Quick to compile, modest speedup (~3-5x), low latency
  → Every function gets Tier 1 quickly

Tier 2: Optimizing JIT (current methodjit/)
  → Slower compilation, aggressive optimization
  → CFG SSA IR → TypeSpec → ConstProp → DCE → Inline → RegAlloc → Emit
  → Raw-int loop mode, shape-guarded field access, function inlining
  → Big speedup (~10-30x) for hot functions
```

**Seamless tier-up**: Tier 0 → Tier 1 at 50 calls. Tier 1 → Tier 2 at 500 calls with stable feedback. Same function, same result, different speed. The runtime decides, never the user.

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

**Optimizing JIT (Tier 2)**: Complete pipeline operational. 40+ files, 12K+ lines, 200+ tests.
- CFG SSA IR + Braun graph builder
- 4 optimization passes: TypeSpec, ConstProp, DCE, Inline
- Forward-walk register allocator (5 GPRs + 8 FPRs)
- ARM64 emitter with raw-int loop optimization (21.4x on tight loops)
- Call-exit + global-exit + table-exit resume
- Shape-guarded inline field access
- Type feedback collection in interpreter
- Tiering: interpreter → Method JIT at 100 calls
- Diagnose tool: one-call diagnostic dump

**Trace JIT**: DEPRECATED. Disconnected from CLI. Scheduled for deletion.

**Performance**: Sum10000 21.4x over VM. Geometric mean vs LuaJIT: 16.7x behind.

## Roadmap

### Phase A: Universal Compilation (IN PROGRESS)
- Remove canCompile rejection — all functions compile
- Generic op-exit for unsupported ops (concat, len, closures, etc.)
- Every benchmark runs through Method JIT

### Phase B: Baseline JIT (Tier 1)
- Fast compile, no optimization, all ops native
- Bytecode → ARM64 template translation (like V8 Sparkplug)
- Quick startup, modest speedup (~3-5x)
- Seamless tier-up to Tier 2 at 500 calls

### Phase C: Interpreter-Resume Deopt
- Guard failures resume interpreter at exact bytecode PC (not restart)
- Enables aggressive speculative optimization
- BytecodePC field on every IR instruction

### Phase D: Native Function Calls
- JIT-to-JIT calls via ARM64 BL (no Go round-trip)
- Self-recursive native BL for fib/ackermann
- Callee inlining at IR level (InlinePass already exists)

### Phase E: Advanced Optimization
- Loop-invariant code motion (LICM)
- Escape analysis
- Range check elimination
- Aggressive inlining with deopt

### Goal
Surpass LuaJIT on all benchmarks via Method JIT with speculative optimization.

## Old Roadmap (Completed)

### M1-M6: DONE
- M1: CFG SSA IR + graph builder ✓
- M2: IR interpreter + validator + pipeline ✓
- M3: Type feedback in VM ✓
- M4: Register allocator + ARM64 emitter ✓ (forward-walk regalloc, NaN-boxed + raw-int emission)
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
