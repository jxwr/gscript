---
layout: default
title: "Three Tiers"
permalink: /21-three-tiers
---
# Three Tiers

The Method JIT got 21.4x on sum(10000). It compiled every function we threw at it, ran tight loops in raw-int mode, inlined callees, and type-specialized arithmetic through four optimization passes. On micro-benchmarks, it looked like we had a real compiler.

Then we ran the benchmarks that matter.

---

## What went wrong

sieve(1M x3) ran 470x slower than LuaJIT. Not 470x slower than the old trace JIT -- 470x slower than LuaJIT. The Method JIT compiled the function, emitted ARM64, and executed it. It just produced the wrong inner loop: the emission layer confused NaN-boxed integers with raw integers, and the type guards fired on every iteration, falling back to the slow path for every addition.

fibonacci_iterative returned 0. Not a wrong answer -- zero. The loop variable's register was clobbered between the phi node resolution and the first use in the loop body. The graph builder produced correct SSA. The register allocator assigned correct registers. The emission layer wrote the prologue in the wrong order.

We ran Diagnose on both. The diagnostic tool was clear: the IR was correct, the passes were correct, the register allocation was correct. The bug was always in the emission layer -- the 5,600 lines of ARM64 code generation spread across 14 files (emit.go, emit_arith.go, emit_call.go, emit_call_exit.go, emit_execute.go, emit_loop.go, emit_op_exit.go, emit_reg.go, emit_table.go, tiering.go, tiering_execute.go, tiering_op_exit.go, and their test files).

These were not isolated bugs. They were symptoms of a design problem: one emission layer trying to handle both NaN-boxed values (for general code) and raw integers (for optimized loops), with implicit contracts between files about which representation a register holds at any given point. emit_arith.go assumed raw-int mode. emit_call.go assumed NaN-boxed mode. emit_loop.go switched between them based on flags that were set three files away.

We tried fixing them incrementally. Fix the sieve loop -- break fibonacci. Fix fibonacci's phi resolution -- break the call-exit path. Fix call-exit -- break table field access. The emission layer was a tangle of implicit state, and pulling one thread unraveled another.

---

## The industry got here first

V8 does not have one compiler. It has three:

**Sparkplug** (baseline): translates bytecodes 1:1 to machine code. No IR, no SSA, no optimization. Every value lives in the interpreter's register file. It compiles fast and runs 2-3x faster than the interpreter. Its job is to eliminate interpretation overhead, nothing more.

**Maglev** (mid-tier): builds a CFG-based SSA IR from bytecode, runs optimization passes (type specialization, constant folding, inlining), allocates registers, emits optimized machine code. Uses type feedback from the interpreter to speculate. Deoptimizes back to the interpreter when speculation fails. 5-10x over the interpreter.

**TurboFan** (top-tier): aggressive optimization. Sea of Nodes IR, escape analysis, range check elimination, loop-invariant code motion. 10-30x over the interpreter. Slow to compile, only used for the hottest functions.

The insight is separation of concerns. Sparkplug does not optimize -- it is simple enough to be correct by inspection. TurboFan does not worry about compilation speed -- it only runs on proven-hot code. Each tier has one job, and each tier can be tested independently.

We tried to build one tier that did everything. It compiled all functions (like Sparkplug), optimized hot loops (like Maglev), and emitted type-specialized raw-int code (like TurboFan). The result was an emission layer where every function had to handle three different value representations, two different calling conventions, and implicit mode flags that changed the semantics of every register.

---

## What we are keeping

The Method JIT is not a total loss. The upper half of the compiler -- everything above the emission layer -- works. It is tested, it is correct, and it represents months of work:

