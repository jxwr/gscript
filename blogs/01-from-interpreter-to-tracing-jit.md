# From Interpreter to Tracing JIT: What We Learned the Hard Way

*March 2026 — Beyond LuaJIT, Post #1*

## The Goal

GScript is a scripting language with Go syntax and Lua semantics, implemented in Go. We wanted to make it fast — specifically, we wanted to approach LuaJIT-level performance on a Xiangqi (Chinese Chess) AI benchmark that does alpha-beta search with Zobrist hashing and transposition tables.

The benchmark: `chess_bench_parallel.gs`, a negamax search with iterative deepening, running single-threaded and parallel (6 workers) with a 10-second time budget.

Baseline: depth 6 single-threaded, depth 4 parallel. ~23K nodes/second.

Target: LuaJIT territory — 500K+ nodes/second.

## What We Built

Over 22 commits, we built four layers of optimization:

### Layer 1: Interpreter Optimization (2.4x)

The biggest gains came from the simplest changes:

**Inline call/return** eliminated Go stack growth per GScript function call. Previously, every `OP_CALL` went through `callValue()` → `call()` → `run()` — three Go function calls creating new stack frames. We inlined this: `OP_CALL` pushes a frame and updates cached locals directly in the `run()` loop. `OP_RETURN` pops and restores. This alone gave ~30% speedup on the chess benchmark.

**Global variable indexing** replaced `map[string]Value` with `[]Value` for globals. Every function call in GScript starts with `OP_GETGLOBAL` to fetch the function. With a string-keyed map, that's ~50ns per lookup (hash + compare). With an indexed array + lazy `GlobalCache` per FuncProto, it's ~2ns. The cache resolves the string name to an array index on first access; subsequent accesses are O(1).

**Compact Value (56→32 bytes)** merged `ival int64` and `fval float64` into a single `data uint64` field (floats stored via `math.Float64bits`). Every register copy, table lookup, and function argument is a Value copy — halving the size from 56 to 32 bytes reduced memory bandwidth across the board.

**Typed table maps** split the generic `map[Value]Value` hash table into specialized maps: `imap map[int64]Value` for integer keys, flat `skeys/svals` slices for ≤12 string keys. The chess benchmark does millions of `piece.type`, `piece.col` lookups — linear scan of 5-element slices is faster than Go's map hash function on 56-byte keys.

### Layer 2: Method JIT (No improvement)

We built a per-function ARM64 JIT compiler (codegen.go, 2467 lines) that natively compiles FORLOOP, ADD/SUB/MUL, GETFIELD, GETTABLE, comparisons, and TEST. Functions with loops are compiled after 10 calls.

**Result: JIT was slower than the interpreter.** The chess AI's hot functions (negamax, isSquareAttacked) have GETGLOBAL, CALL, and table operations in every iteration. The JIT compiles the arithmetic natively but exits to Go for everything else via "call-exit" — store state, return to Go, handle the instruction, re-enter JIT. The exit/re-enter overhead exceeded the native code benefit.

Lesson: **A method JIT that can't compile the full hot path is worse than a good interpreter.**

### Layer 3: Tracing JIT (Marginal improvement)

We built a trace-based JIT (4 phases, ~2000 lines):

- **Phase A**: Trace recorder hooks into the interpreter loop, records hot loop iterations
- **Phase B**: ARM64 code emitter compiles traces to native code
- **Phase C**: Native GETFIELD/GETTABLE/SETTABLE/SETFIELD, string EQ, comparisons
- **Phase D**: Nested loop handling, side-exit recovery

Plus: self-recursive CALL support (native `BL` with depth counter), GoFunction intrinsics (`bit32.bxor` → ARM64 `EOR`), register allocation (top 5 VM registers → X20-X24).

**Result: The tracing JIT barely helped the chess benchmark.** Why? The trace records one loop iteration, but negamax's inner loop contains `OP_GETGLOBAL` (to call `zobristPiece`, `isInCheck`, etc.) which causes immediate side-exit. The trace covers ~5 instructions before bailing.

Lesson: **A tracing JIT that can't cover the hot path is just overhead.**

### Layer 4: Parallel Optimization (2.25x throughput)

Lock-free child VMs with globals snapshot gave the biggest parallel improvement. Each goroutine spawned by `OP_GO` gets a copy of the global array — zero mutex contention. Combined with sparse array expansion (board keys 101-910 use array instead of imap), parallel throughput went from 892K to 2.0M nodes.

