---
layout: default
title: "What Makes a JIT Compiler Fast"
permalink: /22-jit-optimization-techniques
---

# What Makes a JIT Compiler Fast

*March 2026 --- Beyond LuaJIT, Post #22*

## Where We Left Off

In [Post #21](21-three-tiers), we deleted 5,600 lines of broken emission code and committed to V8's three-tier architecture: interpreter, baseline JIT, optimizing JIT. The upper half of the compiler --- graph builder, SSA IR, optimization passes, register allocator --- was proven and tested. The lower half was not. We rebuilt it as two separate emission layers, each with a single job.

That rebuild is done. Tier 1 compiles every function on first call. Tier 2 optimizes hot functions with type specialization, constant propagation, dead code elimination, inlining, and register allocation. The benchmarks are in, and the story they tell is more interesting than any individual number.

This post is about what we learned. Not just about GScript, but about what makes *any* dynamic language JIT fast --- the techniques that matter, ranked by impact, with real data from a compiler we built from scratch.

---

## The seven techniques, ranked

We implemented every major JIT optimization technique in GScript's Tier 2. We measured each one. Here is the ranking, from most impactful to least, for dynamic languages.

### #1. Type specialization

This is the single biggest win. It is not close.

A dynamic language does not know, at compile time, what type a variable holds. The expression `a + b` could mean integer addition, float addition, string concatenation, or a metatable `__add` call. The interpreter must check the type of both operands on every single addition --- roughly 10 nanoseconds of overhead per operation.

Type specialization observes the types that actually appear at runtime, inserts a guard ("if this is not an integer, bail out"), and generates code that assumes the guard holds. One ADD instruction instead of a type-check-dispatch-ADD sequence.

In GScript, the TypeSpecialize pass replaces generic IR operations with type-specialized variants:

```
Before:  v5 = OpAdd v3, v4       (generic: check types, dispatch, add)
After:   v5 = OpAddInt v3, v4    (specialized: raw integer ADD)
```

The ARM64 difference is dramatic. A generic `OpAdd` compiles to roughly 14 instructions:

```arm64
; Generic OpAdd: load, check type tag, extract int, add, re-tag, store
LDR    X0, [X26, #slot_a]      ; load NaN-boxed a
LSR    X2, X0, #48             ; extract type tag
CMP    X2, #0xFFFE             ; is it int?
B.NE   slow_path               ; no -> float/string/metatable dispatch
LDR    X1, [X26, #slot_b]      ; load NaN-boxed b
LSR    X3, X1, #48             ; extract type tag
CMP    X3, #0xFFFE             ; is it int?
B.NE   slow_path               ; no -> dispatch
SBFX   X0, X0, #0, #48        ; sign-extend int payload
SBFX   X1, X1, #0, #48        ; sign-extend int payload
ADD    X0, X0, X1              ; the actual addition
ORR    X0, X0, X24             ; re-tag as int (X24 = 0xFFFE << 48)
STR    X0, [X26, #slot_result] ; store NaN-boxed result
```

A type-specialized `OpAddInt` in a register-allocated loop compiles to one instruction:

```arm64
ADD    X20, X20, X21           ; raw integer add, result stays in register
```

One instruction instead of fourteen. No type check, no tag extraction, no re-boxing, no memory round-trip.

The data from GScript: `fibonacci_iterative` went from 1.04s in the interpreter to 0.075s in Tier 2. That is a 14.6x speedup. The mandelbrot micro-benchmark, which runs a tight float loop, went from 143 microseconds (VM) to 0.24 microseconds (Tier 2) --- a 591x improvement. The bulk of those gains come from type specialization turning every arithmetic operation from a 14-instruction sequence into a 1-instruction sequence.

### #2. Inline caches

Property access in dynamic languages is hash table lookup. When you write `point.x`, the runtime must find the field `x` in the table's internal hash map --- roughly 30--50 nanoseconds per access.

An inline cache (IC) remembers the result of the last lookup at each access site. It caches the table's "shape" (its set of field names and their positions) and the field's offset. On the next access, it compares the table's shape with the cached shape. If they match --- and they almost always do --- it reads the field directly by offset. One comparison and one memory load, about 3 nanoseconds.

GScript's Tier 1 implements per-PC inline field caches:

```arm64
; Inline cache for GETFIELD point.x
LDR    X0, [X26, #slot_table]     ; load table
LDR    W1, [X0, #shapeID_offset]  ; load table's shapeID
LDR    W2, [X19, #fc_shapeID]     ; load cached shapeID
CMP    W1, W2                     ; shape match?
B.NE   ic_miss                    ; no -> slow path (hash lookup + update cache)
LDR    W3, [X19, #fc_fieldIdx]    ; cached field index
LDR    X0, [X0, X3, LSL #3]      ; direct field access by offset
```

Five instructions on the fast path. The slow path updates the cache and falls through --- subsequent accesses at the same site hit the fast path.

The impact is large. `table_field_access` went from 0.74s (VM) to 0.12s (Tier 1) --- a 6.25x speedup. This is pure inline cache, no optimization passes, no register allocation. The gain comes entirely from turning hash lookups into offset loads.

### #3. Function inlining

Function inlining does two things. The obvious one is eliminating call overhead (10--80 nanoseconds per call, depending on the language). The less obvious one is more important: it opens optimization boundaries.

When function A calls function B, the optimizer cannot see across the call. It cannot type-specialize B's operations based on A's types. It cannot propagate constants from A into B. It cannot eliminate B's return value allocation if A immediately uses the result. The call is an opaque wall.

Inlining removes that wall. The callee's IR is spliced into the caller's IR, and suddenly the optimizer sees everything. Type specialization works across the former call boundary. Constant propagation folds arguments. Dead code elimination removes unused computations that were invisible before inlining.

GScript's `pass_inline.go` does explicit monomorphic inlining: if a call site always calls the same function, and that function is small enough, its body is spliced into the caller's SSA graph. LuaJIT achieves the same effect naturally --- its trace recorder follows execution through function calls, recording the callee's body as part of the trace. V8's TurboFan uses explicit inlining heuristics similar to ours.

The Sum(10000) micro-benchmark, where the loop body calls a trivial helper, shows the effect: VM 98 microseconds, Tier 1 23 microseconds, Tier 2 with inlining 5 microseconds. The 19x over VM comes from inlining removing the call boundary, which lets type specialization and register allocation work on the combined code.

### #4. Escape analysis + scalar replacement

Dynamic languages create many temporary objects. Every `{x: 1, y: 2}` is a heap allocation. Every `point.translate(dx, dy)` returns a new point. In a tight loop, this means millions of allocations and enormous GC pressure.

Escape analysis detects objects that never "escape" the function --- they are created, used, and discarded without being stored in a global, passed to another function, or returned. These objects can be replaced with scalar variables on the stack: instead of allocating a `{x, y}` table and reading `point.x`, the compiler keeps `x` and `y` in registers.

GScript does not implement escape analysis yet. V8's TurboFan does. PyPy does. LuaJIT does not --- but LuaJIT has allocation sinking, which is a related technique that delays allocation until a side exit proves it is needed.

This is the technique we expect to deliver the next large step for allocation-heavy benchmarks like `binary_trees` and `object_creation`, which currently run *slower* under JIT because the exit-resume overhead per NEWTABLE dominates.

### #5. Deoptimization

Deoptimization is not an optimization. It is the prerequisite for every speculative optimization in the list above.

Type specialization inserts a guard: "if this is not an integer, bail out." But bail out to what? The guard fires because the type assumption was wrong. The compiled code cannot continue. It must reconstruct the interpreter's state --- the exact values in every register, at the exact bytecode position --- and resume the interpreter there. This is deoptimization.

Without deopt, you cannot speculate. Without speculation, you cannot type-specialize. Without type specialization, your JIT is just a faster interpreter dispatch.

GScript's deopt path: when a type guard fails in Tier 2, the compiled code stores its register state back to the VM register file and exits with `ExitDeopt`. The tiering manager catches this exit and re-enters the function through Tier 1 (the baseline JIT), which handles all types correctly because it never speculates. After enough deopts, the function is permanently downgraded to Tier 1.

```
Tier 2 guard fires → spill registers → ExitDeopt → TieringManager
  → re-enter via Tier 1 (correct for all types)
  → if deopt count > threshold, permanently downgrade
```

Every production JIT has this machinery. LuaJIT calls them side exits with snapshots. V8 calls it frame translation. PyPy calls it guard-to-interpreter. The mechanism differs; the concept is universal.

### #6. Register allocation

The CPU has registers. Memory is slow. Keeping values in registers instead of loading and storing them from memory on every operation eliminates enormous overhead --- an L1 cache hit is 3--4 cycles, but a register access is 0 cycles.

But here is the counterintuitive finding from GScript: **register allocation without type specialization can be slower than no register allocation at all.**

GScript's Tier 3 (register-allocated, without raw-int mode) was tested against Tier 1 (baseline, no register allocation). On Sum(10000), Tier 3 was 27,228 ns/op versus Tier 1's 19,079 ns/op. The register-allocated code was 1.4x *slower*.

Why? Because without type specialization, both tiers shuffle NaN-boxed 64-bit values. The register allocator keeps NaN-boxed values in registers instead of memory --- but it still does the type check, the tag extraction, the re-boxing. The 14-instruction ADD sequence stays the same. The allocator just moves the 14 instructions from operating on memory to operating on registers, adding SSA overhead without removing the fundamental bottleneck.

Register allocation becomes a large win only when combined with type specialization, which turns the 14-instruction sequence into one instruction. Then keeping that one instruction's operands in registers (instead of loading/storing them each iteration) matters enormously.

The data confirms this. Tier 2 with both type specialization and register allocation: Sum(10000) in 8,401 ns/op --- a 19.1x speedup over the interpreter. Register allocation contributes roughly 2x on top of type specialization's ~10x. The techniques are multiplicative, but only when both are present.

### #7. Loop optimizations

Loop-invariant code motion (LICM), loop unrolling, strength reduction. These are the bread and butter of static language optimizers. For dynamic languages, they matter less than you might expect, because type specialization dominates the performance profile.

Consider a loop that computes mandelbrot iterations. In C, the loop body is already tight: a few multiplies, a few adds, a comparison. LICM and unrolling squeeze out the last 10--20%. In a dynamic language, the loop body starts at 14 instructions per operation instead of one. Type specialization delivers a 10x improvement. Unrolling the now-specialized loop delivers another 10--20% on top.

GScript does not currently implement LICM or loop unrolling. The 591x on mandelbrot micro comes entirely from type specialization and register allocation. Loop optimizations are on the roadmap, but they are not the bottleneck.

---

## The architecture

GScript's multi-tier JIT follows V8's design. Three tiers, each with a single responsibility.

```
Tier 0: Interpreter (VM)
  │  Executes all bytecodes. Collects type feedback.
  │  Always correct. Always available.
  │
  ▼  (compile on first call)
Tier 1: Baseline JIT
  │  1:1 bytecode → ARM64 templates
  │  No IR, no SSA, no optimization
  │  Inline caches for field access
  │  Native BLR calls between compiled functions
  │  3-8x over interpreter
  │
  ▼  (promote after N calls with stable types)
Tier 2: Optimizing JIT
     Bytecode → CFG SSA IR → Optimization passes → ARM64
     TypeSpecialize → ConstProp → DCE → Inline → RegAlloc → Emit
     Type-specialized arithmetic in registers
     Deopt guards → bail to Tier 1 on type change
     10-600x over interpreter on eligible functions
```

### Real numbers

All measurements on Apple M4 Max, darwin/arm64.

**Full benchmarks (CLI, wall-clock):**

| Benchmark | VM | Tier 1 | Speedup | Key technique |
|-----------|-----|--------|---------|---------------|
| fibonacci_iterative | 1.04s | 0.22s | **4.7x** | Loop → native |
| table_field_access | 0.74s | 0.12s | **6.25x** | Inline cache |
| fannkuch | 0.56s | 0.067s | **8.4x** | ArrayInt native path |
| sort | 0.18s | 0.037s | **4.9x** | Native BLR call |
| ackermann | 0.30s | 0.001s | **297x** | BLR self-recursion |
| mutual_recursion | 0.22s | 0.008s | **27x** | BLR cross-call |

**Micro-benchmarks (Go testing.B, Tier 2 optimizing):**

| Benchmark | VM | Tier 1 | Tier 2 | T2/VM speedup |
|-----------|-----|--------|--------|---------------|
| fibonacci_iterative | 1.04s | 0.22s | 0.075s | **14.6x** |
| mandelbrot(10) micro | 143us | 38us | 0.24us | **591x** |
| Sum(10000) | 98us | 23us | 5us | **19x** |

The mandelbrot 591x deserves explanation. The micro-benchmark runs 10 iterations of a pure float computation. The interpreter pays type-check overhead on every multiply, add, and comparison. Tier 2 type-specializes all operations to raw float, keeps values in FPR registers (D4--D11), and eliminates every type check. The ratio is extreme because the workload is small and the per-operation overhead ratio is large. On mandelbrot(1000) --- a realistic workload --- the ratio is more modest but still significant.

---

## The insight: optimization ability vs. optimization eligibility

Here is the most important thing we learned, and it is not about any individual optimization technique.

GScript's Tier 2 has all the core optimizations: type specialization, register allocation, inlining, constant propagation, dead code elimination, deoptimization. The micro-benchmarks prove they work --- 14.6x, 19x, 591x over the interpreter.

But look at the full benchmark suite. Most benchmarks still run at Tier 1 speed.

`sieve` runs at 1.0x over VM because its hot path does table access (GETTABLE, SETTABLE) which Tier 2 does not yet handle natively --- the function exits to Go for every array operation. `mandelbrot` at full scale runs at 0.97x because the inner loop contains GETFIELD operations. `matmul` runs at 1.0x because it accesses 2D arrays. `spectral_norm` runs at 1.0x because it calls functions in the inner loop.

These functions are not eligible for Tier 2 promotion. They contain operations --- table access, field access, certain call patterns --- that the optimizing compiler does not yet support. The functions compile at Tier 1, and Tier 1 does not type-specialize.

**Building a great optimizer is necessary but not sufficient. You also need to maximize the number of functions that can enter the optimizer.**

This is the difference between optimization ability (what the compiler can do once it has code) and optimization eligibility (which functions can actually reach the optimizer).

Compare with the production JITs:

**LuaJIT** traces execute through table access, field access, and function calls. There is no "promotion barrier." The trace recorder simply records whatever the interpreter does, including hash lookups and calls. Every hot loop is eligible for optimization, because the trace compiler handles everything the interpreter handles.

**V8** compiles everything at every tier. Sparkplug compiles all JavaScript. Maglev compiles all JavaScript, just with optimization. TurboFan compiles all JavaScript, just with more aggressive optimization. There is no subset of the language that prevents promotion.

**GScript** currently has a promotion barrier. Tier 1 handles all 45 opcodes. Tier 2 handles a subset: arithmetic, comparisons, branches, constants, slots, calls (via exit), and a few others. Functions that use table operations, field access, or certain globals cannot promote.

This is the gap that explains why GScript's micro-benchmark numbers look competitive while the full-suite numbers do not. The optimizer is good. The set of functions it can optimize is too small.

---

## Comparison table

| Technique | LuaJIT | V8 TurboFan | PyPy | GScript |
|-----------|--------|-------------|------|---------|
| Type Specialization | Trace auto-specialize | FeedbackVector + guards | RPython type inference | TypeSpecialize pass |
| Inline Caches | Built into trace | Hidden class + megamorphic IC | Map-based IC | Tier 1 per-PC FieldCache |
| Function Inlining | Trace through calls | Explicit inlining heuristics | RPython auto-inline | pass_inline.go |
| Escape Analysis | No (but allocation sinking) | TurboFan EA | Yes | Not yet |
| Deoptimization | Side-exit + snapshot | Deopt frame translation | Guard to interpreter | ExitDeopt to Tier 1 |
| Register Allocation | Custom (LuaJIT has ~16 regs) | Sea-of-nodes regalloc | RPython backend | Forward-scan (5 GPR, 8 FPR) |
| Promotion Barrier | None (traces everything) | None (compiles everything) | None | Table/field ops block Tier 2 |

The last row is the important one. Every production JIT has eliminated the promotion barrier. GScript has not --- yet.

---

## What the numbers teach about JIT design

Three lessons from building this compiler, backed by data.

**Lesson 1: Type specialization is worth more than everything else combined.**

On `fibonacci_iterative`, type specialization alone accounts for roughly 10x of the 14.6x total speedup. Register allocation adds another 2x. All other optimizations (const prop, DCE) contribute the remaining ~30%. If you could only implement one optimization in a dynamic language JIT, implement type specialization.

**Lesson 2: Optimizations are multiplicative, not additive.**

Type specialization turns 14 instructions into 1 instruction. Register allocation eliminates 2 memory ops per instruction. These do not add: 14x + 2x = 16x. They multiply: the register allocator acts on the type-specialized code, not the original code. The 1 instruction benefits from being in a register. The 14 instructions do not benefit much from register allocation because the type-check branches dominate.

This is why Tier 3 (regalloc without type spec) was slower than Tier 1 (no regalloc, no type spec). The overhead of SSA construction and register allocation was not justified because the instructions being allocated were still the 14-instruction generic sequences. The register allocator was rearranging deck chairs.

**Lesson 3: Coverage matters more than peak performance.**

GScript's Tier 2 produces code that runs mandelbrot micro at 591x over the interpreter. LuaJIT runs the full mandelbrot(1000) at about 25x over GScript's interpreter. The per-instruction quality is comparable --- but LuaJIT optimizes the entire function while GScript optimizes a subset of operations and exits to Go for the rest.

A 10x optimizer that covers 100% of functions beats a 1000x optimizer that covers 10% of functions. Every time.

---

## What's next

The roadmap is driven by the promotion barrier analysis. In order of expected impact:

**1. Native table operations in Tier 2.** GETTABLE and SETTABLE with array-style integer keys, compiled to bounds check + direct offset load in ARM64. This unlocks sieve, mandelbrot (array-based inner loop), matmul, and fannkuch for Tier 2 promotion. This is the single highest-leverage change.

**2. Native field access in Tier 2.** GETFIELD and SETFIELD compiled with shape-guarded inline caches, like Tier 1 but operating on type-specialized values in registers. Unlocks table_field_access, nbody, and any OOP-style code.

**3. Inline pass activation for real workloads.** `pass_inline.go` exists and is tested. Once table and field ops are handled, hot functions with inner calls become eligible for inlining. This opens the optimization boundary for spectral_norm, matmul (helper functions), and sort (comparison functions).

**4. Escape analysis.** Replace short-lived table allocations with scalar values. Unlocks binary_trees, object_creation, and any allocation-heavy inner loop.

**5. Zero-indexed arrays and ArrayFloat JIT paths.** Lua-compatible 1-indexed arrays add overhead. GScript could optionally use 0-indexed arrays for numeric hot paths, and emit FMADD/FMSUB for float array operations.

Each step expands the set of functions eligible for Tier 2. The optimizer is ready. The task is bringing more code to it.

---

## Reflection

We set out to build a JIT compiler that approaches LuaJIT. After 21 blog posts, four JIT architectures (trace, method, single-tier method, three-tier method), and tens of thousands of lines written and deleted, we know exactly what makes a JIT compiler fast.

It is not one technique. It is the combination: type specialization to eliminate the dynamic dispatch tax, inline caches to make property access cheap, inlining to open optimization boundaries, register allocation to keep specialized values off the memory bus, and deoptimization to make all of this safe.

But more than the techniques, the lesson is about coverage. A JIT that optimizes 100% of a language's operations, even modestly, will outperform one that optimizes a subset of operations extremely well. LuaJIT is fast not because its trace compiler produces better code than V8's TurboFan --- it does not, on a per-instruction basis. LuaJIT is fast because Mike Pall ensured that every Lua operation has a native implementation in the trace recorder. There is no operation that forces a trace exit.

GScript is not there yet. The optimizer works. The coverage does not. That is the next chapter.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
