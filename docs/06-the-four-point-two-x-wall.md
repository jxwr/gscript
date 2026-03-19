---
layout: default
title: "The 4.2x Wall"
permalink: /06-the-four-point-two-x-wall
---

# The 4.2x Wall

*March 2026 --- Beyond LuaJIT, Post #6*

## Where We Left Off

In [Post #5](05-the-blacklist-that-changed-everything), we went from 1.53x to **6.09x** on mandelbrot. The headline improvements were native GETFIELD/SETFIELD codegen, `math.sqrt` as an ARM64 FSQRT intrinsic, and --- most significantly --- the abort-blacklisting fix that stopped millions of wasted recording attempts. The CPU profile at the end of Post #5 showed 58% of execution time in the interpreter (outer loops) and only 21% in JIT native code. The next target was clear: nested loop tracing, so the JIT covers the outer loops too.

Here is what actually happened when we tried to close that gap.

## Sub-Trace Calling

The first step toward nested loop tracing was sub-trace calling. Instead of recording all three loops as one monolithic trace (which we tried and failed at in Post #5 --- the SSA builder could not handle `FORPREP` inside a trace), we took LuaJIT's approach: compile each loop independently and have the outer trace *call* the inner trace as a subroutine.

The mechanism is straightforward. When the outer for-x loop's trace encounters the inner for-iter loop, it emits an `SSA_CALL_INNER_TRACE` instruction. The generated ARM64 does the following:

```arm64
// SSA_CALL_INNER_TRACE: outer loop calls inner loop

// Step 1: Spill all allocated registers back to VM register array
STR   X20, [X26, #slot0*32+8]    // store int reg 0 to memory
FSTR  D4,  [X26, #slot3*32+8]    // store float reg 0 to memory
FSTR  D5,  [X26, #slot4*32+8]    // store float reg 1 to memory
...                               // (one STR per allocated register)

// Step 2: Swap constants pointer to inner trace's pool
LDR   X0, [X19, #InnerConstants]
LDR   X1, [X19, #Constants]      // save outer constants
STR   X0, [X19, #Constants]      // set inner constants

// Step 3: Call inner trace
LDR   X8, [X19, #InnerCode]
STP   X29, X1, [SP, #-16]!       // push outer constants
MOV   X0, X19                    // arg0 = TraceContext
BLR   X8                         // call inner trace

// Step 4: Restore outer constants + check exit code
LDP   X29, X1, [SP], #16
STR   X1, [X19, #Constants]
LDR   X0, [X19, #ExitCode]
CMP   X0, #2
B.EQ  side_exit

// Step 5: Reload all allocated registers from memory
LDR   X20, [X26, #slot0*32+8]
FLDR  D4,  [X26, #slot3*32+8]
FLDR  D5,  [X26, #slot4*32+8]
...
```

Even triple-nested loops work: the for-y trace calls the for-x trace, which calls the for-iter trace. Each level spills, calls, and reloads.

The problem is visible in the code above: every sub-trace call requires a full spill of all allocated registers *before* the call and a full reload *after* the call. For mandelbrot, the inner trace runs 50 iterations per pixel (on average), and the for-x trace runs 1,000 times per row. Each sub-trace call adds roughly 20 instructions of pure overhead --- stores, loads, constant-pointer swaps, exit-code checks. Per pixel.

The improvement was about 5%. Not the 2-3x we hoped for. The sub-trace spill/reload cost per pixel nearly offsets the benefit of running the outer loops in native code. The 58% interpreter time from Post #5 dropped to about 35%, but the overhead shifted from "interpreter dispatch" to "sub-trace calling overhead." A lateral move.

## VM Inline Field Cache

While stuck on the nested loop question, we turned to the interpreter side. The CPU profile showed `MulNums` and other boxed-float arithmetic consuming 12% of execution time. But another line item stood out: field access in nbody.

GScript's `RawGetString` does a linear scan over the table's `skeys` slice:

```go
func (t *Table) RawGetString(key string) Value {
    for i, k := range t.skeys {
        if k == key {
            return t.svals[i]
        }
    }
    // ... fallback to hash map
}
```

For nbody, every body has fields `x`, `y`, `z`, `vx`, `vy`, `vz`, `mass`. That is 7 keys per table, and `vx` is at index 3 or 4. Every field access does a linear scan over 3-4 string comparisons. Millions of times.

The fix: per-instruction inline cache entries. Each `GETFIELD`/`SETFIELD` bytecode instruction gets a `FieldCacheEntry` that remembers the index of the field name from the last lookup:

```go
type FieldCacheEntry struct {
    FieldIdx int // cached index into skeys/svals (-1 = not cached)
}

func (t *Table) RawGetStringCached(key string, cache *FieldCacheEntry) Value {
    // Hint check: O(1) if the field is at the cached index
    if cache.FieldIdx >= 0 && cache.FieldIdx < len(t.skeys) {
        if t.skeys[cache.FieldIdx] == key {
            return t.svals[cache.FieldIdx]
        }
    }
    // Miss: linear scan, then update cache
    for i, k := range t.skeys {
        if k == key {
            cache.FieldIdx = i
            return t.svals[i]
        }
    }
    return NilValue()
}
```

The cache works across different table instances with the same field layout. In nbody, all five body tables have the same keys in the same order. The first access to `body.vx` fills the cache; every subsequent access --- even on a different body table --- hits the cache because `vx` is at the same index. One comparison instead of a linear scan.

Result: nbody improved 8.8%. A solid win for a small change.

## The Head-to-Head

With sub-trace calling and the field cache in place, it was time for the comparison we had been avoiding. Not "how fast compared to our interpreter" --- that is a game we can always win by making the interpreter slower. The real question: **how fast compared to LuaJIT?**

I installed LuaJIT 2.1, ported mandelbrot to Lua (identical algorithm, identical constants, identical iteration count), and ran both side by side.

```
GScript mandelbrot(1000):  0.236s    (trace JIT)
LuaJIT  mandelbrot(1000):  0.056s    (trace JIT)

Gap: 4.2x
```

We are 6.3x faster than our own interpreter. We are 4.2x slower than LuaJIT.

The 6.3x number felt good until we put it next to LuaJIT's. LuaJIT's trace JIT runs mandelbrot at roughly 27x its own interpreter speed. Our trace JIT runs it at 6.3x. Even accounting for the fact that our interpreter is slower than LuaJIT's (GScript values are 32 bytes vs LuaJIT's 8-byte NaN-boxed TValues), the JIT itself is generating significantly worse native code.

This is the wall. And understanding *why* it is the wall requires looking inside the inner loop.

## Anatomy of 174 Instructions

The inner trace --- the for-iter loop that does the actual Mandelbrot computation --- compiles to **696 bytes** of ARM64 machine code. That is **174 instructions**. The loop body itself (one iteration of the for-iter loop) takes roughly 50 instructions.

Let us count what happens in those 50 instructions:

```
Prologue / guards:          ~6 instructions
  - Type guards on loop variables (LDRB + CMP + B.NE per guard)
  - Load loop counter and limit

Computation (the actual math):
  zr * zr                    3 instructions  (resolve + FMUL + store)
  zi * zi                    3 instructions
  zr*zr - zi*zi              3 instructions
  + cr                       3 instructions
  2.0 * zr                   3 instructions
  * zi                       3 instructions
  + ci                       3 instructions
  zr*zr (again, for escape)  3 instructions
  zi*zi (again, for escape)  3 instructions
  zr*zr + zi*zi              3 instructions
  > 4.0                      2 instructions  (FCMP + B.cond)

Subtotal math:              ~32 instructions

Loop counter update:        ~4 instructions
  - ADD counter, #1
  - CMP counter, limit
  - B.LE trace_loop

Store-back / spills:        ~8 instructions
  - Write updated float values back to memory for non-allocated slots
  - Type tag writes for store-back

Total per iteration:        ~50 instructions
```

Now consider the theoretical minimum. The Mandelbrot inner loop has 7 floating-point operations (4 multiplies, 2 adds, 1 subtract) plus the escape comparison. If everything lived in registers with no spills:

```
FMUL  D_zr2, D_zr, D_zr       // zr*zr
FMUL  D_zi2, D_zi, D_zi       // zi*zi
FSUB  D_tmp, D_zr2, D_zi2     // zr*zr - zi*zi
FADD  D_zr_new, D_tmp, D_cr   // + cr → new zr
FMUL  D_tmp2, D_zr, D_zi      // zr * zi
FADD  D_tmp2, D_tmp2, D_tmp2  // 2 * zr * zi  (FADD is cheaper than FMUL by 2.0)
FADD  D_zi_new, D_tmp2, D_ci  // + ci → new zi
FADD  D_esc, D_zr2, D_zi2     // zr*zr + zi*zi  (reuse zr2 and zi2 from above)
FCMP  D_esc, D_four           // > 4.0?
B.GT  escape
FMOV  D_zr, D_zr_new          // rename (or eliminated by allocator)
FMOV  D_zi, D_zi_new          // rename
ADD   W_i, W_i, #1
CMP   W_i, W_limit
B.LE  loop

Theoretical: ~15 instructions
```

We generate 50. That is 3.3x the theoretical minimum. The extra 35 instructions per iteration are resolve-from-memory loads, store-to-memory spills, scratch register shuffling, and redundant type tag operations. At 50 million iterations per mandelbrot(1000) run, those 35 extra instructions cost roughly 150 milliseconds.

LuaJIT's inner loop for the same computation is approximately 18-20 instructions. Nearly optimal.

## Why Frequency Fails

The root cause of the 50-instruction inner loop is the register allocator. Here is the current algorithm, from `ssa_codegen.go`:

```go
func newFloatSlotAlloc(f *SSAFunc) *floatSlotAlloc {
    freq := make(map[int]int)
    for _, ir := range f.Trace.IR {
        if ir.AType == runtime.TypeFloat { freq[ir.A]++ }
        if ir.BType == runtime.TypeFloat && ir.B < 256 { freq[ir.B]++ }
        if ir.CType == runtime.TypeFloat && ir.C < 256 { freq[ir.C]++ }
    }
    // ... sort by frequency, assign top N to D4-D11 ...
}
```

The allocator counts how many times each VM slot appears in the trace IR, sorts by frequency, and assigns the top 8 slots to physical D registers. Slots that do not make the cut live in memory --- every use requires a load, every definition requires a store.

This works well when a few slots dominate. In nbody, `body.x` and `body.vx` are accessed far more than other fields --- the frequency allocator correctly prioritizes them. But mandelbrot's inner loop defeats it.

Look at the computation again:

```
tr := zr * zr - zi * zi + cr
ti := 2.0 * zr * zi + ci
zr = tr
zi = ti
```

The VM slots involved are: `zr`, `zi`, `cr`, `ci`, `tr`, `ti`, plus the temporaries for `zr*zr`, `zi*zi`, `2.0*zr`, etc. The bytecode compiler assigns each subexpression to a separate VM slot. In the trace IR, every slot is used exactly 2-3 times: once as a source, once as a destination, maybe once more for the escape check. The frequency distribution is *flat*. No slot dominates.

With 8 allocable float registers (D4-D11) and 10-12 float-typed VM slots, the allocator picks 8 winners and leaves 2-4 losers. But the "losers" are not cold --- they are intermediate results used once per iteration. Every use of a non-allocated slot means:

1. **Load from memory** (FLDR): 1 instruction, ~4 cycle latency
2. **Compute** (FMUL/FADD/FSUB): 1 instruction
3. **Store back to memory** (FSTR): 1 instruction

That is 3 instructions where a register-to-register operation would be 1.

We tried extending to 12 float registers (D4-D15). No improvement. The frequency allocator just assigns the extra 4 registers to more equally-weighted slots. The problem is not the *number* of registers --- it is the *allocation strategy*. Frequency-based allocation assigns one register to one slot for the entire trace lifetime. A value that is born, used once, and dies should share a register with the next value that is born after it dies.

This is the textbook problem that live-range analysis solves.

## What a Live-Range Allocator Would Do

Consider the mandelbrot inner loop's float values and their lifetimes:

```
zr*zr   : born at instruction 3, last used at instruction 8     [3..8]
zi*zi   : born at instruction 5, last used at instruction 9     [5..9]
zr*zr-zi*zi : born at instruction 6, last used at instruction 7 [6..7]
tr      : born at instruction 7, last used at instruction 12    [7..12]
2.0*zr  : born at instruction 8, last used at instruction 9     [8..9]
2.0*zr*zi : born at instruction 9, last used at instruction 10  [9..10]
ti      : born at instruction 10, last used at instruction 12   [10..12]
zr*zr (escape): reuse from instruction 3                        [3..11]
zi*zi (escape): reuse from instruction 5                        [5..11]
escape_sum : born at instruction 11, last used at instruction 11 [11..11]
```

The maximum number of simultaneously live float values is 5. Five values alive at any single point. We have 8 float registers. A live-range allocator would assign registers so that non-overlapping intervals share the same physical register:

- D4: `zr*zr` [3..8], then `2.0*zr*zi` [9..10] (non-overlapping, share D4)
- D5: `zi*zi` [5..9], then `ti` [10..12]
- D6: `zr*zr-zi*zi` [6..7], then `2.0*zr` [8..9], then `escape_sum` [11..11]
- D7: `tr` [7..12]
- D8: `zr` (loop-carried)
- D9: `zi` (loop-carried)
- D10: `cr` (loop-invariant)
- D11: `ci` (loop-invariant)

Zero spills. Zero loads from memory during the loop body. Every float value lives and dies in a register. The inner loop drops from 50 instructions to approximately 18-20 --- right at LuaJIT's level.

The seminal paper for this is Wimmer and Franz, "Linear Scan Register Allocation on SSA Form" (CGO 2010). Their insight: SSA form gives you lifetime intervals for free. Each SSA definition starts a new interval. PHI nodes at loop headers create interval merges. The algorithm walks the IR in reverse, building intervals, then scans forward assigning registers. It runs in O(n) time --- no iterative graph coloring, no NP-hard interference graph.

For a trace JIT, the algorithm is even simpler than general SSA register allocation. A trace is a single basic block with one loop back-edge. There are no control flow merges except the loop header. The live ranges are trivially computed in a single backward pass.

## The Four Walls to LuaJIT

mandelbrot is not the only benchmark where we trail LuaJIT. Each benchmark reveals a different architectural gap:

### mandelbrot: 4.2x gap --- register allocation

As analyzed above. The frequency-based allocator wastes registers on equally-weighted slots instead of packing short-lived temporaries into shared registers. A live-range allocator would close most of this gap.

### fib: 3x gap --- type-specialized function calls

```
GScript fib(35):  0.072s    (method JIT)
LuaJIT  fib(35):  0.024s
```

fib is pure recursion, no loops. The method JIT compiles it, but every recursive call goes through the full GScript calling convention: box arguments into 32-byte Values, push a call frame, unbox at the callee, box the return value. LuaJIT's trace JIT records through recursive calls with type-specialized argument passing --- if it knows the argument is an integer, it passes the raw int64 without boxing.

Our method JIT does not specialize on argument types. Every call pays the boxing tax. For fib(35), that is 29 million function calls, each with 2-3 unnecessary boxing/unboxing round trips.

### Table operations: 7.5x gap --- Value representation

```
GScript nbody(100k):  2.884s    (trace JIT)
LuaJIT  nbody(100k):  0.385s
```

GScript's `Value` struct is 32 bytes:

```go
type Value struct {
    typ  ValueType  // 8 bytes (padded)
    data uint64     // 8 bytes
    ptr  any        // 16 bytes (Go interface = pointer + type)
}
```

LuaJIT's TValue is 8 bytes: a NaN-boxed double that encodes type information in the NaN payload bits. When nbody accesses `body.x`, GScript loads 32 bytes from the table's `svals` array. LuaJIT loads 8 bytes.

But the cost goes deeper than memory bandwidth. GScript's `ptr` field is a Go `interface{}` --- reading it requires loading two words (type pointer + data pointer), and writing it creates a reference that the Go garbage collector must trace. Every table write is a potential GC root update. LuaJIT's Lua tables have no GC write barrier for numeric values because NaN-boxed doubles are plain bitpatterns with no pointers.

The inline field cache we added in this cycle helps (nbody improved 8.8%), but it is optimizing the *lookup* path. The fundamental cost is the *value representation*. Changing Value from 32 bytes to 8 bytes would require NaN-boxing or a similar tagged-pointer scheme, and that means redesigning the entire runtime. It is the right thing to do eventually, but it is a multi-week project.

### Function calls: 9x gap --- inlining

```
GScript call_overhead:  0.090s   (method JIT)
LuaJIT  call_overhead:  0.010s
```

Small, frequently-called functions (like `advance()` in nbody or `A()` in spectral_norm) are dominated by call overhead: save registers, set up the call frame, dispatch to the callee, execute the body, tear down the frame, restore registers. LuaJIT's trace JIT *inlines* called functions: it records through the CALL instruction, copies the callee's body into the caller's trace, and eliminates the call entirely. The callee's code becomes part of the caller's loop body.

We have no function inlining. Every function call exits the trace, runs in the interpreter, and returns. For spectral_norm (which calls `A(i,j)` inside a double-nested loop), this means the trace covers only the loop counter logic --- the actual work happens in the interpreter. This is why spectral_norm is still 0.82x (a regression) with the trace JIT.

## What We Tried That Did Not Work

Not every optimization attempt succeeds. Honest accounting of the dead ends:

### 12 Float Registers

We extended the allocable float registers from 8 (D4-D11) to 12 (D4-D15). Hypothesis: more registers means fewer spills. Result: no measurable improvement on mandelbrot.

The frequency allocator assigns the extra registers to more equally-weighted slots. The slots that spill are the ones with the lowest frequency counts --- but in mandelbrot, the frequency distribution is flat, so the "extra" registers go to slots that are barely less important than the ones that already have registers. The spill traffic barely changes.

This confirmed that the problem is the allocation *strategy*, not the register *budget*.

### Type Tag Write Skipping

In the inner loop, every store to a non-allocated float slot writes both the 8-byte data (the float bits) and a 1-byte type tag (`TypeFloat`). Since the type never changes within the loop (the guard at the top ensures all slots start as floats), writing the type tag every iteration is redundant.

We tried skipping type tag writes during the loop, deferring them to the store-back at loop exit. The improvement was minimal --- roughly 2% --- because the float forwarder already eliminates most of the memory writes. The expressions that reach memory are the ones the forwarder could not eliminate (used more than once, or consumed across a non-adjacent instruction). For those, the type tag write is one instruction out of three (load + compute + store), so eliminating it saves 33% of the spill cost, but only for the small number of spilling operations that the forwarder missed. A rounding error.

### While-Loop Tracing

We attempted to add tracing for while-loops by detecting `OP_JMP` back-edges (jumps backward in the bytecode). The hot-counter and recording mechanisms worked, but the SSA builder could not handle the while-loop structure. For-loops have a clean `FORPREP`/`FORLOOP` pair that the SSA builder understands: the loop variable, limit, and step are in fixed registers. While-loops have arbitrary conditions and arbitrary state updates --- the SSA builder does not know which registers are loop-carried and which are dead.

The attempt was reverted after two days. Supporting while-loops requires a more general loop-detection strategy in the SSA builder, likely based on phi-node insertion at the loop header using dominance frontiers. The current for-loop-only design is a deliberate simplification that works for the benchmark suite, but it is a limitation we will need to address.

## CPU Profile: Where Time Goes at 0.236s

Here is the CPU profile breakdown for mandelbrot(1000) after all the changes in this cycle:

```
35%  VM.run          (interpreter for outer loops, down from 58%)
18%  ExternalCode    (JIT native code --- the inner loop)
12%  MulNums         (boxed float arithmetic in interpreter)
12%  executeCompiledTrace  (Go-level trace call overhead)
 8%  runtime.*       (Go GC, scheduling)
 5%  recording/guards (trace infrastructure)
10%  other
```

Sub-trace calling moved interpreter time from 58% to 35%. But the trace call overhead (`executeCompiledTrace`) appeared as a new 12% cost. This function is the Go-level wrapper that sets up the `TraceContext`, calls into the native code via a function pointer, and processes the exit code. It runs once per sub-trace call --- and there are 1,000,000 sub-trace calls per mandelbrot(1000) run (one per pixel).

The `MulNums` at 12% is the interpreter computing `2.0 * y / size` and `2.0 * x / size` in the outer loops. These are the per-row and per-column setup computations that happen outside the inner trace. They operate on boxed 32-byte Values, going through type-checking dispatch. If the outer loops ran in native code, these would be single `FMUL`/`FDIV` instructions.

The path forward is clear from the profile. Two things dominate:

1. **Register allocation** (the 18% native code is 3.3x larger than necessary)
2. **Call overhead** (the 12% `executeCompiledTrace` + 35% interpreter could be eliminated by better nested loop compilation)

But the register allocator is the prerequisite. Even if we perfectly nest the loops, generating 50 instructions per iteration instead of 18 means the native code runs 2.5x slower than it should. The register allocator is the foundation.

## The Path Forward

The next step is replacing the frequency-based register allocator with a live-range-based allocator. The plan:

**Phase 1: Liveness analysis.** Walk the SSA IR backward from loop exit to loop entry. For each SSA reference, record the instruction where it is defined (birth) and the last instruction where it is used (death). The output is a list of `[birth, death]` intervals.

**Phase 2: Linear scan.** Walk the intervals sorted by birth point. Maintain an "active" set of intervals currently assigned to registers. When a new interval starts:
- Expire any active intervals whose death point is before the current instruction.
- If a free register is available, assign it.
- If no register is available, spill the interval with the latest death point (it will be needed farthest in the future --- this is the Belady-optimal heuristic).

**Phase 3: Loop-carried values.** SSA PHI nodes at the loop header represent values that carry across iterations (like `zr`, `zi`, the loop counter). These intervals span the entire loop body. They get top priority in register assignment --- they should never spill.

For mandelbrot's inner loop, this should reduce the per-iteration instruction count from ~50 to ~20. At 50 million iterations, that is the difference between 0.236s and roughly 0.095s. Still 1.7x behind LuaJIT's 0.056s, but within striking distance --- and the remaining gap would be in call overhead and value representation, not code quality.

Wimmer and Franz showed that linear scan on SSA form produces code quality comparable to graph coloring at a fraction of the compile-time cost. For a trace JIT where compile time matters (we compile on every hot loop entry), this is the right tradeoff.

The 4.2x wall is real. We understand exactly why it exists. The frequency-based allocator was a good first step --- it got us from "everything spills" to "some things spill" --- but it has taken us as far as it can. The next level requires knowing not just *how often* a value is used, but *when* it is born and *when* it dies. That is what live-range analysis provides.

## References

- Christian Wimmer and Michael Franz, [Linear Scan Register Allocation on SSA Form](https://dl.acm.org/doi/10.1145/1772954.1772979), CGO 2010 --- the foundational paper for SSA-based linear scan
- Mike Pall, [LuaJIT 2.0 SSA IR](http://wiki.luajit.org/SSA-IR-2.0) --- register allocation in LuaJIT's trace compiler
- Poletto and Sarkar, [Linear Scan Register Allocation](https://dl.acm.org/doi/10.1145/330249.330250), TOPLAS 1999 --- the original linear scan algorithm (pre-SSA)
- ARM Architecture Reference Manual --- FMUL, FADD, FSUB instruction latencies on Apple Silicon
- Benoit Mandelbrot, *The Fractal Geometry of Nature*, 1982 --- the benchmark that keeps humbling us

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
