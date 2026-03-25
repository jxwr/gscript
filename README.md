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

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7. Updated **2026-03-26**.

### Full suite (21 benchmarks)

| Benchmark | VM (interpreter) | JIT (trace-compiled) | Speedup |
|-----------|-----------------|---------------------|---------|
| **table_field_access** | 0.77s | **0.07s** | **11.3x** |
| **mandelbrot(1000)** | 1.43s | **0.26s** | **5.5x** |
| **fibonacci_iterative(70×1M)** | 1.12s | **0.35s** | **3.2x** |
| **table_array_access** | 0.42s | **0.14s** | **3.0x** |
| **sieve(1M ×3)** | 0.26s | **0.14s** | **1.9x** |
| **fannkuch(9)** | 0.60s | **0.50s** | **1.2x** |
| fib(35) | 1.74s | 1.77s | 1.0x |
| ackermann(3,4 ×500) | 0.30s | 0.37s | 0.8x |
| nbody(500K) | 1.95s | 1.98s | 1.0x |
| sort(50K ×3) | 0.19s | 0.19s | 1.0x |
| sum_primes(100K) | 0.03s | 0.03s | 1.0x |
| binary_trees(15) | 1.73s | 1.75s | 1.0x |
| matmul(300) | 1.07s | 1.54s | 0.7x |
| spectral_norm(500) | 1.03s | 1.12s | 0.9x |
| mutual_recursion | 0.22s | 0.26s | 0.9x |
| method_dispatch(100K) | 0.09s | 0.11s | 0.9x |
| closure_bench | 0.008s | 0.009s | 0.9x |
| string_bench | 0.026s | 0.033s | 0.8x |
| object_creation | 0.68s | 0.73s | 0.9x |
| math_intensive | 0.97s | 0.98s | 1.0x |
| coroutine_bench | 5.74s | 5.68s | 1.0x |

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
