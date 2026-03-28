---
layout: default
title: "The Pivot"
permalink: /20-the-pivot
---

# The Pivot

We changed direction. Again.

The trace JIT works for loops. mandelbrot runs at 4.9x the interpreter, table_field_access at 11x, nbody at 5.8x. These are real numbers on real benchmarks, and the code that produces them is clean: value-based SSA, snapshot-based deoptimization, a proper pass pipeline.

But fib runs at interpreter speed. spectral_norm is 117x behind LuaJIT. mutual_recursion is 62x behind. Any benchmark that needs function calls — recursion, polymorphism, deep call stacks — hits a wall. The trace JIT records loops. It does not record functions.

We tried to fix this. Three times. All three failed. Now we are pivoting to V8's Maglev-style Method JIT architecture — the industry standard for compiling functions.

This is not indecisive. It is learning.

---

## Where we are

After removing the function-entry tracing hack (more on that below), here is the honest state of the trace JIT:

| Benchmark | JIT/VM | vs LuaJIT |
|-----------|--------|-----------|
| table_field_access | **11.0x** | -- |
| nbody | **5.8x** | 9.8x behind |
| mandelbrot | **4.9x** | 5.4x behind |
| matmul | **4.5x** | 10.2x behind |
| table_array_access | **2.8x** | -- |
| fibonacci_iterative | **2.4x** | -- |
| sieve | **1.8x** | 13.0x behind |
| fib(35) | 1.0x | 67.6x behind |
| spectral_norm | 0.9x | 117.6x behind |
| mutual_recursion | 0.8x | 62.8x behind |

The pattern is clear. Everything above the line is loop-dominated. Everything below involves function calls. The trace JIT accelerates loops and is blind to functions.

---

## What we tried

### Attempt 1: Trace-through-calls

The idea was simple: when the trace recorder encounters a CALL instruction, follow the execution into the callee. The callee's bytecode gets inlined into the trace, and when it returns, the recorder continues in the caller. LuaJIT does this.

It failed because of store-back corruption. GScript's trace recorder uses a shared `regRegs` array that maps VM register slots to hardware registers. When the recorder follows a call at depth > 0, the callee's register assignments overwrite the caller's entries in `regRegs`. On side-exit, the snapshot tries to store back register values to the caller's frame, but the mapping is wrong — callee data gets written to caller slots.

LuaJIT solves this with frame-aware snapshots: each snapshot knows which function frame it belongs to, and store-back respects frame boundaries. We do not have that infrastructure. Building it would mean redesigning the snapshot system, the register allocator, and the side-exit mechanism — essentially half the JIT.

### Attempt 2: Function-entry tracing

Since we could not trace through calls, we tried a different angle: compile entire function bodies as traces. Detect when a function is hot (called many times), record its body from entry to return, and emit native code that uses ARM64 `BL` instructions for recursive self-calls.

This got fib(35) down to 46ms — a 33.6x speedup over the interpreter, only 2x behind LuaJIT. The number looked great.

Then we ran the full benchmark suite. Eight benchmarks broke: binary_trees crashed with a segfault, mutual_recursion panicked, and several others produced wrong results. The function-entry tracing code was writing to registers that the caller assumed were preserved. It had no mechanism for handling multiple return values, variadic arguments, or mutual recursion. Each fix introduced a new edge case.

We deleted it. The code was not salvageable — it was a loop-compilation mechanism bolted onto function boundaries, and the abstraction did not fit.

### Attempt 3: The rewrite itself

In a sense, the entire trace JIT rewrite (blog posts 17-18) was an attempt to solve the function problem. We deleted 13,000 lines of the old two-tier JIT (which had both a Method JIT and a trace JIT) and rebuilt from scratch with a trace-only architecture, hoping that clean SSA infrastructure would make trace-through-calls tractable.

The rewrite gave us clean code, correct snapshots, and 11x on table_field_access. But it also deleted the working Method JIT that had handled fib and ackermann. The old JIT got fib(35) in 34ms. After the rewrite, fib was back to 1.5 seconds.

The rewrite was premature. We deleted the Method JIT before having a trace-based replacement for it. The clean SSA infrastructure is valuable — it is the foundation we will build the new Method JIT on. But the decision to go trace-only was wrong.

---

## Why Method JIT

The trace JIT's fundamental problem is that it only sees one execution path. It records what actually happened during one run through a loop body, and compiles that single path to native code. Branches become guards that side-exit to the interpreter.

This works brilliantly for tight numeric loops: mandelbrot's inner loop always takes the same path, nbody's force calculation is straight-line arithmetic. The trace captures exactly the hot path with no wasted compilation.

