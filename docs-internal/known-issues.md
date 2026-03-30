# Known Issues

> Last updated: 2026-03-30

## Current (Tier 1 Baseline JIT)

### sort: stack overflow (max call depth 1000)
- Quicksort on 50K elements can have O(N) worst-case recursion depth
- Native BLR call doesn't push VM frames; slow-path fallback does push VM frames
- When calls fall through to slow path, VM frame count accumulates
- `maxCallDepth` was raised from 200 to 1000 but still insufficient for worst-case

### method_dispatch: 0.65x regression (was 0.83x before native BLR)
- Native BLR call adds type-check + DirectEntryPtr-load overhead even when falling to slow path
- method_dispatch calls many small functions (new_point, point_distance, etc.) per iteration
- Some calls are to GoFunctions (math.sqrt) which always fall to slow path
- The fast-path-miss overhead accumulates across millions of calls

### object_creation: 0.66x regression (was 0.88x before native BLR)
- Same root cause as method_dispatch: many small calls + NEWTABLE exits
- Each NEWTABLE still uses exit-resume (~55ns), dominates execution time

### matmul: 1.27x (regressed from 1.64x in R1)
- SETTABLE in inner loop grows array one element at a time → every write exits
- `syncFieldCache` overhead per exit-resume (partially mitigated by HasFieldOps)

### ackermann: 0.95x (native BLR not helping)
- All CALL instructions have C=0 (variable returns) or B=0 (variable args)
- B=0/C=0 native BLR support was added but may not be working correctly
- Needs investigation: dump bytecodes, verify native BLR path is taken

### mutual_recursion: 0.95x (same issue as ackermann)
- Functions call each other via GETGLOBAL + CALL
- Native BLR path may not be engaging for these calls

## Historical (Trace JIT — deprecated, for reference only)

The following issues are from the Trace JIT era (`internal/jit/`, now disconnected). Preserved for context.

### spectral_norm: float accumulator treated as int
- `findAccumulators()` detected `sum` as int accumulator, but `sum := 0.0` is float

### nbody: guard-fail from slot reuse type mismatch
- GETTABLE not recognized as a write by `isWrittenBeforeFirstReadExt`

### sort: stack overflow from recursive calls via call-exit
- Trace JIT's call-exit went through `vm.call`, deep recursion exceeded `maxCallDepth=200`

### GC scanTableRoots intermittent SIGSEGV
- GC compaction encountered stale NaN-boxed pointers in edge cases
- Partially fixed via safe-point mechanism
