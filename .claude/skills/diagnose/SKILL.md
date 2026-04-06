---
name: diagnose
description: Debug Method JIT (Tier 1/2) test failures using Diagnose(), Validate(), Print() and ARM64 disasm. Invoke when tests fail, guards misfire, or JIT produces wrong results.
---

# /diagnose — Method JIT Diagnostic

You are debugging failures in GScript's **method JIT compiler** (`internal/methodjit/`).

**Argument:** `$ARGUMENTS` (test name pattern, e.g. `TestDiagnose_Mandelbrot`; empty = run all methodjit tests and diagnose failures)

## Principles

**NEVER guess from source code.** Use diagnostic tools first:
1. Run the test, read the error message
2. Use `Diagnose()` to dump full pipeline state
3. Use `Validate()` to check structural invariants
4. Read diagnostic output, then — only then — read the specific source file diagnostics identified

Reference: `docs-internal/diagnostics/debug-jit-correctness.md`

## Step 1: Identify failures

If `$ARGUMENTS` is empty:
```bash
go test ./internal/methodjit/ -short -count=1 -timeout 120s 2>&1 | grep -E "FAIL|PASS"
go test ./internal/vm/ -short -count=1 -timeout 120s 2>&1 | grep -E "FAIL|PASS"
```

If specific test given:
```bash
go test ./internal/methodjit/ -run "$ARGUMENTS" -v -count=1 2>&1
```

Categorize:
- **"wrong result"** → Tier 2 codegen or optimization pass bug
- **"deopt"** → type guard firing unexpectedly (see `debug-deopt.md`)
- **"validate error"** → SSA structural invariant broken (see `debug-ir-pipeline.md`)
- **panic/SIGSEGV** → ARM64 emit bug, use disasm harness
- **hang/timeout** → infinite loop in JIT code or tiering bug

## Step 2: Run Diagnose()

```go
report := methodjit.Diagnose(proto, args)
t.Log(report)
```

This runs: BuildGraph → Validate → all passes → RegAlloc → Emit → Execute, plus the IR interpreter. The report shows:
- `IRBefore` / `IRAfter` — IR before and after optimization
- `PassDiffs` — what each pass changed
- `ValidateErrors` — structural violations
- `InterpResult` vs `NativeResult` — do they match?

## Step 3: Narrow the problem

- **ValidateErrors non-empty** → bug in BuildGraph or a pass. Fix first error.
- **InterpResult ≠ NativeResult, IR valid** → bug in emit (ARM64 codegen)
- **InterpResult wrong** → bug in BuildGraph or optimization pass. Use PassDiffs to find which.

## Step 4: For ARM64 codegen issues

Use the Tier 2 disasm harness (`tier2_float_profile_test.go`) to dump actual ARM64 instructions. Count instructions, check register usage, look for spill/reload anomalies.

Note: pprof is useless for JIT code (shows as opaque `runtime._ExternalCode`).

## Step 5: Fix and verify

- Fix production code, not the test
- Run the specific test to confirm
- Run full suite: `go test ./internal/methodjit/ -short -count=1`
- Check for regressions in related tests

## NaN-Boxing Quick Reference

| Hex tag (upper 16 bits) | Type |
|--------------------------|------|
| `0xFFFE` | Int |
| `0xFFFD` | Bool |
| `0xFFFF` | Pointer (Table/String/Function) |
| `0xFFFC` | Nil |
| other | Float (IEEE 754) |

## Exit Code Quick Reference (Method JIT)

| Code | Meaning |
|------|---------|
| 0 | Normal return |
| 2 | Deopt → interpreter |
| 3 | Call-exit (Tier 2: resume after Go handles call) |
| 4 | Global-exit (Tier 2) |
| 5 | Table-exit (Tier 2) |
| 6 | Op-exit (Tier 2: generic unsupported op) |
| 7 | Baseline op-exit (Tier 1: exit-resume) |
| 8 | Native call exit (Tier 1: callee hit exit during BLR call) |
| 9 | OSR (Tier 1: loop counter expired, request Tier 2 upgrade) |
