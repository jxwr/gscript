# Debug: Deoptimization Failures

## Symptom

Type guards fire unexpectedly. Function falls back to interpreter repeatedly. JIT speedup is lower than expected.

## Step 1: Check if deopt is happening

Look for `JITSideExited` flags or interpreter fallback in benchmarks. If a function's JIT entry is being cleared (tier-down), deopt is firing too often.

## Step 2: Identify which guard failed

The deopt framework logs: which guard, expected type vs actual type, function name, bytecode PC.

Look for:
- **Type guard**: expected `int`, got `float` → type feedback was stale or too narrow
- **Shape guard**: expected shape ID X, got Y → table layout changed between compilation and execution
- **Overflow guard**: integer overflow on arithmetic → need wider type or bail to boxed mode

## Step 3: Check type feedback

```go
// Examine the FeedbackVector for the function
fv := proto.FeedbackVector
// Check what types were observed at each instruction
```

Common causes:
- **Polymorphic site**: type changed after compilation. Fix: broaden guard or accept deopt.
- **Cold path hit**: rare type appears for the first time. Fix: higher tier-up threshold.
- **Feedback stale**: function was compiled before the type stabilized. Fix: reset feedback and recompile.

## Step 4: Verify the fix

1. Run `Diagnose()` — does the function still compile?
2. Run `Validate()` — is the IR still valid after guard removal?
3. Run full benchmark suite — did the fix improve the target without regressing others?

## Anti-patterns to avoid

- **Don't weaken guards globally.** One guard failing doesn't mean all guards should be weaker.
- **Don't suppress deopt.** If a guard fires, the type assumption is wrong. Fix the assumption or the feedback.
- **Don't increase tier-up threshold indefinitely.** Higher threshold means longer warmup.