But functions are not single paths. A recursive fibonacci has two branches (base case and recursive case). A sorting algorithm has comparisons that go both ways. A method dispatch has multiple possible callees. The trace JIT must either:

1. Record one path and guard the rest (exponential trace explosion for deep recursion)
2. Somehow merge multiple paths into one trace (which defeats the purpose of tracing)
3. Give up and let the interpreter handle it (current behavior)

A Method JIT compiles the whole function. It sees all branches, can inline callees, and handles recursion naturally. The compiler reads the function's bytecode, builds an SSA control-flow graph with all paths, and emits native code for the entire function body.

### V8 validated this approach

V8's Maglev is the existence proof. It is a mid-tier Method JIT that sits between Sparkplug (baseline, no optimization) and TurboFan (top-tier, aggressive optimization). Maglev uses:

- **CFG-based SSA IR**: basic blocks, phi nodes at merge points, specialized nodes for JavaScript operations
- **Type feedback from the interpreter**: a FeedbackVector records which types each instruction has seen
- **Speculative optimization**: if `+` always saw integers, emit `Int32AddWithOverflow` with a deopt guard
- **Forward-walk register allocator**: simpler than linear scan, good enough for mid-tier code
- **Deoptimization**: when speculation fails, reconstruct the interpreter frame and resume there

The recent development is even more validating: V8 is building Turbolev, which takes Maglev's IR and feeds it into the Turboshaft backend (a CFG-based replacement for TurboFan's Sea of Nodes graph). The V8 team is choosing CFG-based SSA over Sea of Nodes for their top-tier compiler. If CFG is good enough for V8's hottest code, it is good enough for us.

### A different approach than LuaJIT

LuaJIT is a trace JIT. It is the best trace JIT ever built, with years of hand-tuning by Mike Pall. Trying to beat LuaJIT at tracing is a losing game — we would need frame-aware snapshots, trace-tree linking, allocation sinking, alias analysis, and a hand-written assembly interpreter, and even then we would be replicating what one person spent a decade perfecting.

A Method JIT takes a fundamentally different approach. We are not trying to build a better trace JIT than LuaJIT. We are building a different kind of JIT entirely — one that compiles whole functions with speculative optimization, like V8 Maglev, like JSC's DFG, like SpiderMonkey's Warp.

The trace JIT stays for what it does best: hot inner loops. The Method JIT handles everything else.

---

## The plan

### M1: Type Feedback Collection

Add a `FeedbackVector` to each function prototype. The interpreter collects type information at every arithmetic, comparison, table access, and call instruction:

- **Arithmetic**: Int, Float, or both?
- **Comparisons**: Int vs Int? Float vs Float? Mixed?
- **Table access**: integer key? string key? what shape?
- **Calls**: always the same function? (monomorphic)

The type lattice is monotonic — it can broaden (Int -> Number) but never narrow. This prevents deopt-reopt cycles.

### M2: CFG-based SSA IR + Graph Builder

Extend the current SSA IR with basic blocks, predecessor/successor edges, and phi nodes at control-flow merge points. Build an SSA graph from bytecode using abstract interpretation: walk the bytecode forward, maintain a virtual register file mapping slots to SSA values, and specialize nodes based on the type feedback from M1.

### M3: Code Generation + Register Allocation + Deopt

Forward-walk register allocator (Maglev-style): maintain abstract register state, assign free registers to new values, spill farthest-next-use values when registers run out. No intervals, no backtracking.

Deoptimization stubs: at each guard point, record how to reconstruct the interpreter state from the JIT state. When a guard fails, read the hardware registers, rebuild the interpreter frame, and resume at the recorded bytecode offset.

### M4: Interpreter to Method JIT Tiering

Invocation counter per function. When a function has been called enough times with stable type feedback, compile it with the Method JIT. The trace JIT continues to handle hot loops inside Method JIT-compiled functions.

### M5: Optimization Passes + Function Inlining

Reuse existing optimization passes (CSE, constant hoisting, FMA fusion, dead code elimination) with basic-block awareness. Add monomorphic call-site inlining: if feedback shows a call site always calls the same function, inline it with a guard on callee identity.

---

## What we can reuse

The trace JIT rewrite was not wasted. It produced clean, modular infrastructure that the Method JIT will build on:

