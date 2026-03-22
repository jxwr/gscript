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

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1. Updated **2026-03-22**.

### Warm micro-benchmarks (JIT vs VM)

| Benchmark | JIT | VM | Speedup | LuaJIT | JIT vs LuaJIT |
|-----------|-----|-----|---------|--------|---------------|
| HeavyLoop | 23.1us | 955us | x41.3 | — | — |
| FibIterative(30) | 149ns | 510ns | x3.4 | — | — |
| FunctionCalls(10K) | 2.37us | 309us | x130.4 | 2.3us | parity |
| FibRecursive(20) | 23.6us | 808us | x34.2 | 25.0us | GScript WINS |
| Ackermann(3,4) | 20.6us | 379us | x18.4 | 12.0us | 1.7x gap |

### Full suite (15 benchmarks x 3 modes)

| Benchmark | VM | JIT | Trace | Best | LuaJIT | vs LuaJIT |
|-----------|-----|-----|-------|------|--------|-----------|
| fib(35) | 1.069s | 0.034s | 0.033s | 0.033s | 0.032s | 1.0x (parity) |
| sieve(1M x3) | 0.236s | 0.023s | 0.021s | 0.021s | 0.010s | 2.1x |
| mandelbrot(1000) | 1.332s | 1.409s | 0.141s | 0.141s | 0.052s | 2.7x |
| ackermann(3,4 x500) | 0.187s | 0.011s | 0.010s | 0.010s | 0.006s | 1.7x |
| matmul(300) | 0.962s | 1.015s | 1.148s | 0.962s | 0.022s | 43.7x |
| spectral_norm(500) | 0.776s | 0.709s | 0.702s | 0.702s | 0.007s | 100x |
| nbody(500K) | 1.785s | 1.871s | 1.826s | 1.785s | 0.033s | 54x |
| fannkuch(9) | 0.542s | 0.548s | timeout | 0.542s | 0.019s | 28.5x |
| sort(50K x3) | 0.172s | 0.176s | error | 0.172s | 0.010s | 17.2x |
| sum_primes(100K) | 0.025s | 0.025s | 0.030s | 0.025s | 0.002s | 12.5x |
| mutual_recursion(25x1K) | 0.134s | 0.232s | 0.246s | 0.134s | 0.005s | 26.8x |
| method_dispatch(100K) | 0.073s | 0.104s | 0.105s | 0.073s | 0.000s | ~180x |
| closure_bench | 0.058s | 0.069s | error | 0.058s | 0.009s | 6.4x |
| string_bench | 0.040s | 0.042s | 0.043s | 0.040s | 0.008s | 5.0x |
| binary_trees | 1.324s | crash | crash | 1.324s | 0.17s | 7.8x |

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