## What Didn't Work

**NaN-boxing (32→16 bytes)**: We implemented a full NaN-boxed Value with `{bits uint64, ptr unsafe.Pointer}`. Every integer operation needed 47-bit sign-extension; every type check needed 64-bit AND + CMP instead of a 1-byte compare. The ALU overhead exceeded the memory bandwidth savings. **In an interpreter where Values live in memory (not CPU registers), a wide tagged union with a dedicated type byte is faster than a compact NaN-boxed encoding.** NaN-boxing wins when Values flow through CPU registers — which requires a JIT that keeps values unboxed.

**Table object pool**: `sync.Pool` for Table reuse caused 2x regression. The pool's atomic operations + stale reference leaks (old Values in recycled slice capacity preventing GC collection) outweighed allocation savings.

## Final Numbers

| Metric | Baseline | Optimized | Improvement |
|--------|----------|-----------|-------------|
| Single-thread d=5 | 6.8s | 3.7s | **×1.84** |
| Single-thread depth (10s) | 6 | 7 | +1 level |
| Parallel throughput | 892K | 2.0M | **×2.25** |

Current: ~46K nodes/s. LuaJIT estimate: ~500K-2M nodes/s. **Gap: 10-40x.**

## Why We're Still 10x Away

CPU profile of the chess benchmark:

```
VM dispatch loop (switch)     35.9%  ← Can only be eliminated by JIT
GC (scan + alloc)             23.2%  ← Go runtime limitation
Table operations              10.9%  ← Already optimized
OS/threading                  15.3%  ← Go runtime
Instruction decode             3.6%  ← Eliminated by JIT
```

The 35.9% dispatch overhead is the single biggest target. But our JIT can't touch it because **the trace doesn't cover the hot path**.

The trace records: `GETFIELD → GETTABLE → ADD → GETGLOBAL(side-exit!)`. The remaining 80% of the loop runs in the interpreter.

## The Missing Piece: SSA IR

Every successful JIT compiler uses SSA (Static Single Assignment) intermediate representation:

- LuaJIT: SSA IR (64-bit per instruction, ~160 opcodes)
- V8 Maglev: SSA + CFG
- SpiderMonkey Warp: MIR (SSA)
- JavaScriptCore: DFG/B3 (SSA)

Our current trace compiler goes directly from `TraceIR` (bytecode recording) to ARM64. There's no IR where we can:

1. **Track types at compile time** — know that `sum` is always int64, eliminate runtime type checks
2. **Keep values unboxed** — `sum` lives in X20 as a raw int64, never becomes a 32-byte Value
3. **Hoist guards** — check `piece.type == "R"` once at loop entry, not every iteration
4. **Eliminate dead operations** — if we know the type, don't write the type byte back to memory

The key insight from studying LuaJIT: **the performance gap is not about generating more ARM64 instructions. It's about generating fewer.** With SSA + type specialization, a `sum = sum + i` loop body compiles to a single `ADD X20, X20, X21` — no loads, no stores, no guards, no type bytes. Our current trace compiles it to ~15 instructions.

## Next Steps

1. **SSA IR**: Introduce a typed SSA IR between trace recording and ARM64 codegen
2. **Type specialization**: Integer values stay unboxed in ARM64 registers across the entire trace
3. **Guard hoisting**: Type checks move to trace entry; loop body is guard-free
4. **SSA-based register allocation**: Linear scan on live intervals, not frequency counting

This is the subject of the next post.

## References

- Mike Pall, [LuaJIT 2.0 SSA IR](http://wiki.luajit.org/SSA-IR-2.0)
- V8 Team, [Maglev - V8's Fastest Optimizing JIT](https://v8.dev/blog/maglev)
- Mozilla, [Warp: Improved JS Performance in Firefox 83](https://hacks.mozilla.org/2020/11/warp-improved-js-performance-in-firefox-83/)
- Cornell CS 6120, [Trace-based JIT Type Specialization](https://www.cs.cornell.edu/courses/cs6120/2019fa/blog/tbjit-type-specialization/)
- PlanetScale, [Faster Interpreters in Go](https://planetscale.com/blog/faster-interpreters-in-go-catching-up-with-cpp)
