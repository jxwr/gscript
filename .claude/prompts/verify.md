# VERIFY Phase

You are in the VERIFY phase of the GScript optimization loop.

## Context
Read these files:
1. `.claude/current_plan.md` — the plan with expected effects
2. `benchmarks/data/baseline.json` — baseline from MEASURE phase

## Task
1. **Run all tests**:
   ```
   go test ./internal/methodjit/... -short -count=1 -timeout 120s
   go test ./internal/vm/... -short -count=1 -timeout 120s
   ```
   If tests fail: fix them before proceeding. Correctness first.

2. **Run full benchmark suite**:
   ```
   bash benchmarks/run_all.sh
   ```

3. **Compare vs baseline**:
   ```
   bash benchmarks/benchmark_diff.sh
   ```
   Or manually compare `benchmarks/data/latest.json` vs `benchmarks/data/baseline.json`.

4. **Spawn Evaluator** sub-agent to review the git diff:
   - Read all changed files
   - Check for: correctness risks, scope creep, code quality, missed edge cases
   - Output: pass/fail with specific issues

## Output
Fill the "Results" section in `.claude/current_plan.md`:

```markdown
## Results
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
```

## Decision
- All tests pass + improvement + evaluator pass → proceed to DOCUMENT
- No improvement → fill "Lessons" section, report "no_change"
- Regression → report "regressed" with details
- Evaluator fail → fix issues, re-verify
