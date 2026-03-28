# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 tracing JIT compiler. Built entirely by AI agents (Claude) as an experiment in AI-driven compiler development.

## Blog: "Beyond LuaJIT"

The full story of building this JIT — 18 posts covering architecture, optimization, failures, a complete rewrite, and hard performance data.

Read the series at **[jxwr.github.io/gscript](https://jxwr.github.io/gscript/)**.

## Quick Start

```bash
git clone https://github.com/jxwr/gscript
cd gscript
go build -o gscript ./cmd/gscript/
```

```bash
./gscript examples/fib.gs          # tree-walker
./gscript --vm examples/fib.gs     # bytecode VM
./gscript --jit examples/fib.gs    # ARM64 JIT (Apple Silicon)
./gscript -e 'print("hello")'      # eval a string
./gscript                           # REPL
```

## Performance

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1. Updated **2026-03-28**.

### Full suite (21 benchmarks)

| Benchmark | VM | JIT | JIT/VM | LuaJIT | JIT/LuaJIT |
|-----------|-----|-----|--------|--------|------------|
| **table_field_access** | 0.71s | **0.07s** | **10.4x** | -- | -- |
| **nbody(500K)** | 1.77s | **0.29s** | **6.0x** | 0.032s | 9.1x |
| **mandelbrot(1000)** | 1.33s | **0.28s** | **4.7x** | 0.051s | 5.5x |
| **matmul(300)** | 0.97s | **0.21s** | **4.5x** | 0.021s | 10.0x |
| **table_array_access** | 0.39s | **0.13s** | **2.9x** | -- | -- |
| **fibonacci_iterative(70x1M)** | 1.04s | **0.40s** | **2.6x** | -- | -- |
| **closure_bench** | 0.08s | **0.03s** | **2.4x** | 0.008s | 4.1x |
| **sieve(1M x3)** | 0.23s | **0.13s** | **1.7x** | 0.010s | 13.2x |
| **fannkuch(9)** | 0.55s | **0.47s** | **1.2x** | 0.018s | 25.9x |
| math_intensive | 0.89s | 0.83s | 1.1x | -- | -- |
| coroutine_bench | 5.04s | 4.87s | 1.0x | -- | -- |
| fib(35) | 1.57s | 1.61s | 1.0x | 0.023s | 70.2x |
| binary_trees(15) | 1.54s | 1.60s | 1.0x | 0.179s | 9.0x |
| sort(50K x3) | 0.17s | 0.17s | 1.0x | 0.009s | 19.3x |
| sum_primes(100K) | 0.03s | 0.03s | 0.9x | 0.002s | 14.5x |
| object_creation | 0.62s | 0.66s | 0.9x | -- | -- |
| spectral_norm(500) | 0.94s | 1.07s | 0.9x | 0.007s | 153.3x |
| method_dispatch(100K) | 0.08s | 0.09s | 0.9x | <0.001s | -- |
| mutual_recursion | 0.20s | 0.23s | 0.9x | 0.005s | 45.8x |
| string_bench | 0.04s | 0.05s | 0.9x | 0.008s | 6.0x |
| ackermann(3,4 x500) | 0.27s | 0.34s | 0.8x | 0.007s | 48.7x |

### Architecture

**Trace JIT** — records hot loops, compiles to ARM64 native code via SSA IR:

- **Value-based SSA**: each instruction produces a new value (decoupled from VM slots), eliminating the slot-reuse bugs that plagued the old architecture
- **Snapshots**: per-guard-point slot→value mapping for precise deoptimization on side-exit
- **Native table operations**: GETTABLE/SETTABLE → inline bounds check + direct array access (Mixed/Int/Float/Bool kinds)
- **Native field operations**: GETFIELD/SETFIELD → shape-based svals indexing (no hash lookup)
- **While-loop compilation**: JMP back-edge loops compile alongside FORLOOP-based loops
- **Side-exit for calls**: unsupported ops exit to interpreter; FORLOOP re-enters trace
- **Register allocation**: frequency-based slot allocation (X20-X23) + linear-scan float ref allocation (D4-D11)
- **Intrinsics**: math.sqrt → FSQRT

**Runtime**:

- NaN-boxing (8-byte Value: int/float/pointer in one uint64)
- Type-specialized arrays (ArrayInt, ArrayFloat, ArrayBool)
- Inline field cache (per-instruction O(1) field access)

### Testing

136 tests across 4 layers, designed from first principles:

| Layer | Tests | What it verifies |
|-------|-------|-----------------|
| Codegen Micro | 21 | Each ARM64 instruction sequence |
| Trace Execution | 31 | Exit state, guard behavior, no hangs |
| Opcode Matrix | 48 | VM vs JIT per-opcode correctness |
| Invariant Tests | 36 | JIT result = VM result (5 invariants × interaction patterns) |

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) — 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, etc.)
- **JIT architecture**: [docs/jit-rewrite-design.md](docs/jit-rewrite-design.md) — design document for the trace JIT
- **Examples**: [examples/](examples/) — from fibonacci to a full Chinese Chess AI with Raylib GUI

## License

MIT
