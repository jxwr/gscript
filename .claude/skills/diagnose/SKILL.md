---
name: diagnose
description: Debug JIT compiler test failures using DiagnoseTrace and CompileWithDump diagnostic tools. Invoke when tests fail, guards misfire, or traces produce wrong results.
---

# /diagnose — JIT Test Failure Diagnostic

You are debugging failures in GScript's tracing JIT compiler (`internal/jit/`).

**Argument:** `$ARGUMENTS` (test name pattern, e.g. `TestSSACodegen_CallExit`; empty = run all JIT tests and diagnose failures)

## Principles (from CLAUDE.md Rule 2)

**NEVER read ssa_emit.go or ssa_build.go to guess the bug.** Instead:
1. Run the test, read the error message
2. Use `DiagnoseTrace()` or `CompileWithDump()` to observe pipeline state
3. Read diagnostic output, not implementation code
4. Fix based on evidence

Full tool reference: `docs/jit-debug.md`

## Step 1: Identify failures

If `$ARGUMENTS` is empty, find all failing tests:
```
go test ./internal/jit/ 2>&1 | grep "FAIL:"
```

If a specific test is given:
```
go test ./internal/jit/ -run "$ARGUMENTS" -v 2>&1
```

Categorize each failure by error message:
- **"guard fail"** → pre-loop type check failed
- **"sum = X, want Y"** → wrong computation result
- **"expected N compiled traces, got M"** → trace recording/compilation threshold issue
- **"legacy method JIT removed"** → test uses deleted API, should be removed or skipped
- **compile error** → SSA or codegen issue
- **panic/segfault** → ARM64 codegen bug, use ShowASM

## Step 2: Write a diagnostic test

For each unique failure, write a test using `DiagnoseTrace`:

```go
func TestDiag_XXX(t *testing.T) {
    // ... set up trace and regs same as the failing test ...

    diag := DiagnoseTrace(trace, regs, proto, DiagConfig{
        WatchSlots: []int{0, 1, 2, 3, 4},
    })
    t.Log(diag)

    if diag.GuardFail {
        t.Error("guard fail — check SSA GUARD_TYPE types vs register values")
    }
}
```

Run it with `-v` and read the diagnostic report. Key things to check:
- **Exit line:** `Exit: guard-fail` vs `side-exit` vs `loop-done`
- **Register hex:** does the NaN-boxing tag match expected type? (`0xFFFE`=int, `0xFFFC`=nil, `0xFFFF`=ptr)
- **SSA IR:** do GUARD_TYPE instructions have correct `type=` annotations?
- **Register diff:** did any register change? (unchanged = trace did nothing)

## Step 3: If pipeline is suspect, diff passes

```go
ct, dump := CompileWithDump(trace)
t.Log(dump.String())
// or targeted:
t.Log(dump.Diff("BuildSSA", "ConstHoist"))
```

Look for:
- Instructions that disappear unexpectedly (CSE/DCE removing needed ops)
- Slot numbers changing (ConstHoist reindexing error)
- Type annotations changing between passes

## Step 4: Fix and verify

- Fix the production code (not the test) unless the test expectation is genuinely wrong
- Run the original failing test to confirm the fix
- Run the full JIT test suite: `go test ./internal/jit/`
- Clean up any temporary diagnostic tests

## NaN-Boxing Quick Reference

| Hex tag (upper 16 bits) | Type |
|--------------------------|------|
| `0xFFFE` | Int |
| `0xFFFD` | Bool |
| `0xFFFF` | Pointer (Table/String/Function) |
| `0xFFFC` | Nil |
| other | Float (IEEE 754) |

## Exit Code Quick Reference

| Code | Name | Meaning |
|------|------|---------|
| 0 | loop-done | Normal loop completion |
| 1 | side-exit | JIT can't handle this instruction |
| 2 | guard-fail | Pre-loop type mismatch |
| 3 | call-exit | Legacy (unused) |
| 4 | break-exit | Break statement |
| 5 | max-iter | Hit iteration limit |
