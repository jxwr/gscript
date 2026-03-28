# Method JIT Research Report: V8 Maglev Architecture and Roadmap for GScript

**Date**: 2026-03-28
**Author**: Research Agent (Claude Opus 4.6)
**Purpose**: Deep investigation of V8 Maglev JIT compiler, related method JIT systems, and design of a roadmap for implementing a Maglev-inspired method JIT in GScript.

---

## Table of Contents

1. [V8 Maglev Architecture Summary](#1-v8-maglev-architecture-summary)
2. [Type Feedback + Deoptimization](#2-type-feedback--deoptimization)
3. [Function Inlining](#3-function-inlining)
4. [Comparison: Other Method JITs](#4-comparison-other-method-jits)
5. [Trace JIT vs Method JIT: Fundamental Tradeoffs](#5-trace-jit-vs-method-jit-fundamental-tradeoffs)
6. [Mapping to GScript](#6-mapping-to-gscript)
7. [Minimum Viable Method JIT](#7-minimum-viable-method-jit)
8. [Risk Assessment](#8-risk-assessment)
9. [References](#9-references)

---

## 1. V8 Maglev Architecture Summary

### 1.1 Position in V8's Pipeline

V8 uses a 4-tier compilation strategy:

```
Ignition (interpreter, collects type feedback)
    |  (8 invocations)
    v
Sparkplug (baseline JIT, no optimizations, eliminates dispatch overhead)
    |  (500 invocations, stable feedback)
    v
Maglev (mid-tier optimizing JIT, SSA + CFG, speculative optimizations)
    |  (6000 invocations)
    v
Turbolev/TurboFan (top-tier optimizing JIT, aggressive optimization)
```

**Key insight**: feedback stability matters. If type feedback changes during warmup, the invocation counter resets to zero. This prevents premature optimization on unstable types.

**Recent development (2025)**: V8 is replacing TurboFan with **Turbolev** -- a new top-tier compiler that takes Maglev's IR and feeds it into the Turboshaft backend (a CFG-based backend replacing TurboFan's Sea of Nodes). The pipeline becomes: Maglev IR -> Turboshaft Graph -> machine code. This validates the CFG-based approach over Sea of Nodes.

### 1.2 Compilation Unit

Maglev compiles at the **function level**. The entire function's bytecode is processed into an SSA CFG. This is fundamentally different from trace JIT (which compiles one execution path through potentially multiple functions).

### 1.3 IR Representation

Maglev uses a **CFG-based SSA IR** -- not Sea of Nodes:

- **Basic blocks** form the control flow graph
- **SSA values** represent computation results
- **Phi nodes** handle value merging at control flow joins
- **Nodes know how to emit assembly** -- each IR node has a `GenerateCode()` method

The IR encodes **specialized JavaScript semantics** directly. Rather than lowering JS operations to generic operations and then re-specializing, Maglev keeps JS-specific nodes (e.g., `CheckMaps`, `LoadNamedField`, `Int32AddWithOverflow`).

### 1.4 Graph Building Process

The compilation happens in phases:

**Phase 1: Prepass**
- Scans bytecode for branch targets, loops, variable assignments in loops
- Collects **liveness information** (which registers are live at each point)
- This liveness data reduces the amount of state tracked during graph building

**Phase 2: SSA Construction (single pass)**
- Performs **abstract interpretation** over bytecode
- Maintains an abstract frame state mapping bytecode registers to SSA values
- Creates specialized SSA nodes using type feedback from Ignition
- **Loop phis are pre-created** before the loop body, enabling single-pass construction
- When paths merge (e.g., if/else join), Phi nodes are inserted

**Phase 3: Known Node Information ("do as much as possible during graph building")**
- Rather than building generic nodes and lowering later, Maglev specializes immediately
- If `o.x` always accesses objects with shape S, it generates: `CheckMap(o, S)` + cheap field load
- "De-facto constant" globals are embedded directly with deopt dependencies (no runtime check needed)
- Distinguishes **unstable** info (requires runtime guard) from **stable** info (can register dependency, no guard)

### 1.5 Optimization: Representation Selection

After graph building, a dedicated phase chooses optimal numeric representations:

- Values can be **tagged** (NaN-boxed / Smi) or **untagged** (raw int32, float64)
- The compiler tries to keep values unboxed as long as possible
- Phi nodes force representation decisions at merge points
- Reboxing only happens when strictly necessary (e.g., storing to heap)

This is directly analogous to GScript's `SSA_UNBOX_INT` / `SSA_BOX_INT` operations.

### 1.6 Register Allocation

Maglev uses a **single forward walk** register allocator:

1. **Prepass**: computes linear live ranges for all SSA values (next-use distances)
2. **Forward walk**: maintains abstract machine register state
   - Reuses registers when values are dead
   - Assigns free registers to new values
   - Spills low-priority (farthest next-use) values when out of registers
3. **Branch merges**: consolidates abstract register states from predecessors
4. **Phi handling**: prefers to put phi output in same register as one of its inputs
5. **Stack frame split**: tagged and untagged regions, with a split point for GC safety

This is simpler than linear scan (no intervals, no backtracking) but works well because Maglev's code is relatively simple. GScript's current frequency-based slot allocator is even simpler but limited; the Maglev approach would be a significant upgrade.

### 1.7 Code Generation

Each Maglev node directly emits assembly via a macro assembler. A **Parallel Move Resolver** handles register-to-register moves at merge points, preventing value clobbering through careful ordering.

### 1.8 Full Pipeline Summary

```
Bytecode + FeedbackVector
    |
    v
[Prepass] - find branches, loops, liveness
    |
    v
[Graph Building] - abstract interpretation, SSA construction, specialize with feedback
    |
    v
[Graph Verification] - (debug only)
    |
    v
[Preprocessing] - value location constraints, max call depth, use marking
    |
    v
[Register Allocation] - forward walk with spilling
    |
    v
[Code Generation] - nodes emit assembly, parallel move resolver
    |
    v
[Finalization] - produce executable code, register deopt dependencies
```

### 1.9 Compile Time vs Code Quality

| Tier | Compile Speed | Code Quality | When |
|------|--------------|--------------|------|
| Sparkplug | ~instant | baseline (no optimization) | 8 calls |
| Maglev | 10x slower than Sparkplug | good (speculative, unboxed) | 500 calls |
| Turbolev/TurboFan | 10x slower than Maglev | best (aggressive inlining, escape analysis) | 6000 calls |

---

## 2. Type Feedback + Deoptimization

### 2.1 Type Feedback Collection (Ignition)

V8's Ignition interpreter collects feedback into a **FeedbackVector** attached to each function closure:

- **BinaryOp slots**: records input/output type lattice (None -> SignedSmall -> Number -> NumberOrOddball -> String -> BigInt)
- **Property access slots**: records hidden classes (Maps) seen + property offsets
- **Call slots**: records callee function(s) seen
- **Compare slots**: records operand types

**Critical property**: the feedback lattice is **monotonic** -- it can only broaden, never narrow. `SignedSmall` -> `Number` is allowed; `Number` -> `SignedSmall` is not. This prevents deopt-reopt cycles.

### 2.2 How Maglev Uses Feedback

During graph building, Maglev reads the FeedbackVector to specialize nodes:

- If `+` always saw integers: emit `Int32AddWithOverflow` + deopt on overflow
- If `o.x` always saw shape S: emit `CheckMap(o, S)` + direct field load at known offset
- If a call site always called function F: emit direct call + guard on callee identity
- "De-facto constants" (globals that never changed): embed value, register invalidation dependency

### 2.3 Deoptimization

**When speculation fails, Maglev deoptimizes (bails out) to the interpreter.**

Three types of deoptimization in V8:

1. **Eager deopt**: a guard in the compiled code fails (e.g., type check). Immediately transfers to interpreter.
2. **Lazy deopt**: external code invalidates an assumption (e.g., a "de-facto constant" global changes). The next return to the compiled code triggers deopt.
3. **Soft deopt**: function was compiled too early, before types stabilized.

### 2.4 How Deoptimization Works

**At compile time**: Maglev attaches an **abstract interpreter frame state** to every node that can deoptimize. This state maps interpreter registers to SSA values.

**At code generation**: this frame state becomes **DeoptimizationInputData** -- metadata describing how to reconstruct the interpreter state from JIT state (which registers/stack slots hold which interpreter register values, at which bytecode offset to resume).

**At runtime** (when a guard fails):
1. Read current hardware register file and stack into a buffer
2. Look up the DeoptimizationInputData for the failing guard
3. Build interpreter frame(s) from the optimized frame using the mapping
4. Resume execution in the interpreter at the recorded bytecode offset

**Key insight**: Maglev reuses TurboFan's existing deoptimization infrastructure, including the frame translation mechanism and the `TranslatedState` / `FrameWriter` components.

### 2.5 Comparison with GScript's Snapshots

GScript already has a snapshot-based deoptimization mechanism for trace side-exits:

```go
type Snapshot struct {
    PC      int         // bytecode PC for interpreter recovery
    Entries []SnapEntry // slot -> SSA value mappings (only modified slots)
}
```

This is conceptually similar to V8's `DeoptimizationInputData`, but simpler:
- V8 must reconstruct full interpreter frames (multiple frames for inlined functions)
- GScript snapshots only need to store-back modified slots to the VM register array
- GScript's NaN-boxing means the snapshot entries are just 8-byte values

**For a method JIT, GScript's snapshot infrastructure needs extension** to handle:
- Multiple deopt points per function (not just per-guard in a linear trace)
- Branch merge points (phi resolution before deopt)
- Inlined function frames (if inlining is added)

---

## 3. Function Inlining

### 3.1 V8's Approach

V8 uses several inlining strategies across tiers:

**Maglev inlining** (conservative):
- Looks at call-site feedback to identify monomorphic callees
- Inlines small functions during graph building
- Budget-limited: large functions are never inlined

**TurboFan inlining** (aggressive):
- Multiple levels of recursive inlining
- Polymorphic dispatch inlining (for 2-4 known callees)
- Inlining budget based on cumulative bytecode size

**Inlining heuristics** (general V8):
- Tiny functions: almost always inlined
- Large functions: never inlined
- Maximum inlining budget per compilation unit
- Feedback-driven: only inline callees actually observed at this call site

### 3.2 Deoptimization of Inlined Frames

When inlined code needs to deopt, V8 must reconstruct **multiple** interpreter frames:

```
Compiled frame (caller + inlined callees merged)
    -> deopt ->
[Caller frame @ bytecode offset X] + [Callee frame @ bytecode offset Y]
```

The DeoptimizationInputData includes frame descriptors for each inlined call, allowing the deoptimizer to rebuild the full call stack.

### 3.3 SpiderMonkey's Trial Inlining

SpiderMonkey (Warp) has an innovative **trial inlining** approach:
- Each function gets an **ICScript** storing CacheIR data
- Before compilation, scan for inlinable call sites
- Create **caller-specialized ICScripts** for callees
- When Warp compiles, it has caller-specific type feedback, producing optimal code
- This works recursively: inlined callees get their own specialized feedback

### 3.4 GScript Comparison

GScript's trace JIT already does a form of "natural inlining" -- when the trace recorder follows a CALL instruction, the callee's body is recorded inline into the trace. This provides:
- Zero-cost inlining of the hot path through callees
- No explicit inlining decision needed (tracing does it automatically)
- But: only one path is captured (no support for polymorphic dispatch)

For a method JIT, GScript would need explicit inlining with:
- Analysis of call site feedback to identify monomorphic callees
- Bytecode size budgeting
- Frame reconstruction metadata for deopt of inlined frames

---

## 4. Comparison: Other Method JITs

### 4.1 JavaScriptCore DFG JIT

JSC's DFG is a mid-tier optimizing compiler (similar role to Maglev):

- **IR**: DFG uses its own IR with `GetLocal`/`SetLocal` instead of traditional Phi nodes
- **Type inference**: scrapes type information from LLInt and Baseline JIT inline caches
- **SSA conversion**: DFG IR is first built in CPS form, then lowered to SSA for FTL (top tier)
- **Aggressive**: DFG performs type inference, dead code elimination, and speculative optimization
- **FTL (top tier)**: takes DFG's SSA form and runs through LLVM-quality optimizations

### 4.2 SpiderMonkey Warp

Warp replaced the old IonMonkey frontend:

- **CacheIR**: a linear bytecode format describing inline cache behavior
- **WarpOracle** (main thread): snapshots Baseline CacheIR data
- **WarpBuilder** (background thread): mechanically transpiles CacheIR to MIR (middle IR)
- **Optimizer** (background thread): optimizes MIR and generates machine code
- **Key innovation**: using the SAME CacheIR format for baseline IC stubs AND optimizing compilation input
- **Trial inlining**: caller-specialized feedback for inlined callees

### 4.3 Copy-and-Patch Compilation

From the 2021 OOPSLA paper (Xu & Kjolstad, Stanford):

- **Idea**: pre-compile bytecode semantics to binary "stencils" at build time using Clang/LLVM; at runtime, `memcpy` stencils and patch in runtime values
- **Speed**: compiles 2 orders of magnitude faster than LLVM -O0; code runs 1 order of magnitude faster than interpretation
- **For Lua (Deegen)**: 19.1M bytecodes/second compilation, ~91 bytes of machine code per bytecode
- **Completely branchless** codegen functions enable maximum ILP on modern CPUs

### 4.4 Deegen (Haoran Xu, 2024)

A meta-compiler that auto-generates both interpreter and baseline JIT from bytecode semantics:

- **Input**: bytecode semantics as C++ functions
- **Output**: state-of-the-art interpreter + baseline JIT + tiering logic
- **LuaJIT Remake (LJR)** results:
  - Interpreter: 179% faster than PUC Lua, 31% faster than LuaJIT's interpreter
  - Baseline JIT: ~34% slower than LuaJIT's trace JIT on average, but **faster on 13/44 benchmarks**
  - Compilation: 19.1M bytecodes/second
- **Key techniques**: bytecode specialization, register pinning, call IC, generic IC, JIT polymorphic IC, type-check removal, hot-cold code splitting, OSR-entry
- **Implication for GScript**: a baseline JIT without ANY speculative optimization can get within 34% of LuaJIT's trace JIT. This is the minimum viable bar.

### 4.5 V8 Sparkplug (Baseline)

Sparkplug is V8's non-optimizing baseline compiler:

- Translates bytecode 1:1 to machine code (no IR, no optimization)
- Most operations call into shared "builtins" (same code as interpreter)
- Two passes: discover loops, then generate code
- Nearly instant compilation
- Purpose: eliminate interpreter dispatch overhead (~5-15% speedup)

---

## 5. Trace JIT vs Method JIT: Fundamental Tradeoffs

### 5.1 Trace JIT Advantages

1. **Automatic path specialization**: traces naturally follow the hot path, inlining across function boundaries without explicit decisions
2. **Simpler optimizations**: with minimal control flow (traces are linear), optimizations like CSE, dead code elimination, and load elimination are trivially local
3. **Better code for hot loops**: all time is spent on code that actually executes; cold branches are simply side-exits
4. **Free call-site specialization**: each trace through a function is specialized for the calling context

### 5.2 Trace JIT Disadvantages

1. **Trace explosion**: programs with many unpredictable branches generate too many traces
2. **Nested loop problem**: inner loops must be compiled first; outer loops linking to inner traces adds complexity
3. **Polymorphic dispatch**: traces commit to one path; polymorphic call sites generate trace trees
4. **Performance cliffs**: when tracing goes wrong, performance can be dramatically worse than interpreter
5. **No method-level optimization**: can't optimize across the entire function body (only the observed path)

### 5.3 Method JIT Advantages

1. **Compile once, cover all paths**: every path through the function is compiled
2. **No trace explosion**: the compilation unit is bounded (one function)
3. **Natural support for branches**: if/else compiles to machine code branches (no side-exits for normal control flow)
4. **Predictable performance**: no cliffs from trace abort/blacklisting
5. **Function inlining**: can make cross-function optimization decisions

### 5.4 Method JIT Disadvantages

1. **Must compile cold code too**: time spent compiling paths that rarely execute
2. **Explicit type speculation needed**: must use feedback to speculate (traces get this for free)
3. **More complex deoptimization**: must handle deopt at any point in the function (not just guard failures in a linear trace)
4. **Inlining is hard**: must decide what to inline based on heuristics

### 5.5 Why LuaJIT Succeeds with "Just" a Trace JIT

LuaJIT's success comes from several factors:

1. **Hand-written assembly interpreter**: the interpreter itself is extremely fast (not C/Go)
2. **Lua's simplicity**: fewer types, simpler semantics, no hidden prototype chains
3. **NaN-boxing**: compact 8-byte values (GScript already has this)
4. **Excellent trace abort heuristics**: Mike Pall spent years tuning when to abort/retry/blacklist
5. **SSA IR with advanced optimizations**: alias analysis, allocation sinking, LPEG optimizations
6. **Numerical focus**: Lua code tends to be loop-heavy and numeric, which traces handle well

### 5.6 The Hybrid Opportunity

**The ideal for GScript is both**: a method JIT for broad function coverage + trace JIT for hot loops within those functions.

V8's multi-tier approach validates this: Sparkplug eliminates dispatch overhead for ALL code, Maglev optimizes warm functions, and TurboFan/Turbolev maximizes hot functions. GScript can replicate this:

```
Interpreter (current)
    |
    v
Baseline Method JIT (new: compile whole function, type-specialized arithmetic)
    |  (hot loops detected)
    v
Trace JIT (existing: record and optimize hot loop bodies)
```

---

## 6. Mapping to GScript

### 6.1 What GScript Already Has (Reusable)

| Component | GScript Status | Reusable for Method JIT? |
|-----------|---------------|-------------------------|
| SSA IR (`SSAOp`, `SSAInst`, `SSAFunc`) | Mature, value-based SSA | Partially -- needs extension for CFG (basic blocks, branch targets, phi nodes at merge points) |
| Optimization passes (CSE, ConstHoist, DCE, LoadElim, StrengthReduce, FMA) | Working, well-tested | Yes -- passes operate on `*SSAFunc`, just need basic-block awareness |
| Register allocator | Frequency-based slot alloc + linear scan float | Needs replacement with forward-walk allocator for method JIT |
| Assembler (`assembler.go` family) | Full ARM64 support | 100% reusable |
| Code emission helpers (`value_layout.go`) | NaN-box/unbox, guard type | 100% reusable |
| Snapshot/deopt mechanism | Per-guard-point slot->value mapping | Needs extension for multiple deopt points in branching code |
| Pipeline framework (`ssa_pipeline.go`) | Pass -> Pass -> Pass architecture | 100% reusable |
| Bytecode VM | Register-based, Lua-style opcodes | Interpreter feedback collection needs to be added |
| NaN-boxing runtime | 8-byte values, type tags | 100% reusable |
| Inline field cache (`FuncProto.FieldCache`) | Per-instruction shape+offset cache | Can drive field access specialization |
| Self-call mechanism | Native BL for recursive calls | Can be generalized for method JIT function calls |

### 6.2 What GScript Needs to Build

#### 6.2.1 Type Feedback Collection (Priority: HIGH)

GScript's interpreter currently collects NO type feedback. This is the single biggest gap.

**What to build**: a `FeedbackVector` attached to each `FuncProto`:

```go
type FeedbackSlot struct {
    Kind     FeedbackKind  // BinaryOp, PropertyAccess, Call, Compare
    TypeSeen uint8         // bitset: Int|Float|String|Bool|Table|Nil
    Stable   bool          // has it stopped changing?
}

type FeedbackVector struct {
    Slots []FeedbackSlot  // one per instruction (or per feedback-relevant instruction)
}
```

For GScript's Lua-like type system, the feedback is simpler than V8's:
- **Arithmetic**: did we see Int, Float, or both?
- **Comparisons**: Int vs Int? Float vs Float? Mixed?
- **Table access**: always integer key? Always string key? What shape?
- **Calls**: always the same function? (monomorphic)

**Implementation**: add feedback collection to the VM's main dispatch loop. For each arithmetic/comparison/table/call instruction, record the types observed. This adds a small per-instruction overhead (~2-5ns) but provides the data needed for speculative compilation.

#### 6.2.2 CFG-based SSA IR (Priority: HIGH)

GScript's current SSA IR is a linear array of instructions with a single `SSA_LOOP` marker. For a method JIT, we need:

```go
type BasicBlock struct {
    ID         int
    Insts      []SSAInst
    Preds      []*BasicBlock  // predecessor blocks
    Succs      []*BasicBlock  // successor blocks (0, 1, or 2)
    Phis       []Phi          // phi nodes at block entry
    DeoptState *FrameState    // interpreter state for deopt (nil if no deopt possible)
}

type Phi struct {
    Dst    SSARef
    Type   SSAType
    Slot   int16        // original VM register
    Inputs []PhiInput   // (block, value) pairs
}

type SSAFunction struct {
    Blocks   []*BasicBlock
    Entry    *BasicBlock
    Proto    *vm.FuncProto
    Feedback *FeedbackVector
}
```

**Graph building** follows Maglev's approach:
1. Prepass: scan bytecode for branch targets, build block boundaries
2. Abstract interpretation: walk bytecode, maintain register->SSARef mapping, create nodes
3. At merge points: insert phi nodes
4. Use feedback to specialize operations (e.g., `ADD` with Int feedback -> `SSA_ADD_INT`)

#### 6.2.3 Method-Level Deoptimization (Priority: HIGH)

Extend the existing snapshot mechanism:

```go
type FrameState struct {
    PC       int                    // bytecode PC to resume at
    Locals   map[int]SSARef         // register -> SSA value (for this frame)
    Inlined  *FrameState            // callee frame state (for inlined calls)
}
```

Every node that can deoptimize (guards, speculative ops) gets a `FrameState`. At codegen, this becomes metadata enabling the deoptimizer to reconstruct interpreter state.

**GScript advantage**: since GScript uses a flat register array (not stack frames), deopt is simpler than V8. We just need to write modified registers back to `regs[]` and set the PC. No frame reconstruction needed unless we implement inlining.

#### 6.2.4 Forward-Walk Register Allocator (Priority: MEDIUM)

Replace the frequency-based slot allocator with a Maglev-style forward walk:

1. Prepass: compute next-use distance for each SSA value
2. Walk blocks in order:
   - Assign free registers to new values
   - When out of registers, spill the value with the farthest next-use
   - At block boundaries, insert moves to reconcile register assignments
3. Handle phi nodes: prefer input and output in same register

This will dramatically improve register usage for non-loop code (current allocator only works well for loops).

#### 6.2.5 On-Stack Replacement (Priority: LOW, for later)

OSR allows entering JIT code from the interpreter mid-loop. Currently GScript can only enter JIT code at loop headers or function entries. Full OSR requires:
- Mapping interpreter state to JIT register assignments at the OSR entry point
- Generating an OSR entry stub that loads interpreter values into registers

### 6.3 Architecture Decision: Separate or Unified IR?

**Option A: Extend existing SSA IR** to support basic blocks and branches.
- Pro: reuse existing passes, emission code
- Con: significant refactoring of linear-array assumptions

**Option B: New method JIT IR**, separate from trace JIT IR.
- Pro: clean design, no risk of breaking trace JIT
- Con: code duplication (assembler helpers, emission)

**Recommendation: Option B (new IR) with shared assembler layer.** The trace JIT's linear IR is deeply embedded in its operation (loop markers, pre-loop guards, side-exit model). A method JIT's CFG-based IR is fundamentally different. Share the assembler (`Assembler` struct) and value helpers (`EmitBoxInt`, `EmitGuardType`, etc.) but build a new graph builder and code generator.

This matches V8's approach: Maglev has its own IR and graph builder, separate from TurboFan, but shares the assembler and deoptimization framework.

---

## 7. Minimum Viable Method JIT

### 7.1 What the MVP Must Do

The simplest method JIT that beats the interpreter on most benchmarks:

1. **Compile entire function to native code** (all opcodes handled, either natively or via call-exit)
2. **Type-specialize arithmetic** using feedback (int+int -> native ADD, float+float -> native FADD)
3. **Optimize for-loops** (register-pinned loop variables, no memory traffic)
4. **Handle branches** (if/else compiles to native CMP + B.cond)
5. **Call-exit for unsupported ops** (string ops, metatables, generic calls)
6. **Deoptimize to interpreter** when type guards fail

### 7.2 What the MVP Does NOT Need

- Function inlining (add later)
- Escape analysis (add later)
- Loop unrolling (trace JIT handles hot loops)
- Advanced register allocation (simple forward walk is enough)
- OSR (can enter at function start only)
- Polymorphic inline caches (monomorphic only for MVP)

### 7.3 MVP Pipeline

```
FuncProto + FeedbackVector
    |
    v
[Prepass] - find branch targets, loop headers, liveness
    |
    v
[Graph Build] - abstract interpretation, specialize with feedback
    |  Creates BasicBlock CFG with SSA values
    v
[Simple Opts] - CSE within blocks, constant folding, dead code elimination
    |
    v
[Register Alloc] - forward walk, 12 GPR + 8 FPR (ARM64 callee-saved)
    |
    v
[Code Gen] - emit assembly per block, insert deopt stubs
    |
    v
[Install] - store code pointer in FuncProto.JITEntry
```

### 7.4 Expected Impact

Based on Deegen's results (baseline JIT within 34% of LuaJIT's trace JIT), and GScript's benchmark data, the MVP method JIT should:

| Benchmark | Current JIT Speedup | Expected with Method JIT |
|-----------|-------------------|--------------------------|
| fib(35) | 33.6x (trace JIT self-call) | Comparable or better (direct recursion) |
| fannkuch(9) | 1.2x | 3-5x (branchy code, trace JIT struggles) |
| sort(50K) | 1.0x | 2-3x (branchy, call-heavy) |
| mutual_recursion | 0.8x (SLOWER) | 3-5x (cross-function calls) |
| spectral_norm | 0.9x (SLOWER) | 3-8x (nested function calls) |
| sum_primes | 0.9x | 2-4x (branchy inner loop) |
| closure_bench | error | 1.5-3x (closure calls compiled) |
| ackermann | error | 5-10x (deep recursion) |

The method JIT should **unlock benchmarks that the trace JIT cannot handle**: branchy code, recursive code, and polymorphic call sites.

### 7.5 Phased Roadmap

#### Phase M1: Feedback Collection (1 week of agent work)
- Add `FeedbackVector` to `FuncProto`
- Instrument VM dispatch loop for arithmetic, comparison, table access, call instructions
- Record type bitsets per instruction
- Stability detection (stop updating after N iterations without change)
- Tests: verify feedback is collected correctly for all benchmark programs

#### Phase M2: CFG-based SSA IR (1-2 weeks)
- Define `BasicBlock`, `Phi`, `SSAFunction` types
- Implement graph builder: bytecode -> CFG SSA with type specialization
- Prepass for branch targets and liveness
- Abstract interpretation with phi insertion at merge points
- Tests: verify correct SSA construction for all control flow patterns (if/else, for, while, nested)

#### Phase M3: Code Generation (1-2 weeks)
- Emit assembly for each basic block
- Forward-walk register allocator (simplified Maglev approach)
- Deopt stubs at each guard point (write registers back to VM, jump to interpreter)
- Call-exit mechanism for unsupported opcodes
- Handle for-loops (FORPREP/FORLOOP) with register pinning
- Tests: verify correctness on all 21 benchmarks

#### Phase M4: Integration + Tiering (1 week)
- Install compiled code in `FuncProto.JITEntry`
- Tiering logic: interpreter -> method JIT after N calls with stable feedback
- Method JIT -> trace JIT for hot loops (existing infrastructure)
- Deopt: method JIT guard failure -> interpreter (reset feedback, recompile later)
- Benchmarks: run full suite, compare VM/Method-JIT/Trace-JIT/LuaJIT

#### Phase M5: Optimization Passes (ongoing)
- CSE across basic blocks
- Load elimination (consecutive field accesses to same object)
- Constant propagation across branches
- Dead branch elimination (based on feedback: branch never taken)
- Loop-invariant code motion
- Function inlining (monomorphic call sites)
- Self-recursive optimization (BL to same code, like current trace JIT)

---

## 8. Risk Assessment

### 8.1 Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| Method JIT is slower than trace JIT on loop-heavy benchmarks | Medium | Keep trace JIT as Tier 2 for hot loops; method JIT is Tier 1 |
| Type feedback overhead slows interpreter | Medium | Measure before/after; feedback collection can be disabled for cold code |
| CFG-based SSA is significantly more complex than linear trace SSA | High | Start with simplest possible implementation; no inlining in MVP |
| Register allocator complexity | Medium | Forward walk is simpler than linear scan; Maglev proves it works |
| Deoptimization correctness | High | Reuse existing snapshot infrastructure where possible; extensive testing |
| Two JIT compilers = double maintenance burden | High | Share assembler layer; consider deprecating old method JIT (already stubbed out) |
| GScript's dynamic semantics (metatables, etc.) make speculation fragile | Medium | Conservative speculation; fall back to call-exit for anything with metatables |

### 8.2 Why This Might Not Work

1. **LuaJIT's interpreter is in hand-written assembly**. GScript's interpreter is in Go, with garbage collector overhead. Even a perfect method JIT won't match LuaJIT if the interpreter fallback path (call-exits, deopt) is 5-10x slower.

2. **Trace JIT's natural advantages for numeric loops** may mean the method JIT doesn't help on GScript's strongest benchmarks (fib, mandelbrot, nbody). The method JIT's value is on benchmarks the trace JIT can't handle.

3. **Go's calling convention and GC** add overhead to JIT transitions. Every call-exit and deopt must go through the Go runtime.

### 8.3 Why This Should Work

1. **The benchmarks where GScript loses worst** (spectral_norm 117x, mutual_recursion 63x, fannkuch 26x, sort 17x) are exactly the benchmarks where trace JIT fails (branchy, recursive, polymorphic). A method JIT directly addresses these.

2. **GScript's existing SSA infrastructure** (passes, assembler, NaN-boxing) is solid. The method JIT builds on proven components.

3. **Deegen proves the concept**: even a baseline JIT (no speculation!) gets within 34% of LuaJIT. With speculation based on type feedback, GScript should do significantly better.

4. **The hybrid approach** (method JIT + trace JIT) gives the best of both worlds: method JIT handles branchy/recursive code, trace JIT handles tight loops.

---

## 9. References

### V8 Maglev
- [Maglev - V8's Fastest Optimizing JIT](https://v8.dev/blog/maglev) -- primary source, detailed architecture
- [Land ahoy: leaving the Sea of Nodes](https://v8.dev/blog/leaving-the-sea-of-nodes) -- why V8 abandoned Sea of Nodes for CFG
- [V8 is Faster and Safer than Ever (2023)](https://v8.dev/blog/holiday-season-2023) -- Maglev launch context
- [Maglev source code directory](https://chromium.googlesource.com/v8/v8/+/refs/heads/main/src/maglev/) -- full source listing
- [Maglev compiler pipeline (maglev-compiler.cc)](https://chromium.googlesource.com/v8/v8/+/f73f3b3b5122b806d898c8799da2c104d6bc2c56/src/maglev/maglev-compiler.cc)
- [Expanding to Turbolev (Seokho's blog)](https://blog.seokho.dev/development/2025/07/15/V8-Expanding-To-Turbolev.html) -- Turbolev as new top tier

### V8 Type Feedback and Deoptimization
- [An Introduction to Speculative Optimization in V8 (Benedikt Meurer)](https://benediktmeurer.de/2017/12/13/an-introduction-to-speculative-optimization-in-v8/) -- comprehensive feedback + deopt explanation
- [Notes about V8 Deoptimization (yuvaly0)](https://yuvaly0.github.io/2021/02/26/notes-v8-deoptimization) -- eager/lazy/soft deopt details
- [Deoptimization in V8 (Google Slides)](https://docs.google.com/presentation/d/1Z6oCocRASCfTqGq1GCo1jbULDGS-w-nzxkbVF7Up0u0/htmlpresent)
- [A lighter V8](https://v8.dev/blog/v8-lite) -- FeedbackVector memory impact
- [Profile-Guided Tiering in V8 (Intel)](https://community.intel.com/t5/Blogs/Tech-Innovation/Client/Profile-Guided-Tiering-in-the-V8-JavaScript-Engine/post/1679340) -- tiering thresholds (8/500/6000)

### V8 Sparkplug (Baseline)
- [Sparkplug -- a non-optimizing JavaScript compiler](https://v8.dev/blog/sparkplug)

### JavaScriptCore DFG
- [FTL JIT (WebKit wiki)](https://trac.webkit.org/wiki/FTLJIT)
- [Type Inference (WebKit docs)](https://docs.webkit.org/Deep%20Dive/JSC/JSCTypeInference.html)
- [Understanding JavaScriptCore's DFG JIT (sillycross)](https://sillycross.github.io/2021/09/20/2021-09-20/)
- [Static Analysis in JavaScriptCore (sillycross)](https://sillycross.github.io/2021/09/12/2021-09-12/)

### SpiderMonkey Warp
- [Warp: Improved JS performance in Firefox 83 (Mozilla Hacks)](https://hacks.mozilla.org/2020/11/warp-improved-js-performance-in-firefox-83/) -- CacheIR, WarpBuilder, trial inlining
- [Fast(er) JavaScript on WebAssembly (Chris Fallin)](https://cfallin.org/blog/2023/10/11/spidermonkey-pbl/)

### Copy-and-Patch / Deegen
- [Copy-and-Patch Compilation (OOPSLA 2021 paper)](https://fredrikbk.com/publications/copy-and-patch.pdf)
- [Building the fastest Lua interpreter automatically (Haoran Xu)](https://sillycross.github.io/2022/11/22/2022-11-22/)
- [Building a baseline JIT for Lua automatically (Haoran Xu)](https://sillycross.github.io/2023/05/12/2023-05-12/) -- 19.1M bytecodes/sec, within 34% of LuaJIT
- [Deegen: A JIT-Capable VM Generator (arXiv 2024)](https://arxiv.org/html/2411.13469v1)
- [Deegen: LLVM-based Compiler-Compiler (LLVM Dev Meeting slides)](https://llvm.org/devmtg/2023-10/slides/techtalks/Xu-Deegen-LLVM-Based-Compiler.pdf)

### Trace JIT vs Method JIT
- [Musings on Tracing in PyPy (2025)](https://pypy.org/posts/2025/01/musings-tracing.html) -- tracing advantages (path specialization, simpler opts), disadvantages (cliffs, branchy code)
- [How JIT Compilers are Implemented and Fast (kipply)](https://kipp.ly/jits-impls/)
- [Trace-based JIT Type Specialization (Cornell CS 6120)](https://www.cs.cornell.edu/courses/cs6120/2019fa/blog/tbjit-type-specialization/)
- [Polymorphic Inline Caches Explained (Jay Conrod)](https://jayconrod.com/posts/44/polymorphic-inline-caches-explained)

### LuaJIT
- [LuaJIT Official](https://luajit.org/luajit.html)
- [LuaJIT Wikipedia](https://en.wikipedia.org/wiki/LuaJIT)

### On-Stack Replacement
- [OSR in the CLR (.NET runtime)](https://github.com/dotnet/runtime/blob/main/docs/design/features/OnStackReplacement.md)
- [Bril JIT with On-Stack Replacement (Cornell CS 6120)](https://www.cs.cornell.edu/courses/cs6120/2019fa/blog/bril-osr/)
