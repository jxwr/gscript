# Debug: JIT Correctness (Wrong Results)

## Symptom

JIT produces different output than the interpreter. Benchmark shows wrong values or crashes.

## Step 0: Confirm the hot function is actually in Tier 2

Many perf and correctness investigations from R132 onward started with
the wrong assumption that a function was running in Tier 2 native code
when it was actually being routed to Tier 1 / VM. **Always check first**:

```bash
# Run the benchmark with JIT statistics
go run ./cmd/gscript -jit -jit-stats benchmarks/suite/fib.gs
# Look for:
#   Tier 2 entered:  N functions
#     - fib (entered=yes)
```

- `entered=yes` — the Tier 2 native prologue executed at least once for
  this proto. Safe to reason about Tier 2 emit.
- `entered=no` but `Tier 2 compiled` includes this proto — compiled but
  never actually called through. Routing bug; fix the routing before
  chasing an emit issue.
- Proto missing from the list — never compiled. Check smart-tiering
  (`func_profile.go`) and `shouldPromoteTier2` before anything else.

For `benchmarks/run_bench.sh`, the `T2` column in the results table
shows `entered/compiled` counts per benchmark — same signal, already
in your bench output.

Mechanics: `proto.EnteredTier2` is a byte set to 1 by a ~6-insn STRB at
the head of each Tier 2 entry point (R146). Cost is inside warm-bench
noise; the flag exists purely for observability.

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
