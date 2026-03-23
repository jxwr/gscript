# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 JIT compiler. This is an AI-agent-driven experiment to build a JIT compiler that surpasses LuaJIT -- the entire compiler was designed, implemented, optimized, and documented by Claude.

## Blog: "Beyond LuaJIT"

We are documenting the entire journey of building this JIT compiler as a series of technical blog posts. Each post covers a major optimization milestone -- what we tried, what worked, what failed, and the hard data behind every decision. The blog is as much a part of the project as the code itself.

Read the full series at **[jxwr.github.io/gscript](https://jxwr.github.io/gscript/)**.

## Quick Start

```bash
git clone https://github.com/jxwr/gscript
cd gscript
go build -o gscript ./cmd/gscript/
```

```bash
./gscript examples/fib.gs          # tree-walker
./gscript --vm examples/fib.gs     # bytecode VM (3-5x faster)
./gscript --jit examples/fib.gs    # ARM64 JIT (Apple Silicon)
./gscript -e 'print("hello")'      # eval a string
./gscript                           # REPL
```

## Performance

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1. Updated **2026-03-24**.

### Warm micro-benchmarks (JIT vs VM)

| Benchmark | JIT | VM | Speedup | LuaJIT | JIT vs LuaJIT |
|-----------|-----|-----|---------|--------|---------------|
| HeavyLoop | 23.0us | 984us | x42.8 | — | — |
| FibIterative(30) | 170ns | 518ns | x3.0 | — | — |
| FunctionCalls(10K) | 2.59us | 500us | x193.1 | 2.3us | parity |
| FibRecursive(20) | 23.8us | 1194us | x50.2 | 25.0us | GScript WINS |
| Ackermann(3,4) | 20.6us | 379us | x18.4 | 12.0us | 1.7x gap |

### Full suite (21 benchmarks, JIT mode)

| Benchmark | JIT | LuaJIT | vs LuaJIT |
|-----------|-----|--------|-----------|
| fib(35) | 0.034s | 0.025s | 1.4x |
| sieve(1M x3) | 0.023s | 0.011s | 2.1x |
| mandelbrot(1000) | 0.158s | 0.058s | 2.7x |
| ackermann(3,4 x500) | 0.012s | 0.006s | 2.0x |
| matmul(300) | 1.222s | 0.022s | 55.5x |
| spectral_norm(500) | — | 0.007s | — |
| nbody(500K) | 1.938s | 0.035s | 55.4x |
| binary_trees(15) | 1.698s | 0.172s | 9.9x |
| fannkuch(9) | 0.581s | 0.020s | 29.1x |
| sort(50K x3) | 0.191s | 0.011s | 17.4x |
| sum_primes(100K) | 0.028s | 0.002s | 14.0x |
| mutual_recursion(25x1K) | 0.219s | 0.004s | 54.8x |
| method_dispatch(100K) | 0.110s | 0.000s | ~220x |
| closure_bench | 0.022s | 0.005s | 4.4x |
| string_bench | 0.007s | 0.004s | 1.8x |
| fibonacci_iterative(70x1M) | 0.295s | — | — |
| object_creation | 1.270s | — | — |
| table_array_access | 0.150s | — | — |
| table_field_access | 0.791s | — | — |
| coroutine_bench | 3.174s | — | — |

### Compiler Optimization Techniques

**Method JIT (function-level compilation)**
- Register pinning: hot variables mapped to ARM64 registers (X19-X24)
- Function inlining: small callees inlined into caller's loop body
- Tail call optimization: recursive tail calls -> direct jump (no stack frame)
- BOLT-style cold code splitting: guard failures moved out of hot path
- Native table operations: GETTABLE/SETTABLE/GETFIELD/SETFIELD compiled to ARM64
- Native append: sequential table fill without Go function call overhead

**Tracing JIT (loop-level compilation)**
- SSA IR: BuildSSA -> Optimize -> ConstHoist -> CSE -> FuseMultiplyAdd -> RegAlloc -> Emit
- Full nested loop recording: inner loops inlined into outer trace
- Sub-trace calling: pre-compiled inner traces called via BLR
- Float register allocation: linear-scan ref-level allocator for D4-D11
- Side-exit continuation: escape paths handled in native code, not interpreter
- Write-before-read guard relaxation: skip redundant type guards for overwritten slots

**Runtime optimizations**
- NaN-boxing: Value from 24B struct to 8B uint64 (Season 2)
- Type-specialized arrays: ArrayInt (`[]int64`), ArrayFloat (`[]float64`), ArrayBool (`[]byte`)
- Inline field cache: per-instruction hint for O(1) field access
- DivNums fast path: bypass metamethod dispatch for numeric division

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
