# GScript

Dynamically-typed scripting language with Go syntax and Lua semantics. Three-tier execution on Apple Silicon ARM64: **interpreter → Tier 1 baseline JIT → Tier 2 optimizing JIT**.

## Hard rules

1. **TDD.** Failing test first, then the minimum code to pass.
2. **Correctness first.** A wrong-but-fast compiler poisons every subsequent comparison.
3. **All tests pass before stacking changes.** Never build on unverified code.
4. **JIT code is opaque to `pprof`.** Use `Diagnose()` and the ARM64 disasm in `diag/` for Tier 2 inspection. `pprof` is valid for Go-runtime / interpreter paths.
5. **Authoritative Tier 2 evidence comes from `compileTier2Pipeline` only.** `TieringManager.CompileForDiagnostics` shares the production path and is parity-tested. Parallel pipelines drift.
6. **Median-of-N for benchmark comparisons.** `--runs=5` for publish; `--runs=3` mid-investigation.
7. **Contradicted diagnostic data halts the work.** Root-cause the tool before patching around it.
8. **IR/asm-instruction savings ≠ wall-time savings.** M4 is 6–8-wide superscalar; off-critical-path insns are absorbed. Always validate with benchmarks.
9. **V8's architectural choices are the default.** Deviate only with explicit evidence.
10. **No trace JIT.** Method-JIT-shaped only. Trace-shaped features are admissible only as passes inside the existing Tier 2 pipeline.

## Tools

```bash
# Full bench suite (VM / JIT / LuaJIT)
bash benchmarks/run_all.sh [--runs=N]

# Statistical regression guard with checksum + CV; covers suite + extended + variants
python3 benchmarks/strict_guard.py [--bench <suite>/<name>] [--runs N]

# Production-parity Tier 2 IR/asm dump
bash scripts/diag.sh all
bash scripts/diag.sh suite|extended|variants
bash scripts/diag.sh <bench>            # bare name searched suite→extended→variants
bash scripts/diag.sh <suite>/<bench>    # explicit
# Output: diag/<suite>/<bench>/{<proto>.bin,.asm.txt,.ir.txt,stats.json}

# In-process oracle: IR-interpreter vs ARM64 native, full pass log
Diagnose(proto, args)
```
