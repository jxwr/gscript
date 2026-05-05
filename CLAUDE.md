# GScript

Dynamically-typed scripting language with Go syntax and Lua semantics. Three-tier execution on Apple Silicon ARM64: **interpreter → Tier 1 baseline JIT → Tier 2 optimizing JIT**.

Tier 2 IR pipeline: `BuildGraph → TypeSpec → Intrinsic → Inline → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → RegAlloc → ARM64`.

## Tools

```bash
# Full bench suite (VM / JIT / LuaJIT)
bash benchmarks/run_all.sh [--runs=N]

# Statistical regression guard with checksum + CV; covers suite + extended + variants
python3 benchmarks/strict_guard.py [--bench <suite>/<name>] [--runs N]

# Current vs HEAD vs LuaJIT timing comparison
python3 benchmarks/timing_compare.py --all-groups [--runs N] [--sort=luajit-gap]

# Production-parity Tier 2 IR/asm dump
bash scripts/diag.sh <bench>            # bare name searched suite→extended→variants
bash scripts/diag.sh <suite>/<bench>    # explicit
# Output: diag/<suite>/<bench>/{<proto>.bin,.asm.txt,.ir.txt,stats.json}

# In-process oracle: IR-interpreter vs ARM64 native, full pass log
Diagnose(proto, args)
```

JIT native code is opaque to `pprof`; use `Diagnose()` and the ARM64 disasm under `diag/` for Tier 2 inspection. `pprof` is valid for Go-runtime / interpreter paths.
