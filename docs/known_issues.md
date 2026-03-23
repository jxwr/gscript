# Known Issues

## Trace JIT Interaction Issues

When Method JIT and Trace JIT are both active (`-jit` flag), certain benchmarks have issues:

### sort.gs — Stack Overflow
- **Status**: Investigating
- **Symptom**: `stack overflow (max call depth 200)` when trace JIT is active
- **Root cause**: The trace recorder compiles quicksort's partition loop. The compiled trace uses call-exit for recursive calls, which goes through `vm.call` (pushes VM frames) instead of the Method JIT executor's cross-call (uses JIT depth counter). Deep recursion exceeds `maxCallDepth=200`.
- **Workaround**: The `HasSelfCalls` guard should prevent this, but there may be a timing issue with when `HasSelfCalls` is set vs when the trace recorder activates.

### SSA_CALL Result Handling
- **Status**: Known bug, not yet fixed
- **Symptom**: Traces containing SSA_CALL (function calls within loops) can produce incorrect results
- **Root cause**: The SSA compiler's call-exit register reload may not correctly handle all register state scenarios after a nested call
- **Impact**: Traces with `HasCallInLoop()` are currently disabled to work around this

### GC scanTableRoots Intermittent SIGSEGV
- **Status**: Partially fixed (safe-point mechanism), intermittent occurrences remain
- **Symptom**: SIGSEGV in `scanTableRoots` during GC compaction, typically in table-heavy benchmarks like chess_bench
- **Root cause**: GC compaction may encounter stale NaN-boxed pointers in edge cases not covered by the safe-point mechanism
