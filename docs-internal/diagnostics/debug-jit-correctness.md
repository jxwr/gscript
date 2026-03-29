# Debug: JIT Correctness (Wrong Results)

## Symptom

JIT produces different output than the interpreter. Benchmark shows wrong values or crashes.

## Step 1: Diagnose — one call

```go
report := methodjit.Diagnose(proto, args)
t.Log(report)
```

This runs the full pipeline: BuildGraph → Validate → Passes → RegAlloc → Emit → Execute, plus the IR interpreter. The report shows:

- `IRBefore` / `IRAfter` — IR before and after optimization passes
- `PassDiffs` — what each pass changed
- `ValidateErrors` — structural invariant violations
- `InterpResult` vs `NativeResult` — do they match?
- `Match` — true/false
- `Mismatch` — description of what differed

## Step 2: Narrow the problem

**If `ValidateErrors` is non-empty**: bug is in BuildGraph (graph builder) or a pass that produced invalid IR. Fix the first error; later errors are often cascading.

**If `InterpResult != NativeResult`** but IR is valid: bug is in emit (ARM64 code generation). The IR is correct but native execution is wrong.

**If `InterpResult` is already wrong**: bug is in BuildGraph or an optimization pass. Use `PassDiffs` to find which pass introduced the error.

## Step 3: Binary search passes

```go
// Print IR after each pass to find which one broke it
for _, diff := range report.PassDiffs {
    t.Log(diff)
}
```

Once you identify the offending pass, read only that pass's source file (`pass_<name>.go`).

## Step 4: Minimal reproduction

Run the smallest possible test case:
- `mandelbrot(3)`, not `mandelbrot(1000)`
- `fib(5)`, not `fib(35)`
- Single-iteration loop with known expected values

## Tool Reference

| Tool | Location | Purpose |
|------|----------|---------|
| `Diagnose()` | `internal/methodjit/diagnose.go` | Full pipeline diagnostic |
| `Print()` | `internal/methodjit/printer.go` | Human-readable IR dump |
| `Validate()` | `internal/methodjit/validator.go` | Structural invariant checks |
| `Interpret()` | `internal/methodjit/interp.go` | IR interpreter (correctness oracle) |

## NaN-Boxing Tags (for reading hex dumps)

| Upper 16 bits | Type |
|---------------|------|
| `0xFFFE` | Int |
| `0xFFFD` | Bool |
| `0xFFFF` | Pointer (Table/String/Function) |
| `0xFFFC` | Nil |
| anything else | Float (raw IEEE 754 double) |
