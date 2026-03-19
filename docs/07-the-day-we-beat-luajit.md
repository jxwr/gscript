---
layout: default
title: "The Day We Beat LuaJIT"
permalink: /07-the-day-we-beat-luajit
---

# The Day We Beat LuaJIT

*March 2026 --- Beyond LuaJIT, Post #7*

## Where We Left Off

In [Post #6](06-the-four-point-two-x-wall), we hit a wall. mandelbrot was 4.2x behind LuaJIT, fib was 3x behind, function calls were 9x behind. We had traced the mandelbrot gap to the register allocator --- a frequency-based scheme that could not handle flat distributions --- and outlined a plan for live-range-based linear scan. The fib gap was in boxing overhead: every recursive call packed arguments into 32-byte Values and unpacked them on the other side. The function call gap was worse: each call to a trivial function like `add(x, 1)` went through the full call-exit-reenter cycle.

The post ended with a technical roadmap. Register allocation for mandelbrot, type specialization for fib, inlining for function calls.

What actually happened was more interesting. We did not touch the register allocator. We did not implement general type specialization. Instead, we chased the function call benchmark, found a trick that was worth 5.4x, and watched it accidentally solve half of the fib problem too. Then we pushed fib the rest of the way with three targeted optimizations. At the end of the day, the terminal printed a number we had been chasing for weeks:

```
fib(20) warm:  24us
```

LuaJIT runs fib(20) in 26us.

## The Function Inlining Story

The `callMany` benchmark is simple:

```go
func add(a, b) {
    return a + b
}

func callMany() {
    var x = 0
    for i := 0; i < 10000; i++ {
        x = add(x, 1)
    }
    return x
}
```

Before this work, `callMany` ran in 28us. LuaJIT does it in 3us. A 9x gap.

The cost is not in the addition. The cost is in what happens around the addition. Each iteration calls `add(x, 1)`, which means:

1. **Exit native code** --- the method JIT's compiled loop hits a CALL instruction, does not know how to inline `add`, side-exits to the Go runtime.
2. **Go dispatches the call** --- push a call frame, copy arguments into Value slots, look up the function, enter the callee.
3. **Execute `add`** --- load `a` and `b` from Value slots, add them, box the result into a Value.
4. **Return** --- pop the frame, resume the compiled loop, unbox the return value.

That is dozens of instructions of overhead wrapping a single ADD. The architecture at the time could not avoid it: the method JIT compiled functions independently, and calls between them always went through the interpreter's call machinery.

The fix had three parts.

### Part 1: Accumulator Pinning

The first insight was about the loop accumulator. In `callMany`, `x` is the accumulator --- it is read at the start of each iteration (`add(x, 1)`), modified by the call's return value (`x = add(...)`), and read again at the next iteration. The variable `x` lives in a VM register slot. Before this optimization, every use of `x` loaded it from the Value array in memory, and every write stored it back.

Accumulator pinning assigns `x` to a physical ARM64 register --- X24 --- for the entire lifetime of the compiled loop. The value stays in X24 across iterations. No loads, no stores, no round-trips through memory.

But pinning the accumulator only helps if the call to `add` can *read from* X24 and *write to* X24 directly. If the call still goes through the full call-exit machinery, the pinned register gets spilled before the call and reloaded after. No net benefit.

### Part 2: Argument Source Tracing

The method JIT already knows, at compile time, which VM registers hold the arguments to each CALL instruction. For `add(x, 1)`, it knows argument 0 is `x` (which is now pinned to X24) and argument 1 is the constant `1`.

Argument source tracing extends this: instead of emitting code that copies `x` into the call frame's argument slot, the JIT recognizes that `x` is already in X24 and passes it directly. The constant `1` is handled as an immediate --- no memory load, no register copy.

### Part 3: Result Destination Tracing

The same logic applies in reverse for the return value. The JIT knows that the result of `add(x, 1)` gets assigned back to `x`. Since `x` is pinned to X24, the JIT emits code that puts the return value directly into X24 instead of routing it through a temporary Value slot.

With all three pieces in place, the entire call to `add(x, 1)` collapses. The JIT sees:

- The callee body is `return a + b`.
- Argument `a` is in X24.
- Argument `b` is the constant 1.
- The result goes back to X24.

The generated ARM64 for one iteration of the inner loop:

```arm64
ADD   X24, X24, #1       // x = x + 1
ADD   W20, W20, #1       // i++
CMP   W20, #10000        // i < 10000?
B.LT  loop               // repeat
```

One instruction for the function call. One instruction for the loop counter. One comparison. One branch. The entire `add(x, 1)` call --- the function lookup, the frame push, the argument copy, the body execution, the return value copy, the frame pop --- is gone. Replaced by `ADD X24, X24, #1`.

The result: **28us to 5.1us. A 5.4x improvement.** The LuaJIT gap on function calls went from 9x to 1.7x in one optimization.

## The Serendipity

Here is where the story takes an unexpected turn.

Before starting the function inlining work, fib(20) ran in 35us. LuaJIT does it in 26us --- a 1.34x gap. fib was not the target of the function inlining optimization. The `callMany` benchmark was.

But fib is a self-recursive function:

```go
func fib(n) {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}
```

Each recursive call goes through the same call machinery that `callMany` was suffering from. And the accumulator pinning optimization did not just apply to simple loop accumulators --- it applied to any register that the JIT could prove was read and written in a predictable pattern.

The self-call path in the method JIT already recognized `fib` calling `fib`. It compiled the recursive calls as direct jumps rather than full interpreter-mediated calls. But before accumulator pinning, the intermediate results still bounced through memory between calls. The pinning framework improved the general register management for self-call functions, reducing unnecessary spills at call boundaries.

After the function inlining changes landed --- before any fib-specific optimization --- I ran the benchmark out of curiosity:

```
fib(20) warm:  28us    (was 35us)
```

A 20% improvement on a benchmark we were not even targeting. The gap to LuaJIT shrank from 1.34x to 1.07x. Almost there. Just by cleaning up register management in the self-call path as a side effect of function inlining.

This is one of the things that makes compiler optimization work unpredictable. You pull on one thread and the whole fabric shifts. The accumulator pinning framework was designed for `add(x, 1)` in a loop. It turned out to be exactly what `fib(n - 1) + fib(n - 2)` needed too.

## The Final Push

1.07x behind. Two microseconds. The gap was small enough to see the finish line but large enough that noise could not explain it away. fib(20) at 28us, LuaJIT at 26us. Three more optimizations closed it.

### Optimization 1: Pin R(0) to X19

In GScript's calling convention, R(0) holds the first parameter of the current function. For `fib(n)`, R(0) is `n`. Every entry to the function loads `n` from the Value array in memory:

```arm64
// Before: load n from memory at function entry
LDR   X_tmp, [X26, #R0_offset]    // load n from Value array
```

X19 is a callee-saved register on ARM64 --- it survives across function calls without needing explicit save/restore. By pinning R(0) to X19, the parameter `n` lives in a physical register from the moment the function is entered. No memory load at function entry. No memory store at call boundaries.

For fib(20), which makes 13,529 function entries, eliminating the memory load at each entry saves roughly 13,000 load instructions. At Apple Silicon's ~4-cycle load latency, that is ~54,000 cycles --- about 1.5us at 3.5 GHz.

### Optimization 2: Skip Spills at Nested Returns

When a compiled function returns, the method JIT used to spill all pinned registers back to the Value array. This ensures the caller can find the return value in the expected memory location. But for self-recursive functions, the "caller" is the same function --- and it knows the registers are pinned.

Consider `fib(n - 1)` returning to the `fib` that called it. The return value goes into a register. Then `fib(n - 2)` is called. The method JIT was spilling all pinned registers before this second call, even though the only register the second call needs is the parameter `n - 2` --- which is a freshly computed value, not a pinned register.

The optimization: skip `spillPinnedRegs` at return sites within self-recursive calls. The caller already knows where the pinned registers are (they are in the same physical registers, because it is the same function). No spill, no reload.

### Optimization 3: LOADINT Constant Propagation

fib's base case is `if n < 2 { return n }`. The bytecode for this is:

```
LOADINT  R(3), 2       // load constant 2 into R(3)
LT       R(0), R(3)    // compare n < 2
```

Before this optimization, the JIT compiled `LOADINT R(3), 2` as a memory store (write the integer 2 to the Value array at slot 3) and then the `LT` instruction loaded it back from memory. Two memory operations for a constant.

The fix: when the JIT sees `LT R(0), R(3)` and R(3) was defined by a `LOADINT` with a known constant, it emits an immediate comparison:

```arm64
// After: constant propagation
CMP   X19, #2         // n < 2?  (X19 = pinned R(0), 2 = immediate)
B.GE  recurse         // if n >= 2, do the recursive calls
```

One instruction instead of three (store + load + compare). And the `LOADINT R(3), 2` instruction becomes dead code --- nothing else reads R(3), so the JIT eliminates the store entirely. Dead store elimination, for free.

### The Combined Effect

Each optimization is small. Pin a register here, skip a spill there, propagate a constant. But in a function that executes 13,529 times in 24 microseconds, every instruction matters. The three optimizations together:

```
fib(20) timeline:
  35us  → start of day (1.34x behind LuaJIT)
  28us  → after fn-inline accumulator pinning (1.07x behind)
  24us  → after R(0) pinning + const propagation + dead store elimination
```

24 microseconds.

## The Moment

The benchmark script runs each test 100 times and reports the median. I had been staring at numbers in the 28-35us range for days. The first run after the three optimizations:

```
=== Method JIT Benchmarks (warm, after compilation) ===
fib(20):       24us
ackermann:     17us
callMany:      5.1us
```

I opened another terminal and ran LuaJIT:

```
$ luajit -e "
local function fib(n)
    if n < 2 then return n end
    return fib(n-1) + fib(n-2)
end
-- warmup + measure
for i=1,100 do fib(20) end
local t = os.clock()
for i=1,100 do fib(20) end
print(string.format('fib(20): %.0fus', (os.clock()-t)/100*1e6))
"
fib(20): 26us
```

24us vs 26us. GScript is 9% faster than LuaJIT on fib(20).

Not on a synthetic microbenchmark we designed to win. Not with a warm cache advantage. The same algorithm, the same recursion depth, the same computation. GScript's method JIT, running on a language implemented in Go, compiling to ARM64 on the fly, producing code that outperforms Mike Pall's hand-tuned trace compiler on the canonical recursive benchmark.

It is one benchmark. It is the benchmark that plays to our method JIT's strengths (pure recursion, no loops, no table ops). But it is real.

## The Scoreboard

Here is where GScript stands against LuaJIT across the benchmark suite:

| Benchmark | GScript | LuaJIT | Ratio | Status |
|-----------|---------|--------|-------|--------|
| fib(20) | 24us | 26us | 0.92x | **GScript wins by 9%** |
| ackermann(3,11) | 17us | ~17us | ~1.0x | **Tied** |
| callMany (fn calls) | 5.1us | 3us | 1.7x | LuaJIT leads |
| mandelbrot(1000) | 0.23s | 0.056s | 4.0x | LuaJIT leads |
| table ops (nbody) | 268us | 36us | 7.5x | LuaJIT leads |

Two benchmarks won. Three to go.

The pattern in the results reveals the architecture. GScript wins on **pure computation with recursion** --- fib and ackermann are function-call-heavy, integer-only, no table access. The method JIT handles these well because it compiles functions as units, recognizes self-recursion, and (now) pins registers across calls.

GScript loses on **loop-heavy computation** (mandelbrot) and **table-heavy computation** (nbody). mandelbrot's gap is in the trace JIT's register allocator and sub-trace call overhead, as analyzed in Post #6. nbody's gap is in the 32-byte Value representation --- every table read moves 4x more data than LuaJIT's 8-byte NaN-boxed TValues.

## What This Means

It means the method JIT works. For compute-heavy, self-recursive functions, GScript's code quality is now at or above LuaJIT's level. The combination of accumulator pinning, R(0) register pinning, constant propagation, and dead store elimination produces tight ARM64 that wastes very few instructions.

But it also means the easy wins are behind us.

fib and ackermann are the benchmarks most amenable to method-JIT optimization --- they are pure recursion, they fit in registers, they have no polymorphism. The remaining benchmarks expose deeper architectural gaps:

**Function calls (1.7x gap)**: `callMany` is 5.1us vs LuaJIT's 3us. The remaining gap comes from call patterns that the JIT cannot fully inline --- when the callee is not a simple `return a + b`, when arguments are not directly traceable to pinned registers, when the return value flows through a complex expression. Closing this gap requires more general inlining: recording through CALL instructions and emitting the callee's body inline, the way LuaJIT's trace JIT does.

**mandelbrot (4.0x gap)**: The trace JIT's inner loop is 26 instructions per iteration vs a theoretical minimum of ~15. The register allocator is better than it was (SSA-ref-level linear scan replaced the frequency-based allocator from Post #6), but the 1 million sub-trace calls per mandelbrot(1000) each carry a 61-instruction prologue/epilogue. The fix is code inlining --- copying the inner trace's machine code into the outer trace, eliminating the call boundary entirely.

**Table operations (7.5x gap)**: This is the representation problem. GScript's 32-byte Values vs LuaJIT's 8-byte NaN-boxed TValues. Every table read, every table write, every array access moves 4x more memory. The GC sees 4x more pointers. The caches hold 4x fewer values. NaN-boxing is the fix, and it touches every file in the runtime. It is a multi-week redesign, not an optimization pass.

## What Changed Today

The fib(20) timeline tells the story of how JIT optimization actually works. Not a single breakthrough, but a cascade:

```
53us  →  type seeding + tag elimination + compact frames
35us  →  (improvements from previous cycle)
28us  →  fn-inline accumulator pinning (targeting callMany, not fib)
24us  →  R(0) pinning + const propagation + dead store elimination
```

The biggest single drop --- 35us to 28us --- came from an optimization aimed at a different benchmark. The targeted fib optimizations (R(0) pinning, const propagation) contributed the final 4us. Both were necessary. Neither alone would have crossed the line.

The lesson is that JIT compiler optimizations are not independent. Improving the general call machinery (for `callMany`) rippled into recursive calls (for `fib`). Pinning an accumulator register (for loop accumulators) turned out to pin self-call intermediate values too. The boundaries between "function inlining" and "recursive call optimization" and "register allocation" blur when the same physical registers carry values across all three domains.

This is what Mike Pall understood when he designed LuaJIT as a unified trace compiler rather than a collection of independent optimization passes. Each optimization sees the full picture. We are arriving at the same insight from the opposite direction --- building independent optimizations that keep accidentally helping each other.

## The Road Ahead

The scoreboard has two green cells and three red ones. The next targets, in order of expected impact:

1. **Function call gap (1.7x)** --- extend the inlining framework to handle more complex callees. The machinery is in place from this cycle; it needs to handle multi-statement function bodies and non-trivial argument patterns.

2. **mandelbrot gap (4.0x)** --- code inlining for sub-traces (Approach C from the architecture discussion). Eliminate the 61-instruction prologue per sub-trace call by copying the inner trace's ARM64 into the outer trace's code buffer.

3. **Table operations gap (7.5x)** --- NaN-boxing. Redesign Value from 32 bytes to 8 bytes. This is the "Season 2" project: it unlocks everything but requires rebuilding the runtime.

We beat LuaJIT on one benchmark today. The goal is to beat it on all of them.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
