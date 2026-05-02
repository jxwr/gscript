# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a register-based bytecode VM and ARM64 Method JIT compiler.

## Quick start

```bash
go build -o gscript ./cmd/gscript/
./gscript examples/fib.gs          # tree-walker
./gscript --vm examples/fib.gs     # bytecode VM
./gscript --jit examples/fib.gs    # ARM64 JIT (Apple Silicon)
./gscript -e 'print("hello")'      # eval
./gscript                          # REPL
```

## Architecture

Three-tier execution: **interpreter → Tier 1 baseline JIT → Tier 2 optimizing JIT**.

- **Tier 1** — V8 Sparkplug-style 1:1 bytecode→ARM64 templates. Native BLR calls, inline field cache, GETGLOBAL value cache, NaN-boxing.
- **Tier 2** — SSA IR pipeline: `BuildGraph → TypeSpec → Intrinsic → Inline → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → RegAlloc → ARM64`.

## Benchmarks

```bash
bash benchmarks/run_all.sh
```

## Testing

```bash
go test ./internal/methodjit/ -short -count=1 -timeout 120s
go test ./internal/vm/ -short -count=1 -timeout 120s
```

## License

MIT