```
Keeping (~4,200 lines of proven infrastructure):

  ir.go                 (105)  IR types: Function, Block, Instr, Value, Type
  ir_ops.go             (219)  Op enum covering all 45 bytecodes
  graph_builder.go      (832)  Bytecode -> CFG SSA (Braun et al. 2013)
  graph_builder_ssa.go  (237)  SSA construction with phi insertion
  printer.go            (140)  Human-readable IR dump
  interp.go             (557)  IR interpreter (correctness oracle)
  interp_ops.go          (74)  IR interpreter call handling
  validator.go          (266)  Structural invariant checker
  pipeline.go           (255)  Pass pipeline registry
  pass_typespec.go      (258)  Type specialization
  pass_constprop.go     (256)  Constant propagation
  pass_dce.go            (96)  Dead code elimination
  pass_inline.go        (517)  Function inlining
  regalloc.go           (254)  Forward-walk register allocator
  diagnose.go           (292)  Unified diagnostic tool
```

Plus the low-level infrastructure in `internal/jit/`:

```
  assembler*.go         (759)  ARM64 instruction encoding
  trampoline.go          (14)  Go -> JIT bridge
  memory*.go            (119)  Executable memory allocation
  value_layout.go       (354)  NaN-boxing constants and helpers
```

The graph builder correctly converts any GScript function to CFG-based SSA. The IR interpreter executes that IR and produces the same results as the VM -- it is the correctness oracle that catches graph builder bugs without touching ARM64. The validator checks structural invariants after every pass. The optimization passes are individually tested and composable. The register allocator assigns hardware registers to SSA values.

All of this stays.

## What we are deleting

The emission layer and the single-tier tiering system. Fourteen files, roughly 5,600 lines:

```
Deleting:

  emit.go               (949)  Main emission dispatch, prologue/epilogue
  emit_arith.go         (213)  Arithmetic -- mixed NaN-boxed + raw-int
  emit_call.go          (350)  Call emission, float/div/not
  emit_call_exit.go     (249)  Call-exit resume handlers
  emit_execute.go       (304)  JIT execution loop
  emit_loop.go          (292)  Raw-int loop mode (correctness issues)
  emit_op_exit.go       (111)  Generic op-exit handlers
  emit_reg.go           (239)  Register-resident mode (cross-block bugs)
  emit_table.go         (535)  Table and field access emission
  tiering.go            (263)  Single-tier, buggy canCompile logic
  tiering_execute.go    (193)  Exit handlers
  tiering_op_exit.go    (196)  Op-exit handlers
  + test files          (~1,500)
```

The trace JIT in `internal/jit/` was already deprecated and disconnected from the CLI in an earlier round. Those files are also scheduled for deletion -- the assembler, memory manager, trampoline, and value layout remain.

The total deletion is roughly 5,600 lines of method JIT emission plus whatever remains of the trace JIT infrastructure. We are replacing it with a clean two-layer emission system.

---

## The new architecture

Three tiers, like V8. Each with a single responsibility.

### Tier 0: Interpreter

The interpreter in `internal/vm/vm.go`. Always available. Executes all bytecodes. Collects type feedback into a FeedbackVector on each function prototype. This is the ground truth -- every other tier must produce the same results.

The interpreter already exists and already collects feedback. No changes needed.

### Tier 1: Baseline Compiler

Like V8's Sparkplug. Translates bytecodes 1:1 to ARM64. No IR, no SSA, no optimization passes, no register allocation. Every value lives in the VM register file, NaN-boxed, exactly like the interpreter.

The compilation is a single walk over the bytecode array:

```
for each instruction in proto.Code:
    switch opcode:
    case OP_ADD:
        load operands from VM registers (NaN-boxed)
        type check: both int? ADD. both float? FADD. mixed? convert.
        store NaN-boxed result to VM register.
    case OP_CALL:
        save state, call Go helper, restore state
    case OP_GETFIELD:
        inline cache: shape guard + direct field offset
    ... all 45 opcodes
```

Key properties:
- **Compiles at first call.** No warmup. Every function gets Tier 1 immediately.
- **Handles ALL operations.** No `canCompile` rejection. No op-exit fallback. Every opcode has a native implementation, even if it is just "call the Go runtime helper."
- **Simple enough to be correct.** Each opcode is a self-contained template. No cross-instruction state, no implicit mode flags. If OP_ADD works in isolation, it works in any function.
- **Expected speedup: 3-5x over interpreter.** The gain comes from eliminating the interpreter's dispatch loop (switch on opcode, decode operands, indirect jump). The arithmetic itself is the same.

