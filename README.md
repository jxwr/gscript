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

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1. Updated **2026-03-23**.

### Warm micro-benchmarks (JIT vs VM)

| Benchmark | JIT | VM | Speedup | LuaJIT | JIT vs LuaJIT |
|-----------|-----|-----|---------|--------|---------------|
| HeavyLoop | 23.1us | 955us | x41.3 | — | — |
| FibIterative(30) | 149ns | 510ns | x3.4 | — | — |
| FunctionCalls(10K) | 2.37us | 309us | x130.4 | 2.3us | parity |
| FibRecursive(20) | 23.6us | 808us | x34.2 | 25.0us | GScript WINS |
| Ackermann(3,4) | 20.6us | 379us | x18.4 | 12.0us | 1.7x gap |

### Full suite (21 benchmarks, JIT mode)

| Benchmark | JIT | LuaJIT | vs LuaJIT |
|-----------|-----|--------|-----------|
| fib(35) | 0.034s | 0.032s | 1.0x (parity) |
| sieve(1M x3) | 0.022s | 0.010s | 2.2x |
| mandelbrot(1000) | 1.386s | 0.052s | 26.7x |
| ackermann(3,4 x500) | 0.011s | 0.006s | 1.8x |
| matmul(300) | 1.022s | 0.022s | 46.5x |
| spectral_norm(500) | 0.923s | 0.007s | 132x |
| nbody(500K) | 1.937s | 0.033s | 58.7x |
| binary_trees(15) | 2.222s | 0.17s | 13.1x |
| fannkuch(9) | 0.572s | 0.019s | 30.1x |
| sort(50K x3) | 0.185s | 0.010s | 18.5x |
| sum_primes(100K) | 0.027s | 0.002s | 13.5x |
| mutual_recursion(25x1K) | 0.331s | 0.005s | 66.2x |
| method_dispatch(100K) | 0.115s | 0.000s | ~230x |
| closure_bench | 0.021s | 0.009s | 2.3x |
| string_bench | 0.007s | 0.008s | 0.9x (GScript WINS) |
| fibonacci_iterative(70x1M) | 0.277s | — | — |
| math_intensive | 0.927s | — | — |
| object_creation | 1.168s | — | — |
| table_array_access | 0.141s | — | — |
| table_field_access | 0.725s | — | — |
| coroutine_bench | 4.771s | — | — |

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