- **ARM64 assembler** (`assembler.go` family): all instruction encoding, branch patching, code buffer management. 100% reusable.
- **NaN-boxing value representation** (`value_layout.go`): box/unbox helpers, type tag constants, guard emission. 100% reusable.
- **Optimization passes** (`ssa_opt_*.go`): CSE, constant hoisting, FMA fusion, dead code elimination, strength reduction. These operate on `*SSAFunc` and need only minor adaptation for basic-block-aware iteration.
- **Snapshot concept**: the trace JIT's snapshot mechanism (slot-to-value mapping for deoptimization) is conceptually identical to what Method JIT deopt needs. It needs extension for multiple deopt points and branch merge points, but the core idea carries over.
- **Pass pipeline** (`ssa_pipeline.go`): the `BuildSSA -> [passes] -> RegAlloc -> Emit` architecture works for Method JIT too. New passes plug into the same framework.
- **Modular file structure**: the `ssa_emit_*.go` split (arithmetic, memory, control, table, intrinsics) keeps the emitter manageable. Method JIT emission follows the same pattern.

---

## What is different from V8

GScript is not JavaScript, and this is an advantage.

**Simpler type system.** Lua semantics means only 6 value types: Int, Float, String, Bool, Table, Nil. No prototypes, no classes, no `this` binding, no closures-as-classes, no Symbols, no BigInt. The type feedback lattice is tiny compared to V8's, which must handle Smis, HeapNumbers, Strings, Symbols, BigInts, multiple object shapes, and prototype chains.

**Simpler semantics.** No implicit type coercion (unlike JavaScript's `+` which can mean addition or string concatenation depending on operand types). No `arguments` object. No `with` statements. No getters/setters on tables. The graph builder has fewer special cases.

**Single target.** We target ARM64 on Apple Silicon only. V8 targets x64, ARM64, ARM32, MIPS, RISC-V, s390x, PPC. Our assembler, register allocator, and code emitter are all ARM64-specific, which means simpler code and better optimization for the one target that matters.

**Building from scratch.** V8 Maglev was built inside a 15-year-old codebase with decade-old abstractions (Handle, HeapObject, Map, FeedbackNexus). We can design our IR, feedback system, and deopt framework from scratch, using what we learned from V8 without inheriting its constraints.

---

## Risk assessment

This is a multi-session effort. Building a Method JIT with type feedback, CFG SSA, register allocation, and deoptimization is a significant project.

**What could go wrong:**

- **Deoptimization is hard.** Frame reconstruction from JIT state to interpreter state is the most complex part of any Method JIT. If we get the register mapping wrong, the interpreter resumes with corrupted state. V8's deoptimizer is thousands of lines of carefully tested code. This is the biggest technical risk.

- **Graph building is subtle.** Converting bytecode to SSA with phi nodes requires careful handling of loop back-edges, exception handlers, and dead code. Getting this wrong produces silently incorrect optimized code.

- **Tiering interactions.** When the Method JIT calls into the trace JIT for a hot inner loop, the two systems must agree on register conventions, stack layout, and deopt semantics. Getting the boundary wrong causes hard-to-debug corruption.

**What limits the downside:**

- **The trace JIT keeps working.** While we build the Method JIT, the trace JIT continues to accelerate loops. mandelbrot, nbody, matmul, table_field_access — all keep their speedups. We are not removing anything.

- **Incremental delivery.** Each milestone (M1 through M5) produces a usable artifact. M1 gives us type feedback data we can inspect. M2 gives us an SSA IR we can dump and verify. M3 gives us compiled code for simple functions. We do not need M5 to see value.

- **We know what broke before.** The function-entry tracing hack taught us exactly which interactions cause problems: register ownership across call boundaries, return value handling, multiple return values, variadic arguments. The Method JIT will address each of these by design, not by patch.

---

## Reflection

This project has changed direction twice now. We started with a two-tier JIT (Method + Trace), deleted the Method JIT to go trace-only, tried to hack function support back in, and are now building a new Method JIT.

It looks like we went in a circle. We did not.

The original Method JIT was a mess: no SSA, no snapshots, register allocation by hand, code generation interleaved with optimization. It worked for fib but could not be extended.

The trace JIT rewrite gave us clean SSA infrastructure, a proper pass pipeline, snapshot-based deoptimization, and a modular codebase. These are the foundations the new Method JIT will build on.

The function-entry tracing hack taught us what breaks: shared register state across call boundaries, missing frame metadata, the impossibility of treating function bodies as traces.

Each attempt narrowed the search space. Now we know what the architecture needs to look like, because we have seen three architectures that do not work. The fourth attempt is informed by all three failures and by V8's existence proof that the Method JIT approach works.

The trace JIT is not going anywhere. It handles hot loops better than a Method JIT would — that is what trace JITs are good at. The Method JIT handles everything the trace JIT cannot: functions, recursion, branches, polymorphism, inlining.

Together, they cover the whole program.