This is the tier that would have gotten sieve right. There is no raw-int mode to confuse it. There is no phi resolution that can be reordered. There is just: load, operate, store. For every instruction.

### Tier 2: Optimizing Compiler

Reuses the proven infrastructure -- the graph builder, the passes, the register allocator. But with a new emission layer, written from scratch, with clean separation:

```
BuildGraph -> TypeSpec -> ConstProp -> DCE -> Inline -> RegAlloc -> Emit
                                                                     |
                              (NEW, clean, file-per-concern)  <------+
```

The new emission layer:
- `tier2_compile.go` -- entry point, prologue/epilogue, block dispatch
- `tier2_emit_int.go` -- type-specialized integer arithmetic (raw integers in registers)
- `tier2_emit_float.go` -- float arithmetic in FPRs
- `tier2_emit_control.go` -- branches, loops, phi node resolution
- `tier2_emit_call.go` -- function calls via deoptimization to interpreter

Each file under 500 lines. Each file handles one concern. Each file tested independently.

Key properties:
- **Compiles at 500+ calls with stable feedback.** Only proven-hot functions get the full optimization pipeline.
- **Type-specialized arithmetic.** If feedback says `+` always sees integers, emit raw `ADD` with an overflow guard. No NaN-boxing in the hot path.
- **Deoptimization to Tier 0.** When a type guard fails, reconstruct the interpreter frame at the exact bytecode PC and resume there. The function does not restart -- it continues from where the guard failed.
- **Expected speedup: 10-30x over interpreter.** The gain comes from eliminating type checks, keeping values in registers, and inlining hot callees.

### Tier Manager

A simple routing table:

```
function called:
  if Tier 2 compiled -> run Tier 2
  if Tier 1 compiled -> run Tier 1, check if ready for Tier 2
  else               -> compile Tier 1, start collecting feedback
```

Tier-up is transparent to the caller. The same function, the same arguments, the same result. Just faster.

---

## Why this time is different

This is the third JIT rewrite. The honest history:

**v1: Two-tier JIT (trace + method).** Got fib(35) to 34ms, sieve to 23ms, mandelbrot to 158ms. But the code was unmaintainable -- no SSA, no snapshots, register allocation by hand, optimization interleaved with emission. Extending it to tables, closures, or inlining was impossible. We deleted it.

**v2: Trace-only JIT.** Clean SSA, proper snapshots, 11x on table access. But blind to function calls. fib went from 34ms back to 1,555ms. Three attempts to add function support all failed (shared register state, missing frame metadata, the impossibility of treating function bodies as traces). We pivoted.

**v3: Method JIT (single-tier).** CFG-based SSA, type feedback, four optimization passes, forward-walk register allocator. 21.4x on micro-benchmarks. But the emission layer was a tangle -- sieve 470x behind LuaJIT, fibonacci_iterative returns 0. The upper half works; the lower half does not.

**v3.1: Method JIT (three-tier).** Same upper half. New lower half. Two emission layers instead of one, each with a single job.

Each rewrite was not a circle. Each one preserved what worked and discarded what did not:

- v1 taught us that ad-hoc register allocation and interleaved optimization do not scale. It produced the benchmark numbers that told us what was possible.
- v2 gave us clean SSA infrastructure, the pass pipeline, the IR interpreter, the diagnostic tools. All of that carries forward.
- v3 gave us the graph builder, the register allocator, type specialization, constant propagation, DCE, inlining. 4,200 lines of tested, working compiler infrastructure.
- v3.1 keeps all of v3's infrastructure and replaces only the broken part: the emission layer.

The difference this time: we are not replacing the whole compiler. We are replacing 5,600 lines out of 14,400. The upper half has 200+ tests. The IR interpreter serves as a correctness oracle -- if the IR interpreter produces the right answer and the emitted code does not, the bug is in the emission layer, and nowhere else.

---

## The implementation plan

Seven steps, each independently verifiable.

**Step 1: Clean.** Delete the emission and tiering files. Verify the IR tests still pass. Verify all 21 benchmarks are correct in interpreter mode. This step only removes code.

