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

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1. Updated 2026-03-21 19:00 CST.

### GScript JIT vs LuaJIT

| Benchmark | What it tests | GScript (best) | LuaJIT | Gap | Notes |
|-----------|--------------|---------------|--------|-----|-------|
| fn calls (10K) warm | function call + inlining overhead | **2.6us** | 2.6us | **parity** | |
| fib(20) warm | recursive function calls | 27.0us | 25us | 1.1x | |
| fib(35) | deep recursion, cold start | 0.037s | 0.032s | 1.2x | |
| ackermann(3,4 x500) | deep mutual recursion | 0.011s | 0.008s | 1.4x | |
| ackermann(3,4) warm | recursive calls, warm JIT | 21.5us | 12us | 1.8x | |
| sieve(1M x3) | integer array read/write | **0.025s** | 0.011s | **2.3x** | NaN-boxing 3.2x speedup |
| mandelbrot(1000) | float-heavy nested loops | 0.157s | 0.057s | 2.8x | trace JIT |
| string_bench | string concat/format/compare | 0.051s | 0.010s | 5.1x | |
| closure_bench | closure creation + calls | 0.071s | 0.012s | 5.9x | |
| sort(50K x3) | quicksort, array swap | 0.207s | 0.016s | 13x | |
| sum_primes(100K) | loop + modulo + branch | 0.027s | 0.002s | 14x | |
| binary_trees | deep tree alloc + GC | 2.385s | 0.17s | 14x | |
| fannkuch(9) | permutation + array reversal | 0.662s | 0.025s | 26x | trace timeout |
| mutual_recursion | mutually recursive functions | 0.150s | 0.005s | 30x | |
| matmul(300) | triple-nested loop, 2D float array | 1.16s | 0.029s | 40x | |
| nbody(500K) | N-body simulation, field access | 2.47s | 0.043s | 57x | |
| spectral_norm(500) | matrix math, function call in inner loop | 0.76s | 0.009s | 85x | |
| method_dispatch(100K) | dynamic dispatch, field access | 0.093s | ~0.001s | ~93x | |

### GScript JIT vs Interpreter (warm)

| Benchmark | What it tests | JIT | VM | Speedup |
|-----------|--------------|-----|-----|---------|
| FunctionCalls(10K) | inlined function call loop | **2.6us** | 338us | **x130** |
| HeavyLoop | integer sum loop (100K) | **25.5us** | 1309us | **x51** |
| FibRecursive(20) | recursive fibonacci | **27.0us** | 879us | **x33** |
| Ackermann(3,4) | deep recursion | **21.5us** | 417us | **x19** |
| FibIterative(30) | iterative fibonacci loop | **182ns** | 637ns | **x3.5** |

### Compiler Optimization Techniques

**Method JIT (function-level compilation)**
- Register pinning: hot variables mapped to ARM64 registers (X19-X24)
- Function inlining: small callees inlined into caller's loop body
- Tail call optimization: recursive tail calls → direct jump (no stack frame)
- BOLT-style cold code splitting: guard failures moved out of hot path
- Native table operations: GETTABLE/SETTABLE/GETFIELD/SETFIELD compiled to ARM64
- Native append: sequential table fill without Go function call overhead

**Tracing JIT (loop-level compilation)**
- SSA IR: BuildSSA → Optimize → ConstHoist → CSE → FuseMultiplyAdd → RegAlloc → Emit
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
