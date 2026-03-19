---
layout: default
title: "The Cold Code Revolution"
permalink: /09-the-cold-code-revolution
---

# The Cold Code Revolution

*March 2026 --- Beyond LuaJIT, Post #9*

## Where We Left Off

In [Post #8](08-what-the-academics-know), we paused the optimization grind and looked up. Five academic techniques surveyed. The conclusion was clear: NaN-boxing --- shrinking our 32-byte Value to 8 bytes --- was the only thing that could fundamentally close the table-ops gap. BOLT-style code layout was the best incremental win. Everything else was interesting reading.

The scoreboard at the end of Post #8:

| Benchmark | GScript | LuaJIT | Ratio | Status |
|-----------|---------|--------|-------|--------|
| fib(20) | 24us | 26us | 0.92x | **GScript wins** |
| ackermann(3,11) | 17us | ~17us | ~1.0x | Tied |
| callMany (fn calls) | 5.1us | 3us | 1.7x | LuaJIT leads |
| mandelbrot(1000) | 0.23s | 0.056s | 4.0x | LuaJIT leads |
| table ops (nbody) | 268us | 36us | 7.5x | LuaJIT leads |

Two wins. Three gaps. The plan was: try Value 32B to 16B first, implement BOLT-style layout, then evaluate whether full NaN-boxing was necessary.

What actually happened was a day of seven optimizations, three correctness fixes, one failed experiment, and one idea that turned out to be worth 2x on function calls. By the end of the day, fn_calls was 2.6us --- within 5% of LuaJIT's 2.5us. And the technique that got us there was not NaN-boxing, not trace inlining, not register allocation. It was moving cold code out of the way.

## Optimization 1: While-Loop Back-Edge Detection

The trace JIT has always been loop-centric. It looks for back-edges --- the jump at the bottom of a loop that goes back to the top --- and starts recording when a back-edge gets hot. But the back-edge detection only worked for `for` loops. GScript's `for` loops compile to a `FORPREP`/`FORLOOP` bytecode pair, and the JIT recognized `FORLOOP` as a back-edge.

`while` loops compile differently. A `while` loop is an `LT` or `LE` comparison followed by a conditional `JMP`. There is no explicit `FORLOOP` instruction. The trace JIT did not recognize the backward `JMP` at the bottom of a while loop as a back-edge. Result: while loops never got hot-counted, never got recorded, never got compiled.

The sieve benchmark is entirely while loops:

```go
func sieve(n) {
    var is_prime = make([]bool, n + 1)
    for i := 2; i <= n; i++ {
        is_prime[i] = true
    }
    var count = 0
    var i = 2
    while i <= n {
        if is_prime[i] {
            count++
            var j = i * i
            while j <= n {
                is_prime[j] = false
                j = j + i
            }
        }
        i++
    }
    return count
}
```

Two nested while loops. Neither was being compiled. The fix was straightforward: when the JIT sees a backward `JMP` instruction (target address less than current PC), treat it as a back-edge and increment the hotness counter, just like `FORLOOP`.

The result:

```
sieve(1M x3):  0.17s  -->  0.11s    (35% faster)
```

A 35% speedup from a five-line change. The sieve inner loop was running entirely in the interpreter, doing millions of array stores that could have been native ARM64.

This is the kind of optimization that should have been there from the start. But when you build a JIT incrementally --- first make `for` loops work, then worry about everything else --- gaps like this are inevitable. The while-loop gap had been hiding for weeks because the sieve benchmark was not part of the "critical path" we were focused on.

## Optimization 2: Native SETTABLE/SETFIELD

The trace JIT could already read from tables natively (GETTABLE, GETFIELD). But table writes --- SETTABLE and SETFIELD --- were still exiting to Go. Every `t[k] = v` in a compiled trace would: save all registers, call into the Go runtime, do the table write in Go, restore registers, resume native execution.

For matmul, which writes to a result matrix in its inner loop, this was devastating. The inner loop looked like:

```
... native computation ...
EXIT_TO_GO  (SETTABLE)     // <-- kills performance
... native computation ...
```

The implementation mirrored GETTABLE: emit ARM64 that computes the table slot address, checks the type, and stores the value directly. For array-style access (`t[i]`), the generated code:

1. Loads the table's array pointer
2. Computes the offset from the index
3. Stores the value directly to the slot
4. No Go runtime call, no register save/restore

For field access (`t.x`), the JIT uses the inline cache hit: if the table's shape matches the cached shape, the field offset is known at compile time, and the store is a single `STR` instruction.

```
matmul(300):  1.63s  -->  1.19s    (27% faster)
```

matmul is still far behind LuaJIT (0.031s), because the fundamental Value size problem remains. But eliminating the exit-to-Go for every table write removed a layer of overhead that was compounding on top of the Value size issue.

## Optimization 3: Fixing What Was Broken

Two benchmarks were producing wrong results under the trace JIT: sum_primes and sort. This is the kind of problem that Post #3's Rule #1 exists for: *never optimize wrong results*.

### sum_primes: Three Bugs

sum_primes was returning an incorrect total. The trace JIT was compiling the inner loop but producing wrong values. Three separate bugs, all in the same area --- the snapshot/side-exit machinery:

1. **Off-by-one in snapshot slot mapping.** When the trace exited back to the interpreter, it was restoring VM register values into the wrong slots. Slot N's value ended up in slot N+1. For most loops this was invisible because the loop only used one or two registers. sum_primes uses several, and the shifted values corrupted the computation.

2. **Missing guard on the modulo operator.** The `%` operation assumed integer operands but did not emit a type guard. When one operand was promoted to float by a previous operation, the modulo computed on the wrong representation.

3. **Incorrect loop counter restoration on side-exit.** When the trace exited mid-iteration, the loop counter was restored to its value at the *start* of the iteration rather than the current value. This caused the interpreter to re-execute part of the iteration, double-counting some primes.

### sort: Three More Bugs

sort was crashing. The trace JIT was recording through the quicksort partition loop, hitting a table access pattern it could not handle, and corrupting state.

1. **Unguarded table resize.** When a sort's partition step triggered a table resize (the table grew beyond its allocated array), the trace's cached array pointer became stale. The JIT continued writing to the old memory. Adding a guard on the table's array capacity --- exit if it changed --- fixed the crash.

2. **Comparison function callback not handled.** sort uses a comparison function (`func(a, b) { return a < b }`). The trace recorder tried to record through the callback but could not handle the CALL instruction inside the recorded trace. The fix: mark CALL instructions inside sort's inner loop as non-traceable, forcing the trace to exit before the callback and resume after.

3. **Incorrect SWAP implementation.** The native SETTABLE implementation (from optimization #2) had a subtle bug when two SETTABLE instructions wrote to the same table in the same trace with overlapping indices --- exactly what a swap does. The second write was reading the value that the first write had already overwritten. The fix: load both values before storing either.

Six bugs in total. Each was a 30-minute investigation. The pattern: trace JIT bugs are never where you expect them. You think the problem is in code generation, but it is in snapshot restoration. You think it is in the snapshot, but it is in the table write. The only reliable debugging method is Rule #2: dump state, observe, compare.

After the fixes:

```
sum_primes:  wrong result  -->  correct, 0.062s
sort:        crash         -->  correct, 0.327s
```

Neither benchmark got faster (both still run slower with the trace JIT than without --- the trace JIT hurts on complex control flow). But they are correct. Correctness is prerequisite.

## Optimization 4: Full Nesting Over Sub-Trace Calling

This one is about mandelbrot and any benchmark with nested loops. In [Post #7](07-the-day-we-beat-luajit), we described how sub-trace calling works: the outer trace compiles a BLR instruction that jumps to the inner trace's compiled code, executes the inner loop, and returns. The problem is the 61-instruction prologue/epilogue at each call boundary.

The idea: instead of calling the inner trace, inline its code directly into the outer trace. The inner loop's ARM64 instructions are copied into the outer trace's code buffer. No BLR, no prologue, no epilogue. The outer trace just falls through into the inner loop code.

The implementation is more subtle than "copy the bytes." The inner trace has its own register allocation, its own constants, its own side-exit stubs. All of these need to be remapped when the code is copied into the outer trace's context. Specifically:

- **Register mapping.** The inner trace may use registers that the outer trace has already allocated. A register renaming pass resolves conflicts.
- **Constant pool references.** The inner trace's `LDR` instructions reference its own constant pool. These offsets must be recomputed relative to the outer trace's constant pool (or the constants must be merged).
- **Side-exit targets.** The inner trace's guard-failure branches jump to its own exit stubs. These stubs must be duplicated into the outer trace, with snapshot data remapped to the outer trace's context.

For mandelbrot, which calls the inner trace once per pixel (1 million times for mandelbrot(1000)), the savings should be enormous: 61 million prologue instructions eliminated.

In practice, the current implementation handles the simple case --- inner traces with no sub-traces of their own and compatible register usage. The register remapping is conservative (it adds spill/reload pairs when there are conflicts rather than doing full register renaming). This means the actual savings are less than the theoretical 61 instructions per call, but still significant.

The result on mandelbrot was modest --- the benchmark is still dominated by the Value size overhead and the trace JIT's instruction count per iteration. But for benchmarks with shallow nesting and tight inner loops (like the sieve after while-loop detection), the inlining removes a measurable overhead.

## Optimization 5: The Big One --- BOLT-Style Cold Code Splitting

This is the optimization that changed the scoreboard.

In [Post #8](08-what-the-academics-know), we described BOLT's principle: hot code should be contiguous, cold code should be elsewhere. The specific application to GScript's method JIT: guard-failure handlers (side-exit stubs) sit in the middle of the hot path, polluting the instruction cache.

Here is what the method JIT's compiled code looked like before cold code splitting, for the `callMany` benchmark:

```
ENTRY:
    ... setup ...

LOOP:
    // Hot path: the actual computation
    guard_int  X20                  // check type of x
    branch_if_fail  -->  COLD_1     // <-- branch to cold handler
    add  X24, X24, #1              // x = add(x, 1)
    guard_int  X21                  // check type of i
    branch_if_fail  -->  COLD_2     // <-- branch to cold handler
    add  W20, W20, #1              // i++
    cmp  W20, #10000               // i < 10000?
    b.lt  LOOP                     // repeat

COLD_1:                            // <-- IN THE MIDDLE of hot code
    ... 15 instructions ...        // restore state, exit to interpreter
    ... spill registers ...
    ... call runtime ...
    b  EXIT

COLD_2:                            // <-- also in the middle
    ... 15 instructions ...
    ... spill registers ...
    ... call runtime ...
    b  EXIT

EPILOGUE:
    ... return ...

EXIT:
    ... interpreter reentry ...
```

The problem: COLD_1 and COLD_2 are in the instruction stream between the loop and the epilogue. They almost never execute (guards pass 99.99% of the time), but they occupy cache lines. On Apple M-series processors, a cache line is 64 bytes --- about 16 ARM64 instructions. COLD_1's 15 instructions take almost a full cache line. That is a cache line of cold code sitting between hot code, pushing the epilogue into the next cache line, causing an icache miss on every function return.

### The Fix: Two-Pass Emission

The method JIT's code emitter was restructured into two passes:

**Pass 1: Hot path only.** Emit the main function body --- loop entry, guards (conditional branches only, no handlers), computation, loop back-edge, epilogue. Guard branches target placeholder addresses.

**Pass 2: Cold stubs.** After the entire hot path is emitted, emit all guard-failure handlers. Patch the placeholder branch targets in pass 1 to point to the cold stubs.

The result:

```
ENTRY:
    ... setup ...

LOOP:
    guard_int  X20
    branch_if_fail  -->  COLD_1     // branch target is FAR AWAY
    add  X24, X24, #1
    guard_int  X21
    branch_if_fail  -->  COLD_2     // branch target is FAR AWAY
    add  W20, W20, #1
    cmp  W20, #10000
    b.lt  LOOP

EPILOGUE:                           // <-- immediately after loop!
    ... return ...

    // === Cold zone (separate cache lines) ===

COLD_1:
    ... 15 instructions ...
    b  EXIT

COLD_2:
    ... 15 instructions ...
    b  EXIT

EXIT:
    ... interpreter reentry ...
```

The hot path is now contiguous. The loop body, the epilogue, and the return sequence are packed together. The cold handlers are pushed to the end, where they occupy their own cache lines that the CPU never needs to fetch (because the guards almost always pass).

### Why This Was Worth 2x

The `callMany` benchmark runs 10,000 iterations of a tight loop. Each iteration is 4 instructions. At Apple M-series's ~6 IPC (instructions per cycle) for simple integer code, the loop should take about 6,600 cycles, or roughly 1.9us at 3.5 GHz.

Before cold code splitting, the benchmark ran in 5.1us. After: **2.6us.**

Where did the other 2.5us come from? Instruction cache behavior.

The method JIT's compiled code for `callMany` before splitting was about 400 bytes. The hot loop was 64 bytes (16 instructions). But the cold handlers --- 8 guards, each with a ~60-byte handler --- added 480 bytes of cold code *interspersed* with the hot code. The total code footprint was ~900 bytes, spanning 15 cache lines.

The Apple M4's L1 instruction cache is 192KB, 6-way set-associative, with 64-byte lines. 900 bytes across 15 cache lines sounds like it should fit easily. But the issue is not total cache capacity --- it is *fetch width* and *prefetch prediction*.

The M-series cores use a sophisticated instruction prefetcher that works best when code is sequential. When the hot path is interrupted by cold handlers, the prefetcher encounters branches that are never taken (the guard-failure branches), but it still fetches the target cache lines speculatively. Each speculative fetch of a cold handler cache line is a fetch that could have been used for the next iteration's instructions.

More importantly, the loop stride --- the distance from the start of one iteration to the start of the next --- was larger with cold code interspersed. A 64-byte loop fits in one cache line and the fetch unit can reuse it every iteration. A "64-byte loop with 480 bytes of cold code in the middle" strides across cache lines, and the instruction fetch unit sees a different address pattern.

After splitting, the hot loop is 64 bytes. It fits in a single cache line. The fetch unit loads it once, and every subsequent iteration is a cache hit. The cold handlers are in separate cache lines that are never loaded. The effective code footprint dropped from 900 bytes to 64 bytes --- a 14x reduction in icache pressure.

### The Numbers

```
fn_calls (10K iterations):
    Before:  5.1us
    After:   2.6us
    LuaJIT:  2.5us

    Improvement:  1.95x (almost exactly 2x)
    vs LuaJIT:    1.04x  (5% gap, was 1.7x gap)
```

From 70% behind LuaJIT to 5% behind. One optimization.

To put this in perspective: we spent weeks on function inlining (Post #7) to get fn_calls from 28us to 5.1us. That was a 5.4x improvement. Cold code splitting gave us another 1.95x on top of that, and the implementation was about 200 lines of code.

The insight from BOLT was correct. Code layout is not a minor detail. For tight loops that fit in a single cache line, the difference between "hot code is contiguous" and "hot code has cold code in the middle" is the difference between 1 cache miss and 15.

## Optimization 6: Abort-Blacklist for While Loops

Optimization 1 (while-loop back-edge detection) had a side effect. By recognizing backward JMPs as back-edges, the trace JIT started trying to record traces for *all* backward jumps --- including some that were not actually loop back-edges.

Consider a `switch`-style construct using if/else chains with backward jumps for fallthrough. The JIT would detect the backward jump, try to record a trace, fail (because the "loop" only executed once), and increment the abort counter. After 100 aborts, it would blacklist the instruction. But the abort-and-retry cycle wasted time on instructions that would never form a valid trace.

This caused a regression on some benchmarks. The sieve got faster (its while loops were now compiled), but other benchmarks got slower (the JIT was wasting time recording dead-end traces from backward JMPs that were not loops).

The fix: a smarter blacklist. When a backward JMP fails to produce a valid trace after 3 attempts, blacklist it immediately instead of waiting for 100 attempts. Real loop back-edges succeed on the first or second recording attempt. If a backward JMP fails 3 times, it is not a loop.

The threshold of 3 was chosen empirically. 1 is too aggressive (some loops genuinely fail on the first attempt due to cold code in the loop body). 5 adds unnecessary overhead. 3 catches all real loops in the benchmark suite while quickly blacklisting false back-edges.

## Optimization 7: Cache JIT Entry on FuncProto

The method JIT compiles functions and stores the compiled code in a map keyed by function prototype. Every time a function is called, the JIT checks this map: "has this function been compiled? If so, jump to the compiled code."

The map lookup --- `compiledFuncs[proto]` --- was a Go map access. Go maps are hash maps with good amortized O(1) performance, but each access involves: computing the hash of the key, looking up the bucket, comparing the key, dereferencing the value pointer. For a function that is called millions of times (like `fib` in the recursive benchmark), this lookup adds up.

The fix: store the compiled code pointer directly on the `FuncProto` struct itself. Instead of `compiledFuncs[proto]`, the JIT writes `proto.jitEntry = compiledCode` after compilation, and the dispatch checks `proto.jitEntry != nil`. A nil check on a struct field is a single `LDR` + `CBZ` --- two instructions instead of a map lookup.

```
FibIterative(30):
    Before:  237ns
    After:   200ns
    Improvement:  -16%
```

A 16% improvement on a 237-nanosecond benchmark from eliminating a map lookup. At this scale, Go's map overhead --- which is negligible for normal applications --- is a measurable fraction of the total runtime. The function call was 237ns. The map lookup was ~37ns of that. Removing it brought the benchmark to 200ns.

This optimization matters disproportionately for small, frequently-called functions. For a function that takes milliseconds, 37ns is noise. For a function that takes 200ns, 37ns was 15% of the runtime.

## The Value 16B Experiment: What We Tried and Why It Failed

Post #8 outlined a plan: shrink Value from 32 bytes to 16 bytes by replacing the `any` interface with `unsafe.Pointer` + type tag. This was supposed to be the first step toward NaN-boxing.

```go
// Current: 32 bytes
type Value struct {
    typ  uint8           //  1 byte  + 7 padding
    data uint64          //  8 bytes
    ptr  any             // 16 bytes (Go interface = type ptr + data ptr)
}

// Proposed: 16 bytes
type Value struct {
    typ  uint8           //  1 byte  + 7 padding
    data uint64          //  8 bytes
    // ptr replaced by storing raw unsafe.Pointer in data field
}
```

The idea: since `data` is already 8 bytes, and pointers on ARM64 are 8 bytes, we can store the pointer directly in `data` (cast via `unsafe.Pointer`) and use the `typ` field to know whether `data` holds an integer, a float, or a pointer. No need for the separate `any` interface field.

It does not work. Here is why.

### Go's GC Cannot See Pointers in uint64

Go's garbage collector scans memory to find pointers to live objects. It knows which fields of a struct are pointers by looking at the type metadata. A field of type `unsafe.Pointer` is scanned. A field of type `uint64` is not.

If we store a pointer to a Go object (a string, a table, a closure) in a `uint64` field, the GC does not know it is a pointer. The GC may collect the object because it sees no references to it. The program crashes with a use-after-free.

### The unsafe.Pointer Escape

The obvious fix: make `data` an `unsafe.Pointer` instead of `uint64`.

```go
type Value struct {
    typ  uint8
    data unsafe.Pointer    // GC can see this!
}
```

Now the GC scans `data` and finds the pointer. But there is a new problem: what happens when `data` holds an integer?

Go's `unsafe.Pointer` has strict rules ([documented in the unsafe package](https://pkg.go.dev/unsafe#Pointer)). The fundamental rule: an `unsafe.Pointer` must point to a valid Go object, or be nil. Storing an arbitrary integer (like the value `42`) in an `unsafe.Pointer` field violates this rule. The GC will try to follow the "pointer" `0x2A` to find a Go object, and will either crash or corrupt memory.

### The Bit-Tagging Approach

What if we never store raw integers in the pointer field? Instead, allocate all integers as Go objects (boxed integers), and store pointers to them:

```go
var fortytwo = new(int64)
*fortytwo = 42
v := Value{typ: TypeInt, data: unsafe.Pointer(fortytwo)}
```

This is GC-safe. But it defeats the purpose. Every integer operation now involves a heap allocation and a pointer dereference. We started with 32 bytes and no allocation overhead; now we have 16 bytes but allocation on every integer creation. The net performance impact is negative for integer-heavy code (which is most of our benchmarks).

### The keepalive Approach

Another attempt: keep the `uint64` data field, but maintain a separate "root set" of all Go pointers that are stored in Values. The root set is a Go slice of `interface{}` that the GC can scan. When we store a pointer in a Value, we also append it to the root set. When the GC runs, it sees the pointers in the root set and keeps the objects alive.

This works in theory. In practice, the root set management adds overhead that cancels the benefit of the smaller Value. Every table write, every variable assignment, every function call that passes a string or table argument must check whether the Value contains a pointer and, if so, update the root set. The write barrier is expensive, especially for the trace JIT, which would need to emit ARM64 code for root set management at every store site.

### The Conclusion

Shrinking Value from 32 bytes to 16 bytes is not viable in Go without either (a) violating GC invariants, (b) adding boxing overhead, or (c) adding write-barrier overhead that cancels the size reduction.

The path to 8 bytes (full NaN-boxing) has the same problem, compounded: NaN-boxed pointers in a `uint64` are completely invisible to Go's GC.

The only real solution is option 2 from Post #8: a custom arena allocator where all script objects live outside Go's heap, managed by a custom mark-sweep collector. This is what LuaJIT, V8, and SpiderMonkey do. It is also a multi-week rewrite of the entire runtime. We know the destination. The road is long.

For now, Value stays at 32 bytes. The JIT-level optimizations --- register pinning, cold code splitting, constant propagation --- are where the gains come from. The data representation change is "Season 2."

## The Scoreboard

Here is where things stand after all seven optimizations:

| Benchmark | Before | After | LuaJIT | vs LuaJIT |
|-----------|--------|-------|--------|-----------|
| **fib(20)** | 24us | 24us | 25us | **GScript wins** |
| **fn_calls (10K)** | **5.1us** | **2.6us** | **2.5us** | **5% gap (was 70%)** |
| ackermann(3,11) | 30us | 30us | 12us | 2.5x gap |
| sieve(1M x3) | 0.17s | 0.11s | 0.013s | 8.5x gap |
| mandelbrot(1000) | 0.23s | 0.23s | 0.06s | 4x gap |
| matmul(300) | 1.63s | 1.19s | 0.031s | 38x gap |

The headline: **fn_calls went from 70% behind LuaJIT to 5% behind.** One more small optimization and we have a second benchmark where GScript matches or beats LuaJIT.

The sieve improved 35% but is still 8.5x behind LuaJIT. This is the Value size story --- sieve is table-heavy (array reads and writes in the inner loop), and 32 bytes per Value vs 8 bytes per TValue is a 4x data overhead before we even count instructions.

matmul improved 27% from native SETTABLE but is still 38x behind. Same root cause: triple-nested loop with table access on every iteration, each access moving 32 bytes instead of 8.

## The Emerging Pattern

Look at the benchmarks where GScript is competitive:

- **fib**: pure recursion, no tables, no loops. Method JIT.
- **fn_calls**: tight loop, simple function calls, no tables. Method JIT.

Now look at where GScript is far behind:

- **sieve**: array-heavy. 8.5x gap.
- **matmul**: array-heavy. 38x gap.
- **mandelbrot**: compute-heavy but float-heavy. 4x gap.

The pattern is consistent with everything we have learned: GScript's method JIT produces excellent code for integer computation and function calls. The trace JIT handles simple loops well. But anything that touches tables or floats heavily runs into the Value 32B wall.

The cold code splitting optimization is instructive. It gave fn_calls a 2x improvement --- not by generating better arithmetic, but by making the instruction cache work better. The actual computation was already optimal (4 instructions per iteration, same as LuaJIT). The overhead was in the *layout* of the code. Once we fixed the layout, the computation ran at nearly LuaJIT speed.

This suggests that for the compute-heavy benchmarks (fib, fn_calls, ackermann), the remaining gap is not in code quality but in overhead: entry/exit costs, map lookups, cache effects. The hot loops are already good. The cold infrastructure around them is the bottleneck.

For the table-heavy benchmarks (sieve, matmul, nbody, spectral_norm), no amount of cold code splitting or register pinning will help. The bottleneck is moving 32 bytes per Value through the memory hierarchy. The fix is NaN-boxing, and NaN-boxing requires a custom allocator, and a custom allocator is a different project.

## What Comes Next

The immediate opportunities:

1. **Close the fn_calls gap.** We are 5% behind LuaJIT. The remaining gap is likely in the method JIT's function entry/exit sequence --- a few instructions of overhead per call. Shaving 2-3 instructions off the entry sequence should close it. This would give us a second benchmark where GScript beats LuaJIT.

2. **Apply cold code splitting to the trace JIT.** Today's optimization was in the method JIT only. The trace JIT has the same problem --- guard handlers interspersed with hot code. Applying the same two-pass emission to traced loops should help mandelbrot and sieve.

3. **Ackermann.** The 2.5x gap on ackermann is the next compute-heavy target. Ackermann is deeply recursive with small function bodies --- similar to fib but with more complex control flow. The method JIT's self-call optimization handles fib's two recursive calls well; ackermann's three-way dispatch (n==0, m==0, general case) may need additional work.

The longer-term picture has not changed. NaN-boxing is the architectural change that would transform the table-heavy benchmarks. But today proved that there is still significant ground to cover with code layout and overhead reduction --- the "free" optimizations that do not require touching the data representation.

Seven optimizations. Six bug fixes. One failed experiment. One benchmark within 5% of LuaJIT that was 70% behind yesterday.

Not a bad day.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
