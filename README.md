# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a register-based bytecode VM and ARM64 Method JIT compiler. Built by AI agents (Claude) as an experiment in AI-driven compiler development.

> **Blog**: [jxwr.github.io/gscript](https://jxwr.github.io/gscript/) — the full story of building this JIT.

## Quick Start

```bash
go build -o gscript ./cmd/gscript/
./gscript examples/fib.gs          # tree-walker
./gscript --vm examples/fib.gs     # bytecode VM
./gscript --jit examples/fib.gs    # ARM64 JIT (Apple Silicon)
./gscript -e 'print("hello")'      # eval
./gscript                           # REPL
```

## Architecture

Three-tier execution: **interpreter → Tier 1 baseline JIT → Tier 2 optimizing JIT**.

**Tier 1** — V8 Sparkplug-style 1:1 bytecode→ARM64 templates. Native BLR calls (~10ns), inline field cache, GETGLOBAL value cache, NaN-boxing (8-byte Value: int/float/pointer in uint64).

**Tier 2** — SSA IR pipeline: `BuildGraph → TypeSpec → Intrinsic → Inline → ConstProp → DCE → RangeAnalysis → LICM → RegAlloc → ARM64`. Type-specialized registers, deopt guards, FPR/GPR carry across loop iterations, feedback-typed speculation.

> Architecture details: [docs-internal/architecture/overview.md](docs-internal/architecture/overview.md)

## Benchmarks

Run the full suite:

```bash
bash benchmarks/run_all.sh
```

> Latest numbers and LuaJIT comparison: see `benchmarks/data/latest.json` or the [blog](https://jxwr.github.io/gscript/).

## Optimization Loop

The compiler is optimized by an automated loop ([opt-loop](https://github.com/jxwr/opt-loop)) — each round is an independent Claude Code session:

```
REVIEW → ANALYZE+PLAN → IMPLEMENT → VERIFY+DOCUMENT
```

> Round history: `opt/INDEX.md`. Workflow docs: `CLAUDE.md`.

## Testing

```bash
go test ./internal/methodjit/ -short -count=1 -timeout 120s
go test ./internal/vm/ -short -count=1 -timeout 120s
```

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md)
- **Examples**: [examples/](examples/) — from fibonacci to Chinese Chess AI with Raylib GUI

## License

MIT
