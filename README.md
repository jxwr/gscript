# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 tracing JIT compiler. Built entirely by AI agents (Claude) as an experiment in AI-driven compiler development.

## Blog: "Beyond LuaJIT"

The full story of building this JIT — 20 posts covering architecture, optimization, failures, a complete rewrite, the pivot to Method JIT, and hard performance data.

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

| Benchmark | VM | JIT | JIT/VM | LuaJIT | vs LuaJIT |
|-----------|-----|-----|--------|--------|-----------|
| fib(35) | 1.555s | 1.555s | 1.0x | 23ms | 67.6x |
| **table_field_access** | 718ms | **65ms** | **11.0x** | -- | -- |
| **nbody(500K)** | 1.805s | **313ms** | **5.8x** | 32ms | 9.8x |
| **mandelbrot(1000)** | 1.357s | **276ms** | **4.9x** | 51ms | 5.4x |
| **matmul(300)** | 974ms | **215ms** | **4.5x** | 21ms | 10.2x |
| **table_array_access** | 383ms | **138ms** | **2.8x** | -- | -- |
| **fibonacci_iterative** | 1.014s | **423ms** | **2.4x** | -- | -- |
| **sieve(1M x3)** | 239ms | **130ms** | **1.8x** | 10ms | 13.0x |
| **math_intensive** | 896ms | **676ms** | **1.3x** | -- | -- |
| **fannkuch(9)** | 556ms | **461ms** | **1.2x** | 18ms | 25.6x |
| coroutine_bench | 4.97s | 4.93s | 1.0x | -- | -- |
| sort(50K x3) | 169ms | 174ms | 1.0x | 10ms | 17.4x |
| string_bench | 41ms | 41ms | 1.0x | 9ms | 4.6x |
| sum_primes(100K) | 27ms | 30ms | 0.9x | 2ms | 15.0x |
| spectral_norm(500) | 951ms | 823ms | 0.9x | 7ms | 117.6x |
| binary_trees(15) | 1.55s | ERROR | -- | 161ms | -- |
| mutual_recursion | 194ms | 251ms | 0.8x | 4ms | 62.8x |
| ackermann(3,4 x500) | 270ms | ⚠️ | -- | 6ms | -- |
| closure_bench | 82ms | ⚠️ | -- | 8ms | -- |
| object_creation | 614ms | 1.81s | 0.3x | -- | -- |
| method_dispatch | 84ms | 239ms | 0.4x | <1ms | -- |

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

**Method JIT** (planned, V8 Maglev-inspired):

- Compiles whole functions using type feedback from the interpreter
- CFG-based SSA IR with basic blocks, phi nodes, and speculative optimization
- Handles recursion, branches, and function inlining — what the trace JIT cannot do
- Deoptimization framework for falling back to interpreter when speculation fails
- Tiered: interpreter → Method JIT → trace JIT for hot inner loops

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
