---
layout: default
title: "The Last Thirty Percent"
permalink: /10-the-last-thirty-percent
---

# The Last Thirty Percent

*March 2026 --- Beyond LuaJIT, Post #10*

## Where We Left Off

In [Post #9](09-the-cold-code-revolution), we had a productive day of seven optimizations. BOLT-style cold code splitting moved fn_calls from 70% behind LuaJIT to 5% behind. While-loop back-edge detection unlocked the sieve. Native SETTABLE helped matmul. Six correctness bugs got fixed. The Value 16B experiment failed because Go's GC cannot see pointers hidden in `uint64` fields.

The scoreboard at the end of Post #9:

| Benchmark | GScript | LuaJIT | Gap |
|-----------|---------|--------|-----|
| fib(20) | 24us | 25us | **GScript wins** |
| fn calls (10K) | 2.6us | 2.5us | 5% gap |
| ackermann(3,4) | 30us | 12us | 2.5x |
| mandelbrot(1000) | 0.23s | 0.06s | 4.0x |
| sieve(1M x3) | 0.11s | 0.013s | 8.5x |

Two wins, one near-parity, two big gaps. The plan was to close the remaining compute-heavy gaps before confronting the Value-size wall.

What actually happened was a day of ten optimizations, each small on its own, but together amounting to a 33% improvement on mandelbrot and a major correctness milestone: binary_trees runs for the first time.

## The Mandelbrot Breakthrough

Mandelbrot was our flagship trace JIT benchmark. At 0.23s it was 4.0x behind LuaJIT's 0.06s. The inner loop was already compiled to native ARM64 --- the trace JIT was doing its job. But the generated code was leaving performance on the table in ways that were not obvious until we looked at the details.

### The Slot Reuse Problem

The trace recorder records bytecode operations as they execute. When the VM reuses a register slot for a different purpose --- say, slot 5 holds a loop counter, then later holds a float temporary --- the trace recorder sees two different values flowing through the same slot.

The problem was in the guard emission. When a trace starts, it emits type guards for all live slots: "check that slot 5 is an integer." But if slot 5 is later used as a float, the trace needs a second guard: "check that slot 5 is a float." The guard-failure handler for the first guard would restore the *original* integer value to the slot --- but by the time the second guard fires (which it should not, because the float guard passes), the slot already holds the correct float value. If the *first* guard fails, the handler was restoring the wrong type entirely.

The fix was subtle: when a slot is reused with a different type, the guard-failure snapshot must capture the slot's value at the *point of the guard*, not the value at trace entry. This is a classic "snapshot too early" bug. We were saving the entry state and replaying it on every guard failure, but the entry state was stale for reused slots.

Before the fix, mandelbrot's inner loop was hitting guard failures on every few iterations --- not crashing, but falling back to the interpreter, running a few bytecodes, then re-entering the trace. Each fallback cost microseconds of overhead, multiplied across a million pixels.

After the fix:

```
mandelbrot(1000):  0.214s  -->  0.142s    (33% faster)
```

Wait --- we said mandelbrot was 0.23s. The intermediate number 0.214s came from earlier optimizations in the same session (relaxed float guards and write-before-read improvements) that had already shaved 7%. The slot reuse fix was the big one: 0.214s to 0.142s.

### Relaxed Float Guards

The trace JIT emits type guards to ensure values have the expected type. For integer operations, these guards are essential --- if a value that should be an integer is actually a float, the integer arithmetic instruction will produce garbage.

But for float operations, the situation is different. ARM64's `FMUL`, `FADD`, `FSUB` instructions operate on float registers. If the source value is already a float, the guard is redundant. And if the source value is an integer, GScript's semantics automatically promote it to float --- so the "guard that this is a float" should not abort, it should just convert.

The optimization: for slots that are only used in float operations (no integer operations depend on them), skip the float type guard entirely. Instead, emit a "convert if integer" instruction that handles both cases. This eliminates a branch per float operation.

For mandelbrot, which does nothing but float arithmetic in its inner loop, every elimination of a guard branch saves a cycle per pixel. Across 1 million pixels, it adds up.

### Write-Before-Read Guard Skip

A more general version of the relaxed float guard: if a slot is *written* before it is *read* in the trace body, the entry guard for that slot is unnecessary. The entry guard checks "is slot N the expected type?" But if the first thing the trace does with slot N is overwrite it, the entry type does not matter.

The analysis: scan the trace's SSA IR for each slot. If the first reference to slot N is a STORE (write), not a LOAD (read), skip the entry guard for slot N. This is a simple pass over the IR --- look for the first occurrence of each slot and check if it's a read or write.

For mandelbrot, several temporary slots are write-before-read. Each eliminated guard saves one `CMP` + `B.NE` pair (two instructions, one of which is a branch that the branch predictor handles but that still consumes a slot in the fetch/decode pipeline).

## Type-Specialized Arrays

GScript arrays (created by `make([]int, n)` or `make([]float, n)`) store `Value` elements --- our 24-byte tagged union. When the trace JIT compiles an array access like `a[i]`, it must load 24 bytes, check the type tag, extract the payload, and operate on it.

LuaJIT has the same general problem, but its TValue is 8 bytes (NaN-boxed), so an array of 1000 elements is 8KB. GScript's array of 1000 elements is 24KB. The 3x memory footprint translates directly to 3x more cache misses.

The type-specialized array optimization: when an array is created with a type hint (`make([]int, n)` or `make([]float, n)`), allocate a compact backing store of unboxed `int64` or `float64` values. The `ArrayInt` type stores raw 8-byte integers; `ArrayFloat` stores raw 8-byte floats. No type tags, no padding.

The trace JIT recognizes these specialized arrays and emits direct loads/stores:

```
// Before (generic array): 3 instructions + branch
LDR   X1, [X0, X2, LSL #5]   // load 32-byte slot (shift by 5)
LDR   W3, [X0, X2, LSL #5, #0] // load type tag
CMP   W3, #TypeInt
B.NE  guard_fail
// extract payload...

// After (ArrayInt): 1 instruction
LDR   X1, [X0, X2, LSL #3]   // load 8-byte int (shift by 3)
```

One instruction instead of four. No type check needed --- the array is statically known to contain only integers.

The sieve benchmark was the primary beneficiary. Its inner loop does `is_prime[j] = false` and reads `is_prime[i]` --- all array operations on a boolean array (which maps to ArrayInt with 0/1 values). With type-specialized arrays, each array access is a single `LDR` or `STR` instead of a multi-instruction sequence.

### Native SETTABLE Append Mode

A related optimization for array writes. The sieve's inner loop pattern is:

```go
while j <= n {
    is_prime[j] = false
    j = j + i
}
```

The SETTABLE instruction for `is_prime[j] = false` was doing a bounds check, a capacity check, and then a store. For the sieve, the array is pre-allocated to size `n+1`, so the capacity check always passes. But the trace JIT was emitting it anyway because it could not prove at compile time that `j` would always be in bounds.

The fix: when the trace JIT can prove that the index comes from a loop counter that starts within bounds and increments by a known positive step, emit the bounds check as a loop-entry guard instead of a per-iteration check. If the array is large enough for the maximum possible index (which it is, since we guard on `j <= n` and the array has size `n+1`), the per-iteration check is redundant.

```
sieve(1M x3):  0.113s  -->  0.100s    (about 12% faster)
```

Combined with type-specialized arrays, the total sieve improvement from the Post #9 baseline was about 23%.

## Direct Arg Computation for Ackermann

Ackermann is a deeply recursive function:

```go
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m-1, 1) }
    return ack(m-1, ack(m, n-1))
}
```

The method JIT compiles this to native ARM64. Each recursive call has overhead: push arguments, call, pop result. The arguments are computed from the current `m` and `n`: `m-1`, `n-1`, `1`. These are trivial computations (subtract immediate, load constant).

The optimization: instead of computing the argument to a temporary register and then moving it to the argument register, compute directly into the argument register. `m-1` is `SUB X0, X19, #1` where X19 holds `m` and X0 is the first argument register. This saves one `MOV` per argument per call.

For ackermann(3,4), which makes 10,307,231 recursive calls, saving one instruction per call saves ~10 million instructions. At the M4's decode width of 10 instructions/cycle, that is roughly 1 million cycles --- about 0.3ms at 3.5 GHz.

```
ackermann warm:  30us  -->  18.9us    (37% faster, now 1.5x from LuaJIT)
```

The 37% improvement on warm ackermann came from several optimizations stacking: direct arg computation, guard skip for write-before-read slots, and cold code improvements from Post #9 that had not yet been measured on ackermann.

## DIV Fast Path and FMADD/FMSUB

Two smaller optimizations that affect multiple benchmarks:

**DIV fast path.** Integer division in the trace JIT was going through a general-purpose division routine that handled all edge cases (division by zero, overflow, float promotion). For the common case --- two positive integers, divisor nonzero --- the ARM64 `SDIV` instruction is sufficient. The fast path checks both operands are positive integers and emits a single `SDIV` + remainder check instead of a function call.

**FMADD/FMSUB pipeline integration.** ARM64 has fused multiply-add instructions (`FMADD Rd, Rn, Rm, Ra` computes `Ra + Rn * Rm` in one instruction). The trace JIT was emitting separate `FMUL` + `FADD` pairs. A peephole pass now detects the pattern and fuses them.

For mandelbrot, the inner loop has several multiply-add patterns (`x*x + y*y`, `x*x - y*y + cx`). Each FMADD saves one instruction and one cycle of latency. Across a million pixels with ~10 iterations each, the savings are measurable but modest --- roughly 2-3% on mandelbrot.

## Binary Trees: The Correctness Milestone

binary_trees was the only benchmark in the suite that did not run. It crashed with a stack overflow --- the deep recursion (depth 16, creating millions of tree nodes) exceeded Go's goroutine stack limit.

The fix was not in the JIT compiler. It was in the VM's call stack management. The VM was allocating a fixed-size call stack (1024 frames) and crashing when it overflowed. The fix: make the call stack growable, doubling its capacity when full, up to a limit of 1 million frames.

With the growable call stack, binary_trees runs correctly in all three modes:

```
binary_trees:  VM 1.255s | JIT 1.871s | Trace 1.871s | LuaJIT 0.17s
```

The 7.4x gap to LuaJIT is entirely the Value size problem. binary_trees creates millions of 3-element tables (node, left, right). Each table stores 3 Values = 72 bytes in GScript vs 24 bytes in LuaJIT. The tree has ~131,000 leaf nodes and ~65,000 internal nodes --- roughly 200,000 allocations. At 72 vs 24 bytes per node, GScript allocates 14MB of node data vs LuaJIT's 5MB. The 3x memory overhead means 3x more cache pressure, plus GC pressure from 3x more pointer-sized objects to trace.

Note that the JIT and trace modes are *slower* than the VM interpreter on this benchmark. The method JIT's overhead (compilation time, guard checks) exceeds its benefit for code that is dominated by memory allocation. The trace JIT's overhead is even worse --- it tries to record the recursive tree construction, fails, and adds recording overhead on top of the allocation cost.

## Triple Nesting Sub-Trace Calling

A structural improvement that does not show up in any single benchmark but enables future optimizations: sub-trace calling now works for triply-nested loops. Previously, the trace JIT could compile a loop that contained one inner loop (calling the inner trace via BLR). Now it can compile a loop that contains a loop that contains a loop.

The implementation extends the sub-trace metadata to track not just "this trace calls that trace" but a full call tree. When the outer trace is compiled, it emits BLR instructions for each inner trace, and the inner traces emit their own BLR instructions for their inner traces.

For benchmarks like matmul (triple-nested: i, j, k loops) and spectral_norm (nested within an iterative convergence loop), this is a prerequisite for future trace inlining. Today it simply ensures the trace JIT does not abort on triply-nested structures.

## The Scoreboard

After all ten optimizations:

### vs LuaJIT (warm micro-benchmarks)

| Benchmark | GScript JIT | LuaJIT | Result |
|-----------|-------------|--------|--------|
| **fib(20)** | **19.4us** | 24.7us | **GScript 21% faster** |
| **fn calls (10K)** | **2.66us** | 2.6us | **parity** |
| **ackermann(3,4)** | 18.9us | 12.0us | 1.6x gap |
| mandelbrot(1000) | 0.155s | 0.058s | 2.7x gap |

### Full suite (15 benchmarks)

| Benchmark | Best | LuaJIT | Gap |
|-----------|------|--------|-----|
| fib(35) | 0.026s | 0.025s | ~parity |
| sieve(1M x3) | 0.080s | 0.011s | 7.3x |
| mandelbrot(1000) | 0.155s | 0.058s | 2.7x |
| ackermann(3,4 x500) | 0.009s | 0.006s | 1.5x |
| matmul(300) | 1.120s | 0.022s | 51x |
| spectral_norm(500) | 0.660s | 0.008s | 83x |
| nbody(500K) | 2.376s | 0.037s | 64x |
| fannkuch(9) | 0.588s | 0.019s | 31x |
| sort(50K x3) | 0.158s | 0.012s | 13x |
| sum_primes(100K) | 0.022s | 0.002s | 11x |
| mutual_recursion | 0.103s | 0.005s | 21x |
| method_dispatch | 0.080s | 0.001s | 80x |
| closure_bench | 0.046s | 0.009s | 5.1x |
| string_bench | 0.046s | 0.010s | 4.6x |
| binary_trees | 1.255s | 0.17s | 7.4x |

### Trace JIT speedups (vs VM interpreter)

| Benchmark | VM | Trace | Speedup |
|-----------|-----|-------|---------|
| fib(35) | 0.882s | 0.028s | **x31.5** |
| ackermann(3,4 x500) | 0.153s | 0.010s | **x15.3** |
| mandelbrot(1000) | 1.397s | 0.155s | **x9.0** |
| sieve(1M x3) | 0.308s | 0.100s | **x3.1** |
| spectral_norm(500) | 0.753s | 0.678s | x1.1 |

The rest of the suite benchmarks show trace JIT at parity or slower than the VM --- these are the table-heavy and control-flow-heavy benchmarks where the trace JIT's overhead exceeds its benefit.

## The High-Leverage Lesson

Looking back at the ten optimizations in this post, the most impactful ones were not micro-optimizations. They were architectural fixes:

1. **Slot reuse guard-fail fix** (mandelbrot 33% faster) --- a correctness fix that happened to be the biggest performance win. The trace was falling back to the interpreter on guard failures that should not have happened.

2. **Type-specialized arrays** (sieve 23% faster across sessions) --- a data representation change that eliminated per-element type checks. Not a code generation improvement, but a *data layout* improvement.

3. **Direct arg computation** (ackermann 37% faster) --- eliminating redundant register moves. Simple, but high-leverage because ackermann makes millions of calls.

The DIV fast path and FMADD/FMSUB fusion were satisfying to implement but contributed only 2-3% each. The lesson: when you have architectural bugs (incorrect snapshots) and architectural overhead (24-byte array elements), micro-optimizations in the code generator are rounding errors.

This is the same lesson from Post #5 (the blacklist that changed everything) and Post #9 (cold code splitting). The biggest speedups come from eliminating *work that should not be happening*, not from making the remaining work faster.

## The Value 24B Wall

The scoreboard tells a clear story. There are two categories of benchmarks:

**Compute-heavy (competitive):** fib, fn_calls, ackermann, mandelbrot. These benchmarks do arithmetic in registers, with minimal table access. GScript is within 1-3x of LuaJIT on all of them.

**Table-heavy (10-80x behind):** matmul, nbody, spectral_norm, method_dispatch, sort, sieve, binary_trees. These benchmarks create tables, read fields, write elements. Every table operation moves 24 bytes per Value instead of 8. The 3x memory overhead cascades: 3x more cache misses, 3x more memory bandwidth, 3x more GC pressure.

No amount of register allocation, guard elimination, or code layout optimization will close a 3x memory overhead. The fix is NaN-boxing: shrinking Value from 24 bytes to 8 bytes. As we detailed in Post #9, this requires a custom arena allocator because Go's GC cannot track pointers hidden in NaN-boxed integers. It is a multi-week rewrite of the entire runtime.

But the compute-heavy benchmarks prove the JIT architecture works. The code we generate for integer arithmetic, float arithmetic, function calls, and recursion is within striking distance of LuaJIT. The JIT is not the bottleneck --- the data representation is.

## What Comes Next

The immediate priorities:

1. **Mandelbrot: trace inlining.** At 2.7x behind LuaJIT, mandelbrot is the closest table-free benchmark that still has a significant gap. The trace JIT compiles the inner loop but still has sub-trace call overhead. Inlining the inner trace into the outer trace (copying the ARM64 directly) should eliminate the prologue/epilogue overhead and close the gap toward 2x.

2. **Ackermann: deeper inlining.** At 1.5x behind, ackermann is close. The method JIT handles the recursive calls but has per-call overhead. Inlining the base cases (`m==0` returns `n+1`) directly at the call site would eliminate one level of call/return overhead.

3. **The table-heavy benchmarks: start NaN-boxing design.** Not implementation yet --- but the design phase. How do you build a custom allocator in Go? What are the GC root scanning strategies? How does the write barrier work? The research for Post #11.

The compute-heavy benchmarks are approaching LuaJIT. The table-heavy benchmarks need a different kind of change. The JIT is done accelerating what it can accelerate with the current data representation. The next revolution is in the runtime.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