**Step 2: Tier 1 skeleton.** Tier manager + baseline compiler handling 5 core ops (LOADINT, ADD, SUB, FORLOOP, RETURN). TDD: sum(10) = 55 via Tier 1. Wire into the VM.

**Step 3: Tier 1 arithmetic + control flow.** All arithmetic, all comparisons, all branches, all constants. Verify: sieve, mandelbrot, fibonacci_iterative produce correct results.

**Step 4: Tier 1 calls + globals + tables.** Function calls via Go helper, global access, table/field access with inline caches. Verify: fib, binary_trees, table_field_access correct.

**Step 5: Tier 1 complete.** All remaining opcodes: concat, len, closures, upvalues, vararg, goroutines. **Milestone: ALL 21 benchmarks produce correct results through Tier 1.** This is the "it works" checkpoint.

**Step 6: Tier 2 emission.** Rewrite the emission layer using the proven IR/passes/regalloc. Type-specialized int/float in registers. Deopt to interpreter on guard failure. Verify: sum(10000) > 10x over VM.

**Step 7: Full benchmark.** All 21 benchmarks, four modes: interpreter, Tier 1, Tier 2, LuaJIT.

---

## What we expect

Tier 1 should get 3-5x over the interpreter on everything. No benchmark should produce wrong results, because there is no optimization to get wrong. The baseline compiler is a mechanical translation -- if the interpreter handles an opcode correctly, the baseline compiler handles it correctly, because they do the same operations in the same order on the same data.

Tier 2 should get 10-30x on numeric benchmarks with stable types. sum, sieve, mandelbrot, fibonacci_iterative -- these have integer or float loops with predictable types. The optimization passes will specialize them to raw arithmetic, and the new emission layer will keep values in registers.

Benchmarks that depend on function calls (fib, ackermann, spectral_norm) will initially run at Tier 1 speed. Native BL calls and callee inlining are Phase D and Phase E in the roadmap -- they build on top of a working Tier 2, not alongside it.

The gap to LuaJIT will still be large on some benchmarks. LuaJIT has a hand-tuned C interpreter, trace-through-calls, allocation sinking, alias analysis, and a decade of optimization by Mike Pall. We are building a Method JIT from scratch, in Go, with AI agents. The target is not to match LuaJIT on day one. The target is to have a correct, maintainable compiler that can be incrementally improved -- one pass at a time, one tier at a time -- without the emission layer collapsing under its own complexity.

---

## Reflection

Three rewrites. Tens of thousands of lines written and deleted. The same benchmarks, measured against the same baseline, producing numbers that go up and down and up again.

It would be easy to call this wasted work. It is not.

The first JIT taught us what performance is possible. The second taught us what architecture is necessary. The third taught us where complexity hides. Each failure narrowed the search space. We no longer wonder whether a trace JIT can handle functions (it cannot, not without LuaJIT's frame-aware snapshots). We no longer wonder whether a single emission layer can handle both NaN-boxed and raw-int values (it cannot, not without implicit state that crosses file boundaries). We know these things because we tried them and watched them fail.

The three-tier architecture is not a guess. It is V8's answer to the same problem, arrived at by a team of 50+ engineers over 15 years. Sparkplug exists because V8 learned that baseline compilation is worth doing even if it is not optimizing. Maglev exists because V8 learned that mid-tier optimization needs its own compiler, not a mode flag on the top-tier. TurboFan exists because some code justifies aggressive optimization.

We are building the same thing, on a smaller scale, for a simpler language. The 4,200 lines of IR infrastructure are the foundation. The new emission layers are the part we have not gotten right yet. This time, by splitting them into two layers with clear boundaries, we give ourselves the best chance.

The code deletes will hurt. 5,600 lines of emit*.go and tiering*.go, representing weeks of work, gone. But keeping broken code because it was expensive to write is the sunk cost fallacy. The emission layer does not work. The evidence is in the benchmarks: sieve 470x behind LuaJIT, fibonacci_iterative returning 0. No amount of incremental patching will fix an architecture that conflates two different value representations in the same register.

Delete. Rebuild. Test. This time with two layers instead of one, and the experience of three attempts to know what each layer needs to do.
